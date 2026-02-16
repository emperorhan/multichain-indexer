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
	FromWarmCheckpoint      bool
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
	normalizedActivationHold := normalizeTransitionActivationHold(transition)
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
		if normalizedActivationHold > a.policyActivationLeft {
			a.policyActivationLeft = normalizedActivationHold
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
		if isDeterministicRollbackFencePostReleaseWindowEpochRolloverStaleTransition(
			previousEpoch,
			incomingEpoch,
			previousDigest,
			incomingDigest,
		) {
			// Reject delayed prior-epoch rollback-fence ownership during
			// post-release-window epoch rollover; keep verified ownership until
			// current-epoch lineage is observed.
			a.policyManifestDigest = previousDigest
			a.policyEpoch = previousEpoch
			if normalizedActivationHold > a.policyActivationLeft {
				a.policyActivationLeft = normalizedActivationHold
				a.resetAdaptiveControlState()
			}
			return
		}
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
		if normalizedActivationHold > a.policyActivationLeft {
			a.policyActivationLeft = normalizedActivationHold
			a.resetAdaptiveControlState()
		}
		return
	}

	if incomingEpoch < previousEpoch {
		if isDeterministicRollbackLineage(previousEpoch, incomingEpoch, incomingDigest) {
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			a.policyActivationLeft = a.policyActivationHold
			a.resetAdaptiveControlState()
			return
		}
		// Reject stale/ambiguous rollback: pin previously verified rollback-safe lineage.
		a.policyManifestDigest = previousDigest
		a.policyEpoch = previousEpoch
		if normalizedActivationHold > a.policyActivationLeft {
			a.policyActivationLeft = normalizedActivationHold
			a.resetAdaptiveControlState()
		}
		return
	}

	if incomingEpoch == previousEpoch {
		if isDeterministicRollbackFenceEpochCompactionTransition(previousEpoch, previousDigest, incomingDigest) {
			// Same-epoch rollback fence compaction is metadata ownership only and
			// must not reopen policy holds from pre-compaction checkpoints.
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			return
		}
		if isDeterministicRollbackFenceTombstoneExpiryTransition(previousEpoch, previousDigest, incomingDigest) {
			// Same-epoch tombstone expiry is metadata ownership only and must
			// converge deterministically from retained tombstone to expired state.
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			return
		}
		if isDeterministicRollbackFencePostExpiryLateMarkerQuarantineTransition(previousEpoch, previousDigest, incomingDigest) {
			// Same-epoch post-expiry late-marker quarantine is metadata ownership
			// only and must converge deterministically at marker-hold boundaries.
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			return
		}
		if isDeterministicRollbackFencePostExpiryLateMarkerReleaseTransition(previousEpoch, previousDigest, incomingDigest) {
			// Same-epoch post-expiry late-marker release is metadata ownership only
			// and must converge deterministically at quarantine-release boundaries.
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			return
		}
		if isDeterministicRollbackFencePostExpiryLateMarkerReleaseWindowTransition(previousEpoch, previousDigest, incomingDigest) {
			// Same-epoch post-quarantine release-window progression is metadata
			// ownership only and must converge deterministically at release
			// watermark boundaries.
			a.policyManifestDigest = incomingDigest
			a.policyEpoch = incomingEpoch
			return
		}
		if isDeterministicRollbackFencePostExpiryLateMarkerReleaseWindowTransition(previousEpoch, incomingDigest, previousDigest) {
			// Reject stale/duplicate post-quarantine release-window ownership once
			// a newer release watermark is verified for this lineage.
			a.policyManifestDigest = previousDigest
			a.policyEpoch = previousEpoch
			return
		}
		if isDeterministicRollbackFencePostExpiryLateMarkerReleaseTransition(previousEpoch, incomingDigest, previousDigest) {
			// Reject stale post-release quarantine ownership reactivation once
			// quarantine-release ownership is verified for this lineage.
			a.policyManifestDigest = previousDigest
			a.policyEpoch = previousEpoch
			return
		}
		if isDeterministicRollbackFencePostExpiryLateMarkerQuarantineTransition(previousEpoch, incomingDigest, previousDigest) {
			// Reject stale post-quarantine expiry ownership reactivation once
			// late-marker quarantine ownership is verified for this lineage.
			a.policyManifestDigest = previousDigest
			a.policyEpoch = previousEpoch
			return
		}
		if isDeterministicRollbackFenceTombstoneExpiryTransition(previousEpoch, incomingDigest, previousDigest) {
			// Reject stale post-expiry ownership reactivation once expiry ownership
			// is verified for this rollback fence lineage.
			a.policyManifestDigest = previousDigest
			a.policyEpoch = previousEpoch
			return
		}
		if isDeterministicRollbackFenceEpochCompactionTransition(previousEpoch, incomingDigest, previousDigest) {
			// Reject stale pre-compaction rollback state once compacted ownership is verified.
			a.policyManifestDigest = previousDigest
			a.policyEpoch = previousEpoch
			return
		}
	}

	// Reject stale/ambiguous refresh: pin previously verified manifest lineage.
	a.policyManifestDigest = previousDigest
	a.policyEpoch = previousEpoch
	if normalizedActivationHold > a.policyActivationLeft {
		a.policyActivationLeft = normalizedActivationHold
		a.resetAdaptiveControlState()
	}
}

func normalizeTransitionActivationHold(transition autoTunePolicyTransition) int {
	remaining := maxInt(transition.ActivationHoldRemaining, 0)
	if remaining == 0 || !transition.FromWarmCheckpoint {
		return remaining
	}
	if isRollbackFenceEpochCompactionDigest(transition.Epoch, transition.ManifestDigest) {
		// Compaction restores can replay pre-compaction hold payloads; collapse to
		// deterministic no-hold because compaction is same-epoch metadata ownership.
		return 0
	}
	if !isRollbackFenceTransition(transition.Epoch, transition.ManifestDigest) {
		return remaining
	}
	// Warm-restore snapshots can race with rollback fence flush timing and
	// persist either pre- or post-flush hold counts. Collapse to one
	// deterministic restore hold tick to avoid replay drift.
	if remaining > 1 {
		return 1
	}
	return remaining
}

func isRollbackFenceTransition(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	rollbackFromSeq, rollbackToSeq, _, ok := parseRollbackLineage(normalizePolicyManifestDigest(digest))
	if !ok {
		return false
	}
	if rollbackToSeq != epoch {
		return false
	}
	return rollbackFromSeq > rollbackToSeq
}

func isRollbackFenceEpochCompactionDigest(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
	if !hasRollbackFenceEpochCompactionTombstone(normalized) {
		return false
	}
	rollbackFromSeq, rollbackToSeq, rollbackForwardSeq, ok := parseRollbackLineage(normalized)
	if !ok {
		return false
	}
	if rollbackToSeq != epoch {
		return false
	}
	if rollbackFromSeq <= rollbackToSeq {
		return false
	}
	if rollbackForwardSeq < rollbackFromSeq {
		return false
	}
	return true
}

func isDeterministicRollbackFenceEpochCompactionTransition(
	epoch int64,
	sourceDigest string,
	targetDigest string,
) bool {
	if epoch < 0 {
		return false
	}
	sourceNormalized := normalizePolicyManifestDigest(sourceDigest)
	targetNormalized := normalizePolicyManifestDigest(targetDigest)
	if !hasRollbackFenceEpochCompactionTombstone(targetNormalized) {
		return false
	}
	sourceFromSeq, sourceToSeq, sourceForwardSeq, ok := parseRollbackLineage(sourceNormalized)
	if !ok {
		return false
	}
	targetFromSeq, targetToSeq, targetForwardSeq, ok := parseRollbackLineage(targetNormalized)
	if !ok {
		return false
	}
	if sourceFromSeq != targetFromSeq || sourceToSeq != targetToSeq || sourceForwardSeq != targetForwardSeq {
		return false
	}
	if sourceToSeq != epoch || targetToSeq != epoch {
		return false
	}
	if sourceFromSeq <= sourceToSeq || targetFromSeq <= targetToSeq {
		return false
	}
	if sourceForwardSeq < sourceFromSeq || targetForwardSeq < targetFromSeq {
		return false
	}
	return true
}

