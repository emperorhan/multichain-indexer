package coordinator

import (
	"strconv"
	"strings"
)

type AutoTuneConfig struct {
	Enabled bool

	MinBatchSize int
	MaxBatchSize int
	StepUp       int
	StepDown     int

	LagHighWatermark int64
	LagLowWatermark  int64

	QueueHighWatermarkPct int
	QueueLowWatermarkPct  int

	HysteresisTicks int
	CooldownTicks   int

	TelemetryStaleTicks    int
	TelemetryRecoveryTicks int

	OperatorOverrideBatchSize  int
	OperatorReleaseHoldTicks   int
	PolicyVersion              string
	PolicyManifestDigest       string
	PolicyManifestRefreshEpoch int64
	PolicyActivationHoldTicks  int
}

type autoTuneInputs struct {
	HasHeadSignal      bool
	HeadSequence       int64
	HasMinCursorSignal bool
	MinCursorSequence  int64
	QueueDepth         int
	QueueCapacity      int
}

type autoTuneDiagnostics struct {
	LagSequence            int64
	QueueDepth             int
	QueueCapacity          int
	TelemetryState         string
	OverrideState          string
	Signal                 string
	Decision               string
	BatchBefore            int
	BatchAfter             int
	Streak                 int
	Cooldown               int
	TelemetryStaleTicks    int
	TelemetryRecoveryTicks int
	OverrideReleaseTicks   int
	PolicyVersion          string
	PolicyManifestDigest   string
	PolicyEpoch            int64
	PolicyActivationTicks  int
}

type autoTuneSignal string

const (
	autoTuneSignalHold     autoTuneSignal = "hold"
	autoTuneSignalIncrease autoTuneSignal = "increase"
	autoTuneSignalDecrease autoTuneSignal = "decrease"
)

type autoTuneTelemetryState string

const (
	autoTuneTelemetryHealthy       autoTuneTelemetryState = "healthy"
	autoTuneTelemetryStaleFallback autoTuneTelemetryState = "stale_fallback"
	autoTuneTelemetryRecoveryHold  autoTuneTelemetryState = "recovery_hold"
)

type autoTuneOverrideState string

const (
	autoTuneOverrideAuto        autoTuneOverrideState = "auto"
	autoTuneOverrideManualHold  autoTuneOverrideState = "manual_hold"
	autoTuneOverrideReleaseHold autoTuneOverrideState = "release_hold"
)

type autoTuneOverrideTransition struct {
	WasManualOverride    bool
	ReleaseHoldRemaining int
}

type autoTunePolicyTransition struct {
	HasState                bool
	Version                 string
	ManifestDigest          string
	Epoch                   int64
	ActivationHoldRemaining int
}

type autoTuneController struct {
	minBatchSize int
	maxBatchSize int
	stepUp       int
	stepDown     int

	lagHighWatermark int64
	lagLowWatermark  int64

	queueHighWatermarkPct int
	queueLowWatermarkPct  int

	hysteresisTicks        int
	cooldownTicks          int
	cooldownLeft           int
	telemetryStaleTicks    int
	telemetryRecoveryTicks int
	operatorOverrideBatch  int
	operatorReleaseHold    int
	overrideReleaseLeft    int
	policyVersion          string
	policyManifestDigest   string
	policyActivationHold   int
	policyActivationLeft   int
	policyEpoch            int64
	telemetryFallback      bool
	telemetryArmed         bool
	telemetryStaleObserved int
	telemetryRecoverySeen  int
	currentBatch           int
	lastSignal             autoTuneSignal
	lastApplied            autoTuneSignal
	saturationSignal       autoTuneSignal
	streak                 int
}

const defaultAutoTunePolicyVersion = "policy-v1"
const defaultAutoTunePolicyManifestDigest = "manifest-v1"

func newAutoTuneController(baseBatchSize int, cfg AutoTuneConfig) *autoTuneController {
	return newAutoTuneControllerWithSeed(baseBatchSize, cfg, nil)
}

