package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear env vars that might interfere
	t.Setenv("DB_URL", "postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://sepolia.base.org")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc.testnet.example")
	t.Setenv("SOLANA_RPC_URL", "https://api.devnet.solana.com")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")
	t.Setenv("WATCHED_ADDRESSES", "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable", cfg.DB.URL)
	assert.Equal(t, 25, cfg.DB.MaxOpenConns)
	assert.Equal(t, 5, cfg.DB.MaxIdleConns)
	assert.Equal(t, dbStatementTimeoutDefaultMS, cfg.DB.StatementTimeoutMS)
	assert.Equal(t, dbPoolStatsIntervalDefaultMS, cfg.DB.PoolStatsIntervalMS)
	assert.Equal(t, "redis://localhost:6380", cfg.Redis.URL)
	assert.Equal(t, "localhost:50051", cfg.Sidecar.Addr)
	assert.Equal(t, "https://api.devnet.solana.com", cfg.Solana.RPCURL)
	assert.Equal(t, "devnet", cfg.Solana.Network)
	assert.Equal(t, "https://sepolia.base.org", cfg.Base.RPCURL)
	assert.Equal(t, "sepolia", cfg.Base.Network)
	assert.Equal(t, "https://btc.testnet.example", cfg.BTC.RPCURL)
	assert.Equal(t, "testnet", cfg.BTC.Network)
	assert.Equal(t, 2, cfg.Pipeline.FetchWorkers)
	assert.Equal(t, 2, cfg.Pipeline.NormalizerWorkers)
	assert.Equal(t, 100, cfg.Pipeline.BatchSize)
	assert.Equal(t, 5000, cfg.Pipeline.IndexingIntervalMs)
	assert.Equal(t, 10, cfg.Pipeline.ChannelBufferSize)
	assert.False(t, cfg.Pipeline.CoordinatorAutoTuneEnabled)
	assert.Equal(t, 10, cfg.Pipeline.CoordinatorAutoTuneMinBatchSize)
	assert.Equal(t, 100, cfg.Pipeline.CoordinatorAutoTuneMaxBatchSize)
	assert.Equal(t, 10, cfg.Pipeline.CoordinatorAutoTuneStepUp)
	assert.Equal(t, 10, cfg.Pipeline.CoordinatorAutoTuneStepDown)
	assert.Equal(t, int64(500), cfg.Pipeline.CoordinatorAutoTuneLagHighWatermark)
	assert.Equal(t, int64(100), cfg.Pipeline.CoordinatorAutoTuneLagLowWatermark)
	assert.Equal(t, 80, cfg.Pipeline.CoordinatorAutoTuneQueueHighPct)
	assert.Equal(t, 30, cfg.Pipeline.CoordinatorAutoTuneQueueLowPct)
	assert.Equal(t, 2, cfg.Pipeline.CoordinatorAutoTuneHysteresisTicks)
	assert.Equal(t, 2, cfg.Pipeline.CoordinatorAutoTuneTelemetryStaleTicks)
	assert.Equal(t, 1, cfg.Pipeline.CoordinatorAutoTuneTelemetryRecoveryTicks)
	assert.Equal(t, 0, cfg.Pipeline.CoordinatorAutoTuneOperatorOverrideBatch)
	assert.Equal(t, 2, cfg.Pipeline.CoordinatorAutoTuneOperatorReleaseTicks)
	assert.Equal(t, "policy-v1", cfg.Pipeline.CoordinatorAutoTunePolicyVersion)
	assert.Equal(t, "manifest-v1", cfg.Pipeline.CoordinatorAutoTunePolicyManifestDigest)
	assert.Equal(t, int64(0), cfg.Pipeline.CoordinatorAutoTunePolicyManifestRefreshEpoch)
	assert.Equal(t, 1, cfg.Pipeline.CoordinatorAutoTunePolicyActivationHoldTicks)
	assert.Equal(t, 8080, cfg.Server.HealthPort)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, RuntimeDeploymentModeLikeGroup, cfg.Runtime.DeploymentMode)
	assert.Empty(t, cfg.Runtime.LikeGroup)
	assert.Empty(t, cfg.Runtime.ChainTargets)
	assert.False(t, cfg.Pipeline.StreamTransportEnabled)
	assert.Equal(t, "pipeline", cfg.Pipeline.StreamNamespace)
	assert.Empty(t, cfg.Pipeline.StreamSessionID)
	assert.Empty(t, cfg.Pipeline.WatchedAddresses)
	assert.Empty(t, cfg.Pipeline.SolanaWatchedAddresses)
	assert.Empty(t, cfg.Pipeline.BaseWatchedAddresses)
	assert.Empty(t, cfg.Pipeline.BTCWatchedAddresses)
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("DB_URL", "postgres://test:test@db:5432/testdb")
	t.Setenv("SOLANA_DEVNET_RPC_URL", "https://mainnet.solana.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base-sepolia.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc-testnet.example")
	t.Setenv("SIDECAR_ADDR", "sidecar:50051")
	t.Setenv("REDIS_URL", "redis://redis:6379")
	t.Setenv("SOLANA_NETWORK", "mainnet")
	t.Setenv("PIPELINE_STREAM_TRANSPORT_ENABLED", "true")
	t.Setenv("PIPELINE_STREAM_NAMESPACE", "mandatory-chain")
	t.Setenv("PIPELINE_STREAM_SESSION_ID", "session-2026-02-18")
	t.Setenv("FETCH_WORKERS", "4")
	t.Setenv("NORMALIZER_WORKERS", "3")
	t.Setenv("BATCH_SIZE", "500")
	t.Setenv("COORDINATOR_AUTOTUNE_ENABLED", "true")
	t.Setenv("COORDINATOR_AUTOTUNE_MIN_BATCH_SIZE", "50")
	t.Setenv("COORDINATOR_AUTOTUNE_MAX_BATCH_SIZE", "800")
	t.Setenv("COORDINATOR_AUTOTUNE_STEP_UP", "25")
	t.Setenv("COORDINATOR_AUTOTUNE_STEP_DOWN", "15")
	t.Setenv("COORDINATOR_AUTOTUNE_LAG_HIGH_WATERMARK", "900")
	t.Setenv("COORDINATOR_AUTOTUNE_LAG_LOW_WATERMARK", "120")
	t.Setenv("COORDINATOR_AUTOTUNE_QUEUE_HIGH_PCT", "85")
	t.Setenv("COORDINATOR_AUTOTUNE_QUEUE_LOW_PCT", "35")
	t.Setenv("COORDINATOR_AUTOTUNE_HYSTERESIS_TICKS", "4")
	t.Setenv("COORDINATOR_AUTOTUNE_TELEMETRY_STALE_TICKS", "6")
	t.Setenv("COORDINATOR_AUTOTUNE_TELEMETRY_RECOVERY_TICKS", "3")
	t.Setenv("COORDINATOR_AUTOTUNE_OPERATOR_OVERRIDE_BATCH_SIZE", "72")
	t.Setenv("COORDINATOR_AUTOTUNE_OPERATOR_RELEASE_HOLD_TICKS", "5")
	t.Setenv("COORDINATOR_AUTOTUNE_POLICY_VERSION", "policy-v2")
	t.Setenv("COORDINATOR_AUTOTUNE_POLICY_MANIFEST_DIGEST", "manifest-v2b")
	t.Setenv("COORDINATOR_AUTOTUNE_POLICY_MANIFEST_REFRESH_EPOCH", "4")
	t.Setenv("COORDINATOR_AUTOTUNE_POLICY_ACTIVATION_HOLD_TICKS", "4")
	t.Setenv("DB_STATEMENT_TIMEOUT_MS", "45000")
	t.Setenv("DB_POOL_STATS_INTERVAL_MS", "12500")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("HEALTH_PORT", "9090")
	t.Setenv("RUNTIME_DEPLOYMENT_MODE", RuntimeDeploymentModeIndependent)
	t.Setenv("RUNTIME_CHAIN_TARGET", "base-sepolia")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "postgres://test:test@db:5432/testdb", cfg.DB.URL)
	assert.Equal(t, "https://mainnet.solana.com", cfg.Solana.RPCURL)
	assert.Equal(t, "https://base-sepolia.example", cfg.Base.RPCURL)
	assert.Equal(t, "https://btc-testnet.example", cfg.BTC.RPCURL)
	assert.Equal(t, "sidecar:50051", cfg.Sidecar.Addr)
	assert.Equal(t, "redis://redis:6379", cfg.Redis.URL)
	assert.Equal(t, "mainnet", cfg.Solana.Network)
	assert.Equal(t, 45000, cfg.DB.StatementTimeoutMS)
	assert.Equal(t, 12500, cfg.DB.PoolStatsIntervalMS)
	assert.Equal(t, 4, cfg.Pipeline.FetchWorkers)
	assert.Equal(t, 3, cfg.Pipeline.NormalizerWorkers)
	assert.Equal(t, 500, cfg.Pipeline.BatchSize)
	assert.True(t, cfg.Pipeline.CoordinatorAutoTuneEnabled)
	assert.Equal(t, 50, cfg.Pipeline.CoordinatorAutoTuneMinBatchSize)
	assert.Equal(t, 800, cfg.Pipeline.CoordinatorAutoTuneMaxBatchSize)
	assert.Equal(t, 25, cfg.Pipeline.CoordinatorAutoTuneStepUp)
	assert.Equal(t, 15, cfg.Pipeline.CoordinatorAutoTuneStepDown)
	assert.Equal(t, int64(900), cfg.Pipeline.CoordinatorAutoTuneLagHighWatermark)
	assert.Equal(t, int64(120), cfg.Pipeline.CoordinatorAutoTuneLagLowWatermark)
	assert.Equal(t, 85, cfg.Pipeline.CoordinatorAutoTuneQueueHighPct)
	assert.Equal(t, 35, cfg.Pipeline.CoordinatorAutoTuneQueueLowPct)
	assert.Equal(t, 4, cfg.Pipeline.CoordinatorAutoTuneHysteresisTicks)
	assert.Equal(t, 6, cfg.Pipeline.CoordinatorAutoTuneTelemetryStaleTicks)
	assert.Equal(t, 3, cfg.Pipeline.CoordinatorAutoTuneTelemetryRecoveryTicks)
	assert.Equal(t, 72, cfg.Pipeline.CoordinatorAutoTuneOperatorOverrideBatch)
	assert.Equal(t, 5, cfg.Pipeline.CoordinatorAutoTuneOperatorReleaseTicks)
	assert.Equal(t, "policy-v2", cfg.Pipeline.CoordinatorAutoTunePolicyVersion)
	assert.Equal(t, "manifest-v2b", cfg.Pipeline.CoordinatorAutoTunePolicyManifestDigest)
	assert.Equal(t, int64(4), cfg.Pipeline.CoordinatorAutoTunePolicyManifestRefreshEpoch)
	assert.Equal(t, 4, cfg.Pipeline.CoordinatorAutoTunePolicyActivationHoldTicks)
	assert.True(t, cfg.Pipeline.StreamTransportEnabled)
	assert.Equal(t, "mandatory-chain", cfg.Pipeline.StreamNamespace)
	assert.Equal(t, "session-2026-02-18", cfg.Pipeline.StreamSessionID)
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.Equal(t, 9090, cfg.Server.HealthPort)
	assert.Equal(t, RuntimeDeploymentModeIndependent, cfg.Runtime.DeploymentMode)
	assert.Equal(t, []string{"base-sepolia"}, cfg.Runtime.ChainTargets)
}

