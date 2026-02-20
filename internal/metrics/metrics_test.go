package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMetrics_AllVariablesNonNil(t *testing.T) {
	t.Parallel()

	vars := []struct {
		name string
		val  any
	}{
		{"CoordinatorTicksTotal", CoordinatorTicksTotal},
		{"CoordinatorJobsCreated", CoordinatorJobsCreated},
		{"CoordinatorTickErrors", CoordinatorTickErrors},
		{"FetcherBatchesProcessed", FetcherBatchesProcessed},
		{"FetcherTxFetched", FetcherTxFetched},
		{"FetcherErrors", FetcherErrors},
		{"FetcherLatency", FetcherLatency},
		{"NormalizerBatchesProcessed", NormalizerBatchesProcessed},
		{"NormalizerErrors", NormalizerErrors},
		{"NormalizerLatency", NormalizerLatency},
		{"IngesterBatchesProcessed", IngesterBatchesProcessed},
		{"IngesterBalanceEventsWritten", IngesterBalanceEventsWritten},
		{"IngesterErrors", IngesterErrors},
		{"IngesterLatency", IngesterLatency},
		{"IngesterDeniedEventsSkipped", IngesterDeniedEventsSkipped},
		{"IngesterScamTokensDetected", IngesterScamTokensDetected},
		{"PipelineCursorSequence", PipelineCursorSequence},
		{"PipelineChannelDepth", PipelineChannelDepth},
		{"DBPoolOpen", DBPoolOpen},
		{"DBPoolInUse", DBPoolInUse},
		{"DBPoolIdle", DBPoolIdle},
		{"DBPoolWaitCount", DBPoolWaitCount},
		{"DBPoolWaitDurationSeconds", DBPoolWaitDurationSeconds},
	}

	for _, v := range vars {
		assert.NotNilf(t, v.val, "%s should not be nil", v.name)
	}
}

func TestMetrics_CounterIncrementNoPanic(t *testing.T) {
	t.Parallel()

	labels := []string{"test-chain", "test-network"}

	assert.NotPanics(t, func() { CoordinatorTicksTotal.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { CoordinatorJobsCreated.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { CoordinatorTickErrors.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { FetcherBatchesProcessed.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { FetcherTxFetched.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { FetcherErrors.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { NormalizerBatchesProcessed.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { NormalizerErrors.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { IngesterBatchesProcessed.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { IngesterBalanceEventsWritten.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { IngesterErrors.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { IngesterDeniedEventsSkipped.WithLabelValues(labels...).Inc() })
	assert.NotPanics(t, func() { IngesterScamTokensDetected.WithLabelValues(labels...).Inc() })
}

func TestMetrics_HistogramObserveNoPanic(t *testing.T) {
	t.Parallel()

	labels := []string{"test-chain", "test-network"}

	assert.NotPanics(t, func() { FetcherLatency.WithLabelValues(labels...).Observe(1.5) })
	assert.NotPanics(t, func() { NormalizerLatency.WithLabelValues(labels...).Observe(1.5) })
	assert.NotPanics(t, func() { IngesterLatency.WithLabelValues(labels...).Observe(1.5) })
}

func TestMetrics_GaugeSetNoPanic(t *testing.T) {
	t.Parallel()

	labels2 := []string{"test-chain", "test-network"}

	assert.NotPanics(t, func() { PipelineCursorSequence.WithLabelValues("test-chain", "test-network").Set(42.0) })
	assert.NotPanics(t, func() { PipelineChannelDepth.WithLabelValues("test-chain", "test-network", "test-stage").Set(42.0) })
	assert.NotPanics(t, func() { DBPoolOpen.WithLabelValues(labels2...).Set(42.0) })
	assert.NotPanics(t, func() { DBPoolInUse.WithLabelValues(labels2...).Set(42.0) })
	assert.NotPanics(t, func() { DBPoolIdle.WithLabelValues(labels2...).Set(42.0) })
	assert.NotPanics(t, func() { DBPoolWaitCount.WithLabelValues(labels2...).Set(42.0) })
	assert.NotPanics(t, func() { DBPoolWaitDurationSeconds.WithLabelValues(labels2...).Set(42.0) })
}