func newAutoTuneControllerWithSeed(baseBatchSize int, cfg AutoTuneConfig, seedBatch *int) *autoTuneController {
	return newAutoTuneControllerWithSeedMode(baseBatchSize, cfg, seedBatch, false)
}

func newAutoTuneControllerWithRestartSeed(baseBatchSize int, cfg AutoTuneConfig, seedBatch *int) *autoTuneController {
	return newAutoTuneControllerWithSeedMode(baseBatchSize, cfg, seedBatch, true)
}

func newAutoTuneControllerWithSeedMode(
	baseBatchSize int,
	cfg AutoTuneConfig,
	seedBatch *int,
	boundedWarmStart bool,
) *autoTuneController {
	if !cfg.Enabled {
		return nil
	}

	minBatch := cfg.MinBatchSize
	if minBatch <= 0 {
		minBatch = 1
	}

	maxBatch := cfg.MaxBatchSize
	if maxBatch <= 0 {
		maxBatch = baseBatchSize
	}
	if maxBatch < minBatch {
		maxBatch = minBatch
	}

	stepUp := cfg.StepUp
	if stepUp <= 0 {
		stepUp = 1
	}
	stepDown := cfg.StepDown
	if stepDown <= 0 {
		stepDown = 1
	}

	lagHigh := cfg.LagHighWatermark
	if lagHigh < 0 {
		lagHigh = 0
	}
	lagLow := cfg.LagLowWatermark
	if lagLow < 0 {
		lagLow = 0
	}
	if lagLow > lagHigh {
		lagLow = lagHigh
	}

	queueHigh := clampInt(cfg.QueueHighWatermarkPct, 1, 100)
	if queueHigh == 0 {
		queueHigh = 80
	}
	queueLow := clampInt(cfg.QueueLowWatermarkPct, 0, queueHigh)
	if cfg.QueueLowWatermarkPct == 0 {
		queueLow = minInt(30, queueHigh)
	}

	hysteresisTicks := cfg.HysteresisTicks
	if hysteresisTicks <= 0 {
		hysteresisTicks = 2
	}
	cooldownTicks := cfg.CooldownTicks
	if cooldownTicks < 0 {
		cooldownTicks = 0
	}
	if cooldownTicks == 0 {
		cooldownTicks = hysteresisTicks
	}
	telemetryStaleTicks := cfg.TelemetryStaleTicks
	if telemetryStaleTicks <= 0 {
		telemetryStaleTicks = 2
	}
	telemetryRecoveryTicks := cfg.TelemetryRecoveryTicks
	if telemetryRecoveryTicks <= 0 {
		telemetryRecoveryTicks = 1
	}
	operatorReleaseHold := cfg.OperatorReleaseHoldTicks
	if operatorReleaseHold < 0 {
		operatorReleaseHold = 0
	}
	policyActivationHold := cfg.PolicyActivationHoldTicks
	if policyActivationHold < 0 {
		policyActivationHold = 0
	}
	policyVersion := normalizePolicyVersion(cfg.PolicyVersion)
	policyManifestDigest := normalizePolicyManifestDigest(cfg.PolicyManifestDigest)
	policyManifestEpoch := cfg.PolicyManifestRefreshEpoch
	if policyManifestEpoch < 0 {
		policyManifestEpoch = 0
	}
	operatorOverrideBatch := 0
	if cfg.OperatorOverrideBatchSize > 0 {
		operatorOverrideBatch = clampInt(cfg.OperatorOverrideBatchSize, minBatch, maxBatch)
	}

	baseStartBatch := clampInt(baseBatchSize, minBatch, maxBatch)
	if baseStartBatch <= 0 {
		baseStartBatch = minBatch
	}

	startBatchCandidate := baseStartBatch
	if seedBatch != nil && *seedBatch > 0 {
		seedBatchCandidate := clampInt(*seedBatch, minBatch, maxBatch)
		if boundedWarmStart {
			startBatchCandidate = boundedWarmStartBatch(
				baseStartBatch,
				seedBatchCandidate,
				stepUp,
				stepDown,
				minBatch,
				maxBatch,
			)
		} else {
			startBatchCandidate = seedBatchCandidate
		}
	}
	startBatch := clampInt(startBatchCandidate, minBatch, maxBatch)
	if startBatch <= 0 {
		startBatch = minBatch
	}
	if operatorOverrideBatch > 0 {
		startBatch = operatorOverrideBatch
	}

	return &autoTuneController{
		minBatchSize:           minBatch,
		maxBatchSize:           maxBatch,
		stepUp:                 stepUp,
		stepDown:               stepDown,
		lagHighWatermark:       lagHigh,
		lagLowWatermark:        lagLow,
		queueHighWatermarkPct:  queueHigh,
		queueLowWatermarkPct:   queueLow,
		hysteresisTicks:        hysteresisTicks,
		cooldownTicks:          cooldownTicks,
		telemetryStaleTicks:    telemetryStaleTicks,
		telemetryRecoveryTicks: telemetryRecoveryTicks,
		operatorOverrideBatch:  operatorOverrideBatch,
		operatorReleaseHold:    operatorReleaseHold,
		policyVersion:          policyVersion,
		policyManifestDigest:   policyManifestDigest,
		policyActivationHold:   policyActivationHold,
		policyEpoch:            policyManifestEpoch,
		currentBatch:           startBatch,
		lastSignal:             autoTuneSignalHold,
		lastApplied:            autoTuneSignalHold,
		saturationSignal:       autoTuneSignalHold,
	}
}

