package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/arbitrum"
	"github.com/emperorhan/multichain-indexer/internal/chain/base"
	"github.com/emperorhan/multichain-indexer/internal/chain/bsc"
	"github.com/emperorhan/multichain-indexer/internal/chain/btc"
	"github.com/emperorhan/multichain-indexer/internal/chain/ethereum"
	"github.com/emperorhan/multichain-indexer/internal/chain/polygon"
	"github.com/emperorhan/multichain-indexer/internal/chain/solana"
	"github.com/emperorhan/multichain-indexer/internal/config"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/pipeline"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/ingester"
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
		cfg.Redis.URL,
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

func buildRuntimeTargets(cfg *config.Config, logger *slog.Logger) []runtimeTarget {
	targets := []runtimeTarget{
		{
			chain:   model.ChainSolana,
			network: model.Network(cfg.Solana.Network),
			group:   config.RuntimeLikeGroupSolana,
			watched: cfg.Pipeline.SolanaWatchedAddresses,
			adapter: solana.NewAdapter(cfg.Solana.RPCURL, logger),
			rpcURL:  cfg.Solana.RPCURL,
		},
		{
			chain:   model.ChainBase,
			network: model.Network(cfg.Base.Network),
			group:   config.RuntimeLikeGroupEVM,
			watched: cfg.Pipeline.BaseWatchedAddresses,
			adapter: base.NewAdapter(cfg.Base.RPCURL, logger),
			rpcURL:  cfg.Base.RPCURL,
		},
		{
			chain:   model.ChainBTC,
			network: model.Network(cfg.BTC.Network),
			group:   config.RuntimeLikeGroupBTC,
			watched: cfg.Pipeline.BTCWatchedAddresses,
			adapter: btc.NewAdapter(cfg.BTC.RPCURL, logger),
			rpcURL:  cfg.BTC.RPCURL,
		},
	}

	if shouldBuildChainRuntimeTarget(cfg.Runtime.ChainTargets, "ethereum") {
		targets = append(targets, runtimeTarget{
			chain:   model.ChainEthereum,
			network: model.NetworkMainnet,
			group:   config.RuntimeLikeGroupEVM,
			watched: cfg.Pipeline.EthereumWatchedAddresses,
			adapter: ethereum.NewAdapter(cfg.Ethereum.RPCURL, logger),
			rpcURL:  cfg.Ethereum.RPCURL,
		})
	}

	if shouldBuildChainRuntimeTarget(cfg.Runtime.ChainTargets, "polygon") {
		targets = append(targets, runtimeTarget{
			chain:   model.ChainPolygon,
			network: model.Network(cfg.Polygon.Network),
			group:   config.RuntimeLikeGroupEVM,
			watched: cfg.Pipeline.PolygonWatchedAddresses,
			adapter: polygon.NewAdapter(cfg.Polygon.RPCURL, logger),
			rpcURL:  cfg.Polygon.RPCURL,
		})
	}

	if shouldBuildChainRuntimeTarget(cfg.Runtime.ChainTargets, "arbitrum") {
		targets = append(targets, runtimeTarget{
			chain:   model.ChainArbitrum,
			network: model.Network(cfg.Arbitrum.Network),
			group:   config.RuntimeLikeGroupEVM,
			watched: cfg.Pipeline.ArbitrumWatchedAddresses,
			adapter: arbitrum.NewAdapter(cfg.Arbitrum.RPCURL, logger),
			rpcURL:  cfg.Arbitrum.RPCURL,
		})
	}

	if shouldBuildChainRuntimeTarget(cfg.Runtime.ChainTargets, "bsc") {
		targets = append(targets, runtimeTarget{
			chain:   model.ChainBSC,
			network: model.Network(cfg.BSC.Network),
			group:   config.RuntimeLikeGroupEVM,
			watched: cfg.Pipeline.BSCWatchedAddresses,
			adapter: bsc.NewAdapter(cfg.BSC.RPCURL, logger),
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
		"solana_rpc", cfg.Solana.RPCURL,
		"solana_network", cfg.Solana.Network,
		"base_rpc", cfg.Base.RPCURL,
		"base_network", cfg.Base.Network,
		"ethereum_rpc", cfg.Ethereum.RPCURL,
		"ethereum_network", cfg.Ethereum.Network,
		"btc_rpc", cfg.BTC.RPCURL,
		"btc_network", cfg.BTC.Network,
		"polygon_rpc", cfg.Polygon.RPCURL,
		"polygon_network", cfg.Polygon.Network,
		"arbitrum_rpc", cfg.Arbitrum.RPCURL,
		"arbitrum_network", cfg.Arbitrum.Network,
		"bsc_rpc", cfg.BSC.RPCURL,
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

	// Initialize OpenTelemetry tracing
	tracingEndpoint := ""
	if cfg.Tracing.Enabled {
		tracingEndpoint = cfg.Tracing.Endpoint
	}
	shutdownTracing, err := tracing.Init(context.Background(), "multichain-indexer", tracingEndpoint, cfg.Tracing.Insecure)
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
		logger.Error("failed to initialize stream transport", "error", err, "redis_url", cfg.Redis.URL)
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

	pipelines := make([]*pipeline.Pipeline, 0, len(targets))
	commitInterleaver := ingester.NewDeterministicMandatoryChainInterleaver(deterministicInterleaveMaxSkew)
	for _, target := range targets {
		pipelineCfg := pipeline.Config{
			Chain:            target.chain,
			Network:          target.network,
			BatchSize:        cfg.Pipeline.BatchSize,
			IndexingInterval: time.Duration(cfg.Pipeline.IndexingIntervalMs) * time.Millisecond,
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
		}
		pipelines = append(pipelines, pipeline.New(pipelineCfg, target.adapter, db, repos, logger.With("chain", target.chain, "network", target.network, "rpc", target.rpcURL)))
	}

	// Context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	g, gCtx := errgroup.WithContext(ctx)

	// Health check server
	g.Go(func() error {
		return runHealthServer(gCtx, cfg.Server.HealthPort, logger)
	})

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

func runHealthServer(ctx context.Context, port int, logger *slog.Logger) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			logger.Warn("failed to write health response", "error", err)
		}
	})
	mux.Handle("/metrics", promhttp.Handler())

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