func hasRollbackFenceEpochCompactionTombstone(digest string) bool {
	const (
		tombstoneKey      = "rollback-fence-tombstone"
		tombstoneValueKey = tombstoneKey + "="
	)
	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		switch {
		case token == tombstoneKey:
			return true
		case strings.HasPrefix(token, tombstoneValueKey):
			value := strings.TrimSpace(strings.TrimPrefix(token, tombstoneValueKey))
			switch value {
			case "1", "true", "yes", "on":
				return true
			}
		}
	}
	return false
}

func parseRollbackFenceTombstoneExpiryEpoch(digest string) (int64, bool) {
	const expiryKey = "rollback-fence-tombstone-expiry-epoch="

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		if !strings.HasPrefix(token, expiryKey) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(token, expiryKey))
		if value == "" {
			return 0, false
		}
		expiryEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || expiryEpoch < 0 {
			return 0, false
		}
		return expiryEpoch, true
	}

	return 0, false
}

func parseRollbackFenceLateMarkerHoldEpoch(digest string) (int64, bool) {
	const holdKey = "rollback-fence-late-marker-hold-epoch="

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		if !strings.HasPrefix(token, holdKey) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(token, holdKey))
		if value == "" {
			return 0, false
		}
		holdEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || holdEpoch < 0 {
			return 0, false
		}
		return holdEpoch, true
	}

	return 0, false
}

func parseRollbackFenceLateMarkerReleaseEpoch(digest string) (int64, bool) {
	const releaseKey = "rollback-fence-late-marker-release-epoch="

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		if !strings.HasPrefix(token, releaseKey) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(token, releaseKey))
		if value == "" {
			return 0, false
		}
		releaseEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || releaseEpoch < 0 {
			return 0, false
		}
		return releaseEpoch, true
	}

	return 0, false
}

func parseRollbackFenceLateBridgeSequence(digest string) (int64, bool) {
	const (
		sequenceKeyHyphen = "rollback-fence-late-bridge-sequence="
		sequenceKeyShort  = "rollback-fence-late-bridge-seq="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, sequenceKeyHyphen):
			value = strings.TrimSpace(strings.TrimPrefix(token, sequenceKeyHyphen))
		case strings.HasPrefix(token, sequenceKeyShort):
			value = strings.TrimSpace(strings.TrimPrefix(token, sequenceKeyShort))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		sequence, err := strconv.ParseInt(value, 10, 64)
		if err != nil || sequence < 0 {
			return 0, false
		}
		return sequence, true
	}

	return 0, false
}

func parseRollbackFenceLateBridgeReleaseWatermark(digest string) (int64, bool) {
	const (
		watermarkKeyHyphen  = "rollback-fence-late-bridge-release-watermark="
		watermarkKeyGeneric = "rollback-fence-release-watermark="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, watermarkKeyHyphen):
			value = strings.TrimSpace(strings.TrimPrefix(token, watermarkKeyHyphen))
		case strings.HasPrefix(token, watermarkKeyGeneric):
			value = strings.TrimSpace(strings.TrimPrefix(token, watermarkKeyGeneric))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		watermark, err := strconv.ParseInt(value, 10, 64)
		if err != nil || watermark < 0 {
			return 0, false
		}
		return watermark, true
	}

	return 0, false
}

func parseRollbackFenceLateBridgeDrainWatermark(digest string) (int64, bool) {
	const (
		drainWatermarkKeyLateBridge = "rollback-fence-late-bridge-drain-watermark="
		drainWatermarkKeyBacklog    = "rollback-fence-backlog-drain-watermark="
		drainWatermarkKeyGeneric    = "rollback-fence-drain-watermark="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, drainWatermarkKeyLateBridge):
			value = strings.TrimSpace(strings.TrimPrefix(token, drainWatermarkKeyLateBridge))
		case strings.HasPrefix(token, drainWatermarkKeyBacklog):
			value = strings.TrimSpace(strings.TrimPrefix(token, drainWatermarkKeyBacklog))
		case strings.HasPrefix(token, drainWatermarkKeyGeneric):
			value = strings.TrimSpace(strings.TrimPrefix(token, drainWatermarkKeyGeneric))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		watermark, err := strconv.ParseInt(value, 10, 64)
		if err != nil || watermark < 0 {
			return 0, false
		}
		return watermark, true
	}

	return 0, false
}

func parseRollbackFenceLiveHeadWatermark(digest string) (int64, bool) {
	const (
		liveHeadKeyHyphen  = "rollback-fence-live-head="
		liveHeadKeyCatchup = "rollback-fence-live-catchup-head="
		liveHeadKeyGeneric = "rollback-fence-live-watermark="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, liveHeadKeyHyphen):
			value = strings.TrimSpace(strings.TrimPrefix(token, liveHeadKeyHyphen))
		case strings.HasPrefix(token, liveHeadKeyCatchup):
			value = strings.TrimSpace(strings.TrimPrefix(token, liveHeadKeyCatchup))
		case strings.HasPrefix(token, liveHeadKeyGeneric):
			value = strings.TrimSpace(strings.TrimPrefix(token, liveHeadKeyGeneric))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		liveHead, err := strconv.ParseInt(value, 10, 64)
		if err != nil || liveHead < 0 {
			return 0, false
		}
		return liveHead, true
	}

	return 0, false
}

func parseRollbackFenceSteadyStateWatermark(digest string) (int64, bool) {
	const (
		steadyStateKeyHyphen   = "rollback-fence-steady-state-watermark="
		steadyStateKeyShort    = "rollback-fence-steady-watermark="
		rebaselineWatermarkKey = "rollback-fence-rebaseline-watermark="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, steadyStateKeyHyphen):
			value = strings.TrimSpace(strings.TrimPrefix(token, steadyStateKeyHyphen))
		case strings.HasPrefix(token, steadyStateKeyShort):
			value = strings.TrimSpace(strings.TrimPrefix(token, steadyStateKeyShort))
		case strings.HasPrefix(token, rebaselineWatermarkKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, rebaselineWatermarkKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		steadyState, err := strconv.ParseInt(value, 10, 64)
		if err != nil || steadyState < 0 {
			return 0, false
		}
		return steadyState, true
	}

	return 0, false
}

func parseRollbackFenceSteadyGeneration(digest string) (int64, bool) {
	const (
		steadyGenerationKey = "rollback-fence-steady-generation="
		baselineGenKey      = "rollback-fence-baseline-generation="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, steadyGenerationKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, steadyGenerationKey))
		case strings.HasPrefix(token, baselineGenKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, baselineGenKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		generation, err := strconv.ParseInt(value, 10, 64)
		if err != nil || generation < 0 {
			return 0, false
		}
		return generation, true
	}

	return 0, false
}

func parseRollbackFenceGenerationRetentionFloor(digest string) (int64, bool) {
	const (
		retentionFloorKey      = "rollback-fence-generation-retention-floor="
		retentionFloorShortKey = "rollback-fence-retention-floor="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, retentionFloorKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, retentionFloorKey))
		case strings.HasPrefix(token, retentionFloorShortKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, retentionFloorShortKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		retentionFloor, err := strconv.ParseInt(value, 10, 64)
		if err != nil || retentionFloor < 0 {
			return 0, false
		}
		return retentionFloor, true
	}

	return 0, false
}

func parseRollbackFenceFloorLiftEpoch(digest string) (int64, bool) {
	const (
		floorLiftEpochKey          = "rollback-fence-floor-lift-epoch="
		retentionFloorLiftEpochKey = "rollback-fence-retention-floor-lift-epoch="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, floorLiftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, floorLiftEpochKey))
		case strings.HasPrefix(token, retentionFloorLiftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, retentionFloorLiftEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		floorLiftEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || floorLiftEpoch < 0 {
			return 0, false
		}
		return floorLiftEpoch, true
	}

	return 0, false
}