func (a *autoTuneController) Resolve(inputs autoTuneInputs) (int, autoTuneDiagnostics) {
	telemetryState := a.resolveTelemetryState(inputs)
	lagSequence := int64(0)
	if inputs.HasHeadSignal && inputs.HasMinCursorSignal {
		lagSequence = inputs.HeadSequence - inputs.MinCursorSequence
		if lagSequence < 0 {
			lagSequence = 0
		}
	}

	overrideState := a.resolveOverrideState()
	before := a.currentBatch
	if overrideState == autoTuneOverrideManualHold {
		a.currentBatch = clampInt(a.operatorOverrideBatch, a.minBatchSize, a.maxBatchSize)
		a.resetAdaptiveControlState()
		return a.currentBatch, autoTuneDiagnostics{
			LagSequence:            lagSequence,
			QueueDepth:             inputs.QueueDepth,
			QueueCapacity:          inputs.QueueCapacity,
			TelemetryState:         string(telemetryState),
			OverrideState:          string(overrideState),
			Signal:                 string(autoTuneSignalHold),
			Decision:               "hold_operator_override",
			BatchBefore:            before,
			BatchAfter:             a.currentBatch,
			Streak:                 a.streak,
			Cooldown:               a.cooldownLeft,
			TelemetryStaleTicks:    a.telemetryStaleObserved,
			TelemetryRecoveryTicks: a.telemetryRecoverySeen,
			OverrideReleaseTicks:   a.overrideReleaseLeft,
			PolicyVersion:          a.policyVersion,
			PolicyManifestDigest:   a.policyManifestDigest,
			PolicyEpoch:            a.policyEpoch,
			PolicyActivationTicks:  a.policyActivationLeft,
		}
	}
	if overrideState == autoTuneOverrideReleaseHold {
		remainingBefore := a.overrideReleaseLeft
		a.resetAdaptiveControlState()
		if a.overrideReleaseLeft > 0 {
			a.overrideReleaseLeft--
		}
		return a.currentBatch, autoTuneDiagnostics{
			LagSequence:            lagSequence,
			QueueDepth:             inputs.QueueDepth,
			QueueCapacity:          inputs.QueueCapacity,
			TelemetryState:         string(telemetryState),
			OverrideState:          string(overrideState),
			Signal:                 string(autoTuneSignalHold),
			Decision:               "hold_operator_release",
			BatchBefore:            before,
			BatchAfter:             a.currentBatch,
			Streak:                 a.streak,
			Cooldown:               a.cooldownLeft,
			TelemetryStaleTicks:    a.telemetryStaleObserved,
			TelemetryRecoveryTicks: a.telemetryRecoverySeen,
			OverrideReleaseTicks:   remainingBefore,
			PolicyVersion:          a.policyVersion,
			PolicyManifestDigest:   a.policyManifestDigest,
			PolicyEpoch:            a.policyEpoch,
			PolicyActivationTicks:  a.policyActivationLeft,
		}
	}
	if a.policyActivationLeft > 0 {
		remainingBefore := a.policyActivationLeft
		a.resetAdaptiveControlState()
		a.policyActivationLeft--
		return a.currentBatch, autoTuneDiagnostics{
			LagSequence:            lagSequence,
			QueueDepth:             inputs.QueueDepth,
			QueueCapacity:          inputs.QueueCapacity,
			TelemetryState:         string(telemetryState),
			OverrideState:          string(overrideState),
			Signal:                 string(autoTuneSignalHold),
			Decision:               "hold_policy_transition",
			BatchBefore:            before,
			BatchAfter:             a.currentBatch,
			Streak:                 a.streak,
			Cooldown:               a.cooldownLeft,
			TelemetryStaleTicks:    a.telemetryStaleObserved,
			TelemetryRecoveryTicks: a.telemetryRecoverySeen,
			OverrideReleaseTicks:   a.overrideReleaseLeft,
			PolicyVersion:          a.policyVersion,
			PolicyManifestDigest:   a.policyManifestDigest,
			PolicyEpoch:            a.policyEpoch,
			PolicyActivationTicks:  remainingBefore,
		}
	}

	signal := autoTuneSignalHold
	if telemetryState == autoTuneTelemetryHealthy {
		signal = a.classifySignal(inputs, lagSequence)
	}
	decision := "hold"
	appliedControl := false
	blockedByCooldown := a.cooldownLeft > 0 && isOppositeSignal(signal, a.lastApplied)
	if signal != a.saturationSignal || !a.isSaturatedBoundary(signal) {
		a.saturationSignal = autoTuneSignalHold
	}

	if blockedByCooldown {
		decision = "defer_cooldown"
		// Preserve opposite-pressure continuity while cooldown is active so
		// recovery after cooldown remains deterministic at the hysteresis boundary.
		if a.lastSignal == signal {
			a.streak++
		} else {
			a.lastSignal = signal
			a.streak = 1
		}
	} else {
		switch signal {
		case autoTuneSignalHold:
			a.lastSignal = autoTuneSignalHold
			a.saturationSignal = autoTuneSignalHold
			a.streak = 0
		case autoTuneSignalIncrease, autoTuneSignalDecrease:
			if a.lastSignal == signal {
				a.streak++
			} else {
				a.lastSignal = signal
				a.streak = 1
			}

			if a.saturationSignal == signal && a.isSaturatedBoundary(signal) {
				decision = clampedDecisionForSignal(signal)
				a.streak = 0
			} else if a.streak >= a.hysteresisTicks {
				batchChanged := false
				switch signal {
				case autoTuneSignalIncrease:
					next := clampInt(a.currentBatch+a.stepUp, a.minBatchSize, a.maxBatchSize)
					if next > a.currentBatch {
						decision = "apply_increase"
						batchChanged = true
					} else {
						decision = "clamped_increase"
					}
					a.currentBatch = next
				case autoTuneSignalDecrease:
					next := clampInt(a.currentBatch-a.stepDown, a.minBatchSize, a.maxBatchSize)
					if next < a.currentBatch {
						decision = "apply_decrease"
						batchChanged = true
					} else {
						decision = "clamped_decrease"
					}
					a.currentBatch = next
				}
				a.streak = 0
				if batchChanged {
					a.lastApplied = signal
					a.cooldownLeft = a.cooldownTicks
					appliedControl = true
					if a.isSaturatedBoundary(signal) {
						a.saturationSignal = signal
					} else {
						a.saturationSignal = autoTuneSignalHold
					}
				} else {
					a.saturationSignal = signal
				}
			} else {
				decision = "defer_hysteresis"
			}
		}
	}

	if !appliedControl && a.cooldownLeft > 0 {
		a.cooldownLeft--
	}
	if decision == "hold" {
		switch telemetryState {
		case autoTuneTelemetryStaleFallback:
			decision = "hold_telemetry_stale"
		case autoTuneTelemetryRecoveryHold:
			decision = "hold_telemetry_recovery"
		}
	}

	return a.currentBatch, autoTuneDiagnostics{
		LagSequence:            lagSequence,
		QueueDepth:             inputs.QueueDepth,
		QueueCapacity:          inputs.QueueCapacity,
		TelemetryState:         string(telemetryState),
		OverrideState:          string(overrideState),
		Signal:                 string(signal),
		Decision:               decision,
		BatchBefore:            before,
		BatchAfter:             a.currentBatch,
		Streak:                 a.streak,
		Cooldown:               a.cooldownLeft,
		TelemetryStaleTicks:    a.telemetryStaleObserved,
		TelemetryRecoveryTicks: a.telemetryRecoverySeen,
		OverrideReleaseTicks:   a.overrideReleaseLeft,
		PolicyVersion:          a.policyVersion,
		PolicyManifestDigest:   a.policyManifestDigest,
		PolicyEpoch:            a.policyEpoch,
		PolicyActivationTicks:  a.policyActivationLeft,
	}
}

