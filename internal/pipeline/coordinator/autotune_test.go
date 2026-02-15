package coordinator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoTuneController_BoundedStepAndHysteresis(t *testing.T) {
	controller := newAutoTuneController(80, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          40,
		MaxBatchSize:          120,
		StepUp:                20,
		StepDown:              15,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 80,
		QueueLowWatermarkPct:  30,
		HysteresisTicks:       2,
		CooldownTicks:         1,
	})
	require.NotNil(t, controller)

	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	batch, d1 := controller.Resolve(highLag)
	assert.Equal(t, 80, batch)
	assert.Equal(t, "defer_hysteresis", d1.Decision)

	batch, d2 := controller.Resolve(highLag)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "apply_increase", d2.Decision)

	batch, _ = controller.Resolve(highLag)
	assert.Equal(t, 100, batch)
	batch, d4 := controller.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "apply_increase", d4.Decision)

	highQueue := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  900,
		QueueDepth:         9,
		QueueCapacity:      10,
	}

	batch, _ = controller.Resolve(highQueue)
	assert.Equal(t, 120, batch)
	batch, d6 := controller.Resolve(highQueue)
	assert.Equal(t, 105, batch)
	assert.Equal(t, "apply_decrease", d6.Decision)
	batch, d7 := controller.Resolve(highQueue)
	assert.Equal(t, 105, batch)
	assert.Equal(t, "defer_hysteresis", d7.Decision)
}

func TestAutoTuneController_QueuePressureWorksWithoutHeadSignal(t *testing.T) {
	controller := newAutoTuneController(50, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          20,
		MaxBatchSize:          80,
		StepUp:                10,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 80,
		QueueLowWatermarkPct:  30,
		HysteresisTicks:       1,
		CooldownTicks:         1,
	})
	require.NotNil(t, controller)

	batch, d1 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      false,
		HasMinCursorSignal: false,
		QueueDepth:         8,
		QueueCapacity:      10,
	})
	assert.Equal(t, 40, batch)
	assert.Equal(t, "decrease", d1.Signal)
	assert.Equal(t, "apply_decrease", d1.Decision)

	batch, d2 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      false,
		HasMinCursorSignal: false,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 40, batch)
	assert.Equal(t, "hold", d2.Signal)
}

func TestAutoTuneController_CooldownDefersOppositeSignalJitter(t *testing.T) {
	controller := newAutoTuneController(80, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          40,
		MaxBatchSize:          160,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
		CooldownTicks:         2,
	})
	require.NotNil(t, controller)

	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}
	lowLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       115,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	batch, d1 := controller.Resolve(highLag)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "apply_increase", d1.Decision)

	batch, d2 := controller.Resolve(lowLag)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "defer_cooldown", d2.Decision)

	batch, d3 := controller.Resolve(lowLag)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "defer_cooldown", d3.Decision)

	batch, d4 := controller.Resolve(lowLag)
	assert.Equal(t, 90, batch)
	assert.Equal(t, "apply_decrease", d4.Decision)
}

func TestAutoTuneController_CooldownPreservesOppositeStreakForDeterministicRecovery(t *testing.T) {
	controller := newAutoTuneController(80, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          40,
		MaxBatchSize:          160,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       3,
		CooldownTicks:         2,
	})
	require.NotNil(t, controller)

	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}
	lowLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       115,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	batch, d1 := controller.Resolve(highLag)
	assert.Equal(t, 80, batch)
	assert.Equal(t, "defer_hysteresis", d1.Decision)

	batch, d2 := controller.Resolve(highLag)
	assert.Equal(t, 80, batch)
	assert.Equal(t, "defer_hysteresis", d2.Decision)

	batch, d3 := controller.Resolve(highLag)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "apply_increase", d3.Decision)

	batch, d4 := controller.Resolve(lowLag)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "defer_cooldown", d4.Decision)

	batch, d5 := controller.Resolve(lowLag)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "defer_cooldown", d5.Decision)

	batch, d6 := controller.Resolve(lowLag)
	assert.Equal(t, 90, batch)
	assert.Equal(t, "apply_decrease", d6.Decision, "opposite-pressure streak observed during cooldown should deterministically recover immediately at cooldown release")
}

func TestAutoTuneController_CooldownAllowsSustainedSameDirectionPressure(t *testing.T) {
	controller := newAutoTuneController(80, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          40,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
		CooldownTicks:         2,
	})
	require.NotNil(t, controller)

	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	batch, d1 := controller.Resolve(highLag)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "apply_increase", d1.Decision)

	batch, d2 := controller.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "apply_increase", d2.Decision)
}

func TestAutoTuneController_ClampedBoundaryDoesNotRefreshCooldown(t *testing.T) {
	controller := newAutoTuneController(60, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          40,
		MaxBatchSize:          100,
		StepUp:                20,
		StepDown:              20,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
		CooldownTicks:         2,
	})
	require.NotNil(t, controller)

	saturation := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       120,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         9,
		QueueCapacity:      10,
	}
	recovery := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       320,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	batch, d1 := controller.Resolve(saturation)
	assert.Equal(t, 40, batch)
	assert.Equal(t, "apply_decrease", d1.Decision)

	batch, d2 := controller.Resolve(saturation)
	assert.Equal(t, 40, batch)
	assert.Equal(t, "clamped_decrease", d2.Decision)
	assert.Equal(t, 1, d2.Cooldown)

	batch, d3 := controller.Resolve(saturation)
	assert.Equal(t, 40, batch)
	assert.Equal(t, "clamped_decrease", d3.Decision)
	assert.Equal(t, 0, d3.Cooldown)

	batch, d4 := controller.Resolve(recovery)
	assert.Equal(t, 60, batch)
	assert.Equal(t, "apply_increase", d4.Decision)
}

func TestAutoTuneController_SustainedSaturationBypassesHysteresisPhaseOscillation(t *testing.T) {
	controller := newAutoTuneController(80, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          40,
		MaxBatchSize:          100,
		StepUp:                20,
		StepDown:              20,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       2,
		CooldownTicks:         2,
	})
	require.NotNil(t, controller)

	saturation := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       320,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	batch, d1 := controller.Resolve(saturation)
	assert.Equal(t, 80, batch)
	assert.Equal(t, "defer_hysteresis", d1.Decision)

	batch, d2 := controller.Resolve(saturation)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "apply_increase", d2.Decision)

	batch, d3 := controller.Resolve(saturation)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "clamped_increase", d3.Decision)
	assert.Equal(t, 1, d3.Cooldown)

	batch, d4 := controller.Resolve(saturation)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "clamped_increase", d4.Decision)
	assert.Equal(t, 0, d4.Cooldown)

	batch, d5 := controller.Resolve(saturation)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "clamped_increase", d5.Decision)
	assert.Equal(t, 0, d5.Cooldown)
}

