package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/admin"
	"github.com/emperorhan/multichain-indexer/internal/alert"
	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/arbitrum"
	"github.com/emperorhan/multichain-indexer/internal/chain/base"
	"github.com/emperorhan/multichain-indexer/internal/chain/bsc"
	"github.com/emperorhan/multichain-indexer/internal/chain/btc"
	"github.com/emperorhan/multichain-indexer/internal/chain/ethereum"
	"github.com/emperorhan/multichain-indexer/internal/chain/polygon"
	"github.com/emperorhan/multichain-indexer/internal/chain/ratelimit"
	"github.com/emperorhan/multichain-indexer/internal/chain/solana"
	"github.com/emperorhan/multichain-indexer/internal/config"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/pipeline"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/ingester"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/replay"
	"github.com/emperorhan/multichain-indexer/internal/reconciliation"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/emperorhan/multichain-indexer/internal/store/postgres"
	"github.com/emperorhan/multichain-indexer/internal/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

const deterministicInterleaveMaxSkew = 250 * time.Millisecond

type runtimeTarget struct {
	chain   model.Chain
	network model.Network
	group   string
	watched []string
	adapter chain.ChainAdapter
	rpcURL  string
}

type dbStatsProvider interface {
	Stats() sql.DBStats
}

type dbPoolStatsGauges struct {
	open         *prometheus.GaugeVec
	inUse        *prometheus.GaugeVec
	idle         *prometheus.GaugeVec
	waitCount    *prometheus.GaugeVec
	waitDuration *prometheus.GaugeVec
}

func collectDBPoolStats(db dbStatsProvider, targets []runtimeTarget, gauges dbPoolStatsGauges) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("db pool stats collection panicked: %v", r)
		}
	}()
	if db == nil {
		return fmt.Errorf("db stats provider is nil")
	}

	stats := db.Stats()
	for _, target := range targets {
		chainLabel := target.chain.String()
		networkLabel := string(target.network)

		gauges.open.WithLabelValues(chainLabel, networkLabel).Set(float64(stats.OpenConnections))
		gauges.inUse.WithLabelValues(chainLabel, networkLabel).Set(float64(stats.InUse))
		gauges.idle.WithLabelValues(chainLabel, networkLabel).Set(float64(stats.Idle))
		gauges.waitCount.WithLabelValues(chainLabel, networkLabel).Set(float64(stats.WaitCount))
		gauges.waitDuration.WithLabelValues(chainLabel, networkLabel).Set(stats.WaitDuration.Seconds())
	}

	return nil
}

func logSecurityWarnings(cfg *config.Config, logger *slog.Logger) error {
	if strings.Contains(cfg.DB.URL, "sslmode=disable") {
		logger.Warn("SECURITY: database connection uses sslmode=disable — use sslmode=require or sslmode=verify-full in production")
	}
	if !cfg.Sidecar.TLSEnabled {
		logger.Warn("SECURITY: sidecar gRPC TLS is disabled — set SIDECAR_TLS_ENABLED=true in production")
	}
	if cfg.Tracing.Enabled && cfg.Tracing.Insecure {
		logger.Warn("SECURITY: OTLP tracing uses plaintext gRPC — set OTEL_EXPORTER_OTLP_INSECURE=false in production")
	}
	if cfg.Server.MetricsAuthUser == "" {
		logger.Warn("SECURITY: /metrics endpoint has no authentication — set METRICS_AUTH_USER and METRICS_AUTH_PASS in production")
	}
	if cfg.Server.AdminAddr != "" && cfg.Server.AdminAuthUser == "" && cfg.Server.AdminAuthToken == "" {
		if cfg.Server.AdminRequireAuth {
			return fmt.Errorf("admin API requires authentication but neither ADMIN_AUTH_USER nor ADMIN_AUTH_TOKEN is set — configure one or set ADMIN_REQUIRE_AUTH=false")
		}
		logger.Warn("SECURITY: admin API has no authentication — set ADMIN_AUTH_TOKEN (bearer) or ADMIN_AUTH_USER/ADMIN_AUTH_PASS (basic) in production")
	}
	return nil
}

func maskCredentials(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	// Mask user:pass in URLs like postgres://user:pass@host/db
	if idx := strings.Index(rawURL, "@"); idx > 0 {
		schemeEnd := strings.Index(rawURL, "://")
		if schemeEnd < 0 {
			return rawURL
		}
		scheme := rawURL[:schemeEnd+3]
		return scheme + "***@" + rawURL[idx+1:]
	}
	// Mask API keys in RPC URLs like https://eth-mainnet.g.alchemy.com/v2/KEY
	// or query-string keys like ?apikey=KEY
	if strings.Contains(rawURL, "/v2/") || strings.Contains(rawURL, "/v3/") ||
		strings.Contains(rawURL, "apikey=") || strings.Contains(rawURL, "api_key=") {
		schemeEnd := strings.Index(rawURL, "://")
		if schemeEnd < 0 {
			return "***"
		}
		hostEnd := strings.Index(rawURL[schemeEnd+3:], "/")
		if hostEnd < 0 {
			return rawURL
		}
		return rawURL[:schemeEnd+3+hostEnd] + "/***"
	}
	return rawURL
}

