package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	dbPoolStatsIntervalDefaultMS = 5000
	dbPoolStatsIntervalMinMS     = 100
	dbPoolStatsIntervalMaxMS     = 3_600_000
)

type Config struct {
	DB       DBConfig       `yaml:"db"`
	Redis    RedisConfig    `yaml:"redis"`
	Sidecar  SidecarConfig  `yaml:"sidecar"`
	Solana   SolanaConfig   `yaml:"solana"`
	Base     BaseConfig     `yaml:"base"`
	Ethereum EthereumConfig `yaml:"ethereum"`
	BTC      BTCConfig      `yaml:"btc"`
	Polygon  PolygonConfig  `yaml:"polygon"`
	Arbitrum ArbitrumConfig `yaml:"arbitrum"`
	BSC      BSCConfig      `yaml:"bsc"`
	Runtime  RuntimeConfig  `yaml:"runtime"`
	Pipeline PipelineConfig `yaml:"pipeline"`
	Server         ServerConfig         `yaml:"server"`
	Log            LogConfig            `yaml:"log"`
	Tracing        TracingConfig        `yaml:"tracing"`
	Alert          AlertConfig          `yaml:"alert"`
	Reconciliation ReconciliationConfig `yaml:"reconciliation"`
	ReorgDetector  ReorgDetectorConfig  `yaml:"reorg_detector"`
}

type AlertConfig struct {
	SlackWebhookURL string `yaml:"slack_webhook_url"`
	WebhookURL      string `yaml:"webhook_url"`
	CooldownMS      int    `yaml:"cooldown_ms"`
}

type DBConfig struct {
	URL                string `yaml:"url"`
	MaxOpenConns       int    `yaml:"max_open_conns"`
	MaxIdleConns       int    `yaml:"max_idle_conns"`
	ConnMaxLifetimeMin int    `yaml:"conn_max_lifetime_min"`
	// TimeoutSec is used internally for YAML; SidecarConfig has similar pattern.
	ConnMaxLifetime     time.Duration `yaml:"-"`
	PoolStatsIntervalMS int           `yaml:"pool_stats_interval_ms"`
}

type RedisConfig struct {
	URL string `yaml:"url"`
}

type SidecarConfig struct {
	Addr       string        `yaml:"addr"`
	TimeoutSec int           `yaml:"timeout_sec"`
	Timeout    time.Duration `yaml:"-"`
	TLSEnabled bool          `yaml:"tls_enabled"`
	TLSCert    string        `yaml:"tls_cert"`
	TLSKey     string        `yaml:"tls_key"`
	TLSCA      string        `yaml:"tls_ca"`
}

type RPCRateLimitConfig struct {
	RPS   float64 `yaml:"rps"`
	Burst int     `yaml:"burst"`
}

type SolanaConfig struct {
	RPCURL               string             `yaml:"rpc_url"`
	Network              string             `yaml:"network"`
	RateLimit            RPCRateLimitConfig `yaml:"rate_limit"`
	MaxPageSize          int                `yaml:"max_page_size"`
	MaxConcurrentTxs     int                `yaml:"max_concurrent_txs"`
	BlockScanBatchSize   int                `yaml:"block_scan_batch_size"`
	BlockScanConcurrency int                `yaml:"block_scan_concurrency"`
}

type BaseConfig struct {
	RPCURL                   string             `yaml:"rpc_url"`
	Network                  string             `yaml:"network"`
	RateLimit                RPCRateLimitConfig `yaml:"rate_limit"`
	MaxInitialLookbackBlocks int                `yaml:"max_initial_lookback_blocks"`
	MaxConcurrentTxs         int                `yaml:"max_concurrent_txs"`
}

type EthereumConfig struct {
	RPCURL                   string             `yaml:"rpc_url"`
	Network                  string             `yaml:"network"`
	RateLimit                RPCRateLimitConfig `yaml:"rate_limit"`
	MaxInitialLookbackBlocks int                `yaml:"max_initial_lookback_blocks"`
	MaxConcurrentTxs         int                `yaml:"max_concurrent_txs"`
}

type BTCConfig struct {
	RPCURL                   string             `yaml:"rpc_url"`
	Network                  string             `yaml:"network"`
	RateLimit                RPCRateLimitConfig `yaml:"rate_limit"`
	MaxInitialLookbackBlocks int                `yaml:"max_initial_lookback_blocks"`
}

type PolygonConfig struct {
	RPCURL                   string             `yaml:"rpc_url"`
	Network                  string             `yaml:"network"`
	RateLimit                RPCRateLimitConfig `yaml:"rate_limit"`
	MaxInitialLookbackBlocks int                `yaml:"max_initial_lookback_blocks"`
	MaxConcurrentTxs         int                `yaml:"max_concurrent_txs"`
}

type ArbitrumConfig struct {
	RPCURL                   string             `yaml:"rpc_url"`
	Network                  string             `yaml:"network"`
	RateLimit                RPCRateLimitConfig `yaml:"rate_limit"`
	MaxInitialLookbackBlocks int                `yaml:"max_initial_lookback_blocks"`
	MaxConcurrentTxs         int                `yaml:"max_concurrent_txs"`
}

type BSCConfig struct {
	RPCURL                   string             `yaml:"rpc_url"`
	Network                  string             `yaml:"network"`
	RateLimit                RPCRateLimitConfig `yaml:"rate_limit"`
	MaxInitialLookbackBlocks int                `yaml:"max_initial_lookback_blocks"`
	MaxConcurrentTxs         int                `yaml:"max_concurrent_txs"`
}

const (
	RuntimeDeploymentModeLikeGroup   = "like-group"
	RuntimeDeploymentModeIndependent = "independent"
)

const (
	RuntimeLikeGroupSolana = "solana-like"
	RuntimeLikeGroupEVM    = "evm-like"
	RuntimeLikeGroupBTC    = "btc-like"
)

type RuntimeConfig struct {
	DeploymentMode string   `yaml:"deployment_mode"`
	LikeGroup      string   `yaml:"like_group"`
	ChainTargets   []string `yaml:"chain_targets"`
}

// FetcherStageConfig holds tuning parameters for the fetcher pipeline stage.
type FetcherStageConfig struct {
	RetryMaxAttempts         int `yaml:"retry_max_attempts"`
	BackoffInitialMs         int `yaml:"backoff_initial_ms"`
	BackoffMaxMs             int `yaml:"backoff_max_ms"`
	AdaptiveMinBatch         int `yaml:"adaptive_min_batch"`
	BoundaryOverlapLookahead int `yaml:"boundary_overlap_lookahead"`
}

// NormalizerStageConfig holds tuning parameters for the normalizer pipeline stage.
type NormalizerStageConfig struct {
	RetryMaxAttempts    int `yaml:"retry_max_attempts"`
	RetryDelayInitialMs int `yaml:"retry_delay_initial_ms"`
	RetryDelayMaxMs     int `yaml:"retry_delay_max_ms"`
}

