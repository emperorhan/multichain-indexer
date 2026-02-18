package pipeline

import (
	"errors"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/fetcher"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/ingester"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/normalizer"
	"github.com/emperorhan/multichain-indexer/internal/store"
	redisstream "github.com/emperorhan/multichain-indexer/internal/store/redis"
	"golang.org/x/sync/errgroup"
)

type Config struct {
	Chain                  model.Chain
	Network                model.Network
	BatchSize              int
	IndexingInterval       time.Duration
	CoordinatorAutoTune    CoordinatorAutoTuneConfig
	FetchWorkers           int
	NormalizerWorkers      int
	ChannelBufferSize      int
	SidecarAddr            string
	SidecarTimeout         time.Duration
	SidecarTLSEnabled      bool
	SidecarTLSCert         string
	SidecarTLSKey          string
	SidecarTLSCA           string
	StreamTransportEnabled bool
	StreamBackend          redisstream.MessageTransport
	StreamNamespace        string
	StreamSessionID        string
	CommitInterleaver      ingester.CommitInterleaver
}

const streamBoundaryFetchToNormal = "fetcher-normalizer"

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

	HysteresisTicks            int
	TelemetryStaleTicks        int
	TelemetryRecoveryTicks     int
	OperatorOverrideBatch      int
	OperatorReleaseHoldTicks   int
	PolicyVersion              string
	PolicyManifestDigest       string
	PolicyManifestRefreshEpoch int64
	PolicyActivationHoldTicks  int
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
	if p.cfg.StreamTransportEnabled && p.cfg.StreamBackend == nil {
		return fmt.Errorf("stream transport enabled but stream backend is not configured")
	}

	// Channels between stages
	jobCh := make(chan event.FetchJob, bufSize)
	rawBatchInputCh := make(chan event.RawBatch, bufSize)
	rawBatchOutputCh := rawBatchInputCh
	normalizedCh := make(chan event.NormalizedBatch, bufSize)

	if p.cfg.StreamTransportEnabled {
		streamName := p.streamBoundaryName(streamBoundaryFetchToNormal)
		rawBatchOutputCh = make(chan event.RawBatch, bufSize)
		p.logger.Info("pipeline stream transport active", "boundary", streamBoundaryFetchToNormal, "stream", streamName, "chain", p.cfg.Chain, "network", p.cfg.Network)
	}

	// Create stages
	coord := coordinator.New(
		p.cfg.Chain, p.cfg.Network,
		p.repos.WatchedAddr, p.repos.Cursor,
		p.cfg.BatchSize, p.cfg.IndexingInterval,
		jobCh, p.logger,
	).WithHeadProvider(p.adapter).WithAutoTune(coordinator.AutoTuneConfig{
		Enabled:                    p.cfg.CoordinatorAutoTune.Enabled,
		MinBatchSize:               p.cfg.CoordinatorAutoTune.MinBatchSize,
		MaxBatchSize:               p.cfg.CoordinatorAutoTune.MaxBatchSize,
		StepUp:                     p.cfg.CoordinatorAutoTune.StepUp,
		StepDown:                   p.cfg.CoordinatorAutoTune.StepDown,
		LagHighWatermark:           p.cfg.CoordinatorAutoTune.LagHighWatermark,
		LagLowWatermark:            p.cfg.CoordinatorAutoTune.LagLowWatermark,
		QueueHighWatermarkPct:      p.cfg.CoordinatorAutoTune.QueueHighWatermarkPct,
		QueueLowWatermarkPct:       p.cfg.CoordinatorAutoTune.QueueLowWatermarkPct,
		HysteresisTicks:            p.cfg.CoordinatorAutoTune.HysteresisTicks,
		TelemetryStaleTicks:        p.cfg.CoordinatorAutoTune.TelemetryStaleTicks,
		TelemetryRecoveryTicks:     p.cfg.CoordinatorAutoTune.TelemetryRecoveryTicks,
		OperatorOverrideBatchSize:  p.cfg.CoordinatorAutoTune.OperatorOverrideBatch,
		OperatorReleaseHoldTicks:   p.cfg.CoordinatorAutoTune.OperatorReleaseHoldTicks,
		PolicyVersion:              p.cfg.CoordinatorAutoTune.PolicyVersion,
		PolicyManifestDigest:       p.cfg.CoordinatorAutoTune.PolicyManifestDigest,
		PolicyManifestRefreshEpoch: p.cfg.CoordinatorAutoTune.PolicyManifestRefreshEpoch,
		PolicyActivationHoldTicks:  p.cfg.CoordinatorAutoTune.PolicyActivationHoldTicks,
	})

	fetch := fetcher.New(
		p.adapter, jobCh, rawBatchOutputCh,
		p.cfg.FetchWorkers, p.logger,
	)

	norm := normalizer.New(
		p.cfg.SidecarAddr, p.cfg.SidecarTimeout,
		rawBatchInputCh, normalizedCh,
		p.cfg.NormalizerWorkers, p.logger,
		normalizer.WithTLS(p.cfg.SidecarTLSEnabled, p.cfg.SidecarTLSCA, p.cfg.SidecarTLSCert, p.cfg.SidecarTLSKey),
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

	// Periodic channel depth sampling for PipelineChannelDepth gauge.
	chainStr := p.cfg.Chain.String()
	networkStr := p.cfg.Network.String()
	g.Go(func() error {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-gCtx.Done():
				return nil
			case <-ticker.C:
				metrics.PipelineChannelDepth.WithLabelValues(chainStr, networkStr, "fetch_job").Set(float64(len(jobCh)))
				metrics.PipelineChannelDepth.WithLabelValues(chainStr, networkStr, "raw_batch").Set(float64(len(rawBatchInputCh)))
				metrics.PipelineChannelDepth.WithLabelValues(chainStr, networkStr, "normalized").Set(float64(len(normalizedCh)))
			}
		}
	})

	if p.cfg.StreamTransportEnabled {
		streamName := p.streamBoundaryName(streamBoundaryFetchToNormal)
		g.Go(func() error {
			return p.runRawBatchStreamProducer(gCtx, rawBatchOutputCh, p.cfg.StreamBackend, streamName)
		})
		g.Go(func() error {
			return p.runRawBatchStreamConsumer(gCtx, rawBatchInputCh, p.cfg.StreamBackend, streamName)
		})
	}

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

func (p *Pipeline) streamBoundaryName(boundary string) string {
	namespace := strings.TrimSpace(p.cfg.StreamNamespace)
	if namespace == "" {
		namespace = "pipeline"
	}
	sessionID := strings.TrimSpace(p.cfg.StreamSessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	return fmt.Sprintf("%s:chain=%s:network=%s:session=%s:boundary=%s", namespace, p.cfg.Chain, p.cfg.Network, sessionID, boundary)
}

func (p *Pipeline) runRawBatchStreamProducer(ctx context.Context, in <-chan event.RawBatch, stream redisstream.MessageTransport, streamName string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case batch, ok := <-in:
			if !ok {
				return nil
			}
			if _, err := stream.PublishJSON(ctx, streamName, batch); err != nil {
				return fmt.Errorf("stream producer failed: %w", err)
			}
		}
	}
}

func (p *Pipeline) runRawBatchStreamConsumer(ctx context.Context, out chan<- event.RawBatch, stream redisstream.MessageTransport, streamName string) error {
	lastID := "0"
	for {
		var batch event.RawBatch
		nextID, err := stream.ReadJSON(ctx, streamName, lastID, &batch)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return fmt.Errorf("stream consumer failed: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- batch:
			lastID = nextID
		}
	}
}
