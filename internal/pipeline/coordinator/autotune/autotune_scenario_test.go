package autotune

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoTune_GradualLagIncreaseThenDecrease(t *testing.T) {
	controller := newAutoTuneController(50, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          10,
		MaxBatchSize:          100,
		StepUp:                10,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 80,
		QueueLowWatermarkPct:  30,
		HysteresisTicks:       2,
		CooldownTicks:         1,
	})
	require.NotNil(t, controller)

	// Phase 1: Gradually increasing lag should trigger batch increases.
	lagValues := []int64{50, 80, 120, 150, 200, 250}
	var lastBatch int
	for _, lag := range lagValues {
		input := autoTuneInputs{
			HasHeadSignal:      true,
			HeadSequence:       1000 + lag,
			HasMinCursorSignal: true,
			MinCursorSequence:  1000,
			QueueDepth:         0,
			QueueCapacity:      10,
		}
		lastBatch, _ = controller.Resolve(input)
	}
	// After sustained high lag, batch should have increased from 50.
	assert.Greater(t, lastBatch, 50, "batch should increase under sustained high lag")

	// Phase 2: Lag decreases below low watermark with low queue.
	for i := 0; i < 6; i++ {
		input := autoTuneInputs{
			HasHeadSignal:      true,
			HeadSequence:       1010,
			HasMinCursorSignal: true,
			MinCursorSequence:  1000,
			QueueDepth:         1,
			QueueCapacity:      10,
		}
		lastBatch, _ = controller.Resolve(input)
	}
	// After sustained low lag and low queue, batch should have decreased.
	assert.Less(t, lastBatch, 100, "batch should decrease when lag is low and queue is low")
}

func TestAutoTune_RapidOscillationCooldownSuppression(t *testing.T) {
	controller := newAutoTuneController(50, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          10,
		MaxBatchSize:          100,
		StepUp:                10,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 80,
		QueueLowWatermarkPct:  30,
		HysteresisTicks:       2,
		CooldownTicks:         3,
	})
	require.NotNil(t, controller)

	highLag := autoTuneInputs{
		HasHeadSignal: true, HeadSequence: 1200,
		HasMinCursorSignal: true, MinCursorSequence: 1000,
		QueueDepth: 0, QueueCapacity: 10,
	}
	highQueue := autoTuneInputs{
		HasHeadSignal: true, HeadSequence: 1010,
		HasMinCursorSignal: true, MinCursorSequence: 1000,
		QueueDepth: 9, QueueCapacity: 10,
	}

	// Trigger an increase first.
	controller.Resolve(highLag)
	_, d := controller.Resolve(highLag)
	assert.Equal(t, "apply_increase", d.Decision)

	// Now immediately oscillate to high queue.
	batchAfterIncrease := d.BatchAfter
	var deferCount int
	for i := 0; i < 4; i++ {
		_, d = controller.Resolve(highQueue)
		if d.Decision == "defer_cooldown" {
			deferCount++
		}
	}
	assert.Greater(t, deferCount, 0, "cooldown should suppress at least one opposite signal")

	// Eventually the decrease should apply after cooldown expires.
	for i := 0; i < 10; i++ {
		_, d = controller.Resolve(highQueue)
		if d.Decision == "apply_decrease" {
			break
		}
	}
	assert.Equal(t, "apply_decrease", d.Decision, "decrease should eventually apply after cooldown")
	assert.Less(t, d.BatchAfter, batchAfterIncrease, "batch should have decreased")
}

func TestAutoTune_ChainScopedIsolation(t *testing.T) {
	cfg := AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          10,
		MaxBatchSize:          100,
		StepUp:                10,
		StepDown:              10,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 80,
		QueueLowWatermarkPct:  30,
		HysteresisTicks:       2,
		CooldownTicks:         1,
	}

	controllerA := newAutoTuneController(50, cfg)
	controllerB := newAutoTuneController(50, cfg)
	require.NotNil(t, controllerA)
	require.NotNil(t, controllerB)

	// Drive controller A with high lag to increase batch.
	highLagA := autoTuneInputs{
		Chain: "solana", Network: "devnet",
		HasHeadSignal: true, HeadSequence: 1500,
		HasMinCursorSignal: true, MinCursorSequence: 1000,
		QueueDepth: 0, QueueCapacity: 10,
	}
	controllerA.Resolve(highLagA)
	batchA, _ := controllerA.Resolve(highLagA)

	// Controller B sees no lag at all.
	noLagB := autoTuneInputs{
		Chain: "base", Network: "sepolia",
		HasHeadSignal: true, HeadSequence: 1005,
		HasMinCursorSignal: true, MinCursorSequence: 1000,
		QueueDepth: 0, QueueCapacity: 10,
	}
	batchB, _ := controllerB.Resolve(noLagB)

	assert.Greater(t, batchA, batchB, "controller A should have higher batch than B due to high lag")
	assert.Equal(t, 50, batchB, "controller B should remain at initial batch size")
}