func TestLoad_RejectsInvalidRuntimeTargetFormat(t *testing.T) {
	t.Setenv("DB_URL", "postgres://test:test@db:5432/testdb")
	t.Setenv("SOLANA_DEVNET_RPC_URL", "https://mainnet.solana.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base-sepolia.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc-testnet.example")
	t.Setenv("SIDECAR_ADDR", "sidecar:50051")
	t.Setenv("RUNTIME_CHAIN_TARGET", "solana")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be in <chain>-<network> format")
}

func TestLoad_RejectsUnsupportedRuntimeTargetNetwork(t *testing.T) {
	t.Setenv("DB_URL", "postgres://test:test@db:5432/testdb")
	t.Setenv("SOLANA_DEVNET_RPC_URL", "https://mainnet.solana.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base-sepolia.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc-testnet.example")
	t.Setenv("SIDECAR_ADDR", "sidecar:50051")
	t.Setenv("RUNTIME_CHAIN_TARGET", "solana-mainnet")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported network")
}

func TestLoad_DBStatementTimeout_InvalidValue(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x:x@localhost/db")
	t.Setenv("SOLANA_RPC_URL", "https://rpc.example.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc.example")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")
	t.Setenv("DB_STATEMENT_TIMEOUT_MS", "not-a-number")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_STATEMENT_TIMEOUT_MS")
}

