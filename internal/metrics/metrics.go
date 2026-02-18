package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Pipeline stage counters and histograms, partitioned by chain + network.

var (
	// Coordinator
	CoordinatorTicksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "coordinator",
		Name:      "ticks_total",
		Help:      "Total coordinator ticks",
	}, []string{"chain", "network"})

	CoordinatorJobsCreated = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "coordinator",
		Name:      "jobs_created_total",
		Help:      "Total fetch jobs created",
	}, []string{"chain", "network"})

	CoordinatorTickErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "coordinator",
		Name:      "tick_errors_total",
		Help:      "Total coordinator tick errors",
	}, []string{"chain", "network"})

	// Fetcher
	FetcherBatchesProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "fetcher",
		Name:      "batches_processed_total",
		Help:      "Total raw batches produced by fetcher",
	}, []string{"chain", "network"})

	FetcherTxFetched = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "fetcher",
		Name:      "transactions_fetched_total",
		Help:      "Total transactions fetched from chain RPC",
	}, []string{"chain", "network"})

	FetcherErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "fetcher",
		Name:      "errors_total",
		Help:      "Total fetcher errors (after retry exhaustion)",
	}, []string{"chain", "network"})

	FetcherLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "fetcher",
		Name:      "job_duration_seconds",
		Help:      "Fetcher job processing duration",
		Buckets:   prometheus.DefBuckets,
	}, []string{"chain", "network"})

	// Normalizer
	NormalizerBatchesProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "normalizer",
		Name:      "batches_processed_total",
		Help:      "Total normalized batches produced",
	}, []string{"chain", "network"})

	NormalizerErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "normalizer",
		Name:      "errors_total",
		Help:      "Total normalizer errors (after retry exhaustion)",
	}, []string{"chain", "network"})

	NormalizerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "normalizer",
		Name:      "batch_duration_seconds",
		Help:      "Normalizer batch processing duration (including gRPC decode)",
		Buckets:   prometheus.DefBuckets,
	}, []string{"chain", "network"})

	// Ingester
	IngesterBatchesProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "ingester",
		Name:      "batches_processed_total",
		Help:      "Total batches ingested into database",
	}, []string{"chain", "network"})

	IngesterBalanceEventsWritten = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "ingester",
		Name:      "balance_events_written_total",
		Help:      "Total balance events written to database",
	}, []string{"chain", "network"})

	IngesterErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "ingester",
		Name:      "errors_total",
		Help:      "Total ingester errors (after retry exhaustion)",
	}, []string{"chain", "network"})

	IngesterLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "ingester",
		Name:      "batch_duration_seconds",
		Help:      "Ingester batch processing duration (DB transaction)",
		Buckets:   prometheus.DefBuckets,
	}, []string{"chain", "network"})

	// Scam detection
	IngesterDeniedEventsSkipped = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "ingester",
		Name:      "denied_events_skipped_total",
		Help:      "Total balance events skipped due to denied/scam token",
	}, []string{"chain", "network"})

	IngesterScamTokensDetected = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "ingester",
		Name:      "scam_tokens_detected_total",
		Help:      "Total tokens auto-detected and denied as scam",
	}, []string{"chain", "network"})

	// Pipeline-level
	PipelineCursorSequence = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "pipeline",
		Name:      "cursor_sequence",
		Help:      "Latest committed cursor sequence per chain/network/address",
	}, []string{"chain", "network", "address"})

	PipelineChannelDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "pipeline",
		Name:      "channel_depth",
		Help:      "Current depth of pipeline channel buffers",
	}, []string{"chain", "network", "stage"})

	// Database pool
	DBPoolOpen = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "postgres",
		Name:      "db_pool_open",
		Help:      "Current number of open PostgreSQL connections in the pool",
	}, []string{"chain", "network"})

	DBPoolInUse = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "postgres",
		Name:      "db_pool_in_use",
		Help:      "Current number of in-use PostgreSQL connections in the pool",
	}, []string{"chain", "network"})

	DBPoolIdle = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "postgres",
		Name:      "db_pool_idle",
		Help:      "Current number of idle PostgreSQL connections in the pool",
	}, []string{"chain", "network"})

	DBPoolWaitCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "postgres",
		Name:      "db_pool_wait_count",
		Help:      "Cumulative count of waits for PostgreSQL connections from pool",
	}, []string{"chain", "network"})

	DBPoolWaitDurationSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "postgres",
		Name:      "db_pool_wait_duration_seconds",
		Help:      "Latest PostgreSQL pool wait duration in seconds",
	}, []string{"chain", "network"})
)
