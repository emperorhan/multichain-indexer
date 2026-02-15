package pipeline

import (
	"context"
	"log/slog"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/fetcher"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/ingester"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/normalizer"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"golang.org/x/sync/errgroup"
)

type Config struct {
	Chain               model.Chain
	Network             model.Network
	BatchSize           int
	IndexingInterval    time.Duration
	CoordinatorAutoTune CoordinatorAutoTuneConfig
	FetchWorkers        int
	NormalizerWorkers   int
	ChannelBufferSize   int
	SidecarAddr         string
	SidecarTimeout      time.Duration
	CommitInterleaver   ingester.CommitInterleaver
}

type CoordinatorAutoTuneConfig struct {
	Enabled bool

	MinBatchSize int
	MaxBatchSize int
	StepUp       int
	StepDown     int

	LagHighWatermark int64
	LagLowWatermark  int64

	QueueHighWatermarkPct int
	QueueLowWatermarkPct  int

	HysteresisTicks          int
	TelemetryStaleTicks      int
	TelemetryRecoveryTicks   int
	OperatorOverrideBatch    int
	OperatorReleaseHoldTicks int
}

type Pipeline struct {
	cfg     Config
	adapter chain.ChainAdapter
	db      store.TxBeginner
	repos   *Repos
	logger  *slog.Logger
}

type Repos struct {
	WatchedAddr  store.WatchedAddressRepository
	Cursor       store.CursorRepository
	Transaction  store.TransactionRepository
	BalanceEvent store.BalanceEventRepository
	Balance      store.BalanceRepository
	Token        store.TokenRepository
	Config       store.IndexerConfigRepository
}

func New(
	cfg Config,
	adapter chain.ChainAdapter,
	db store.TxBeginner,
	repos *Repos,
	logger *slog.Logger,
) *Pipeline {
	return &Pipeline{
		cfg:     cfg,
		adapter: adapter,
		db:      db,
		repos:   repos,
		logger:  logger.With("component", "pipeline"),
	}
}

func (p *Pipeline) Run(ctx context.Context) error {
	bufSize := p.cfg.ChannelBufferSize

	// Channels between stages
	jobCh := make(chan event.FetchJob, bufSize)
	rawBatchCh := make(chan event.RawBatch, bufSize)
	normalizedCh := make(chan event.NormalizedBatch, bufSize)

	// Create stages
	coord := coordinator.New(
		p.cfg.Chain, p.cfg.Network,
		p.repos.WatchedAddr, p.repos.Cursor,
		p.cfg.BatchSize, p.cfg.IndexingInterval,
		jobCh, p.logger,
	).WithHeadProvider(p.adapter).WithAutoTune(coordinator.AutoTuneConfig{
		Enabled:                   p.cfg.CoordinatorAutoTune.Enabled,
		MinBatchSize:              p.cfg.CoordinatorAutoTune.MinBatchSize,
		MaxBatchSize:              p.cfg.CoordinatorAutoTune.MaxBatchSize,
		StepUp:                    p.cfg.CoordinatorAutoTune.StepUp,
		StepDown:                  p.cfg.CoordinatorAutoTune.StepDown,
		LagHighWatermark:          p.cfg.CoordinatorAutoTune.LagHighWatermark,
		LagLowWatermark:           p.cfg.CoordinatorAutoTune.LagLowWatermark,
		QueueHighWatermarkPct:     p.cfg.CoordinatorAutoTune.QueueHighWatermarkPct,
		QueueLowWatermarkPct:      p.cfg.CoordinatorAutoTune.QueueLowWatermarkPct,
		HysteresisTicks:           p.cfg.CoordinatorAutoTune.HysteresisTicks,
		TelemetryStaleTicks:       p.cfg.CoordinatorAutoTune.TelemetryStaleTicks,
		TelemetryRecoveryTicks:    p.cfg.CoordinatorAutoTune.TelemetryRecoveryTicks,
		OperatorOverrideBatchSize: p.cfg.CoordinatorAutoTune.OperatorOverrideBatch,
		OperatorReleaseHoldTicks:  p.cfg.CoordinatorAutoTune.OperatorReleaseHoldTicks,
	})

	fetch := fetcher.New(
		p.adapter, jobCh, rawBatchCh,
		p.cfg.FetchWorkers, p.logger,
	)

	norm := normalizer.New(
		p.cfg.SidecarAddr, p.cfg.SidecarTimeout,
		rawBatchCh, normalizedCh,
		p.cfg.NormalizerWorkers, p.logger,
	)

	ingest := ingester.New(
		p.db,
		p.repos.Transaction, p.repos.BalanceEvent,
		p.repos.Balance, p.repos.Token,
		p.repos.Cursor, p.repos.Config,
		normalizedCh, p.logger,
		ingester.WithCommitInterleaver(p.cfg.CommitInterleaver),
	)

	p.logger.Info("pipeline starting",
		"chain", p.cfg.Chain,
		"network", p.cfg.Network,
		"fetch_workers", p.cfg.FetchWorkers,
		"normalizer_workers", p.cfg.NormalizerWorkers,
		"batch_size", p.cfg.BatchSize,
		"interval", p.cfg.IndexingInterval,
		"coordinator_auto_tune_enabled", p.cfg.CoordinatorAutoTune.Enabled,
	)

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return coord.Run(gCtx)
	})
	g.Go(func() error {
		return fetch.Run(gCtx)
	})
	g.Go(func() error {
		return norm.Run(gCtx)
	})
	g.Go(func() error {
		return ingest.Run(gCtx)
	})

	return g.Wait()
}
