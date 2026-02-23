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

	CoordinatorJobsDropped = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "coordinator",
		Name:      "jobs_dropped_total",
		Help:      "Number of times coordinator had to wait for full job channel",
	}, []string{"chain", "network"})

	CoordinatorTickErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "coordinator",
		Name:      "tick_errors_total",
		Help:      "Total coordinator tick errors",
	}, []string{"chain", "network"})

	CoordinatorTickLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "coordinator",
		Name:      "tick_duration_seconds",
		Help:      "Coordinator tick processing duration",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
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
		Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
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

	NormalizerDecodeFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "normalizer",
		Name:      "decode_failures_total",
		Help:      "Total individual signature decode failures (partial decode isolation)",
	}, []string{"chain", "network"})

	NormalizerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "normalizer",
		Name:      "batch_duration_seconds",
		Help:      "Normalizer batch processing duration (including gRPC decode)",
		Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
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
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
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

	// Negative balance detection
	IngesterNegativeBalancesDetected = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "ingester",
		Name:      "negative_balances_detected_total",
		Help:      "Number of negative balance calculations detected and clamped to zero",
	}, []string{"chain", "network"})

	// Pipeline-level
	PipelineCursorSequence = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "pipeline",
		Name:      "cursor_sequence",
		Help:      "Latest committed cursor sequence per chain/network",
	}, []string{"chain", "network"})

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

	// Token deny list cache
	DeniedCacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "cache",
		Name:      "denied_token_hits_total",
		Help:      "Total denied token cache hits",
	}, []string{"chain", "network"})

	DeniedCacheMisses = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "cache",
		Name:      "denied_token_misses_total",
		Help:      "Total denied token cache misses",
	}, []string{"chain", "network"})

	// RPC rate limiter
	RPCRateLimitWaits = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "rpc",
		Name:      "rate_limit_waits_total",
		Help:      "Total times RPC calls waited for rate limiter",
	}, []string{"chain"})

	// Reorg detector
	ReorgDetectedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "reorg_detector",
		Name:      "reorg_detected_total",
		Help:      "Total block reorgs detected",
	}, []string{"chain", "network"})

	ReorgDetectorCheckLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "reorg_detector",
		Name:      "check_duration_seconds",
		Help:      "Reorg detector check duration",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"chain", "network"})

	ReorgDetectorUnfinalizedBlocks = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "reorg_detector",
		Name:      "unfinalized_blocks",
		Help:      "Current number of unfinalized indexed blocks",
	}, []string{"chain", "network"})

	ReorgDetectorRPCErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "reorg_detector",
		Name:      "rpc_errors_total",
		Help:      "Total RPC errors encountered during reorg detection",
	}, []string{"chain", "network"})

	// Finalizer
	FinalizerPromotionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "finalizer",
		Name:      "promotions_total",
		Help:      "Total finality promotions sent",
	}, []string{"chain", "network"})

	FinalizerPrunedBlocksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "finalizer",
		Name:      "pruned_blocks_total",
		Help:      "Total finalized blocks pruned from indexed_blocks",
	}, []string{"chain", "network"})

	FinalizerLatestFinalizedBlock = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "finalizer",
		Name:      "latest_finalized_block",
		Help:      "Latest finalized block number",
	}, []string{"chain", "network"})

	// Ingester reorg/finality
	IngesterReorgRollbacksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "ingester",
		Name:      "reorg_rollbacks_total",
		Help:      "Total reorg rollbacks processed by ingester",
	}, []string{"chain", "network"})

	IngesterFinalityPromotionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "ingester",
		Name:      "finality_promotions_total",
		Help:      "Total finality promotions processed by ingester",
	}, []string{"chain", "network"})

	// Replay / re-indexing
	ReplayPurgesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "replay",
		Name:      "purges_total",
		Help:      "Total replay purge operations executed",
	}, []string{"chain", "network"})

	ReplayPurgedEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "replay",
		Name:      "purged_events_total",
		Help:      "Total balance events purged by replay operations",
	}, []string{"chain", "network"})

	ReplayPurgeDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "replay",
		Name:      "purge_duration_seconds",
		Help:      "Replay purge operation duration",
		Buckets:   []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"chain", "network"})

	ReplayDryRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "replay",
		Name:      "dry_runs_total",
		Help:      "Total replay dry run operations executed",
	}, []string{"chain", "network"})

	ReplayBalancesReversedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "replay",
		Name:      "balances_reversed_total",
		Help:      "Total balance deltas reversed during replay purge operations",
	}, []string{"chain", "network"})

	ReplayTransactionsDeletedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "replay",
		Name:      "transactions_deleted_total",
		Help:      "Total transactions deleted during replay purge operations",
	}, []string{"chain", "network"})

	ReplayBlocksDeletedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "replay",
		Name:      "blocks_deleted_total",
		Help:      "Total indexed blocks deleted during replay purge operations",
	}, []string{"chain", "network"})

	ReplayCursorsRewoundTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "replay",
		Name:      "cursors_rewound_total",
		Help:      "Total address cursors rewound during replay purge operations",
	}, []string{"chain", "network"})

	// Alerts
	AlertsSentTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "alert",
		Name:      "sent_total",
		Help:      "Total alerts sent",
	}, []string{"channel", "alert_type"})

	AlertsCooldownSkipped = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "alert",
		Name:      "cooldown_skipped_total",
		Help:      "Total alerts skipped due to cooldown",
	}, []string{"channel", "alert_type"})

	// Address index
	AddressIndexBloomRejects = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "address_index",
		Name:      "bloom_rejects_total",
		Help:      "Total addresses rejected by bloom filter (definite non-members)",
	}, []string{"chain", "network"})

	AddressIndexDBLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "address_index",
		Name:      "db_lookups_total",
		Help:      "Total address lookups that fell through to database",
	}, []string{"chain", "network"})

	// Solana block scan
	SolanaBlockScanSkippedSlots = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "solana",
		Name:      "block_scan_skipped_slots_total",
		Help:      "Total skipped slots encountered during Solana block scanning",
	}, []string{"network"})

	// Balance event bulk insert
	BalanceEventBulkInsertSize = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "balance_event",
		Name:      "bulk_insert_size",
		Help:      "Number of events per bulk insert batch",
		Buckets:   []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 1500},
	}, []string{"chain", "network"})

	// Pipeline stage-to-stage latency attribution
	PipelineE2ELatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "pipeline",
		Name:      "stage_e2e_latency_seconds",
		Help:      "End-to-end latency from fetch to ingest completion",
		Buckets:   prometheus.ExponentialBuckets(0.1, 2, 12),
	}, []string{"chain", "network"})

	IngestLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "pipeline",
		Name:      "ingest_latency_seconds",
		Help:      "Latency from normalization to ingest completion",
		Buckets:   prometheus.ExponentialBuckets(0.05, 2, 10),
	}, []string{"chain", "network"})

	// Pipeline E2E latency
	PipelineE2ELatencySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "indexer",
		Subsystem: "pipeline",
		Name:      "e2e_latency_seconds",
		Help:      "End-to-end latency from block production to DB commit",
		Buckets:   []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"chain", "network"})

	// RPC call metrics
	RPCCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "rpc",
		Name:      "calls_total",
		Help:      "Total RPC calls by chain, method, and status",
	}, []string{"chain", "method", "status"})

	// Address index LRU hit ratio
	AddressIndexLRUHitRatio = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "indexer",
		Subsystem: "address_index",
		Name:      "lru_hit_ratio",
		Help:      "LRU cache hit ratio for address index lookups",
	}, []string{"chain", "network"})

	AddressIndexLRUHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "address_index",
		Name:      "lru_hits_total",
		Help:      "Total LRU cache hits for address index lookups",
	}, []string{"chain", "network"})

	AddressIndexLRUMisses = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "address_index",
		Name:      "lru_misses_total",
		Help:      "Total LRU cache misses for address index lookups",
	}, []string{"chain", "network"})

	// Reconciliation
	ReconciliationRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "reconciliation",
		Name:      "runs_total",
		Help:      "Total reconciliation runs executed",
	}, []string{"chain", "network"})

	ReconciliationMismatchesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "reconciliation",
		Name:      "mismatches_total",
		Help:      "Total balance mismatches detected during reconciliation",
	}, []string{"chain", "network"})

	// Config watcher
	ConfigWatcherErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "config_watcher",
		Name:      "errors_total",
		Help:      "Total config watcher poll failures",
	}, []string{"chain", "network"})

	// Reconciliation errors
	ReconciliationErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Subsystem: "reconciliation",
		Name:      "errors_total",
		Help:      "Total errors during reconciliation comparison",
	}, []string{"chain", "network"})

	// Circuit breaker
	CircuitBreakerStateChanges = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "indexer",
		Name:      "circuit_breaker_state_changes_total",
		Help:      "Number of circuit breaker state transitions",
	}, []string{"component", "chain", "network", "from_state", "to_state"})
)
