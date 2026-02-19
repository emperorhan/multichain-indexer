package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	dbStatementTimeoutDefaultMS  = 30000
	dbStatementTimeoutMinMS      = 0
	dbStatementTimeoutMaxMS      = 3_600_000
	dbPoolStatsIntervalDefaultMS = 5000
	dbPoolStatsIntervalMinMS     = 100
	dbPoolStatsIntervalMaxMS     = 3_600_000
)

type Config struct {
	DB       DBConfig
	Redis    RedisConfig
	Sidecar  SidecarConfig
	Solana   SolanaConfig
	Base     BaseConfig
	Ethereum EthereumConfig
	BTC      BTCConfig
	Polygon  PolygonConfig
	Arbitrum ArbitrumConfig
	BSC      BSCConfig
	Runtime  RuntimeConfig
	Pipeline PipelineConfig
	Server   ServerConfig
	Log      LogConfig
	Tracing  TracingConfig
	Alert    AlertConfig
}

type AlertConfig struct {
	SlackWebhookURL    string
	WebhookURL         string
	CooldownMS         int
}

type DBConfig struct {
	URL                 string
	MaxOpenConns        int
	MaxIdleConns        int
	ConnMaxLifetime     time.Duration
	StatementTimeoutMS  int
	PoolStatsIntervalMS int
}

type RedisConfig struct {
	URL string
}

type SidecarConfig struct {
	Addr       string
	Timeout    time.Duration
	TLSEnabled bool
	TLSCert    string // client cert for mTLS (PEM path)
	TLSKey     string // client key for mTLS (PEM path)
	TLSCA      string // CA cert to verify server (PEM path)
}

type RPCRateLimitConfig struct {
	RPS   float64
	Burst int
}

type SolanaConfig struct {
	RPCURL    string
	Network   string
	RateLimit RPCRateLimitConfig
	DBURL     string // optional chain-specific DB URL (env: SOLANA_DB_URL)
}

type BaseConfig struct {
	RPCURL    string
	Network   string
	RateLimit RPCRateLimitConfig
	DBURL     string // optional chain-specific DB URL (env: BASE_DB_URL)
}

type EthereumConfig struct {
	RPCURL    string
	Network   string
	RateLimit RPCRateLimitConfig
	DBURL     string // optional chain-specific DB URL (env: ETHEREUM_DB_URL)
}

type BTCConfig struct {
	RPCURL    string
	Network   string
	RateLimit RPCRateLimitConfig
	DBURL     string // optional chain-specific DB URL (env: BTC_DB_URL)
}

type PolygonConfig struct {
	RPCURL    string
	Network   string
	RateLimit RPCRateLimitConfig
	DBURL     string // optional chain-specific DB URL (env: POLYGON_DB_URL)
}

type ArbitrumConfig struct {
	RPCURL    string
	Network   string
	RateLimit RPCRateLimitConfig
	DBURL     string // optional chain-specific DB URL (env: ARBITRUM_DB_URL)
}

type BSCConfig struct {
	RPCURL    string
	Network   string
	RateLimit RPCRateLimitConfig
	DBURL     string // optional chain-specific DB URL (env: BSC_DB_URL)
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
	DeploymentMode string
	LikeGroup      string
	ChainTargets   []string
}