func TestAutoTuneController_ProfileTransitionSeedsCurrentBatch(t *testing.T) {
	ramp := newAutoTuneController(80, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          40,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	})
	require.NotNil(t, ramp)

	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}
	batch, _ := ramp.Resolve(highLag)
	assert.Equal(t, 100, batch)
	batch, _ = ramp.Resolve(highLag)
	assert.Equal(t, 120, batch)

	seed := ramp.currentBatch
	transitioned := newAutoTuneControllerWithSeed(80, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          40,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      5_000,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}, &seed)
	require.NotNil(t, transitioned)
	assert.Equal(t, 120, transitioned.currentBatch)

	batch, diagnostics := transitioned.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "hold", diagnostics.Signal)
	assert.Equal(t, "hold", diagnostics.Decision)
}

func TestAutoTuneController_ProfileTransitionClampsSeedToBounds(t *testing.T) {
	seed := 170
	transitioned := newAutoTuneControllerWithSeed(80, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          140,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}, &seed)
	require.NotNil(t, transitioned)
	assert.Equal(t, 140, transitioned.currentBatch)
}

func TestAutoTuneController_WarmStartAdoptsBoundedDeltaFromBaseline(t *testing.T) {
	seed := 170
	warmStarted := newAutoTuneControllerWithRestartSeed(80, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}, &seed)
	require.NotNil(t, warmStarted)
	assert.Equal(t, 100, warmStarted.currentBatch, "warm-start adoption must be bounded to one upward control step from baseline")

	lowSeed := 20
	warmStarted = newAutoTuneControllerWithRestartSeed(120, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              15,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
	}, &lowSeed)
	require.NotNil(t, warmStarted)
	assert.Equal(t, 105, warmStarted.currentBatch, "warm-start adoption must be bounded to one downward control step from baseline")
}

func TestAutoTuneController_TelemetryStalenessFallbackHoldsThenDeterministicallyRecovers(t *testing.T) {
	controller := newAutoTuneController(100, AutoTuneConfig{
		Enabled:                true,
		MinBatchSize:           60,
		MaxBatchSize:           200,
		StepUp:                 20,
		StepDown:               10,
		LagHighWatermark:       80,
		LagLowWatermark:        20,
		QueueHighWatermarkPct:  90,
		QueueLowWatermarkPct:   10,
		HysteresisTicks:        1,
		CooldownTicks:          1,
		TelemetryStaleTicks:    2,
		TelemetryRecoveryTicks: 2,
	})
	require.NotNil(t, controller)

	batch, d1 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       260,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 120, batch)
	assert.Equal(t, "apply_increase", d1.Decision)
	assert.Equal(t, "healthy", d1.TelemetryState)

	batch, d2 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       95,
		HasMinCursorSignal: true,
		MinCursorSequence:  101,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 120, batch)
	assert.Equal(t, "defer_cooldown", d2.Decision)
	assert.Equal(t, "healthy", d2.TelemetryState)

	batch, d3 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       95,
		HasMinCursorSignal: true,
		MinCursorSequence:  102,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 120, batch)
	assert.Equal(t, "hold_telemetry_stale", d3.Decision)
	assert.Equal(t, "stale_fallback", d3.TelemetryState)

	batch, d4 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       95,
		HasMinCursorSignal: true,
		MinCursorSequence:  103,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 120, batch)
	assert.Equal(t, "hold_telemetry_stale", d4.Decision)
	assert.Equal(t, "stale_fallback", d4.TelemetryState)

	batch, d5 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       300,
		HasMinCursorSignal: true,
		MinCursorSequence:  104,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 120, batch)
	assert.Equal(t, "hold_telemetry_recovery", d5.Decision)
	assert.Equal(t, "recovery_hold", d5.TelemetryState)

	batch, d6 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       301,
		HasMinCursorSignal: true,
		MinCursorSequence:  105,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 120, batch)
	assert.Equal(t, "hold_telemetry_recovery", d6.Decision)
	assert.Equal(t, "recovery_hold", d6.TelemetryState)

	batch, d7 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       320,
		HasMinCursorSignal: true,
		MinCursorSequence:  106,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 140, batch)
	assert.Equal(t, "apply_increase", d7.Decision)
	assert.Equal(t, "healthy", d7.TelemetryState)
}

func TestAutoTuneController_MissingTelemetryAfterBaselineTriggersFallback(t *testing.T) {
	controller := newAutoTuneController(100, AutoTuneConfig{
		Enabled:                true,
		MinBatchSize:           60,
		MaxBatchSize:           200,
		StepUp:                 20,
		StepDown:               10,
		LagHighWatermark:       80,
		LagLowWatermark:        20,
		QueueHighWatermarkPct:  90,
		QueueLowWatermarkPct:   10,
		HysteresisTicks:        1,
		CooldownTicks:          1,
		TelemetryStaleTicks:    2,
		TelemetryRecoveryTicks: 1,
	})
	require.NotNil(t, controller)

	batch, d1 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       260,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 120, batch)
	assert.Equal(t, "apply_increase", d1.Decision)

	batch, d2 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      false,
		HasMinCursorSignal: false,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 120, batch)
	assert.Equal(t, "hold", d2.Decision)
	assert.Equal(t, "healthy", d2.TelemetryState)

	batch, d3 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      false,
		HasMinCursorSignal: false,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 120, batch)
	assert.Equal(t, "hold_telemetry_stale", d3.Decision)
	assert.Equal(t, "stale_fallback", d3.TelemetryState)

	batch, d4 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       320,
		HasMinCursorSignal: true,
		MinCursorSequence:  101,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 120, batch)
	assert.Equal(t, "hold_telemetry_recovery", d4.Decision)
	assert.Equal(t, "recovery_hold", d4.TelemetryState)

	batch, d5 := controller.Resolve(autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       321,
		HasMinCursorSignal: true,
		MinCursorSequence:  102,
		QueueDepth:         0,
		QueueCapacity:      10,
	})
	assert.Equal(t, 140, batch)
	assert.Equal(t, "apply_increase", d5.Decision)
	assert.Equal(t, "healthy", d5.TelemetryState)
}