// IngesterStageConfig holds tuning parameters for the ingester pipeline stage.
type IngesterStageConfig struct {
	RetryMaxAttempts    int `yaml:"retry_max_attempts"`
	RetryDelayInitialMs int `yaml:"retry_delay_initial_ms"`
	RetryDelayMaxMs     int `yaml:"retry_delay_max_ms"`
	DeniedCacheCapacity int `yaml:"denied_cache_capacity"`
	DeniedCacheTTLSec   int `yaml:"denied_cache_ttl_sec"`
}

// HealthStageConfig holds tuning parameters for pipeline health tracking.
type HealthStageConfig struct {
	UnhealthyThreshold int `yaml:"unhealthy_threshold"`
}

// ConfigWatcherStageConfig holds tuning parameters for the runtime config watcher.
type ConfigWatcherStageConfig struct {
	IntervalSec int `yaml:"interval_sec"`
}

// AddressIndexConfig configures the 3-tier address index (bloom + LRU + DB).
type AddressIndexConfig struct {
	BloomExpectedItems int     `yaml:"bloom_expected_items"` // default 10_000_000
	BloomFPR           float64 `yaml:"bloom_fpr"`            // default 0.001
	LRUCapacity        int     `yaml:"lru_capacity"`         // default 100_000
	LRUTTLSec          int     `yaml:"lru_ttl_sec"`          // default 600
}

type PipelineConfig struct {
	ReorgDetectorIntervalMs                       int      `yaml:"reorg_detector_interval_ms"`
	FinalizerIntervalMs                           int      `yaml:"finalizer_interval_ms"`
	BTCFinalityConfirmations                      int      `yaml:"btc_finality_confirmations"`
	SolanaWatchedAddresses                        []string `yaml:"solana_watched_addresses"`
	BaseWatchedAddresses                          []string `yaml:"base_watched_addresses"`
	EthereumWatchedAddresses                      []string `yaml:"ethereum_watched_addresses"`
	BTCWatchedAddresses                           []string `yaml:"btc_watched_addresses"`
	PolygonWatchedAddresses                       []string `yaml:"polygon_watched_addresses"`
	ArbitrumWatchedAddresses                      []string `yaml:"arbitrum_watched_addresses"`
	BSCWatchedAddresses                           []string `yaml:"bsc_watched_addresses"`
	FetchWorkers                                  int      `yaml:"fetch_workers"`
	NormalizerWorkers                             int      `yaml:"normalizer_workers"`
	BatchSize                                     int      `yaml:"batch_size"`
	IndexingIntervalMs                            int      `yaml:"indexing_interval_ms"`
	ChannelBufferSize                             int      `yaml:"channel_buffer_size"`
	CoordinatorAutoTuneEnabled                    bool     `yaml:"coordinator_autotune_enabled"`
	CoordinatorAutoTuneMinBatchSize               int      `yaml:"coordinator_autotune_min_batch_size"`
	CoordinatorAutoTuneMaxBatchSize               int      `yaml:"coordinator_autotune_max_batch_size"`
	CoordinatorAutoTuneStepUp                     int      `yaml:"coordinator_autotune_step_up"`
	CoordinatorAutoTuneStepDown                   int      `yaml:"coordinator_autotune_step_down"`
	CoordinatorAutoTuneLagHighWatermark           int64    `yaml:"coordinator_autotune_lag_high_watermark"`
	CoordinatorAutoTuneLagLowWatermark            int64    `yaml:"coordinator_autotune_lag_low_watermark"`
	CoordinatorAutoTuneQueueHighPct               int      `yaml:"coordinator_autotune_queue_high_pct"`
	CoordinatorAutoTuneQueueLowPct                int      `yaml:"coordinator_autotune_queue_low_pct"`
	CoordinatorAutoTuneHysteresisTicks            int      `yaml:"coordinator_autotune_hysteresis_ticks"`
	CoordinatorAutoTuneTelemetryStaleTicks        int      `yaml:"coordinator_autotune_telemetry_stale_ticks"`
	CoordinatorAutoTuneTelemetryRecoveryTicks     int      `yaml:"coordinator_autotune_telemetry_recovery_ticks"`
	CoordinatorAutoTuneOperatorOverrideBatch      int      `yaml:"coordinator_autotune_operator_override_batch"`
	CoordinatorAutoTuneOperatorReleaseTicks       int      `yaml:"coordinator_autotune_operator_release_ticks"`
	CoordinatorAutoTunePolicyVersion              string   `yaml:"coordinator_autotune_policy_version"`
	CoordinatorAutoTunePolicyManifestDigest       string   `yaml:"coordinator_autotune_policy_manifest_digest"`
	CoordinatorAutoTunePolicyManifestRefreshEpoch int64    `yaml:"coordinator_autotune_policy_manifest_refresh_epoch"`
	CoordinatorAutoTunePolicyActivationHoldTicks  int      `yaml:"coordinator_autotune_policy_activation_hold_ticks"`
	IndexedBlocksRetention                        int      `yaml:"indexed_blocks_retention"`
	StreamTransportEnabled                        bool     `yaml:"stream_transport_enabled"`
	StreamNamespace                               string   `yaml:"stream_namespace"`
	StreamSessionID                               string   `yaml:"stream_session_id"`
	AddressIndex                                  AddressIndexConfig       `yaml:"address_index"`
	Fetcher                                       FetcherStageConfig       `yaml:"fetcher"`
	Normalizer                                    NormalizerStageConfig    `yaml:"normalizer"`
	Ingester                                      IngesterStageConfig      `yaml:"ingester"`
	Health                                        HealthStageConfig        `yaml:"health"`
	ConfigWatcher                                 ConfigWatcherStageConfig `yaml:"config_watcher"`
}

type ServerConfig struct {
	HealthPort            int    `yaml:"health_port"`
	MetricsAuthUser       string `yaml:"metrics_auth_user"`
	MetricsAuthPass       string `yaml:"metrics_auth_pass"`
	AdminAddr             string `yaml:"admin_addr"`
	AdminAuthUser         string `yaml:"admin_auth_user"`
	AdminAuthPass         string `yaml:"admin_auth_pass"`
	AdminRateLimitEnabled bool   `yaml:"admin_rate_limit_enabled"`
	AdminRequireAuth      bool   `yaml:"admin_require_auth"`
}

// ReconciliationConfig holds settings for periodic balance reconciliation.
type ReconciliationConfig struct {
	IntervalMS int `yaml:"interval_ms"`
}