type PipelineConfig struct {
	ReorgDetectorIntervalMs  int
	FinalizerIntervalMs      int
	BTCFinalityConfirmations int
	WatchedAddresses         []string // legacy alias of SolanaWatchedAddresses
	SolanaWatchedAddresses                        []string
	BaseWatchedAddresses                          []string
	EthereumWatchedAddresses                      []string
	BTCWatchedAddresses                           []string
	PolygonWatchedAddresses                       []string
	ArbitrumWatchedAddresses                      []string
	BSCWatchedAddresses                           []string
	FetchWorkers                                  int
	NormalizerWorkers                             int
	BatchSize                                     int
	IndexingIntervalMs                            int
	ChannelBufferSize                             int
	CoordinatorAutoTuneEnabled                    bool
	CoordinatorAutoTuneMinBatchSize               int
	CoordinatorAutoTuneMaxBatchSize               int
	CoordinatorAutoTuneStepUp                     int
	CoordinatorAutoTuneStepDown                   int
	CoordinatorAutoTuneLagHighWatermark           int64
	CoordinatorAutoTuneLagLowWatermark            int64
	CoordinatorAutoTuneQueueHighPct               int
	CoordinatorAutoTuneQueueLowPct                int
	CoordinatorAutoTuneHysteresisTicks            int
	CoordinatorAutoTuneTelemetryStaleTicks        int
	CoordinatorAutoTuneTelemetryRecoveryTicks     int
	CoordinatorAutoTuneOperatorOverrideBatch      int
	CoordinatorAutoTuneOperatorReleaseTicks       int
	CoordinatorAutoTunePolicyVersion              string
	CoordinatorAutoTunePolicyManifestDigest       string
	CoordinatorAutoTunePolicyManifestRefreshEpoch int64
	CoordinatorAutoTunePolicyActivationHoldTicks  int
	IndexedBlocksRetention                        int
	StreamTransportEnabled                        bool
	StreamNamespace                               string
	StreamSessionID                               string
}

type ServerConfig struct {
	HealthPort      int
	MetricsAuthUser string
	MetricsAuthPass string
	AdminAddr       string
	AdminAuthUser   string
	AdminAuthPass   string
}

type LogConfig struct {
	Level string
}

type TracingConfig struct {
	Enabled     bool
	Endpoint    string  // OTLP gRPC endpoint (e.g. "localhost:4317")
	Insecure    bool    // Use plaintext gRPC (true for local, false for TLS-enabled collectors)
	SampleRatio float64 // Fraction of traces to sample (0.0–1.0). 0 defaults to 0.1.
}

