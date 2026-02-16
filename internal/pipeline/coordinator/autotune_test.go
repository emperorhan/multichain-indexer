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

func TestAutoTuneController_RollbackCheckpointFencePostReleaseWindowEpochRolloverRejectsStalePriorEpochFence(t *testing.T) {
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
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	releaseWindowCfg := quarantineCfg
	releaseWindowCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=7"

	releaseSeed := 220
	releaseController := newAutoTuneControllerWithSeed(100, releaseCfg, &releaseSeed)
	require.NotNil(t, releaseController)
	releaseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        quarantineCfg.PolicyVersion,
		ManifestDigest: quarantineCfg.PolicyManifestDigest,
		Epoch:          quarantineCfg.PolicyManifestRefreshEpoch,
	})
	batch, releaseDecision := releaseController.Resolve(highLag)
	assert.Equal(t, releaseSeed+20, batch)
	assert.Equal(t, "apply_increase", releaseDecision.Decision)

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

	staleRollbackAtRolloverCfg := rollbackCfg
	staleRollbackAtRolloverCfg.PolicyManifestRefreshEpoch = 3
	staleRolloverSeed := releaseWindowController.currentBatch
	staleRolloverController := newAutoTuneControllerWithSeed(100, staleRollbackAtRolloverCfg, &staleRolloverSeed)
	require.NotNil(t, staleRolloverController)
	staleRolloverController.reconcilePolicyTransition(releaseWindowController.exportPolicyTransition())
	batch, staleRolloverDecision := staleRolloverController.Resolve(highLag)
	assert.Equal(t, staleRolloverSeed+20, batch)
	assert.Equal(t, "apply_increase", staleRolloverDecision.Decision, "stale prior-epoch rollback fence must be rejected at epoch rollover")
	assert.Equal(t, releaseWindowCfg.PolicyManifestDigest, staleRolloverDecision.PolicyManifestDigest)
	assert.Equal(t, releaseWindowCfg.PolicyManifestRefreshEpoch, staleRolloverDecision.PolicyEpoch)
	assert.Equal(t, 0, staleRolloverDecision.PolicyActivationTicks)

	liveRolloverSeed := staleRolloverController.currentBatch
	liveRolloverController := newAutoTuneControllerWithSeed(100, segment3Cfg, &liveRolloverSeed)
	require.NotNil(t, liveRolloverController)
	liveRolloverController.reconcilePolicyTransition(staleRolloverController.exportPolicyTransition())
	batch, liveRolloverHold := liveRolloverController.Resolve(highLag)
	assert.Equal(t, liveRolloverSeed, batch)
	assert.Equal(t, "hold_policy_transition", liveRolloverHold.Decision)
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, liveRolloverHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, liveRolloverHold.PolicyEpoch)
	assert.Equal(t, 1, liveRolloverHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostEpochRolloverLateBridgeOrdersByBridgeSequenceAndReleaseWatermark(t *testing.T) {
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
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	releaseWindowCfg := quarantineCfg
	releaseWindowCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=7"
	lateBridgeSeq1Cfg := releaseWindowCfg
	lateBridgeSeq1Cfg.PolicyManifestDigest = releaseWindowCfg.PolicyManifestDigest +
		"|rollback-fence-late-bridge-seq=1|rollback-fence-late-bridge-release-watermark=70"
	lateBridgeSeq2Cfg := releaseWindowCfg
	lateBridgeSeq2Cfg.PolicyManifestDigest = releaseWindowCfg.PolicyManifestDigest +
		"|rollback-fence-late-bridge-seq=2|rollback-fence-late-bridge-release-watermark=80"
	staleBridgeCfg := releaseWindowCfg
	staleBridgeCfg.PolicyManifestDigest = releaseWindowCfg.PolicyManifestDigest +
		"|rollback-fence-late-bridge-seq=1|rollback-fence-late-bridge-release-watermark=90"
	ambiguousBridgeCfg := releaseWindowCfg
	ambiguousBridgeCfg.PolicyManifestDigest = releaseWindowCfg.PolicyManifestDigest +
		"|rollback-fence-late-bridge-seq=3"

	releaseWindowSeed := 220
	releaseWindowController := newAutoTuneControllerWithSeed(100, releaseWindowCfg, &releaseWindowSeed)
	require.NotNil(t, releaseWindowController)
	releaseWindowController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, releaseWindowDecision := releaseWindowController.Resolve(highLag)
	assert.Equal(t, releaseWindowSeed+20, batch)
	assert.Equal(t, "apply_increase", releaseWindowDecision.Decision)
	assert.Equal(t, releaseWindowCfg.PolicyManifestDigest, releaseWindowDecision.PolicyManifestDigest)
	assert.Equal(t, releaseWindowCfg.PolicyManifestRefreshEpoch, releaseWindowDecision.PolicyEpoch)

	bridgeSeq1Seed := releaseWindowController.currentBatch
	bridgeSeq1Controller := newAutoTuneControllerWithSeed(100, lateBridgeSeq1Cfg, &bridgeSeq1Seed)
	require.NotNil(t, bridgeSeq1Controller)
	bridgeSeq1Controller.reconcilePolicyTransition(releaseWindowController.exportPolicyTransition())
	batch, bridgeSeq1Decision := bridgeSeq1Controller.Resolve(highLag)
	assert.Equal(t, bridgeSeq1Seed+20, batch)
	assert.Equal(t, "apply_increase", bridgeSeq1Decision.Decision)
	assert.Equal(t, lateBridgeSeq1Cfg.PolicyManifestDigest, bridgeSeq1Decision.PolicyManifestDigest)
	assert.Equal(t, lateBridgeSeq1Cfg.PolicyManifestRefreshEpoch, bridgeSeq1Decision.PolicyEpoch)
	assert.Equal(t, 0, bridgeSeq1Decision.PolicyActivationTicks)

	bridgeSeq2Seed := bridgeSeq1Controller.currentBatch
	bridgeSeq2Controller := newAutoTuneControllerWithSeed(100, lateBridgeSeq2Cfg, &bridgeSeq2Seed)
	require.NotNil(t, bridgeSeq2Controller)
	bridgeSeq2Controller.reconcilePolicyTransition(bridgeSeq1Controller.exportPolicyTransition())
	batch, bridgeSeq2Decision := bridgeSeq2Controller.Resolve(highLag)
	assert.Equal(t, bridgeSeq2Seed+20, batch)
	assert.Equal(t, "apply_increase", bridgeSeq2Decision.Decision)
	assert.Equal(t, lateBridgeSeq2Cfg.PolicyManifestDigest, bridgeSeq2Decision.PolicyManifestDigest)
	assert.Equal(t, lateBridgeSeq2Cfg.PolicyManifestRefreshEpoch, bridgeSeq2Decision.PolicyEpoch)
	assert.Equal(t, 0, bridgeSeq2Decision.PolicyActivationTicks)

	staleBridgeSeed := bridgeSeq2Controller.currentBatch
	staleBridgeController := newAutoTuneControllerWithSeed(100, staleBridgeCfg, &staleBridgeSeed)
	require.NotNil(t, staleBridgeController)
	staleBridgeController.reconcilePolicyTransition(bridgeSeq2Controller.exportPolicyTransition())
	batch, staleBridgeDecision := staleBridgeController.Resolve(highLag)
	assert.Equal(t, staleBridgeSeed+20, batch)
	assert.Equal(t, "apply_increase", staleBridgeDecision.Decision)
	assert.Equal(t, lateBridgeSeq2Cfg.PolicyManifestDigest, staleBridgeDecision.PolicyManifestDigest, "lower bridge sequence must not reopen stale ownership even with higher watermark")
	assert.Equal(t, lateBridgeSeq2Cfg.PolicyManifestRefreshEpoch, staleBridgeDecision.PolicyEpoch)
	assert.Equal(t, 0, staleBridgeDecision.PolicyActivationTicks)

	ambiguousBridgeSeed := staleBridgeController.currentBatch
	ambiguousBridgeController := newAutoTuneControllerWithSeed(100, ambiguousBridgeCfg, &ambiguousBridgeSeed)
	require.NotNil(t, ambiguousBridgeController)
	ambiguousBridgeController.reconcilePolicyTransition(staleBridgeController.exportPolicyTransition())
	batch, ambiguousBridgeDecision := ambiguousBridgeController.Resolve(highLag)
	assert.Equal(t, ambiguousBridgeSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousBridgeDecision.Decision)
	assert.Equal(t, lateBridgeSeq2Cfg.PolicyManifestDigest, ambiguousBridgeDecision.PolicyManifestDigest, "ambiguous late-bridge markers must remain quarantined until ordering tuple is complete")
	assert.Equal(t, lateBridgeSeq2Cfg.PolicyManifestRefreshEpoch, ambiguousBridgeDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousBridgeDecision.PolicyActivationTicks)

	staleRollbackAtRolloverCfg := rollbackCfg
	staleRollbackAtRolloverCfg.PolicyManifestRefreshEpoch = 3
	staleRolloverSeed := ambiguousBridgeController.currentBatch
	staleRolloverController := newAutoTuneControllerWithSeed(100, staleRollbackAtRolloverCfg, &staleRolloverSeed)
	require.NotNil(t, staleRolloverController)
	staleRolloverController.reconcilePolicyTransition(ambiguousBridgeController.exportPolicyTransition())
	batch, staleRolloverDecision := staleRolloverController.Resolve(highLag)
	assert.Equal(t, staleRolloverSeed+20, batch)
	assert.Equal(t, "apply_increase", staleRolloverDecision.Decision)
	assert.Equal(t, lateBridgeSeq2Cfg.PolicyManifestDigest, staleRolloverDecision.PolicyManifestDigest, "post-epoch-rollover stale rollback markers must stay pinned behind adopted late-bridge ownership")
	assert.Equal(t, lateBridgeSeq2Cfg.PolicyManifestRefreshEpoch, staleRolloverDecision.PolicyEpoch)
	assert.Equal(t, 0, staleRolloverDecision.PolicyActivationTicks)

	liveRolloverSeed := staleRolloverController.currentBatch
	liveRolloverController := newAutoTuneControllerWithSeed(100, segment3Cfg, &liveRolloverSeed)
	require.NotNil(t, liveRolloverController)
	liveRolloverController.reconcilePolicyTransition(staleRolloverController.exportPolicyTransition())
	batch, liveRolloverHold := liveRolloverController.Resolve(highLag)
	assert.Equal(t, liveRolloverSeed, batch)
	assert.Equal(t, "hold_policy_transition", liveRolloverHold.Decision, "rollback+re-forward after late-bridge adoption must preserve deterministic one-tick activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, liveRolloverHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, liveRolloverHold.PolicyEpoch)
	assert.Equal(t, 1, liveRolloverHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostLateBridgeBacklogDrainOrdersByBridgeSequenceAndDrainWatermark(t *testing.T) {
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
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	lateBridgeSeq2Cfg := quarantineCfg
	lateBridgeSeq2Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=7|rollback-fence-late-bridge-seq=2|rollback-fence-late-bridge-release-watermark=80"
	drainStage1Cfg := lateBridgeSeq2Cfg
	drainStage1Cfg.PolicyManifestDigest = lateBridgeSeq2Cfg.PolicyManifestDigest + "|rollback-fence-late-bridge-drain-watermark=81"
	drainStage2Cfg := lateBridgeSeq2Cfg
	drainStage2Cfg.PolicyManifestDigest = lateBridgeSeq2Cfg.PolicyManifestDigest + "|rollback-fence-late-bridge-drain-watermark=82"
	staleDrainCfg := lateBridgeSeq2Cfg
	staleDrainCfg.PolicyManifestDigest = lateBridgeSeq2Cfg.PolicyManifestDigest + "|rollback-fence-late-bridge-drain-watermark=79"
	liveBridgeSeq3Cfg := quarantineCfg
	liveBridgeSeq3Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90"
	staleDrainedSeq2AfterLiveCfg := lateBridgeSeq2Cfg
	staleDrainedSeq2AfterLiveCfg.PolicyManifestDigest = lateBridgeSeq2Cfg.PolicyManifestDigest + "|rollback-fence-late-bridge-drain-watermark=120"
	ambiguousDrainCfg := quarantineCfg
	ambiguousDrainCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=7|rollback-fence-late-bridge-drain-watermark=83"

	baseSeed := 220
	baseController := newAutoTuneControllerWithSeed(100, lateBridgeSeq2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, lateBridgeSeq2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, lateBridgeSeq2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)

	drainStage1Seed := baseController.currentBatch
	drainStage1Controller := newAutoTuneControllerWithSeed(100, drainStage1Cfg, &drainStage1Seed)
	require.NotNil(t, drainStage1Controller)
	drainStage1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, drainStage1Decision := drainStage1Controller.Resolve(highLag)
	assert.Equal(t, drainStage1Seed+20, batch)
	assert.Equal(t, "apply_increase", drainStage1Decision.Decision)
	assert.Equal(t, drainStage1Cfg.PolicyManifestDigest, drainStage1Decision.PolicyManifestDigest)
	assert.Equal(t, drainStage1Cfg.PolicyManifestRefreshEpoch, drainStage1Decision.PolicyEpoch)
	assert.Equal(t, 0, drainStage1Decision.PolicyActivationTicks)

	drainStage2Seed := drainStage1Controller.currentBatch
	drainStage2Controller := newAutoTuneControllerWithSeed(100, drainStage2Cfg, &drainStage2Seed)
	require.NotNil(t, drainStage2Controller)
	drainStage2Controller.reconcilePolicyTransition(drainStage1Controller.exportPolicyTransition())
	batch, drainStage2Decision := drainStage2Controller.Resolve(highLag)
	assert.Equal(t, drainStage2Seed+20, batch)
	assert.Equal(t, "apply_increase", drainStage2Decision.Decision)
	assert.Equal(t, drainStage2Cfg.PolicyManifestDigest, drainStage2Decision.PolicyManifestDigest)
	assert.Equal(t, drainStage2Cfg.PolicyManifestRefreshEpoch, drainStage2Decision.PolicyEpoch)
	assert.Equal(t, 0, drainStage2Decision.PolicyActivationTicks)

	staleDrainSeed := drainStage2Controller.currentBatch
	staleDrainController := newAutoTuneControllerWithSeed(100, staleDrainCfg, &staleDrainSeed)
	require.NotNil(t, staleDrainController)
	staleDrainController.reconcilePolicyTransition(drainStage2Controller.exportPolicyTransition())
	batch, staleDrainDecision := staleDrainController.Resolve(highLag)
	assert.Equal(t, staleDrainSeed+20, batch)
	assert.Equal(t, "apply_increase", staleDrainDecision.Decision)
	assert.Equal(
		t,
		drainStage2Cfg.PolicyManifestDigest,
		staleDrainDecision.PolicyManifestDigest,
		"lower drain watermark must stay pinned behind the latest verified backlog-drain ownership",
	)
	assert.Equal(t, drainStage2Cfg.PolicyManifestRefreshEpoch, staleDrainDecision.PolicyEpoch)
	assert.Equal(t, 0, staleDrainDecision.PolicyActivationTicks)

	ambiguousDrainSeed := staleDrainController.currentBatch
	ambiguousDrainController := newAutoTuneControllerWithSeed(100, ambiguousDrainCfg, &ambiguousDrainSeed)
	require.NotNil(t, ambiguousDrainController)
	ambiguousDrainController.reconcilePolicyTransition(staleDrainController.exportPolicyTransition())
	batch, ambiguousDrainDecision := ambiguousDrainController.Resolve(highLag)
	assert.Equal(t, ambiguousDrainSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousDrainDecision.Decision)
	assert.Equal(
		t,
		drainStage2Cfg.PolicyManifestDigest,
		ambiguousDrainDecision.PolicyManifestDigest,
		"ambiguous backlog-drain markers must remain quarantined until bridge ownership tuple is complete",
	)
	assert.Equal(t, drainStage2Cfg.PolicyManifestRefreshEpoch, ambiguousDrainDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousDrainDecision.PolicyActivationTicks)

	liveBridgeSeed := ambiguousDrainController.currentBatch
	liveBridgeController := newAutoTuneControllerWithSeed(100, liveBridgeSeq3Cfg, &liveBridgeSeed)
	require.NotNil(t, liveBridgeController)
	liveBridgeController.reconcilePolicyTransition(ambiguousDrainController.exportPolicyTransition())
	batch, liveBridgeDecision := liveBridgeController.Resolve(highLag)
	assert.Equal(t, liveBridgeSeed+20, batch)
	assert.Equal(t, "apply_increase", liveBridgeDecision.Decision)
	assert.Equal(t, liveBridgeSeq3Cfg.PolicyManifestDigest, liveBridgeDecision.PolicyManifestDigest)
	assert.Equal(t, liveBridgeSeq3Cfg.PolicyManifestRefreshEpoch, liveBridgeDecision.PolicyEpoch)
	assert.Equal(t, 0, liveBridgeDecision.PolicyActivationTicks)

	staleAfterLiveSeed := liveBridgeController.currentBatch
	staleAfterLiveController := newAutoTuneControllerWithSeed(100, staleDrainedSeq2AfterLiveCfg, &staleAfterLiveSeed)
	require.NotNil(t, staleAfterLiveController)
	staleAfterLiveController.reconcilePolicyTransition(liveBridgeController.exportPolicyTransition())
	batch, staleAfterLiveDecision := staleAfterLiveController.Resolve(highLag)
	assert.Equal(t, staleAfterLiveSeed+20, batch)
	assert.Equal(t, "apply_increase", staleAfterLiveDecision.Decision)
	assert.Equal(
		t,
		liveBridgeSeq3Cfg.PolicyManifestDigest,
		staleAfterLiveDecision.PolicyManifestDigest,
		"delayed drained markers must not reclaim ownership once a newer live bridge sequence is verified",
	)
	assert.Equal(t, liveBridgeSeq3Cfg.PolicyManifestRefreshEpoch, staleAfterLiveDecision.PolicyEpoch)
	assert.Equal(t, 0, staleAfterLiveDecision.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostBacklogDrainLiveCatchupOrdersByLiveHeadAndQuarantinesAmbiguousMarkers(t *testing.T) {
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
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	lateBridgeSeq2Cfg := quarantineCfg
	lateBridgeSeq2Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=7|rollback-fence-late-bridge-seq=2|rollback-fence-late-bridge-release-watermark=80"
	drainStage2Cfg := lateBridgeSeq2Cfg
	drainStage2Cfg.PolicyManifestDigest = lateBridgeSeq2Cfg.PolicyManifestDigest + "|rollback-fence-late-bridge-drain-watermark=82"
	liveCatchupHead100Cfg := quarantineCfg
	liveCatchupHead100Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	staleLiveCatchupHead95Cfg := liveCatchupHead100Cfg
	staleLiveCatchupHead95Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=125"
	advancedLiveCatchupHead130Cfg := liveCatchupHead100Cfg
	advancedLiveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=150"
	ambiguousLiveCatchupCfg := quarantineCfg
	ambiguousLiveCatchupCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=4|rollback-fence-late-bridge-release-watermark=120|rollback-fence-live-head=140"

	baseSeed := 220
	baseController := newAutoTuneControllerWithSeed(100, drainStage2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, drainStage2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, drainStage2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)

	liveCatchupSeed := baseController.currentBatch
	liveCatchupController := newAutoTuneControllerWithSeed(100, liveCatchupHead100Cfg, &liveCatchupSeed)
	require.NotNil(t, liveCatchupController)
	liveCatchupController.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, liveCatchupDecision := liveCatchupController.Resolve(highLag)
	assert.Equal(t, liveCatchupSeed+20, batch)
	assert.Equal(t, "apply_increase", liveCatchupDecision.Decision)
	assert.Equal(t, liveCatchupHead100Cfg.PolicyManifestDigest, liveCatchupDecision.PolicyManifestDigest)
	assert.Equal(t, liveCatchupHead100Cfg.PolicyManifestRefreshEpoch, liveCatchupDecision.PolicyEpoch)
	assert.Equal(t, 0, liveCatchupDecision.PolicyActivationTicks)

	staleLiveCatchupSeed := liveCatchupController.currentBatch
	staleLiveCatchupController := newAutoTuneControllerWithSeed(100, staleLiveCatchupHead95Cfg, &staleLiveCatchupSeed)
	require.NotNil(t, staleLiveCatchupController)
	staleLiveCatchupController.reconcilePolicyTransition(liveCatchupController.exportPolicyTransition())
	batch, staleLiveCatchupDecision := staleLiveCatchupController.Resolve(highLag)
	assert.Equal(t, staleLiveCatchupSeed+20, batch)
	assert.Equal(t, "apply_increase", staleLiveCatchupDecision.Decision)
	assert.Equal(
		t,
		liveCatchupHead100Cfg.PolicyManifestDigest,
		staleLiveCatchupDecision.PolicyManifestDigest,
		"lower live-head markers must remain pinned behind the latest verified drain-to-live handoff ownership",
	)
	assert.Equal(t, liveCatchupHead100Cfg.PolicyManifestRefreshEpoch, staleLiveCatchupDecision.PolicyEpoch)
	assert.Equal(t, 0, staleLiveCatchupDecision.PolicyActivationTicks)

	ambiguousLiveCatchupSeed := staleLiveCatchupController.currentBatch
	ambiguousLiveCatchupController := newAutoTuneControllerWithSeed(100, ambiguousLiveCatchupCfg, &ambiguousLiveCatchupSeed)
	require.NotNil(t, ambiguousLiveCatchupController)
	ambiguousLiveCatchupController.reconcilePolicyTransition(staleLiveCatchupController.exportPolicyTransition())
	batch, ambiguousLiveCatchupDecision := ambiguousLiveCatchupController.Resolve(highLag)
	assert.Equal(t, ambiguousLiveCatchupSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousLiveCatchupDecision.Decision)
	assert.Equal(
		t,
		liveCatchupHead100Cfg.PolicyManifestDigest,
		ambiguousLiveCatchupDecision.PolicyManifestDigest,
		"live-head markers must remain quarantined until drain watermark ownership is explicit",
	)
	assert.Equal(t, liveCatchupHead100Cfg.PolicyManifestRefreshEpoch, ambiguousLiveCatchupDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousLiveCatchupDecision.PolicyActivationTicks)

	advancedLiveCatchupSeed := ambiguousLiveCatchupController.currentBatch
	advancedLiveCatchupController := newAutoTuneControllerWithSeed(100, advancedLiveCatchupHead130Cfg, &advancedLiveCatchupSeed)
	require.NotNil(t, advancedLiveCatchupController)
	advancedLiveCatchupController.reconcilePolicyTransition(ambiguousLiveCatchupController.exportPolicyTransition())
	batch, advancedLiveCatchupDecision := advancedLiveCatchupController.Resolve(highLag)
	assert.Equal(t, advancedLiveCatchupSeed+20, batch)
	assert.Equal(t, "apply_increase", advancedLiveCatchupDecision.Decision)
	assert.Equal(t, advancedLiveCatchupHead130Cfg.PolicyManifestDigest, advancedLiveCatchupDecision.PolicyManifestDigest)
	assert.Equal(t, advancedLiveCatchupHead130Cfg.PolicyManifestRefreshEpoch, advancedLiveCatchupDecision.PolicyEpoch)
	assert.Equal(t, 0, advancedLiveCatchupDecision.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostLiveCatchupSteadyStateRebaselineOrdersBySteadyWatermarkAndQuarantinesAmbiguousMarkers(t *testing.T) {
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
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	lateBridgeSeq2Cfg := quarantineCfg
	lateBridgeSeq2Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=7|rollback-fence-late-bridge-seq=2|rollback-fence-late-bridge-release-watermark=80"
	drainStage2Cfg := lateBridgeSeq2Cfg
	drainStage2Cfg.PolicyManifestDigest = lateBridgeSeq2Cfg.PolicyManifestDigest + "|rollback-fence-late-bridge-drain-watermark=82"
	liveCatchupHead130Cfg := quarantineCfg
	liveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	steadyState131Cfg := liveCatchupHead130Cfg
	steadyState131Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=131"
	staleSteadyState130Cfg := liveCatchupHead130Cfg
	staleSteadyState130Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=130"
	advancedSteadyState145Cfg := liveCatchupHead130Cfg
	advancedSteadyState145Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=145"
	ambiguousSteadyStateCfg := quarantineCfg
	ambiguousSteadyStateCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-steady-state-watermark=140"

	baseSeed := 220
	baseController := newAutoTuneControllerWithSeed(100, drainStage2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, drainStage2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, drainStage2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)

	liveCatchupSeed := baseController.currentBatch
	liveCatchupController := newAutoTuneControllerWithSeed(100, liveCatchupHead130Cfg, &liveCatchupSeed)
	require.NotNil(t, liveCatchupController)
	liveCatchupController.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, liveCatchupDecision := liveCatchupController.Resolve(highLag)
	assert.Equal(t, liveCatchupSeed+20, batch)
	assert.Equal(t, "apply_increase", liveCatchupDecision.Decision)
	assert.Equal(t, liveCatchupHead130Cfg.PolicyManifestDigest, liveCatchupDecision.PolicyManifestDigest)
	assert.Equal(t, liveCatchupHead130Cfg.PolicyManifestRefreshEpoch, liveCatchupDecision.PolicyEpoch)
	assert.Equal(t, 0, liveCatchupDecision.PolicyActivationTicks)

	steadyStateSeed := liveCatchupController.currentBatch
	steadyStateController := newAutoTuneControllerWithSeed(100, steadyState131Cfg, &steadyStateSeed)
	require.NotNil(t, steadyStateController)
	steadyStateController.reconcilePolicyTransition(liveCatchupController.exportPolicyTransition())
	batch, steadyStateDecision := steadyStateController.Resolve(highLag)
	assert.Equal(t, steadyStateSeed+20, batch)
	assert.Equal(t, "apply_increase", steadyStateDecision.Decision)
	assert.Equal(t, steadyState131Cfg.PolicyManifestDigest, steadyStateDecision.PolicyManifestDigest)
	assert.Equal(t, steadyState131Cfg.PolicyManifestRefreshEpoch, steadyStateDecision.PolicyEpoch)
	assert.Equal(t, 0, steadyStateDecision.PolicyActivationTicks)

	staleSteadySeed := steadyStateController.currentBatch
	staleSteadyController := newAutoTuneControllerWithSeed(100, staleSteadyState130Cfg, &staleSteadySeed)
	require.NotNil(t, staleSteadyController)
	staleSteadyController.reconcilePolicyTransition(steadyStateController.exportPolicyTransition())
	batch, staleSteadyDecision := staleSteadyController.Resolve(highLag)
	assert.Equal(t, staleSteadySeed+20, batch)
	assert.Equal(t, "apply_increase", staleSteadyDecision.Decision)
	assert.Equal(
		t,
		steadyState131Cfg.PolicyManifestDigest,
		staleSteadyDecision.PolicyManifestDigest,
		"lower steady-state rebaseline markers must remain pinned behind the latest verified steady-state ownership",
	)
	assert.Equal(t, steadyState131Cfg.PolicyManifestRefreshEpoch, staleSteadyDecision.PolicyEpoch)
	assert.Equal(t, 0, staleSteadyDecision.PolicyActivationTicks)

	ambiguousSteadySeed := staleSteadyController.currentBatch
	ambiguousSteadyController := newAutoTuneControllerWithSeed(100, ambiguousSteadyStateCfg, &ambiguousSteadySeed)
	require.NotNil(t, ambiguousSteadyController)
	ambiguousSteadyController.reconcilePolicyTransition(staleSteadyController.exportPolicyTransition())
	batch, ambiguousSteadyDecision := ambiguousSteadyController.Resolve(highLag)
	assert.Equal(t, ambiguousSteadySeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousSteadyDecision.Decision)
	assert.Equal(
		t,
		steadyState131Cfg.PolicyManifestDigest,
		ambiguousSteadyDecision.PolicyManifestDigest,
		"steady-state rebaseline markers must remain quarantined until live-catchup ownership is explicit",
	)
	assert.Equal(t, steadyState131Cfg.PolicyManifestRefreshEpoch, ambiguousSteadyDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousSteadyDecision.PolicyActivationTicks)

	staleLiveSeed := ambiguousSteadyController.currentBatch
	staleLiveController := newAutoTuneControllerWithSeed(100, liveCatchupHead130Cfg, &staleLiveSeed)
	require.NotNil(t, staleLiveController)
	staleLiveController.reconcilePolicyTransition(ambiguousSteadyController.exportPolicyTransition())
	batch, staleLiveDecision := staleLiveController.Resolve(highLag)
	assert.Equal(t, staleLiveSeed+20, batch)
	assert.Equal(t, "apply_increase", staleLiveDecision.Decision)
	assert.Equal(
		t,
		steadyState131Cfg.PolicyManifestDigest,
		staleLiveDecision.PolicyManifestDigest,
		"post-rebaseline stale live-catchup markers must not reclaim ownership",
	)
	assert.Equal(t, steadyState131Cfg.PolicyManifestRefreshEpoch, staleLiveDecision.PolicyEpoch)
	assert.Equal(t, 0, staleLiveDecision.PolicyActivationTicks)

	advancedSteadySeed := staleLiveController.currentBatch
	advancedSteadyController := newAutoTuneControllerWithSeed(100, advancedSteadyState145Cfg, &advancedSteadySeed)
	require.NotNil(t, advancedSteadyController)
	advancedSteadyController.reconcilePolicyTransition(staleLiveController.exportPolicyTransition())
	batch, advancedSteadyDecision := advancedSteadyController.Resolve(highLag)
	assert.Equal(t, advancedSteadySeed+20, batch)
	assert.Equal(t, "apply_increase", advancedSteadyDecision.Decision)
	assert.Equal(t, advancedSteadyState145Cfg.PolicyManifestDigest, advancedSteadyDecision.PolicyManifestDigest)
	assert.Equal(t, advancedSteadyState145Cfg.PolicyManifestRefreshEpoch, advancedSteadyDecision.PolicyEpoch)
	assert.Equal(t, 0, advancedSteadyDecision.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostRebaselineBaselineRotationOrdersBySteadyGenerationAndQuarantinesAmbiguousMarkers(t *testing.T) {
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
	rollbackCfg := segment2Cfg
	rollbackCfg.PolicyManifestDigest = "manifest-tail-v2b|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3"
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	lateBridgeSeq2Cfg := quarantineCfg
	lateBridgeSeq2Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=7|rollback-fence-late-bridge-seq=2|rollback-fence-late-bridge-release-watermark=80"
	drainStage2Cfg := lateBridgeSeq2Cfg
	drainStage2Cfg.PolicyManifestDigest = lateBridgeSeq2Cfg.PolicyManifestDigest + "|rollback-fence-late-bridge-drain-watermark=82"
	liveCatchupHead130Cfg := quarantineCfg
	liveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	advancedSteadyState145Cfg := liveCatchupHead130Cfg
	advancedSteadyState145Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=145"
	steadyGeneration1Cfg := advancedSteadyState145Cfg
	steadyGeneration1Cfg.PolicyManifestDigest = advancedSteadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=1"
	staleSteadyGeneration0Cfg := advancedSteadyState145Cfg
	staleSteadyGeneration0Cfg.PolicyManifestDigest = advancedSteadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=0"
	steadyGeneration2Cfg := advancedSteadyState145Cfg
	steadyGeneration2Cfg.PolicyManifestDigest = advancedSteadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=2"
	ambiguousGenerationCfg := liveCatchupHead130Cfg
	ambiguousGenerationCfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=3"
	stalePreRotationCfg := advancedSteadyState145Cfg

	baseSeed := 220
	baseController := newAutoTuneControllerWithSeed(100, drainStage2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, drainStage2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, drainStage2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)

	liveCatchupSeed := baseController.currentBatch
	liveCatchupController := newAutoTuneControllerWithSeed(100, liveCatchupHead130Cfg, &liveCatchupSeed)
	require.NotNil(t, liveCatchupController)
	liveCatchupController.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, liveCatchupDecision := liveCatchupController.Resolve(highLag)
	assert.Equal(t, liveCatchupSeed+20, batch)
	assert.Equal(t, "apply_increase", liveCatchupDecision.Decision)
	assert.Equal(t, liveCatchupHead130Cfg.PolicyManifestDigest, liveCatchupDecision.PolicyManifestDigest)
	assert.Equal(t, liveCatchupHead130Cfg.PolicyManifestRefreshEpoch, liveCatchupDecision.PolicyEpoch)
	assert.Equal(t, 0, liveCatchupDecision.PolicyActivationTicks)

	rotationGen1Seed := liveCatchupController.currentBatch
	rotationGen1Controller := newAutoTuneControllerWithSeed(100, steadyGeneration1Cfg, &rotationGen1Seed)
	require.NotNil(t, rotationGen1Controller)
	rotationGen1Controller.reconcilePolicyTransition(liveCatchupController.exportPolicyTransition())
	batch, rotationGen1Decision := rotationGen1Controller.Resolve(highLag)
	assert.Equal(t, rotationGen1Seed+20, batch)
	assert.Equal(t, "apply_increase", rotationGen1Decision.Decision)
	assert.Equal(t, steadyGeneration1Cfg.PolicyManifestDigest, rotationGen1Decision.PolicyManifestDigest)
	assert.Equal(t, steadyGeneration1Cfg.PolicyManifestRefreshEpoch, rotationGen1Decision.PolicyEpoch)
	assert.Equal(t, 0, rotationGen1Decision.PolicyActivationTicks)

	staleGen0Seed := rotationGen1Controller.currentBatch
	staleGen0Controller := newAutoTuneControllerWithSeed(100, staleSteadyGeneration0Cfg, &staleGen0Seed)
	require.NotNil(t, staleGen0Controller)
	staleGen0Controller.reconcilePolicyTransition(rotationGen1Controller.exportPolicyTransition())
	batch, staleGen0Decision := staleGen0Controller.Resolve(highLag)
	assert.Equal(t, staleGen0Seed+20, batch)
	assert.Equal(t, "apply_increase", staleGen0Decision.Decision)
	assert.Equal(
		t,
		steadyGeneration1Cfg.PolicyManifestDigest,
		staleGen0Decision.PolicyManifestDigest,
		"lower steady-generation markers must remain pinned behind the latest verified baseline-rotation ownership",
	)
	assert.Equal(t, steadyGeneration1Cfg.PolicyManifestRefreshEpoch, staleGen0Decision.PolicyEpoch)
	assert.Equal(t, 0, staleGen0Decision.PolicyActivationTicks)

	ambiguousGenerationSeed := staleGen0Controller.currentBatch
	ambiguousGenerationController := newAutoTuneControllerWithSeed(100, ambiguousGenerationCfg, &ambiguousGenerationSeed)
	require.NotNil(t, ambiguousGenerationController)
	ambiguousGenerationController.reconcilePolicyTransition(staleGen0Controller.exportPolicyTransition())
	batch, ambiguousGenerationDecision := ambiguousGenerationController.Resolve(highLag)
	assert.Equal(t, ambiguousGenerationSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousGenerationDecision.Decision)
	assert.Equal(
		t,
		steadyGeneration1Cfg.PolicyManifestDigest,
		ambiguousGenerationDecision.PolicyManifestDigest,
		"steady-generation rotation markers must remain quarantined until explicit steady-state ownership is present",
	)
	assert.Equal(t, steadyGeneration1Cfg.PolicyManifestRefreshEpoch, ambiguousGenerationDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousGenerationDecision.PolicyActivationTicks)

	stalePreRotationSeed := ambiguousGenerationController.currentBatch
	stalePreRotationController := newAutoTuneControllerWithSeed(100, stalePreRotationCfg, &stalePreRotationSeed)
	require.NotNil(t, stalePreRotationController)
	stalePreRotationController.reconcilePolicyTransition(ambiguousGenerationController.exportPolicyTransition())
	batch, stalePreRotationDecision := stalePreRotationController.Resolve(highLag)
	assert.Equal(t, stalePreRotationSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreRotationDecision.Decision)
	assert.Equal(
		t,
		steadyGeneration1Cfg.PolicyManifestDigest,
		stalePreRotationDecision.PolicyManifestDigest,
		"post-rotation stale pre-rotation markers must not reclaim ownership",
	)
	assert.Equal(t, steadyGeneration1Cfg.PolicyManifestRefreshEpoch, stalePreRotationDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreRotationDecision.PolicyActivationTicks)

	rotationGen2Seed := stalePreRotationController.currentBatch
	rotationGen2Controller := newAutoTuneControllerWithSeed(100, steadyGeneration2Cfg, &rotationGen2Seed)
	require.NotNil(t, rotationGen2Controller)
	rotationGen2Controller.reconcilePolicyTransition(stalePreRotationController.exportPolicyTransition())
	batch, rotationGen2Decision := rotationGen2Controller.Resolve(highLag)
	assert.Equal(t, rotationGen2Seed+20, batch)
	assert.Equal(t, "apply_increase", rotationGen2Decision.Decision)
	assert.Equal(t, steadyGeneration2Cfg.PolicyManifestDigest, rotationGen2Decision.PolicyManifestDigest)
	assert.Equal(t, steadyGeneration2Cfg.PolicyManifestRefreshEpoch, rotationGen2Decision.PolicyEpoch)
	assert.Equal(t, 0, rotationGen2Decision.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostBaselineRotationGenerationPruneRejectsRetiredMarkers(t *testing.T) {
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
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	lateBridgeSeq2Cfg := quarantineCfg
	lateBridgeSeq2Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=7|rollback-fence-late-bridge-seq=2|rollback-fence-late-bridge-release-watermark=80"
	drainStage2Cfg := lateBridgeSeq2Cfg
	drainStage2Cfg.PolicyManifestDigest = lateBridgeSeq2Cfg.PolicyManifestDigest + "|rollback-fence-late-bridge-drain-watermark=82"
	liveCatchupHead130Cfg := quarantineCfg
	liveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	steadyState145Cfg := liveCatchupHead130Cfg
	steadyState145Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=145"
	steadyGeneration2Cfg := steadyState145Cfg
	steadyGeneration2Cfg.PolicyManifestDigest = steadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=2"
	pruneFloor1Cfg := steadyGeneration2Cfg
	pruneFloor1Cfg.PolicyManifestDigest = steadyGeneration2Cfg.PolicyManifestDigest + "|rollback-fence-generation-retention-floor=1"
	pruneFloor2Cfg := steadyGeneration2Cfg
	pruneFloor2Cfg.PolicyManifestDigest = steadyGeneration2Cfg.PolicyManifestDigest + "|rollback-fence-generation-retention-floor=2"
	staleRetiredGeneration1Cfg := steadyState145Cfg
	staleRetiredGeneration1Cfg.PolicyManifestDigest = steadyState145Cfg.PolicyManifestDigest +
		"|rollback-fence-steady-generation=1|rollback-fence-generation-retention-floor=2"
	ambiguousPruneCfg := steadyState145Cfg
	ambiguousPruneCfg.PolicyManifestDigest = steadyState145Cfg.PolicyManifestDigest + "|rollback-fence-generation-retention-floor=2"
	stalePrePruneCfg := steadyGeneration2Cfg

	baseSeed := 220
	baseController := newAutoTuneControllerWithSeed(100, steadyGeneration2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, steadyGeneration2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, steadyGeneration2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	pruneFloor1Seed := baseController.currentBatch
	pruneFloor1Controller := newAutoTuneControllerWithSeed(100, pruneFloor1Cfg, &pruneFloor1Seed)
	require.NotNil(t, pruneFloor1Controller)
	pruneFloor1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, pruneFloor1Decision := pruneFloor1Controller.Resolve(highLag)
	assert.Equal(t, pruneFloor1Seed+20, batch)
	assert.Equal(t, "apply_increase", pruneFloor1Decision.Decision)
	assert.Equal(t, pruneFloor1Cfg.PolicyManifestDigest, pruneFloor1Decision.PolicyManifestDigest)
	assert.Equal(t, pruneFloor1Cfg.PolicyManifestRefreshEpoch, pruneFloor1Decision.PolicyEpoch)
	assert.Equal(t, 0, pruneFloor1Decision.PolicyActivationTicks)

	pruneFloor2Seed := pruneFloor1Controller.currentBatch
	pruneFloor2Controller := newAutoTuneControllerWithSeed(100, pruneFloor2Cfg, &pruneFloor2Seed)
	require.NotNil(t, pruneFloor2Controller)
	pruneFloor2Controller.reconcilePolicyTransition(pruneFloor1Controller.exportPolicyTransition())
	batch, pruneFloor2Decision := pruneFloor2Controller.Resolve(highLag)
	assert.Equal(t, pruneFloor2Seed+20, batch)
	assert.Equal(t, "apply_increase", pruneFloor2Decision.Decision)
	assert.Equal(t, pruneFloor2Cfg.PolicyManifestDigest, pruneFloor2Decision.PolicyManifestDigest)
	assert.Equal(t, pruneFloor2Cfg.PolicyManifestRefreshEpoch, pruneFloor2Decision.PolicyEpoch)
	assert.Equal(t, 0, pruneFloor2Decision.PolicyActivationTicks)

	staleRetiredSeed := pruneFloor2Controller.currentBatch
	staleRetiredController := newAutoTuneControllerWithSeed(100, staleRetiredGeneration1Cfg, &staleRetiredSeed)
	require.NotNil(t, staleRetiredController)
	staleRetiredController.reconcilePolicyTransition(pruneFloor2Controller.exportPolicyTransition())
	batch, staleRetiredDecision := staleRetiredController.Resolve(highLag)
	assert.Equal(t, staleRetiredSeed+20, batch)
	assert.Equal(t, "apply_increase", staleRetiredDecision.Decision)
	assert.Equal(
		t,
		pruneFloor2Cfg.PolicyManifestDigest,
		staleRetiredDecision.PolicyManifestDigest,
		"retired-generation markers below the retention floor must stay pinned behind the latest verified generation-prune ownership",
	)
	assert.Equal(t, pruneFloor2Cfg.PolicyManifestRefreshEpoch, staleRetiredDecision.PolicyEpoch)
	assert.Equal(t, 0, staleRetiredDecision.PolicyActivationTicks)

	ambiguousPruneSeed := staleRetiredController.currentBatch
	ambiguousPruneController := newAutoTuneControllerWithSeed(100, ambiguousPruneCfg, &ambiguousPruneSeed)
	require.NotNil(t, ambiguousPruneController)
	ambiguousPruneController.reconcilePolicyTransition(staleRetiredController.exportPolicyTransition())
	batch, ambiguousPruneDecision := ambiguousPruneController.Resolve(highLag)
	assert.Equal(t, ambiguousPruneSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousPruneDecision.Decision)
	assert.Equal(
		t,
		pruneFloor2Cfg.PolicyManifestDigest,
		ambiguousPruneDecision.PolicyManifestDigest,
		"generation-prune markers must remain quarantined until steady-generation ownership is explicit",
	)
	assert.Equal(t, pruneFloor2Cfg.PolicyManifestRefreshEpoch, ambiguousPruneDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousPruneDecision.PolicyActivationTicks)

	stalePrePruneSeed := ambiguousPruneController.currentBatch
	stalePrePruneController := newAutoTuneControllerWithSeed(100, stalePrePruneCfg, &stalePrePruneSeed)
	require.NotNil(t, stalePrePruneController)
	stalePrePruneController.reconcilePolicyTransition(ambiguousPruneController.exportPolicyTransition())
	batch, stalePrePruneDecision := stalePrePruneController.Resolve(highLag)
	assert.Equal(t, stalePrePruneSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePrePruneDecision.Decision)
	assert.Equal(
		t,
		pruneFloor2Cfg.PolicyManifestDigest,
		stalePrePruneDecision.PolicyManifestDigest,
		"pre-prune markers without retention-floor ownership must not reclaim control after prune commitment",
	)
	assert.Equal(t, pruneFloor2Cfg.PolicyManifestRefreshEpoch, stalePrePruneDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePrePruneDecision.PolicyActivationTicks)

	reForwardSeed := stalePrePruneController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePrePruneController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after generation-prune must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostGenerationPruneRetentionFloorLiftRejectsPreLiftMarkers(t *testing.T) {
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
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	liveCatchupHead130Cfg := quarantineCfg
	liveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	steadyState145Cfg := liveCatchupHead130Cfg
	steadyState145Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=145"
	steadyGeneration2Cfg := steadyState145Cfg
	steadyGeneration2Cfg.PolicyManifestDigest = steadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=2"
	pruneFloor2Cfg := steadyGeneration2Cfg
	pruneFloor2Cfg.PolicyManifestDigest = steadyGeneration2Cfg.PolicyManifestDigest + "|rollback-fence-generation-retention-floor=2"
	floorLiftEpoch1Cfg := pruneFloor2Cfg
	floorLiftEpoch1Cfg.PolicyManifestDigest = pruneFloor2Cfg.PolicyManifestDigest + "|rollback-fence-floor-lift-epoch=1"
	floorLiftEpoch2Cfg := pruneFloor2Cfg
	floorLiftEpoch2Cfg.PolicyManifestDigest = pruneFloor2Cfg.PolicyManifestDigest + "|rollback-fence-floor-lift-epoch=2"
	ambiguousFloorLiftCfg := steadyGeneration2Cfg
	ambiguousFloorLiftCfg.PolicyManifestDigest = steadyGeneration2Cfg.PolicyManifestDigest + "|rollback-fence-floor-lift-epoch=3"
	stalePreLiftCfg := pruneFloor2Cfg

	baseSeed := 220
	baseController := newAutoTuneControllerWithSeed(100, pruneFloor2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, pruneFloor2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, pruneFloor2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	floorLift1Seed := baseController.currentBatch
	floorLift1Controller := newAutoTuneControllerWithSeed(100, floorLiftEpoch1Cfg, &floorLift1Seed)
	require.NotNil(t, floorLift1Controller)
	floorLift1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, floorLift1Decision := floorLift1Controller.Resolve(highLag)
	assert.Equal(t, floorLift1Seed+20, batch)
	assert.Equal(t, "apply_increase", floorLift1Decision.Decision)
	assert.Equal(t, floorLiftEpoch1Cfg.PolicyManifestDigest, floorLift1Decision.PolicyManifestDigest)
	assert.Equal(t, floorLiftEpoch1Cfg.PolicyManifestRefreshEpoch, floorLift1Decision.PolicyEpoch)
	assert.Equal(t, 0, floorLift1Decision.PolicyActivationTicks)

	floorLift2Seed := floorLift1Controller.currentBatch
	floorLift2Controller := newAutoTuneControllerWithSeed(100, floorLiftEpoch2Cfg, &floorLift2Seed)
	require.NotNil(t, floorLift2Controller)
	floorLift2Controller.reconcilePolicyTransition(floorLift1Controller.exportPolicyTransition())
	batch, floorLift2Decision := floorLift2Controller.Resolve(highLag)
	assert.Equal(t, floorLift2Seed+20, batch)
	assert.Equal(t, "apply_increase", floorLift2Decision.Decision)
	assert.Equal(t, floorLiftEpoch2Cfg.PolicyManifestDigest, floorLift2Decision.PolicyManifestDigest)
	assert.Equal(t, floorLiftEpoch2Cfg.PolicyManifestRefreshEpoch, floorLift2Decision.PolicyEpoch)
	assert.Equal(t, 0, floorLift2Decision.PolicyActivationTicks)

	staleLiftSeed := floorLift2Controller.currentBatch
	staleLiftController := newAutoTuneControllerWithSeed(100, floorLiftEpoch1Cfg, &staleLiftSeed)
	require.NotNil(t, staleLiftController)
	staleLiftController.reconcilePolicyTransition(floorLift2Controller.exportPolicyTransition())
	batch, staleLiftDecision := staleLiftController.Resolve(highLag)
	assert.Equal(t, staleLiftSeed+20, batch)
	assert.Equal(t, "apply_increase", staleLiftDecision.Decision)
	assert.Equal(
		t,
		floorLiftEpoch2Cfg.PolicyManifestDigest,
		staleLiftDecision.PolicyManifestDigest,
		"lower floor-lift epochs must remain pinned behind the latest verified retention-floor-lift ownership",
	)
	assert.Equal(t, floorLiftEpoch2Cfg.PolicyManifestRefreshEpoch, staleLiftDecision.PolicyEpoch)
	assert.Equal(t, 0, staleLiftDecision.PolicyActivationTicks)

	ambiguousLiftSeed := staleLiftController.currentBatch
	ambiguousLiftController := newAutoTuneControllerWithSeed(100, ambiguousFloorLiftCfg, &ambiguousLiftSeed)
	require.NotNil(t, ambiguousLiftController)
	ambiguousLiftController.reconcilePolicyTransition(staleLiftController.exportPolicyTransition())
	batch, ambiguousLiftDecision := ambiguousLiftController.Resolve(highLag)
	assert.Equal(t, ambiguousLiftSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousLiftDecision.Decision)
	assert.Equal(
		t,
		floorLiftEpoch2Cfg.PolicyManifestDigest,
		ambiguousLiftDecision.PolicyManifestDigest,
		"retention-floor-lift markers must remain quarantined until generation-retention-floor ownership is explicit",
	)
	assert.Equal(t, floorLiftEpoch2Cfg.PolicyManifestRefreshEpoch, ambiguousLiftDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousLiftDecision.PolicyActivationTicks)

	stalePreLiftSeed := ambiguousLiftController.currentBatch
	stalePreLiftController := newAutoTuneControllerWithSeed(100, stalePreLiftCfg, &stalePreLiftSeed)
	require.NotNil(t, stalePreLiftController)
	stalePreLiftController.reconcilePolicyTransition(ambiguousLiftController.exportPolicyTransition())
	batch, stalePreLiftDecision := stalePreLiftController.Resolve(highLag)
	assert.Equal(t, stalePreLiftSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreLiftDecision.Decision)
	assert.Equal(
		t,
		floorLiftEpoch2Cfg.PolicyManifestDigest,
		stalePreLiftDecision.PolicyManifestDigest,
		"post-lift markers without floor-lift ownership must not reclaim control after lift commitment",
	)
	assert.Equal(t, floorLiftEpoch2Cfg.PolicyManifestRefreshEpoch, stalePreLiftDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreLiftDecision.PolicyActivationTicks)

	reForwardSeed := stalePreLiftController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreLiftController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after retention-floor-lift must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostRetentionFloorLiftSettleWindowRejectsPreSettleMarkers(t *testing.T) {
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
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	liveCatchupHead130Cfg := quarantineCfg
	liveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	steadyState145Cfg := liveCatchupHead130Cfg
	steadyState145Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=145"
	steadyGeneration2Cfg := steadyState145Cfg
	steadyGeneration2Cfg.PolicyManifestDigest = steadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=2"
	pruneFloor2Cfg := steadyGeneration2Cfg
	pruneFloor2Cfg.PolicyManifestDigest = steadyGeneration2Cfg.PolicyManifestDigest + "|rollback-fence-generation-retention-floor=2"
	floorLift2Cfg := pruneFloor2Cfg
	floorLift2Cfg.PolicyManifestDigest = pruneFloor2Cfg.PolicyManifestDigest + "|rollback-fence-floor-lift-epoch=2"
	settleWindow1Cfg := floorLift2Cfg
	settleWindow1Cfg.PolicyManifestDigest = floorLift2Cfg.PolicyManifestDigest + "|rollback-fence-settle-window-epoch=1"
	settleWindow2Cfg := floorLift2Cfg
	settleWindow2Cfg.PolicyManifestDigest = floorLift2Cfg.PolicyManifestDigest + "|rollback-fence-settle-window-epoch=2"
	ambiguousSettleWindowCfg := pruneFloor2Cfg
	ambiguousSettleWindowCfg.PolicyManifestDigest = pruneFloor2Cfg.PolicyManifestDigest + "|rollback-fence-settle-window-epoch=3"
	stalePreSettleCfg := floorLift2Cfg

	baseSeed := 220
	baseController := newAutoTuneControllerWithSeed(100, floorLift2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, floorLift2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, floorLift2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	settleWindow1Seed := baseController.currentBatch
	settleWindow1Controller := newAutoTuneControllerWithSeed(100, settleWindow1Cfg, &settleWindow1Seed)
	require.NotNil(t, settleWindow1Controller)
	settleWindow1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, settleWindow1Decision := settleWindow1Controller.Resolve(highLag)
	assert.Equal(t, settleWindow1Seed+20, batch)
	assert.Equal(t, "apply_increase", settleWindow1Decision.Decision)
	assert.Equal(t, settleWindow1Cfg.PolicyManifestDigest, settleWindow1Decision.PolicyManifestDigest)
	assert.Equal(t, settleWindow1Cfg.PolicyManifestRefreshEpoch, settleWindow1Decision.PolicyEpoch)
	assert.Equal(t, 0, settleWindow1Decision.PolicyActivationTicks)

	settleWindow2Seed := settleWindow1Controller.currentBatch
	settleWindow2Controller := newAutoTuneControllerWithSeed(100, settleWindow2Cfg, &settleWindow2Seed)
	require.NotNil(t, settleWindow2Controller)
	settleWindow2Controller.reconcilePolicyTransition(settleWindow1Controller.exportPolicyTransition())
	batch, settleWindow2Decision := settleWindow2Controller.Resolve(highLag)
	assert.Equal(t, settleWindow2Seed+20, batch)
	assert.Equal(t, "apply_increase", settleWindow2Decision.Decision)
	assert.Equal(t, settleWindow2Cfg.PolicyManifestDigest, settleWindow2Decision.PolicyManifestDigest)
	assert.Equal(t, settleWindow2Cfg.PolicyManifestRefreshEpoch, settleWindow2Decision.PolicyEpoch)
	assert.Equal(t, 0, settleWindow2Decision.PolicyActivationTicks)

	staleSettleSeed := settleWindow2Controller.currentBatch
	staleSettleController := newAutoTuneControllerWithSeed(100, settleWindow1Cfg, &staleSettleSeed)
	require.NotNil(t, staleSettleController)
	staleSettleController.reconcilePolicyTransition(settleWindow2Controller.exportPolicyTransition())
	batch, staleSettleDecision := staleSettleController.Resolve(highLag)
	assert.Equal(t, staleSettleSeed+20, batch)
	assert.Equal(t, "apply_increase", staleSettleDecision.Decision)
	assert.Equal(
		t,
		settleWindow2Cfg.PolicyManifestDigest,
		staleSettleDecision.PolicyManifestDigest,
		"lower settle-window epochs must remain pinned behind the latest verified settle-window ownership",
	)
	assert.Equal(t, settleWindow2Cfg.PolicyManifestRefreshEpoch, staleSettleDecision.PolicyEpoch)
	assert.Equal(t, 0, staleSettleDecision.PolicyActivationTicks)

	ambiguousSettleSeed := staleSettleController.currentBatch
	ambiguousSettleController := newAutoTuneControllerWithSeed(100, ambiguousSettleWindowCfg, &ambiguousSettleSeed)
	require.NotNil(t, ambiguousSettleController)
	ambiguousSettleController.reconcilePolicyTransition(staleSettleController.exportPolicyTransition())
	batch, ambiguousSettleDecision := ambiguousSettleController.Resolve(highLag)
	assert.Equal(t, ambiguousSettleSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousSettleDecision.Decision)
	assert.Equal(
		t,
		settleWindow2Cfg.PolicyManifestDigest,
		ambiguousSettleDecision.PolicyManifestDigest,
		"settle-window markers must remain quarantined until retention-floor-lift ownership is explicit",
	)
	assert.Equal(t, settleWindow2Cfg.PolicyManifestRefreshEpoch, ambiguousSettleDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousSettleDecision.PolicyActivationTicks)

	stalePreSettleSeed := ambiguousSettleController.currentBatch
	stalePreSettleController := newAutoTuneControllerWithSeed(100, stalePreSettleCfg, &stalePreSettleSeed)
	require.NotNil(t, stalePreSettleController)
	stalePreSettleController.reconcilePolicyTransition(ambiguousSettleController.exportPolicyTransition())
	batch, stalePreSettleDecision := stalePreSettleController.Resolve(highLag)
	assert.Equal(t, stalePreSettleSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreSettleDecision.Decision)
	assert.Equal(
		t,
		settleWindow2Cfg.PolicyManifestDigest,
		stalePreSettleDecision.PolicyManifestDigest,
		"post-settle stale pre-settle markers must not reclaim ownership",
	)
	assert.Equal(t, settleWindow2Cfg.PolicyManifestRefreshEpoch, stalePreSettleDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreSettleDecision.PolicyActivationTicks)

	reForwardSeed := stalePreSettleController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreSettleController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after settle-window must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostSettleWindowLateSpilloverRejectsStaleMarkers(t *testing.T) {
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
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	liveCatchupHead130Cfg := quarantineCfg
	liveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	steadyState145Cfg := liveCatchupHead130Cfg
	steadyState145Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=145"
	steadyGeneration2Cfg := steadyState145Cfg
	steadyGeneration2Cfg.PolicyManifestDigest = steadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=2"
	pruneFloor2Cfg := steadyGeneration2Cfg
	pruneFloor2Cfg.PolicyManifestDigest = steadyGeneration2Cfg.PolicyManifestDigest + "|rollback-fence-generation-retention-floor=2"
	floorLift2Cfg := pruneFloor2Cfg
	floorLift2Cfg.PolicyManifestDigest = pruneFloor2Cfg.PolicyManifestDigest + "|rollback-fence-floor-lift-epoch=2"
	settleWindow2Cfg := floorLift2Cfg
	settleWindow2Cfg.PolicyManifestDigest = floorLift2Cfg.PolicyManifestDigest + "|rollback-fence-settle-window-epoch=2"
	spillover1Cfg := settleWindow2Cfg
	spillover1Cfg.PolicyManifestDigest = settleWindow2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-epoch=1"
	spillover2Cfg := settleWindow2Cfg
	spillover2Cfg.PolicyManifestDigest = settleWindow2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-epoch=2"
	ambiguousSpilloverCfg := floorLift2Cfg
	ambiguousSpilloverCfg.PolicyManifestDigest = floorLift2Cfg.PolicyManifestDigest + "|rollback-fence-late-spillover-epoch=3"
	stalePreSpilloverCfg := settleWindow2Cfg

	baseSeed := 230
	baseController := newAutoTuneControllerWithSeed(100, settleWindow2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, settleWindow2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, settleWindow2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	spillover1Seed := baseController.currentBatch
	spillover1Controller := newAutoTuneControllerWithSeed(100, spillover1Cfg, &spillover1Seed)
	require.NotNil(t, spillover1Controller)
	spillover1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, spillover1Decision := spillover1Controller.Resolve(highLag)
	assert.Equal(t, spillover1Seed+20, batch)
	assert.Equal(t, "apply_increase", spillover1Decision.Decision)
	assert.Equal(t, spillover1Cfg.PolicyManifestDigest, spillover1Decision.PolicyManifestDigest)
	assert.Equal(t, spillover1Cfg.PolicyManifestRefreshEpoch, spillover1Decision.PolicyEpoch)
	assert.Equal(t, 0, spillover1Decision.PolicyActivationTicks)

	spillover2Seed := spillover1Controller.currentBatch
	spillover2Controller := newAutoTuneControllerWithSeed(100, spillover2Cfg, &spillover2Seed)
	require.NotNil(t, spillover2Controller)
	spillover2Controller.reconcilePolicyTransition(spillover1Controller.exportPolicyTransition())
	batch, spillover2Decision := spillover2Controller.Resolve(highLag)
	assert.Equal(t, spillover2Seed+20, batch)
	assert.Equal(t, "apply_increase", spillover2Decision.Decision)
	assert.Equal(t, spillover2Cfg.PolicyManifestDigest, spillover2Decision.PolicyManifestDigest)
	assert.Equal(t, spillover2Cfg.PolicyManifestRefreshEpoch, spillover2Decision.PolicyEpoch)
	assert.Equal(t, 0, spillover2Decision.PolicyActivationTicks)

	staleSpilloverSeed := spillover2Controller.currentBatch
	staleSpilloverController := newAutoTuneControllerWithSeed(100, spillover1Cfg, &staleSpilloverSeed)
	require.NotNil(t, staleSpilloverController)
	staleSpilloverController.reconcilePolicyTransition(spillover2Controller.exportPolicyTransition())
	batch, staleSpilloverDecision := staleSpilloverController.Resolve(highLag)
	assert.Equal(t, staleSpilloverSeed+20, batch)
	assert.Equal(t, "apply_increase", staleSpilloverDecision.Decision)
	assert.Equal(
		t,
		spillover2Cfg.PolicyManifestDigest,
		staleSpilloverDecision.PolicyManifestDigest,
		"lower spillover epochs must remain pinned behind the latest verified spillover ownership",
	)
	assert.Equal(t, spillover2Cfg.PolicyManifestRefreshEpoch, staleSpilloverDecision.PolicyEpoch)
	assert.Equal(t, 0, staleSpilloverDecision.PolicyActivationTicks)

	ambiguousSpilloverSeed := staleSpilloverController.currentBatch
	ambiguousSpilloverController := newAutoTuneControllerWithSeed(100, ambiguousSpilloverCfg, &ambiguousSpilloverSeed)
	require.NotNil(t, ambiguousSpilloverController)
	ambiguousSpilloverController.reconcilePolicyTransition(staleSpilloverController.exportPolicyTransition())
	batch, ambiguousSpilloverDecision := ambiguousSpilloverController.Resolve(highLag)
	assert.Equal(t, ambiguousSpilloverSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousSpilloverDecision.Decision)
	assert.Equal(
		t,
		spillover2Cfg.PolicyManifestDigest,
		ambiguousSpilloverDecision.PolicyManifestDigest,
		"late-spillover markers must remain quarantined until settle-window ownership is explicit",
	)
	assert.Equal(t, spillover2Cfg.PolicyManifestRefreshEpoch, ambiguousSpilloverDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousSpilloverDecision.PolicyActivationTicks)

	stalePreSpilloverSeed := ambiguousSpilloverController.currentBatch
	stalePreSpilloverController := newAutoTuneControllerWithSeed(100, stalePreSpilloverCfg, &stalePreSpilloverSeed)
	require.NotNil(t, stalePreSpilloverController)
	stalePreSpilloverController.reconcilePolicyTransition(ambiguousSpilloverController.exportPolicyTransition())
	batch, stalePreSpilloverDecision := stalePreSpilloverController.Resolve(highLag)
	assert.Equal(t, stalePreSpilloverSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreSpilloverDecision.Decision)
	assert.Equal(
		t,
		spillover2Cfg.PolicyManifestDigest,
		stalePreSpilloverDecision.PolicyManifestDigest,
		"post-spillover stale pre-spillover markers must not reclaim ownership",
	)
	assert.Equal(t, spillover2Cfg.PolicyManifestRefreshEpoch, stalePreSpilloverDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreSpilloverDecision.PolicyActivationTicks)

	reForwardSeed := stalePreSpilloverController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreSpilloverController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after spillover must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostLateSpilloverRejoinWindowRejectsStaleMarkers(t *testing.T) {
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
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	liveCatchupHead130Cfg := quarantineCfg
	liveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	steadyState145Cfg := liveCatchupHead130Cfg
	steadyState145Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=145"
	steadyGeneration2Cfg := steadyState145Cfg
	steadyGeneration2Cfg.PolicyManifestDigest = steadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=2"
	pruneFloor2Cfg := steadyGeneration2Cfg
	pruneFloor2Cfg.PolicyManifestDigest = steadyGeneration2Cfg.PolicyManifestDigest + "|rollback-fence-generation-retention-floor=2"
	floorLift2Cfg := pruneFloor2Cfg
	floorLift2Cfg.PolicyManifestDigest = pruneFloor2Cfg.PolicyManifestDigest + "|rollback-fence-floor-lift-epoch=2"
	settleWindow2Cfg := floorLift2Cfg
	settleWindow2Cfg.PolicyManifestDigest = floorLift2Cfg.PolicyManifestDigest + "|rollback-fence-settle-window-epoch=2"
	spillover2Cfg := settleWindow2Cfg
	spillover2Cfg.PolicyManifestDigest = settleWindow2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-epoch=2"
	rejoin1Cfg := spillover2Cfg
	rejoin1Cfg.PolicyManifestDigest = spillover2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-rejoin-epoch=1"
	rejoin2Cfg := spillover2Cfg
	rejoin2Cfg.PolicyManifestDigest = spillover2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-rejoin-epoch=2"
	ambiguousRejoinCfg := settleWindow2Cfg
	ambiguousRejoinCfg.PolicyManifestDigest = settleWindow2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-rejoin-epoch=3"
	stalePreRejoinCfg := spillover2Cfg

	baseSeed := 230
	baseController := newAutoTuneControllerWithSeed(100, spillover2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, spillover2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, spillover2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	rejoin1Seed := baseController.currentBatch
	rejoin1Controller := newAutoTuneControllerWithSeed(100, rejoin1Cfg, &rejoin1Seed)
	require.NotNil(t, rejoin1Controller)
	rejoin1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, rejoin1Decision := rejoin1Controller.Resolve(highLag)
	assert.Equal(t, rejoin1Seed+20, batch)
	assert.Equal(t, "apply_increase", rejoin1Decision.Decision)
	assert.Equal(t, rejoin1Cfg.PolicyManifestDigest, rejoin1Decision.PolicyManifestDigest)
	assert.Equal(t, rejoin1Cfg.PolicyManifestRefreshEpoch, rejoin1Decision.PolicyEpoch)
	assert.Equal(t, 0, rejoin1Decision.PolicyActivationTicks)

	rejoin2Seed := rejoin1Controller.currentBatch
	rejoin2Controller := newAutoTuneControllerWithSeed(100, rejoin2Cfg, &rejoin2Seed)
	require.NotNil(t, rejoin2Controller)
	rejoin2Controller.reconcilePolicyTransition(rejoin1Controller.exportPolicyTransition())
	batch, rejoin2Decision := rejoin2Controller.Resolve(highLag)
	assert.Equal(t, rejoin2Seed+20, batch)
	assert.Equal(t, "apply_increase", rejoin2Decision.Decision)
	assert.Equal(t, rejoin2Cfg.PolicyManifestDigest, rejoin2Decision.PolicyManifestDigest)
	assert.Equal(t, rejoin2Cfg.PolicyManifestRefreshEpoch, rejoin2Decision.PolicyEpoch)
	assert.Equal(t, 0, rejoin2Decision.PolicyActivationTicks)

	staleRejoinSeed := rejoin2Controller.currentBatch
	staleRejoinController := newAutoTuneControllerWithSeed(100, rejoin1Cfg, &staleRejoinSeed)
	require.NotNil(t, staleRejoinController)
	staleRejoinController.reconcilePolicyTransition(rejoin2Controller.exportPolicyTransition())
	batch, staleRejoinDecision := staleRejoinController.Resolve(highLag)
	assert.Equal(t, staleRejoinSeed+20, batch)
	assert.Equal(t, "apply_increase", staleRejoinDecision.Decision)
	assert.Equal(
		t,
		rejoin2Cfg.PolicyManifestDigest,
		staleRejoinDecision.PolicyManifestDigest,
		"lower rejoin-window epochs must remain pinned behind latest rejoin ownership",
	)
	assert.Equal(t, rejoin2Cfg.PolicyManifestRefreshEpoch, staleRejoinDecision.PolicyEpoch)
	assert.Equal(t, 0, staleRejoinDecision.PolicyActivationTicks)

	ambiguousRejoinSeed := staleRejoinController.currentBatch
	ambiguousRejoinController := newAutoTuneControllerWithSeed(100, ambiguousRejoinCfg, &ambiguousRejoinSeed)
	require.NotNil(t, ambiguousRejoinController)
	ambiguousRejoinController.reconcilePolicyTransition(staleRejoinController.exportPolicyTransition())
	batch, ambiguousRejoinDecision := ambiguousRejoinController.Resolve(highLag)
	assert.Equal(t, ambiguousRejoinSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousRejoinDecision.Decision)
	assert.Equal(
		t,
		rejoin2Cfg.PolicyManifestDigest,
		ambiguousRejoinDecision.PolicyManifestDigest,
		"rejoin-window markers must remain quarantined until spillover ownership is explicit",
	)
	assert.Equal(t, rejoin2Cfg.PolicyManifestRefreshEpoch, ambiguousRejoinDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousRejoinDecision.PolicyActivationTicks)

	stalePreRejoinSeed := ambiguousRejoinController.currentBatch
	stalePreRejoinController := newAutoTuneControllerWithSeed(100, stalePreRejoinCfg, &stalePreRejoinSeed)
	require.NotNil(t, stalePreRejoinController)
	stalePreRejoinController.reconcilePolicyTransition(ambiguousRejoinController.exportPolicyTransition())
	batch, stalePreRejoinDecision := stalePreRejoinController.Resolve(highLag)
	assert.Equal(t, stalePreRejoinSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreRejoinDecision.Decision)
	assert.Equal(
		t,
		rejoin2Cfg.PolicyManifestDigest,
		stalePreRejoinDecision.PolicyManifestDigest,
		"post-rejoin stale pre-rejoin markers must not reclaim ownership",
	)
	assert.Equal(t, rejoin2Cfg.PolicyManifestRefreshEpoch, stalePreRejoinDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreRejoinDecision.PolicyActivationTicks)

	reForwardSeed := stalePreRejoinController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreRejoinController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after rejoin-window must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostRejoinWindowSteadySealRejectsStaleMarkers(t *testing.T) {
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
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	liveCatchupHead130Cfg := quarantineCfg
	liveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	steadyState145Cfg := liveCatchupHead130Cfg
	steadyState145Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=145"
	steadyGeneration2Cfg := steadyState145Cfg
	steadyGeneration2Cfg.PolicyManifestDigest = steadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=2"
	pruneFloor2Cfg := steadyGeneration2Cfg
	pruneFloor2Cfg.PolicyManifestDigest = steadyGeneration2Cfg.PolicyManifestDigest + "|rollback-fence-generation-retention-floor=2"
	floorLift2Cfg := pruneFloor2Cfg
	floorLift2Cfg.PolicyManifestDigest = pruneFloor2Cfg.PolicyManifestDigest + "|rollback-fence-floor-lift-epoch=2"
	settleWindow2Cfg := floorLift2Cfg
	settleWindow2Cfg.PolicyManifestDigest = floorLift2Cfg.PolicyManifestDigest + "|rollback-fence-settle-window-epoch=2"
	spillover2Cfg := settleWindow2Cfg
	spillover2Cfg.PolicyManifestDigest = settleWindow2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-epoch=2"
	rejoin2Cfg := spillover2Cfg
	rejoin2Cfg.PolicyManifestDigest = spillover2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-rejoin-epoch=2"
	steadySeal1Cfg := rejoin2Cfg
	steadySeal1Cfg.PolicyManifestDigest = rejoin2Cfg.PolicyManifestDigest + "|rollback-fence-rejoin-seal-epoch=1"
	steadySeal2Cfg := rejoin2Cfg
	steadySeal2Cfg.PolicyManifestDigest = rejoin2Cfg.PolicyManifestDigest + "|rollback-fence-rejoin-seal-epoch=2"
	ambiguousSteadySealCfg := spillover2Cfg
	ambiguousSteadySealCfg.PolicyManifestDigest = spillover2Cfg.PolicyManifestDigest + "|rollback-fence-rejoin-seal-epoch=3"
	stalePreSteadySealCfg := rejoin2Cfg

	baseSeed := 230
	baseController := newAutoTuneControllerWithSeed(100, rejoin2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, rejoin2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, rejoin2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	steadySeal1Seed := baseController.currentBatch
	steadySeal1Controller := newAutoTuneControllerWithSeed(100, steadySeal1Cfg, &steadySeal1Seed)
	require.NotNil(t, steadySeal1Controller)
	steadySeal1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, steadySeal1Decision := steadySeal1Controller.Resolve(highLag)
	assert.Equal(t, steadySeal1Seed+20, batch)
	assert.Equal(t, "apply_increase", steadySeal1Decision.Decision)
	assert.Equal(t, steadySeal1Cfg.PolicyManifestDigest, steadySeal1Decision.PolicyManifestDigest)
	assert.Equal(t, steadySeal1Cfg.PolicyManifestRefreshEpoch, steadySeal1Decision.PolicyEpoch)
	assert.Equal(t, 0, steadySeal1Decision.PolicyActivationTicks)

	steadySeal2Seed := steadySeal1Controller.currentBatch
	steadySeal2Controller := newAutoTuneControllerWithSeed(100, steadySeal2Cfg, &steadySeal2Seed)
	require.NotNil(t, steadySeal2Controller)
	steadySeal2Controller.reconcilePolicyTransition(steadySeal1Controller.exportPolicyTransition())
	batch, steadySeal2Decision := steadySeal2Controller.Resolve(highLag)
	assert.Equal(t, steadySeal2Seed+20, batch)
	assert.Equal(t, "apply_increase", steadySeal2Decision.Decision)
	assert.Equal(t, steadySeal2Cfg.PolicyManifestDigest, steadySeal2Decision.PolicyManifestDigest)
	assert.Equal(t, steadySeal2Cfg.PolicyManifestRefreshEpoch, steadySeal2Decision.PolicyEpoch)
	assert.Equal(t, 0, steadySeal2Decision.PolicyActivationTicks)

	staleSteadySealSeed := steadySeal2Controller.currentBatch
	staleSteadySealController := newAutoTuneControllerWithSeed(100, steadySeal1Cfg, &staleSteadySealSeed)
	require.NotNil(t, staleSteadySealController)
	staleSteadySealController.reconcilePolicyTransition(steadySeal2Controller.exportPolicyTransition())
	batch, staleSteadySealDecision := staleSteadySealController.Resolve(highLag)
	assert.Equal(t, staleSteadySealSeed+20, batch)
	assert.Equal(t, "apply_increase", staleSteadySealDecision.Decision)
	assert.Equal(
		t,
		steadySeal2Cfg.PolicyManifestDigest,
		staleSteadySealDecision.PolicyManifestDigest,
		"lower steady-seal epochs must remain pinned behind latest steady-seal ownership",
	)
	assert.Equal(t, steadySeal2Cfg.PolicyManifestRefreshEpoch, staleSteadySealDecision.PolicyEpoch)
	assert.Equal(t, 0, staleSteadySealDecision.PolicyActivationTicks)

	ambiguousSteadySealSeed := staleSteadySealController.currentBatch
	ambiguousSteadySealController := newAutoTuneControllerWithSeed(100, ambiguousSteadySealCfg, &ambiguousSteadySealSeed)
	require.NotNil(t, ambiguousSteadySealController)
	ambiguousSteadySealController.reconcilePolicyTransition(staleSteadySealController.exportPolicyTransition())
	batch, ambiguousSteadySealDecision := ambiguousSteadySealController.Resolve(highLag)
	assert.Equal(t, ambiguousSteadySealSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousSteadySealDecision.Decision)
	assert.Equal(
		t,
		steadySeal2Cfg.PolicyManifestDigest,
		ambiguousSteadySealDecision.PolicyManifestDigest,
		"steady-seal markers must remain quarantined until spillover-rejoin ownership is explicit",
	)
	assert.Equal(t, steadySeal2Cfg.PolicyManifestRefreshEpoch, ambiguousSteadySealDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousSteadySealDecision.PolicyActivationTicks)

	stalePreSteadySealSeed := ambiguousSteadySealController.currentBatch
	stalePreSteadySealController := newAutoTuneControllerWithSeed(100, stalePreSteadySealCfg, &stalePreSteadySealSeed)
	require.NotNil(t, stalePreSteadySealController)
	stalePreSteadySealController.reconcilePolicyTransition(ambiguousSteadySealController.exportPolicyTransition())
	batch, stalePreSteadySealDecision := stalePreSteadySealController.Resolve(highLag)
	assert.Equal(t, stalePreSteadySealSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreSteadySealDecision.Decision)
	assert.Equal(
		t,
		steadySeal2Cfg.PolicyManifestDigest,
		stalePreSteadySealDecision.PolicyManifestDigest,
		"post-seal stale pre-seal markers must not reclaim ownership",
	)
	assert.Equal(t, steadySeal2Cfg.PolicyManifestRefreshEpoch, stalePreSteadySealDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreSteadySealDecision.PolicyActivationTicks)

	reForwardSeed := stalePreSteadySealController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreSteadySealController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after steady-seal must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostSteadySealDriftReconciliationRejectsStaleMarkers(t *testing.T) {
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
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	liveCatchupHead130Cfg := quarantineCfg
	liveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	steadyState145Cfg := liveCatchupHead130Cfg
	steadyState145Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=145"
	steadyGeneration2Cfg := steadyState145Cfg
	steadyGeneration2Cfg.PolicyManifestDigest = steadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=2"
	pruneFloor2Cfg := steadyGeneration2Cfg
	pruneFloor2Cfg.PolicyManifestDigest = steadyGeneration2Cfg.PolicyManifestDigest + "|rollback-fence-generation-retention-floor=2"
	floorLift2Cfg := pruneFloor2Cfg
	floorLift2Cfg.PolicyManifestDigest = pruneFloor2Cfg.PolicyManifestDigest + "|rollback-fence-floor-lift-epoch=2"
	settleWindow2Cfg := floorLift2Cfg
	settleWindow2Cfg.PolicyManifestDigest = floorLift2Cfg.PolicyManifestDigest + "|rollback-fence-settle-window-epoch=2"
	spillover2Cfg := settleWindow2Cfg
	spillover2Cfg.PolicyManifestDigest = settleWindow2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-epoch=2"
	rejoin2Cfg := spillover2Cfg
	rejoin2Cfg.PolicyManifestDigest = spillover2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-rejoin-epoch=2"
	steadySeal2Cfg := rejoin2Cfg
	steadySeal2Cfg.PolicyManifestDigest = rejoin2Cfg.PolicyManifestDigest + "|rollback-fence-rejoin-seal-epoch=2"
	drift1Cfg := steadySeal2Cfg
	drift1Cfg.PolicyManifestDigest = steadySeal2Cfg.PolicyManifestDigest + "|rollback-fence-seal-drift-epoch=1"
	drift2Cfg := steadySeal2Cfg
	drift2Cfg.PolicyManifestDigest = steadySeal2Cfg.PolicyManifestDigest + "|rollback-fence-post-steady-seal-drift-epoch=2"
	staleDriftCfg := steadySeal2Cfg
	staleDriftCfg.PolicyManifestDigest = steadySeal2Cfg.PolicyManifestDigest + "|rollback-fence-seal-drift-epoch=1"
	ambiguousDriftCfg := rejoin2Cfg
	ambiguousDriftCfg.PolicyManifestDigest = rejoin2Cfg.PolicyManifestDigest + "|rollback-fence-seal-drift-epoch=3"
	stalePreDriftCfg := steadySeal2Cfg

	baseSeed := 230
	baseController := newAutoTuneControllerWithSeed(100, steadySeal2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, steadySeal2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, steadySeal2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	drift1Seed := baseController.currentBatch
	drift1Controller := newAutoTuneControllerWithSeed(100, drift1Cfg, &drift1Seed)
	require.NotNil(t, drift1Controller)
	drift1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, drift1Decision := drift1Controller.Resolve(highLag)
	assert.Equal(t, drift1Seed+20, batch)
	assert.Equal(t, "apply_increase", drift1Decision.Decision)
	assert.Equal(t, drift1Cfg.PolicyManifestDigest, drift1Decision.PolicyManifestDigest)
	assert.Equal(t, drift1Cfg.PolicyManifestRefreshEpoch, drift1Decision.PolicyEpoch)
	assert.Equal(t, 0, drift1Decision.PolicyActivationTicks)

	drift2Seed := drift1Controller.currentBatch
	drift2Controller := newAutoTuneControllerWithSeed(100, drift2Cfg, &drift2Seed)
	require.NotNil(t, drift2Controller)
	drift2Controller.reconcilePolicyTransition(drift1Controller.exportPolicyTransition())
	batch, drift2Decision := drift2Controller.Resolve(highLag)
	assert.Equal(t, drift2Seed+20, batch)
	assert.Equal(t, "apply_increase", drift2Decision.Decision)
	assert.Equal(t, drift2Cfg.PolicyManifestDigest, drift2Decision.PolicyManifestDigest)
	assert.Equal(t, drift2Cfg.PolicyManifestRefreshEpoch, drift2Decision.PolicyEpoch)
	assert.Equal(t, 0, drift2Decision.PolicyActivationTicks)

	staleDriftSeed := drift2Controller.currentBatch
	staleDriftController := newAutoTuneControllerWithSeed(100, staleDriftCfg, &staleDriftSeed)
	require.NotNil(t, staleDriftController)
	staleDriftController.reconcilePolicyTransition(drift2Controller.exportPolicyTransition())
	batch, staleDriftDecision := staleDriftController.Resolve(highLag)
	assert.Equal(t, staleDriftSeed+20, batch)
	assert.Equal(t, "apply_increase", staleDriftDecision.Decision)
	assert.Equal(
		t,
		drift2Cfg.PolicyManifestDigest,
		staleDriftDecision.PolicyManifestDigest,
		"lower drift epochs must remain pinned behind latest verified drift ownership",
	)
	assert.Equal(t, drift2Cfg.PolicyManifestRefreshEpoch, staleDriftDecision.PolicyEpoch)
	assert.Equal(t, 0, staleDriftDecision.PolicyActivationTicks)

	ambiguousDriftSeed := staleDriftController.currentBatch
	ambiguousDriftController := newAutoTuneControllerWithSeed(100, ambiguousDriftCfg, &ambiguousDriftSeed)
	require.NotNil(t, ambiguousDriftController)
	ambiguousDriftController.reconcilePolicyTransition(staleDriftController.exportPolicyTransition())
	batch, ambiguousDriftDecision := ambiguousDriftController.Resolve(highLag)
	assert.Equal(t, ambiguousDriftSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousDriftDecision.Decision)
	assert.Equal(
		t,
		drift2Cfg.PolicyManifestDigest,
		ambiguousDriftDecision.PolicyManifestDigest,
		"drift markers must remain quarantined until steady-seal ownership is explicit",
	)
	assert.Equal(t, drift2Cfg.PolicyManifestRefreshEpoch, ambiguousDriftDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousDriftDecision.PolicyActivationTicks)

	stalePreDriftSeed := ambiguousDriftController.currentBatch
	stalePreDriftController := newAutoTuneControllerWithSeed(100, stalePreDriftCfg, &stalePreDriftSeed)
	require.NotNil(t, stalePreDriftController)
	stalePreDriftController.reconcilePolicyTransition(ambiguousDriftController.exportPolicyTransition())
	batch, stalePreDriftDecision := stalePreDriftController.Resolve(highLag)
	assert.Equal(t, stalePreDriftSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreDriftDecision.Decision)
	assert.Equal(
		t,
		drift2Cfg.PolicyManifestDigest,
		stalePreDriftDecision.PolicyManifestDigest,
		"post-drift stale pre-drift markers must not reclaim ownership",
	)
	assert.Equal(t, drift2Cfg.PolicyManifestRefreshEpoch, stalePreDriftDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreDriftDecision.PolicyActivationTicks)

	reForwardSeed := stalePreDriftController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreDriftController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after drift reconciliation must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostDriftReanchorRejectsStaleMarkers(t *testing.T) {
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
	expiryCfg := rollbackCfg
	expiryCfg.PolicyManifestDigest = rollbackCfg.PolicyManifestDigest + "|rollback-fence-tombstone-expiry-epoch=4"
	quarantineCfg := expiryCfg
	quarantineCfg.PolicyManifestDigest = expiryCfg.PolicyManifestDigest + "|rollback-fence-late-marker-hold-epoch=5"
	releaseCfg := quarantineCfg
	releaseCfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest + "|rollback-fence-late-marker-release-epoch=6"
	liveCatchupHead130Cfg := quarantineCfg
	liveCatchupHead130Cfg.PolicyManifestDigest = quarantineCfg.PolicyManifestDigest +
		"|rollback-fence-late-marker-release-epoch=8|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130"
	steadyState145Cfg := liveCatchupHead130Cfg
	steadyState145Cfg.PolicyManifestDigest = liveCatchupHead130Cfg.PolicyManifestDigest + "|rollback-fence-steady-state-watermark=145"
	steadyGeneration2Cfg := steadyState145Cfg
	steadyGeneration2Cfg.PolicyManifestDigest = steadyState145Cfg.PolicyManifestDigest + "|rollback-fence-steady-generation=2"
	pruneFloor2Cfg := steadyGeneration2Cfg
	pruneFloor2Cfg.PolicyManifestDigest = steadyGeneration2Cfg.PolicyManifestDigest + "|rollback-fence-generation-retention-floor=2"
	floorLift2Cfg := pruneFloor2Cfg
	floorLift2Cfg.PolicyManifestDigest = pruneFloor2Cfg.PolicyManifestDigest + "|rollback-fence-floor-lift-epoch=2"
	settleWindow2Cfg := floorLift2Cfg
	settleWindow2Cfg.PolicyManifestDigest = floorLift2Cfg.PolicyManifestDigest + "|rollback-fence-settle-window-epoch=2"
	spillover2Cfg := settleWindow2Cfg
	spillover2Cfg.PolicyManifestDigest = settleWindow2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-epoch=2"
	rejoin2Cfg := spillover2Cfg
	rejoin2Cfg.PolicyManifestDigest = spillover2Cfg.PolicyManifestDigest + "|rollback-fence-spillover-rejoin-epoch=2"
	steadySeal2Cfg := rejoin2Cfg
	steadySeal2Cfg.PolicyManifestDigest = rejoin2Cfg.PolicyManifestDigest + "|rollback-fence-rejoin-seal-epoch=2"
	drift2Cfg := steadySeal2Cfg
	drift2Cfg.PolicyManifestDigest = steadySeal2Cfg.PolicyManifestDigest + "|rollback-fence-post-steady-seal-drift-epoch=2"
	reanchor1Cfg := drift2Cfg
	reanchor1Cfg.PolicyManifestDigest = drift2Cfg.PolicyManifestDigest + "|rollback-fence-drift-reanchor-epoch=1"
	reanchor2Cfg := drift2Cfg
	reanchor2Cfg.PolicyManifestDigest = drift2Cfg.PolicyManifestDigest + "|rollback-fence-post-drift-reanchor-epoch=2"
	staleReanchorCfg := drift2Cfg
	staleReanchorCfg.PolicyManifestDigest = drift2Cfg.PolicyManifestDigest + "|rollback-fence-drift-reanchor-epoch=1"
	ambiguousReanchorCfg := steadySeal2Cfg
	ambiguousReanchorCfg.PolicyManifestDigest = steadySeal2Cfg.PolicyManifestDigest + "|rollback-fence-drift-reanchor-epoch=3"
	conflictingReanchorAliasForwardCfg := drift2Cfg
	conflictingReanchorAliasForwardCfg.PolicyManifestDigest = drift2Cfg.PolicyManifestDigest + "|rollback-fence-post-drift-reanchor-epoch=3|rollback-fence-drift-reanchor-epoch=1"
	conflictingReanchorAliasReverseCfg := drift2Cfg
	conflictingReanchorAliasReverseCfg.PolicyManifestDigest = drift2Cfg.PolicyManifestDigest + "|rollback-fence-drift-reanchor-epoch=1|rollback-fence-post-drift-reanchor-epoch=3"
	stalePreReanchorCfg := drift2Cfg
	reanchorCompaction1Cfg := reanchor2Cfg
	reanchorCompaction1Cfg.PolicyManifestDigest = reanchor2Cfg.PolicyManifestDigest + "|rollback-fence-reanchor-compaction-epoch=1"
	reanchorCompaction2Cfg := reanchor2Cfg
	reanchorCompaction2Cfg.PolicyManifestDigest = reanchor2Cfg.PolicyManifestDigest + "|rollback-fence-post-reanchor-compaction-epoch=2"
	staleReanchorCompactionCfg := reanchor2Cfg
	staleReanchorCompactionCfg.PolicyManifestDigest = reanchor2Cfg.PolicyManifestDigest + "|rollback-fence-reanchor-compaction-epoch=1"
	ambiguousReanchorCompactionCfg := drift2Cfg
	ambiguousReanchorCompactionCfg.PolicyManifestDigest = drift2Cfg.PolicyManifestDigest + "|rollback-fence-reanchor-compaction-epoch=3"
	conflictingReanchorCompactionAliasForwardCfg := reanchor2Cfg
	conflictingReanchorCompactionAliasForwardCfg.PolicyManifestDigest = reanchor2Cfg.PolicyManifestDigest + "|rollback-fence-post-reanchor-compaction-epoch=3|rollback-fence-reanchor-compaction-epoch=1"
	conflictingReanchorCompactionAliasReverseCfg := reanchor2Cfg
	conflictingReanchorCompactionAliasReverseCfg.PolicyManifestDigest = reanchor2Cfg.PolicyManifestDigest + "|rollback-fence-reanchor-compaction-epoch=1|rollback-fence-post-reanchor-compaction-epoch=3"
	stalePreReanchorCompactionCfg := reanchor2Cfg
	compactionExpiry1Cfg := reanchorCompaction2Cfg
	compactionExpiry1Cfg.PolicyManifestDigest = reanchorCompaction2Cfg.PolicyManifestDigest + "|rollback-fence-compaction-expiry-epoch=1"
	compactionExpiry2Cfg := reanchorCompaction2Cfg
	compactionExpiry2Cfg.PolicyManifestDigest = reanchorCompaction2Cfg.PolicyManifestDigest + "|rollback-fence-post-lineage-compaction-expiry-epoch=2"
	staleCompactionExpiryCfg := reanchorCompaction2Cfg
	staleCompactionExpiryCfg.PolicyManifestDigest = reanchorCompaction2Cfg.PolicyManifestDigest + "|rollback-fence-compaction-expiry-epoch=1"
	ambiguousCompactionExpiryCfg := reanchor2Cfg
	ambiguousCompactionExpiryCfg.PolicyManifestDigest = reanchor2Cfg.PolicyManifestDigest + "|rollback-fence-compaction-expiry-epoch=3"
	conflictingCompactionExpiryAliasForwardCfg := reanchorCompaction2Cfg
	conflictingCompactionExpiryAliasForwardCfg.PolicyManifestDigest = reanchorCompaction2Cfg.PolicyManifestDigest + "|rollback-fence-post-lineage-compaction-expiry-epoch=3|rollback-fence-compaction-expiry-epoch=1"
	conflictingCompactionExpiryAliasReverseCfg := reanchorCompaction2Cfg
	conflictingCompactionExpiryAliasReverseCfg.PolicyManifestDigest = reanchorCompaction2Cfg.PolicyManifestDigest + "|rollback-fence-compaction-expiry-epoch=1|rollback-fence-post-lineage-compaction-expiry-epoch=3"
	stalePreCompactionExpiryCfg := reanchorCompaction2Cfg
	lateResurrection1Cfg := compactionExpiry2Cfg
	lateResurrection1Cfg.PolicyManifestDigest = compactionExpiry2Cfg.PolicyManifestDigest + "|rollback-fence-resurrection-quarantine-epoch=3"
	lateResurrection2Cfg := compactionExpiry2Cfg
	lateResurrection2Cfg.PolicyManifestDigest = compactionExpiry2Cfg.PolicyManifestDigest + "|rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch=4"
	staleLateResurrectionCfg := compactionExpiry2Cfg
	staleLateResurrectionCfg.PolicyManifestDigest = compactionExpiry2Cfg.PolicyManifestDigest + "|rollback-fence-resurrection-quarantine-epoch=3"
	ambiguousLateResurrectionCfg := reanchorCompaction2Cfg
	ambiguousLateResurrectionCfg.PolicyManifestDigest = reanchorCompaction2Cfg.PolicyManifestDigest + "|rollback-fence-resurrection-quarantine-epoch=5"
	conflictingLateResurrectionAliasForwardCfg := compactionExpiry2Cfg
	conflictingLateResurrectionAliasForwardCfg.PolicyManifestDigest = compactionExpiry2Cfg.PolicyManifestDigest + "|rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch=5|rollback-fence-resurrection-quarantine-epoch=3"
	conflictingLateResurrectionAliasReverseCfg := compactionExpiry2Cfg
	conflictingLateResurrectionAliasReverseCfg.PolicyManifestDigest = compactionExpiry2Cfg.PolicyManifestDigest + "|rollback-fence-resurrection-quarantine-epoch=3|rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch=5"
	stalePreLateResurrectionCfg := compactionExpiry2Cfg
	reintegration1Cfg := lateResurrection2Cfg
	reintegration1Cfg.PolicyManifestDigest = lateResurrection2Cfg.PolicyManifestDigest + "|rollback-fence-resurrection-reintegration-epoch=5"
	reintegration2Cfg := lateResurrection2Cfg
	reintegration2Cfg.PolicyManifestDigest = lateResurrection2Cfg.PolicyManifestDigest + "|rollback-fence-post-late-resurrection-quarantine-reintegration-epoch=6"
	staleReintegrationCfg := lateResurrection2Cfg
	staleReintegrationCfg.PolicyManifestDigest = lateResurrection2Cfg.PolicyManifestDigest + "|rollback-fence-resurrection-reintegration-epoch=5"
	ambiguousReintegrationCfg := compactionExpiry2Cfg
	ambiguousReintegrationCfg.PolicyManifestDigest = compactionExpiry2Cfg.PolicyManifestDigest + "|rollback-fence-resurrection-reintegration-epoch=7"
	conflictingReintegrationAliasForwardCfg := lateResurrection2Cfg
	conflictingReintegrationAliasForwardCfg.PolicyManifestDigest = lateResurrection2Cfg.PolicyManifestDigest + "|rollback-fence-post-late-resurrection-quarantine-reintegration-epoch=7|rollback-fence-resurrection-reintegration-epoch=5"
	conflictingReintegrationAliasReverseCfg := lateResurrection2Cfg
	conflictingReintegrationAliasReverseCfg.PolicyManifestDigest = lateResurrection2Cfg.PolicyManifestDigest + "|rollback-fence-resurrection-reintegration-epoch=5|rollback-fence-post-late-resurrection-quarantine-reintegration-epoch=7"
	stalePreReintegrationCfg := lateResurrection2Cfg

	baseSeed := 230
	baseController := newAutoTuneControllerWithSeed(100, drift2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	baseController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:       true,
		Version:        releaseCfg.PolicyVersion,
		ManifestDigest: releaseCfg.PolicyManifestDigest,
		Epoch:          releaseCfg.PolicyManifestRefreshEpoch,
	})
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, drift2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, drift2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	reanchor1Seed := baseController.currentBatch
	reanchor1Controller := newAutoTuneControllerWithSeed(100, reanchor1Cfg, &reanchor1Seed)
	require.NotNil(t, reanchor1Controller)
	reanchor1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, reanchor1Decision := reanchor1Controller.Resolve(highLag)
	assert.Equal(t, reanchor1Seed+20, batch)
	assert.Equal(t, "apply_increase", reanchor1Decision.Decision)
	assert.Equal(t, reanchor1Cfg.PolicyManifestDigest, reanchor1Decision.PolicyManifestDigest)
	assert.Equal(t, reanchor1Cfg.PolicyManifestRefreshEpoch, reanchor1Decision.PolicyEpoch)
	assert.Equal(t, 0, reanchor1Decision.PolicyActivationTicks)

	reanchor2Seed := reanchor1Controller.currentBatch
	reanchor2Controller := newAutoTuneControllerWithSeed(100, reanchor2Cfg, &reanchor2Seed)
	require.NotNil(t, reanchor2Controller)
	reanchor2Controller.reconcilePolicyTransition(reanchor1Controller.exportPolicyTransition())
	batch, reanchor2Decision := reanchor2Controller.Resolve(highLag)
	assert.Equal(t, reanchor2Seed+20, batch)
	assert.Equal(t, "apply_increase", reanchor2Decision.Decision)
	assert.Equal(t, reanchor2Cfg.PolicyManifestDigest, reanchor2Decision.PolicyManifestDigest)
	assert.Equal(t, reanchor2Cfg.PolicyManifestRefreshEpoch, reanchor2Decision.PolicyEpoch)
	assert.Equal(t, 0, reanchor2Decision.PolicyActivationTicks)

	staleReanchorSeed := reanchor2Controller.currentBatch
	staleReanchorController := newAutoTuneControllerWithSeed(100, staleReanchorCfg, &staleReanchorSeed)
	require.NotNil(t, staleReanchorController)
	staleReanchorController.reconcilePolicyTransition(reanchor2Controller.exportPolicyTransition())
	batch, staleReanchorDecision := staleReanchorController.Resolve(highLag)
	assert.Equal(t, staleReanchorSeed+20, batch)
	assert.Equal(t, "apply_increase", staleReanchorDecision.Decision)
	assert.Equal(
		t,
		reanchor2Cfg.PolicyManifestDigest,
		staleReanchorDecision.PolicyManifestDigest,
		"lower reanchor epochs must remain pinned behind latest verified reanchor ownership",
	)
	assert.Equal(t, reanchor2Cfg.PolicyManifestRefreshEpoch, staleReanchorDecision.PolicyEpoch)
	assert.Equal(t, 0, staleReanchorDecision.PolicyActivationTicks)

	ambiguousReanchorSeed := staleReanchorController.currentBatch
	ambiguousReanchorController := newAutoTuneControllerWithSeed(100, ambiguousReanchorCfg, &ambiguousReanchorSeed)
	require.NotNil(t, ambiguousReanchorController)
	ambiguousReanchorController.reconcilePolicyTransition(staleReanchorController.exportPolicyTransition())
	batch, ambiguousReanchorDecision := ambiguousReanchorController.Resolve(highLag)
	assert.Equal(t, ambiguousReanchorSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousReanchorDecision.Decision)
	assert.Equal(
		t,
		reanchor2Cfg.PolicyManifestDigest,
		ambiguousReanchorDecision.PolicyManifestDigest,
		"reanchor markers must remain quarantined until drift ownership is explicit",
	)
	assert.Equal(t, reanchor2Cfg.PolicyManifestRefreshEpoch, ambiguousReanchorDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousReanchorDecision.PolicyActivationTicks)

	conflictingForwardSeed := ambiguousReanchorController.currentBatch
	conflictingForwardController := newAutoTuneControllerWithSeed(100, conflictingReanchorAliasForwardCfg, &conflictingForwardSeed)
	require.NotNil(t, conflictingForwardController)
	conflictingForwardController.reconcilePolicyTransition(ambiguousReanchorController.exportPolicyTransition())
	batch, conflictingForwardDecision := conflictingForwardController.Resolve(highLag)
	assert.Equal(t, conflictingForwardSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingForwardDecision.Decision)
	assert.Equal(
		t,
		reanchor2Cfg.PolicyManifestDigest,
		conflictingForwardDecision.PolicyManifestDigest,
		"conflicting reanchor alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reanchor2Cfg.PolicyManifestRefreshEpoch, conflictingForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingForwardDecision.PolicyActivationTicks)

	conflictingReverseSeed := conflictingForwardController.currentBatch
	conflictingReverseController := newAutoTuneControllerWithSeed(100, conflictingReanchorAliasReverseCfg, &conflictingReverseSeed)
	require.NotNil(t, conflictingReverseController)
	conflictingReverseController.reconcilePolicyTransition(conflictingForwardController.exportPolicyTransition())
	batch, conflictingReverseDecision := conflictingReverseController.Resolve(highLag)
	assert.Equal(t, 360, batch)
	assert.Equal(t, "apply_increase", conflictingReverseDecision.Decision)
	assert.Equal(
		t,
		reanchor2Cfg.PolicyManifestDigest,
		conflictingReverseDecision.PolicyManifestDigest,
		"conflicting reanchor alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reanchor2Cfg.PolicyManifestRefreshEpoch, conflictingReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReverseDecision.PolicyActivationTicks)

	stalePreReanchorSeed := conflictingReverseController.currentBatch
	stalePreReanchorController := newAutoTuneControllerWithSeed(100, stalePreReanchorCfg, &stalePreReanchorSeed)
	require.NotNil(t, stalePreReanchorController)
	stalePreReanchorController.reconcilePolicyTransition(conflictingReverseController.exportPolicyTransition())
	batch, stalePreReanchorDecision := stalePreReanchorController.Resolve(highLag)
	assert.Equal(t, stalePreReanchorSeed, batch)
	assert.Equal(t, "clamped_increase", stalePreReanchorDecision.Decision)
	assert.Equal(
		t,
		reanchor2Cfg.PolicyManifestDigest,
		stalePreReanchorDecision.PolicyManifestDigest,
		"post-reanchor stale pre-reanchor markers must not reclaim ownership",
	)
	assert.Equal(t, reanchor2Cfg.PolicyManifestRefreshEpoch, stalePreReanchorDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreReanchorDecision.PolicyActivationTicks)

	reanchorCompaction1Seed := stalePreReanchorController.currentBatch
	reanchorCompaction1Controller := newAutoTuneControllerWithSeed(100, reanchorCompaction1Cfg, &reanchorCompaction1Seed)
	require.NotNil(t, reanchorCompaction1Controller)
	reanchorCompaction1Controller.reconcilePolicyTransition(stalePreReanchorController.exportPolicyTransition())
	batch, reanchorCompaction1Decision := reanchorCompaction1Controller.Resolve(highLag)
	assert.Equal(t, reanchorCompaction1Seed, batch)
	assert.Equal(t, "clamped_increase", reanchorCompaction1Decision.Decision)
	assert.Equal(t, reanchorCompaction1Cfg.PolicyManifestDigest, reanchorCompaction1Decision.PolicyManifestDigest)
	assert.Equal(t, reanchorCompaction1Cfg.PolicyManifestRefreshEpoch, reanchorCompaction1Decision.PolicyEpoch)
	assert.Equal(t, 0, reanchorCompaction1Decision.PolicyActivationTicks)

	reanchorCompaction2Seed := reanchorCompaction1Controller.currentBatch
	reanchorCompaction2Controller := newAutoTuneControllerWithSeed(100, reanchorCompaction2Cfg, &reanchorCompaction2Seed)
	require.NotNil(t, reanchorCompaction2Controller)
	reanchorCompaction2Controller.reconcilePolicyTransition(reanchorCompaction1Controller.exportPolicyTransition())
	batch, reanchorCompaction2Decision := reanchorCompaction2Controller.Resolve(highLag)
	assert.Equal(t, reanchorCompaction2Seed, batch)
	assert.Equal(t, "clamped_increase", reanchorCompaction2Decision.Decision)
	assert.Equal(t, reanchorCompaction2Cfg.PolicyManifestDigest, reanchorCompaction2Decision.PolicyManifestDigest)
	assert.Equal(t, reanchorCompaction2Cfg.PolicyManifestRefreshEpoch, reanchorCompaction2Decision.PolicyEpoch)
	assert.Equal(t, 0, reanchorCompaction2Decision.PolicyActivationTicks)

	staleReanchorCompactionSeed := reanchorCompaction2Controller.currentBatch
	staleReanchorCompactionController := newAutoTuneControllerWithSeed(100, staleReanchorCompactionCfg, &staleReanchorCompactionSeed)
	require.NotNil(t, staleReanchorCompactionController)
	staleReanchorCompactionController.reconcilePolicyTransition(reanchorCompaction2Controller.exportPolicyTransition())
	batch, staleReanchorCompactionDecision := staleReanchorCompactionController.Resolve(highLag)
	assert.Equal(t, staleReanchorCompactionSeed, batch)
	assert.Equal(t, "clamped_increase", staleReanchorCompactionDecision.Decision)
	assert.Equal(
		t,
		reanchorCompaction2Cfg.PolicyManifestDigest,
		staleReanchorCompactionDecision.PolicyManifestDigest,
		"lower lineage-compaction epochs must remain pinned behind latest verified compaction ownership",
	)
	assert.Equal(t, reanchorCompaction2Cfg.PolicyManifestRefreshEpoch, staleReanchorCompactionDecision.PolicyEpoch)
	assert.Equal(t, 0, staleReanchorCompactionDecision.PolicyActivationTicks)

	ambiguousReanchorCompactionSeed := staleReanchorCompactionController.currentBatch
	ambiguousReanchorCompactionController := newAutoTuneControllerWithSeed(100, ambiguousReanchorCompactionCfg, &ambiguousReanchorCompactionSeed)
	require.NotNil(t, ambiguousReanchorCompactionController)
	ambiguousReanchorCompactionController.reconcilePolicyTransition(staleReanchorCompactionController.exportPolicyTransition())
	batch, ambiguousReanchorCompactionDecision := ambiguousReanchorCompactionController.Resolve(highLag)
	assert.Equal(t, ambiguousReanchorCompactionSeed, batch)
	assert.Equal(t, "clamped_increase", ambiguousReanchorCompactionDecision.Decision)
	assert.Equal(
		t,
		reanchorCompaction2Cfg.PolicyManifestDigest,
		ambiguousReanchorCompactionDecision.PolicyManifestDigest,
		"lineage-compaction markers must remain quarantined until reanchor ownership is explicit",
	)
	assert.Equal(t, reanchorCompaction2Cfg.PolicyManifestRefreshEpoch, ambiguousReanchorCompactionDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousReanchorCompactionDecision.PolicyActivationTicks)

	conflictingReanchorCompactionForwardSeed := ambiguousReanchorCompactionController.currentBatch
	conflictingReanchorCompactionForwardController := newAutoTuneControllerWithSeed(100, conflictingReanchorCompactionAliasForwardCfg, &conflictingReanchorCompactionForwardSeed)
	require.NotNil(t, conflictingReanchorCompactionForwardController)
	conflictingReanchorCompactionForwardController.reconcilePolicyTransition(ambiguousReanchorCompactionController.exportPolicyTransition())
	batch, conflictingReanchorCompactionForwardDecision := conflictingReanchorCompactionForwardController.Resolve(highLag)
	assert.Equal(t, conflictingReanchorCompactionForwardSeed, batch)
	assert.Equal(t, "clamped_increase", conflictingReanchorCompactionForwardDecision.Decision)
	assert.Equal(
		t,
		reanchorCompaction2Cfg.PolicyManifestDigest,
		conflictingReanchorCompactionForwardDecision.PolicyManifestDigest,
		"conflicting lineage-compaction alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reanchorCompaction2Cfg.PolicyManifestRefreshEpoch, conflictingReanchorCompactionForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReanchorCompactionForwardDecision.PolicyActivationTicks)

	conflictingReanchorCompactionReverseSeed := conflictingReanchorCompactionForwardController.currentBatch
	conflictingReanchorCompactionReverseController := newAutoTuneControllerWithSeed(100, conflictingReanchorCompactionAliasReverseCfg, &conflictingReanchorCompactionReverseSeed)
	require.NotNil(t, conflictingReanchorCompactionReverseController)
	conflictingReanchorCompactionReverseController.reconcilePolicyTransition(conflictingReanchorCompactionForwardController.exportPolicyTransition())
	batch, conflictingReanchorCompactionReverseDecision := conflictingReanchorCompactionReverseController.Resolve(highLag)
	assert.Equal(t, conflictingReanchorCompactionReverseSeed, batch)
	assert.Equal(t, "clamped_increase", conflictingReanchorCompactionReverseDecision.Decision)
	assert.Equal(
		t,
		reanchorCompaction2Cfg.PolicyManifestDigest,
		conflictingReanchorCompactionReverseDecision.PolicyManifestDigest,
		"conflicting lineage-compaction alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reanchorCompaction2Cfg.PolicyManifestRefreshEpoch, conflictingReanchorCompactionReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReanchorCompactionReverseDecision.PolicyActivationTicks)

	stalePreReanchorCompactionSeed := conflictingReanchorCompactionReverseController.currentBatch
	stalePreReanchorCompactionController := newAutoTuneControllerWithSeed(100, stalePreReanchorCompactionCfg, &stalePreReanchorCompactionSeed)
	require.NotNil(t, stalePreReanchorCompactionController)
	stalePreReanchorCompactionController.reconcilePolicyTransition(conflictingReanchorCompactionReverseController.exportPolicyTransition())
	batch, stalePreReanchorCompactionDecision := stalePreReanchorCompactionController.Resolve(highLag)
	assert.Equal(t, stalePreReanchorCompactionSeed, batch)
	assert.Equal(t, "clamped_increase", stalePreReanchorCompactionDecision.Decision)
	assert.Equal(
		t,
		reanchorCompaction2Cfg.PolicyManifestDigest,
		stalePreReanchorCompactionDecision.PolicyManifestDigest,
		"post-reanchor compaction stale pre-compaction markers must not reclaim ownership",
	)
	assert.Equal(t, reanchorCompaction2Cfg.PolicyManifestRefreshEpoch, stalePreReanchorCompactionDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreReanchorCompactionDecision.PolicyActivationTicks)

	compactionExpiry1Seed := stalePreReanchorCompactionController.currentBatch
	compactionExpiry1Controller := newAutoTuneControllerWithSeed(100, compactionExpiry1Cfg, &compactionExpiry1Seed)
	require.NotNil(t, compactionExpiry1Controller)
	compactionExpiry1Controller.reconcilePolicyTransition(stalePreReanchorCompactionController.exportPolicyTransition())
	batch, compactionExpiry1Decision := compactionExpiry1Controller.Resolve(highLag)
	assert.Equal(t, compactionExpiry1Seed, batch)
	assert.Equal(t, "clamped_increase", compactionExpiry1Decision.Decision)
	assert.Equal(t, compactionExpiry1Cfg.PolicyManifestDigest, compactionExpiry1Decision.PolicyManifestDigest)
	assert.Equal(t, compactionExpiry1Cfg.PolicyManifestRefreshEpoch, compactionExpiry1Decision.PolicyEpoch)
	assert.Equal(t, 0, compactionExpiry1Decision.PolicyActivationTicks)

	compactionExpiry2Seed := compactionExpiry1Controller.currentBatch
	compactionExpiry2Controller := newAutoTuneControllerWithSeed(100, compactionExpiry2Cfg, &compactionExpiry2Seed)
	require.NotNil(t, compactionExpiry2Controller)
	compactionExpiry2Controller.reconcilePolicyTransition(compactionExpiry1Controller.exportPolicyTransition())
	batch, compactionExpiry2Decision := compactionExpiry2Controller.Resolve(highLag)
	assert.Equal(t, compactionExpiry2Seed, batch)
	assert.Equal(t, "clamped_increase", compactionExpiry2Decision.Decision)
	assert.Equal(t, compactionExpiry2Cfg.PolicyManifestDigest, compactionExpiry2Decision.PolicyManifestDigest)
	assert.Equal(t, compactionExpiry2Cfg.PolicyManifestRefreshEpoch, compactionExpiry2Decision.PolicyEpoch)
	assert.Equal(t, 0, compactionExpiry2Decision.PolicyActivationTicks)

	staleCompactionExpirySeed := compactionExpiry2Controller.currentBatch
	staleCompactionExpiryController := newAutoTuneControllerWithSeed(100, staleCompactionExpiryCfg, &staleCompactionExpirySeed)
	require.NotNil(t, staleCompactionExpiryController)
	staleCompactionExpiryController.reconcilePolicyTransition(compactionExpiry2Controller.exportPolicyTransition())
	batch, staleCompactionExpiryDecision := staleCompactionExpiryController.Resolve(highLag)
	assert.Equal(t, staleCompactionExpirySeed, batch)
	assert.Equal(t, "clamped_increase", staleCompactionExpiryDecision.Decision)
	assert.Equal(
		t,
		compactionExpiry2Cfg.PolicyManifestDigest,
		staleCompactionExpiryDecision.PolicyManifestDigest,
		"lower marker-expiry epochs must remain pinned behind latest verified marker-expiry ownership",
	)
	assert.Equal(t, compactionExpiry2Cfg.PolicyManifestRefreshEpoch, staleCompactionExpiryDecision.PolicyEpoch)
	assert.Equal(t, 0, staleCompactionExpiryDecision.PolicyActivationTicks)

	ambiguousCompactionExpirySeed := staleCompactionExpiryController.currentBatch
	ambiguousCompactionExpiryController := newAutoTuneControllerWithSeed(100, ambiguousCompactionExpiryCfg, &ambiguousCompactionExpirySeed)
	require.NotNil(t, ambiguousCompactionExpiryController)
	ambiguousCompactionExpiryController.reconcilePolicyTransition(staleCompactionExpiryController.exportPolicyTransition())
	batch, ambiguousCompactionExpiryDecision := ambiguousCompactionExpiryController.Resolve(highLag)
	assert.Equal(t, ambiguousCompactionExpirySeed, batch)
	assert.Equal(t, "clamped_increase", ambiguousCompactionExpiryDecision.Decision)
	assert.Equal(
		t,
		compactionExpiry2Cfg.PolicyManifestDigest,
		ambiguousCompactionExpiryDecision.PolicyManifestDigest,
		"marker-expiry markers must remain quarantined until post-reanchor compaction ownership is explicit",
	)
	assert.Equal(t, compactionExpiry2Cfg.PolicyManifestRefreshEpoch, ambiguousCompactionExpiryDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousCompactionExpiryDecision.PolicyActivationTicks)

	conflictingCompactionExpiryForwardSeed := ambiguousCompactionExpiryController.currentBatch
	conflictingCompactionExpiryForwardController := newAutoTuneControllerWithSeed(100, conflictingCompactionExpiryAliasForwardCfg, &conflictingCompactionExpiryForwardSeed)
	require.NotNil(t, conflictingCompactionExpiryForwardController)
	conflictingCompactionExpiryForwardController.reconcilePolicyTransition(ambiguousCompactionExpiryController.exportPolicyTransition())
	batch, conflictingCompactionExpiryForwardDecision := conflictingCompactionExpiryForwardController.Resolve(highLag)
	assert.Equal(t, conflictingCompactionExpiryForwardSeed, batch)
	assert.Equal(t, "clamped_increase", conflictingCompactionExpiryForwardDecision.Decision)
	assert.Equal(
		t,
		compactionExpiry2Cfg.PolicyManifestDigest,
		conflictingCompactionExpiryForwardDecision.PolicyManifestDigest,
		"conflicting marker-expiry alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, compactionExpiry2Cfg.PolicyManifestRefreshEpoch, conflictingCompactionExpiryForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingCompactionExpiryForwardDecision.PolicyActivationTicks)

	conflictingCompactionExpiryReverseSeed := conflictingCompactionExpiryForwardController.currentBatch
	conflictingCompactionExpiryReverseController := newAutoTuneControllerWithSeed(100, conflictingCompactionExpiryAliasReverseCfg, &conflictingCompactionExpiryReverseSeed)
	require.NotNil(t, conflictingCompactionExpiryReverseController)
	conflictingCompactionExpiryReverseController.reconcilePolicyTransition(conflictingCompactionExpiryForwardController.exportPolicyTransition())
	batch, conflictingCompactionExpiryReverseDecision := conflictingCompactionExpiryReverseController.Resolve(highLag)
	assert.Equal(t, conflictingCompactionExpiryReverseSeed, batch)
	assert.Equal(t, "clamped_increase", conflictingCompactionExpiryReverseDecision.Decision)
	assert.Equal(
		t,
		compactionExpiry2Cfg.PolicyManifestDigest,
		conflictingCompactionExpiryReverseDecision.PolicyManifestDigest,
		"conflicting marker-expiry alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, compactionExpiry2Cfg.PolicyManifestRefreshEpoch, conflictingCompactionExpiryReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingCompactionExpiryReverseDecision.PolicyActivationTicks)

	stalePreCompactionExpirySeed := conflictingCompactionExpiryReverseController.currentBatch
	stalePreCompactionExpiryController := newAutoTuneControllerWithSeed(100, stalePreCompactionExpiryCfg, &stalePreCompactionExpirySeed)
	require.NotNil(t, stalePreCompactionExpiryController)
	stalePreCompactionExpiryController.reconcilePolicyTransition(conflictingCompactionExpiryReverseController.exportPolicyTransition())
	batch, stalePreCompactionExpiryDecision := stalePreCompactionExpiryController.Resolve(highLag)
	assert.Equal(t, stalePreCompactionExpirySeed, batch)
	assert.Equal(t, "clamped_increase", stalePreCompactionExpiryDecision.Decision)
	assert.Equal(
		t,
		compactionExpiry2Cfg.PolicyManifestDigest,
		stalePreCompactionExpiryDecision.PolicyManifestDigest,
		"post-lineage-compaction marker-expiry stale pre-expiry markers must not reclaim ownership",
	)
	assert.Equal(t, compactionExpiry2Cfg.PolicyManifestRefreshEpoch, stalePreCompactionExpiryDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreCompactionExpiryDecision.PolicyActivationTicks)

	lateResurrection1Seed := stalePreCompactionExpiryController.currentBatch
	lateResurrection1Controller := newAutoTuneControllerWithSeed(100, lateResurrection1Cfg, &lateResurrection1Seed)
	require.NotNil(t, lateResurrection1Controller)
	lateResurrection1Controller.reconcilePolicyTransition(stalePreCompactionExpiryController.exportPolicyTransition())
	batch, lateResurrection1Decision := lateResurrection1Controller.Resolve(highLag)
	assert.Equal(t, lateResurrection1Seed, batch)
	assert.Equal(t, "clamped_increase", lateResurrection1Decision.Decision)
	assert.Equal(t, lateResurrection1Cfg.PolicyManifestDigest, lateResurrection1Decision.PolicyManifestDigest)
	assert.Equal(t, lateResurrection1Cfg.PolicyManifestRefreshEpoch, lateResurrection1Decision.PolicyEpoch)
	assert.Equal(t, 0, lateResurrection1Decision.PolicyActivationTicks)

	lateResurrection2Seed := lateResurrection1Controller.currentBatch
	lateResurrection2Controller := newAutoTuneControllerWithSeed(100, lateResurrection2Cfg, &lateResurrection2Seed)
	require.NotNil(t, lateResurrection2Controller)
	lateResurrection2Controller.reconcilePolicyTransition(lateResurrection1Controller.exportPolicyTransition())
	batch, lateResurrection2Decision := lateResurrection2Controller.Resolve(highLag)
	assert.Equal(t, lateResurrection2Seed, batch)
	assert.Equal(t, "clamped_increase", lateResurrection2Decision.Decision)
	assert.Equal(t, lateResurrection2Cfg.PolicyManifestDigest, lateResurrection2Decision.PolicyManifestDigest)
	assert.Equal(t, lateResurrection2Cfg.PolicyManifestRefreshEpoch, lateResurrection2Decision.PolicyEpoch)
	assert.Equal(t, 0, lateResurrection2Decision.PolicyActivationTicks)

	staleLateResurrectionSeed := lateResurrection2Controller.currentBatch
	staleLateResurrectionController := newAutoTuneControllerWithSeed(100, staleLateResurrectionCfg, &staleLateResurrectionSeed)
	require.NotNil(t, staleLateResurrectionController)
	staleLateResurrectionController.reconcilePolicyTransition(lateResurrection2Controller.exportPolicyTransition())
	batch, staleLateResurrectionDecision := staleLateResurrectionController.Resolve(highLag)
	assert.Equal(t, staleLateResurrectionSeed, batch)
	assert.Equal(t, "clamped_increase", staleLateResurrectionDecision.Decision)
	assert.Equal(
		t,
		lateResurrection2Cfg.PolicyManifestDigest,
		staleLateResurrectionDecision.PolicyManifestDigest,
		"lower late-resurrection epochs must remain pinned behind latest verified late-resurrection ownership",
	)
	assert.Equal(t, lateResurrection2Cfg.PolicyManifestRefreshEpoch, staleLateResurrectionDecision.PolicyEpoch)
	assert.Equal(t, 0, staleLateResurrectionDecision.PolicyActivationTicks)

	ambiguousLateResurrectionSeed := staleLateResurrectionController.currentBatch
	ambiguousLateResurrectionController := newAutoTuneControllerWithSeed(100, ambiguousLateResurrectionCfg, &ambiguousLateResurrectionSeed)
	require.NotNil(t, ambiguousLateResurrectionController)
	ambiguousLateResurrectionController.reconcilePolicyTransition(staleLateResurrectionController.exportPolicyTransition())
	batch, ambiguousLateResurrectionDecision := ambiguousLateResurrectionController.Resolve(highLag)
	assert.Equal(t, ambiguousLateResurrectionSeed, batch)
	assert.Equal(t, "clamped_increase", ambiguousLateResurrectionDecision.Decision)
	assert.Equal(
		t,
		lateResurrection2Cfg.PolicyManifestDigest,
		ambiguousLateResurrectionDecision.PolicyManifestDigest,
		"late-resurrection markers must remain quarantined until post-marker-expiry ownership is explicit",
	)
	assert.Equal(t, lateResurrection2Cfg.PolicyManifestRefreshEpoch, ambiguousLateResurrectionDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousLateResurrectionDecision.PolicyActivationTicks)

	conflictingLateResurrectionForwardSeed := ambiguousLateResurrectionController.currentBatch
	conflictingLateResurrectionForwardController := newAutoTuneControllerWithSeed(100, conflictingLateResurrectionAliasForwardCfg, &conflictingLateResurrectionForwardSeed)
	require.NotNil(t, conflictingLateResurrectionForwardController)
	conflictingLateResurrectionForwardController.reconcilePolicyTransition(ambiguousLateResurrectionController.exportPolicyTransition())
	batch, conflictingLateResurrectionForwardDecision := conflictingLateResurrectionForwardController.Resolve(highLag)
	assert.Equal(t, conflictingLateResurrectionForwardSeed, batch)
	assert.Equal(t, "clamped_increase", conflictingLateResurrectionForwardDecision.Decision)
	assert.Equal(
		t,
		lateResurrection2Cfg.PolicyManifestDigest,
		conflictingLateResurrectionForwardDecision.PolicyManifestDigest,
		"conflicting late-resurrection alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, lateResurrection2Cfg.PolicyManifestRefreshEpoch, conflictingLateResurrectionForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingLateResurrectionForwardDecision.PolicyActivationTicks)

	conflictingLateResurrectionReverseSeed := conflictingLateResurrectionForwardController.currentBatch
	conflictingLateResurrectionReverseController := newAutoTuneControllerWithSeed(100, conflictingLateResurrectionAliasReverseCfg, &conflictingLateResurrectionReverseSeed)
	require.NotNil(t, conflictingLateResurrectionReverseController)
	conflictingLateResurrectionReverseController.reconcilePolicyTransition(conflictingLateResurrectionForwardController.exportPolicyTransition())
	batch, conflictingLateResurrectionReverseDecision := conflictingLateResurrectionReverseController.Resolve(highLag)
	assert.Equal(t, conflictingLateResurrectionReverseSeed, batch)
	assert.Equal(t, "clamped_increase", conflictingLateResurrectionReverseDecision.Decision)
	assert.Equal(
		t,
		lateResurrection2Cfg.PolicyManifestDigest,
		conflictingLateResurrectionReverseDecision.PolicyManifestDigest,
		"conflicting late-resurrection alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, lateResurrection2Cfg.PolicyManifestRefreshEpoch, conflictingLateResurrectionReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingLateResurrectionReverseDecision.PolicyActivationTicks)

	stalePreLateResurrectionSeed := conflictingLateResurrectionReverseController.currentBatch
	stalePreLateResurrectionController := newAutoTuneControllerWithSeed(100, stalePreLateResurrectionCfg, &stalePreLateResurrectionSeed)
	require.NotNil(t, stalePreLateResurrectionController)
	stalePreLateResurrectionController.reconcilePolicyTransition(conflictingLateResurrectionReverseController.exportPolicyTransition())
	batch, stalePreLateResurrectionDecision := stalePreLateResurrectionController.Resolve(highLag)
	assert.Equal(t, stalePreLateResurrectionSeed, batch)
	assert.Equal(t, "clamped_increase", stalePreLateResurrectionDecision.Decision)
	assert.Equal(
		t,
		lateResurrection2Cfg.PolicyManifestDigest,
		stalePreLateResurrectionDecision.PolicyManifestDigest,
		"post-marker-expiry late-resurrection stale pre-resurrection markers must not reclaim ownership",
	)
	assert.Equal(t, lateResurrection2Cfg.PolicyManifestRefreshEpoch, stalePreLateResurrectionDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreLateResurrectionDecision.PolicyActivationTicks)

	reintegration1Seed := stalePreLateResurrectionController.currentBatch
	reintegration1Controller := newAutoTuneControllerWithSeed(100, reintegration1Cfg, &reintegration1Seed)
	require.NotNil(t, reintegration1Controller)
	reintegration1Controller.reconcilePolicyTransition(stalePreLateResurrectionController.exportPolicyTransition())
	batch, reintegration1Decision := reintegration1Controller.Resolve(highLag)
	assert.Equal(t, reintegration1Seed, batch)
	assert.Equal(t, "clamped_increase", reintegration1Decision.Decision)
	assert.Equal(t, reintegration1Cfg.PolicyManifestDigest, reintegration1Decision.PolicyManifestDigest)
	assert.Equal(t, reintegration1Cfg.PolicyManifestRefreshEpoch, reintegration1Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegration1Decision.PolicyActivationTicks)

	reintegration2Seed := reintegration1Controller.currentBatch
	reintegration2Controller := newAutoTuneControllerWithSeed(100, reintegration2Cfg, &reintegration2Seed)
	require.NotNil(t, reintegration2Controller)
	reintegration2Controller.reconcilePolicyTransition(reintegration1Controller.exportPolicyTransition())
	batch, reintegration2Decision := reintegration2Controller.Resolve(highLag)
	assert.Equal(t, reintegration2Seed, batch)
	assert.Equal(t, "clamped_increase", reintegration2Decision.Decision)
	assert.Equal(t, reintegration2Cfg.PolicyManifestDigest, reintegration2Decision.PolicyManifestDigest)
	assert.Equal(t, reintegration2Cfg.PolicyManifestRefreshEpoch, reintegration2Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegration2Decision.PolicyActivationTicks)

	staleReintegrationSeed := reintegration2Controller.currentBatch
	staleReintegrationController := newAutoTuneControllerWithSeed(100, staleReintegrationCfg, &staleReintegrationSeed)
	require.NotNil(t, staleReintegrationController)
	staleReintegrationController.reconcilePolicyTransition(reintegration2Controller.exportPolicyTransition())
	batch, staleReintegrationDecision := staleReintegrationController.Resolve(highLag)
	assert.Equal(t, staleReintegrationSeed, batch)
	assert.Equal(t, "clamped_increase", staleReintegrationDecision.Decision)
	assert.Equal(
		t,
		reintegration2Cfg.PolicyManifestDigest,
		staleReintegrationDecision.PolicyManifestDigest,
		"lower reintegration epochs must remain pinned behind latest verified reintegration ownership",
	)
	assert.Equal(t, reintegration2Cfg.PolicyManifestRefreshEpoch, staleReintegrationDecision.PolicyEpoch)
	assert.Equal(t, 0, staleReintegrationDecision.PolicyActivationTicks)

	ambiguousReintegrationSeed := staleReintegrationController.currentBatch
	ambiguousReintegrationController := newAutoTuneControllerWithSeed(100, ambiguousReintegrationCfg, &ambiguousReintegrationSeed)
	require.NotNil(t, ambiguousReintegrationController)
	ambiguousReintegrationController.reconcilePolicyTransition(staleReintegrationController.exportPolicyTransition())
	batch, ambiguousReintegrationDecision := ambiguousReintegrationController.Resolve(highLag)
	assert.Equal(t, ambiguousReintegrationSeed, batch)
	assert.Equal(t, "clamped_increase", ambiguousReintegrationDecision.Decision)
	assert.Equal(
		t,
		reintegration2Cfg.PolicyManifestDigest,
		ambiguousReintegrationDecision.PolicyManifestDigest,
		"reintegration markers must remain quarantined until post-late-resurrection ownership is explicit",
	)
	assert.Equal(t, reintegration2Cfg.PolicyManifestRefreshEpoch, ambiguousReintegrationDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousReintegrationDecision.PolicyActivationTicks)

	conflictingReintegrationForwardSeed := ambiguousReintegrationController.currentBatch
	conflictingReintegrationForwardController := newAutoTuneControllerWithSeed(100, conflictingReintegrationAliasForwardCfg, &conflictingReintegrationForwardSeed)
	require.NotNil(t, conflictingReintegrationForwardController)
	conflictingReintegrationForwardController.reconcilePolicyTransition(ambiguousReintegrationController.exportPolicyTransition())
	batch, conflictingReintegrationForwardDecision := conflictingReintegrationForwardController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationForwardSeed, batch)
	assert.Equal(t, "clamped_increase", conflictingReintegrationForwardDecision.Decision)
	assert.Equal(
		t,
		reintegration2Cfg.PolicyManifestDigest,
		conflictingReintegrationForwardDecision.PolicyManifestDigest,
		"conflicting reintegration alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegration2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationForwardDecision.PolicyActivationTicks)

	conflictingReintegrationReverseSeed := conflictingReintegrationForwardController.currentBatch
	conflictingReintegrationReverseController := newAutoTuneControllerWithSeed(100, conflictingReintegrationAliasReverseCfg, &conflictingReintegrationReverseSeed)
	require.NotNil(t, conflictingReintegrationReverseController)
	conflictingReintegrationReverseController.reconcilePolicyTransition(conflictingReintegrationForwardController.exportPolicyTransition())
	batch, conflictingReintegrationReverseDecision := conflictingReintegrationReverseController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationReverseSeed, batch)
	assert.Equal(t, "clamped_increase", conflictingReintegrationReverseDecision.Decision)
	assert.Equal(
		t,
		reintegration2Cfg.PolicyManifestDigest,
		conflictingReintegrationReverseDecision.PolicyManifestDigest,
		"conflicting reintegration alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegration2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationReverseDecision.PolicyActivationTicks)

	stalePreReintegrationSeed := conflictingReintegrationReverseController.currentBatch
	stalePreReintegrationController := newAutoTuneControllerWithSeed(100, stalePreReintegrationCfg, &stalePreReintegrationSeed)
	require.NotNil(t, stalePreReintegrationController)
	stalePreReintegrationController.reconcilePolicyTransition(conflictingReintegrationReverseController.exportPolicyTransition())
	batch, stalePreReintegrationDecision := stalePreReintegrationController.Resolve(highLag)
	assert.Equal(t, stalePreReintegrationSeed, batch)
	assert.Equal(t, "clamped_increase", stalePreReintegrationDecision.Decision)
	assert.Equal(
		t,
		reintegration2Cfg.PolicyManifestDigest,
		stalePreReintegrationDecision.PolicyManifestDigest,
		"post-late-resurrection reintegration stale pre-reintegration markers must not reclaim ownership",
	)
	assert.Equal(t, reintegration2Cfg.PolicyManifestRefreshEpoch, stalePreReintegrationDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreReintegrationDecision.PolicyActivationTicks)

	reForwardSeed := stalePreReintegrationController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreReintegrationController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after post-late-resurrection reintegration must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostReintegrationSealRejectsStaleMarkers(t *testing.T) {
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

	lateResurrection2Digest := "manifest-tail-v2b" +
		"|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3" +
		"|rollback-fence-tombstone-expiry-epoch=4" +
		"|rollback-fence-late-marker-hold-epoch=5" +
		"|rollback-fence-late-marker-release-epoch=8" +
		"|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90" +
		"|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130" +
		"|rollback-fence-steady-state-watermark=145|rollback-fence-steady-generation=2" +
		"|rollback-fence-generation-retention-floor=2|rollback-fence-floor-lift-epoch=2" +
		"|rollback-fence-settle-window-epoch=2|rollback-fence-spillover-epoch=2" +
		"|rollback-fence-spillover-rejoin-epoch=2|rollback-fence-rejoin-seal-epoch=2" +
		"|rollback-fence-post-steady-seal-drift-epoch=2|rollback-fence-post-drift-reanchor-epoch=2" +
		"|rollback-fence-post-reanchor-compaction-epoch=2" +
		"|rollback-fence-post-lineage-compaction-expiry-epoch=2" +
		"|rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch=4"
	reintegration2Digest := lateResurrection2Digest + "|rollback-fence-post-late-resurrection-quarantine-reintegration-epoch=6"

	reintegration2Cfg := segment2Cfg
	reintegration2Cfg.PolicyManifestDigest = reintegration2Digest
	reintegrationSeal1Cfg := segment2Cfg
	reintegrationSeal1Cfg.PolicyManifestDigest = reintegration2Digest + "|rollback-fence-resurrection-reintegration-seal-epoch=7"
	reintegrationSeal2Cfg := segment2Cfg
	reintegrationSeal2Cfg.PolicyManifestDigest = reintegration2Digest + "|rollback-fence-post-late-resurrection-reintegration-seal-epoch=8"
	staleReintegrationSealCfg := segment2Cfg
	staleReintegrationSealCfg.PolicyManifestDigest = reintegration2Digest + "|rollback-fence-resurrection-reintegration-seal-epoch=7"
	ambiguousReintegrationSealCfg := segment2Cfg
	ambiguousReintegrationSealCfg.PolicyManifestDigest = lateResurrection2Digest + "|rollback-fence-resurrection-reintegration-seal-epoch=9"
	conflictingReintegrationSealAliasForwardCfg := segment2Cfg
	conflictingReintegrationSealAliasForwardCfg.PolicyManifestDigest = reintegration2Digest +
		"|rollback-fence-post-late-resurrection-reintegration-seal-epoch=9|rollback-fence-resurrection-reintegration-seal-epoch=7"
	conflictingReintegrationSealAliasReverseCfg := segment2Cfg
	conflictingReintegrationSealAliasReverseCfg.PolicyManifestDigest = reintegration2Digest +
		"|rollback-fence-resurrection-reintegration-seal-epoch=7|rollback-fence-post-late-resurrection-reintegration-seal-epoch=9"
	stalePreReintegrationSealCfg := segment2Cfg
	stalePreReintegrationSealCfg.PolicyManifestDigest = reintegration2Digest

	baseSeed := 140
	baseController := newAutoTuneControllerWithSeed(100, reintegration2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, reintegration2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, reintegration2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	reintegrationSeal1Seed := baseController.currentBatch
	reintegrationSeal1Controller := newAutoTuneControllerWithSeed(100, reintegrationSeal1Cfg, &reintegrationSeal1Seed)
	require.NotNil(t, reintegrationSeal1Controller)
	reintegrationSeal1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, reintegrationSeal1Decision := reintegrationSeal1Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSeal1Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSeal1Decision.Decision)
	assert.Equal(t, reintegrationSeal1Cfg.PolicyManifestDigest, reintegrationSeal1Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSeal1Cfg.PolicyManifestRefreshEpoch, reintegrationSeal1Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSeal1Decision.PolicyActivationTicks)

	reintegrationSeal2Seed := reintegrationSeal1Controller.currentBatch
	reintegrationSeal2Controller := newAutoTuneControllerWithSeed(100, reintegrationSeal2Cfg, &reintegrationSeal2Seed)
	require.NotNil(t, reintegrationSeal2Controller)
	reintegrationSeal2Controller.reconcilePolicyTransition(reintegrationSeal1Controller.exportPolicyTransition())
	batch, reintegrationSeal2Decision := reintegrationSeal2Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSeal2Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSeal2Decision.Decision)
	assert.Equal(t, reintegrationSeal2Cfg.PolicyManifestDigest, reintegrationSeal2Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSeal2Cfg.PolicyManifestRefreshEpoch, reintegrationSeal2Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSeal2Decision.PolicyActivationTicks)

	staleReintegrationSealSeed := reintegrationSeal2Controller.currentBatch
	staleReintegrationSealController := newAutoTuneControllerWithSeed(100, staleReintegrationSealCfg, &staleReintegrationSealSeed)
	require.NotNil(t, staleReintegrationSealController)
	staleReintegrationSealController.reconcilePolicyTransition(reintegrationSeal2Controller.exportPolicyTransition())
	batch, staleReintegrationSealDecision := staleReintegrationSealController.Resolve(highLag)
	assert.Equal(t, staleReintegrationSealSeed+20, batch)
	assert.Equal(t, "apply_increase", staleReintegrationSealDecision.Decision)
	assert.Equal(
		t,
		reintegrationSeal2Cfg.PolicyManifestDigest,
		staleReintegrationSealDecision.PolicyManifestDigest,
		"lower reintegration seal epochs must remain pinned behind latest verified reintegration-seal ownership",
	)
	assert.Equal(t, reintegrationSeal2Cfg.PolicyManifestRefreshEpoch, staleReintegrationSealDecision.PolicyEpoch)
	assert.Equal(t, 0, staleReintegrationSealDecision.PolicyActivationTicks)

	ambiguousReintegrationSealSeed := staleReintegrationSealController.currentBatch
	ambiguousReintegrationSealController := newAutoTuneControllerWithSeed(100, ambiguousReintegrationSealCfg, &ambiguousReintegrationSealSeed)
	require.NotNil(t, ambiguousReintegrationSealController)
	ambiguousReintegrationSealController.reconcilePolicyTransition(staleReintegrationSealController.exportPolicyTransition())
	batch, ambiguousReintegrationSealDecision := ambiguousReintegrationSealController.Resolve(highLag)
	assert.Equal(t, ambiguousReintegrationSealSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousReintegrationSealDecision.Decision)
	assert.Equal(
		t,
		reintegrationSeal2Cfg.PolicyManifestDigest,
		ambiguousReintegrationSealDecision.PolicyManifestDigest,
		"reintegration-seal markers must remain quarantined until reintegration ownership is explicit",
	)
	assert.Equal(t, reintegrationSeal2Cfg.PolicyManifestRefreshEpoch, ambiguousReintegrationSealDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousReintegrationSealDecision.PolicyActivationTicks)

	conflictingReintegrationSealForwardSeed := ambiguousReintegrationSealController.currentBatch
	conflictingReintegrationSealForwardController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealAliasForwardCfg, &conflictingReintegrationSealForwardSeed)
	require.NotNil(t, conflictingReintegrationSealForwardController)
	conflictingReintegrationSealForwardController.reconcilePolicyTransition(ambiguousReintegrationSealController.exportPolicyTransition())
	batch, conflictingReintegrationSealForwardDecision := conflictingReintegrationSealForwardController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealForwardSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealForwardDecision.Decision)
	assert.Equal(
		t,
		reintegrationSeal2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealForwardDecision.PolicyManifestDigest,
		"conflicting reintegration-seal alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSeal2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealForwardDecision.PolicyActivationTicks)

	conflictingReintegrationSealReverseSeed := conflictingReintegrationSealForwardController.currentBatch
	conflictingReintegrationSealReverseController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealAliasReverseCfg, &conflictingReintegrationSealReverseSeed)
	require.NotNil(t, conflictingReintegrationSealReverseController)
	conflictingReintegrationSealReverseController.reconcilePolicyTransition(conflictingReintegrationSealForwardController.exportPolicyTransition())
	batch, conflictingReintegrationSealReverseDecision := conflictingReintegrationSealReverseController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealReverseSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealReverseDecision.Decision)
	assert.Equal(
		t,
		reintegrationSeal2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealReverseDecision.PolicyManifestDigest,
		"conflicting reintegration-seal alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSeal2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealReverseDecision.PolicyActivationTicks)

	stalePreReintegrationSealSeed := conflictingReintegrationSealReverseController.currentBatch
	stalePreReintegrationSealController := newAutoTuneControllerWithSeed(100, stalePreReintegrationSealCfg, &stalePreReintegrationSealSeed)
	require.NotNil(t, stalePreReintegrationSealController)
	stalePreReintegrationSealController.reconcilePolicyTransition(conflictingReintegrationSealReverseController.exportPolicyTransition())
	batch, stalePreReintegrationSealDecision := stalePreReintegrationSealController.Resolve(highLag)
	assert.Equal(t, stalePreReintegrationSealSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreReintegrationSealDecision.Decision)
	assert.Equal(
		t,
		reintegrationSeal2Cfg.PolicyManifestDigest,
		stalePreReintegrationSealDecision.PolicyManifestDigest,
		"post-reintegration seal stale pre-seal markers must not reclaim ownership",
	)
	assert.Equal(t, reintegrationSeal2Cfg.PolicyManifestRefreshEpoch, stalePreReintegrationSealDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreReintegrationSealDecision.PolicyActivationTicks)

	crashRestartSeed := stalePreReintegrationSealController.currentBatch
	crashRestartController := newAutoTuneControllerWithSeed(100, reintegrationSeal2Cfg, &crashRestartSeed)
	require.NotNil(t, crashRestartController)
	crashRestartController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:                true,
		Version:                 reintegrationSeal2Cfg.PolicyVersion,
		ManifestDigest:          reintegrationSeal2Cfg.PolicyManifestDigest,
		Epoch:                   reintegrationSeal2Cfg.PolicyManifestRefreshEpoch,
		ActivationHoldRemaining: 2,
		FromWarmCheckpoint:      true,
	})
	batch, crashRestartHold := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed, batch)
	assert.Equal(t, "hold_policy_transition", crashRestartHold.Decision)
	assert.Equal(
		t,
		1,
		crashRestartHold.PolicyActivationTicks,
		"crash-restart at reintegration-seal boundary must collapse ambiguous hold windows to one deterministic hold tick",
	)
	batch, crashRestartApplied := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed+20, batch)
	assert.Equal(t, "apply_increase", crashRestartApplied.Decision)
	assert.Equal(t, 0, crashRestartApplied.PolicyActivationTicks)

	reForwardSeed := stalePreReintegrationSealController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreReintegrationSealController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after reintegration-seal must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostReintegrationSealDriftReconciliationRejectsStaleMarkers(t *testing.T) {
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

	lateResurrection2Digest := "manifest-tail-v2b" +
		"|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3" +
		"|rollback-fence-tombstone-expiry-epoch=4" +
		"|rollback-fence-late-marker-hold-epoch=5" +
		"|rollback-fence-late-marker-release-epoch=8" +
		"|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90" +
		"|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130" +
		"|rollback-fence-steady-state-watermark=145|rollback-fence-steady-generation=2" +
		"|rollback-fence-generation-retention-floor=2|rollback-fence-floor-lift-epoch=2" +
		"|rollback-fence-settle-window-epoch=2|rollback-fence-spillover-epoch=2" +
		"|rollback-fence-spillover-rejoin-epoch=2|rollback-fence-rejoin-seal-epoch=2" +
		"|rollback-fence-post-steady-seal-drift-epoch=2|rollback-fence-post-drift-reanchor-epoch=2" +
		"|rollback-fence-post-reanchor-compaction-epoch=2" +
		"|rollback-fence-post-lineage-compaction-expiry-epoch=2" +
		"|rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch=4"
	reintegration2Digest := lateResurrection2Digest + "|rollback-fence-post-late-resurrection-quarantine-reintegration-epoch=6"
	reintegrationSeal2Digest := reintegration2Digest + "|rollback-fence-post-late-resurrection-reintegration-seal-epoch=8"

	reintegrationSeal2Cfg := segment2Cfg
	reintegrationSeal2Cfg.PolicyManifestDigest = reintegrationSeal2Digest
	reintegrationSealDrift1Cfg := segment2Cfg
	reintegrationSealDrift1Cfg.PolicyManifestDigest = reintegrationSeal2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-epoch=9"
	reintegrationSealDrift2Cfg := segment2Cfg
	reintegrationSealDrift2Cfg.PolicyManifestDigest = reintegrationSeal2Digest + "|rollback-fence-post-reintegration-seal-drift-epoch=10"
	staleReintegrationSealDriftCfg := segment2Cfg
	staleReintegrationSealDriftCfg.PolicyManifestDigest = reintegrationSeal2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-epoch=9"
	ambiguousReintegrationSealDriftCfg := segment2Cfg
	ambiguousReintegrationSealDriftCfg.PolicyManifestDigest = reintegration2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-epoch=11"
	conflictingReintegrationSealDriftAliasForwardCfg := segment2Cfg
	conflictingReintegrationSealDriftAliasForwardCfg.PolicyManifestDigest = reintegrationSeal2Digest +
		"|rollback-fence-post-reintegration-seal-drift-epoch=11|rollback-fence-resurrection-reintegration-seal-drift-epoch=9"
	conflictingReintegrationSealDriftAliasReverseCfg := segment2Cfg
	conflictingReintegrationSealDriftAliasReverseCfg.PolicyManifestDigest = reintegrationSeal2Digest +
		"|rollback-fence-resurrection-reintegration-seal-drift-epoch=9|rollback-fence-post-reintegration-seal-drift-epoch=11"
	stalePreReintegrationSealDriftCfg := segment2Cfg
	stalePreReintegrationSealDriftCfg.PolicyManifestDigest = reintegrationSeal2Digest

	baseSeed := 140
	baseController := newAutoTuneControllerWithSeed(100, reintegrationSeal2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, reintegrationSeal2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSeal2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	reintegrationSealDrift1Seed := baseController.currentBatch
	reintegrationSealDrift1Controller := newAutoTuneControllerWithSeed(100, reintegrationSealDrift1Cfg, &reintegrationSealDrift1Seed)
	require.NotNil(t, reintegrationSealDrift1Controller)
	reintegrationSealDrift1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, reintegrationSealDrift1Decision := reintegrationSealDrift1Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSealDrift1Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSealDrift1Decision.Decision)
	assert.Equal(t, reintegrationSealDrift1Cfg.PolicyManifestDigest, reintegrationSealDrift1Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDrift1Cfg.PolicyManifestRefreshEpoch, reintegrationSealDrift1Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSealDrift1Decision.PolicyActivationTicks)

	reintegrationSealDrift2Seed := reintegrationSealDrift1Controller.currentBatch
	reintegrationSealDrift2Controller := newAutoTuneControllerWithSeed(100, reintegrationSealDrift2Cfg, &reintegrationSealDrift2Seed)
	require.NotNil(t, reintegrationSealDrift2Controller)
	reintegrationSealDrift2Controller.reconcilePolicyTransition(reintegrationSealDrift1Controller.exportPolicyTransition())
	batch, reintegrationSealDrift2Decision := reintegrationSealDrift2Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSealDrift2Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSealDrift2Decision.Decision)
	assert.Equal(t, reintegrationSealDrift2Cfg.PolicyManifestDigest, reintegrationSealDrift2Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDrift2Cfg.PolicyManifestRefreshEpoch, reintegrationSealDrift2Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSealDrift2Decision.PolicyActivationTicks)

	staleReintegrationSealDriftSeed := reintegrationSealDrift2Controller.currentBatch
	staleReintegrationSealDriftController := newAutoTuneControllerWithSeed(100, staleReintegrationSealDriftCfg, &staleReintegrationSealDriftSeed)
	require.NotNil(t, staleReintegrationSealDriftController)
	staleReintegrationSealDriftController.reconcilePolicyTransition(reintegrationSealDrift2Controller.exportPolicyTransition())
	batch, staleReintegrationSealDriftDecision := staleReintegrationSealDriftController.Resolve(highLag)
	assert.Equal(t, staleReintegrationSealDriftSeed+20, batch)
	assert.Equal(t, "apply_increase", staleReintegrationSealDriftDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDrift2Cfg.PolicyManifestDigest,
		staleReintegrationSealDriftDecision.PolicyManifestDigest,
		"lower reintegration-seal drift epochs must remain pinned behind latest verified reintegration-seal drift ownership",
	)
	assert.Equal(t, reintegrationSealDrift2Cfg.PolicyManifestRefreshEpoch, staleReintegrationSealDriftDecision.PolicyEpoch)
	assert.Equal(t, 0, staleReintegrationSealDriftDecision.PolicyActivationTicks)

	ambiguousReintegrationSealDriftSeed := staleReintegrationSealDriftController.currentBatch
	ambiguousReintegrationSealDriftController := newAutoTuneControllerWithSeed(100, ambiguousReintegrationSealDriftCfg, &ambiguousReintegrationSealDriftSeed)
	require.NotNil(t, ambiguousReintegrationSealDriftController)
	ambiguousReintegrationSealDriftController.reconcilePolicyTransition(staleReintegrationSealDriftController.exportPolicyTransition())
	batch, ambiguousReintegrationSealDriftDecision := ambiguousReintegrationSealDriftController.Resolve(highLag)
	assert.Equal(t, ambiguousReintegrationSealDriftSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousReintegrationSealDriftDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDrift2Cfg.PolicyManifestDigest,
		ambiguousReintegrationSealDriftDecision.PolicyManifestDigest,
		"reintegration-seal drift markers must remain quarantined until reintegration-seal ownership is explicit",
	)
	assert.Equal(t, reintegrationSealDrift2Cfg.PolicyManifestRefreshEpoch, ambiguousReintegrationSealDriftDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousReintegrationSealDriftDecision.PolicyActivationTicks)

	conflictingReintegrationSealDriftForwardSeed := ambiguousReintegrationSealDriftController.currentBatch
	conflictingReintegrationSealDriftForwardController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealDriftAliasForwardCfg, &conflictingReintegrationSealDriftForwardSeed)
	require.NotNil(t, conflictingReintegrationSealDriftForwardController)
	conflictingReintegrationSealDriftForwardController.reconcilePolicyTransition(ambiguousReintegrationSealDriftController.exportPolicyTransition())
	batch, conflictingReintegrationSealDriftForwardDecision := conflictingReintegrationSealDriftForwardController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealDriftForwardSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealDriftForwardDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDrift2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealDriftForwardDecision.PolicyManifestDigest,
		"conflicting reintegration-seal drift alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSealDrift2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealDriftForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealDriftForwardDecision.PolicyActivationTicks)

	conflictingReintegrationSealDriftReverseSeed := conflictingReintegrationSealDriftForwardController.currentBatch
	conflictingReintegrationSealDriftReverseController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealDriftAliasReverseCfg, &conflictingReintegrationSealDriftReverseSeed)
	require.NotNil(t, conflictingReintegrationSealDriftReverseController)
	conflictingReintegrationSealDriftReverseController.reconcilePolicyTransition(conflictingReintegrationSealDriftForwardController.exportPolicyTransition())
	batch, conflictingReintegrationSealDriftReverseDecision := conflictingReintegrationSealDriftReverseController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealDriftReverseSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealDriftReverseDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDrift2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealDriftReverseDecision.PolicyManifestDigest,
		"conflicting reintegration-seal drift alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSealDrift2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealDriftReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealDriftReverseDecision.PolicyActivationTicks)

	stalePreReintegrationSealDriftSeed := conflictingReintegrationSealDriftReverseController.currentBatch
	stalePreReintegrationSealDriftController := newAutoTuneControllerWithSeed(100, stalePreReintegrationSealDriftCfg, &stalePreReintegrationSealDriftSeed)
	require.NotNil(t, stalePreReintegrationSealDriftController)
	stalePreReintegrationSealDriftController.reconcilePolicyTransition(conflictingReintegrationSealDriftReverseController.exportPolicyTransition())
	batch, stalePreReintegrationSealDriftDecision := stalePreReintegrationSealDriftController.Resolve(highLag)
	assert.Equal(t, stalePreReintegrationSealDriftSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreReintegrationSealDriftDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDrift2Cfg.PolicyManifestDigest,
		stalePreReintegrationSealDriftDecision.PolicyManifestDigest,
		"post-reintegration-seal drift stale pre-drift markers must not reclaim ownership",
	)
	assert.Equal(t, reintegrationSealDrift2Cfg.PolicyManifestRefreshEpoch, stalePreReintegrationSealDriftDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreReintegrationSealDriftDecision.PolicyActivationTicks)

	crashRestartSeed := stalePreReintegrationSealDriftController.currentBatch
	crashRestartController := newAutoTuneControllerWithSeed(100, reintegrationSealDrift2Cfg, &crashRestartSeed)
	require.NotNil(t, crashRestartController)
	crashRestartController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:                true,
		Version:                 reintegrationSealDrift2Cfg.PolicyVersion,
		ManifestDigest:          reintegrationSealDrift2Cfg.PolicyManifestDigest,
		Epoch:                   reintegrationSealDrift2Cfg.PolicyManifestRefreshEpoch,
		ActivationHoldRemaining: 2,
		FromWarmCheckpoint:      true,
	})
	batch, crashRestartHold := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed, batch)
	assert.Equal(t, "hold_policy_transition", crashRestartHold.Decision)
	assert.Equal(
		t,
		1,
		crashRestartHold.PolicyActivationTicks,
		"crash-restart at reintegration-seal drift boundary must collapse ambiguous hold windows to one deterministic hold tick",
	)
	batch, crashRestartApplied := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed+20, batch)
	assert.Equal(t, "apply_increase", crashRestartApplied.Decision)
	assert.Equal(t, 0, crashRestartApplied.PolicyActivationTicks)

	reForwardSeed := stalePreReintegrationSealDriftController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreReintegrationSealDriftController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after reintegration-seal drift reconciliation must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostReintegrationSealDriftReanchorRejectsStaleMarkers(t *testing.T) {
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

	lateResurrection2Digest := "manifest-tail-v2b" +
		"|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3" +
		"|rollback-fence-tombstone-expiry-epoch=4" +
		"|rollback-fence-late-marker-hold-epoch=5" +
		"|rollback-fence-late-marker-release-epoch=8" +
		"|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90" +
		"|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130" +
		"|rollback-fence-steady-state-watermark=145|rollback-fence-steady-generation=2" +
		"|rollback-fence-generation-retention-floor=2|rollback-fence-floor-lift-epoch=2" +
		"|rollback-fence-settle-window-epoch=2|rollback-fence-spillover-epoch=2" +
		"|rollback-fence-spillover-rejoin-epoch=2|rollback-fence-rejoin-seal-epoch=2" +
		"|rollback-fence-post-steady-seal-drift-epoch=2|rollback-fence-post-drift-reanchor-epoch=2" +
		"|rollback-fence-post-reanchor-compaction-epoch=2" +
		"|rollback-fence-post-lineage-compaction-expiry-epoch=2" +
		"|rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch=4"
	reintegration2Digest := lateResurrection2Digest + "|rollback-fence-post-late-resurrection-quarantine-reintegration-epoch=6"
	reintegrationSeal2Digest := reintegration2Digest + "|rollback-fence-post-late-resurrection-reintegration-seal-epoch=8"
	reintegrationSealDrift2Digest := reintegrationSeal2Digest + "|rollback-fence-post-reintegration-seal-drift-epoch=10"

	reintegrationSealDrift2Cfg := segment2Cfg
	reintegrationSealDrift2Cfg.PolicyManifestDigest = reintegrationSealDrift2Digest
	reintegrationSealDriftReanchor1Cfg := segment2Cfg
	reintegrationSealDriftReanchor1Cfg.PolicyManifestDigest = reintegrationSealDrift2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-epoch=11"
	reintegrationSealDriftReanchor2Cfg := segment2Cfg
	reintegrationSealDriftReanchor2Cfg.PolicyManifestDigest = reintegrationSealDrift2Digest + "|rollback-fence-post-reintegration-seal-drift-reanchor-epoch=12"
	staleReintegrationSealDriftReanchorCfg := segment2Cfg
	staleReintegrationSealDriftReanchorCfg.PolicyManifestDigest = reintegrationSealDrift2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-epoch=11"
	ambiguousReintegrationSealDriftReanchorCfg := segment2Cfg
	ambiguousReintegrationSealDriftReanchorCfg.PolicyManifestDigest = reintegrationSeal2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-epoch=13"
	conflictingReintegrationSealDriftReanchorAliasForwardCfg := segment2Cfg
	conflictingReintegrationSealDriftReanchorAliasForwardCfg.PolicyManifestDigest = reintegrationSealDrift2Digest +
		"|rollback-fence-post-reintegration-seal-drift-reanchor-epoch=13|rollback-fence-resurrection-reintegration-seal-drift-reanchor-epoch=11"
	conflictingReintegrationSealDriftReanchorAliasReverseCfg := segment2Cfg
	conflictingReintegrationSealDriftReanchorAliasReverseCfg.PolicyManifestDigest = reintegrationSealDrift2Digest +
		"|rollback-fence-resurrection-reintegration-seal-drift-reanchor-epoch=11|rollback-fence-post-reintegration-seal-drift-reanchor-epoch=13"
	stalePreReintegrationSealDriftReanchorCfg := segment2Cfg
	stalePreReintegrationSealDriftReanchorCfg.PolicyManifestDigest = reintegrationSealDrift2Digest

	baseSeed := 140
	baseController := newAutoTuneControllerWithSeed(100, reintegrationSealDrift2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, reintegrationSealDrift2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDrift2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	reintegrationSealDriftReanchor1Seed := baseController.currentBatch
	reintegrationSealDriftReanchor1Controller := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchor1Cfg, &reintegrationSealDriftReanchor1Seed)
	require.NotNil(t, reintegrationSealDriftReanchor1Controller)
	reintegrationSealDriftReanchor1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, reintegrationSealDriftReanchor1Decision := reintegrationSealDriftReanchor1Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSealDriftReanchor1Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSealDriftReanchor1Decision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchor1Cfg.PolicyManifestDigest, reintegrationSealDriftReanchor1Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchor1Cfg.PolicyManifestRefreshEpoch, reintegrationSealDriftReanchor1Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSealDriftReanchor1Decision.PolicyActivationTicks)

	reintegrationSealDriftReanchor2Seed := reintegrationSealDriftReanchor1Controller.currentBatch
	reintegrationSealDriftReanchor2Controller := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchor2Cfg, &reintegrationSealDriftReanchor2Seed)
	require.NotNil(t, reintegrationSealDriftReanchor2Controller)
	reintegrationSealDriftReanchor2Controller.reconcilePolicyTransition(reintegrationSealDriftReanchor1Controller.exportPolicyTransition())
	batch, reintegrationSealDriftReanchor2Decision := reintegrationSealDriftReanchor2Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSealDriftReanchor2Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSealDriftReanchor2Decision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchor2Cfg.PolicyManifestDigest, reintegrationSealDriftReanchor2Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchor2Cfg.PolicyManifestRefreshEpoch, reintegrationSealDriftReanchor2Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSealDriftReanchor2Decision.PolicyActivationTicks)

	staleReintegrationSealDriftReanchorSeed := reintegrationSealDriftReanchor2Controller.currentBatch
	staleReintegrationSealDriftReanchorController := newAutoTuneControllerWithSeed(100, staleReintegrationSealDriftReanchorCfg, &staleReintegrationSealDriftReanchorSeed)
	require.NotNil(t, staleReintegrationSealDriftReanchorController)
	staleReintegrationSealDriftReanchorController.reconcilePolicyTransition(reintegrationSealDriftReanchor2Controller.exportPolicyTransition())
	batch, staleReintegrationSealDriftReanchorDecision := staleReintegrationSealDriftReanchorController.Resolve(highLag)
	assert.Equal(t, staleReintegrationSealDriftReanchorSeed+20, batch)
	assert.Equal(t, "apply_increase", staleReintegrationSealDriftReanchorDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchor2Cfg.PolicyManifestDigest,
		staleReintegrationSealDriftReanchorDecision.PolicyManifestDigest,
		"lower reintegration-seal drift-reanchor epochs must remain pinned behind latest verified reintegration-seal drift-reanchor ownership",
	)
	assert.Equal(t, reintegrationSealDriftReanchor2Cfg.PolicyManifestRefreshEpoch, staleReintegrationSealDriftReanchorDecision.PolicyEpoch)
	assert.Equal(t, 0, staleReintegrationSealDriftReanchorDecision.PolicyActivationTicks)

	ambiguousReintegrationSealDriftReanchorSeed := staleReintegrationSealDriftReanchorController.currentBatch
	ambiguousReintegrationSealDriftReanchorController := newAutoTuneControllerWithSeed(100, ambiguousReintegrationSealDriftReanchorCfg, &ambiguousReintegrationSealDriftReanchorSeed)
	require.NotNil(t, ambiguousReintegrationSealDriftReanchorController)
	ambiguousReintegrationSealDriftReanchorController.reconcilePolicyTransition(staleReintegrationSealDriftReanchorController.exportPolicyTransition())
	batch, ambiguousReintegrationSealDriftReanchorDecision := ambiguousReintegrationSealDriftReanchorController.Resolve(highLag)
	assert.Equal(t, ambiguousReintegrationSealDriftReanchorSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousReintegrationSealDriftReanchorDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchor2Cfg.PolicyManifestDigest,
		ambiguousReintegrationSealDriftReanchorDecision.PolicyManifestDigest,
		"reintegration-seal drift-reanchor markers must remain quarantined until reintegration-seal drift ownership is explicit",
	)
	assert.Equal(t, reintegrationSealDriftReanchor2Cfg.PolicyManifestRefreshEpoch, ambiguousReintegrationSealDriftReanchorDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousReintegrationSealDriftReanchorDecision.PolicyActivationTicks)

	conflictingReintegrationSealDriftReanchorForwardSeed := ambiguousReintegrationSealDriftReanchorController.currentBatch
	conflictingReintegrationSealDriftReanchorForwardController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealDriftReanchorAliasForwardCfg, &conflictingReintegrationSealDriftReanchorForwardSeed)
	require.NotNil(t, conflictingReintegrationSealDriftReanchorForwardController)
	conflictingReintegrationSealDriftReanchorForwardController.reconcilePolicyTransition(ambiguousReintegrationSealDriftReanchorController.exportPolicyTransition())
	batch, conflictingReintegrationSealDriftReanchorForwardDecision := conflictingReintegrationSealDriftReanchorForwardController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealDriftReanchorForwardSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealDriftReanchorForwardDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchor2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealDriftReanchorForwardDecision.PolicyManifestDigest,
		"conflicting reintegration-seal drift-reanchor alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSealDriftReanchor2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealDriftReanchorForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealDriftReanchorForwardDecision.PolicyActivationTicks)

	conflictingReintegrationSealDriftReanchorReverseSeed := conflictingReintegrationSealDriftReanchorForwardController.currentBatch
	conflictingReintegrationSealDriftReanchorReverseController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealDriftReanchorAliasReverseCfg, &conflictingReintegrationSealDriftReanchorReverseSeed)
	require.NotNil(t, conflictingReintegrationSealDriftReanchorReverseController)
	conflictingReintegrationSealDriftReanchorReverseController.reconcilePolicyTransition(conflictingReintegrationSealDriftReanchorForwardController.exportPolicyTransition())
	batch, conflictingReintegrationSealDriftReanchorReverseDecision := conflictingReintegrationSealDriftReanchorReverseController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealDriftReanchorReverseSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealDriftReanchorReverseDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchor2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealDriftReanchorReverseDecision.PolicyManifestDigest,
		"conflicting reintegration-seal drift-reanchor alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSealDriftReanchor2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealDriftReanchorReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealDriftReanchorReverseDecision.PolicyActivationTicks)

	stalePreReintegrationSealDriftReanchorSeed := conflictingReintegrationSealDriftReanchorReverseController.currentBatch
	stalePreReintegrationSealDriftReanchorController := newAutoTuneControllerWithSeed(100, stalePreReintegrationSealDriftReanchorCfg, &stalePreReintegrationSealDriftReanchorSeed)
	require.NotNil(t, stalePreReintegrationSealDriftReanchorController)
	stalePreReintegrationSealDriftReanchorController.reconcilePolicyTransition(conflictingReintegrationSealDriftReanchorReverseController.exportPolicyTransition())
	batch, stalePreReintegrationSealDriftReanchorDecision := stalePreReintegrationSealDriftReanchorController.Resolve(highLag)
	assert.Equal(t, stalePreReintegrationSealDriftReanchorSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreReintegrationSealDriftReanchorDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchor2Cfg.PolicyManifestDigest,
		stalePreReintegrationSealDriftReanchorDecision.PolicyManifestDigest,
		"post-reintegration-seal drift-reanchor stale pre-reanchor markers must not reclaim ownership",
	)
	assert.Equal(t, reintegrationSealDriftReanchor2Cfg.PolicyManifestRefreshEpoch, stalePreReintegrationSealDriftReanchorDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreReintegrationSealDriftReanchorDecision.PolicyActivationTicks)

	crashRestartSeed := stalePreReintegrationSealDriftReanchorController.currentBatch
	crashRestartController := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchor2Cfg, &crashRestartSeed)
	require.NotNil(t, crashRestartController)
	crashRestartController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:                true,
		Version:                 reintegrationSealDriftReanchor2Cfg.PolicyVersion,
		ManifestDigest:          reintegrationSealDriftReanchor2Cfg.PolicyManifestDigest,
		Epoch:                   reintegrationSealDriftReanchor2Cfg.PolicyManifestRefreshEpoch,
		ActivationHoldRemaining: 2,
		FromWarmCheckpoint:      true,
	})
	batch, crashRestartHold := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed, batch)
	assert.Equal(t, "hold_policy_transition", crashRestartHold.Decision)
	assert.Equal(
		t,
		1,
		crashRestartHold.PolicyActivationTicks,
		"crash-restart at reintegration-seal drift-reanchor boundary must collapse ambiguous hold windows to one deterministic hold tick",
	)
	batch, crashRestartApplied := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed+20, batch)
	assert.Equal(t, "apply_increase", crashRestartApplied.Decision)
	assert.Equal(t, 0, crashRestartApplied.PolicyActivationTicks)

	reForwardSeed := stalePreReintegrationSealDriftReanchorController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreReintegrationSealDriftReanchorController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after reintegration-seal drift-reanchor must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostReintegrationSealDriftReanchorLineageCompactionRejectsStaleMarkers(t *testing.T) {
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

	lateResurrection2Digest := "manifest-tail-v2b" +
		"|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3" +
		"|rollback-fence-tombstone-expiry-epoch=4" +
		"|rollback-fence-late-marker-hold-epoch=5" +
		"|rollback-fence-late-marker-release-epoch=8" +
		"|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90" +
		"|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130" +
		"|rollback-fence-steady-state-watermark=145|rollback-fence-steady-generation=2" +
		"|rollback-fence-generation-retention-floor=2|rollback-fence-floor-lift-epoch=2" +
		"|rollback-fence-settle-window-epoch=2|rollback-fence-spillover-epoch=2" +
		"|rollback-fence-spillover-rejoin-epoch=2|rollback-fence-rejoin-seal-epoch=2" +
		"|rollback-fence-post-steady-seal-drift-epoch=2|rollback-fence-post-drift-reanchor-epoch=2" +
		"|rollback-fence-post-reanchor-compaction-epoch=2" +
		"|rollback-fence-post-lineage-compaction-expiry-epoch=2" +
		"|rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch=4"
	reintegration2Digest := lateResurrection2Digest + "|rollback-fence-post-late-resurrection-quarantine-reintegration-epoch=6"
	reintegrationSeal2Digest := reintegration2Digest + "|rollback-fence-post-late-resurrection-reintegration-seal-epoch=8"
	reintegrationSealDrift2Digest := reintegrationSeal2Digest + "|rollback-fence-post-reintegration-seal-drift-epoch=10"
	reintegrationSealDriftReanchor2Digest := reintegrationSealDrift2Digest + "|rollback-fence-post-reintegration-seal-drift-reanchor-epoch=12"

	reintegrationSealDriftReanchor2Cfg := segment2Cfg
	reintegrationSealDriftReanchor2Cfg.PolicyManifestDigest = reintegrationSealDriftReanchor2Digest
	reintegrationSealDriftReanchorCompaction1Cfg := segment2Cfg
	reintegrationSealDriftReanchorCompaction1Cfg.PolicyManifestDigest = reintegrationSealDriftReanchor2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-epoch=13"
	reintegrationSealDriftReanchorCompaction2Cfg := segment2Cfg
	reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestDigest = reintegrationSealDriftReanchor2Digest + "|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-epoch=14"
	staleReintegrationSealDriftReanchorCompactionCfg := segment2Cfg
	staleReintegrationSealDriftReanchorCompactionCfg.PolicyManifestDigest = reintegrationSealDriftReanchor2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-epoch=13"
	ambiguousReintegrationSealDriftReanchorCompactionCfg := segment2Cfg
	ambiguousReintegrationSealDriftReanchorCompactionCfg.PolicyManifestDigest = reintegrationSealDrift2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-epoch=15"
	conflictingReintegrationSealDriftReanchorCompactionAliasForwardCfg := segment2Cfg
	conflictingReintegrationSealDriftReanchorCompactionAliasForwardCfg.PolicyManifestDigest = reintegrationSealDriftReanchor2Digest +
		"|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-epoch=15|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-epoch=13"
	conflictingReintegrationSealDriftReanchorCompactionAliasReverseCfg := segment2Cfg
	conflictingReintegrationSealDriftReanchorCompactionAliasReverseCfg.PolicyManifestDigest = reintegrationSealDriftReanchor2Digest +
		"|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-epoch=13|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-epoch=15"
	stalePreReintegrationSealDriftReanchorCompactionCfg := segment2Cfg
	stalePreReintegrationSealDriftReanchorCompactionCfg.PolicyManifestDigest = reintegrationSealDriftReanchor2Digest

	baseSeed := 140
	baseController := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchor2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchor2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchor2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	reintegrationSealDriftReanchorCompaction1Seed := baseController.currentBatch
	reintegrationSealDriftReanchorCompaction1Controller := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompaction1Cfg, &reintegrationSealDriftReanchorCompaction1Seed)
	require.NotNil(t, reintegrationSealDriftReanchorCompaction1Controller)
	reintegrationSealDriftReanchorCompaction1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, reintegrationSealDriftReanchorCompaction1Decision := reintegrationSealDriftReanchorCompaction1Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction1Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSealDriftReanchorCompaction1Decision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction1Cfg.PolicyManifestDigest, reintegrationSealDriftReanchorCompaction1Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction1Cfg.PolicyManifestRefreshEpoch, reintegrationSealDriftReanchorCompaction1Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSealDriftReanchorCompaction1Decision.PolicyActivationTicks)

	reintegrationSealDriftReanchorCompaction2Seed := reintegrationSealDriftReanchorCompaction1Controller.currentBatch
	reintegrationSealDriftReanchorCompaction2Controller := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompaction2Cfg, &reintegrationSealDriftReanchorCompaction2Seed)
	require.NotNil(t, reintegrationSealDriftReanchorCompaction2Controller)
	reintegrationSealDriftReanchorCompaction2Controller.reconcilePolicyTransition(reintegrationSealDriftReanchorCompaction1Controller.exportPolicyTransition())
	batch, reintegrationSealDriftReanchorCompaction2Decision := reintegrationSealDriftReanchorCompaction2Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction2Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSealDriftReanchorCompaction2Decision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestDigest, reintegrationSealDriftReanchorCompaction2Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestRefreshEpoch, reintegrationSealDriftReanchorCompaction2Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSealDriftReanchorCompaction2Decision.PolicyActivationTicks)

	staleReintegrationSealDriftReanchorCompactionSeed := reintegrationSealDriftReanchorCompaction2Controller.currentBatch
	staleReintegrationSealDriftReanchorCompactionController := newAutoTuneControllerWithSeed(100, staleReintegrationSealDriftReanchorCompactionCfg, &staleReintegrationSealDriftReanchorCompactionSeed)
	require.NotNil(t, staleReintegrationSealDriftReanchorCompactionController)
	staleReintegrationSealDriftReanchorCompactionController.reconcilePolicyTransition(reintegrationSealDriftReanchorCompaction2Controller.exportPolicyTransition())
	batch, staleReintegrationSealDriftReanchorCompactionDecision := staleReintegrationSealDriftReanchorCompactionController.Resolve(highLag)
	assert.Equal(t, staleReintegrationSealDriftReanchorCompactionSeed+20, batch)
	assert.Equal(t, "apply_increase", staleReintegrationSealDriftReanchorCompactionDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestDigest,
		staleReintegrationSealDriftReanchorCompactionDecision.PolicyManifestDigest,
		"lower reintegration-seal drift-reanchor lineage-compaction epochs must remain pinned behind latest verified lineage-compaction ownership",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestRefreshEpoch, staleReintegrationSealDriftReanchorCompactionDecision.PolicyEpoch)
	assert.Equal(t, 0, staleReintegrationSealDriftReanchorCompactionDecision.PolicyActivationTicks)

	ambiguousReintegrationSealDriftReanchorCompactionSeed := staleReintegrationSealDriftReanchorCompactionController.currentBatch
	ambiguousReintegrationSealDriftReanchorCompactionController := newAutoTuneControllerWithSeed(100, ambiguousReintegrationSealDriftReanchorCompactionCfg, &ambiguousReintegrationSealDriftReanchorCompactionSeed)
	require.NotNil(t, ambiguousReintegrationSealDriftReanchorCompactionController)
	ambiguousReintegrationSealDriftReanchorCompactionController.reconcilePolicyTransition(staleReintegrationSealDriftReanchorCompactionController.exportPolicyTransition())
	batch, ambiguousReintegrationSealDriftReanchorCompactionDecision := ambiguousReintegrationSealDriftReanchorCompactionController.Resolve(highLag)
	assert.Equal(t, ambiguousReintegrationSealDriftReanchorCompactionSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousReintegrationSealDriftReanchorCompactionDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestDigest,
		ambiguousReintegrationSealDriftReanchorCompactionDecision.PolicyManifestDigest,
		"reintegration-seal drift-reanchor lineage-compaction markers must remain quarantined until reintegration-seal drift-reanchor ownership is explicit",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestRefreshEpoch, ambiguousReintegrationSealDriftReanchorCompactionDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousReintegrationSealDriftReanchorCompactionDecision.PolicyActivationTicks)

	conflictingReintegrationSealDriftReanchorCompactionForwardSeed := ambiguousReintegrationSealDriftReanchorCompactionController.currentBatch
	conflictingReintegrationSealDriftReanchorCompactionForwardController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealDriftReanchorCompactionAliasForwardCfg, &conflictingReintegrationSealDriftReanchorCompactionForwardSeed)
	require.NotNil(t, conflictingReintegrationSealDriftReanchorCompactionForwardController)
	conflictingReintegrationSealDriftReanchorCompactionForwardController.reconcilePolicyTransition(ambiguousReintegrationSealDriftReanchorCompactionController.exportPolicyTransition())
	batch, conflictingReintegrationSealDriftReanchorCompactionForwardDecision := conflictingReintegrationSealDriftReanchorCompactionForwardController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealDriftReanchorCompactionForwardSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealDriftReanchorCompactionForwardDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealDriftReanchorCompactionForwardDecision.PolicyManifestDigest,
		"conflicting reintegration-seal drift-reanchor lineage-compaction alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealDriftReanchorCompactionForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealDriftReanchorCompactionForwardDecision.PolicyActivationTicks)

	conflictingReintegrationSealDriftReanchorCompactionReverseSeed := conflictingReintegrationSealDriftReanchorCompactionForwardController.currentBatch
	conflictingReintegrationSealDriftReanchorCompactionReverseController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealDriftReanchorCompactionAliasReverseCfg, &conflictingReintegrationSealDriftReanchorCompactionReverseSeed)
	require.NotNil(t, conflictingReintegrationSealDriftReanchorCompactionReverseController)
	conflictingReintegrationSealDriftReanchorCompactionReverseController.reconcilePolicyTransition(conflictingReintegrationSealDriftReanchorCompactionForwardController.exportPolicyTransition())
	batch, conflictingReintegrationSealDriftReanchorCompactionReverseDecision := conflictingReintegrationSealDriftReanchorCompactionReverseController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealDriftReanchorCompactionReverseSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealDriftReanchorCompactionReverseDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealDriftReanchorCompactionReverseDecision.PolicyManifestDigest,
		"conflicting reintegration-seal drift-reanchor lineage-compaction alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealDriftReanchorCompactionReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealDriftReanchorCompactionReverseDecision.PolicyActivationTicks)

	stalePreReintegrationSealDriftReanchorCompactionSeed := conflictingReintegrationSealDriftReanchorCompactionReverseController.currentBatch
	stalePreReintegrationSealDriftReanchorCompactionController := newAutoTuneControllerWithSeed(100, stalePreReintegrationSealDriftReanchorCompactionCfg, &stalePreReintegrationSealDriftReanchorCompactionSeed)
	require.NotNil(t, stalePreReintegrationSealDriftReanchorCompactionController)
	stalePreReintegrationSealDriftReanchorCompactionController.reconcilePolicyTransition(conflictingReintegrationSealDriftReanchorCompactionReverseController.exportPolicyTransition())
	batch, stalePreReintegrationSealDriftReanchorCompactionDecision := stalePreReintegrationSealDriftReanchorCompactionController.Resolve(highLag)
	assert.Equal(t, stalePreReintegrationSealDriftReanchorCompactionSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreReintegrationSealDriftReanchorCompactionDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestDigest,
		stalePreReintegrationSealDriftReanchorCompactionDecision.PolicyManifestDigest,
		"post-reintegration-seal drift-reanchor lineage-compaction stale pre-compaction markers must not reclaim ownership",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestRefreshEpoch, stalePreReintegrationSealDriftReanchorCompactionDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreReintegrationSealDriftReanchorCompactionDecision.PolicyActivationTicks)

	crashRestartSeed := stalePreReintegrationSealDriftReanchorCompactionController.currentBatch
	crashRestartController := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompaction2Cfg, &crashRestartSeed)
	require.NotNil(t, crashRestartController)
	crashRestartController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:                true,
		Version:                 reintegrationSealDriftReanchorCompaction2Cfg.PolicyVersion,
		ManifestDigest:          reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestDigest,
		Epoch:                   reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestRefreshEpoch,
		ActivationHoldRemaining: 2,
		FromWarmCheckpoint:      true,
	})
	batch, crashRestartHold := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed, batch)
	assert.Equal(t, "hold_policy_transition", crashRestartHold.Decision)
	assert.Equal(
		t,
		1,
		crashRestartHold.PolicyActivationTicks,
		"crash-restart at reintegration-seal drift-reanchor lineage-compaction boundary must collapse ambiguous hold windows to one deterministic hold tick",
	)
	batch, crashRestartApplied := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed+20, batch)
	assert.Equal(t, "apply_increase", crashRestartApplied.Decision)
	assert.Equal(t, 0, crashRestartApplied.PolicyActivationTicks)

	reForwardSeed := stalePreReintegrationSealDriftReanchorCompactionController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreReintegrationSealDriftReanchorCompactionController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after reintegration-seal drift-reanchor lineage-compaction must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostReintegrationSealDriftReanchorLineageCompactionMarkerExpiryRejectsStaleMarkers(t *testing.T) {
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

	lateResurrection2Digest := "manifest-tail-v2b" +
		"|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3" +
		"|rollback-fence-tombstone-expiry-epoch=4" +
		"|rollback-fence-late-marker-hold-epoch=5" +
		"|rollback-fence-late-marker-release-epoch=8" +
		"|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90" +
		"|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130" +
		"|rollback-fence-steady-state-watermark=145|rollback-fence-steady-generation=2" +
		"|rollback-fence-generation-retention-floor=2|rollback-fence-floor-lift-epoch=2" +
		"|rollback-fence-settle-window-epoch=2|rollback-fence-spillover-epoch=2" +
		"|rollback-fence-spillover-rejoin-epoch=2|rollback-fence-rejoin-seal-epoch=2" +
		"|rollback-fence-post-steady-seal-drift-epoch=2|rollback-fence-post-drift-reanchor-epoch=2" +
		"|rollback-fence-post-reanchor-compaction-epoch=2" +
		"|rollback-fence-post-lineage-compaction-expiry-epoch=2" +
		"|rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch=4"
	reintegration2Digest := lateResurrection2Digest + "|rollback-fence-post-late-resurrection-quarantine-reintegration-epoch=6"
	reintegrationSeal2Digest := reintegration2Digest + "|rollback-fence-post-late-resurrection-reintegration-seal-epoch=8"
	reintegrationSealDrift2Digest := reintegrationSeal2Digest + "|rollback-fence-post-reintegration-seal-drift-epoch=10"
	reintegrationSealDriftReanchor2Digest := reintegrationSealDrift2Digest + "|rollback-fence-post-reintegration-seal-drift-reanchor-epoch=12"
	reintegrationSealDriftReanchorCompaction2Digest := reintegrationSealDriftReanchor2Digest + "|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-epoch=14"

	reintegrationSealDriftReanchorCompaction2Cfg := segment2Cfg
	reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompaction2Digest
	reintegrationSealDriftReanchorCompactionExpiry1Cfg := segment2Cfg
	reintegrationSealDriftReanchorCompactionExpiry1Cfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompaction2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch=15"
	reintegrationSealDriftReanchorCompactionExpiry2Cfg := segment2Cfg
	reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompaction2Digest + "|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-epoch=16"
	staleReintegrationSealDriftReanchorCompactionExpiryCfg := segment2Cfg
	staleReintegrationSealDriftReanchorCompactionExpiryCfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompaction2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch=15"
	ambiguousReintegrationSealDriftReanchorCompactionExpiryCfg := segment2Cfg
	ambiguousReintegrationSealDriftReanchorCompactionExpiryCfg.PolicyManifestDigest = reintegrationSealDriftReanchor2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch=17"
	conflictingReintegrationSealDriftReanchorCompactionExpiryAliasForwardCfg := segment2Cfg
	conflictingReintegrationSealDriftReanchorCompactionExpiryAliasForwardCfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompaction2Digest +
		"|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-epoch=17|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch=15"
	conflictingReintegrationSealDriftReanchorCompactionExpiryAliasReverseCfg := segment2Cfg
	conflictingReintegrationSealDriftReanchorCompactionExpiryAliasReverseCfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompaction2Digest +
		"|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-epoch=15|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-epoch=17"
	stalePreReintegrationSealDriftReanchorCompactionExpiryCfg := segment2Cfg
	stalePreReintegrationSealDriftReanchorCompactionExpiryCfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompaction2Digest

	baseSeed := 140
	baseController := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompaction2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchorCompaction2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	reintegrationSealDriftReanchorCompactionExpiry1Seed := baseController.currentBatch
	reintegrationSealDriftReanchorCompactionExpiry1Controller := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompactionExpiry1Cfg, &reintegrationSealDriftReanchorCompactionExpiry1Seed)
	require.NotNil(t, reintegrationSealDriftReanchorCompactionExpiry1Controller)
	reintegrationSealDriftReanchorCompactionExpiry1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, reintegrationSealDriftReanchorCompactionExpiry1Decision := reintegrationSealDriftReanchorCompactionExpiry1Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry1Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSealDriftReanchorCompactionExpiry1Decision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry1Cfg.PolicyManifestDigest, reintegrationSealDriftReanchorCompactionExpiry1Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry1Cfg.PolicyManifestRefreshEpoch, reintegrationSealDriftReanchorCompactionExpiry1Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSealDriftReanchorCompactionExpiry1Decision.PolicyActivationTicks)

	reintegrationSealDriftReanchorCompactionExpiry2Seed := reintegrationSealDriftReanchorCompactionExpiry1Controller.currentBatch
	reintegrationSealDriftReanchorCompactionExpiry2Controller := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompactionExpiry2Cfg, &reintegrationSealDriftReanchorCompactionExpiry2Seed)
	require.NotNil(t, reintegrationSealDriftReanchorCompactionExpiry2Controller)
	reintegrationSealDriftReanchorCompactionExpiry2Controller.reconcilePolicyTransition(reintegrationSealDriftReanchorCompactionExpiry1Controller.exportPolicyTransition())
	batch, reintegrationSealDriftReanchorCompactionExpiry2Decision := reintegrationSealDriftReanchorCompactionExpiry2Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry2Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSealDriftReanchorCompactionExpiry2Decision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestDigest, reintegrationSealDriftReanchorCompactionExpiry2Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestRefreshEpoch, reintegrationSealDriftReanchorCompactionExpiry2Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSealDriftReanchorCompactionExpiry2Decision.PolicyActivationTicks)

	staleReintegrationSealDriftReanchorCompactionExpirySeed := reintegrationSealDriftReanchorCompactionExpiry2Controller.currentBatch
	staleReintegrationSealDriftReanchorCompactionExpiryController := newAutoTuneControllerWithSeed(100, staleReintegrationSealDriftReanchorCompactionExpiryCfg, &staleReintegrationSealDriftReanchorCompactionExpirySeed)
	require.NotNil(t, staleReintegrationSealDriftReanchorCompactionExpiryController)
	staleReintegrationSealDriftReanchorCompactionExpiryController.reconcilePolicyTransition(reintegrationSealDriftReanchorCompactionExpiry2Controller.exportPolicyTransition())
	batch, staleReintegrationSealDriftReanchorCompactionExpiryDecision := staleReintegrationSealDriftReanchorCompactionExpiryController.Resolve(highLag)
	assert.Equal(t, staleReintegrationSealDriftReanchorCompactionExpirySeed+20, batch)
	assert.Equal(t, "apply_increase", staleReintegrationSealDriftReanchorCompactionExpiryDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestDigest,
		staleReintegrationSealDriftReanchorCompactionExpiryDecision.PolicyManifestDigest,
		"lower reintegration-seal drift-reanchor lineage-compaction marker-expiry epochs must remain pinned behind latest verified marker-expiry ownership",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestRefreshEpoch, staleReintegrationSealDriftReanchorCompactionExpiryDecision.PolicyEpoch)
	assert.Equal(t, 0, staleReintegrationSealDriftReanchorCompactionExpiryDecision.PolicyActivationTicks)

	ambiguousReintegrationSealDriftReanchorCompactionExpirySeed := staleReintegrationSealDriftReanchorCompactionExpiryController.currentBatch
	ambiguousReintegrationSealDriftReanchorCompactionExpiryController := newAutoTuneControllerWithSeed(100, ambiguousReintegrationSealDriftReanchorCompactionExpiryCfg, &ambiguousReintegrationSealDriftReanchorCompactionExpirySeed)
	require.NotNil(t, ambiguousReintegrationSealDriftReanchorCompactionExpiryController)
	ambiguousReintegrationSealDriftReanchorCompactionExpiryController.reconcilePolicyTransition(staleReintegrationSealDriftReanchorCompactionExpiryController.exportPolicyTransition())
	batch, ambiguousReintegrationSealDriftReanchorCompactionExpiryDecision := ambiguousReintegrationSealDriftReanchorCompactionExpiryController.Resolve(highLag)
	assert.Equal(t, ambiguousReintegrationSealDriftReanchorCompactionExpirySeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousReintegrationSealDriftReanchorCompactionExpiryDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestDigest,
		ambiguousReintegrationSealDriftReanchorCompactionExpiryDecision.PolicyManifestDigest,
		"reintegration-seal drift-reanchor lineage-compaction marker-expiry candidates must remain quarantined until reintegration-seal drift-reanchor lineage-compaction ownership is explicit",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestRefreshEpoch, ambiguousReintegrationSealDriftReanchorCompactionExpiryDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousReintegrationSealDriftReanchorCompactionExpiryDecision.PolicyActivationTicks)

	conflictingReintegrationSealDriftReanchorCompactionExpiryForwardSeed := ambiguousReintegrationSealDriftReanchorCompactionExpiryController.currentBatch
	conflictingReintegrationSealDriftReanchorCompactionExpiryForwardController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealDriftReanchorCompactionExpiryAliasForwardCfg, &conflictingReintegrationSealDriftReanchorCompactionExpiryForwardSeed)
	require.NotNil(t, conflictingReintegrationSealDriftReanchorCompactionExpiryForwardController)
	conflictingReintegrationSealDriftReanchorCompactionExpiryForwardController.reconcilePolicyTransition(ambiguousReintegrationSealDriftReanchorCompactionExpiryController.exportPolicyTransition())
	batch, conflictingReintegrationSealDriftReanchorCompactionExpiryForwardDecision := conflictingReintegrationSealDriftReanchorCompactionExpiryForwardController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealDriftReanchorCompactionExpiryForwardSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealDriftReanchorCompactionExpiryForwardDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealDriftReanchorCompactionExpiryForwardDecision.PolicyManifestDigest,
		"conflicting reintegration-seal drift-reanchor lineage-compaction marker-expiry alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealDriftReanchorCompactionExpiryForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealDriftReanchorCompactionExpiryForwardDecision.PolicyActivationTicks)

	conflictingReintegrationSealDriftReanchorCompactionExpiryReverseSeed := conflictingReintegrationSealDriftReanchorCompactionExpiryForwardController.currentBatch
	conflictingReintegrationSealDriftReanchorCompactionExpiryReverseController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealDriftReanchorCompactionExpiryAliasReverseCfg, &conflictingReintegrationSealDriftReanchorCompactionExpiryReverseSeed)
	require.NotNil(t, conflictingReintegrationSealDriftReanchorCompactionExpiryReverseController)
	conflictingReintegrationSealDriftReanchorCompactionExpiryReverseController.reconcilePolicyTransition(conflictingReintegrationSealDriftReanchorCompactionExpiryForwardController.exportPolicyTransition())
	batch, conflictingReintegrationSealDriftReanchorCompactionExpiryReverseDecision := conflictingReintegrationSealDriftReanchorCompactionExpiryReverseController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealDriftReanchorCompactionExpiryReverseSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealDriftReanchorCompactionExpiryReverseDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealDriftReanchorCompactionExpiryReverseDecision.PolicyManifestDigest,
		"conflicting reintegration-seal drift-reanchor lineage-compaction marker-expiry alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealDriftReanchorCompactionExpiryReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealDriftReanchorCompactionExpiryReverseDecision.PolicyActivationTicks)

	stalePreReintegrationSealDriftReanchorCompactionExpirySeed := conflictingReintegrationSealDriftReanchorCompactionExpiryReverseController.currentBatch
	stalePreReintegrationSealDriftReanchorCompactionExpiryController := newAutoTuneControllerWithSeed(100, stalePreReintegrationSealDriftReanchorCompactionExpiryCfg, &stalePreReintegrationSealDriftReanchorCompactionExpirySeed)
	require.NotNil(t, stalePreReintegrationSealDriftReanchorCompactionExpiryController)
	stalePreReintegrationSealDriftReanchorCompactionExpiryController.reconcilePolicyTransition(conflictingReintegrationSealDriftReanchorCompactionExpiryReverseController.exportPolicyTransition())
	batch, stalePreReintegrationSealDriftReanchorCompactionExpiryDecision := stalePreReintegrationSealDriftReanchorCompactionExpiryController.Resolve(highLag)
	assert.Equal(t, stalePreReintegrationSealDriftReanchorCompactionExpirySeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreReintegrationSealDriftReanchorCompactionExpiryDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestDigest,
		stalePreReintegrationSealDriftReanchorCompactionExpiryDecision.PolicyManifestDigest,
		"post-reintegration-seal drift-reanchor lineage-compaction marker-expiry stale pre-expiry markers must not reclaim ownership",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestRefreshEpoch, stalePreReintegrationSealDriftReanchorCompactionExpiryDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreReintegrationSealDriftReanchorCompactionExpiryDecision.PolicyActivationTicks)

	crashRestartSeed := stalePreReintegrationSealDriftReanchorCompactionExpiryController.currentBatch
	crashRestartController := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompactionExpiry2Cfg, &crashRestartSeed)
	require.NotNil(t, crashRestartController)
	crashRestartController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:                true,
		Version:                 reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyVersion,
		ManifestDigest:          reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestDigest,
		Epoch:                   reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestRefreshEpoch,
		ActivationHoldRemaining: 2,
		FromWarmCheckpoint:      true,
	})
	batch, crashRestartHold := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed, batch)
	assert.Equal(t, "hold_policy_transition", crashRestartHold.Decision)
	assert.Equal(
		t,
		1,
		crashRestartHold.PolicyActivationTicks,
		"crash-restart at reintegration-seal drift-reanchor lineage-compaction marker-expiry boundary must collapse ambiguous hold windows to one deterministic hold tick",
	)
	batch, crashRestartApplied := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed+20, batch)
	assert.Equal(t, "apply_increase", crashRestartApplied.Decision)
	assert.Equal(t, 0, crashRestartApplied.PolicyActivationTicks)

	reForwardSeed := stalePreReintegrationSealDriftReanchorCompactionExpiryController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreReintegrationSealDriftReanchorCompactionExpiryController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after reintegration-seal drift-reanchor lineage-compaction marker-expiry must deterministically apply one activation hold")
	assert.Equal(t, segment3Cfg.PolicyManifestDigest, reForwardHold.PolicyManifestDigest)
	assert.Equal(t, segment3Cfg.PolicyManifestRefreshEpoch, reForwardHold.PolicyEpoch)
	assert.Equal(t, 1, reForwardHold.PolicyActivationTicks)
}