func (a *autoTuneController) exportOverrideTransition() autoTuneOverrideTransition {
	return autoTuneOverrideTransition{
		WasManualOverride:    a.operatorOverrideBatch > 0,
		ReleaseHoldRemaining: a.overrideReleaseLeft,
	}
}

func (a *autoTuneController) exportPolicyTransition() autoTunePolicyTransition {
	return autoTunePolicyTransition{
		HasState:                true,
		Version:                 a.policyVersion,
		ManifestDigest:          a.policyManifestDigest,
		Epoch:                   a.policyEpoch,
		ActivationHoldRemaining: a.policyActivationLeft,
	}
}

func (a *autoTuneController) reconcileOverrideTransition(transition autoTuneOverrideTransition) {
	if a.operatorOverrideBatch > 0 {
		a.currentBatch = clampInt(a.operatorOverrideBatch, a.minBatchSize, a.maxBatchSize)
		a.overrideReleaseLeft = 0
		a.resetAdaptiveControlState()
		return
	}
	if transition.ReleaseHoldRemaining > 0 {
		a.overrideReleaseLeft = transition.ReleaseHoldRemaining
		a.resetAdaptiveControlState()
		return
	}
	if transition.WasManualOverride && a.operatorReleaseHold > 0 {
		a.overrideReleaseLeft = a.operatorReleaseHold
		a.resetAdaptiveControlState()
	}
}

