package coordinator

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
	LagSequence   int64
	QueueDepth    int
	QueueCapacity int
	Signal        string
	Decision      string
	BatchBefore   int
	BatchAfter    int
	Streak        int
	Cooldown      int
}

type autoTuneSignal string

const (
	autoTuneSignalHold     autoTuneSignal = "hold"
	autoTuneSignalIncrease autoTuneSignal = "increase"
	autoTuneSignalDecrease autoTuneSignal = "decrease"
)

type autoTuneController struct {
	minBatchSize int
	maxBatchSize int
	stepUp       int
	stepDown     int

	lagHighWatermark int64
	lagLowWatermark  int64

	queueHighWatermarkPct int
	queueLowWatermarkPct  int

	hysteresisTicks  int
	cooldownTicks    int
	cooldownLeft     int
	currentBatch     int
	lastSignal       autoTuneSignal
	lastApplied      autoTuneSignal
	saturationSignal autoTuneSignal
	streak           int
}

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

	return &autoTuneController{
		minBatchSize:          minBatch,
		maxBatchSize:          maxBatch,
		stepUp:                stepUp,
		stepDown:              stepDown,
		lagHighWatermark:      lagHigh,
		lagLowWatermark:       lagLow,
		queueHighWatermarkPct: queueHigh,
		queueLowWatermarkPct:  queueLow,
		hysteresisTicks:       hysteresisTicks,
		cooldownTicks:         cooldownTicks,
		currentBatch:          startBatch,
		lastSignal:            autoTuneSignalHold,
		lastApplied:           autoTuneSignalHold,
		saturationSignal:      autoTuneSignalHold,
	}
}

func (a *autoTuneController) Resolve(inputs autoTuneInputs) (int, autoTuneDiagnostics) {
	lagSequence := int64(0)
	if inputs.HasHeadSignal && inputs.HasMinCursorSignal {
		lagSequence = inputs.HeadSequence - inputs.MinCursorSequence
		if lagSequence < 0 {
			lagSequence = 0
		}
	}

	signal := a.classifySignal(inputs, lagSequence)
	decision := "hold"
	before := a.currentBatch
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

	return a.currentBatch, autoTuneDiagnostics{
		LagSequence:   lagSequence,
		QueueDepth:    inputs.QueueDepth,
		QueueCapacity: inputs.QueueCapacity,
		Signal:        string(signal),
		Decision:      decision,
		BatchBefore:   before,
		BatchAfter:    a.currentBatch,
		Streak:        a.streak,
		Cooldown:      a.cooldownLeft,
	}
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