type rollbackFenceOwnershipOrdering struct {
	epoch                                                                                                                              int64
	bridgeSequence                                                                                                                     int64
	drainWatermark                                                                                                                     int64
	liveHead                                                                                                                           int64
	steadyStateWatermark                                                                                                               int64
	steadyGeneration                                                                                                                   int64
	generationFloor                                                                                                                    int64
	floorLiftEpoch                                                                                                                     int64
	settleWindowEpoch                                                                                                                  int64
	spilloverEpoch                                                                                                                     int64
	spilloverRejoinEpoch                                                                                                               int64
	rejoinSealEpoch                                                                                                                    int64
	sealDriftEpoch                                                                                                                     int64
	driftReanchorEpoch                                                                                                                 int64
	reanchorCompactionEpoch                                                                                                            int64
	compactionExpiryEpoch                                                                                                              int64
	resurrectionEpoch                                                                                                                  int64
	reintegrationEpoch                                                                                                                 int64
	reintegrationSealEpoch                                                                                                             int64
	reintegrationSealDriftEpoch                                                                                                        int64
	reintegrationSealDriftReanchorEpoch                                                                                                int64
	reintegrationSealDriftReanchorCompactionEpoch                                                                                      int64
	reintegrationSealDriftReanchorCompactionExpiryEpoch                                                                                int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch                                                                      int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch                                                         int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch                                                     int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch                                                int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch                                        int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch                              int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch                        int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch              int64
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch int64
}

