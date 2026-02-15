package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/base"
	"github.com/emperorhan/multichain-indexer/internal/chain/solana"
	"github.com/emperorhan/multichain-indexer/internal/config"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/emperorhan/multichain-indexer/internal/store/postgres"
	"golang.org/x/sync/errgroup"
)

type runtimeTarget struct {
	chain   model.Chain
	network model.Network
	group   string
	watched []string
	adapter chain.ChainAdapter
	rpcURL  string
}

func buildRuntimeTargets(cfg *config.Config, logger *slog.Logger) []runtimeTarget {
	return []runtimeTarget{
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
	}
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
		"sidecar_addr", cfg.Sidecar.Addr,
		"solana_watched_addresses", len(cfg.Pipeline.SolanaWatchedAddresses),
		"base_watched_addresses", len(cfg.Pipeline.BaseWatchedAddresses),
	)

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

	// Create repositories
	repos := &pipeline.Repos{
		WatchedAddr:  postgres.NewWatchedAddressRepo(db),
		Cursor:       postgres.NewCursorRepo(db),
		Transaction:  postgres.NewTransactionRepo(db),
		BalanceEvent: postgres.NewBalanceEventRepo(db),
		Balance:      postgres.NewBalanceRepo(db),
		Token:        postgres.NewTokenRepo(db),
		Config:       postgres.NewIndexerConfigRepo(db),
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
	for _, target := range targets {
		pipelineCfg := pipeline.Config{
			Chain:             target.chain,
			Network:           target.network,
			BatchSize:         cfg.Pipeline.BatchSize,
			IndexingInterval:  time.Duration(cfg.Pipeline.IndexingIntervalMs) * time.Millisecond,
			FetchWorkers:      cfg.Pipeline.FetchWorkers,
			NormalizerWorkers: cfg.Pipeline.NormalizerWorkers,
			ChannelBufferSize: cfg.Pipeline.ChannelBufferSize,
			SidecarAddr:       cfg.Sidecar.Addr,
			SidecarTimeout:    cfg.Sidecar.Timeout,
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

	requested := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		requested[strings.TrimSpace(strings.ToLower(key))] = struct{}{}
	}

	filtered := make([]runtimeTarget, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		key := runtimeTargetKey(target.chain, target.network)
		if _, want := requested[key]; want {
			filtered = append(filtered, target)
			seen[key] = struct{}{}
		}
	}

	missing := make([]string, 0)
	for key := range requested {
		if _, exists := seen[key]; !exists {
			missing = append(missing, key)
		}
	}
	return filtered, missing
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