func Load() (*Config, error) {
	statementTimeoutMS, err := getEnvIntBounded("DB_STATEMENT_TIMEOUT_MS", dbStatementTimeoutDefaultMS, dbStatementTimeoutMinMS, dbStatementTimeoutMaxMS)
	if err != nil {
		return nil, fmt.Errorf("DB_STATEMENT_TIMEOUT_MS: %w", err)
	}

	poolStatsIntervalMS, err := getEnvIntBounded("DB_POOL_STATS_INTERVAL_MS", dbPoolStatsIntervalDefaultMS, dbPoolStatsIntervalMinMS, dbPoolStatsIntervalMaxMS)
	if err != nil {
		return nil, fmt.Errorf("DB_POOL_STATS_INTERVAL_MS: %w", err)
	}

	pipelineBatchSize := getEnvInt("BATCH_SIZE", 100)

	cfg := &Config{
		DB: DBConfig{
			URL:                 getEnv("DB_URL", ""),
			MaxOpenConns:        getEnvInt("DB_MAX_OPEN_CONNS", 50),
			MaxIdleConns:        getEnvInt("DB_MAX_IDLE_CONNS", 10),
			ConnMaxLifetime:     time.Duration(getEnvInt("DB_CONN_MAX_LIFETIME_MIN", 30)) * time.Minute,
			StatementTimeoutMS:  statementTimeoutMS,
			PoolStatsIntervalMS: poolStatsIntervalMS,
		},
		Redis: RedisConfig{
			URL: getEnv("REDIS_URL", "redis://localhost:6380"),
		},
		Sidecar: SidecarConfig{
			Addr:       getEnv("SIDECAR_ADDR", "localhost:50051"),
			Timeout:    time.Duration(getEnvInt("SIDECAR_TIMEOUT_SEC", 30)) * time.Second,
			TLSEnabled: getEnvBool("SIDECAR_TLS_ENABLED", false),
			TLSCert:    getEnv("SIDECAR_TLS_CERT", ""),
			TLSKey:     getEnv("SIDECAR_TLS_KEY", ""),
			TLSCA:      getEnv("SIDECAR_TLS_CA", ""),
		},
		Solana: SolanaConfig{
			RPCURL:  getEnvAny([]string{"SOLANA_DEVNET_RPC_URL", "SOLANA_RPC_URL"}, "https://api.devnet.solana.com"),
			Network: getEnv("SOLANA_NETWORK", "devnet"),
			RateLimit: RPCRateLimitConfig{
				RPS:   getEnvFloat("SOLANA_RPC_RATE_LIMIT", 10),
				Burst: getEnvInt("SOLANA_RPC_BURST", 20),
			},
			DBURL: getEnv("SOLANA_DB_URL", ""),
		},
		Base: BaseConfig{
			RPCURL:  getEnvAny([]string{"BASE_SEPOLIA_RPC_URL", "BASE_RPC_URL"}, ""),
			Network: getEnv("BASE_NETWORK", "sepolia"),
			RateLimit: RPCRateLimitConfig{
				RPS:   getEnvFloat("BASE_RPC_RATE_LIMIT", 25),
				Burst: getEnvInt("BASE_RPC_BURST", 50),
			},
			DBURL: getEnv("BASE_DB_URL", ""),
		},
		Ethereum: EthereumConfig{
			RPCURL:  getEnvAny([]string{"ETH_MAINNET_RPC_URL", "ETHEREUM_RPC_URL"}, ""),
			Network: getEnv("ETHEREUM_NETWORK", "mainnet"),
			RateLimit: RPCRateLimitConfig{
				RPS:   getEnvFloat("ETH_RPC_RATE_LIMIT", 25),
				Burst: getEnvInt("ETH_RPC_BURST", 50),
			},
			DBURL: getEnv("ETHEREUM_DB_URL", ""),
		},
		BTC: BTCConfig{
			RPCURL:  getEnvAny([]string{"BTC_TESTNET_RPC_URL", "BTC_RPC_URL"}, ""),
			Network: getEnv("BTC_NETWORK", "testnet"),
			RateLimit: RPCRateLimitConfig{
				RPS:   getEnvFloat("BTC_RPC_RATE_LIMIT", 5),
				Burst: getEnvInt("BTC_RPC_BURST", 10),
			},
			DBURL: getEnv("BTC_DB_URL", ""),
		},
		Polygon: PolygonConfig{
			RPCURL:  getEnvAny([]string{"POLYGON_RPC_URL", "POLYGON_MAINNET_RPC_URL"}, ""),
			Network: getEnv("POLYGON_NETWORK", "mainnet"),
			RateLimit: RPCRateLimitConfig{
				RPS:   getEnvFloat("POLYGON_RPC_RATE_LIMIT", 25),
				Burst: getEnvInt("POLYGON_RPC_BURST", 50),
			},
			DBURL: getEnv("POLYGON_DB_URL", ""),
		},
		Arbitrum: ArbitrumConfig{
			RPCURL:  getEnvAny([]string{"ARBITRUM_RPC_URL", "ARBITRUM_MAINNET_RPC_URL"}, ""),
			Network: getEnv("ARBITRUM_NETWORK", "mainnet"),
			RateLimit: RPCRateLimitConfig{
				RPS:   getEnvFloat("ARBITRUM_RPC_RATE_LIMIT", 25),
				Burst: getEnvInt("ARBITRUM_RPC_BURST", 50),
			},
			DBURL: getEnv("ARBITRUM_DB_URL", ""),
		},
		BSC: BSCConfig{
			RPCURL:  getEnvAny([]string{"BSC_RPC_URL", "BSC_MAINNET_RPC_URL"}, ""),
			Network: getEnv("BSC_NETWORK", "mainnet"),
			RateLimit: RPCRateLimitConfig{
				RPS:   getEnvFloat("BSC_RPC_RATE_LIMIT", 25),
				Burst: getEnvInt("BSC_RPC_BURST", 50),
			},
			DBURL: getEnv("BSC_DB_URL", ""),
		},
		Runtime: RuntimeConfig{
			DeploymentMode: strings.ToLower(getEnv("RUNTIME_DEPLOYMENT_MODE", RuntimeDeploymentModeLikeGroup)),
			LikeGroup:      strings.ToLower(getEnv("RUNTIME_LIKE_GROUP", "")),
		},
		Pipeline: PipelineConfig{
			ReorgDetectorIntervalMs:  getEnvInt("REORG_DETECTOR_INTERVAL_MS", 30000),
			FinalizerIntervalMs:      getEnvInt("FINALIZER_INTERVAL_MS", 60000),
			BTCFinalityConfirmations: getEnvInt("BTC_FINALITY_CONFIRMATIONS", 6),
			FetchWorkers:                                  getEnvInt("FETCH_WORKERS", 2),
			NormalizerWorkers:                             getEnvInt("NORMALIZER_WORKERS", 2),
			BatchSize:                                     pipelineBatchSize,
			IndexingIntervalMs:                            getEnvInt("INDEXING_INTERVAL_MS", 5000),
			ChannelBufferSize:                             getEnvInt("CHANNEL_BUFFER_SIZE", 10),
			CoordinatorAutoTuneEnabled:                    getEnvBool("COORDINATOR_AUTOTUNE_ENABLED", false),
			CoordinatorAutoTuneMinBatchSize:               getEnvInt("COORDINATOR_AUTOTUNE_MIN_BATCH_SIZE", 10),
			CoordinatorAutoTuneMaxBatchSize:               getEnvInt("COORDINATOR_AUTOTUNE_MAX_BATCH_SIZE", pipelineBatchSize),
			CoordinatorAutoTuneStepUp:                     getEnvInt("COORDINATOR_AUTOTUNE_STEP_UP", 10),
			CoordinatorAutoTuneStepDown:                   getEnvInt("COORDINATOR_AUTOTUNE_STEP_DOWN", 10),
			CoordinatorAutoTuneLagHighWatermark:           int64(getEnvInt("COORDINATOR_AUTOTUNE_LAG_HIGH_WATERMARK", 500)),
			CoordinatorAutoTuneLagLowWatermark:            int64(getEnvInt("COORDINATOR_AUTOTUNE_LAG_LOW_WATERMARK", 100)),
			CoordinatorAutoTuneQueueHighPct:               getEnvInt("COORDINATOR_AUTOTUNE_QUEUE_HIGH_PCT", 80),
			CoordinatorAutoTuneQueueLowPct:                getEnvInt("COORDINATOR_AUTOTUNE_QUEUE_LOW_PCT", 30),
			CoordinatorAutoTuneHysteresisTicks:            getEnvInt("COORDINATOR_AUTOTUNE_HYSTERESIS_TICKS", 2),
			CoordinatorAutoTuneTelemetryStaleTicks:        getEnvInt("COORDINATOR_AUTOTUNE_TELEMETRY_STALE_TICKS", 2),
			CoordinatorAutoTuneTelemetryRecoveryTicks:     getEnvInt("COORDINATOR_AUTOTUNE_TELEMETRY_RECOVERY_TICKS", 1),
			CoordinatorAutoTuneOperatorOverrideBatch:      getEnvInt("COORDINATOR_AUTOTUNE_OPERATOR_OVERRIDE_BATCH_SIZE", 0),
			CoordinatorAutoTuneOperatorReleaseTicks:       getEnvInt("COORDINATOR_AUTOTUNE_OPERATOR_RELEASE_HOLD_TICKS", 2),
			CoordinatorAutoTunePolicyVersion:              getEnv("COORDINATOR_AUTOTUNE_POLICY_VERSION", "policy-v1"),
			CoordinatorAutoTunePolicyManifestDigest:       getEnv("COORDINATOR_AUTOTUNE_POLICY_MANIFEST_DIGEST", "manifest-v1"),
			CoordinatorAutoTunePolicyManifestRefreshEpoch: int64(getEnvInt("COORDINATOR_AUTOTUNE_POLICY_MANIFEST_REFRESH_EPOCH", 0)),
			CoordinatorAutoTunePolicyActivationHoldTicks:  getEnvInt("COORDINATOR_AUTOTUNE_POLICY_ACTIVATION_HOLD_TICKS", 1),
			IndexedBlocksRetention:                        getEnvInt("INDEXED_BLOCKS_RETENTION", 10000),
			StreamTransportEnabled:                        getEnvBool("PIPELINE_STREAM_TRANSPORT_ENABLED", false),
			StreamNamespace:                               getEnv("PIPELINE_STREAM_NAMESPACE", "pipeline"),
			StreamSessionID:                               getEnv("PIPELINE_STREAM_SESSION_ID", ""),
		},
		Server: ServerConfig{
			HealthPort:      getEnvInt("HEALTH_PORT", 8080),
			MetricsAuthUser: getEnv("METRICS_AUTH_USER", ""),
			MetricsAuthPass: getEnv("METRICS_AUTH_PASS", ""),
			AdminAddr:       getEnv("ADMIN_ADDR", ""),
			AdminAuthUser:   getEnv("ADMIN_AUTH_USER", ""),
			AdminAuthPass:   getEnv("ADMIN_AUTH_PASS", ""),
		},
		Log: LogConfig{
			Level: getEnv("LOG_LEVEL", "info"),
		},
		Tracing: TracingConfig{
			Enabled:     getEnvBool("OTEL_TRACING_ENABLED", false),
			Endpoint:    getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
			Insecure:    getEnvBool("OTEL_EXPORTER_OTLP_INSECURE", false),
			SampleRatio: getEnvFloat("OTEL_TRACE_SAMPLE_RATIO", 0.1),
		},
		Alert: AlertConfig{
			SlackWebhookURL: getEnv("SLACK_WEBHOOK_URL", ""),
			WebhookURL:      getEnv("ALERT_WEBHOOK_URL", ""),
			CooldownMS:      getEnvInt("ALERT_COOLDOWN_MS", 1800000), // default 30 min
		},
	}

	cfg.Pipeline.SolanaWatchedAddresses = parseAddressCSV(
		getEnvAny([]string{"SOLANA_WATCHED_ADDRESSES", "WATCHED_ADDRESSES"}, ""),
	)
	cfg.Pipeline.BaseWatchedAddresses = parseAddressCSV(getEnv("BASE_WATCHED_ADDRESSES", ""))
	cfg.Pipeline.EthereumWatchedAddresses = parseAddressCSV(getEnv("ETH_WATCHED_ADDRESSES", ""))
	cfg.Pipeline.BTCWatchedAddresses = parseAddressCSV(getEnv("BTC_WATCHED_ADDRESSES", ""))
	cfg.Pipeline.PolygonWatchedAddresses = parseAddressCSV(getEnv("POLYGON_WATCHED_ADDRESSES", ""))
	cfg.Pipeline.ArbitrumWatchedAddresses = parseAddressCSV(getEnv("ARBITRUM_WATCHED_ADDRESSES", ""))
	cfg.Pipeline.BSCWatchedAddresses = parseAddressCSV(getEnv("BSC_WATCHED_ADDRESSES", ""))
	cfg.Runtime.ChainTargets = normalizeCSVValues(parseAddressCSV(
		getEnvAny([]string{"RUNTIME_CHAIN_TARGETS", "RUNTIME_CHAIN_TARGET"}, ""),
	))
	cfg.Pipeline.WatchedAddresses = append([]string(nil), cfg.Pipeline.SolanaWatchedAddresses...)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

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

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvAny(keys []string, fallback string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return fallback
}

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

func getEnvIntBounded(key string, fallback int, min int, max int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("must be an integer")
	}

	if value < min || value > max {
		return 0, fmt.Errorf("must be within [%d, %d]", min, max)
	}

	return value, nil
}

func getEnvFloat(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		slog.Warn("invalid float env var, using fallback", "key", key, "value", v, "fallback", fallback)
		return fallback
	}
	return f
}

func getEnvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch v {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