func basicAuthMiddleware(user, pass string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="metrics"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type healthChecker struct {
	db        *sql.DB
	pipelines []*pipeline.Pipeline
}

func (h *healthChecker) check(ctx context.Context) error {
	if h.db == nil {
		return fmt.Errorf("database not initialized")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := h.db.PingContext(checkCtx); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	for _, p := range h.pipelines {
		snap := p.Health().Snapshot()
		if snap.Status == string(pipeline.HealthStatusUnhealthy) {
			return fmt.Errorf("pipeline %s/%s: unhealthy (%d consecutive failures)", snap.Chain, snap.Network, snap.ConsecutiveFailures)
		}
	}
	return nil
}

func startDBPoolStatsPump(ctx context.Context, db dbStatsProvider, targets []runtimeTarget, intervalMS int, logger *slog.Logger) {
	if db == nil || len(targets) == 0 || intervalMS <= 0 {
		return
	}

	gauges := dbPoolStatsGauges{
		open:         metrics.DBPoolOpen,
		inUse:        metrics.DBPoolInUse,
		idle:         metrics.DBPoolIdle,
		waitCount:    metrics.DBPoolWaitCount,
		waitDuration: metrics.DBPoolWaitDurationSeconds,
	}

	interval := time.Duration(intervalMS) * time.Millisecond
	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()

		if err := collectDBPoolStats(db, targets, gauges); err != nil {
			logger.Warn("failed to collect initial db pool stats", "error", err)
		}

		for {
			select {
			case <-ctx.Done():
				logger.Info("db pool stats sampler stopped", "cause", "context_done")
				return
			case <-ticker.C:
				if err := collectDBPoolStats(db, targets, gauges); err != nil {
					logger.Warn("failed to collect db pool stats", "error", err)
				}
			}
		}
	}()
}

// startDBPoolExhaustionAlert runs a background goroutine that checks db.Stats()
// every 30 seconds and sends an alert if pool usage exceeds the given threshold.
// If MaxOpenConnections is 0 (unlimited), the check is skipped.
func startDBPoolExhaustionAlert(ctx context.Context, db dbStatsProvider, alerter alert.Alerter, threshold float64, logger *slog.Logger) {
	if db == nil || alerter == nil {
		return
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := db.Stats()
				if stats.MaxOpenConnections <= 0 {
					// Unlimited pool; skip the check.
					continue
				}
				usage := float64(stats.InUse) / float64(stats.MaxOpenConnections)
				if usage > threshold {
					logger.Warn("DB connection pool near exhaustion",
						"in_use", stats.InUse,
						"max_open", stats.MaxOpenConnections,
						"usage_pct", fmt.Sprintf("%.0f%%", usage*100),
					)
					if err := alerter.Send(ctx, alert.Alert{
						Type:    alert.AlertTypeDBPool,
						Title:   "DB connection pool near exhaustion",
						Message: fmt.Sprintf("Pool usage: %d/%d (%.0f%%)", stats.InUse, stats.MaxOpenConnections, usage*100),
					}); err != nil {
						logger.Warn("failed to send DB pool alert", "error", err)
					}
				}
			}
		}
	}()
}

// rateLimitSetter is implemented by adapters that support rate limiting.
type rateLimitSetter interface {
	SetRateLimiter(l *ratelimit.Limiter)
}

func applyRateLimit(adapter chain.ChainAdapter, rlCfg config.RPCRateLimitConfig, chainName string) {
	if rlCfg.RPS <= 0 {
		return
	}
	if setter, ok := adapter.(rateLimitSetter); ok {
		setter.SetRateLimiter(ratelimit.NewLimiter(rlCfg.RPS, rlCfg.Burst, chainName))
	}
}