func parseRollbackFenceOwnershipOrdering(epoch int64, digest string) (rollbackFenceOwnershipOrdering, bool) {
	if epoch < 0 {
		return rollbackFenceOwnershipOrdering{}, false
	}
	normalized := normalizePolicyManifestDigest(digest)
	if !isRollbackFencePostExpiryLateMarkerReleaseDigest(epoch, normalized) {
		return rollbackFenceOwnershipOrdering{}, false
	}
	releaseEpoch, ok := parseRollbackFenceLateMarkerReleaseEpoch(normalized)
	if !ok {
		return rollbackFenceOwnershipOrdering{}, false
	}
	bridgeSequence, hasBridgeSequence := parseRollbackFenceLateBridgeSequence(normalized)
	releaseWatermark, hasExplicitWatermark := parseRollbackFenceLateBridgeReleaseWatermark(normalized)
	drainWatermark, hasDrainWatermark := parseRollbackFenceLateBridgeDrainWatermark(normalized)
	liveHead, hasLiveHead := parseRollbackFenceLiveHeadWatermark(normalized)
	steadyStateWatermark, hasSteadyStateWatermark := parseRollbackFenceSteadyStateWatermark(normalized)
	steadyGeneration, hasSteadyGeneration := parseRollbackFenceSteadyGeneration(normalized)
	generationFloor, hasGenerationFloor := parseRollbackFenceGenerationRetentionFloor(normalized)
	floorLiftEpoch, hasFloorLiftEpoch := parseRollbackFenceFloorLiftEpoch(normalized)
	settleWindowEpoch, hasSettleWindowEpoch := parseRollbackFenceSettleWindowEpoch(normalized)
	spilloverEpoch, hasSpilloverEpoch := parseRollbackFenceSpilloverEpoch(normalized)
	spilloverRejoinEpoch, hasSpilloverRejoinEpoch := parseRollbackFenceSpilloverRejoinEpoch(normalized)
	rejoinSealEpoch, hasRejoinSealEpoch := parseRollbackFenceRejoinSealEpoch(normalized)
	sealDriftEpoch, hasSealDriftEpoch := parseRollbackFenceSealDriftEpoch(normalized)
	driftReanchorEpoch, hasDriftReanchorEpoch := parseRollbackFenceDriftReanchorEpoch(normalized)
	reanchorCompactionEpoch, hasReanchorCompactionEpoch := parseRollbackFenceReanchorCompactionEpoch(normalized)
	compactionExpiryEpoch, hasCompactionExpiryEpoch := parseRollbackFenceCompactionExpiryEpoch(normalized)
	resurrectionEpoch, hasResurrectionEpoch := parseRollbackFenceResurrectionQuarantineEpoch(normalized)
	reintegrationEpoch, hasReintegrationEpoch := parseRollbackFenceResurrectionReintegrationEpoch(normalized)
	reintegrationSealEpoch, hasReintegrationSealEpoch := parseRollbackFenceResurrectionReintegrationSealEpoch(normalized)
	reintegrationSealDriftEpoch, hasReintegrationSealDriftEpoch := parseRollbackFenceResurrectionReintegrationSealDriftEpoch(normalized)
	reintegrationSealDriftReanchorEpoch, hasReintegrationSealDriftReanchorEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorEpoch(normalized)
	reintegrationSealDriftReanchorCompactionEpoch, hasReintegrationSealDriftReanchorCompactionEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryEpoch, hasReintegrationSealDriftReanchorCompactionExpiryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(normalized)
	reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch, hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(normalized)
	if hasBridgeSequence != hasExplicitWatermark {
		// Quarantine ambiguous late-bridge markers until both sequence and
		// release watermark are present for deterministic ordering.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasDrainWatermark && !hasBridgeSequence {
		// Quarantine ambiguous backlog-drain markers until the corresponding
		// late-bridge ownership tuple is complete.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasLiveHead && !hasBridgeSequence {
		// Quarantine ambiguous live-catchup markers until the corresponding
		// late-bridge ownership tuple is complete.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasLiveHead && !hasDrainWatermark {
		// Quarantine drain-to-live handoff markers until an explicit
		// backlog-drain watermark is present in the ownership tuple.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSteadyStateWatermark && !hasLiveHead {
		// Quarantine steady-state rebaseline markers until the corresponding
		// live-catchup ownership tuple is complete.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSteadyGeneration && !hasSteadyStateWatermark {
		// Quarantine baseline-rotation generation markers until explicit
		// steady-state ownership is present for deterministic cross-generation
		// ordering.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasGenerationFloor && !hasSteadyGeneration {
		// Quarantine generation-prune markers until the corresponding
		// steady-generation ownership tuple is explicit.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasFloorLiftEpoch && !hasGenerationFloor {
		// Quarantine retention-floor-lift markers until explicit
		// generation-retention-floor ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSettleWindowEpoch && !hasFloorLiftEpoch {
		// Quarantine settle-window markers until explicit retention-floor-lift
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSpilloverEpoch && !hasSettleWindowEpoch {
		// Quarantine late-spillover markers until explicit settle-window
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSpilloverRejoinEpoch && !hasSpilloverEpoch {
		// Quarantine spillover-rejoin markers until explicit spillover
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasRejoinSealEpoch && !hasSpilloverRejoinEpoch {
		// Quarantine steady-seal markers until explicit spillover-rejoin
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasSealDriftEpoch && !hasRejoinSealEpoch {
		// Quarantine post-steady-seal drift markers until explicit steady-seal
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasDriftReanchorEpoch && !hasSealDriftEpoch {
		// Quarantine post-drift reanchor markers until explicit post-drift
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReanchorCompactionEpoch && !hasDriftReanchorEpoch {
		// Quarantine post-reanchor lineage-compaction markers until explicit
		// post-drift reanchor ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasCompactionExpiryEpoch && !hasReanchorCompactionEpoch {
		// Quarantine post-lineage-compaction marker-expiry markers until
		// explicit post-reanchor compaction ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasResurrectionEpoch && !hasCompactionExpiryEpoch {
		// Quarantine post-marker-expiry late-resurrection markers until
		// explicit post-lineage-compaction marker-expiry ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationEpoch && !hasResurrectionEpoch {
		// Quarantine reintegration markers until explicit post-late-resurrection
		// quarantine ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealEpoch && !hasReintegrationEpoch {
		// Quarantine post-reintegration seal markers until explicit reintegration
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftEpoch && !hasReintegrationSealEpoch {
		// Quarantine post-reintegration-seal drift markers until explicit
		// reintegration-seal ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorEpoch && !hasReintegrationSealDriftEpoch {
		// Quarantine post-reintegration-seal drift-reanchor markers until
		// explicit reintegration-seal drift ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionEpoch && !hasReintegrationSealDriftReanchorEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// markers until explicit reintegration-seal drift-reanchor ownership is
		// present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryEpoch && !hasReintegrationSealDriftReanchorCompactionEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry candidates until explicit reintegration-seal
		// drift-reanchor lineage-compaction ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch && !hasReintegrationSealDriftReanchorCompactionExpiryEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine candidates until explicit
		// reintegration-seal drift-reanchor lineage-compaction marker-expiry
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration candidates
		// until explicit quarantine ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal
		// candidates until explicit reintegration ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift
		// candidates until explicit reintegration-seal ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor
		// candidates until explicit reintegration-seal-drift ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction
		// candidates until explicit reintegration-seal-drift-reanchor ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction
		// marker-expiry candidates until explicit reintegration-seal-drift-reanchor-lineage-compaction
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction
		// marker-expiry-late-resurrection-quarantine candidates until explicit
		// reintegration-seal-drift-reanchor-lineage-compaction marker-expiry
		// ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch &&
		!hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		// Quarantine post-reintegration-seal drift-reanchor lineage-compaction
		// marker-expiry late-resurrection quarantine reintegration-seal-drift-reanchor-lineage-compaction
		// marker-expiry-late-resurrection-quarantine-reintegration candidates
		// until explicit late-resurrection-quarantine ownership is present.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if !hasBridgeSequence {
		bridgeSequence = 0
		releaseWatermark = releaseEpoch
	}
	if releaseWatermark < releaseEpoch {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if !hasDrainWatermark {
		drainWatermark = releaseWatermark
	}
	if drainWatermark < releaseWatermark {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if !hasLiveHead {
		liveHead = drainWatermark
	}
	if liveHead < drainWatermark {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if !hasSteadyStateWatermark {
		steadyStateWatermark = liveHead
	}
	if steadyStateWatermark < liveHead {
		return rollbackFenceOwnershipOrdering{}, false
	}
	if !hasSteadyGeneration {
		steadyGeneration = 0
	}
	if !hasGenerationFloor {
		generationFloor = 0
	}
	if !hasFloorLiftEpoch {
		floorLiftEpoch = 0
	}
	if !hasSettleWindowEpoch {
		settleWindowEpoch = 0
	}
	if !hasSpilloverEpoch {
		spilloverEpoch = 0
	}
	if !hasSpilloverRejoinEpoch {
		spilloverRejoinEpoch = 0
	}
	if !hasRejoinSealEpoch {
		rejoinSealEpoch = 0
	}
	if !hasSealDriftEpoch {
		sealDriftEpoch = 0
	}
	if !hasDriftReanchorEpoch {
		driftReanchorEpoch = 0
	}
	if !hasReanchorCompactionEpoch {
		reanchorCompactionEpoch = 0
	}
	if !hasCompactionExpiryEpoch {
		compactionExpiryEpoch = 0
	}
	if !hasResurrectionEpoch {
		resurrectionEpoch = 0
	}
	if !hasReintegrationEpoch {
		reintegrationEpoch = 0
	}
	if !hasReintegrationSealEpoch {
		reintegrationSealEpoch = 0
	}
	if !hasReintegrationSealDriftEpoch {
		reintegrationSealDriftEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorEpoch {
		reintegrationSealDriftReanchorEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionEpoch {
		reintegrationSealDriftReanchorCompactionEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryEpoch {
		reintegrationSealDriftReanchorCompactionExpiryEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch = 0
	}
	if !hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch {
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch = 0
	}
	if hasResurrectionEpoch && resurrectionEpoch <= compactionExpiryEpoch {
		// Quarantine ambiguous late-resurrection markers that do not advance
		// strictly beyond verified marker-expiry ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationEpoch && reintegrationEpoch <= resurrectionEpoch {
		// Quarantine ambiguous reintegration markers that do not advance
		// strictly beyond verified late-resurrection ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealEpoch && reintegrationSealEpoch <= reintegrationEpoch {
		// Quarantine ambiguous reintegration seal markers that do not advance
		// strictly beyond verified reintegration ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftEpoch && reintegrationSealDriftEpoch <= reintegrationSealEpoch {
		// Quarantine ambiguous reintegration-seal drift markers that do not
		// advance strictly beyond verified reintegration-seal ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorEpoch && reintegrationSealDriftReanchorEpoch <= reintegrationSealDriftEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor markers that
		// do not advance strictly beyond verified reintegration-seal drift
		// ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionEpoch && reintegrationSealDriftReanchorCompactionEpoch <= reintegrationSealDriftReanchorEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction markers that do not advance strictly beyond
		// verified reintegration-seal drift-reanchor ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryEpoch && reintegrationSealDriftReanchorCompactionExpiryEpoch <= reintegrationSealDriftReanchorCompactionEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry candidates that do not advance
		// strictly beyond verified reintegration-seal drift-reanchor
		// lineage-compaction ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch <= reintegrationSealDriftReanchorCompactionExpiryEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// candidates that do not advance strictly beyond verified
		// reintegration-seal drift-reanchor lineage-compaction marker-expiry
		// ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration candidates that do not advance strictly beyond verified
		// quarantine ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal candidates that do not advance strictly beyond
		// verified reintegration ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift candidates that do not advance strictly
		// beyond verified reintegration-seal ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift-reanchor candidates that do not advance
		// strictly beyond verified reintegration-seal-drift ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift-reanchor-lineage-compaction candidates that
		// do not advance strictly beyond verified reintegration-seal-drift-reanchor
		// ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift-reanchor-lineage-compaction marker-expiry
		// candidates that do not advance strictly beyond verified
		// reintegration-seal-drift-reanchor-lineage-compaction ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift-reanchor-lineage-compaction marker-expiry
		// late-resurrection-quarantine candidates that do not advance strictly
		// beyond verified marker-expiry ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if hasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch &&
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch <= reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch {
		// Quarantine ambiguous reintegration-seal drift-reanchor
		// lineage-compaction marker-expiry late-resurrection quarantine
		// reintegration-seal-drift-reanchor-lineage-compaction marker-expiry
		// late-resurrection-quarantine-reintegration candidates that do not
		// advance strictly beyond verified late-resurrection-quarantine
		// ownership.
		return rollbackFenceOwnershipOrdering{}, false
	}
	if generationFloor > steadyGeneration {
		// Quarantine unresolved retired-generation markers whose ownership
		// points below the active retention floor.
		return rollbackFenceOwnershipOrdering{}, false
	}
	return rollbackFenceOwnershipOrdering{
		epoch:                               epoch,
		bridgeSequence:                      bridgeSequence,
		drainWatermark:                      drainWatermark,
		liveHead:                            liveHead,
		steadyStateWatermark:                steadyStateWatermark,
		steadyGeneration:                    steadyGeneration,
		generationFloor:                     generationFloor,
		floorLiftEpoch:                      floorLiftEpoch,
		settleWindowEpoch:                   settleWindowEpoch,
		spilloverEpoch:                      spilloverEpoch,
		spilloverRejoinEpoch:                spilloverRejoinEpoch,
		rejoinSealEpoch:                     rejoinSealEpoch,
		sealDriftEpoch:                      sealDriftEpoch,
		driftReanchorEpoch:                  driftReanchorEpoch,
		reanchorCompactionEpoch:             reanchorCompactionEpoch,
		compactionExpiryEpoch:               compactionExpiryEpoch,
		resurrectionEpoch:                   resurrectionEpoch,
		reintegrationEpoch:                  reintegrationEpoch,
		reintegrationSealEpoch:              reintegrationSealEpoch,
		reintegrationSealDriftEpoch:         reintegrationSealDriftEpoch,
		reintegrationSealDriftReanchorEpoch: reintegrationSealDriftReanchorEpoch,
		reintegrationSealDriftReanchorCompactionEpoch:                                                                                      reintegrationSealDriftReanchorCompactionEpoch,
		reintegrationSealDriftReanchorCompactionExpiryEpoch:                                                                                reintegrationSealDriftReanchorCompactionExpiryEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:                                                                      reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch:                                                         reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch:                                                     reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch:                                                reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch:                                        reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch:                              reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch:                        reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:              reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch,
		reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch: reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch,
	}, true
}

func compareRollbackFenceOwnershipOrdering(
	left rollbackFenceOwnershipOrdering,
	right rollbackFenceOwnershipOrdering,
) int {
	switch {
	case left.epoch < right.epoch:
		return -1
	case left.epoch > right.epoch:
		return 1
	}
	switch {
	case left.bridgeSequence < right.bridgeSequence:
		return -1
	case left.bridgeSequence > right.bridgeSequence:
		return 1
	}
	switch {
	case left.drainWatermark < right.drainWatermark:
		return -1
	case left.drainWatermark > right.drainWatermark:
		return 1
	}
	switch {
	case left.liveHead < right.liveHead:
		return -1
	case left.liveHead > right.liveHead:
		return 1
	}
	switch {
	case left.steadyStateWatermark < right.steadyStateWatermark:
		return -1
	case left.steadyStateWatermark > right.steadyStateWatermark:
		return 1
	}
	switch {
	case left.steadyGeneration < right.steadyGeneration:
		return -1
	case left.steadyGeneration > right.steadyGeneration:
		return 1
	}
	switch {
	case left.generationFloor < right.generationFloor:
		return -1
	case left.generationFloor > right.generationFloor:
		return 1
	}
	switch {
	case left.floorLiftEpoch < right.floorLiftEpoch:
		return -1
	case left.floorLiftEpoch > right.floorLiftEpoch:
		return 1
	}
	switch {
	case left.settleWindowEpoch < right.settleWindowEpoch:
		return -1
	case left.settleWindowEpoch > right.settleWindowEpoch:
		return 1
	}
	switch {
	case left.spilloverEpoch < right.spilloverEpoch:
		return -1
	case left.spilloverEpoch > right.spilloverEpoch:
		return 1
	}
	switch {
	case left.spilloverRejoinEpoch < right.spilloverRejoinEpoch:
		return -1
	case left.spilloverRejoinEpoch > right.spilloverRejoinEpoch:
		return 1
	}
	switch {
	case left.rejoinSealEpoch < right.rejoinSealEpoch:
		return -1
	case left.rejoinSealEpoch > right.rejoinSealEpoch:
		return 1
	default:
	}
	switch {
	case left.sealDriftEpoch < right.sealDriftEpoch:
		return -1
	case left.sealDriftEpoch > right.sealDriftEpoch:
		return 1
	}
	switch {
	case left.driftReanchorEpoch < right.driftReanchorEpoch:
		return -1
	case left.driftReanchorEpoch > right.driftReanchorEpoch:
		return 1
	}
	switch {
	case left.reanchorCompactionEpoch < right.reanchorCompactionEpoch:
		return -1
	case left.reanchorCompactionEpoch > right.reanchorCompactionEpoch:
		return 1
	}
	switch {
	case left.compactionExpiryEpoch < right.compactionExpiryEpoch:
		return -1
	case left.compactionExpiryEpoch > right.compactionExpiryEpoch:
		return 1
	}
	switch {
	case left.resurrectionEpoch < right.resurrectionEpoch:
		return -1
	case left.resurrectionEpoch > right.resurrectionEpoch:
		return 1
	}
	switch {
	case left.reintegrationEpoch < right.reintegrationEpoch:
		return -1
	case left.reintegrationEpoch > right.reintegrationEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealEpoch < right.reintegrationSealEpoch:
		return -1
	case left.reintegrationSealEpoch > right.reintegrationSealEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftEpoch < right.reintegrationSealDriftEpoch:
		return -1
	case left.reintegrationSealDriftEpoch > right.reintegrationSealDriftEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorEpoch < right.reintegrationSealDriftReanchorEpoch:
		return -1
	case left.reintegrationSealDriftReanchorEpoch > right.reintegrationSealDriftReanchorEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionEpoch < right.reintegrationSealDriftReanchorCompactionEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionEpoch > right.reintegrationSealDriftReanchorCompactionEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryEpoch < right.reintegrationSealDriftReanchorCompactionExpiryEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryEpoch > right.reintegrationSealDriftReanchorCompactionExpiryEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch:
		return 1
	}
	switch {
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch < right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch:
		return -1
	case left.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch > right.reintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch:
		return 1
	default:
		return 0
	}
}

func parseRollbackFenceSettleWindowEpoch(digest string) (int64, bool) {
	const (
		settleWindowEpochKey     = "rollback-fence-settle-window-epoch="
		floorLiftSettleWindowKey = "rollback-fence-floor-lift-settle-window-epoch="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, settleWindowEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, settleWindowEpochKey))
		case strings.HasPrefix(token, floorLiftSettleWindowKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, floorLiftSettleWindowKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		settleWindowEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || settleWindowEpoch < 0 {
			return 0, false
		}
		return settleWindowEpoch, true
	}

	return 0, false
}

func parseRollbackFenceSpilloverEpoch(digest string) (int64, bool) {
	const (
		spilloverEpochKey  = "rollback-fence-spillover-epoch="
		lateSpilloverKey   = "rollback-fence-late-spillover-epoch="
		settleSpilloverKey = "rollback-fence-settle-window-spillover-epoch="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, spilloverEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, spilloverEpochKey))
		case strings.HasPrefix(token, lateSpilloverKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateSpilloverKey))
		case strings.HasPrefix(token, settleSpilloverKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, settleSpilloverKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		spilloverEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || spilloverEpoch < 0 {
			return 0, false
		}
		return spilloverEpoch, true
	}

	return 0, false
}

func parseRollbackFenceSpilloverRejoinEpoch(digest string) (int64, bool) {
	const (
		spilloverRejoinEpochKey = "rollback-fence-spillover-rejoin-epoch="
		rejoinWindowEpochKey    = "rollback-fence-rejoin-window-epoch="
		lateSpilloverRejoinKey  = "rollback-fence-late-spillover-rejoin-epoch="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, spilloverRejoinEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, spilloverRejoinEpochKey))
		case strings.HasPrefix(token, rejoinWindowEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, rejoinWindowEpochKey))
		case strings.HasPrefix(token, lateSpilloverRejoinKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateSpilloverRejoinKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		spilloverRejoinEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || spilloverRejoinEpoch < 0 {
			return 0, false
		}
		return spilloverRejoinEpoch, true
	}

	return 0, false
}

func parseRollbackFenceRejoinSealEpoch(digest string) (int64, bool) {
	const (
		rejoinSealEpochKey = "rollback-fence-rejoin-seal-epoch="
		steadySealEpochKey = "rollback-fence-steady-seal-epoch="
		postRejoinSealKey  = "rollback-fence-post-rejoin-seal-epoch="
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, rejoinSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, rejoinSealEpochKey))
		case strings.HasPrefix(token, steadySealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, steadySealEpochKey))
		case strings.HasPrefix(token, postRejoinSealKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postRejoinSealKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		rejoinSealEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || rejoinSealEpoch < 0 {
			return 0, false
		}
		return rejoinSealEpoch, true
	}

	return 0, false
}

func parseRollbackFenceSealDriftEpoch(digest string) (int64, bool) {
	const (
		sealDriftEpochKey       = "rollback-fence-seal-drift-epoch="
		steadySealDriftEpochKey = "rollback-fence-steady-seal-drift-epoch="
		postSteadySealDriftKey  = "rollback-fence-post-steady-seal-drift-epoch="
	)

	var (
		sealDriftEpoch int64
		seen           bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, sealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, sealDriftEpochKey))
		case strings.HasPrefix(token, steadySealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, steadySealDriftEpochKey))
		case strings.HasPrefix(token, postSteadySealDriftKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postSteadySealDriftKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			sealDriftEpoch = parsedEpoch
			seen = true
			continue
		}
		if sealDriftEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return sealDriftEpoch, true
}

func parseRollbackFenceDriftReanchorEpoch(digest string) (int64, bool) {
	const (
		driftReanchorEpochKey      = "rollback-fence-drift-reanchor-epoch="
		postDriftReanchorEpochKey  = "rollback-fence-post-drift-reanchor-epoch="
		postSteadyDriftReanchorKey = "rollback-fence-post-steady-drift-reanchor-epoch="
	)

	var (
		driftReanchorEpoch int64
		seen               bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, driftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, driftReanchorEpochKey))
		case strings.HasPrefix(token, postDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postDriftReanchorEpochKey))
		case strings.HasPrefix(token, postSteadyDriftReanchorKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postSteadyDriftReanchorKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			driftReanchorEpoch = parsedEpoch
			seen = true
			continue
		}
		if driftReanchorEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return driftReanchorEpoch, true
}

func parseRollbackFenceReanchorCompactionEpoch(digest string) (int64, bool) {
	const (
		reanchorCompactionEpochKey     = "rollback-fence-reanchor-compaction-epoch="
		postReanchorCompactionEpochKey = "rollback-fence-post-reanchor-compaction-epoch="
		postDriftReanchorCompactionKey = "rollback-fence-post-drift-reanchor-compaction-epoch="
	)

	var (
		reanchorCompactionEpoch int64
		seen                    bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, reanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, reanchorCompactionEpochKey))
		case strings.HasPrefix(token, postReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postDriftReanchorCompactionKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postDriftReanchorCompactionKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			reanchorCompactionEpoch = parsedEpoch
			seen = true
			continue
		}
		if reanchorCompactionEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return reanchorCompactionEpoch, true
}

func parseRollbackFenceCompactionExpiryEpoch(digest string) (int64, bool) {
	const (
		compactionExpiryEpochKey   = "rollback-fence-compaction-expiry-epoch="
		lineageCompactionExpiryKey = "rollback-fence-lineage-compaction-expiry-epoch="
		postLineageCompactionKey   = "rollback-fence-post-lineage-compaction-expiry-epoch="
	)

	var (
		compactionExpiryEpoch int64
		seen                  bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, compactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, compactionExpiryEpochKey))
		case strings.HasPrefix(token, lineageCompactionExpiryKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lineageCompactionExpiryKey))
		case strings.HasPrefix(token, postLineageCompactionKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postLineageCompactionKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			compactionExpiryEpoch = parsedEpoch
			seen = true
			continue
		}
		if compactionExpiryEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return compactionExpiryEpoch, true
}