func TestLoad_DBStatementTimeout_OutOfRangeValue(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x:x@localhost/db")
	t.Setenv("SOLANA_RPC_URL", "https://rpc.example.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc.example")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")
	t.Setenv("DB_STATEMENT_TIMEOUT_MS", "-1")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_STATEMENT_TIMEOUT_MS")
}

func TestLoad_DBPoolStatsInterval_Default(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x:x@localhost/db")
	t.Setenv("SOLANA_RPC_URL", "https://rpc.example.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc.example")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, dbPoolStatsIntervalDefaultMS, cfg.DB.PoolStatsIntervalMS)
}

func TestLoad_DBPoolStatsInterval_OutOfRangeLow(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x:x@localhost/db")
	t.Setenv("SOLANA_RPC_URL", "https://rpc.example.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc.example")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")
	t.Setenv("DB_POOL_STATS_INTERVAL_MS", "1")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_POOL_STATS_INTERVAL_MS")
}

func TestLoad_DBPoolStatsInterval_OutOfRangeHigh(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x:x@localhost/db")
	t.Setenv("SOLANA_RPC_URL", "https://rpc.example.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc.example")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")
	t.Setenv("DB_POOL_STATS_INTERVAL_MS", "99999999")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_POOL_STATS_INTERVAL_MS")
}