// ReorgDetectorConfig holds extra settings for the reorg detector.
type ReorgDetectorConfig struct {
	MaxCheckDepth int `yaml:"max_check_depth"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

type TracingConfig struct {
	Enabled     bool    `yaml:"enabled"`
	Endpoint    string  `yaml:"endpoint"`
	Insecure    bool    `yaml:"insecure"`
	SampleRatio float64 `yaml:"sample_ratio"`
}

// Load loads the configuration using a 3-stage approach:
//  1. Load from YAML file (if present)
//  2. Override with environment variables (only explicitly set ones)
//  3. Apply defaults for any remaining zero-value fields
func Load() (*Config, error) {
	cfg := &Config{}

	// Step 1: YAML load (file not found is not an error — backward compat)
	if err := loadYAMLFile(cfg); err != nil {
		return nil, err
	}

	// Step 2: env var overrides (only explicitly set vars)
	applyEnvOverrides(cfg)

	// Step 3: apply defaults for zero-value fields
	applyDefaults(cfg)

	// Step 4: compute derived fields (Duration from int, etc.)
	computeDerived(cfg)

	// Step 5: bounded validation for pool stats interval
	if v, ok := os.LookupEnv("DB_POOL_STATS_INTERVAL_MS"); ok && strings.TrimSpace(v) != "" {
		trimmed := strings.TrimSpace(v)
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("DB_POOL_STATS_INTERVAL_MS: must be an integer")
		}
		if parsed < dbPoolStatsIntervalMinMS || parsed > dbPoolStatsIntervalMaxMS {
			return nil, fmt.Errorf("DB_POOL_STATS_INTERVAL_MS: must be within [%d, %d]", dbPoolStatsIntervalMinMS, dbPoolStatsIntervalMaxMS)
		}
	} else if cfg.DB.PoolStatsIntervalMS < dbPoolStatsIntervalMinMS || cfg.DB.PoolStatsIntervalMS > dbPoolStatsIntervalMaxMS {
		// Reset to default if YAML value is out of range
		cfg.DB.PoolStatsIntervalMS = dbPoolStatsIntervalDefaultMS
	}

	// Step 6: validate
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadYAMLFile loads config from a YAML file specified by CONFIG_FILE env var.
// Default path is "config.yaml". If the file doesn't exist, this is a no-op.
func loadYAMLFile(cfg *Config) error {
	path := os.Getenv("CONFIG_FILE")
	if path == "" {
		path = "config.yaml"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File not found is fine — env-only mode
		}
		return fmt.Errorf("read config file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse config file %s: %w", path, err)
	}

	return nil
}

// applyEnvOverrides overrides config values with environment variables.
// Only explicitly set env vars (via LookupEnv) override YAML values.
func applyEnvOverrides(cfg *Config) {
	// DB
	overrideStr(&cfg.DB.URL, "DB_URL")
	overrideInt(&cfg.DB.MaxOpenConns, "DB_MAX_OPEN_CONNS")
	overrideInt(&cfg.DB.MaxIdleConns, "DB_MAX_IDLE_CONNS")
	overrideInt(&cfg.DB.ConnMaxLifetimeMin, "DB_CONN_MAX_LIFETIME_MIN")
	overrideIntBounded(&cfg.DB.PoolStatsIntervalMS, "DB_POOL_STATS_INTERVAL_MS")

	// Redis
	overrideStr(&cfg.Redis.URL, "REDIS_URL")

	// Sidecar
	overrideStr(&cfg.Sidecar.Addr, "SIDECAR_ADDR")
	overrideInt(&cfg.Sidecar.TimeoutSec, "SIDECAR_TIMEOUT_SEC")
	overrideBool(&cfg.Sidecar.TLSEnabled, "SIDECAR_TLS_ENABLED")
	overrideStr(&cfg.Sidecar.TLSCert, "SIDECAR_TLS_CERT")
	overrideStr(&cfg.Sidecar.TLSKey, "SIDECAR_TLS_KEY")
	overrideStr(&cfg.Sidecar.TLSCA, "SIDECAR_TLS_CA")

	// Solana
	overrideStrAny(&cfg.Solana.RPCURL, "SOLANA_DEVNET_RPC_URL", "SOLANA_RPC_URL")
	overrideStr(&cfg.Solana.Network, "SOLANA_NETWORK")
	overrideFloat64(&cfg.Solana.RateLimit.RPS, "SOLANA_RPC_RATE_LIMIT")
	overrideInt(&cfg.Solana.RateLimit.Burst, "SOLANA_RPC_BURST")
	overrideInt(&cfg.Solana.MaxPageSize, "SOLANA_MAX_PAGE_SIZE")
	overrideInt(&cfg.Solana.MaxConcurrentTxs, "SOLANA_MAX_CONCURRENT_TXS")
	overrideInt(&cfg.Solana.BlockScanBatchSize, "SOLANA_BLOCK_SCAN_BATCH_SIZE")
	overrideInt(&cfg.Solana.BlockScanConcurrency, "SOLANA_BLOCK_SCAN_CONCURRENCY")

	// Base
	overrideStrAny(&cfg.Base.RPCURL, "BASE_SEPOLIA_RPC_URL", "BASE_RPC_URL")
	overrideStr(&cfg.Base.Network, "BASE_NETWORK")
	overrideFloat64(&cfg.Base.RateLimit.RPS, "BASE_RPC_RATE_LIMIT")
	overrideInt(&cfg.Base.RateLimit.Burst, "BASE_RPC_BURST")
	overrideInt(&cfg.Base.MaxInitialLookbackBlocks, "BASE_MAX_INITIAL_LOOKBACK_BLOCKS")
	overrideInt(&cfg.Base.MaxConcurrentTxs, "BASE_MAX_CONCURRENT_TXS")

	// Ethereum
	overrideStrAny(&cfg.Ethereum.RPCURL, "ETH_MAINNET_RPC_URL", "ETHEREUM_RPC_URL")
	overrideStr(&cfg.Ethereum.Network, "ETHEREUM_NETWORK")
	overrideFloat64(&cfg.Ethereum.RateLimit.RPS, "ETH_RPC_RATE_LIMIT")
	overrideInt(&cfg.Ethereum.RateLimit.Burst, "ETH_RPC_BURST")
	overrideInt(&cfg.Ethereum.MaxInitialLookbackBlocks, "ETH_MAX_INITIAL_LOOKBACK_BLOCKS")
	overrideInt(&cfg.Ethereum.MaxConcurrentTxs, "ETH_MAX_CONCURRENT_TXS")

	// BTC
	overrideStrAny(&cfg.BTC.RPCURL, "BTC_TESTNET_RPC_URL", "BTC_RPC_URL")
	overrideStr(&cfg.BTC.Network, "BTC_NETWORK")
	overrideFloat64(&cfg.BTC.RateLimit.RPS, "BTC_RPC_RATE_LIMIT")
	overrideInt(&cfg.BTC.RateLimit.Burst, "BTC_RPC_BURST")
	overrideInt(&cfg.BTC.MaxInitialLookbackBlocks, "BTC_MAX_INITIAL_LOOKBACK_BLOCKS")

	// Polygon
	overrideStrAny(&cfg.Polygon.RPCURL, "POLYGON_RPC_URL", "POLYGON_MAINNET_RPC_URL")
	overrideStr(&cfg.Polygon.Network, "POLYGON_NETWORK")
	overrideFloat64(&cfg.Polygon.RateLimit.RPS, "POLYGON_RPC_RATE_LIMIT")
	overrideInt(&cfg.Polygon.RateLimit.Burst, "POLYGON_RPC_BURST")
	overrideInt(&cfg.Polygon.MaxInitialLookbackBlocks, "POLYGON_MAX_INITIAL_LOOKBACK_BLOCKS")
	overrideInt(&cfg.Polygon.MaxConcurrentTxs, "POLYGON_MAX_CONCURRENT_TXS")

	// Arbitrum
	overrideStrAny(&cfg.Arbitrum.RPCURL, "ARBITRUM_RPC_URL", "ARBITRUM_MAINNET_RPC_URL")
	overrideStr(&cfg.Arbitrum.Network, "ARBITRUM_NETWORK")
	overrideFloat64(&cfg.Arbitrum.RateLimit.RPS, "ARBITRUM_RPC_RATE_LIMIT")
	overrideInt(&cfg.Arbitrum.RateLimit.Burst, "ARBITRUM_RPC_BURST")
	overrideInt(&cfg.Arbitrum.MaxInitialLookbackBlocks, "ARBITRUM_MAX_INITIAL_LOOKBACK_BLOCKS")
	overrideInt(&cfg.Arbitrum.MaxConcurrentTxs, "ARBITRUM_MAX_CONCURRENT_TXS")

	// BSC
	overrideStrAny(&cfg.BSC.RPCURL, "BSC_RPC_URL", "BSC_MAINNET_RPC_URL")
	overrideStr(&cfg.BSC.Network, "BSC_NETWORK")
	overrideFloat64(&cfg.BSC.RateLimit.RPS, "BSC_RPC_RATE_LIMIT")
	overrideInt(&cfg.BSC.RateLimit.Burst, "BSC_RPC_BURST")
	overrideInt(&cfg.BSC.MaxInitialLookbackBlocks, "BSC_MAX_INITIAL_LOOKBACK_BLOCKS")
	overrideInt(&cfg.BSC.MaxConcurrentTxs, "BSC_MAX_CONCURRENT_TXS")

	// Runtime
	overrideStrLower(&cfg.Runtime.DeploymentMode, "RUNTIME_DEPLOYMENT_MODE")
	overrideStrLower(&cfg.Runtime.LikeGroup, "RUNTIME_LIKE_GROUP")
	overrideCSVList(&cfg.Runtime.ChainTargets, "RUNTIME_CHAIN_TARGETS", "RUNTIME_CHAIN_TARGET")

	// Pipeline
	overrideInt(&cfg.Pipeline.ReorgDetectorIntervalMs, "REORG_DETECTOR_INTERVAL_MS")
	overrideInt(&cfg.Pipeline.FinalizerIntervalMs, "FINALIZER_INTERVAL_MS")
	overrideInt(&cfg.Pipeline.BTCFinalityConfirmations, "BTC_FINALITY_CONFIRMATIONS")
	overrideInt(&cfg.Pipeline.FetchWorkers, "FETCH_WORKERS")
	overrideInt(&cfg.Pipeline.NormalizerWorkers, "NORMALIZER_WORKERS")
	overrideInt(&cfg.Pipeline.BatchSize, "BATCH_SIZE")
	overrideInt(&cfg.Pipeline.IndexingIntervalMs, "INDEXING_INTERVAL_MS")
	overrideInt(&cfg.Pipeline.ChannelBufferSize, "CHANNEL_BUFFER_SIZE")
	overrideBool(&cfg.Pipeline.CoordinatorAutoTuneEnabled, "COORDINATOR_AUTOTUNE_ENABLED")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneMinBatchSize, "COORDINATOR_AUTOTUNE_MIN_BATCH_SIZE")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneMaxBatchSize, "COORDINATOR_AUTOTUNE_MAX_BATCH_SIZE")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneStepUp, "COORDINATOR_AUTOTUNE_STEP_UP")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneStepDown, "COORDINATOR_AUTOTUNE_STEP_DOWN")
	overrideInt64(&cfg.Pipeline.CoordinatorAutoTuneLagHighWatermark, "COORDINATOR_AUTOTUNE_LAG_HIGH_WATERMARK")
	overrideInt64(&cfg.Pipeline.CoordinatorAutoTuneLagLowWatermark, "COORDINATOR_AUTOTUNE_LAG_LOW_WATERMARK")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneQueueHighPct, "COORDINATOR_AUTOTUNE_QUEUE_HIGH_PCT")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneQueueLowPct, "COORDINATOR_AUTOTUNE_QUEUE_LOW_PCT")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneHysteresisTicks, "COORDINATOR_AUTOTUNE_HYSTERESIS_TICKS")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneTelemetryStaleTicks, "COORDINATOR_AUTOTUNE_TELEMETRY_STALE_TICKS")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneTelemetryRecoveryTicks, "COORDINATOR_AUTOTUNE_TELEMETRY_RECOVERY_TICKS")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneOperatorOverrideBatch, "COORDINATOR_AUTOTUNE_OPERATOR_OVERRIDE_BATCH_SIZE")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTuneOperatorReleaseTicks, "COORDINATOR_AUTOTUNE_OPERATOR_RELEASE_HOLD_TICKS")
	overrideStr(&cfg.Pipeline.CoordinatorAutoTunePolicyVersion, "COORDINATOR_AUTOTUNE_POLICY_VERSION")
	overrideStr(&cfg.Pipeline.CoordinatorAutoTunePolicyManifestDigest, "COORDINATOR_AUTOTUNE_POLICY_MANIFEST_DIGEST")
	overrideInt64(&cfg.Pipeline.CoordinatorAutoTunePolicyManifestRefreshEpoch, "COORDINATOR_AUTOTUNE_POLICY_MANIFEST_REFRESH_EPOCH")
	overrideInt(&cfg.Pipeline.CoordinatorAutoTunePolicyActivationHoldTicks, "COORDINATOR_AUTOTUNE_POLICY_ACTIVATION_HOLD_TICKS")
	overrideInt(&cfg.Pipeline.IndexedBlocksRetention, "INDEXED_BLOCKS_RETENTION")
	overrideBool(&cfg.Pipeline.StreamTransportEnabled, "PIPELINE_STREAM_TRANSPORT_ENABLED")
	overrideStr(&cfg.Pipeline.StreamNamespace, "PIPELINE_STREAM_NAMESPACE")
	overrideStr(&cfg.Pipeline.StreamSessionID, "PIPELINE_STREAM_SESSION_ID")

	// Pipeline stage configs
	overrideInt(&cfg.Pipeline.Fetcher.RetryMaxAttempts, "FETCHER_RETRY_MAX_ATTEMPTS")
	overrideInt(&cfg.Pipeline.Fetcher.BackoffInitialMs, "FETCHER_BACKOFF_INITIAL_MS")
	overrideInt(&cfg.Pipeline.Fetcher.BackoffMaxMs, "FETCHER_BACKOFF_MAX_MS")
	overrideInt(&cfg.Pipeline.Fetcher.AdaptiveMinBatch, "FETCHER_ADAPTIVE_MIN_BATCH")
	overrideInt(&cfg.Pipeline.Fetcher.BoundaryOverlapLookahead, "FETCHER_BOUNDARY_OVERLAP_LOOKAHEAD")
	overrideInt(&cfg.Pipeline.Normalizer.RetryMaxAttempts, "NORMALIZER_RETRY_MAX_ATTEMPTS")
	overrideInt(&cfg.Pipeline.Normalizer.RetryDelayInitialMs, "NORMALIZER_RETRY_DELAY_INITIAL_MS")
	overrideInt(&cfg.Pipeline.Normalizer.RetryDelayMaxMs, "NORMALIZER_RETRY_DELAY_MAX_MS")
	overrideInt(&cfg.Pipeline.Ingester.RetryMaxAttempts, "INGESTER_RETRY_MAX_ATTEMPTS")
	overrideInt(&cfg.Pipeline.Ingester.RetryDelayInitialMs, "INGESTER_RETRY_DELAY_INITIAL_MS")
	overrideInt(&cfg.Pipeline.Ingester.RetryDelayMaxMs, "INGESTER_RETRY_DELAY_MAX_MS")
	overrideInt(&cfg.Pipeline.Ingester.DeniedCacheCapacity, "INGESTER_DENIED_CACHE_CAPACITY")
	overrideInt(&cfg.Pipeline.Ingester.DeniedCacheTTLSec, "INGESTER_DENIED_CACHE_TTL_SEC")
	overrideInt(&cfg.Pipeline.Health.UnhealthyThreshold, "HEALTH_UNHEALTHY_THRESHOLD")
	overrideInt(&cfg.Pipeline.ConfigWatcher.IntervalSec, "CONFIG_WATCHER_INTERVAL_SEC")

	// Address index
	overrideInt(&cfg.Pipeline.AddressIndex.BloomExpectedItems, "ADDRESS_INDEX_BLOOM_EXPECTED_ITEMS")
	overrideFloat64(&cfg.Pipeline.AddressIndex.BloomFPR, "ADDRESS_INDEX_BLOOM_FPR")
	overrideInt(&cfg.Pipeline.AddressIndex.LRUCapacity, "ADDRESS_INDEX_LRU_CAPACITY")
	overrideInt(&cfg.Pipeline.AddressIndex.LRUTTLSec, "ADDRESS_INDEX_LRU_TTL_SEC")

	// Watched addresses
	overrideCSVList(&cfg.Pipeline.SolanaWatchedAddresses, "SOLANA_WATCHED_ADDRESSES", "WATCHED_ADDRESSES")
	overrideCSVList(&cfg.Pipeline.BaseWatchedAddresses, "BASE_WATCHED_ADDRESSES")
	overrideCSVList(&cfg.Pipeline.EthereumWatchedAddresses, "ETH_WATCHED_ADDRESSES")
	overrideCSVList(&cfg.Pipeline.BTCWatchedAddresses, "BTC_WATCHED_ADDRESSES")
	overrideCSVList(&cfg.Pipeline.PolygonWatchedAddresses, "POLYGON_WATCHED_ADDRESSES")
	overrideCSVList(&cfg.Pipeline.ArbitrumWatchedAddresses, "ARBITRUM_WATCHED_ADDRESSES")
	overrideCSVList(&cfg.Pipeline.BSCWatchedAddresses, "BSC_WATCHED_ADDRESSES")

	// Server
	overrideInt(&cfg.Server.HealthPort, "HEALTH_PORT")
	overrideStr(&cfg.Server.MetricsAuthUser, "METRICS_AUTH_USER")
	overrideStr(&cfg.Server.MetricsAuthPass, "METRICS_AUTH_PASS")
	overrideStr(&cfg.Server.AdminAddr, "ADMIN_ADDR")
	overrideStr(&cfg.Server.AdminAuthUser, "ADMIN_AUTH_USER")
	overrideStr(&cfg.Server.AdminAuthPass, "ADMIN_AUTH_PASS")

	// Log
	overrideStr(&cfg.Log.Level, "LOG_LEVEL")

	// Tracing
	overrideBool(&cfg.Tracing.Enabled, "OTEL_TRACING_ENABLED")
	overrideStr(&cfg.Tracing.Endpoint, "OTEL_EXPORTER_OTLP_ENDPOINT")
	overrideBool(&cfg.Tracing.Insecure, "OTEL_EXPORTER_OTLP_INSECURE")
	overrideFloat64(&cfg.Tracing.SampleRatio, "OTEL_TRACE_SAMPLE_RATIO")

	// Alert
	overrideStr(&cfg.Alert.SlackWebhookURL, "SLACK_WEBHOOK_URL")
	overrideStr(&cfg.Alert.WebhookURL, "ALERT_WEBHOOK_URL")
	overrideInt(&cfg.Alert.CooldownMS, "ALERT_COOLDOWN_MS")

	// Reconciliation
	overrideInt(&cfg.Reconciliation.IntervalMS, "RECONCILIATION_INTERVAL_MS")

	// Reorg detector
	overrideInt(&cfg.ReorgDetector.MaxCheckDepth, "REORG_DETECTOR_MAX_CHECK_DEPTH")

	// Admin security
	overrideBool(&cfg.Server.AdminRateLimitEnabled, "ADMIN_RATE_LIMIT_ENABLED")
	overrideBool(&cfg.Server.AdminRequireAuth, "ADMIN_REQUIRE_AUTH")

	// Normalize chain targets
	if len(cfg.Runtime.ChainTargets) > 0 {
		cfg.Runtime.ChainTargets = normalizeCSVValues(cfg.Runtime.ChainTargets)
	}
}

// applyDefaults sets default values for zero-value fields.
func applyDefaults(cfg *Config) {
	// Redis
	setDefaultStr(&cfg.Redis.URL, "redis://localhost:6380")

	// Sidecar
	setDefaultStr(&cfg.Sidecar.Addr, "localhost:50051")
	setDefault(&cfg.Sidecar.TimeoutSec, 30)

	// DB
	setDefault(&cfg.DB.MaxOpenConns, 50)
	setDefault(&cfg.DB.MaxIdleConns, 10)
	setDefault(&cfg.DB.ConnMaxLifetimeMin, 30)
	setDefault(&cfg.DB.PoolStatsIntervalMS, dbPoolStatsIntervalDefaultMS)

	// Solana
	setDefaultStr(&cfg.Solana.RPCURL, "https://api.devnet.solana.com")
	setDefaultStr(&cfg.Solana.Network, "devnet")
	setDefaultFloat(&cfg.Solana.RateLimit.RPS, 10)
	setDefault(&cfg.Solana.RateLimit.Burst, 20)
	setDefault(&cfg.Solana.MaxPageSize, 1000)
	setDefault(&cfg.Solana.MaxConcurrentTxs, 10)
	setDefault(&cfg.Solana.BlockScanBatchSize, 10)
	setDefault(&cfg.Solana.BlockScanConcurrency, 10)

	// Base
	setDefaultStr(&cfg.Base.Network, "sepolia")
	setDefaultFloat(&cfg.Base.RateLimit.RPS, 25)
	setDefault(&cfg.Base.RateLimit.Burst, 50)
	setDefault(&cfg.Base.MaxInitialLookbackBlocks, 200)
	setDefault(&cfg.Base.MaxConcurrentTxs, 10)

	// Ethereum
	setDefaultStr(&cfg.Ethereum.Network, "mainnet")
	setDefaultFloat(&cfg.Ethereum.RateLimit.RPS, 25)
	setDefault(&cfg.Ethereum.RateLimit.Burst, 50)
	setDefault(&cfg.Ethereum.MaxInitialLookbackBlocks, 200)
	setDefault(&cfg.Ethereum.MaxConcurrentTxs, 10)

	// BTC
	setDefaultStr(&cfg.BTC.Network, "testnet")
	setDefaultFloat(&cfg.BTC.RateLimit.RPS, 5)
	setDefault(&cfg.BTC.RateLimit.Burst, 10)
	setDefault(&cfg.BTC.MaxInitialLookbackBlocks, 200)

	// Polygon
	setDefaultStr(&cfg.Polygon.Network, "mainnet")
	setDefaultFloat(&cfg.Polygon.RateLimit.RPS, 25)
	setDefault(&cfg.Polygon.RateLimit.Burst, 50)
	setDefault(&cfg.Polygon.MaxInitialLookbackBlocks, 200)
	setDefault(&cfg.Polygon.MaxConcurrentTxs, 10)

	// Arbitrum
	setDefaultStr(&cfg.Arbitrum.Network, "mainnet")
	setDefaultFloat(&cfg.Arbitrum.RateLimit.RPS, 25)
	setDefault(&cfg.Arbitrum.RateLimit.Burst, 50)
	setDefault(&cfg.Arbitrum.MaxInitialLookbackBlocks, 200)
	setDefault(&cfg.Arbitrum.MaxConcurrentTxs, 10)

	// BSC
	setDefaultStr(&cfg.BSC.Network, "mainnet")
	setDefaultFloat(&cfg.BSC.RateLimit.RPS, 25)
	setDefault(&cfg.BSC.RateLimit.Burst, 50)
	setDefault(&cfg.BSC.MaxInitialLookbackBlocks, 200)
	setDefault(&cfg.BSC.MaxConcurrentTxs, 10)

	// Runtime
	setDefaultStr(&cfg.Runtime.DeploymentMode, RuntimeDeploymentModeLikeGroup)

	// Pipeline
	setDefault(&cfg.Pipeline.ReorgDetectorIntervalMs, 30000)
	setDefault(&cfg.Pipeline.FinalizerIntervalMs, 60000)
	setDefault(&cfg.Pipeline.BTCFinalityConfirmations, 6)
	setDefault(&cfg.Pipeline.FetchWorkers, 2)
	setDefault(&cfg.Pipeline.NormalizerWorkers, 2)
	setDefault(&cfg.Pipeline.BatchSize, 100)
	setDefault(&cfg.Pipeline.IndexingIntervalMs, 5000)
	setDefault(&cfg.Pipeline.ChannelBufferSize, 10)
	setDefault(&cfg.Pipeline.CoordinatorAutoTuneMinBatchSize, 10)
	setDefault(&cfg.Pipeline.CoordinatorAutoTuneStepUp, 10)
	setDefault(&cfg.Pipeline.CoordinatorAutoTuneStepDown, 10)
	setDefaultInt64(&cfg.Pipeline.CoordinatorAutoTuneLagHighWatermark, 500)
	setDefaultInt64(&cfg.Pipeline.CoordinatorAutoTuneLagLowWatermark, 100)
	setDefault(&cfg.Pipeline.CoordinatorAutoTuneQueueHighPct, 80)
	setDefault(&cfg.Pipeline.CoordinatorAutoTuneQueueLowPct, 30)
	setDefault(&cfg.Pipeline.CoordinatorAutoTuneHysteresisTicks, 2)
	setDefault(&cfg.Pipeline.CoordinatorAutoTuneTelemetryStaleTicks, 2)
	setDefault(&cfg.Pipeline.CoordinatorAutoTuneTelemetryRecoveryTicks, 1)
	setDefault(&cfg.Pipeline.CoordinatorAutoTuneOperatorReleaseTicks, 2)
	setDefaultStr(&cfg.Pipeline.CoordinatorAutoTunePolicyVersion, "policy-v1")
	setDefaultStr(&cfg.Pipeline.CoordinatorAutoTunePolicyManifestDigest, "manifest-v1")
	setDefault(&cfg.Pipeline.CoordinatorAutoTunePolicyActivationHoldTicks, 1)
	setDefault(&cfg.Pipeline.IndexedBlocksRetention, 10000)
	setDefaultStr(&cfg.Pipeline.StreamNamespace, "pipeline")

	// AutoTune max batch defaults to batch_size if not set
	if cfg.Pipeline.CoordinatorAutoTuneMaxBatchSize == 0 {
		cfg.Pipeline.CoordinatorAutoTuneMaxBatchSize = cfg.Pipeline.BatchSize
	}

	// Pipeline stage defaults
	setDefault(&cfg.Pipeline.Fetcher.RetryMaxAttempts, 4)
	setDefault(&cfg.Pipeline.Fetcher.BackoffInitialMs, 200)
	setDefault(&cfg.Pipeline.Fetcher.BackoffMaxMs, 3000)
	setDefault(&cfg.Pipeline.Fetcher.AdaptiveMinBatch, 1)
	setDefault(&cfg.Pipeline.Fetcher.BoundaryOverlapLookahead, 1)
	setDefault(&cfg.Pipeline.Normalizer.RetryMaxAttempts, 3)
	setDefault(&cfg.Pipeline.Normalizer.RetryDelayInitialMs, 100)
	setDefault(&cfg.Pipeline.Normalizer.RetryDelayMaxMs, 1000)
	setDefault(&cfg.Pipeline.Ingester.RetryMaxAttempts, 3)
	setDefault(&cfg.Pipeline.Ingester.RetryDelayInitialMs, 100)
	setDefault(&cfg.Pipeline.Ingester.RetryDelayMaxMs, 1000)
	setDefault(&cfg.Pipeline.Ingester.DeniedCacheCapacity, 10000)
	setDefault(&cfg.Pipeline.Ingester.DeniedCacheTTLSec, 300)
	setDefault(&cfg.Pipeline.Health.UnhealthyThreshold, 5)
	setDefault(&cfg.Pipeline.ConfigWatcher.IntervalSec, 30)

	// Address index
	setDefault(&cfg.Pipeline.AddressIndex.BloomExpectedItems, 10_000_000)
	setDefaultFloat(&cfg.Pipeline.AddressIndex.BloomFPR, 0.001)
	setDefault(&cfg.Pipeline.AddressIndex.LRUCapacity, 100_000)
	setDefault(&cfg.Pipeline.AddressIndex.LRUTTLSec, 600)

	// Server
	setDefault(&cfg.Server.HealthPort, 8080)

	// Log
	setDefaultStr(&cfg.Log.Level, "info")

	// Tracing
	setDefaultFloat(&cfg.Tracing.SampleRatio, 0.1)

	// Alert
	setDefault(&cfg.Alert.CooldownMS, 1800000)

	// Reconciliation
	setDefault(&cfg.Reconciliation.IntervalMS, 3600000) // 1 hour

	// Reorg detector
	setDefault(&cfg.ReorgDetector.MaxCheckDepth, 256)

	// Admin security defaults
	if !cfg.Server.AdminRateLimitEnabled && cfg.Server.AdminAddr != "" {
		cfg.Server.AdminRateLimitEnabled = true
	}
	if !cfg.Server.AdminRequireAuth && cfg.Server.AdminAddr != "" {
		cfg.Server.AdminRequireAuth = true
	}
}

// computeDerived calculates derived Duration fields from integer config values.
func computeDerived(cfg *Config) {
	cfg.DB.ConnMaxLifetime = time.Duration(cfg.DB.ConnMaxLifetimeMin) * time.Minute
	cfg.Sidecar.Timeout = time.Duration(cfg.Sidecar.TimeoutSec) * time.Second
}

// --- override helpers (LookupEnv-based: only override if env var is explicitly set) ---

func overrideStr(target *string, envKeys ...string) {
	for _, key := range envKeys {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			*target = v
			return
		}
	}
}

func overrideStrAny(target *string, envKeys ...string) {
	for _, key := range envKeys {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			*target = v
			return
		}
	}
}

func overrideStrLower(target *string, envKeys ...string) {
	for _, key := range envKeys {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			*target = strings.ToLower(v)
			return
		}
	}
}

func overrideInt(target *int, envKeys ...string) {
	for _, key := range envKeys {
		if v, ok := os.LookupEnv(key); ok {
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				continue
			}
			i, err := strconv.Atoi(trimmed)
			if err != nil {
				slog.Warn("invalid integer env var, ignoring", "key", key, "value", v)
				continue
			}
			*target = i
			return
		}
	}
}

func overrideIntBounded(target *int, envKeys ...string) {
	for _, key := range envKeys {
		if v, ok := os.LookupEnv(key); ok {
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				continue
			}
			i, err := strconv.Atoi(trimmed)
			if err != nil {
				slog.Warn("invalid integer env var, ignoring", "key", key, "value", v)
				continue
			}
			*target = i
			return
		}
	}
}

func overrideInt64(target *int64, envKeys ...string) {
	for _, key := range envKeys {
		if v, ok := os.LookupEnv(key); ok {
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				continue
			}
			i, err := strconv.ParseInt(trimmed, 10, 64)
			if err != nil {
				slog.Warn("invalid int64 env var, ignoring", "key", key, "value", v)
				continue
			}
			*target = i
			return
		}
	}
}

func overrideBool(target *bool, envKeys ...string) {
	for _, key := range envKeys {
		if v, ok := os.LookupEnv(key); ok {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "1", "true", "yes", "on":
				*target = true
				return
			case "0", "false", "no", "off":
				*target = false
				return
			}
		}
	}
}

func overrideFloat64(target *float64, envKeys ...string) {
	for _, key := range envKeys {
		if v, ok := os.LookupEnv(key); ok {
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				continue
			}
			f, err := strconv.ParseFloat(trimmed, 64)
			if err != nil {
				slog.Warn("invalid float env var, ignoring", "key", key, "value", v)
				continue
			}
			*target = f
			return
		}
	}
}

func overrideCSVList(target *[]string, envKeys ...string) {
	for _, key := range envKeys {
		if v, ok := os.LookupEnv(key); ok {
			parsed := parseAddressCSV(v)
			if parsed != nil {
				*target = parsed
				return
			}
		}
	}
}

// --- default helpers (only set if current value is zero) ---

func setDefaultStr(target *string, defaultVal string) {
	if *target == "" {
		*target = defaultVal
	}
}

func setDefault(target *int, defaultVal int) {
	if *target == 0 {
		*target = defaultVal
	}
}

func setDefaultInt64(target *int64, defaultVal int64) {
	if *target == 0 {
		*target = defaultVal
	}
}

func setDefaultFloat(target *float64, defaultVal float64) {
	if *target == 0 {
		*target = defaultVal
	}
}

// --- validation (unchanged) ---

func (c *Config) validate() error {
	if c.DB.URL == "" {
		return fmt.Errorf("DB_URL is required")
	}
	if c.Sidecar.Addr == "" {
		return fmt.Errorf("SIDECAR_ADDR is required")
	}
	if c.Sidecar.TLSEnabled && c.Sidecar.TLSCA == "" {
		return fmt.Errorf("SIDECAR_TLS_CA is required when SIDECAR_TLS_ENABLED=true")
	}
	switch c.Runtime.DeploymentMode {
	case RuntimeDeploymentModeLikeGroup, RuntimeDeploymentModeIndependent:
	default:
		return fmt.Errorf("RUNTIME_DEPLOYMENT_MODE must be one of %q, %q", RuntimeDeploymentModeLikeGroup, RuntimeDeploymentModeIndependent)
	}

	if c.Runtime.LikeGroup != "" {
		switch c.Runtime.LikeGroup {
		case RuntimeLikeGroupSolana, RuntimeLikeGroupEVM, RuntimeLikeGroupBTC:
		default:
			return fmt.Errorf("RUNTIME_LIKE_GROUP must be one of %q, %q, %q", RuntimeLikeGroupSolana, RuntimeLikeGroupEVM, RuntimeLikeGroupBTC)
		}
	}

	if c.Runtime.DeploymentMode == RuntimeDeploymentModeIndependent {
		if c.Runtime.LikeGroup != "" {
			return fmt.Errorf("RUNTIME_LIKE_GROUP is not allowed when RUNTIME_DEPLOYMENT_MODE=%q", RuntimeDeploymentModeIndependent)
		}
		if len(c.Runtime.ChainTargets) == 0 {
			return fmt.Errorf("RUNTIME_CHAIN_TARGET is required when RUNTIME_DEPLOYMENT_MODE=%q", RuntimeDeploymentModeIndependent)
		}
		if len(c.Runtime.ChainTargets) != 1 {
			return fmt.Errorf("RUNTIME_DEPLOYMENT_MODE=%q supports exactly one chain target", RuntimeDeploymentModeIndependent)
		}
	}

	if err := validateRuntimeChainTargets(c.Runtime.ChainTargets); err != nil {
		return err
	}

	requiredChains, err := requiredRuntimeChains(c.Runtime)
	if err != nil {
		return err
	}
	if requiredChains["solana"] && c.Solana.RPCURL == "" {
		return fmt.Errorf("SOLANA_RPC_URL is required for selected runtime targets")
	}
	if requiredChains["base"] && c.Base.RPCURL == "" {
		return fmt.Errorf("BASE_SEPOLIA_RPC_URL is required for selected runtime targets")
	}
	if requiredChains["ethereum"] && c.Ethereum.RPCURL == "" {
		return fmt.Errorf("ETH_MAINNET_RPC_URL is required for selected runtime targets")
	}
	if requiredChains["btc"] && c.BTC.RPCURL == "" {
		return fmt.Errorf("BTC_TESTNET_RPC_URL is required for selected runtime targets")
	}
	if requiredChains["polygon"] && c.Polygon.RPCURL == "" {
		return fmt.Errorf("POLYGON_RPC_URL is required for selected runtime targets")
	}
	if requiredChains["arbitrum"] && c.Arbitrum.RPCURL == "" {
		return fmt.Errorf("ARBITRUM_RPC_URL is required for selected runtime targets")
	}
	if requiredChains["bsc"] && c.BSC.RPCURL == "" {
		return fmt.Errorf("BSC_RPC_URL is required for selected runtime targets")
	}

	if c.Server.AdminAddr != "" && !isValidAddr(c.Server.AdminAddr) {
		return fmt.Errorf("ADMIN_ADDR %q must be in host:port or :port format", c.Server.AdminAddr)
	}

	if err := validateRateLimitConfig("SOLANA", c.Solana.RateLimit); err != nil {
		return err
	}
	if err := validateRateLimitConfig("BASE", c.Base.RateLimit); err != nil {
		return err
	}
	if err := validateRateLimitConfig("ETH", c.Ethereum.RateLimit); err != nil {
		return err
	}
	if err := validateRateLimitConfig("BTC", c.BTC.RateLimit); err != nil {
		return err
	}
	if err := validateRateLimitConfig("POLYGON", c.Polygon.RateLimit); err != nil {
		return err
	}
	if err := validateRateLimitConfig("ARBITRUM", c.Arbitrum.RateLimit); err != nil {
		return err
	}
	if err := validateRateLimitConfig("BSC", c.BSC.RateLimit); err != nil {
		return err
	}

	return nil
}

func isValidAddr(addr string) bool {
	if addr == "" {
		return false
	}
	parts := strings.Split(addr, ":")
	if len(parts) < 2 {
		return false
	}
	portStr := parts[len(parts)-1]
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	return true
}

func validateRateLimitConfig(prefix string, rl RPCRateLimitConfig) error {
	if rl.RPS < 0 {
		return fmt.Errorf("%s_RPC_RATE_LIMIT must be non-negative, got %f", prefix, rl.RPS)
	}
	if rl.Burst < 0 {
		return fmt.Errorf("%s_RPC_BURST must be non-negative, got %d", prefix, rl.Burst)
	}
	return nil
}

func requiredRuntimeChains(runtime RuntimeConfig) (map[string]bool, error) {
	required := map[string]bool{
		"solana":   false,
		"base":     false,
		"ethereum": false,
		"btc":      false,
		"polygon":  false,
		"arbitrum": false,
		"bsc":      false,
	}

	addChainTargets := func(targets []string) error {
		for _, target := range targets {
			chainName, err := chainNameFromTargetKey(target)
			if err != nil {
				return err
			}
			required[chainName] = true
		}
		return nil
	}

	switch runtime.DeploymentMode {
	case RuntimeDeploymentModeIndependent:
		if err := addChainTargets(runtime.ChainTargets); err != nil {
			return nil, err
		}
	case RuntimeDeploymentModeLikeGroup:
		if len(runtime.ChainTargets) > 0 {
			if err := addChainTargets(runtime.ChainTargets); err != nil {
				return nil, err
			}
			return required, nil
		}

		switch runtime.LikeGroup {
		case "":
			required["solana"] = true
			required["base"] = true
			required["btc"] = true
		case RuntimeLikeGroupSolana:
			required["solana"] = true
		case RuntimeLikeGroupEVM:
			required["base"] = true
		case RuntimeLikeGroupBTC:
			required["btc"] = true
		default:
			return nil, fmt.Errorf("RUNTIME_LIKE_GROUP must be one of %q, %q, %q", RuntimeLikeGroupSolana, RuntimeLikeGroupEVM, RuntimeLikeGroupBTC)
		}
	default:
		return nil, fmt.Errorf("RUNTIME_DEPLOYMENT_MODE must be one of %q, %q", RuntimeDeploymentModeLikeGroup, RuntimeDeploymentModeIndependent)
	}

	return required, nil
}

func chainNameFromTargetKey(target string) (string, error) {
	chainName, _, err := parseRuntimeTargetKey(target)
	return chainName, err
}

func parseRuntimeTargetKey(target string) (string, string, error) {
	normalized := strings.TrimSpace(strings.ToLower(target))
	if normalized == "" {
		return "", "", fmt.Errorf("runtime chain target must not be empty")
	}

	parts := strings.SplitN(normalized, "-", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("runtime chain target %q must be in <chain>-<network> format", target)
	}

	chain := strings.TrimSpace(parts[0])
	network := strings.TrimSpace(parts[1])
	supportedNetworks := map[string]map[string]bool{
		"solana":   {"devnet": true},
		"base":     {"sepolia": true},
		"ethereum": {"mainnet": true},
		"btc":      {"testnet": true},
		"polygon":  {"mainnet": true, "amoy": true},
		"arbitrum": {"mainnet": true, "sepolia": true},
		"bsc":      {"mainnet": true, "testnet": true},
	}

	networks, chainSupported := supportedNetworks[chain]
	if !chainSupported {
		return "", "", fmt.Errorf("runtime chain target %q has unsupported chain %q", target, chain)
	}
	if !networks[network] {
		return "", "", fmt.Errorf("runtime chain target %q has unsupported network %q for chain %q", target, network, chain)
	}

	return chain, network, nil
}

func validateRuntimeChainTargets(targets []string) error {
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		chain, network, err := parseRuntimeTargetKey(target)
		if err != nil {
			return fmt.Errorf("RUNTIME_CHAIN_TARGETS contains invalid value %q: %w", target, err)
		}
		key := chain + "-" + network
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
	}

	return nil
}

// --- utility functions ---

func parseAddressCSV(addrs string) []string {
	if addrs == "" {
		return nil
	}
	parsed := make([]string, 0)
	for _, addr := range strings.Split(addrs, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		parsed = append(parsed, addr)
	}
	return parsed
}

func normalizeCSVValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		candidate := strings.ToLower(strings.TrimSpace(value))
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		normalized = append(normalized, candidate)
	}
	return normalized
}

// Legacy helpers kept for backward compatibility with tests that call them directly.

func getEnvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid integer env var, using fallback", "key", key, "value", v, "fallback", fallback)
		return fallback
	}
	return i
}
