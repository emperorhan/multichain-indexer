package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
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

type streamCheckpointManager interface {
	LoadStreamCheckpoint(ctx context.Context, checkpointKey string) (string, error)
	PersistStreamCheckpoint(ctx context.Context, checkpointKey, streamID string) error
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
	return fmt.Sprintf("%s:chain=%s:network=%s:boundary=%s", p.streamBoundaryNamespace(), p.cfg.Chain, p.cfg.Network, boundary)
}

func (p *Pipeline) streamBoundaryNamespace() string {
	namespace := strings.TrimSpace(p.cfg.StreamNamespace)
	if namespace == "" {
		namespace = "pipeline"
	}
	return namespace
}

func (p *Pipeline) streamBoundarySessionID() string {
	sessionID := strings.TrimSpace(p.cfg.StreamSessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	return sessionID
}

func (p *Pipeline) streamBoundaryCheckpointKey(boundary string) string {
	return fmt.Sprintf("stream-checkpoint:namespace=%s:chain=%s:network=%s:session=%s:boundary=%s", p.streamBoundaryNamespace(), p.cfg.Chain, p.cfg.Network, p.streamBoundarySessionID(), boundary)
}

func (p *Pipeline) streamBoundaryLegacyCheckpointKey(boundary string) string {
	return fmt.Sprintf("stream-checkpoint:chain=%s:network=%s:boundary=%s", p.cfg.Chain, p.cfg.Network, boundary)
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
	lastID, err := p.loadStreamCheckpoint(ctx, stream, streamBoundaryFetchToNormal)
	if err != nil {
		return err
	}

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
			if err := p.storeStreamCheckpoint(ctx, stream, streamBoundaryFetchToNormal, nextID); err != nil {
				return err
			}
		}
	}
}

func (p *Pipeline) loadStreamCheckpoint(ctx context.Context, stream redisstream.MessageTransport, boundary string) (string, error) {
	checkpointManager, ok := stream.(streamCheckpointManager)
	if !ok {
		return "0", nil
	}

	checkpointKey := p.streamBoundaryCheckpointKey(boundary)
	legacyCheckpointKey := p.streamBoundaryLegacyCheckpointKey(boundary)

	raw, err := checkpointManager.LoadStreamCheckpoint(ctx, checkpointKey)
	if err != nil {
		p.logger.Warn("stream checkpoint load failed; bootstrapping from stream start", "boundary", boundary, "checkpoint_key", checkpointKey, "error", err)
	} else if trimmed := strings.TrimSpace(raw); trimmed != "" && trimmed != "0" {
		if streamErr := validateStreamOffset(trimmed); streamErr == nil {
			return trimmed, nil
		} else {
			p.logger.Warn("stream checkpoint invalid; bootstrapping from stream start", "boundary", boundary, "checkpoint_key", checkpointKey, "checkpoint", trimmed, "error", streamErr)
		}
	}

	legacyRaw, err := checkpointManager.LoadStreamCheckpoint(ctx, legacyCheckpointKey)
	if err != nil {
		p.logger.Warn("legacy stream checkpoint load failed; bootstrapping from stream start", "boundary", boundary, "checkpoint_key", legacyCheckpointKey, "error", err)
		return "0", nil
	}
	trimmedLegacy := strings.TrimSpace(legacyRaw)
	if trimmedLegacy != "" && trimmedLegacy != "0" {
		if streamErr := validateStreamOffset(trimmedLegacy); streamErr == nil {
			if err := checkpointManager.PersistStreamCheckpoint(ctx, checkpointKey, trimmedLegacy); err != nil {
				p.logger.Warn("legacy stream checkpoint migration failed", "boundary", boundary, "from_key", legacyCheckpointKey, "to_key", checkpointKey, "error", err)
			}
			return trimmedLegacy, nil
		} else {
			p.logger.Warn("legacy stream checkpoint invalid; bootstrapping from stream start", "boundary", boundary, "checkpoint_key", legacyCheckpointKey, "checkpoint", trimmedLegacy, "error", streamErr)
		}
	}

	return "0", nil
}

func (p *Pipeline) storeStreamCheckpoint(ctx context.Context, stream redisstream.MessageTransport, boundary string, streamID string) error {
	checkpointManager, ok := stream.(streamCheckpointManager)
	if !ok {
		return nil
	}

	checkpointKey := p.streamBoundaryCheckpointKey(boundary)
	if err := checkpointManager.PersistStreamCheckpoint(ctx, checkpointKey, streamID); err != nil {
		return fmt.Errorf("stream checkpoint persist failed: %w", err)
	}

	return nil
}

func validateStreamOffset(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "0" {
		return nil
	}

	parts := strings.SplitN(trimmed, "-", 2)
	if len(parts) == 2 {
		if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return fmt.Errorf("invalid stream offset %q: missing components", raw)
		}
		msg, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil || msg < 0 {
			return fmt.Errorf("invalid stream offset %q: malformed id", raw)
		}
		seq, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || seq < 0 {
			return fmt.Errorf("invalid stream offset %q: malformed id", raw)
		}
		return nil
	}

	msg, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || msg < 0 {
		return fmt.Errorf("invalid stream offset %q: malformed id", raw)
	}
	return nil
}
