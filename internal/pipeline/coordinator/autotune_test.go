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

func intPtr(v int) *int {
	return &v
}