func TestLoad_DBPoolStatsInterval_InvalidValue(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x:x@localhost/db")
	t.Setenv("SOLANA_RPC_URL", "https://rpc.example.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc.example")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")
	t.Setenv("DB_POOL_STATS_INTERVAL_MS", "not-a-number")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_POOL_STATS_INTERVAL_MS")
}

func TestLoad_DBStatementTimeout_OutOfRangeHighValue(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x:x@localhost/db")
	t.Setenv("SOLANA_RPC_URL", "https://rpc.example.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc.example")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")
	t.Setenv("DB_STATEMENT_TIMEOUT_MS", "6000000")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_STATEMENT_TIMEOUT_MS")
}

func TestLoad_WatchedAddresses_Parsing(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x:x@localhost/db")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc.example")
	t.Setenv("SOLANA_RPC_URL", "https://rpc.example.com")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")

	tests := []struct {
		name     string
		env      string
		expected []string
	}{
		{
			name:     "single address",
			env:      "addr1",
			expected: []string{"addr1"},
		},
		{
			name:     "multiple addresses",
			env:      "addr1,addr2,addr3",
			expected: []string{"addr1", "addr2", "addr3"},
		},
		{
			name:     "with whitespace",
			env:      " addr1 , addr2 , addr3 ",
			expected: []string{"addr1", "addr2", "addr3"},
		},
		{
			name:     "empty strings filtered",
			env:      "addr1,,addr2,",
			expected: []string{"addr1", "addr2"},
		},
		{
			name:     "empty env",
			env:      "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WATCHED_ADDRESSES", tt.env)

			cfg, err := Load()
			require.NoError(t, err)

			assert.Equal(t, tt.expected, cfg.Pipeline.WatchedAddresses)
			assert.Equal(t, tt.expected, cfg.Pipeline.SolanaWatchedAddresses)
		})
	}
}