func TestAutoTuneController_RollbackCheckpointFencePostReintegrationSealDriftReanchorLineageCompactionMarkerExpiryLateResurrectionQuarantineRejectsStaleMarkers(t *testing.T) {
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

	lateResurrection2Digest := "manifest-tail-v2b" +
		"|rollback-from-seq=3|rollback-to-seq=2|rollback-forward-seq=3" +
		"|rollback-fence-tombstone-expiry-epoch=4" +
		"|rollback-fence-late-marker-hold-epoch=5" +
		"|rollback-fence-late-marker-release-epoch=8" +
		"|rollback-fence-late-bridge-seq=3|rollback-fence-late-bridge-release-watermark=90" +
		"|rollback-fence-late-bridge-drain-watermark=120|rollback-fence-live-head=130" +
		"|rollback-fence-steady-state-watermark=145|rollback-fence-steady-generation=2" +
		"|rollback-fence-generation-retention-floor=2|rollback-fence-floor-lift-epoch=2" +
		"|rollback-fence-settle-window-epoch=2|rollback-fence-spillover-epoch=2" +
		"|rollback-fence-spillover-rejoin-epoch=2|rollback-fence-rejoin-seal-epoch=2" +
		"|rollback-fence-post-steady-seal-drift-epoch=2|rollback-fence-post-drift-reanchor-epoch=2" +
		"|rollback-fence-post-reanchor-compaction-epoch=2" +
		"|rollback-fence-post-lineage-compaction-expiry-epoch=2" +
		"|rollback-fence-post-marker-expiry-late-resurrection-quarantine-epoch=4"
	reintegration2Digest := lateResurrection2Digest + "|rollback-fence-post-late-resurrection-quarantine-reintegration-epoch=6"
	reintegrationSeal2Digest := reintegration2Digest + "|rollback-fence-post-late-resurrection-reintegration-seal-epoch=8"
	reintegrationSealDrift2Digest := reintegrationSeal2Digest + "|rollback-fence-post-reintegration-seal-drift-epoch=10"
	reintegrationSealDriftReanchor2Digest := reintegrationSealDrift2Digest + "|rollback-fence-post-reintegration-seal-drift-reanchor-epoch=12"
	reintegrationSealDriftReanchorCompaction2Digest := reintegrationSealDriftReanchor2Digest + "|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-epoch=14"
	reintegrationSealDriftReanchorCompactionExpiry2Digest := reintegrationSealDriftReanchorCompaction2Digest + "|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-epoch=16"

	reintegrationSealDriftReanchorCompactionExpiry2Cfg := segment2Cfg
	reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompactionExpiry2Digest
	reintegrationSealDriftReanchorCompactionExpiryQuarantine1Cfg := segment2Cfg
	reintegrationSealDriftReanchorCompactionExpiryQuarantine1Cfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompactionExpiry2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=17"
	reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg := segment2Cfg
	reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompactionExpiry2Digest + "|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=18"
	staleReintegrationSealDriftReanchorCompactionExpiryQuarantineCfg := segment2Cfg
	staleReintegrationSealDriftReanchorCompactionExpiryQuarantineCfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompactionExpiry2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=17"
	ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineCfg := segment2Cfg
	ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineCfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompaction2Digest + "|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=19"
	conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineAliasForwardCfg := segment2Cfg
	conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineAliasForwardCfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompactionExpiry2Digest +
		"|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=18|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=17"
	conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineAliasReverseCfg := segment2Cfg
	conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineAliasReverseCfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompactionExpiry2Digest +
		"|rollback-fence-resurrection-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=17|rollback-fence-post-reintegration-seal-drift-reanchor-compaction-expiry-quarantine-epoch=18"
	stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineCfg := segment2Cfg
	stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineCfg.PolicyManifestDigest = reintegrationSealDriftReanchorCompactionExpiry2Digest

	baseSeed := 140
	baseController := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompactionExpiry2Cfg, &baseSeed)
	require.NotNil(t, baseController)
	batch, baseDecision := baseController.Resolve(highLag)
	assert.Equal(t, baseSeed+20, batch)
	assert.Equal(t, "apply_increase", baseDecision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestDigest, baseDecision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiry2Cfg.PolicyManifestRefreshEpoch, baseDecision.PolicyEpoch)
	assert.Equal(t, 0, baseDecision.PolicyActivationTicks)

	reintegrationSealDriftReanchorCompactionExpiryQuarantine1Seed := baseController.currentBatch
	reintegrationSealDriftReanchorCompactionExpiryQuarantine1Controller := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompactionExpiryQuarantine1Cfg, &reintegrationSealDriftReanchorCompactionExpiryQuarantine1Seed)
	require.NotNil(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine1Controller)
	reintegrationSealDriftReanchorCompactionExpiryQuarantine1Controller.reconcilePolicyTransition(baseController.exportPolicyTransition())
	batch, reintegrationSealDriftReanchorCompactionExpiryQuarantine1Decision := reintegrationSealDriftReanchorCompactionExpiryQuarantine1Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine1Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSealDriftReanchorCompactionExpiryQuarantine1Decision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine1Cfg.PolicyManifestDigest, reintegrationSealDriftReanchorCompactionExpiryQuarantine1Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine1Cfg.PolicyManifestRefreshEpoch, reintegrationSealDriftReanchorCompactionExpiryQuarantine1Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSealDriftReanchorCompactionExpiryQuarantine1Decision.PolicyActivationTicks)

	reintegrationSealDriftReanchorCompactionExpiryQuarantine2Seed := reintegrationSealDriftReanchorCompactionExpiryQuarantine1Controller.currentBatch
	reintegrationSealDriftReanchorCompactionExpiryQuarantine2Controller := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg, &reintegrationSealDriftReanchorCompactionExpiryQuarantine2Seed)
	require.NotNil(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Controller)
	reintegrationSealDriftReanchorCompactionExpiryQuarantine2Controller.reconcilePolicyTransition(reintegrationSealDriftReanchorCompactionExpiryQuarantine1Controller.exportPolicyTransition())
	batch, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Decision := reintegrationSealDriftReanchorCompactionExpiryQuarantine2Controller.Resolve(highLag)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Seed+20, batch)
	assert.Equal(t, "apply_increase", reintegrationSealDriftReanchorCompactionExpiryQuarantine2Decision.Decision)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestDigest, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Decision.PolicyManifestDigest)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestRefreshEpoch, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Decision.PolicyEpoch)
	assert.Equal(t, 0, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Decision.PolicyActivationTicks)

	staleReintegrationSealDriftReanchorCompactionExpiryQuarantineSeed := reintegrationSealDriftReanchorCompactionExpiryQuarantine2Controller.currentBatch
	staleReintegrationSealDriftReanchorCompactionExpiryQuarantineController := newAutoTuneControllerWithSeed(100, staleReintegrationSealDriftReanchorCompactionExpiryQuarantineCfg, &staleReintegrationSealDriftReanchorCompactionExpiryQuarantineSeed)
	require.NotNil(t, staleReintegrationSealDriftReanchorCompactionExpiryQuarantineController)
	staleReintegrationSealDriftReanchorCompactionExpiryQuarantineController.reconcilePolicyTransition(reintegrationSealDriftReanchorCompactionExpiryQuarantine2Controller.exportPolicyTransition())
	batch, staleReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision := staleReintegrationSealDriftReanchorCompactionExpiryQuarantineController.Resolve(highLag)
	assert.Equal(t, staleReintegrationSealDriftReanchorCompactionExpiryQuarantineSeed+20, batch)
	assert.Equal(t, "apply_increase", staleReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestDigest,
		staleReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.PolicyManifestDigest,
		"lower reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection-quarantine epochs must remain pinned behind latest verified quarantine ownership",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestRefreshEpoch, staleReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.PolicyEpoch)
	assert.Equal(t, 0, staleReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.PolicyActivationTicks)

	ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineSeed := staleReintegrationSealDriftReanchorCompactionExpiryQuarantineController.currentBatch
	ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineController := newAutoTuneControllerWithSeed(100, ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineCfg, &ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineSeed)
	require.NotNil(t, ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineController)
	ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineController.reconcilePolicyTransition(staleReintegrationSealDriftReanchorCompactionExpiryQuarantineController.exportPolicyTransition())
	batch, ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision := ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineController.Resolve(highLag)
	assert.Equal(t, ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineSeed+20, batch)
	assert.Equal(t, "apply_increase", ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestDigest,
		ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.PolicyManifestDigest,
		"reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection-quarantine candidates must remain quarantined until reintegration-seal drift-reanchor lineage-compaction marker-expiry ownership is explicit",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestRefreshEpoch, ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.PolicyEpoch)
	assert.Equal(t, 0, ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.PolicyActivationTicks)

	conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardSeed := ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineController.currentBatch
	conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineAliasForwardCfg, &conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardSeed)
	require.NotNil(t, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardController)
	conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardController.reconcilePolicyTransition(ambiguousReintegrationSealDriftReanchorCompactionExpiryQuarantineController.exportPolicyTransition())
	batch, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardDecision := conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardDecision.PolicyManifestDigest,
		"conflicting reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection-quarantine alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardDecision.PolicyActivationTicks)

	conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseSeed := conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardController.currentBatch
	conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseController := newAutoTuneControllerWithSeed(100, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineAliasReverseCfg, &conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseSeed)
	require.NotNil(t, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseController)
	conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseController.reconcilePolicyTransition(conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineForwardController.exportPolicyTransition())
	batch, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseDecision := conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseController.Resolve(highLag)
	assert.Equal(t, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseSeed+20, batch)
	assert.Equal(t, "apply_increase", conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestDigest,
		conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseDecision.PolicyManifestDigest,
		"conflicting reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection-quarantine alias values must remain quarantined regardless of token order",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestRefreshEpoch, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseDecision.PolicyEpoch)
	assert.Equal(t, 0, conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseDecision.PolicyActivationTicks)

	stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineSeed := conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseController.currentBatch
	stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineController := newAutoTuneControllerWithSeed(100, stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineCfg, &stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineSeed)
	require.NotNil(t, stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineController)
	stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineController.reconcilePolicyTransition(conflictingReintegrationSealDriftReanchorCompactionExpiryQuarantineReverseController.exportPolicyTransition())
	batch, stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision := stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineController.Resolve(highLag)
	assert.Equal(t, stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineSeed+20, batch)
	assert.Equal(t, "apply_increase", stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.Decision)
	assert.Equal(
		t,
		reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestDigest,
		stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.PolicyManifestDigest,
		"post-reintegration-seal drift-reanchor lineage-compaction marker-expiry stale pre-quarantine markers must not reclaim ownership",
	)
	assert.Equal(t, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestRefreshEpoch, stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.PolicyEpoch)
	assert.Equal(t, 0, stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineDecision.PolicyActivationTicks)

	crashRestartSeed := stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineController.currentBatch
	crashRestartController := newAutoTuneControllerWithSeed(100, reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg, &crashRestartSeed)
	require.NotNil(t, crashRestartController)
	crashRestartController.reconcilePolicyTransition(autoTunePolicyTransition{
		HasState:                true,
		Version:                 reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyVersion,
		ManifestDigest:          reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestDigest,
		Epoch:                   reintegrationSealDriftReanchorCompactionExpiryQuarantine2Cfg.PolicyManifestRefreshEpoch,
		ActivationHoldRemaining: 2,
		FromWarmCheckpoint:      true,
	})
	batch, crashRestartHold := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed, batch)
	assert.Equal(t, "hold_policy_transition", crashRestartHold.Decision)
	assert.Equal(
		t,
		1,
		crashRestartHold.PolicyActivationTicks,
		"crash-restart at reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection-quarantine boundary must collapse ambiguous hold windows to one deterministic hold tick",
	)
	batch, crashRestartApplied := crashRestartController.Resolve(highLag)
	assert.Equal(t, crashRestartSeed+20, batch)
	assert.Equal(t, "apply_increase", crashRestartApplied.Decision)
	assert.Equal(t, 0, crashRestartApplied.PolicyActivationTicks)

	reForwardSeed := stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineController.currentBatch
	reForwardController := newAutoTuneControllerWithSeed(100, segment3Cfg, &reForwardSeed)
	require.NotNil(t, reForwardController)
	reForwardController.reconcilePolicyTransition(stalePreReintegrationSealDriftReanchorCompactionExpiryQuarantineController.exportPolicyTransition())
	batch, reForwardHold := reForwardController.Resolve(highLag)
	assert.Equal(t, reForwardSeed, batch)
	assert.Equal(t, "hold_policy_transition", reForwardHold.Decision, "rollback+re-forward after reintegration-seal drift-reanchor lineage-compaction marker-expiry late-resurrection-quarantine must deterministically apply one activation hold")
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