func TestAutoTuneController_OperatorOverridePinsBatchAndReleasesDeterministically(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	autoController := newAutoTuneController(100, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          60,
		MaxBatchSize:          200,
		StepUp:                20,
		StepDown:              10,
		LagHighWatermark:      80,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 90,
		QueueLowWatermarkPct:  10,
		HysteresisTicks:       1,
		CooldownTicks:         1,
	})
	require.NotNil(t, autoController)

	batch, d1 := autoController.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "apply_increase", d1.Decision)

	batch, d2 := autoController.Resolve(highLag)
	assert.Equal(t, 140, batch)
	assert.Equal(t, "apply_increase", d2.Decision)

	seed := autoController.currentBatch
	manualController := newAutoTuneControllerWithSeed(100, AutoTuneConfig{
		Enabled:                   true,
		MinBatchSize:              60,
		MaxBatchSize:              200,
		StepUp:                    20,
		StepDown:                  10,
		LagHighWatermark:          80,
		LagLowWatermark:           20,
		QueueHighWatermarkPct:     90,
		QueueLowWatermarkPct:      10,
		HysteresisTicks:           1,
		CooldownTicks:             1,
		OperatorOverrideBatchSize: 70,
		OperatorReleaseHoldTicks:  2,
	}, &seed)
	require.NotNil(t, manualController)
	manualController.reconcileOverrideTransition(autoController.exportOverrideTransition())

	batch, d3 := manualController.Resolve(highLag)
	assert.Equal(t, 70, batch)
	assert.Equal(t, "hold_operator_override", d3.Decision)
	assert.Equal(t, "manual_hold", d3.OverrideState)

	batch, d4 := manualController.Resolve(highLag)
	assert.Equal(t, 70, batch)
	assert.Equal(t, "hold_operator_override", d4.Decision)

	releaseSeed := manualController.currentBatch
	releaseController := newAutoTuneControllerWithSeed(100, AutoTuneConfig{
		Enabled:                  true,
		MinBatchSize:             60,
		MaxBatchSize:             200,
		StepUp:                   20,
		StepDown:                 10,
		LagHighWatermark:         80,
		LagLowWatermark:          20,
		QueueHighWatermarkPct:    90,
		QueueLowWatermarkPct:     10,
		HysteresisTicks:          1,
		CooldownTicks:            1,
		OperatorReleaseHoldTicks: 2,
	}, &releaseSeed)
	require.NotNil(t, releaseController)
	releaseController.reconcileOverrideTransition(manualController.exportOverrideTransition())

	batch, d5 := releaseController.Resolve(highLag)
	assert.Equal(t, 70, batch)
	assert.Equal(t, "hold_operator_release", d5.Decision)
	assert.Equal(t, "release_hold", d5.OverrideState)
	assert.Equal(t, 2, d5.OverrideReleaseTicks)

	batch, d6 := releaseController.Resolve(highLag)
	assert.Equal(t, 70, batch)
	assert.Equal(t, "hold_operator_release", d6.Decision)
	assert.Equal(t, "release_hold", d6.OverrideState)
	assert.Equal(t, 1, d6.OverrideReleaseTicks)

	batch, d7 := releaseController.Resolve(highLag)
	assert.Equal(t, 90, batch)
	assert.Equal(t, "apply_increase", d7.Decision)
	assert.Equal(t, "auto", d7.OverrideState)
}

func TestAutoTuneController_OperatorReleaseHoldStatePersistsAcrossWarmRestartSeed(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	controller := newAutoTuneControllerWithRestartSeed(100, AutoTuneConfig{
		Enabled:                  true,
		MinBatchSize:             60,
		MaxBatchSize:             200,
		StepUp:                   20,
		StepDown:                 10,
		LagHighWatermark:         80,
		LagLowWatermark:          20,
		QueueHighWatermarkPct:    90,
		QueueLowWatermarkPct:     10,
		HysteresisTicks:          1,
		CooldownTicks:            1,
		OperatorReleaseHoldTicks: 3,
	}, intPtr(90))
	require.NotNil(t, controller)
	controller.reconcileOverrideTransition(autoTuneOverrideTransition{
		WasManualOverride:    false,
		ReleaseHoldRemaining: 2,
	})

	batch, d1 := controller.Resolve(highLag)
	assert.Equal(t, 90, batch)
	assert.Equal(t, "hold_operator_release", d1.Decision)
	assert.Equal(t, 2, d1.OverrideReleaseTicks)

	batch, d2 := controller.Resolve(highLag)
	assert.Equal(t, 90, batch)
	assert.Equal(t, "hold_operator_release", d2.Decision)
	assert.Equal(t, 1, d2.OverrideReleaseTicks)

	batch, d3 := controller.Resolve(highLag)
	assert.Equal(t, 110, batch)
	assert.Equal(t, "apply_increase", d3.Decision)
}

func TestAutoTuneController_PolicyVersionTransitionAppliesDeterministicActivationFence(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	baseCfg := AutoTuneConfig{
		Enabled:                   true,
		MinBatchSize:              60,
		MaxBatchSize:              260,
		StepUp:                    20,
		StepDown:                  10,
		LagHighWatermark:          80,
		LagLowWatermark:           20,
		QueueHighWatermarkPct:     90,
		QueueLowWatermarkPct:      10,
		HysteresisTicks:           1,
		CooldownTicks:             1,
		PolicyVersion:             "policy-v1",
		PolicyActivationHoldTicks: 2,
	}
	v1Controller := newAutoTuneController(100, baseCfg)
	require.NotNil(t, v1Controller)

	batch, d1 := v1Controller.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "apply_increase", d1.Decision)

	v2Cfg := baseCfg
	v2Cfg.PolicyVersion = "policy-v2"
	seed := v1Controller.currentBatch
	v2Controller := newAutoTuneControllerWithSeed(100, v2Cfg, &seed)
	require.NotNil(t, v2Controller)
	v2Controller.reconcilePolicyTransition(v1Controller.exportPolicyTransition())

	batch, d2 := v2Controller.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "hold_policy_transition", d2.Decision)
	assert.Equal(t, "policy-v2", d2.PolicyVersion)
	assert.Equal(t, int64(1), d2.PolicyEpoch)
	assert.Equal(t, 2, d2.PolicyActivationTicks)

	batch, d3 := v2Controller.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "hold_policy_transition", d3.Decision)
	assert.Equal(t, int64(1), d3.PolicyEpoch)
	assert.Equal(t, 1, d3.PolicyActivationTicks)

	batch, d4 := v2Controller.Resolve(highLag)
	assert.Equal(t, 140, batch)
	assert.Equal(t, "apply_increase", d4.Decision)
	assert.Equal(t, int64(1), d4.PolicyEpoch)

	rollbackCfg := baseCfg
	rollbackSeed := v2Controller.currentBatch
	rollbackController := newAutoTuneControllerWithSeed(100, rollbackCfg, &rollbackSeed)
	require.NotNil(t, rollbackController)
	rollbackController.reconcilePolicyTransition(v2Controller.exportPolicyTransition())

	batch, rollbackDecision := rollbackController.Resolve(highLag)
	assert.Equal(t, rollbackSeed, batch)
	assert.Equal(t, "hold_policy_transition", rollbackDecision.Decision)
	assert.Equal(t, "policy-v1", rollbackDecision.PolicyVersion)
	assert.Equal(t, int64(2), rollbackDecision.PolicyEpoch)
	assert.Equal(t, 2, rollbackDecision.PolicyActivationTicks)
}