func TestLoad_ChainSpecificWatchedAddresses(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x:x@localhost/db")
	t.Setenv("SOLANA_RPC_URL", "https://rpc.example.com")
	t.Setenv("BASE_SEPOLIA_RPC_URL", "https://base.example")
	t.Setenv("BTC_TESTNET_RPC_URL", "https://btc.example")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")
	t.Setenv("WATCHED_ADDRESSES", "legacy1,legacy2")
	t.Setenv("SOLANA_WATCHED_ADDRESSES", "sol1,sol2")
	t.Setenv("BASE_WATCHED_ADDRESSES", "base1,base2")
	t.Setenv("BTC_WATCHED_ADDRESSES", "btc1,btc2")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, []string{"sol1", "sol2"}, cfg.Pipeline.SolanaWatchedAddresses)
	assert.Equal(t, []string{"sol1", "sol2"}, cfg.Pipeline.WatchedAddresses)
	assert.Equal(t, []string{"base1", "base2"}, cfg.Pipeline.BaseWatchedAddresses)
	assert.Equal(t, []string{"btc1", "btc2"}, cfg.Pipeline.BTCWatchedAddresses)
}

func TestValidate_MissingDBURL(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: ""},
		Solana:  SolanaConfig{RPCURL: "https://rpc.example.com"},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{DeploymentMode: RuntimeDeploymentModeLikeGroup},
	}
	err := cfg.validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DB_URL")
}

func TestValidate_MissingSolanaRPCURL(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: ""},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{DeploymentMode: RuntimeDeploymentModeLikeGroup},
	}
	err := cfg.validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SOLANA_RPC_URL")
}

func TestValidate_MissingBaseRPCURL(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: "https://rpc.example.com"},
		Base:    BaseConfig{RPCURL: ""},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{DeploymentMode: RuntimeDeploymentModeLikeGroup},
	}
	err := cfg.validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "BASE_SEPOLIA_RPC_URL")
}

func TestValidate_MissingBTCRPCURL(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: "https://rpc.example.com"},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		BTC:     BTCConfig{RPCURL: ""},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{DeploymentMode: RuntimeDeploymentModeLikeGroup},
	}
	err := cfg.validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "BTC_TESTNET_RPC_URL")
}

func TestValidate_MissingSidecarAddr(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: "https://rpc.example.com"},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		Sidecar: SidecarConfig{Addr: ""},
		Runtime: RuntimeConfig{DeploymentMode: RuntimeDeploymentModeLikeGroup},
	}
	err := cfg.validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_ADDR")
}

func TestValidate_InvalidRuntimeDeploymentMode(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: "https://rpc.example.com"},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{DeploymentMode: "wrong-mode"},
	}
	err := cfg.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RUNTIME_DEPLOYMENT_MODE")
}

func TestValidate_IndependentModeRequiresSingleChainTarget(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: "https://rpc.example.com"},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{
			DeploymentMode: RuntimeDeploymentModeIndependent,
			ChainTargets:   []string{"solana-devnet", "base-sepolia"},
		},
	}
	err := cfg.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "supports exactly one chain target")
}

func TestValidate_IndependentModeRejectsLikeGroup(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: "https://rpc.example.com"},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{
			DeploymentMode: RuntimeDeploymentModeIndependent,
			LikeGroup:      RuntimeLikeGroupEVM,
			ChainTargets:   []string{"base-sepolia"},
		},
	}
	err := cfg.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RUNTIME_LIKE_GROUP is not allowed")
}