func parseRollbackFenceResurrectionQuarantineEpoch(digest string) (int64, bool) {
	const (
		resurrectionQuarantineEpochKey         = "rollback-fence-resurrection-quarantine-epoch="
		lateResurrectionQuarantineEpochKey     = "rollback-fence-late-resurrection-quarantine-epoch="
		postExpiryResurrectionQuarantineEpoch  = "rollback-fence-post-expiry-late-resurrection-quarantine-epoch="
		postMarkerExpiryResurrectionQuarantine = "rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch="
	)

	var (
		resurrectionQuarantineEpoch int64
		seen                        bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionQuarantineEpochKey))
		case strings.HasPrefix(token, lateResurrectionQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionQuarantineEpochKey))
		case strings.HasPrefix(token, postExpiryResurrectionQuarantineEpoch):
			value = strings.TrimSpace(strings.TrimPrefix(token, postExpiryResurrectionQuarantineEpoch))
		case strings.HasPrefix(token, postMarkerExpiryResurrectionQuarantine):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerExpiryResurrectionQuarantine))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionQuarantineEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionQuarantineEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionQuarantineEpoch, true
}

func parseRollbackFenceResurrectionReintegrationEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationEpochKey     = "rollback-fence-resurrection-reintegration-epoch="
		lateResurrectionReintegrationEpochKey = "rollback-fence-late-resurrection-reintegration-epoch="
		postQuarantineReintegrationEpochKey   = "rollback-fence-post-late-resurrection-quarantine-reintegration-epoch="
		postMarkerReintegrationEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-epoch="
	)

	var (
		resurrectionReintegrationEpoch int64
		seen                           bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealEpochKey     = "rollback-fence-resurrection-reintegration-seal-epoch="
		lateResurrectionReintegrationSealEpochKey = "rollback-fence-late-resurrection-reintegration-seal-epoch="
		postQuarantineReintegrationSealEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-epoch="
		postMarkerReintegrationSealEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-epoch="
		postReintegrationSealEpochKey             = "rollback-fence-post-reintegration-seal-epoch="
	)

	var (
		resurrectionReintegrationSealEpoch int64
		seen                               bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealEpochKey))
		case strings.HasPrefix(token, postReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-epoch="
		lateResurrectionReintegrationSealDriftEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-epoch="
		postQuarantineReintegrationSealDriftEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-epoch="
		postMarkerReintegrationSealDriftEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-epoch="
		postReintegrationSealDriftEpochKey             = "rollback-fence-post-reintegration-seal-drift-epoch="
	)

	var (
		resurrectionReintegrationSealDriftEpoch int64
		seen                                    bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-epoch="
		lateResurrectionReintegrationSealDriftReanchorEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-epoch="
		postQuarantineReintegrationSealDriftReanchorEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-epoch="
		postMarkerReintegrationSealDriftReanchorEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-epoch="
		postReintegrationSealDriftReanchorEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorEpoch int64
		seen                                            bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-epoch="
		postReintegrationSealDriftReanchorCompactionEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionEpoch int64
		seen                                                      bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch int64
		seen                                                            bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch int64
		seen                                                                      bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch int64
		seen                                                                                   bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch int64
		seen                                                                                       bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch int64
		seen                                                                                            bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch int64
		seen                                                                                                    bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch int64
		seen                                                                                                              bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch int64
		seen                                                                                                                    bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch int64
		seen                                                                                                                              bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch, true
}

func parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(digest string) (int64, bool) {
	const (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey     = "rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey = "rollback-fence-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey   = "rollback-fence-post-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey       = "rollback-fence-post-marker-expiry-late-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
		postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey             = "rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-reintegration-epoch="
	)

	var (
		resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch int64
		seen                                                                                                                                           bool
	)

	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		var value string
		switch {
		case strings.HasPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, lateResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postMarkerReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		case strings.HasPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey):
			value = strings.TrimSpace(strings.TrimPrefix(token, postReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpochKey))
		default:
			continue
		}
		if value == "" {
			return 0, false
		}
		parsedEpoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsedEpoch < 0 {
			return 0, false
		}
		if !seen {
			resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch = parsedEpoch
			seen = true
			continue
		}
		if resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch != parsedEpoch {
			return 0, false
		}
	}

	if !seen {
		return 0, false
	}
	return resurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch, true
}

func isRollbackFenceTombstoneExpiryDigest(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
	if hasRollbackFenceEpochCompactionTombstone(normalized) {
		return false
	}
	if _, hasHold := parseRollbackFenceLateMarkerHoldEpoch(normalized); hasHold {
		return false
	}
	if _, hasRelease := parseRollbackFenceLateMarkerReleaseEpoch(normalized); hasRelease {
		return false
	}
	expiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(normalized)
	if !ok {
		return false
	}
	rollbackFromSeq, rollbackToSeq, rollbackForwardSeq, ok := parseRollbackLineage(normalized)
	if !ok {
		return false
	}
	if rollbackToSeq != epoch {
		return false
	}
	if rollbackFromSeq <= rollbackToSeq {
		return false
	}
	if rollbackForwardSeq < rollbackFromSeq {
		return false
	}
	// Expiry must be explicitly post-fence to avoid accepting ambiguous
	// same-epoch markers that can re-open stale ownership.
	return expiryEpoch > epoch
}

func isRollbackFencePostExpiryLateMarkerQuarantineDigest(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
	if hasRollbackFenceEpochCompactionTombstone(normalized) {
		return false
	}
	if _, hasRelease := parseRollbackFenceLateMarkerReleaseEpoch(normalized); hasRelease {
		return false
	}
	expiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(normalized)
	if !ok {
		return false
	}
	holdEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(normalized)
	if !ok {
		return false
	}
	rollbackFromSeq, rollbackToSeq, rollbackForwardSeq, ok := parseRollbackLineage(normalized)
	if !ok {
		return false
	}
	if rollbackToSeq != epoch {
		return false
	}
	if rollbackFromSeq <= rollbackToSeq {
		return false
	}
	if rollbackForwardSeq < rollbackFromSeq {
		return false
	}
	if expiryEpoch <= epoch {
		return false
	}
	// Quarantine hold epochs must be explicitly post-expiry to avoid ambiguous
	// same-epoch marker ownership.
	return holdEpoch > expiryEpoch
}

