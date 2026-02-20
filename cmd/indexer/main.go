package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	redispkg "github.com/emperorhan/multichain-indexer/internal/store/redis"
	"github.com/emperorhan/multichain-indexer/internal/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

const deterministicInterleaveMaxSkew = 250 * time.Millisecond
const defaultStreamSessionID = "default"

var (
	newStreamFactory         = func(redisURL string) (redispkg.MessageTransport, error) { return redispkg.NewStream(redisURL) }
	newInMemoryStreamFactory = func() redispkg.MessageTransport { return redispkg.NewInMemoryStream() }
)

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

func resolveStreamBackend(cfg *config.Config, streamSessionID string, logger *slog.Logger) (redispkg.MessageTransport, bool, error) {
	if !cfg.Pipeline.StreamTransportEnabled {
		return newInMemoryStreamFactory(), false, nil
	}

	redisURL := strings.TrimSpace(cfg.Redis.URL)
	if redisURL == "" {
		return nil, true, fmt.Errorf("initialize redis stream transport: redis URL is empty")
	}

	redisStream, err := newStreamFactory(redisURL)
	if err != nil {
		return nil, true, fmt.Errorf("initialize redis stream transport: %w", err)
	}
	if redisStream == nil {
		return nil, true, fmt.Errorf("initialize redis stream transport: backend is nil")
	}

	logger.Info("redis stream transport enabled",
		"redis_url",
		maskCredentials(cfg.Redis.URL),
		"stream_namespace",
		cfg.Pipeline.StreamNamespace,
		"stream_session_id",
		streamSessionID,
	)

	return redisStream, true, nil
}

func resolveStreamSessionID(rawSessionID string) string {
	sessionID := strings.TrimSpace(rawSessionID)
	if sessionID == "" {
		return defaultStreamSessionID
	}
	return sessionID
}