func buildRuntimeTargets(cfg *config.Config, logger *slog.Logger) []runtimeTarget {
	var solanaOpts []solana.AdapterOption
	if cfg.Solana.MaxPageSize > 0 {
		solanaOpts = append(solanaOpts, solana.WithMaxPageSize(cfg.Solana.MaxPageSize))
	}
	if cfg.Solana.MaxConcurrentTxs > 0 {
		solanaOpts = append(solanaOpts, solana.WithMaxConcurrentTxs(cfg.Solana.MaxConcurrentTxs))
	}
	solanaOpts = append(solanaOpts, solana.WithNetwork(cfg.Solana.Network))
	solanaAdapter := solana.NewAdapter(cfg.Solana.RPCURL, logger, solanaOpts...)
	applyRateLimit(solanaAdapter, cfg.Solana.RateLimit, "solana")

	var baseOpts []base.AdapterOption
	if cfg.Base.MaxInitialLookbackBlocks > 0 {
		baseOpts = append(baseOpts, base.WithMaxInitialLookbackBlocks(cfg.Base.MaxInitialLookbackBlocks))
	}
	if cfg.Base.MaxConcurrentTxs > 0 {
		baseOpts = append(baseOpts, base.WithMaxConcurrentTxs(cfg.Base.MaxConcurrentTxs))
	}
	baseAdapter := base.NewAdapter(cfg.Base.RPCURL, logger, baseOpts...)
	applyRateLimit(baseAdapter, cfg.Base.RateLimit, "base")

	var btcOpts []btc.AdapterOption
	if cfg.BTC.MaxInitialLookbackBlocks > 0 {
		btcOpts = append(btcOpts, btc.WithMaxInitialLookbackBlocks(cfg.BTC.MaxInitialLookbackBlocks))
	}
	btcAdapter := btc.NewAdapter(cfg.BTC.RPCURL, logger, btcOpts...)
	applyRateLimit(btcAdapter, cfg.BTC.RateLimit, "btc")

	targets := []runtimeTarget{
		{
			chain:   model.ChainSolana,
			network: model.Network(cfg.Solana.Network),
			group:   config.RuntimeLikeGroupSolana,
			watched: cfg.Pipeline.SolanaWatchedAddresses,
			adapter: solanaAdapter,
			rpcURL:  cfg.Solana.RPCURL,
		},
		{
			chain:   model.ChainBase,
			network: model.Network(cfg.Base.Network),
			group:   config.RuntimeLikeGroupEVM,
			watched: cfg.Pipeline.BaseWatchedAddresses,
			adapter: baseAdapter,
			rpcURL:  cfg.Base.RPCURL,
		},
		{
			chain:   model.ChainBTC,
			network: model.Network(cfg.BTC.Network),
			group:   config.RuntimeLikeGroupBTC,
			watched: cfg.Pipeline.BTCWatchedAddresses,
			adapter: btcAdapter,
			rpcURL:  cfg.BTC.RPCURL,
		},
	}

	if shouldBuildChainRuntimeTarget(cfg.Runtime.ChainTargets, "ethereum") {
		var ethOpts []base.AdapterOption
		if cfg.Ethereum.MaxInitialLookbackBlocks > 0 {
			ethOpts = append(ethOpts, base.WithMaxInitialLookbackBlocks(cfg.Ethereum.MaxInitialLookbackBlocks))
		}
		if cfg.Ethereum.MaxConcurrentTxs > 0 {
			ethOpts = append(ethOpts, base.WithMaxConcurrentTxs(cfg.Ethereum.MaxConcurrentTxs))
		}
		ethAdapter := ethereum.NewAdapter(cfg.Ethereum.RPCURL, logger, ethOpts...)
		applyRateLimit(ethAdapter, cfg.Ethereum.RateLimit, "ethereum")
		targets = append(targets, runtimeTarget{
			chain:   model.ChainEthereum,
			network: model.NetworkMainnet,
			group:   config.RuntimeLikeGroupEVM,
			watched: cfg.Pipeline.EthereumWatchedAddresses,
			adapter: ethAdapter,
			rpcURL:  cfg.Ethereum.RPCURL,
		})
	}

	if shouldBuildChainRuntimeTarget(cfg.Runtime.ChainTargets, "polygon") {
		var polyOpts []base.AdapterOption
		if cfg.Polygon.MaxInitialLookbackBlocks > 0 {
			polyOpts = append(polyOpts, base.WithMaxInitialLookbackBlocks(cfg.Polygon.MaxInitialLookbackBlocks))
		}
		if cfg.Polygon.MaxConcurrentTxs > 0 {
			polyOpts = append(polyOpts, base.WithMaxConcurrentTxs(cfg.Polygon.MaxConcurrentTxs))
		}
		polyAdapter := polygon.NewAdapter(cfg.Polygon.RPCURL, logger, polyOpts...)
		applyRateLimit(polyAdapter, cfg.Polygon.RateLimit, "polygon")
		targets = append(targets, runtimeTarget{
			chain:   model.ChainPolygon,
			network: model.Network(cfg.Polygon.Network),
			group:   config.RuntimeLikeGroupEVM,
			watched: cfg.Pipeline.PolygonWatchedAddresses,
			adapter: polyAdapter,
			rpcURL:  cfg.Polygon.RPCURL,
		})
	}

	if shouldBuildChainRuntimeTarget(cfg.Runtime.ChainTargets, "arbitrum") {
		var arbOpts []base.AdapterOption
		if cfg.Arbitrum.MaxInitialLookbackBlocks > 0 {
			arbOpts = append(arbOpts, base.WithMaxInitialLookbackBlocks(cfg.Arbitrum.MaxInitialLookbackBlocks))
		}
		if cfg.Arbitrum.MaxConcurrentTxs > 0 {
			arbOpts = append(arbOpts, base.WithMaxConcurrentTxs(cfg.Arbitrum.MaxConcurrentTxs))
		}
		arbAdapter := arbitrum.NewAdapter(cfg.Arbitrum.RPCURL, logger, arbOpts...)
		applyRateLimit(arbAdapter, cfg.Arbitrum.RateLimit, "arbitrum")
		targets = append(targets, runtimeTarget{
			chain:   model.ChainArbitrum,
			network: model.Network(cfg.Arbitrum.Network),
			group:   config.RuntimeLikeGroupEVM,
			watched: cfg.Pipeline.ArbitrumWatchedAddresses,
			adapter: arbAdapter,
			rpcURL:  cfg.Arbitrum.RPCURL,
		})
	}

	if shouldBuildChainRuntimeTarget(cfg.Runtime.ChainTargets, "bsc") {
		var bscOpts []base.AdapterOption
		if cfg.BSC.MaxInitialLookbackBlocks > 0 {
			bscOpts = append(bscOpts, base.WithMaxInitialLookbackBlocks(cfg.BSC.MaxInitialLookbackBlocks))
		}
		if cfg.BSC.MaxConcurrentTxs > 0 {
			bscOpts = append(bscOpts, base.WithMaxConcurrentTxs(cfg.BSC.MaxConcurrentTxs))
		}
		bscAdapter := bsc.NewAdapter(cfg.BSC.RPCURL, logger, bscOpts...)
		applyRateLimit(bscAdapter, cfg.BSC.RateLimit, "bsc")
		targets = append(targets, runtimeTarget{
			chain:   model.ChainBSC,
			network: model.Network(cfg.BSC.Network),
			group:   config.RuntimeLikeGroupEVM,
			watched: cfg.Pipeline.BSCWatchedAddresses,
			adapter: bscAdapter,
			rpcURL:  cfg.BSC.RPCURL,
		})
	}

	return targets
}