func isRollbackFencePostExpiryLateMarkerReleaseDigest(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
	if hasRollbackFenceEpochCompactionTombstone(normalized) {
		return false
	}
	expiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(normalized)
	if !ok {
		return false
	}
	holdEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(normalized)
	if !ok {
		return false
	}
	releaseEpoch, ok := parseRollbackFenceLateMarkerReleaseEpoch(normalized)
	if !ok {
		return false
	}
	rollbackFromSeq, rollbackToSeq, rollbackForwardSeq, ok := parseRollbackLineage(normalized)
	if !ok {
		return false
	}
	if rollbackToSeq != epoch {
		return false
	}
	if rollbackFromSeq <= rollbackToSeq {
		return false
	}
	if rollbackForwardSeq < rollbackFromSeq {
		return false
	}
	if expiryEpoch <= epoch {
		return false
	}
	if holdEpoch <= expiryEpoch {
		return false
	}
	// Release boundaries must be explicitly post-hold to avoid accepting
	// ambiguous release ownership.
	return releaseEpoch > holdEpoch
}

func isDeterministicRollbackFenceTombstoneExpiryTransition(
	epoch int64,
	sourceDigest string,
	targetDigest string,
) bool {
	if epoch < 0 {
		return false
	}
	sourceNormalized := normalizePolicyManifestDigest(sourceDigest)
	targetNormalized := normalizePolicyManifestDigest(targetDigest)
	if !isRollbackFenceEpochCompactionDigest(epoch, sourceNormalized) {
		return false
	}
	if !isRollbackFenceTombstoneExpiryDigest(epoch, targetNormalized) {
		return false
	}
	sourceFromSeq, sourceToSeq, sourceForwardSeq, ok := parseRollbackLineage(sourceNormalized)
	if !ok {
		return false
	}
	targetFromSeq, targetToSeq, targetForwardSeq, ok := parseRollbackLineage(targetNormalized)
	if !ok {
		return false
	}
	if sourceFromSeq != targetFromSeq || sourceToSeq != targetToSeq || sourceForwardSeq != targetForwardSeq {
		return false
	}
	if sourceToSeq != epoch || targetToSeq != epoch {
		return false
	}
	if sourceFromSeq <= sourceToSeq || targetFromSeq <= targetToSeq {
		return false
	}
	if sourceForwardSeq < sourceFromSeq || targetForwardSeq < targetFromSeq {
		return false
	}
	return true
}

func isDeterministicRollbackFencePostExpiryLateMarkerQuarantineTransition(
	epoch int64,
	sourceDigest string,
	targetDigest string,
) bool {
	if epoch < 0 {
		return false
	}
	sourceNormalized := normalizePolicyManifestDigest(sourceDigest)
	targetNormalized := normalizePolicyManifestDigest(targetDigest)
	if !isRollbackFenceTombstoneExpiryDigest(epoch, sourceNormalized) {
		return false
	}
	if !isRollbackFencePostExpiryLateMarkerQuarantineDigest(epoch, targetNormalized) {
		return false
	}
	sourceFromSeq, sourceToSeq, sourceForwardSeq, ok := parseRollbackLineage(sourceNormalized)
	if !ok {
		return false
	}
	targetFromSeq, targetToSeq, targetForwardSeq, ok := parseRollbackLineage(targetNormalized)
	if !ok {
		return false
	}
	if sourceFromSeq != targetFromSeq || sourceToSeq != targetToSeq || sourceForwardSeq != targetForwardSeq {
		return false
	}
	if sourceToSeq != epoch || targetToSeq != epoch {
		return false
	}
	if sourceFromSeq <= sourceToSeq || targetFromSeq <= targetToSeq {
		return false
	}
	if sourceForwardSeq < sourceFromSeq || targetForwardSeq < targetFromSeq {
		return false
	}
	sourceExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(sourceNormalized)
	if !ok {
		return false
	}
	targetExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(targetNormalized)
	if !ok {
		return false
	}
	return sourceExpiryEpoch == targetExpiryEpoch
}

func isDeterministicRollbackFencePostExpiryLateMarkerReleaseTransition(
	epoch int64,
	sourceDigest string,
	targetDigest string,
) bool {
	if epoch < 0 {
		return false
	}
	sourceNormalized := normalizePolicyManifestDigest(sourceDigest)
	targetNormalized := normalizePolicyManifestDigest(targetDigest)
	if !isRollbackFencePostExpiryLateMarkerQuarantineDigest(epoch, sourceNormalized) {
		return false
	}
	if !isRollbackFencePostExpiryLateMarkerReleaseDigest(epoch, targetNormalized) {
		return false
	}
	sourceFromSeq, sourceToSeq, sourceForwardSeq, ok := parseRollbackLineage(sourceNormalized)
	if !ok {
		return false
	}
	targetFromSeq, targetToSeq, targetForwardSeq, ok := parseRollbackLineage(targetNormalized)
	if !ok {
		return false
	}
	if sourceFromSeq != targetFromSeq || sourceToSeq != targetToSeq || sourceForwardSeq != targetForwardSeq {
		return false
	}
	if sourceToSeq != epoch || targetToSeq != epoch {
		return false
	}
	if sourceFromSeq <= sourceToSeq || targetFromSeq <= targetToSeq {
		return false
	}
	if sourceForwardSeq < sourceFromSeq || targetForwardSeq < targetFromSeq {
		return false
	}
	sourceExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(sourceNormalized)
	if !ok {
		return false
	}
	targetExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(targetNormalized)
	if !ok {
		return false
	}
	if sourceExpiryEpoch != targetExpiryEpoch {
		return false
	}
	sourceHoldEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(sourceNormalized)
	if !ok {
		return false
	}
	targetHoldEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(targetNormalized)
	if !ok {
		return false
	}
	if sourceHoldEpoch != targetHoldEpoch {
		return false
	}
	targetReleaseEpoch, ok := parseRollbackFenceLateMarkerReleaseEpoch(targetNormalized)
	if !ok {
		return false
	}
	return targetReleaseEpoch > targetHoldEpoch
}