func (a *autoTuneController) reconcilePolicyTransition(transition autoTunePolicyTransition) {
	if !transition.HasState {
		return
	}
	previousVersion := normalizePolicyVersion(transition.Version)
	previousDigest := normalizePolicyManifestDigest(transition.ManifestDigest)
	previousEpoch := transition.Epoch
	if previousEpoch < 0 {
		previousEpoch = 0
	}

	incomingVersion := a.policyVersion
	incomingDigest := normalizePolicyManifestDigest(a.policyManifestDigest)
	incomingEpoch := a.policyEpoch
	if incomingEpoch < 0 {
		incomingEpoch = 0
	}

	if incomingVersion == previousVersion && incomingDigest == previousDigest {
		if incomingEpoch == previousEpoch+1 {
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			a.policyActivationLeft = a.policyActivationHold
			a.resetAdaptiveControlState()
		} else {
			// Reject duplicate/stale/sequence-gap transitions for identical digest lineage.
			a.policyManifestDigest = previousDigest
			a.policyEpoch = previousEpoch
		}
		if transition.ActivationHoldRemaining > a.policyActivationLeft {
			a.policyActivationLeft = transition.ActivationHoldRemaining
			a.resetAdaptiveControlState()
		}
		return
	}

	if incomingVersion != previousVersion {
		nextEpoch := maxInt64(previousEpoch+1, incomingEpoch)
		a.policyManifestDigest = incomingDigest
		a.policyEpoch = nextEpoch
		a.policyActivationLeft = a.policyActivationHold
		a.resetAdaptiveControlState()
		return
	}

	if incomingEpoch > previousEpoch {
		if incomingEpoch == previousEpoch+1 {
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			a.policyActivationLeft = a.policyActivationHold
			a.resetAdaptiveControlState()
			return
		}
		if isDeterministicSnapshotCutover(previousEpoch, incomingEpoch, incomingDigest) {
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			a.policyActivationLeft = a.policyActivationHold
			a.resetAdaptiveControlState()
			return
		}
		// Reject sequence-gap transitions: pin previously verified contiguous lineage.
		a.policyManifestDigest = previousDigest
		a.policyEpoch = previousEpoch
		if transition.ActivationHoldRemaining > a.policyActivationLeft {
			a.policyActivationLeft = transition.ActivationHoldRemaining
			a.resetAdaptiveControlState()
		}
		return
	}

	// Reject stale/ambiguous refresh: pin previously verified manifest lineage.
	a.policyManifestDigest = previousDigest
	a.policyEpoch = previousEpoch
	if transition.ActivationHoldRemaining > a.policyActivationLeft {
		a.policyActivationLeft = transition.ActivationHoldRemaining
		a.resetAdaptiveControlState()
	}
}