func shouldBuildChainRuntimeTarget(targets []string, chainName string) bool {
	for _, target := range targets {
		normalized := strings.ToLower(strings.TrimSpace(target))
		if strings.HasPrefix(normalized, chainName+"-") {
			return true
		}
	}
	return false
}

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func initLogger(cfg *config.Config) *slog.Logger {
	logLevel := slog.LevelInfo
	switch cfg.Log.Level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	logger.Info("starting multichain-indexer",
		"solana_rpc", maskCredentials(cfg.Solana.RPCURL),
		"solana_network", cfg.Solana.Network,
		"base_rpc", maskCredentials(cfg.Base.RPCURL),
		"base_network", cfg.Base.Network,
		"ethereum_rpc", maskCredentials(cfg.Ethereum.RPCURL),
		"ethereum_network", cfg.Ethereum.Network,
		"btc_rpc", maskCredentials(cfg.BTC.RPCURL),
		"btc_network", cfg.BTC.Network,
		"polygon_rpc", maskCredentials(cfg.Polygon.RPCURL),
		"polygon_network", cfg.Polygon.Network,
		"arbitrum_rpc", maskCredentials(cfg.Arbitrum.RPCURL),
		"arbitrum_network", cfg.Arbitrum.Network,
		"bsc_rpc", maskCredentials(cfg.BSC.RPCURL),
		"bsc_network", cfg.BSC.Network,
		"sidecar_addr", cfg.Sidecar.Addr,
		"solana_watched_addresses", len(cfg.Pipeline.SolanaWatchedAddresses),
		"base_watched_addresses", len(cfg.Pipeline.BaseWatchedAddresses),
		"ethereum_watched_addresses", len(cfg.Pipeline.EthereumWatchedAddresses),
		"btc_watched_addresses", len(cfg.Pipeline.BTCWatchedAddresses),
		"polygon_watched_addresses", len(cfg.Pipeline.PolygonWatchedAddresses),
		"arbitrum_watched_addresses", len(cfg.Pipeline.ArbitrumWatchedAddresses),
		"bsc_watched_addresses", len(cfg.Pipeline.BSCWatchedAddresses),
	)
	return logger
}

func initTracing(cfg *config.Config, logger *slog.Logger) (func(context.Context) error, error) {
	tracingEndpoint := ""
	if cfg.Tracing.Enabled {
		tracingEndpoint = cfg.Tracing.Endpoint
	}
	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer initCancel()
	shutdown, err := tracing.Init(initCtx, "multichain-indexer", tracingEndpoint, cfg.Tracing.Insecure, cfg.Tracing.SampleRatio)
	if err != nil {
		return nil, fmt.Errorf("initialize tracing: %w", err)
	}
	if cfg.Tracing.Enabled {
		logger.Info("tracing enabled", "endpoint", cfg.Tracing.Endpoint)
	}
	return shutdown, nil
}