func TestAutoTuneController_PolicyManifestRefreshRejectsStaleAndDigestReapplyIsDeterministic(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	baseCfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               260,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	baseController := newAutoTuneController(100, baseCfg)
	require.NotNil(t, baseController)

	batch, baselineDecision := baseController.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "apply_increase", baselineDecision.Decision)
	assert.Equal(t, "manifest-v2a", baselineDecision.PolicyManifestDigest)
	assert.Equal(t, int64(1), baselineDecision.PolicyEpoch)

	refreshCfg := baseCfg
	refreshCfg.PolicyManifestDigest = "manifest-v2b"
	refreshCfg.PolicyManifestRefreshEpoch = 2
	refreshSeed := baseController.currentBatch
	refreshController := newAutoTuneControllerWithSeed(100, refreshCfg, &refreshSeed)
	require.NotNil(t, refreshController)
	refreshController.reconcilePolicyTransition(baseController.exportPolicyTransition())

	batch, refreshDecision := refreshController.Resolve(highLag)
	assert.Equal(t, refreshSeed, batch)
	assert.Equal(t, "hold_policy_transition", refreshDecision.Decision)
	assert.Equal(t, "manifest-v2b", refreshDecision.PolicyManifestDigest)
	assert.Equal(t, int64(2), refreshDecision.PolicyEpoch)
	assert.Equal(t, 1, refreshDecision.PolicyActivationTicks)

	batch, refreshApplied := refreshController.Resolve(highLag)
	assert.Equal(t, 140, batch)
	assert.Equal(t, "apply_increase", refreshApplied.Decision)
	assert.Equal(t, "manifest-v2b", refreshApplied.PolicyManifestDigest)
	assert.Equal(t, int64(2), refreshApplied.PolicyEpoch)

	staleCfg := baseCfg
	staleSeed := refreshController.currentBatch
	staleController := newAutoTuneControllerWithSeed(100, staleCfg, &staleSeed)
	require.NotNil(t, staleController)
	staleController.reconcilePolicyTransition(refreshController.exportPolicyTransition())

	batch, staleDecision := staleController.Resolve(highLag)
	assert.Equal(t, 160, batch)
	assert.Equal(t, "apply_increase", staleDecision.Decision, "stale refresh must not re-open policy activation hold")
	assert.Equal(t, "manifest-v2b", staleDecision.PolicyManifestDigest, "stale refresh must pin previously verified digest")
	assert.Equal(t, int64(2), staleDecision.PolicyEpoch, "stale refresh must preserve previously verified lineage epoch")
	assert.Equal(t, 0, staleDecision.PolicyActivationTicks)

	reapplyCfg := refreshCfg
	reapplySeed := staleController.currentBatch
	reapplyController := newAutoTuneControllerWithSeed(100, reapplyCfg, &reapplySeed)
	require.NotNil(t, reapplyController)
	reapplyController.reconcilePolicyTransition(staleController.exportPolicyTransition())

	batch, reapplyDecision := reapplyController.Resolve(highLag)
	assert.Equal(t, 180, batch)
	assert.Equal(t, "apply_increase", reapplyDecision.Decision, "digest re-apply must be replay-stable and avoid duplicate transition hold")
	assert.Equal(t, "manifest-v2b", reapplyDecision.PolicyManifestDigest)
	assert.Equal(t, int64(2), reapplyDecision.PolicyEpoch)
	assert.Equal(t, 0, reapplyDecision.PolicyActivationTicks)
}

func TestAutoTuneController_PolicyManifestSequenceGapRequiresContiguousEpochProgress(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	baseCfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               260,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	baseController := newAutoTuneController(100, baseCfg)
	require.NotNil(t, baseController)

	batch, baselineDecision := baseController.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "apply_increase", baselineDecision.Decision)
	assert.Equal(t, "manifest-v2a", baselineDecision.PolicyManifestDigest)
	assert.Equal(t, int64(1), baselineDecision.PolicyEpoch)

	gapCfg := baseCfg
	gapCfg.PolicyManifestDigest = "manifest-v2c"
	gapCfg.PolicyManifestRefreshEpoch = 3
	gapSeed := baseController.currentBatch
	gapController := newAutoTuneControllerWithSeed(100, gapCfg, &gapSeed)
	require.NotNil(t, gapController)
	gapController.reconcilePolicyTransition(baseController.exportPolicyTransition())

	batch, gapDecision := gapController.Resolve(highLag)
	assert.Equal(t, 140, batch)
	assert.Equal(t, "apply_increase", gapDecision.Decision, "sequence-gap transition must not apply non-contiguous manifest segment")
	assert.Equal(t, "manifest-v2a", gapDecision.PolicyManifestDigest, "sequence-gap transition must pin last contiguous digest")
	assert.Equal(t, int64(1), gapDecision.PolicyEpoch, "sequence-gap transition must pin last contiguous epoch")
	assert.Equal(t, 0, gapDecision.PolicyActivationTicks, "sequence-gap transition must not open activation hold")

	gapFillCfg := baseCfg
	gapFillCfg.PolicyManifestDigest = "manifest-v2b"
	gapFillCfg.PolicyManifestRefreshEpoch = 2
	gapFillSeed := gapController.currentBatch
	gapFillController := newAutoTuneControllerWithSeed(100, gapFillCfg, &gapFillSeed)
	require.NotNil(t, gapFillController)
	gapFillController.reconcilePolicyTransition(gapController.exportPolicyTransition())

	batch, gapFillHold := gapFillController.Resolve(highLag)
	assert.Equal(t, gapFillSeed, batch)
	assert.Equal(t, "hold_policy_transition", gapFillHold.Decision, "late contiguous gap-fill must apply deterministic activation hold")
	assert.Equal(t, "manifest-v2b", gapFillHold.PolicyManifestDigest)
	assert.Equal(t, int64(2), gapFillHold.PolicyEpoch)
	assert.Equal(t, 1, gapFillHold.PolicyActivationTicks)

	batch, gapFillApplied := gapFillController.Resolve(highLag)
	assert.Equal(t, gapFillSeed+20, batch)
	assert.Equal(t, "apply_increase", gapFillApplied.Decision)
	assert.Equal(t, "manifest-v2b", gapFillApplied.PolicyManifestDigest)
	assert.Equal(t, int64(2), gapFillApplied.PolicyEpoch)

	reapplySeed := gapFillController.currentBatch
	reapplyController := newAutoTuneControllerWithSeed(100, gapCfg, &reapplySeed)
	require.NotNil(t, reapplyController)
	reapplyController.reconcilePolicyTransition(gapFillController.exportPolicyTransition())

	batch, reapplyHold := reapplyController.Resolve(highLag)
	assert.Equal(t, reapplySeed, batch)
	assert.Equal(t, "hold_policy_transition", reapplyHold.Decision, "duplicate segment re-apply after gap-fill must deterministically activate once")
	assert.Equal(t, "manifest-v2c", reapplyHold.PolicyManifestDigest)
	assert.Equal(t, int64(3), reapplyHold.PolicyEpoch)
	assert.Equal(t, 1, reapplyHold.PolicyActivationTicks)

	duplicateSeed := reapplyController.currentBatch
	duplicateController := newAutoTuneControllerWithSeed(100, gapCfg, &duplicateSeed)
	require.NotNil(t, duplicateController)
	duplicateController.reconcilePolicyTransition(reapplyController.exportPolicyTransition())

	batch, duplicateDecision := duplicateController.Resolve(highLag)
	assert.Equal(t, duplicateSeed+20, batch)
	assert.Equal(t, "apply_increase", duplicateDecision.Decision, "duplicate segment re-apply at same contiguous epoch must not reopen activation hold")
	assert.Equal(t, "manifest-v2c", duplicateDecision.PolicyManifestDigest)
	assert.Equal(t, int64(3), duplicateDecision.PolicyEpoch)
	assert.Equal(t, 0, duplicateDecision.PolicyActivationTicks)
}

