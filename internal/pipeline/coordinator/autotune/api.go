package autotune

// Config is the public auto-tune configuration contract.
type Config = AutoTuneConfig

// Inputs is the public signal input contract for one resolve tick.
type Inputs = autoTuneInputs

// Diagnostics is the public decision trace emitted for each resolve tick.
type Diagnostics = autoTuneDiagnostics

// OverrideTransition carries override boundary state across restarts/transitions.
type OverrideTransition = autoTuneOverrideTransition

// PolicyTransition carries policy boundary state across restarts/transitions.
type PolicyTransition = autoTunePolicyTransition

// Snapshot exposes controller internals required by coordinator observability.
type Snapshot struct {
	CurrentBatch           int
	MinBatchSize           int
	MaxBatchSize           int
	StepUp                 int
	StepDown               int
	LagHighWatermark       int64
	LagLowWatermark        int64
	QueueHighWatermarkPct  int
	QueueLowWatermarkPct   int
	HysteresisTicks        int
	CooldownTicks          int
	TelemetryStaleTicks    int
	TelemetryRecoveryTicks int
	OperatorOverrideBatch  int
	OperatorReleaseHold    int
	OverrideReleaseLeft    int
	PolicyVersion          string
	PolicyManifestDigest   string
	PolicyEpoch            int64
	PolicyActivationHold   int
	PolicyActivationLeft   int
}

// Controller is the public auto-tune controller entrypoint.
type Controller struct {
	controller *autoTuneController
}

func New(baseBatchSize int, cfg Config) *Controller {
	return wrapController(newAutoTuneController(baseBatchSize, cfg))
}

func NewWithSeed(baseBatchSize int, cfg Config, seedBatch *int) *Controller {
	return wrapController(newAutoTuneControllerWithSeed(baseBatchSize, cfg, seedBatch))
}

func NewWithRestartSeed(baseBatchSize int, cfg Config, seedBatch *int) *Controller {
	return wrapController(newAutoTuneControllerWithRestartSeed(baseBatchSize, cfg, seedBatch))
}

func wrapController(controller *autoTuneController) *Controller {
	if controller == nil {
		return nil
	}
	return &Controller{controller: controller}
}

func (c *Controller) Resolve(inputs Inputs) (int, Diagnostics) {
	if c == nil || c.controller == nil {
		return 0, Diagnostics{}
	}
	return c.controller.Resolve(inputs)
}

func (c *Controller) ExportOverrideTransition() OverrideTransition {
	if c == nil || c.controller == nil {
		return OverrideTransition{}
	}
	return c.controller.exportOverrideTransition()
}

func (c *Controller) ExportPolicyTransition() PolicyTransition {
	if c == nil || c.controller == nil {
		return PolicyTransition{}
	}
	return c.controller.exportPolicyTransition()
}

func (c *Controller) ReconcileOverrideTransition(transition OverrideTransition) {
	if c == nil || c.controller == nil {
		return
	}
	c.controller.reconcileOverrideTransition(transition)
}

func (c *Controller) ReconcilePolicyTransition(transition PolicyTransition) {
	if c == nil || c.controller == nil {
		return
	}
	c.controller.reconcilePolicyTransition(transition)
}

func (c *Controller) CurrentBatch() int {
	if c == nil || c.controller == nil {
		return 0
	}
	return c.controller.currentBatch
}

func (c *Controller) Snapshot() Snapshot {
	if c == nil || c.controller == nil {
		return Snapshot{}
	}
	controller := c.controller
	return Snapshot{
		CurrentBatch:           controller.currentBatch,
		MinBatchSize:           controller.minBatchSize,
		MaxBatchSize:           controller.maxBatchSize,
		StepUp:                 controller.stepUp,
		StepDown:               controller.stepDown,
		LagHighWatermark:       controller.lagHighWatermark,
		LagLowWatermark:        controller.lagLowWatermark,
		QueueHighWatermarkPct:  controller.queueHighWatermarkPct,
		QueueLowWatermarkPct:   controller.queueLowWatermarkPct,
		HysteresisTicks:        controller.hysteresisTicks,
		CooldownTicks:          controller.cooldownTicks,
		TelemetryStaleTicks:    controller.telemetryStaleTicks,
		TelemetryRecoveryTicks: controller.telemetryRecoveryTicks,
		OperatorOverrideBatch:  controller.operatorOverrideBatch,
		OperatorReleaseHold:    controller.operatorReleaseHold,
		OverrideReleaseLeft:    controller.overrideReleaseLeft,
		PolicyVersion:          controller.policyVersion,
		PolicyManifestDigest:   controller.policyManifestDigest,
		PolicyEpoch:            controller.policyEpoch,
		PolicyActivationHold:   controller.policyActivationHold,
		PolicyActivationLeft:   controller.policyActivationLeft,
	}
}