func initDatabase(cfg *config.Config, logger *slog.Logger) (*postgres.DB, error) {
	db, err := postgres.New(postgres.Config{
		URL:             cfg.DB.URL,
		MaxOpenConns:    cfg.DB.MaxOpenConns,
		MaxIdleConns:    cfg.DB.MaxIdleConns,
		ConnMaxLifetime: cfg.DB.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.DB.ConnMaxIdleTime,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	logger.Info("connected to database")
	return db, nil
}

func initRepositories(db *postgres.DB) *pipeline.Repos {
	return &pipeline.Repos{
		WatchedAddr:   postgres.NewWatchedAddressRepo(db),
		Transaction:   postgres.NewTransactionRepo(db),
		BalanceEvent:  postgres.NewBalanceEventRepo(db),
		Balance:       postgres.NewBalanceRepo(db),
		Token:         postgres.NewTokenRepo(db),
		Config:        postgres.NewIndexerConfigRepo(db),
		RuntimeConfig: postgres.NewRuntimeConfigRepo(db),
		IndexedBlock:  postgres.NewIndexedBlockRepo(db.DB),
	}
}

func buildPipelineConfig(
	cfg *config.Config,
	target runtimeTarget,
	alerter alert.Alerter,
	commitInterleaver ingester.CommitInterleaver,
) pipeline.Config {
	return pipeline.Config{
		Chain:                      target.chain,
		Network:                    target.network,
		BatchSize:                  resolveBlockScanBatchSize(cfg, target.chain),
		IndexingInterval:           time.Duration(cfg.Pipeline.IndexingIntervalMs) * time.Millisecond,
		ReorgDetectorMaxCheckDepth: cfg.ReorgDetector.MaxCheckDepth,
		Alerter:                    alerter,
		CoordinatorAutoTune: pipeline.CoordinatorAutoTuneConfig{
			Enabled:                    cfg.Pipeline.CoordinatorAutoTuneEnabled,
			MinBatchSize:               cfg.Pipeline.CoordinatorAutoTuneMinBatchSize,
			MaxBatchSize:               cfg.Pipeline.CoordinatorAutoTuneMaxBatchSize,
			StepUp:                     cfg.Pipeline.CoordinatorAutoTuneStepUp,
			StepDown:                   cfg.Pipeline.CoordinatorAutoTuneStepDown,
			LagHighWatermark:           cfg.Pipeline.CoordinatorAutoTuneLagHighWatermark,
			LagLowWatermark:            cfg.Pipeline.CoordinatorAutoTuneLagLowWatermark,
			QueueHighWatermarkPct:      cfg.Pipeline.CoordinatorAutoTuneQueueHighPct,
			QueueLowWatermarkPct:       cfg.Pipeline.CoordinatorAutoTuneQueueLowPct,
			HysteresisTicks:            cfg.Pipeline.CoordinatorAutoTuneHysteresisTicks,
			TelemetryStaleTicks:        cfg.Pipeline.CoordinatorAutoTuneTelemetryStaleTicks,
			TelemetryRecoveryTicks:     cfg.Pipeline.CoordinatorAutoTuneTelemetryRecoveryTicks,
			OperatorOverrideBatch:      cfg.Pipeline.CoordinatorAutoTuneOperatorOverrideBatch,
			OperatorReleaseHoldTicks:   cfg.Pipeline.CoordinatorAutoTuneOperatorReleaseTicks,
			PolicyVersion:              cfg.Pipeline.CoordinatorAutoTunePolicyVersion,
			PolicyManifestDigest:       cfg.Pipeline.CoordinatorAutoTunePolicyManifestDigest,
			PolicyManifestRefreshEpoch: cfg.Pipeline.CoordinatorAutoTunePolicyManifestRefreshEpoch,
			PolicyActivationHoldTicks:  cfg.Pipeline.CoordinatorAutoTunePolicyActivationHoldTicks,
		},
		FetchWorkers:           cfg.Pipeline.FetchWorkers,
		NormalizerWorkers:      cfg.Pipeline.NormalizerWorkers,
		ChannelBufferSize:      cfg.Pipeline.ChannelBufferSize,
		JobChBufferSize:        cfg.Pipeline.JobChBufferSize,
		RawBatchChBufferSize:   cfg.Pipeline.RawBatchChBufferSize,
		NormalizedChBufferSize: cfg.Pipeline.NormalizedChBufferSize,
		SidecarAddr:            cfg.Sidecar.Addr,
		SidecarTimeout:         cfg.Sidecar.Timeout,
		SidecarMaxMsgSizeMB:   cfg.Sidecar.MaxMsgSizeMB,
		SidecarTLSEnabled:      cfg.Sidecar.TLSEnabled,
		SidecarTLSCert:         cfg.Sidecar.TLSCert,
		SidecarTLSKey:          cfg.Sidecar.TLSKey,
		SidecarTLSCA:           cfg.Sidecar.TLSCA,
		CommitInterleaver:      commitInterleaver,
		ReorgDetectorInterval:  time.Duration(cfg.Pipeline.ReorgDetectorIntervalMs) * time.Millisecond,
		FinalizerInterval:      time.Duration(cfg.Pipeline.FinalizerIntervalMs) * time.Millisecond,
		IndexedBlocksRetention: int64(cfg.Pipeline.IndexedBlocksRetention),
		AddressIndex:               cfg.Pipeline.AddressIndex,
		Fetcher:                    cfg.Pipeline.Fetcher,
		Normalizer:                 cfg.Pipeline.Normalizer,
		Ingester:                   cfg.Pipeline.Ingester,
		Health:                     cfg.Pipeline.Health,
		ConfigWatcher:              cfg.Pipeline.ConfigWatcher,
		MaxInitialLookbackBlocks:   resolveMaxInitialLookbackBlocks(cfg, target.chain),
	}
}

func resolveMaxInitialLookbackBlocks(cfg *config.Config, ch model.Chain) int {
	switch ch {
	case model.ChainSolana:
		return cfg.Solana.MaxInitialLookbackBlocks
	case model.ChainBase:
		return cfg.Base.MaxInitialLookbackBlocks
	case model.ChainEthereum:
		return cfg.Ethereum.MaxInitialLookbackBlocks
	case model.ChainBTC:
		return cfg.BTC.MaxInitialLookbackBlocks
	case model.ChainPolygon:
		return cfg.Polygon.MaxInitialLookbackBlocks
	case model.ChainArbitrum:
		return cfg.Arbitrum.MaxInitialLookbackBlocks
	case model.ChainBSC:
		return cfg.BSC.MaxInitialLookbackBlocks
	default:
		return 0
	}
}

// resolveBlockScanBatchSize returns a chain-specific block-scan batch size
// when configured, falling back to the global Pipeline.BatchSize.
// BTC defaults to 3 blocks/tick because prevout resolution is extremely slow.
func resolveBlockScanBatchSize(cfg *config.Config, ch model.Chain) int {
	switch ch {
	case model.ChainBTC:
		if cfg.BTC.BlockScanBatchSize > 0 {
			return cfg.BTC.BlockScanBatchSize
		}
	case model.ChainSolana:
		if cfg.Solana.BlockScanBatchSize > 0 {
			return cfg.Solana.BlockScanBatchSize
		}
	}
	return cfg.Pipeline.BatchSize
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := initLogger(cfg)

	if err := logSecurityWarnings(cfg, logger); err != nil {
		return err
	}

	shutdownTracing, err := initTracing(cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			logger.Warn("tracing shutdown error", "error", err)
		}
	}()

	db, err := initDatabase(cfg, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	repos := initRepositories(db)

	allTargets := buildRuntimeTargets(cfg, logger)
	targets, err := selectRuntimeTargets(allTargets, cfg.Runtime)
	if err != nil {
		return fmt.Errorf("select runtime targets: %w", err)
	}
	if err := validateRuntimeWiring(targets); err != nil {
		return fmt.Errorf("runtime wiring preflight: %w", err)
	}
	logger.Info("runtime targets selected",
		"deployment_mode", cfg.Runtime.DeploymentMode,
		"like_group", cfg.Runtime.LikeGroup,
		"chain_targets", strings.Join(cfg.Runtime.ChainTargets, ","),
		"selected_targets", strings.Join(runtimeTargetKeys(targets), ","),
	)

	for _, target := range targets {
		if err := syncWatchedAddresses(context.Background(), repos.WatchedAddr, target.chain, target.network, target.watched); err != nil {
			return fmt.Errorf("sync watched addresses %s/%s: %w", target.chain, target.network, err)
		}
	}

	alerter := buildAlerter(cfg, logger)
	replayService := replay.NewService(db, repos.Balance, repos.Config, repos.IndexedBlock, logger)

	registry := pipeline.NewRegistry()
	pipelines := make([]*pipeline.Pipeline, 0, len(targets))
	commitInterleaver := ingester.NewDeterministicMandatoryChainInterleaver(deterministicInterleaveMaxSkew)
	for _, target := range targets {
		pipelineCfg := buildPipelineConfig(cfg, target, alerter, commitInterleaver)
		p := pipeline.New(pipelineCfg, target.adapter, db, repos, logger.With("chain", target.chain, "network", target.network, "rpc", target.rpcURL))
		p.SetReplayService(replayService)
		registry.Register(p)
		pipelines = append(pipelines, p)
	}

	// Context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	g, gCtx := errgroup.WithContext(ctx)

	// Health check server
	checker := &healthChecker{db: db.DB, pipelines: pipelines}
	g.Go(func() error {
		return runHealthServer(gCtx, cfg.Server.HealthPort, cfg.Server.MetricsAuthUser, cfg.Server.MetricsAuthPass, checker, logger)
	})

	// Reconciliation service
	reconService := reconciliation.NewService(
		db.DB, repos.Balance, repos.WatchedAddr, repos.Token, alerter, logger,
	)
	reconService.SetSnapshotRepository(postgres.NewReconciliationSnapshotRepo(db.DB))
	for _, target := range targets {
		if bqa, ok := target.adapter.(chain.BalanceQueryAdapter); ok {
			reconService.RegisterAdapter(target.chain, target.network, bqa)
		}
	}
	if cfg.Reconciliation.IntervalMS > 0 {
		reconInterval := time.Duration(cfg.Reconciliation.IntervalMS) * time.Millisecond
		g.Go(func() error {
			return reconService.RunPeriodic(gCtx, reconInterval)
		})
	}

	// Admin API server (optional)
	if cfg.Server.AdminAddr != "" {
		replayAdapter := pipeline.NewRegistryReplayAdapter(registry, replayService, repos.Config)
		healthAdapter := pipeline.NewRegistryHealthAdapter(registry)
		addressBookRepo := postgres.NewAddressBookRepo(db.DB)
		dashboardRepo := postgres.NewDashboardRepo(db.DB)
		adminSrv := admin.NewServer(repos.WatchedAddr, repos.Config, logger,
			admin.WithReplayRequester(replayAdapter),
			admin.WithHealthProvider(healthAdapter),
			admin.WithReconcileRequester(reconService),
			admin.WithAddressBookRepo(addressBookRepo),
			admin.WithDashboardRepo(dashboardRepo),
			admin.WithAuthToken(cfg.Server.AdminAuthToken),
		)
		var adminHandler http.Handler = adminSrv.Handler()
		adminHandler = admin.AuditMiddleware(logger, adminHandler)
		if cfg.Server.AdminRateLimitEnabled {
			adminHandler = admin.NewRateLimitMiddleware(logger).Wrap(adminHandler)
		}
		if cfg.Server.AdminAuthUser != "" && cfg.Server.AdminAuthPass != "" {
			adminHandler = basicAuthMiddleware(cfg.Server.AdminAuthUser, cfg.Server.AdminAuthPass, adminHandler)
		}
		g.Go(func() error {
			return runAdminServer(gCtx, cfg.Server.AdminAddr, adminHandler, logger)
		})
	}

	// Pipelines — each runs independently so one crash does not kill others.
	var pipelineWg sync.WaitGroup
	for _, p := range pipelines {
		p := p
		pipelineWg.Add(1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("pipeline panicked",
						"chain", p.Chain(), "network", p.Network(),
						"panic", fmt.Sprintf("%v", r),
						"stack", string(debug.Stack()))
				}
				pipelineWg.Done()
			}()
			if err := p.Run(gCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("pipeline exited with error",
					"chain", p.Chain(), "network", p.Network(), "error", err)
			}
		}()
	}

	startDBPoolStatsPump(gCtx, db.DB, targets, cfg.DB.PoolStatsIntervalMS, logger)
	startDBPoolExhaustionAlert(gCtx, db.DB, alerter, cfg.DB.PoolExhaustionThreshold, logger)

	// Signal handler
	g.Go(func() error {
		select {
		case sig := <-sigCh:
			logger.Info("received signal, shutting down", "signal", sig)
			cancel()
			return nil
		case <-gCtx.Done():
			return nil
		}
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("indexer exited: %w", err)
	}

	// Wait for pipelines with a timeout.
	done := make(chan struct{})
	go func() {
		pipelineWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.Info("all pipelines shut down gracefully")
	case <-time.After(time.Duration(cfg.Pipeline.ShutdownTimeoutSec) * time.Second):
		logger.Error("pipeline shutdown timed out, forcing exit",
			"timeout_sec", cfg.Pipeline.ShutdownTimeoutSec)
		os.Exit(1)
	}

	logger.Info("indexer shut down gracefully")
	return nil
}

func validateRuntimeWiring(targets []runtimeTarget) error {
	if len(targets) == 0 {
		return fmt.Errorf("runtime wiring preflight failed: no runtime targets selected")
	}

	targetsByKey := make(map[string]runtimeTarget, len(targets))
	failures := make([]string, 0)
	for _, target := range targets {
		key := runtimeTargetKey(target.chain, target.network)
		if _, exists := targetsByKey[key]; exists {
			failures = append(failures, fmt.Sprintf("duplicate target %s", key))
			continue
		}
		targetsByKey[key] = target

		expectedGroup := runtimeLikeGroupForChain(target.chain)
		if expectedGroup == "" {
			failures = append(failures, fmt.Sprintf("unsupported chain for target %s", key))
			continue
		}
		if target.group == "" {
			failures = append(failures, fmt.Sprintf("missing runtime group for target %s", key))
		}
		if target.group != expectedGroup {
			failures = append(failures, fmt.Sprintf("runtime group mismatch for %s (expected=%s got=%s)", key, expectedGroup, target.group))
		}

		if target.adapter == nil {
			failures = append(failures, fmt.Sprintf("nil adapter for target %s", key))
			continue
		}
		adapterChain := target.adapter.Chain()
		expectedChain := target.chain.String()
		if adapterChain != expectedChain {
			failures = append(failures, fmt.Sprintf("adapter mismatch for %s (expected=%s got=%s)", key, expectedChain, adapterChain))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("runtime wiring preflight failed: %s", strings.Join(failures, "; "))
	}

	return nil
}

func runtimeLikeGroupForChain(chain model.Chain) string {
	switch chain {
	case model.ChainSolana:
		return config.RuntimeLikeGroupSolana
	case model.ChainBase, model.ChainEthereum, model.ChainPolygon, model.ChainArbitrum, model.ChainBSC:
		return config.RuntimeLikeGroupEVM
	case model.ChainBTC:
		return config.RuntimeLikeGroupBTC
	default:
		return ""
	}
}

func selectRuntimeTargets(all []runtimeTarget, runtimeCfg config.RuntimeConfig) ([]runtimeTarget, error) {
	switch runtimeCfg.DeploymentMode {
	case config.RuntimeDeploymentModeLikeGroup:
		selected := append([]runtimeTarget(nil), all...)
		if runtimeCfg.LikeGroup != "" {
			filtered := make([]runtimeTarget, 0, len(selected))
			for _, target := range selected {
				if target.group == runtimeCfg.LikeGroup {
					filtered = append(filtered, target)
				}
			}
			selected = filtered
		}
		if len(runtimeCfg.ChainTargets) > 0 {
			filtered, missing := filterRuntimeTargetsByKeys(selected, runtimeCfg.ChainTargets)
			if len(missing) > 0 {
				return nil, fmt.Errorf("requested runtime chain targets not found: %s", strings.Join(missing, ","))
			}
			selected = filtered
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("no runtime targets selected for mode=%s", runtimeCfg.DeploymentMode)
		}
		return selected, nil

	case config.RuntimeDeploymentModeIndependent:
		filtered, missing := filterRuntimeTargetsByKeys(all, runtimeCfg.ChainTargets)
		if len(missing) > 0 {
			return nil, fmt.Errorf("requested runtime chain targets not found: %s", strings.Join(missing, ","))
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("no runtime targets selected for mode=%s", runtimeCfg.DeploymentMode)
		}
		return filtered, nil

	default:
		return nil, fmt.Errorf("unsupported runtime deployment mode: %s", runtimeCfg.DeploymentMode)
	}
}

func filterRuntimeTargetsByKeys(targets []runtimeTarget, keys []string) ([]runtimeTarget, []string) {
	if len(keys) == 0 {
		return nil, nil
	}

	requested := normalizeRuntimeTargetKeys(keys)
	requestedSet := make(map[string]struct{}, len(requested))
	for _, key := range requested {
		requestedSet[key] = struct{}{}
	}

	filtered := make([]runtimeTarget, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		key := runtimeTargetKey(target.chain, target.network)
		if _, want := requestedSet[key]; want {
			filtered = append(filtered, target)
			seen[key] = struct{}{}
		}
	}

	missing := make([]string, 0)
	for _, key := range requested {
		if _, exists := seen[key]; !exists {
			missing = append(missing, key)
		}
	}
	return filtered, missing
}

func normalizeRuntimeTargetKeys(keys []string) []string {
	normalized := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		norm := strings.ToLower(strings.TrimSpace(key))
		if norm == "" {
			continue
		}
		if _, exists := seen[norm]; exists {
			continue
		}
		seen[norm] = struct{}{}
		normalized = append(normalized, norm)
	}
	return normalized
}

func runtimeTargetKeys(targets []runtimeTarget) []string {
	keys := make([]string, 0, len(targets))
	for _, target := range targets {
		keys = append(keys, runtimeTargetKey(target.chain, target.network))
	}
	return keys
}

func runtimeTargetKey(chain model.Chain, network model.Network) string {
	return string(chain) + "-" + string(network)
}

func syncWatchedAddresses(
	ctx context.Context,
	repo store.WatchedAddressRepository,
	chain model.Chain,
	network model.Network,
	addresses []string,
) error {
	for _, addr := range addresses {
		wa := &model.WatchedAddress{
			Chain:    chain,
			Network:  network,
			Address:  addr,
			IsActive: true,
			Source:   model.AddressSourceEnv,
		}
		if err := repo.Upsert(ctx, wa); err != nil {
			return fmt.Errorf("upsert watched address %s: %w", addr, err)
		}
	}
	slog.Info("synced watched addresses from env", "chain", chain, "network", network, "count", len(addresses))
	return nil
}

func runHealthServer(ctx context.Context, port int, metricsUser, metricsPass string, checker *healthChecker, logger *slog.Logger) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			logger.Warn("failed to write health response", "error", err)
		}
	})
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			logger.Warn("failed to write livez response", "error", err)
		}
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := checker.check(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, wErr := w.Write([]byte("not ready: " + err.Error())); wErr != nil {
				logger.Warn("failed to write readyz response", "error", wErr)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ready")); err != nil {
			logger.Warn("failed to write readyz response", "error", err)
		}
	})
	metricsHandler := promhttp.Handler()
	if metricsUser != "" && metricsPass != "" {
		metricsHandler = basicAuthMiddleware(metricsUser, metricsPass, metricsHandler)
	}
	mux.Handle("/metrics", metricsHandler)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && err != http.ErrServerClosed {
			logger.Warn("health server shutdown error", "error", err)
		}
	}()

	logger.Info("health server started", "port", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("health server: %w", err)
	}
	return nil
}

func buildAlerter(cfg *config.Config, logger *slog.Logger) alert.Alerter {
	var alerters []alert.Alerter

	if cfg.Alert.SlackWebhookURL != "" {
		alerters = append(alerters, alert.NewSlackAlerter(cfg.Alert.SlackWebhookURL))
		logger.Info("slack alerter enabled")
	}
	if cfg.Alert.WebhookURL != "" {
		alerters = append(alerters, alert.NewWebhookAlerter(cfg.Alert.WebhookURL))
		logger.Info("webhook alerter enabled")
	}

	if len(alerters) == 0 {
		return &alert.NoopAlerter{}
	}

	cooldown := time.Duration(cfg.Alert.CooldownMS) * time.Millisecond
	return alert.NewMultiAlerter(cooldown, logger, alerters...)
}

func runAdminServer(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && err != http.ErrServerClosed {
			logger.Warn("admin server shutdown error", "error", err)
		}
	}()

	logger.Info("admin server started", "addr", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("admin server: %w", err)
	}
	return nil
}
