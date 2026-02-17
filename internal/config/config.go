package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DB       DBConfig
	Redis    RedisConfig
	Sidecar  SidecarConfig
	Solana   SolanaConfig
	Base     BaseConfig
	BTC      BTCConfig
	Runtime  RuntimeConfig
	Pipeline PipelineConfig
	Server   ServerConfig
	Log      LogConfig
	Tracing  TracingConfig
}

type DBConfig struct {
	URL             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
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

type SolanaConfig struct {
	RPCURL  string
	Network string
}

type BaseConfig struct {
	RPCURL  string
	Network string
}

type BTCConfig struct {
	RPCURL  string
	Network string
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
	WatchedAddresses                              []string // legacy alias of SolanaWatchedAddresses
	SolanaWatchedAddresses                        []string
	BaseWatchedAddresses                          []string
	BTCWatchedAddresses                           []string
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
}

type ServerConfig struct {
	HealthPort int
}

type LogConfig struct {
	Level string
}

type TracingConfig struct {
	Enabled  bool
	Endpoint string // OTLP gRPC endpoint (e.g. "localhost:4317")
}

func Load() (*Config, error) {
	pipelineBatchSize := getEnvInt("BATCH_SIZE", 100)

	cfg := &Config{
		DB: DBConfig{
			URL:             getEnv("DB_URL", "postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable"),
			MaxOpenConns:    getEnvInt("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:    getEnvInt("DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: time.Duration(getEnvInt("DB_CONN_MAX_LIFETIME_MIN", 30)) * time.Minute,
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
		},
		Base: BaseConfig{
			RPCURL:  getEnvAny([]string{"BASE_SEPOLIA_RPC_URL", "BASE_RPC_URL"}, ""),
			Network: getEnv("BASE_NETWORK", "sepolia"),
		},
		BTC: BTCConfig{
			RPCURL:  getEnvAny([]string{"BTC_TESTNET_RPC_URL", "BTC_RPC_URL"}, ""),
			Network: getEnv("BTC_NETWORK", "testnet"),
		},
		Runtime: RuntimeConfig{
			DeploymentMode: strings.ToLower(getEnv("RUNTIME_DEPLOYMENT_MODE", RuntimeDeploymentModeLikeGroup)),
			LikeGroup:      strings.ToLower(getEnv("RUNTIME_LIKE_GROUP", "")),
		},
		Pipeline: PipelineConfig{
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
		},
		Server: ServerConfig{
			HealthPort: getEnvInt("HEALTH_PORT", 8080),
		},
		Log: LogConfig{
			Level: getEnv("LOG_LEVEL", "info"),
		},
		Tracing: TracingConfig{
			Enabled:  getEnvBool("OTEL_TRACING_ENABLED", false),
			Endpoint: getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		},
	}

	cfg.Pipeline.SolanaWatchedAddresses = parseAddressCSV(
		getEnvAny([]string{"SOLANA_WATCHED_ADDRESSES", "WATCHED_ADDRESSES"}, ""),
	)
	cfg.Pipeline.BaseWatchedAddresses = parseAddressCSV(getEnv("BASE_WATCHED_ADDRESSES", ""))
	cfg.Pipeline.BTCWatchedAddresses = parseAddressCSV(getEnv("BTC_WATCHED_ADDRESSES", ""))
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
	if requiredChains["btc"] && c.BTC.RPCURL == "" {
		return fmt.Errorf("BTC_TESTNET_RPC_URL is required for selected runtime targets")
	}
	return nil
}

func requiredRuntimeChains(runtime RuntimeConfig) (map[string]bool, error) {
	required := map[string]bool{
		"solana": false,
		"base":   false,
		"btc":    false,
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
	normalized := strings.TrimSpace(strings.ToLower(target))
	if normalized == "" {
		return "", fmt.Errorf("runtime chain target must not be empty")
	}
	parts := strings.SplitN(normalized, "-", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("runtime chain target %q must be in <chain>-<network> format", target)
	}

	switch parts[0] {
	case "solana", "base", "btc":
		return parts[0], nil
	default:
		return "", fmt.Errorf("runtime chain target %q has unsupported chain %q", target, parts[0])
	}
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
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
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
