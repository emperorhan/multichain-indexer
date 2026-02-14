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
	Pipeline PipelineConfig
	Server   ServerConfig
	Log      LogConfig
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
	Addr    string
	Timeout time.Duration
}

type SolanaConfig struct {
	RPCURL  string
	Network string
}

type BaseConfig struct {
	RPCURL  string
	Network string
}

type PipelineConfig struct {
	WatchedAddresses       []string // legacy alias of SolanaWatchedAddresses
	SolanaWatchedAddresses []string
	BaseWatchedAddresses   []string
	FetchWorkers           int
	NormalizerWorkers      int
	BatchSize              int
	IndexingIntervalMs     int
	ChannelBufferSize      int
}

type ServerConfig struct {
	HealthPort int
}

type LogConfig struct {
	Level string
}

func Load() (*Config, error) {
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
			Addr:    getEnv("SIDECAR_ADDR", "localhost:50051"),
			Timeout: time.Duration(getEnvInt("SIDECAR_TIMEOUT_SEC", 30)) * time.Second,
		},
		Solana: SolanaConfig{
			RPCURL:  getEnvAny([]string{"SOLANA_DEVNET_RPC_URL", "SOLANA_RPC_URL"}, "https://api.devnet.solana.com"),
			Network: getEnv("SOLANA_NETWORK", "devnet"),
		},
		Base: BaseConfig{
			RPCURL:  getEnvAny([]string{"BASE_SEPOLIA_RPC_URL", "BASE_RPC_URL"}, ""),
			Network: getEnv("BASE_NETWORK", "sepolia"),
		},
		Pipeline: PipelineConfig{
			FetchWorkers:       getEnvInt("FETCH_WORKERS", 2),
			NormalizerWorkers:  getEnvInt("NORMALIZER_WORKERS", 2),
			BatchSize:          getEnvInt("BATCH_SIZE", 100),
			IndexingIntervalMs: getEnvInt("INDEXING_INTERVAL_MS", 5000),
			ChannelBufferSize:  getEnvInt("CHANNEL_BUFFER_SIZE", 10),
		},
		Server: ServerConfig{
			HealthPort: getEnvInt("HEALTH_PORT", 8080),
		},
		Log: LogConfig{
			Level: getEnv("LOG_LEVEL", "info"),
		},
	}

	cfg.Pipeline.SolanaWatchedAddresses = parseAddressCSV(
		getEnvAny([]string{"SOLANA_WATCHED_ADDRESSES", "WATCHED_ADDRESSES"}, ""),
	)
	cfg.Pipeline.BaseWatchedAddresses = parseAddressCSV(getEnv("BASE_WATCHED_ADDRESSES", ""))
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
	if c.Solana.RPCURL == "" {
		return fmt.Errorf("SOLANA_RPC_URL is required")
	}
	if c.Base.RPCURL == "" {
		return fmt.Errorf("BASE_SEPOLIA_RPC_URL is required")
	}
	if c.Sidecar.Addr == "" {
		return fmt.Errorf("SIDECAR_ADDR is required")
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

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