func TestAutoTuneController_PolicyManifestSnapshotCutoverRequiresLineageFence(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	baseCfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               260,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-v2tail-a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	baseController := newAutoTuneController(100, baseCfg)
	require.NotNil(t, baseController)

	batch, baselineDecision := baseController.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "apply_increase", baselineDecision.Decision)
	assert.Equal(t, "manifest-v2tail-a", baselineDecision.PolicyManifestDigest)
	assert.Equal(t, int64(1), baselineDecision.PolicyEpoch)

	snapshotCfg := baseCfg
	snapshotCfg.PolicyManifestDigest = "manifest-v2snapshot|snapshot-base-seq=1|snapshot-tail-seq=3"
	snapshotCfg.PolicyManifestRefreshEpoch = 3
	snapshotSeed := baseController.currentBatch
	snapshotController := newAutoTuneControllerWithSeed(100, snapshotCfg, &snapshotSeed)
	require.NotNil(t, snapshotController)
	snapshotController.reconcilePolicyTransition(baseController.exportPolicyTransition())

	batch, snapshotHold := snapshotController.Resolve(highLag)
	assert.Equal(t, snapshotSeed, batch)
	assert.Equal(t, "hold_policy_transition", snapshotHold.Decision, "snapshot cutover must apply deterministic transition hold")
	assert.Equal(t, snapshotCfg.PolicyManifestDigest, snapshotHold.PolicyManifestDigest)
	assert.Equal(t, snapshotCfg.PolicyManifestRefreshEpoch, snapshotHold.PolicyEpoch)
	assert.Equal(t, 1, snapshotHold.PolicyActivationTicks)

	batch, snapshotApplied := snapshotController.Resolve(highLag)
	assert.Equal(t, snapshotSeed+20, batch)
	assert.Equal(t, "apply_increase", snapshotApplied.Decision)
	assert.Equal(t, snapshotCfg.PolicyManifestDigest, snapshotApplied.PolicyManifestDigest)
	assert.Equal(t, snapshotCfg.PolicyManifestRefreshEpoch, snapshotApplied.PolicyEpoch)
	assert.Equal(t, 0, snapshotApplied.PolicyActivationTicks)

	staleSnapshotCfg := baseCfg
	staleSnapshotCfg.PolicyManifestDigest = "manifest-v2snapshot-stale|snapshot-base-seq=0|snapshot-tail-seq=2"
	staleSnapshotCfg.PolicyManifestRefreshEpoch = 2
	staleSeed := snapshotController.currentBatch
	staleController := newAutoTuneControllerWithSeed(100, staleSnapshotCfg, &staleSeed)
	require.NotNil(t, staleController)
	staleController.reconcilePolicyTransition(snapshotController.exportPolicyTransition())

	batch, staleDecision := staleController.Resolve(highLag)
	assert.Equal(t, staleSeed+20, batch)
	assert.Equal(t, "apply_increase", staleDecision.Decision, "stale snapshot must not reopen transition hold")
	assert.Equal(t, snapshotCfg.PolicyManifestDigest, staleDecision.PolicyManifestDigest, "stale snapshot must pin last verified snapshot digest")
	assert.Equal(t, snapshotCfg.PolicyManifestRefreshEpoch, staleDecision.PolicyEpoch, "stale snapshot must preserve last verified snapshot epoch")
	assert.Equal(t, 0, staleDecision.PolicyActivationTicks)

	reapplySeed := staleController.currentBatch
	reapplyController := newAutoTuneControllerWithSeed(100, snapshotCfg, &reapplySeed)
	require.NotNil(t, reapplyController)
	reapplyController.reconcilePolicyTransition(staleController.exportPolicyTransition())

	batch, reapplyDecision := reapplyController.Resolve(highLag)
	assert.Equal(t, reapplySeed+20, batch)
	assert.Equal(t, "apply_increase", reapplyDecision.Decision, "snapshot+tail re-apply must remain replay-stable")
	assert.Equal(t, snapshotCfg.PolicyManifestDigest, reapplyDecision.PolicyManifestDigest)
	assert.Equal(t, snapshotCfg.PolicyManifestRefreshEpoch, reapplyDecision.PolicyEpoch)
	assert.Equal(t, 0, reapplyDecision.PolicyActivationTicks)
}

