package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear env vars that might interfere
	t.Setenv("DB_URL", "postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable")
	t.Setenv("SOLANA_RPC_URL", "https://api.devnet.solana.com")
	t.Setenv("SIDECAR_ADDR", "localhost:50051")
	t.Setenv("WATCHED_ADDRESSES", "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "postgres://indexer:indexer@localhost:5433/custody_indexer?sslmode=disable", cfg.DB.URL)
	assert.Equal(t, 25, cfg.DB.MaxOpenConns)
	assert.Equal(t, 5, cfg.DB.MaxIdleConns)
	assert.Equal(t, "redis://localhost:6380", cfg.Redis.URL)
	assert.Equal(t, "localhost:50051", cfg.Sidecar.Addr)
	assert.Equal(t, "https://api.devnet.solana.com", cfg.Solana.RPCURL)
	assert.Equal(t, "devnet", cfg.Solana.Network)
	assert.Equal(t, 2, cfg.Pipeline.FetchWorkers)
	assert.Equal(t, 2, cfg.Pipeline.NormalizerWorkers)
	assert.Equal(t, 100, cfg.Pipeline.BatchSize)
	assert.Equal(t, 5000, cfg.Pipeline.IndexingIntervalMs)
	assert.Equal(t, 10, cfg.Pipeline.ChannelBufferSize)
	assert.Equal(t, 8080, cfg.Server.HealthPort)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Empty(t, cfg.Pipeline.WatchedAddresses)
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("DB_URL", "postgres://test:test@db:5432/testdb")
	t.Setenv("SOLANA_RPC_URL", "https://mainnet.solana.com")
	t.Setenv("SIDECAR_ADDR", "sidecar:50051")
	t.Setenv("REDIS_URL", "redis://redis:6379")
	t.Setenv("SOLANA_NETWORK", "mainnet")
	t.Setenv("FETCH_WORKERS", "4")
	t.Setenv("NORMALIZER_WORKERS", "3")
	t.Setenv("BATCH_SIZE", "500")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("HEALTH_PORT", "9090")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "postgres://test:test@db:5432/testdb", cfg.DB.URL)
	assert.Equal(t, "https://mainnet.solana.com", cfg.Solana.RPCURL)
	assert.Equal(t, "sidecar:50051", cfg.Sidecar.Addr)
	assert.Equal(t, "redis://redis:6379", cfg.Redis.URL)
	assert.Equal(t, "mainnet", cfg.Solana.Network)
	assert.Equal(t, 4, cfg.Pipeline.FetchWorkers)
	assert.Equal(t, 3, cfg.Pipeline.NormalizerWorkers)
	assert.Equal(t, 500, cfg.Pipeline.BatchSize)
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.Equal(t, 9090, cfg.Server.HealthPort)
}

func TestLoad_WatchedAddresses_Parsing(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x:x@localhost/db")
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
		})
	}
}

func TestValidate_MissingDBURL(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: ""},
		Solana:  SolanaConfig{RPCURL: "https://rpc.example.com"},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
	}
	err := cfg.validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DB_URL")
}

func TestValidate_MissingSolanaRPCURL(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: ""},
		Sidecar: SidecarConfig{Addr: "localhost:50051"},
	}
	err := cfg.validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SOLANA_RPC_URL")
}

func TestValidate_MissingSidecarAddr(t *testing.T) {
	cfg := &Config{
		DB:      DBConfig{URL: "postgres://x:x@localhost/db"},
		Solana:  SolanaConfig{RPCURL: "https://rpc.example.com"},
		Sidecar: SidecarConfig{Addr: ""},
	}
	err := cfg.validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_ADDR")
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