func (a *autoTuneController) resolveOverrideState() autoTuneOverrideState {
	if a.operatorOverrideBatch > 0 {
		return autoTuneOverrideManualHold
	}
	if a.overrideReleaseLeft > 0 {
		return autoTuneOverrideReleaseHold
	}
	return autoTuneOverrideAuto
}

func (a *autoTuneController) resetAdaptiveControlState() {
	a.lastSignal = autoTuneSignalHold
	a.lastApplied = autoTuneSignalHold
	a.saturationSignal = autoTuneSignalHold
	a.streak = 0
	a.cooldownLeft = 0
}

func (a *autoTuneController) resolveTelemetryState(inputs autoTuneInputs) autoTuneTelemetryState {
	hasLagTelemetry := inputs.HasHeadSignal && inputs.HasMinCursorSignal
	if hasLagTelemetry {
		a.telemetryArmed = true
	}
	if !a.telemetryArmed {
		return autoTuneTelemetryHealthy
	}

	telemetryInvalid := !hasLagTelemetry || inputs.HeadSequence < inputs.MinCursorSequence
	if telemetryInvalid {
		a.telemetryStaleObserved++
		a.telemetryRecoverySeen = 0
		if !a.telemetryFallback && a.telemetryStaleObserved >= a.telemetryStaleTicks {
			a.telemetryFallback = true
		}
		if a.telemetryFallback {
			return autoTuneTelemetryStaleFallback
		}
		return autoTuneTelemetryHealthy
	}

	a.telemetryStaleObserved = 0
	if !a.telemetryFallback {
		a.telemetryRecoverySeen = 0
		return autoTuneTelemetryHealthy
	}

	a.telemetryRecoverySeen++
	state := autoTuneTelemetryRecoveryHold
	if a.telemetryRecoverySeen >= a.telemetryRecoveryTicks {
		a.telemetryFallback = false
		a.telemetryRecoverySeen = 0
	}
	return state
}

func (a *autoTuneController) isSaturatedBoundary(signal autoTuneSignal) bool {
	switch signal {
	case autoTuneSignalIncrease:
		return a.currentBatch >= a.maxBatchSize
	case autoTuneSignalDecrease:
		return a.currentBatch <= a.minBatchSize
	default:
		return false
	}
}

func clampedDecisionForSignal(signal autoTuneSignal) string {
	switch signal {
	case autoTuneSignalIncrease:
		return "clamped_increase"
	case autoTuneSignalDecrease:
		return "clamped_decrease"
	default:
		return "hold"
	}
}