func TestAutoTuneController_PolicyManifestRollbackLineageRequiresDeterministicFence(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               260,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3

	segment1Controller := newAutoTuneController(100, segment1Cfg)
	require.NotNil(t, segment1Controller)

	batch, segment1Decision := segment1Controller.Resolve(highLag)
	assert.Equal(t, 120, batch)
	assert.Equal(t, "apply_increase", segment1Decision.Decision)
	assert.Equal(t, "manifest-tail-v2a", segment1Decision.PolicyManifestDigest)
	assert.Equal(t, int64(1), segment1Decision.PolicyEpoch)

	segment2Seed := segment1Controller.currentBatch
	segment2Controller := newAutoTuneControllerWithSeed(100, segment2Cfg, &segment2Seed)
	require.NotNil(t, segment2Controller)
	segment2Controller.reconcilePolicyTransition(segment1Controller.exportPolicyTransition())

	batch, segment2Hold := segment2Controller.Resolve(highLag)
	assert.Equal(t, segment2Seed, batch)
	assert.Equal(t, "hold_policy_transition", segment2Hold.Decision)
	assert.Equal(t, "manifest-tail-v2b", segment2Hold.PolicyManifestDigest)
	assert.Equal(t, int64(2), segment2Hold.PolicyEpoch)
	assert.Equal(t, 1, segment2Hold.PolicyActivationTicks)

	batch, segment2Applied := segment2Controller.Resolve(highLag)
	assert.Equal(t, 140, batch)
	assert.Equal(t, "apply_increase", segment2Applied.Decision)
	assert.Equal(t, "manifest-tail-v2b", segment2Applied.PolicyManifestDigest)
	assert.Equal(t, int64(2), segment2Applied.PolicyEpoch)

	segment3Seed := segment2Controller.currentBatch
	segment3Controller := newAutoTuneControllerWithSeed(100, segment3Cfg, &segment3Seed)
	require.NotNil(t, segment3Controller)
	segment3Controller.reconcilePolicyTransition(segment2Controller.exportPolicyTransition())

	batch, segment3Hold := segment3Controller.Resolve(highLag)
	assert.Equal(t, segment3Seed, batch)
	assert.Equal(t, "hold_policy_transition", segment3Hold.Decision)
	assert.Equal(t, "manifest-tail-v2c", segment3Hold.PolicyManifestDigest)
	assert.Equal(t, int64(3), segment3Hold.PolicyEpoch)
	assert.Equal(t, 1, segment3Hold.PolicyActivationTicks)

	batch, segment3Applied := segment3Controller.Resolve(highLag)
	assert.Equal(t, 160, batch)
	assert.Equal(t, "apply_increase", segment3Applied.Decision)
	assert.Equal(t, "manifest-tail-v2c", segment3Applied.PolicyManifestDigest)
	assert.Equal(t, int64(3), segment3Applied.PolicyEpoch)

	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	staleRollbackCfg := segment1Cfg
	staleRollbackCfg.PolicyManifestDigest = "manifest-tail-v2a|rollback-from-seq=3|rollback-to-seq=1|rollback-forward-seq=3"

	rollbackSeed := segment3Controller.currentBatch
	rollbackController := newAutoTuneControllerWithSeed(100, rollbackCfg, &rollbackSeed)
	require.NotNil(t, rollbackController)
	rollbackController.reconcilePolicyTransition(segment3Controller.exportPolicyTransition())

	batch, rollbackHold := rollbackController.Resolve(highLag)
	assert.Equal(t, rollbackSeed, batch)
	assert.Equal(t, "hold_policy_transition", rollbackHold.Decision)
	assert.Equal(t, rollbackCfg.PolicyManifestDigest, rollbackHold.PolicyManifestDigest)
	assert.Equal(t, rollbackCfg.PolicyManifestRefreshEpoch, rollbackHold.PolicyEpoch)
	assert.Equal(t, 1, rollbackHold.PolicyActivationTicks)

	batch, rollbackApplied := rollbackController.Resolve(highLag)
	assert.Equal(t, rollbackSeed+20, batch)
	assert.Equal(t, "apply_increase", rollbackApplied.Decision)
	assert.Equal(t, rollbackCfg.PolicyManifestDigest, rollbackApplied.PolicyManifestDigest)
	assert.Equal(t, rollbackCfg.PolicyManifestRefreshEpoch, rollbackApplied.PolicyEpoch)
	assert.Equal(t, 0, rollbackApplied.PolicyActivationTicks)

	staleSeed := rollbackController.currentBatch
	staleController := newAutoTuneControllerWithSeed(100, staleRollbackCfg, &staleSeed)
	require.NotNil(t, staleController)
	staleController.reconcilePolicyTransition(rollbackController.exportPolicyTransition())

	batch, staleDecision := staleController.Resolve(highLag)
	assert.Equal(t, staleSeed+20, batch)
	assert.Equal(t, "apply_increase", staleDecision.Decision, "stale rollback must not reopen transition hold")
	assert.Equal(t, rollbackCfg.PolicyManifestDigest, staleDecision.PolicyManifestDigest, "stale rollback must pin last verified rollback-safe digest")
	assert.Equal(t, rollbackCfg.PolicyManifestRefreshEpoch, staleDecision.PolicyEpoch, "stale rollback must preserve last verified rollback-safe epoch")
	assert.Equal(t, 0, staleDecision.PolicyActivationTicks)

	reForwardSeed := staleController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(staleController.exportPolicyTransition())

	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward apply must deterministically reopen one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)

	batch, reForwardApplied := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed+20, batch)
	assert.Equal(t, "apply_increase", reForwardApplied.Decision)
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardApplied.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardApplied.PolicyEpoch)
	assert.Equal(t, 0, reForwardApplied.PolicyActivationTicks)

	reapplySeed := reForwardController.currentBatch
	reapplyController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reapplySeed)
	require.NotNil(t, reapplyController)
	reapplyController.reconcilePolicyTransition(reForwardController.exportPolicyTransition())

	batch, reapplyDecision := reapplyController.Resolve(highLag)
	assert.Equal(t, reapplySeed+20, batch)
	assert.Equal(t, "apply_increase", reapplyDecision.Decision, "rollback+re-forward re-apply must remain replay-stable without reopening hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reapplyDecision.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reapplyDecision.PolicyEpoch)
	assert.Equal(t, 0, reapplyDecision.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFenceEpochCompactionRejectsStaleFenceReactivation(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               260,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	compactionCfg := rollbackCfg
	compactionCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone=1"

	segment1Controller := newAutoTuneController(100, segment1Cfg)
	require.NotNil(t, segment1Controller)
	batch, _ := segment1Controller.Resolve(highLag)
	assert.Equal(t, 120, batch)

	segment2Seed := segment1Controller.currentBatch
	segment2Controller := newAutoTuneControllerWithSeed(100, segment2Cfg, &segment2Seed)
	require.NotNil(t, segment2Controller)
	segment2Controller.reconcilePolicyTransition(segment1Controller.exportPolicyTransition())
	batch, _ = segment2Controller.Resolve(highLag)
	assert.Equal(t, segment2Seed, batch)
	batch, _ = segment2Controller.Resolve(highLag)
	assert.Equal(t, 140, batch)

	segment3Seed := segment2Controller.currentBatch
	segment3Controller := newAutoTuneControllerWithSeed(100, segment3Cfg, &segment3Seed)
	require.NotNil(t, segment3Controller)
	segment3Controller.reconcilePolicyTransition(segment2Controller.exportPolicyTransition())
	batch, _ = segment3Controller.Resolve(highLag)
	assert.Equal(t, segment3Seed, batch)
	batch, _ = segment3Controller.Resolve(highLag)
	assert.Equal(t, 160, batch)

	rollbackSeed := segment3Controller.currentBatch
	rollbackController := newAutoTuneControllerWithSeed(100, rollbackCfg, &rollbackSeed)
	require.NotNil(t, rollbackController)
	rollbackController.reconcilePolicyTransition(segment3Controller.exportPolicyTransition())
	batch, _ = rollbackController.Resolve(highLag)
	assert.Equal(t, rollbackSeed, batch)
	batch, rollbackApplied := rollbackController.Resolve(highLag)
	assert.Equal(t, rollbackSeed+20, batch)
	assert.Equal(t, "apply_increase", rollbackApplied.Decision)
	assert.Equal(t, rollbackCfg.PolicyManifestDigest, rollbackApplied.PolicyManifestDigest)
	assert.Equal(t, int64(2), rollbackApplied.PolicyEpoch)

	compactionSeed := rollbackController.currentBatch
	compactionController := newAutoTuneControllerWithSeed(100, compactionCfg, &compactionSeed)
	require.NotNil(t, compactionController)
	compactionController.reconcilePolicyTransition(rollbackController.exportPolicyTransition())

	batch, compactionDecision := compactionController.Resolve(highLag)
	assert.Equal(t, compactionSeed+20, batch)
	assert.Equal(t, "apply_increase", compactionDecision.Decision, "same-epoch rollback fence compaction must not reopen transition hold")
	assert.Equal(t, compactionCfg.PolicyManifestDigest, compactionDecision.PolicyManifestDigest)
	assert.Equal(t, compactionCfg.PolicyManifestRefreshEpoch, compactionDecision.PolicyEpoch)
	assert.Equal(t, 0, compactionDecision.PolicyActivationTicks)

	staleRollbackSeed := compactionController.currentBatch
	staleRollbackController := newAutoTuneControllerWithSeed(100, rollbackCfg, &staleRollbackSeed)
	require.NotNil(t, staleRollbackController)
	staleRollbackController.reconcilePolicyTransition(compactionController.exportPolicyTransition())

	batch, staleRollbackDecision := staleRollbackController.Resolve(highLag)
	assert.Equal(t, staleRollbackSeed+20, batch)
	assert.Equal(t, "apply_increase", staleRollbackDecision.Decision, "stale pre-compaction rollback must not reactivate fence ownership")
	assert.Equal(t, compactionCfg.PolicyManifestDigest, staleRollbackDecision.PolicyManifestDigest)
	assert.Equal(t, compactionCfg.PolicyManifestRefreshEpoch, staleRollbackDecision.PolicyEpoch)
	assert.Equal(t, 0, staleRollbackDecision.PolicyActivationTicks)

	reForwardSeed := staleRollbackController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(staleRollbackController.exportPolicyTransition())

	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after compaction must remain deterministic")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostQuarantineReleaseWindowRejectsStaleReactivation(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	segment1Cfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               360,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2a",
		PolicyManifestRefreshEpoch: 1,
		PolicyActivationHoldTicks:  1,
	}
	segment2Cfg := segment1Cfg
	segment2Cfg.PolicyManifestDigest = "manifest-tail-v2b"
	segment2Cfg.PolicyManifestRefreshEpoch = 2
	segment3Cfg := segment1Cfg
	segment3Cfg.PolicyManifestDigest = "manifest-tail-v2c"
	segment3Cfg.PolicyManifestRefreshEpoch = 3
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	compactionCfg := rollbackCfg
	compactionCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone=1"
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	releaseWindowCfg := quarantineCfg
	releaseWindowCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=7"

	segment1Controller := newAutoTuneController(100, segment1Cfg)
	require.NotNil(t, segment1Controller)
	batch, _ := segment1Controller.Resolve(highLag)
	assert.Equal(t, 120, batch)

	segment2Seed := segment1Controller.currentBatch
	segment2Controller := newAutoTuneControllerWithSeed(100, segment2Cfg, &segment2Seed)
	require.NotNil(t, segment2Controller)
	segment2Controller.reconcilePolicyTransition(segment1Controller.exportPolicyTransition())
	batch, _ = segment2Controller.Resolve(highLag)
	assert.Equal(t, segment2Seed, batch)
	batch, _ = segment2Controller.Resolve(highLag)
	assert.Equal(t, 140, batch)

	segment3Seed := segment2Controller.currentBatch
	segment3Controller := newAutoTuneControllerWithSeed(100, segment3Cfg, &segment3Seed)
	require.NotNil(t, segment3Controller)
	segment3Controller.reconcilePolicyTransition(segment2Controller.exportPolicyTransition())
	batch, _ = segment3Controller.Resolve(highLag)
	assert.Equal(t, segment3Seed, batch)
	batch, _ = segment3Controller.Resolve(highLag)
	assert.Equal(t, 160, batch)

	rollbackSeed := segment3Controller.currentBatch
	rollbackController := newAutoTuneControllerWithSeed(100, rollbackCfg, &rollbackSeed)
	require.NotNil(t, rollbackController)
	rollbackController.reconcilePolicyTransition(segment3Controller.exportPolicyTransition())
	batch, _ = rollbackController.Resolve(highLag)
	assert.Equal(t, rollbackSeed, batch)
	batch, rollbackApplied := rollbackController.Resolve(highLag)
	assert.Equal(t, rollbackSeed+20, batch)
	assert.Equal(t, "apply_increase", rollbackApplied.Decision)
	assert.Equal(t, rollbackCfg.PolicyManifestDigest, rollbackApplied.PolicyManifestDigest)
	assert.Equal(t, int64(2), rollbackApplied.PolicyEpoch)
	assert.Equal(t, 0, rollbackApplied.PolicyActivationTicks)

	compactionSeed := rollbackController.currentBatch
	compactionController := newAutoTuneControllerWithSeed(100, compactionCfg, &compactionSeed)
	require.NotNil(t, compactionController)
	compactionController.reconcilePolicyTransition(rollbackController.exportPolicyTransition())
	batch, compactionDecision := compactionController.Resolve(highLag)
	assert.Equal(t, compactionSeed+20, batch)
	assert.Equal(t, "apply_increase", compactionDecision.Decision)
	assert.Equal(t, compactionCfg.PolicyManifestDigest, compactionDecision.PolicyManifestDigest)
	assert.Equal(t, compactionCfg.PolicyManifestRefreshEpoch, compactionDecision.PolicyEpoch)
	assert.Equal(t, 0, compactionDecision.PolicyActivationTicks)

	expirySeed := compactionController.currentBatch
	expiryController := newAutoTuneControllerWithSeed(100, expiryCfg, &expirySeed)
	require.NotNil(t, expiryController)
	expiryController.reconcilePolicyTransition(compactionController.exportPolicyTransition())
	batch, expiryDecision := expiryController.Resolve(highLag)
	assert.Equal(t, expirySeed+20, batch)
	assert.Equal(t, "apply_increase", expiryDecision.Decision)
	assert.Equal(t, expiryCfg.PolicyManifestDigest, expiryDecision.PolicyManifestDigest)
	assert.Equal(t, expiryCfg.PolicyManifestRefreshEpoch, expiryDecision.PolicyEpoch)
	assert.Equal(t, 0, expiryDecision.PolicyActivationTicks)

	quarantineSeed := expiryController.currentBatch
	quarantineController := newAutoTuneControllerWithSeed(100, quarantineCfg, &quarantineSeed)
	require.NotNil(t, quarantineController)
	quarantineController.reconcilePolicyTransition(expiryController.exportPolicyTransition())
	batch, quarantineDecision := quarantineController.Resolve(highLag)
	assert.Equal(t, quarantineSeed+20, batch)
	assert.Equal(t, "apply_increase", quarantineDecision.Decision)
	assert.Equal(t, quarantineCfg.PolicyManifestDigest, quarantineDecision.PolicyManifestDigest)
	assert.Equal(t, quarantineCfg.PolicyManifestRefreshEpoch, quarantineDecision.PolicyEpoch)
	assert.Equal(t, 0, quarantineDecision.PolicyActivationTicks)

	releaseSeed := quarantineController.currentBatch
	releaseController := newAutoTuneControllerWithSeed(100, releaseCfg, &releaseSeed)
	require.NotNil(t, releaseController)
	releaseController.reconcilePolicyTransition(quarantineController.exportPolicyTransition())
	batch, releaseDecision := releaseController.Resolve(highLag)
	assert.Equal(t, releaseSeed+20, batch)
	assert.Equal(t, "apply_increase", releaseDecision.Decision)
	assert.Equal(t, releaseCfg.PolicyManifestDigest, releaseDecision.PolicyManifestDigest)
	assert.Equal(t, releaseCfg.PolicyManifestRefreshEpoch, releaseDecision.PolicyEpoch)
	assert.Equal(t, 0, releaseDecision.PolicyActivationTicks)

	releaseWindowSeed := releaseController.currentBatch
	releaseWindowController := newAutoTuneControllerWithSeed(100, releaseWindowCfg, &releaseWindowSeed)
	require.NotNil(t, releaseWindowController)
	releaseWindowController.reconcilePolicyTransition(releaseController.exportPolicyTransition())
	batch, releaseWindowDecision := releaseWindowController.Resolve(highLag)
	assert.Equal(t, releaseWindowSeed+20, batch)
	assert.Equal(t, "apply_increase", releaseWindowDecision.Decision)
	assert.Equal(t, releaseWindowCfg.PolicyManifestDigest, releaseWindowDecision.PolicyManifestDigest)
	assert.Equal(t, releaseWindowCfg.PolicyManifestRefreshEpoch, releaseWindowDecision.PolicyEpoch)
	assert.Equal(t, 0, releaseWindowDecision.PolicyActivationTicks)

	staleReleaseSeed := releaseWindowController.currentBatch
	staleReleaseController := newAutoTuneControllerWithSeed(100, releaseCfg, &staleReleaseSeed)
	require.NotNil(t, staleReleaseController)
	staleReleaseController.reconcilePolicyTransition(releaseWindowController.exportPolicyTransition())
	batch, staleReleaseDecision := staleReleaseController.Resolve(highLag)
	assert.Equal(t, staleReleaseSeed+20, batch)
	assert.Equal(t, "apply_increase", staleReleaseDecision.Decision)
	assert.Equal(t, releaseWindowCfg.PolicyManifestDigest, staleReleaseDecision.PolicyManifestDigest, "stale release watermark must remain pinned to latest release-window ownership")
	assert.Equal(t, releaseWindowCfg.PolicyManifestRefreshEpoch, staleReleaseDecision.PolicyEpoch)
	assert.Equal(t, 0, staleReleaseDecision.PolicyActivationTicks)

	staleQuarantineSeed := staleReleaseController.currentBatch
	staleQuarantineController := newAutoTuneControllerWithSeed(100, quarantineCfg, &staleQuarantineSeed)
	require.NotNil(t, staleQuarantineController)
	staleQuarantineController.reconcilePolicyTransition(staleReleaseController.exportPolicyTransition())
	batch, staleQuarantineDecision := staleQuarantineController.Resolve(highLag)
	assert.Equal(t, staleQuarantineSeed+20, batch)
	assert.Equal(t, "apply_increase", staleQuarantineDecision.Decision)
	assert.Equal(t, releaseWindowCfg.PolicyManifestDigest, staleQuarantineDecision.PolicyManifestDigest, "stale post-release quarantine marker must not reactivate")
	assert.Equal(t, releaseWindowCfg.PolicyManifestRefreshEpoch, staleQuarantineDecision.PolicyEpoch)
	assert.Equal(t, 0, staleQuarantineDecision.PolicyActivationTicks)

	reForwardSeed := staleQuarantineController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(staleQuarantineController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after late-marker release must remain deterministic")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFenceWarmRestoreCollapsesAmbiguousHoldWindow(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	rollbackCfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               260,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3",
		PolicyManifestRefreshEpoch: 2,
		PolicyActivationHoldTicks:  2,
	}

	seed := 140
	controller := newAutoTuneControllerWithSeed(100, rollbackCfg, &seed)
	require.NotNil(t, controller)
	controller.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:                true,
		Version:                 rollbackCfg.PolicyVersion,
		ManifestDigest:          rollbackCfg.PolicyManifestDigest,
		Epoch:                   rollbackCfg.PolicyManifestRefreshEpoch,
		ActivationHoldRemaining: 2,
		FromWarmCheckpoint:      true,
	})

	batch, hold := controller.Resolve(highLag)
	assert.Equal(t, seed, batch)
	assert.Equal(t, "hold_policy_transition", hold.Decision)
	assert.Equal(
		t,
		1,
		hold.PolicyActivationTicks,
		"warm rollback fence restore should collapse ambiguous pre/post-flush hold windows to one deterministic hold tick",
	)

	batch, applied := controller.Resolve(highLag)
	assert.Equal(t, seed+20, batch)
	assert.Equal(t, "apply_increase", applied.Decision)
	assert.Equal(t, 0, applied.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFenceEpochCompactionWarmRestoreCollapsesHoldToZero(t *testing.T) {
	highLag := autoTuneInputs{
		HasHeadSignal:      true,
		HeadSequence:       1_000,
		HasMinCursorSignal: true,
		MinCursorSequence:  100,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	compactionCfg := AutoTuneConfig{
		Enabled:                    true,
		MinBatchSize:               60,
		MaxBatchSize:               260,
		StepUp:                     20,
		StepDown:                   10,
		LagHighWatermark:           80,
		LagLowWatermark:            20,
		QueueHighWatermarkPct:      90,
		QueueLowWatermarkPct:       10,
		HysteresisTicks:            1,
		CooldownTicks:              1,
		PolicyVersion:              "policy-v2",
		PolicyManifestDigest:       "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3|rollback-fence-tombstone=1",
		PolicyManifestRefreshEpoch: 2,
		PolicyActivationHoldTicks:  2,
	}

	seed := 140
	controller := newAutoTuneControllerWithSeed(100, compactionCfg, &seed)
	require.NotNil(t, controller)
	controller.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:                true,
		Version:                 compactionCfg.PolicyVersion,
		ManifestDigest:          compactionCfg.PolicyManifestDigest,
		Epoch:                   compactionCfg.PolicyManifestRefreshEpoch,
		ActivationHoldRemaining: 2,
		FromWarmCheckpoint:      true,
	})

	batch, decision := controller.Resolve(highLag)
	assert.Equal(t, seed+20, batch)
	assert.Equal(t, "apply_increase", decision.Decision)
	assert.Equal(t, 0, decision.PolicyActivationTicks)
}

func intPtr(v int) *int {
	return &v
}