func TestAutoTune_TelemetryStaleRecoverySequence(t *testing.T) {
	controller := newAutoTuneController(50, AutoTuneConfig{
		Enabled:                true,
		MinBatchSize:           10,
		MaxBatchSize:           100,
		StepUp:                 10,
		StepDown:               10,
		LagHighWatermark:       100,
		LagLowWatermark:        20,
		QueueHighWatermarkPct:  80,
		QueueLowWatermarkPct:   30,
		HysteresisTicks:        2,
		CooldownTicks:          1,
		TelemetryStaleTicks:    2,
		TelemetryRecoveryTicks: 2,
	})
	require.NotNil(t, controller)

	healthyInput := autoTuneInputs{
		HasHeadSignal: true, HeadSequence: 1200,
		HasMinCursorSignal: true, MinCursorSequence: 1000,
		QueueDepth: 0, QueueCapacity: 10,
	}

	// Initial healthy resolve to arm telemetry.
	_, d := controller.Resolve(healthyInput)
	assert.Equal(t, "healthy", d.TelemetryState)

	// Simulate telemetry going stale (no head signal).
	staleInput := autoTuneInputs{
		HasHeadSignal:      false,
		HasMinCursorSignal: false,
		QueueDepth:         0,
		QueueCapacity:      10,
	}

	// Tick 1: not yet stale (threshold is 2)
	_, d = controller.Resolve(staleInput)
	assert.Equal(t, "healthy", d.TelemetryState)

	// Tick 2: now stale
	_, d = controller.Resolve(staleInput)
	assert.Equal(t, "stale_fallback", d.TelemetryState)

	// Stay stale one more tick
	_, d = controller.Resolve(staleInput)
	assert.Equal(t, "stale_fallback", d.TelemetryState)

	// Recover: first healthy tick triggers recovery_hold (tick 1 of 2)
	_, d = controller.Resolve(healthyInput)
	assert.Equal(t, "recovery_hold", d.TelemetryState)

	// Second healthy tick completes recovery (tick 2 of 2), returns recovery_hold
	_, d = controller.Resolve(healthyInput)
	assert.Equal(t, "recovery_hold", d.TelemetryState)

	// Third healthy tick: recovery complete, back to healthy
	_, d = controller.Resolve(healthyInput)
	assert.Equal(t, "healthy", d.TelemetryState)
}

func TestAutoTune_BoundaryClampingSaturation(t *testing.T) {
	controller := newAutoTuneController(90, AutoTuneConfig{
		Enabled:               true,
		MinBatchSize:          10,
		MaxBatchSize:          100,
		StepUp:                20,
		StepDown:              20,
		LagHighWatermark:      100,
		LagLowWatermark:       20,
		QueueHighWatermarkPct: 80,
		QueueLowWatermarkPct:  30,
		HysteresisTicks:       2,
		CooldownTicks:         1,
	})
	require.NotNil(t, controller)

	highLag := autoTuneInputs{
		HasHeadSignal: true, HeadSequence: 1500,
		HasMinCursorSignal: true, MinCursorSequence: 1000,
		QueueDepth: 0, QueueCapacity: 10,
	}

	// Drive to max: 90 -> 100 (clamped at max)
	controller.Resolve(highLag)
	batch, d := controller.Resolve(highLag)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "apply_increase", d.Decision)

	// Further increases should be clamped.
	controller.Resolve(highLag)
	batch, d = controller.Resolve(highLag)
	assert.Equal(t, 100, batch)
	assert.Equal(t, "clamped_increase", d.Decision)

	// Now drive to min with high queue pressure.
	lowInput := autoTuneInputs{
		HasHeadSignal: true, HeadSequence: 1010,
		HasMinCursorSignal: true, MinCursorSequence: 1000,
		QueueDepth: 9, QueueCapacity: 10,
	}

	// Drive down repeatedly.
	for i := 0; i < 20; i++ {
		batch, d = controller.Resolve(lowInput)
		if batch <= 10 {
			break
		}
	}
	assert.Equal(t, 10, batch, "batch should reach minimum")

	// Further decreases should be clamped.
	controller.Resolve(lowInput)
	batch, d = controller.Resolve(lowInput)
	assert.Equal(t, 10, batch)
	assert.Equal(t, "clamped_decrease", d.Decision)
}
