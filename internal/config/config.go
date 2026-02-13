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

type PipelineConfig struct {
	WatchedAddresses   []string
	FetchWorkers       int
	NormalizerWorkers  int
	BatchSize          int
	IndexingIntervalMs int
	ChannelBufferSize  int
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
			RPCURL:  getEnv("SOLANA_RPC_URL", "https://api.devnet.solana.com"),
			Network: getEnv("SOLANA_NETWORK", "devnet"),
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

	if addrs := getEnv("WATCHED_ADDRESSES", ""); addrs != "" {
		for _, addr := range strings.Split(addrs, ",") {
			addr = strings.TrimSpace(addr)
			if addr != "" {
				cfg.Pipeline.WatchedAddresses = append(cfg.Pipeline.WatchedAddresses, addr)
			}
		}
	}

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

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