func TestValidate_IndependentMode_BaseOnlyDoesNotRequireSolanaRPC(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: ""},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{
			DeploymentMode: RuntimeDeploymentModeIndependent,
			ChainTargets:   []string{"base-sepolia"},
		},
	}
	require.NoError(t, cfg.validate())
}

func TestValidate_IndependentMode_SolanaOnlyDoesNotRequireBaseRPC(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: "https://solana.example"},
		Base:    BaseConfig{RPCURL: ""},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{
			DeploymentMode: RuntimeDeploymentModeIndependent,
			ChainTargets:   []string{"solana-devnet"},
		},
	}
	require.NoError(t, cfg.validate())
}

func TestValidate_IndependentMode_AcceptsEthereumTarget(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: ""},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{
			DeploymentMode: RuntimeDeploymentModeIndependent,
			ChainTargets:   []string{"ethereum-mainnet"},
		},
	}
	require.NoError(t, cfg.validate())
}

func TestValidate_IndependentMode_AcceptsBTCTarget(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: ""},
		Base:    BaseConfig{RPCURL: ""},
		BTC:     BTCConfig{RPCURL: "https://btc.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{
			DeploymentMode: RuntimeDeploymentModeIndependent,
			ChainTargets:   []string{"btc-testnet"},
		},
	}
	require.NoError(t, cfg.validate())
}

func TestValidate_IndependentMode_RejectsUnsupportedTargetChain(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: "https://solana.example"},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{
			DeploymentMode: RuntimeDeploymentModeIndependent,
			ChainTargets:   []string{"dogecoin-mainnet"},
		},
	}
	err := cfg.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported chain")
}

func TestChainNameFromTargetKey_AcceptsEthereumMainnet(t *testing.T) {
	chainName, err := chainNameFromTargetKey("ethereum-mainnet")
	require.NoError(t, err)
	assert.Equal(t, "ethereum", chainName)
}

func TestChainNameFromTargetKey_NormalizesWhitespaceAndCase(t *testing.T) {
	chainName, err := chainNameFromTargetKey(" SOLANA-devNET ")
	require.NoError(t, err)
	assert.Equal(t, "solana", chainName)
}

func TestChainNameFromTargetKey_RejectsUnsupportedNetwork(t *testing.T) {
	_, err := chainNameFromTargetKey("base-mainnet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported network")
}

func TestChainNameFromTargetKey_RejectsInvalidFormat(t *testing.T) {
	_, err := chainNameFromTargetKey("solana")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be in <chain>-<network> format")
}

func TestValidate_LikeGroupBTCAllowedWhenRPCConfigured(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: "https://solana.example"},
		Base:    BaseConfig{RPCURL: "https://base.example"},
		BTC:     BTCConfig{RPCURL: "https://btc.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{
			DeploymentMode: RuntimeDeploymentModeLikeGroup,
			LikeGroup:      RuntimeLikeGroupBTC,
		},
	}
	require.NoError(t, cfg.validate())
}

func TestValidate_IndependentMode_BTCOnlyDoesNotRequireSolanaOrBaseRPC(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: ""},
		Base:    BaseConfig{RPCURL: ""},
		BTC:     BTCConfig{RPCURL: "https://btc.example"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
		Runtime: RuntimeConfig{
			DeploymentMode: RuntimeDeploymentModeIndependent,
			ChainTargets:   []string{"btc-testnet"},
		},
	}
	require.NoError(t, cfg.validate())
}

func TestGetEnvInt_InvalidValue(t *testing.T) {
	t.Setenv("TEST_INT", "not_a_number")
	result := getEnvInt("TEST_INT", 42)
	assert.Equal(t, 42, result)
}

func TestGetEnvInt_ValidValue(t *testing.T) {
	t.Setenv("TEST_INT", "99")
	result := getEnvInt("TEST_INT", 42)
	assert.Equal(t, 99, result)
}

func TestGetEnvInt_EmptyValue(t *testing.T) {
	t.Setenv("TEST_INT", "")
	result := getEnvInt("TEST_INT", 42)
	assert.Equal(t, 42, result)
}