func isDeterministicRollbackFencePostExpiryLateMarkerReleaseWindowTransition(
	epoch int64,
	sourceDigest string,
	targetDigest string,
) bool {
	if epoch < 0 {
		return false
	}
	sourceNormalized := normalizePolicyManifestDigest(sourceDigest)
	targetNormalized := normalizePolicyManifestDigest(targetDigest)
	if !isRollbackFencePostExpiryLateMarkerReleaseDigest(epoch, sourceNormalized) {
		return false
	}
	if !isRollbackFencePostExpiryLateMarkerReleaseDigest(epoch, targetNormalized) {
		return false
	}
	sourceFromSeq, sourceToSeq, sourceForwardSeq, ok := parseRollbackLineage(sourceNormalized)
	if !ok {
		return false
	}
	targetFromSeq, targetToSeq, targetForwardSeq, ok := parseRollbackLineage(targetNormalized)
	if !ok {
		return false
	}
	if sourceFromSeq != targetFromSeq || sourceToSeq != targetToSeq || sourceForwardSeq != targetForwardSeq {
		return false
	}
	if sourceToSeq != epoch || targetToSeq != epoch {
		return false
	}
	if sourceFromSeq <= sourceToSeq || targetFromSeq <= targetToSeq {
		return false
	}
	if sourceForwardSeq < sourceFromSeq || targetForwardSeq < targetFromSeq {
		return false
	}
	sourceExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(sourceNormalized)
	if !ok {
		return false
	}
	targetExpiryEpoch, ok := parseRollbackFenceTombstoneExpiryEpoch(targetNormalized)
	if !ok {
		return false
	}
	if sourceExpiryEpoch != targetExpiryEpoch {
		return false
	}
	sourceHoldEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(sourceNormalized)
	if !ok {
		return false
	}
	targetHoldEpoch, ok := parseRollbackFenceLateMarkerHoldEpoch(targetNormalized)
	if !ok {
		return false
	}
	if sourceHoldEpoch != targetHoldEpoch {
		return false
	}
	sourceOwnership, ok := parseRollbackFenceOwnershipOrdering(epoch, sourceNormalized)
	if !ok {
		return false
	}
	targetOwnership, ok := parseRollbackFenceOwnershipOrdering(epoch, targetNormalized)
	if !ok {
		return false
	}
	_, sourceHasReintegrationSealDriftReanchorBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch(sourceNormalized)
	_, targetHasReintegrationSealDriftReanchorBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorEpoch(targetNormalized)
	if !sourceHasReintegrationSealDriftReanchorBoundaryEpoch && targetHasReintegrationSealDriftReanchorBoundaryEpoch {
		// Once reintegration-seal-drift-reanchor ownership is verified for a
		// lineage, delayed reintegration-seal-drift echoes without explicit
		// reanchor ownership must not reclaim control even if drift epochs are
		// numerically larger.
		return true
	}
	if sourceHasReintegrationSealDriftReanchorBoundaryEpoch && !targetHasReintegrationSealDriftReanchorBoundaryEpoch {
		// Reject stage-regression from verified reintegration-seal-drift-reanchor
		// ownership back to reintegration-seal-drift-only ownership.
		return false
	}
	_, sourceHasReintegrationSealDriftReanchorCompactionBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch(sourceNormalized)
	_, targetHasReintegrationSealDriftReanchorCompactionBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionEpoch(targetNormalized)
	if !sourceHasReintegrationSealDriftReanchorCompactionBoundaryEpoch && targetHasReintegrationSealDriftReanchorCompactionBoundaryEpoch {
		// Once reintegration-seal-drift-reanchor-lineage-compaction ownership is
		// verified for a lineage, explicit compaction ownership progression must
		// win deterministically over prior reanchor-only ownership.
		return true
	}
	if sourceHasReintegrationSealDriftReanchorCompactionBoundaryEpoch && !targetHasReintegrationSealDriftReanchorCompactionBoundaryEpoch {
		// Reject stage-regression from verified reintegration-seal-drift-reanchor
		// lineage-compaction ownership back to reanchor-only ownership.
		return false
	}
	_, sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(sourceNormalized)
	_, targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineEpoch(targetNormalized)
	if !sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch && targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch {
		// Once reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry
		// ownership is verified for a lineage, explicit late-resurrection-quarantine
		// progression must win deterministically over prior marker-expiry ownership.
		return true
	}
	if sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch && !targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineBoundaryEpoch {
		// Reject stage-regression from verified reintegration-seal-drift-reanchor
		// lineage-compaction marker-expiry late-resurrection-quarantine ownership
		// back to marker-expiry-only ownership.
		return false
	}
	_, sourceHasReintegrationBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(sourceNormalized)
	_, targetHasReintegrationBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(targetNormalized)
	if !sourceHasReintegrationBoundaryEpoch && targetHasReintegrationBoundaryEpoch {
		// Once late-resurrection-quarantine reintegration ownership is verified for
		// a lineage, explicit reintegration progression must win deterministically
		// over pre-reintegration quarantine ownership.
		return true
	}
	if sourceHasReintegrationBoundaryEpoch && !targetHasReintegrationBoundaryEpoch {
		// Reject stage-regression from verified reintegration ownership back to
		// late-resurrection-quarantine ownership.
		return false
	}
	_, sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(sourceNormalized)
	_, targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch := parseRollbackFenceResurrectionReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationEpoch(targetNormalized)
	if !sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch && targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch {
		// Once reintegration-seal-drift-reanchor-lineage-compaction-marker-expiry
		// late-resurrection-quarantine-reintegration ownership is verified for a
		// lineage, explicit reintegration progression must win deterministically
		// over pre-reintegration late-resurrection-quarantine ownership.
		return true
	}
	if sourceHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch && !targetHasReintegrationSealDriftReanchorCompactionExpiryQuarantineReintegrationBoundaryEpoch {
		// Reject stage-regression from verified reintegration ownership back to
		// late-resurrection-quarantine ownership for the same lineage.
		return false
	}
	// Ownership progression is strictly monotonic under explicit
	// (epoch, bridge_sequence, drain_watermark, live_head,
	// steady_state_watermark, steady_generation, generation_retention_floor,
	// floor_lift_epoch, settle_window_epoch, spillover_epoch,
	// spillover_rejoin_epoch, rejoin_seal_epoch, seal_drift_epoch,
	// drift_reanchor_epoch, reanchor_compaction_epoch,
	// compaction_expiry_epoch, resurrection_quarantine_epoch,
	// resurrection_reintegration_epoch, resurrection_reintegration_seal_epoch,
	// resurrection_reintegration_seal_drift_epoch,
	// resurrection_reintegration_seal_drift_reanchor_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_expiry_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_epoch,
	// resurrection_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_seal_drift_reanchor_compaction_expiry_quarantine_reintegration_epoch)
	// ordering.
	return compareRollbackFenceOwnershipOrdering(sourceOwnership, targetOwnership) < 0
}

func isDeterministicRollbackFencePostReleaseWindowEpochRolloverStaleTransition(
	previousEpoch int64,
	incomingEpoch int64,
	previousDigest string,
	incomingDigest string,
) bool {
	if previousEpoch < 0 {
		return false
	}
	if incomingEpoch != previousEpoch+1 {
		return false
	}
	previousNormalized := normalizePolicyManifestDigest(previousDigest)
	if !isRollbackFencePostExpiryLateMarkerReleaseDigest(previousEpoch, previousNormalized) {
		return false
	}
	// Once post-release-window ownership is verified, any digest still anchored
	// to the prior rollback-fence epoch is stale at rollover.
	return isRollbackFenceDigestAnchoredToEpoch(previousEpoch, incomingDigest)
}

func isRollbackFenceDigestAnchoredToEpoch(epoch int64, digest string) bool {
	if epoch < 0 {
		return false
	}
	normalized := normalizePolicyManifestDigest(digest)
	rollbackFromSeq, rollbackToSeq, rollbackForwardSeq, ok := parseRollbackLineage(normalized)
	if !ok {
		return false
	}
	if rollbackToSeq != epoch {
		return false
	}
	if rollbackFromSeq <= rollbackToSeq {
		return false
	}
	if rollbackForwardSeq < rollbackFromSeq {
		return false
	}
	return true
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

func isDeterministicRollbackLineage(previousEpoch, incomingEpoch int64, incomingDigest string) bool {
	if incomingEpoch >= previousEpoch {
		return false
	}
	rollbackFromSeq, rollbackToSeq, rollbackForwardSeq, ok := parseRollbackLineage(incomingDigest)
	if !ok {
		return false
	}
	if rollbackFromSeq != previousEpoch {
		return false
	}
	if rollbackToSeq != incomingEpoch {
		return false
	}
	if rollbackFromSeq <= rollbackToSeq {
		return false
	}
	if rollbackForwardSeq < rollbackFromSeq {
		return false
	}
	return true
}

func parseRollbackLineage(digest string) (int64, int64, int64, bool) {
	const fromKey = "rollback-from-seq="
	const toKey = "rollback-to-seq="
	const forwardKey = "rollback-forward-seq="

	var (
		fromValue    string
		toValue      string
		forwardValue string
	)
	for _, rawToken := range strings.Split(digest, "|") {
		token := strings.TrimSpace(rawToken)
		switch {
		case strings.HasPrefix(token, fromKey):
			fromValue = strings.TrimSpace(strings.TrimPrefix(token, fromKey))
		case strings.HasPrefix(token, toKey):
			toValue = strings.TrimSpace(strings.TrimPrefix(token, toKey))
		case strings.HasPrefix(token, forwardKey):
			forwardValue = strings.TrimSpace(strings.TrimPrefix(token, forwardKey))
		}
	}
	if fromValue == "" || toValue == "" || forwardValue == "" {
		return 0, 0, 0, false
	}
	fromSeq, err := strconv.ParseInt(fromValue, 10, 64)
	if err != nil || fromSeq < 0 {
		return 0, 0, 0, false
	}
	toSeq, err := strconv.ParseInt(toValue, 10, 64)
	if err != nil || toSeq < 0 {
		return 0, 0, 0, false
	}
	forwardSeq, err := strconv.ParseInt(forwardValue, 10, 64)
	if err != nil || forwardSeq < 0 {
		return 0, 0, 0, false
	}
	return fromSeq, toSeq, forwardSeq, true
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
