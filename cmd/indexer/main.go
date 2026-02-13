package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kodax/koda-custody-indexer/internal/chain/solana"
	"github.com/kodax/koda-custody-indexer/internal/config"
	"github.com/kodax/koda-custody-indexer/internal/domain/model"
	"github.com/kodax/koda-custody-indexer/internal/pipeline"
	"github.com/kodax/koda-custody-indexer/internal/store"
	"github.com/kodax/koda-custody-indexer/internal/store/postgres"
	"golang.org/x/sync/errgroup"
)

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

	logger.Info("starting koda-custody-indexer",
		"solana_rpc", cfg.Solana.RPCURL,
		"solana_network", cfg.Solana.Network,
		"sidecar_addr", cfg.Sidecar.Addr,
		"watched_addresses", len(cfg.Pipeline.WatchedAddresses),
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
		WatchedAddr: postgres.NewWatchedAddressRepo(db),
		Cursor:      postgres.NewCursorRepo(db),
		Transaction: postgres.NewTransactionRepo(db),
		Transfer:    postgres.NewTransferRepo(db),
		Balance:     postgres.NewBalanceRepo(db),
		Token:       postgres.NewTokenRepo(db),
		Config:      postgres.NewIndexerConfigRepo(db),
	}

	// Sync watched addresses from env to DB
	network := model.Network(cfg.Solana.Network)
	if err := syncWatchedAddresses(context.Background(), repos.WatchedAddr, repos.Cursor, network, cfg.Pipeline.WatchedAddresses); err != nil {
		logger.Error("failed to sync watched addresses", "error", err)
		os.Exit(1)
	}

	// Create Solana adapter
	adapter := solana.NewAdapter(cfg.Solana.RPCURL, logger)

	// Setup pipeline
	pipelineCfg := pipeline.Config{
		Chain:             model.ChainSolana,
		Network:           network,
		BatchSize:         cfg.Pipeline.BatchSize,
		IndexingInterval:  time.Duration(cfg.Pipeline.IndexingIntervalMs) * time.Millisecond,
		FetchWorkers:      cfg.Pipeline.FetchWorkers,
		NormalizerWorkers: cfg.Pipeline.NormalizerWorkers,
		ChannelBufferSize: cfg.Pipeline.ChannelBufferSize,
		SidecarAddr:       cfg.Sidecar.Addr,
		SidecarTimeout:    cfg.Sidecar.Timeout,
	}

	p := pipeline.New(pipelineCfg, adapter, db, repos, logger)

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

	// Pipeline
	g.Go(func() error {
		return p.Run(gCtx)
	})

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

func syncWatchedAddresses(ctx context.Context, repo store.WatchedAddressRepository, cursorRepo store.CursorRepository, network model.Network, addresses []string) error {
	for _, addr := range addresses {
		wa := &model.WatchedAddress{
			Chain:    model.ChainSolana,
			Network:  network,
			Address:  addr,
			IsActive: true,
			Source:   model.AddressSourceEnv,
		}
		if err := repo.Upsert(ctx, wa); err != nil {
			return fmt.Errorf("upsert watched address %s: %w", addr, err)
		}
		// Ensure cursor exists
		if err := cursorRepo.EnsureExists(ctx, model.ChainSolana, network, addr); err != nil {
			return fmt.Errorf("ensure cursor for %s: %w", addr, err)
		}
	}
	slog.Info("synced watched addresses from env", "count", len(addresses))
	return nil
}

func runHealthServer(ctx context.Context, port int, logger *slog.Logger) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	logger.Info("health server started", "port", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("health server: %w", err)
	}
	return nil
}