func logSecurityWarnings(cfg *config.Config, logger *slog.Logger) {
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
	if cfg.Server.AdminAddr != "" && cfg.Server.AdminAuthUser == "" {
		if cfg.Server.AdminRequireAuth {
			logger.Error("SECURITY: admin API requires authentication but ADMIN_AUTH_USER is not set — set ADMIN_AUTH_USER and ADMIN_AUTH_PASS or set ADMIN_REQUIRE_AUTH=false")
			os.Exit(1)
		}
		logger.Warn("SECURITY: admin API has no authentication — set ADMIN_AUTH_USER and ADMIN_AUTH_PASS in production")
	}
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
	db *sql.DB
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
	// Setup logger
	logLevel := slog.LevelInfo
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

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

	logSecurityWarnings(cfg, logger)

	// Initialize OpenTelemetry tracing
	tracingEndpoint := ""
	if cfg.Tracing.Enabled {
		tracingEndpoint = cfg.Tracing.Endpoint
	}
	shutdownTracing, err := tracing.Init(context.Background(), "multichain-indexer", tracingEndpoint, cfg.Tracing.Insecure, cfg.Tracing.SampleRatio)
	if err != nil {
		logger.Error("failed to initialize tracing", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := shutdownTracing(context.Background()); err != nil {
			logger.Warn("tracing shutdown error", "error", err)
		}
	}()
	if cfg.Tracing.Enabled {
		logger.Info("tracing enabled", "endpoint", cfg.Tracing.Endpoint)
	}

	// Connect to PostgreSQL
	db, err := postgres.New(postgres.Config{
		URL:             cfg.DB.URL,
		MaxOpenConns:    cfg.DB.MaxOpenConns,
		MaxIdleConns:    cfg.DB.MaxIdleConns,
		ConnMaxLifetime: cfg.DB.ConnMaxLifetime,
	})
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	logger.Info("connected to database")

	streamSessionID := resolveStreamSessionID(cfg.Pipeline.StreamSessionID)

	streamBackend, streamTransportEnabled, err := resolveStreamBackend(cfg, streamSessionID, logger)
	if err != nil {
		logger.Error("failed to initialize stream transport", "error", err, "redis_url", maskCredentials(cfg.Redis.URL))
		os.Exit(1)
	}

	if streamBackend != nil {
		defer streamBackend.Close()
	}

	// Create repositories
	repos := &pipeline.Repos{
		WatchedAddr:   postgres.NewWatchedAddressRepo(db),
		Cursor:        postgres.NewCursorRepo(db),
		Transaction:   postgres.NewTransactionRepo(db),
		BalanceEvent:  postgres.NewBalanceEventRepo(db),
		Balance:       postgres.NewBalanceRepo(db),
		Token:         postgres.NewTokenRepo(db),
		Config:        postgres.NewIndexerConfigRepo(db),
		RuntimeConfig: postgres.NewRuntimeConfigRepo(db),
		IndexedBlock:  postgres.NewIndexedBlockRepo(db.DB),
	}

	allTargets := buildRuntimeTargets(cfg, logger)
	targets, err := selectRuntimeTargets(allTargets, cfg.Runtime)
	if err != nil {
		logger.Error("failed to select runtime targets", "error", err)
		os.Exit(1)
	}
	if err := validateRuntimeWiring(targets); err != nil {
		logger.Error("runtime wiring preflight failed", "error", err)
		os.Exit(1)
	}
	logger.Info("runtime targets selected",
		"deployment_mode", cfg.Runtime.DeploymentMode,
		"like_group", cfg.Runtime.LikeGroup,
		"chain_targets", strings.Join(cfg.Runtime.ChainTargets, ","),
		"selected_targets", strings.Join(runtimeTargetKeys(targets), ","),
	)

	for _, target := range targets {
		if err := syncWatchedAddresses(context.Background(), repos.WatchedAddr, repos.Cursor, target.chain, target.network, target.watched); err != nil {
			logger.Error("failed to sync watched addresses",
				"chain", target.chain,
				"network", target.network,
				"error", err,
			)
			os.Exit(1)
		}
	}

	// Build alerter from config (needed by pipeline + reconciliation)
	alerter := buildAlerter(cfg, logger)

	// Create shared replay service
	replayService := replay.NewService(db, repos.Balance, repos.Config, repos.IndexedBlock, logger)

	registry := pipeline.NewRegistry()
	pipelines := make([]*pipeline.Pipeline, 0, len(targets))
	commitInterleaver := ingester.NewDeterministicMandatoryChainInterleaver(deterministicInterleaveMaxSkew)
	for _, target := range targets {
		pipelineCfg := pipeline.Config{
			Chain:                      target.chain,
			Network:                    target.network,
			BatchSize:                  cfg.Pipeline.BatchSize,
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
			SidecarAddr:            cfg.Sidecar.Addr,
			SidecarTimeout:         cfg.Sidecar.Timeout,
			SidecarTLSEnabled:      cfg.Sidecar.TLSEnabled,
			SidecarTLSCert:         cfg.Sidecar.TLSCert,
			SidecarTLSKey:          cfg.Sidecar.TLSKey,
			SidecarTLSCA:           cfg.Sidecar.TLSCA,
			StreamTransportEnabled: streamTransportEnabled,
			StreamBackend:          streamBackend,
			StreamNamespace:        cfg.Pipeline.StreamNamespace,
			StreamSessionID:        streamSessionID,
			CommitInterleaver:      commitInterleaver,
			ReorgDetectorInterval:  time.Duration(cfg.Pipeline.ReorgDetectorIntervalMs) * time.Millisecond,
			FinalizerInterval:      time.Duration(cfg.Pipeline.FinalizerIntervalMs) * time.Millisecond,
			IndexedBlocksRetention: int64(cfg.Pipeline.IndexedBlocksRetention),
			AddressIndex:           cfg.Pipeline.AddressIndex,
			Fetcher:                cfg.Pipeline.Fetcher,
			Normalizer:             cfg.Pipeline.Normalizer,
			Ingester:               cfg.Pipeline.Ingester,
			Health:                 cfg.Pipeline.Health,
			ConfigWatcher:          cfg.Pipeline.ConfigWatcher,
		}
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
	checker := &healthChecker{db: db.DB}
	g.Go(func() error {
		return runHealthServer(gCtx, cfg.Server.HealthPort, cfg.Server.MetricsAuthUser, cfg.Server.MetricsAuthPass, checker, logger)
	})

	// Build reconciliation service
	reconService := reconciliation.NewService(
		db.DB, repos.Balance, repos.WatchedAddr, repos.Token, alerter, logger,
	)
	reconService.SetSnapshotRepository(postgres.NewReconciliationSnapshotRepo(db.DB))
	// Register balance query adapters for reconciliation
	for _, target := range targets {
		if bqa, ok := target.adapter.(chain.BalanceQueryAdapter); ok {
			reconService.RegisterAdapter(target.chain, target.network, bqa)
		}
	}

	// Start periodic reconciliation
	if cfg.Reconciliation.IntervalMS > 0 {
		reconInterval := time.Duration(cfg.Reconciliation.IntervalMS) * time.Millisecond
		g.Go(func() error {
			return reconService.RunPeriodic(gCtx, reconInterval)
		})
	}

	// Address book repo
	addressBookRepo := postgres.NewAddressBookRepo(db.DB)

	// Admin API server (optional)
	if cfg.Server.AdminAddr != "" {
		replayAdapter := pipeline.NewRegistryReplayAdapter(registry, replayService, repos.Config)
		healthAdapter := pipeline.NewRegistryHealthAdapter(registry)
		adminSrv := admin.NewServer(repos.WatchedAddr, repos.Config, logger,
			admin.WithReplayRequester(replayAdapter),
			admin.WithHealthProvider(healthAdapter),
			admin.WithReconcileRequester(reconService),
			admin.WithAddressBookRepo(addressBookRepo),
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

	// Pipelines
	for _, p := range pipelines {
		p := p
		g.Go(func() error {
			return p.Run(gCtx)
		})
	}

	startDBPoolStatsPump(gCtx, db.DB, targets, cfg.DB.PoolStatsIntervalMS, logger)

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

	if err := g.Wait(); err != nil && err != context.Canceled {
		logger.Error("indexer exited with error", "error", err)
		os.Exit(1)
	}

	logger.Info("indexer shut down gracefully")
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
	return fmt.Sprintf("%s-%s", chain, network)
}

func syncWatchedAddresses(
	ctx context.Context,
	repo store.WatchedAddressRepository,
	cursorRepo store.CursorRepository,
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
		// Ensure cursor exists
		if err := cursorRepo.EnsureExists(ctx, chain, network, addr); err != nil {
			return fmt.Errorf("ensure cursor for %s: %w", addr, err)
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
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := checker.check(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: " + err.Error()))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	metricsHandler := promhttp.Handler()
	if metricsUser != "" && metricsPass != "" {
		metricsHandler = basicAuthMiddleware(metricsUser, metricsPass, metricsHandler)
	}
	mux.Handle("/metrics", metricsHandler)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
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
		Addr:    addr,
		Handler: handler,
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