func isOppositeSignal(signal autoTuneSignal, lastApplied autoTuneSignal) bool {
	switch signal {
	case autoTuneSignalIncrease:
		return lastApplied == autoTuneSignalDecrease
	case autoTuneSignalDecrease:
		return lastApplied == autoTuneSignalIncrease
	default:
		return false
	}
}

func (a *autoTuneController) classifySignal(inputs autoTuneInputs, lagSequence int64) autoTuneSignal {
	if isQueueHigh(inputs.QueueDepth, inputs.QueueCapacity, a.queueHighWatermarkPct) {
		return autoTuneSignalDecrease
	}
	if inputs.HasHeadSignal && inputs.HasMinCursorSignal && lagSequence >= a.lagHighWatermark {
		return autoTuneSignalIncrease
	}
	if inputs.HasHeadSignal &&
		inputs.HasMinCursorSignal &&
		lagSequence <= a.lagLowWatermark &&
		isQueueLow(inputs.QueueDepth, inputs.QueueCapacity, a.queueLowWatermarkPct) {
		return autoTuneSignalDecrease
	}
	return autoTuneSignalHold
}

func isQueueHigh(depth, capacity, pct int) bool {
	if capacity <= 0 || depth < 0 || pct <= 0 {
		return false
	}
	return depth*100 >= pct*capacity
}

func isQueueLow(depth, capacity, pct int) bool {
	if capacity <= 0 || depth < 0 {
		return false
	}
	return depth*100 <= pct*capacity
}

func clampInt(v, minV, maxV int) int {
	if maxV < minV {
		return minV
	}
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func boundedWarmStartBatch(baseBatch, seedBatch, stepUp, stepDown, minBatch, maxBatch int) int {
	base := clampInt(baseBatch, minBatch, maxBatch)
	seed := clampInt(seedBatch, minBatch, maxBatch)
	if seed == base {
		return base
	}

	if seed > base {
		delta := seed - base
		limit := maxInt(stepUp, 1)
		if delta > limit {
			delta = limit
		}
		return clampInt(base+delta, minBatch, maxBatch)
	}

	delta := base - seed
	limit := maxInt(stepDown, 1)
	if delta > limit {
		delta = limit
	}
	return clampInt(base-delta, minBatch, maxBatch)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func normalizePolicyVersion(version string) string {
	trimmed := strings.TrimSpace(strings.ToLower(version))
	if trimmed == "" {
		return defaultAutoTunePolicyVersion
	}
	return trimmed
}

func normalizePolicyManifestDigest(digest string) string {
	trimmed := strings.TrimSpace(strings.ToLower(digest))
	if trimmed == "" {
		return defaultAutoTunePolicyManifestDigest
	}
	return trimmed
}

func isDeterministicSnapshotCutover(previousEpoch, incomingEpoch int64, incomingDigest string) bool {
	if incomingEpoch <= previousEpoch+1 {
		return false
	}
	baseSeq, tailSeq, ok := parseSnapshotCutoverLineage(incomingDigest)
	if !ok {
		return false
	}
	if baseSeq != previousEpoch {
		return false
	}
	if tailSeq != incomingEpoch {
		return false
	}
	return tailSeq > baseSeq
}

func parseSnapshotCutoverLineage(digest string) (int64, int64, bool) {
	const baseKey = "snapshot-base-seq="
	const tailKey = "snapshot-tail-seq="

	var (
		baseValue string
		tailValue string
	)
	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		switch {
		case strings.HasPrefix(token, baseKey):
			baseValue = strings.TrimSpace(strings.TrimPrefix(token, baseKey))
		case strings.HasPrefix(token, tailKey):
			tailValue = strings.TrimSpace(strings.TrimPrefix(token, tailKey))
		}
	}
	if baseValue == "" || tailValue == "" {
		return 0, 0, false
	}
	baseSeq, err := strconv.ParseInt(baseValue, 10, 64)
	if err != nil || baseSeq < 0 {
		return 0, 0, false
	}
	tailSeq, err := strconv.ParseInt(tailValue, 10, 64)
	if err != nil || tailSeq < 0 {
		return 0, 0, false
	}
	return baseSeq, tailSeq, true
}
