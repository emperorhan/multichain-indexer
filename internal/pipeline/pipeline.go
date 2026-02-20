package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/addressindex"
	"github.com/emperorhan/multichain-indexer/internal/alert"
	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/config"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator/autotune"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/fetcher"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/finalizer"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/ingester"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/normalizer"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/reorgdetector"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/replay"
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
	ReorgDetectorInterval  time.Duration
	FinalizerInterval      time.Duration
	IndexedBlocksRetention int64
	AddressIndex              config.AddressIndexConfig
	Fetcher                   config.FetcherStageConfig
	Normalizer                config.NormalizerStageConfig
	Ingester                  config.IngesterStageConfig
	Health                    config.HealthStageConfig
	ConfigWatcher             config.ConfigWatcherStageConfig
	ReorgDetectorMaxCheckDepth int
	Alerter                   alert.Alerter
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

// replayOp represents a pending replay operation sent via replayCh.
type replayOp struct {
	req      replay.PurgeRequest
	resultCh chan replayOpResult
}

type replayOpResult struct {
	result *replay.PurgeResult
	err    error
}

type Pipeline struct {
	cfg           Config
	adapter       chain.ChainAdapter
	db            store.TxBeginner
	repos         *Repos
	logger        *slog.Logger
	replayService *replay.Service
	replayCh      chan replayOp
	health        *PipelineHealth

	// Activation control: uses sync.Cond to avoid signal loss.
	active    atomic.Bool
	activeMu  sync.Mutex
	stateCond *sync.Cond
	// stateFlag: 0=inactive, 1=active, -1=deactivation requested
	stateFlag  atomic.Int32
	deactiveCh chan struct{} // signaled when deactivated
}

type streamCheckpointManager interface {
	LoadStreamCheckpoint(ctx context.Context, checkpointKey string) (string, error)
	PersistStreamCheckpoint(ctx context.Context, checkpointKey, streamID string) error
}

type Repos struct {
	WatchedAddr   store.WatchedAddressRepository
	Cursor        store.CursorRepository
	Transaction   store.TransactionRepository
	BalanceEvent  store.BalanceEventRepository
	Balance       store.BalanceRepository
	Token         store.TokenRepository
	Config        store.IndexerConfigRepository
	RuntimeConfig store.RuntimeConfigRepository
	IndexedBlock  store.IndexedBlockRepository
}

func New(
	cfg Config,
	adapter chain.ChainAdapter,
	db store.TxBeginner,
	repos *Repos,
	logger *slog.Logger,
) *Pipeline {
	health := NewPipelineHealth(cfg.Chain, cfg.Network)
	if cfg.Health.UnhealthyThreshold > 0 {
		health.unhealthyThreshold = cfg.Health.UnhealthyThreshold
	}
	p := &Pipeline{
		cfg:        cfg,
		adapter:    adapter,
		db:         db,
		repos:      repos,
		logger:     logger.With("component", "pipeline"),
		replayCh:   make(chan replayOp, 1),
		health:     health,
		deactiveCh: make(chan struct{}, 1),
	}
	p.stateCond = sync.NewCond(&p.activeMu)
	p.active.Store(true)
	p.stateFlag.Store(1)
	return p
}

// Chain returns the pipeline's chain.
func (p *Pipeline) Chain() model.Chain { return p.cfg.Chain }

// Network returns the pipeline's network.
func (p *Pipeline) Network() model.Network { return p.cfg.Network }

// Health returns the pipeline's health tracker.
func (p *Pipeline) Health() *PipelineHealth { return p.health }

// Deactivate stops the pipeline gracefully. It can be reactivated later.
func (p *Pipeline) Deactivate() {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()
	if !p.active.Load() {
		return
	}
	p.active.Store(false)
	p.stateFlag.Store(0)
	p.health.SetStatus(HealthStatusInactive)
	select {
	case p.deactiveCh <- struct{}{}:
	default:
	}
	p.stateCond.Broadcast()
}

// Activate reactivates a deactivated pipeline.
func (p *Pipeline) Activate() {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()
	if p.active.Load() {
		return
	}
	p.active.Store(true)
	p.stateFlag.Store(1)
	p.stateCond.Broadcast()
}

// IsActive returns whether the pipeline is currently active.
func (p *Pipeline) IsActive() bool {
	return p.active.Load()
}

// SetReplayService sets the replay service on the pipeline. This must be
// called before Run().
func (p *Pipeline) SetReplayService(svc *replay.Service) {
	p.replayService = svc
}

// ReplayService returns the pipeline's replay service (may be nil).
func (p *Pipeline) ReplayService() *replay.Service {
	return p.replayService
}

// RequestReplay is a synchronous method called by the Admin API. It sends
// a replay request to the pipeline's restart loop, which will stop all workers,
// execute the purge, and restart the pipeline.
func (p *Pipeline) RequestReplay(ctx context.Context, req replay.PurgeRequest) (*replay.PurgeResult, error) {
	resultCh := make(chan replayOpResult, 1)
	select {
	case p.replayCh <- replayOp{req: req, resultCh: resultCh}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case res := <-resultCh:
		return res.result, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Pipeline) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Wait if pipeline is deactivated — uses sync.Cond for signal safety.
		if !p.active.Load() {
			p.logger.Info("pipeline is deactivated, waiting for reactivation",
				"chain", p.cfg.Chain, "network", p.cfg.Network)

			// Wait for activation using sync.Cond (no signal loss)
			activated := make(chan struct{})
			go func() {
				p.activeMu.Lock()
				for p.stateFlag.Load() == 0 {
					p.stateCond.Wait()
				}
				p.activeMu.Unlock()
				close(activated)
			}()

			select {
			case <-activated:
				p.logger.Info("pipeline reactivated",
					"chain", p.cfg.Chain, "network", p.cfg.Network)
				p.health.SetStatus(HealthStatusHealthy)
				continue
			case <-ctx.Done():
				p.stateCond.Broadcast() // unblock waiting goroutine
				return ctx.Err()
			}
		}

		p.health.SetStatus(HealthStatusHealthy)

		// Each iteration creates a fresh context, channels, and errgroup.
		runCtx, runCancel := context.WithCancel(ctx)
		errCh := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errCh <- fmt.Errorf("pipeline panic: %v\n%s", r, debug.Stack())
				}
			}()
			errCh <- p.runPipeline(runCtx)
		}()

		select {
		case err := <-errCh:
			// Pipeline terminated normally or with error.
			runCancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				p.health.RecordFailure()
				return err
			}
			return nil

		case op := <-p.replayCh:
			// 1. Graceful stop: cancel all workers and wait for completion.
			p.logger.Warn("stopping pipeline for replay",
				"chain", p.cfg.Chain,
				"network", p.cfg.Network,
				"from_block", op.req.FromBlock,
			)
			runCancel()
			<-errCh // wait for full shutdown

			// 2. Execute purge while workers are stopped.
			result, err := p.replayService.PurgeFromBlock(ctx, op.req)
			op.resultCh <- replayOpResult{result: result, err: err}

			if err != nil {
				p.logger.Error("replay purge failed, restarting pipeline",
					"error", err,
					"chain", p.cfg.Chain,
					"network", p.cfg.Network,
				)
			} else {
				p.logger.Info("pipeline restarting after replay",
					"chain", p.cfg.Chain,
					"network", p.cfg.Network,
					"new_watermark", result.NewWatermark,
				)
			}
			// 3. Loop continues → pipeline restarts with fresh channels.
			continue

		case <-p.deactiveCh:
			// Deactivation requested: stop pipeline and loop back to wait.
			p.logger.Warn("pipeline deactivated via runtime config",
				"chain", p.cfg.Chain, "network", p.cfg.Network)
			runCancel()
			<-errCh
			continue

		case <-ctx.Done():
			runCancel()
			<-errCh
			return ctx.Err()
		}
	}
}

// runPipeline contains the original Run() body. All channels and stages are
// created fresh on each invocation so a restart starts clean.
func (p *Pipeline) runPipeline(ctx context.Context) error {
	bufSize := p.cfg.ChannelBufferSize
	if p.cfg.StreamTransportEnabled && p.cfg.StreamBackend == nil {
		return fmt.Errorf("stream transport enabled but stream backend is not configured")
	}

	// Channels between stages
	jobCh := make(chan event.FetchJob, bufSize)
	rawBatchInputCh := make(chan event.RawBatch, bufSize)
	rawBatchOutputCh := rawBatchInputCh
	normalizedCh := make(chan event.NormalizedBatch, bufSize)
	autoTuneSignalCollector := autotune.NewRuntimeSignalRegistry()

	if p.cfg.StreamTransportEnabled {
		streamName := p.streamBoundaryName(streamBoundaryFetchToNormal)
		rawBatchOutputCh = make(chan event.RawBatch, bufSize)
		p.logger.Info("pipeline stream transport active", "boundary", streamBoundaryFetchToNormal, "stream", streamName, "chain", p.cfg.Chain, "network", p.cfg.Network)
	}

	// Create stages
	// Detect block-scan capable adapter (EVM/BTC)
	_, isBlockScanAdapter := p.adapter.(chain.BlockScanAdapter)

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
	}).WithAutoTuneSignalSource(autoTuneSignalCollector)

	// Enable block-scan mode for EVM/BTC adapters
	if isBlockScanAdapter {
		coord = coord.WithBlockScanMode(p.repos.Config)
	}

	fetchOpts := []fetcher.Option{
		fetcher.WithAutoTuneSignalSink(autoTuneSignalCollector),
	}
	if p.cfg.Fetcher.RetryMaxAttempts > 0 {
		fetchOpts = append(fetchOpts, fetcher.WithRetryConfig(
			p.cfg.Fetcher.RetryMaxAttempts,
			time.Duration(p.cfg.Fetcher.BackoffInitialMs)*time.Millisecond,
			time.Duration(p.cfg.Fetcher.BackoffMaxMs)*time.Millisecond,
		))
	}
	if p.cfg.Fetcher.AdaptiveMinBatch > 0 {
		fetchOpts = append(fetchOpts, fetcher.WithAdaptiveMinBatch(p.cfg.Fetcher.AdaptiveMinBatch))
	}
	if p.cfg.Fetcher.BoundaryOverlapLookahead > 0 {
		fetchOpts = append(fetchOpts, fetcher.WithBoundaryOverlapLookahead(p.cfg.Fetcher.BoundaryOverlapLookahead))
	}
	fetch := fetcher.New(
		p.adapter, jobCh, rawBatchOutputCh,
		p.cfg.FetchWorkers, p.logger,
		fetchOpts...,
	)

	normOpts := []normalizer.Option{
		normalizer.WithTLS(p.cfg.SidecarTLSEnabled, p.cfg.SidecarTLSCA, p.cfg.SidecarTLSCert, p.cfg.SidecarTLSKey),
	}
	if p.cfg.Normalizer.RetryMaxAttempts > 0 {
		normOpts = append(normOpts, normalizer.WithRetryConfig(
			p.cfg.Normalizer.RetryMaxAttempts,
			time.Duration(p.cfg.Normalizer.RetryDelayInitialMs)*time.Millisecond,
			time.Duration(p.cfg.Normalizer.RetryDelayMaxMs)*time.Millisecond,
		))
	}
	norm := normalizer.New(
		p.cfg.SidecarAddr, p.cfg.SidecarTimeout,
		rawBatchInputCh, normalizedCh,
		p.cfg.NormalizerWorkers, p.logger,
		normOpts...,
	)

	// Reorg/finality channels (nil-safe if not used)
	reorgCh := make(chan event.ReorgEvent, 1)
	finalityCh := make(chan event.FinalityPromotion, 1)

	ingesterOpts := []ingester.Option{
		ingester.WithCommitInterleaver(p.cfg.CommitInterleaver),
		ingester.WithAutoTuneSignalSink(autoTuneSignalCollector),
		ingester.WithReorgChannel(reorgCh),
		ingester.WithFinalityChannel(finalityCh),
	}
	if p.cfg.Ingester.RetryMaxAttempts > 0 {
		ingesterOpts = append(ingesterOpts, ingester.WithRetryConfig(
			p.cfg.Ingester.RetryMaxAttempts,
			time.Duration(p.cfg.Ingester.RetryDelayInitialMs)*time.Millisecond,
			time.Duration(p.cfg.Ingester.RetryDelayMaxMs)*time.Millisecond,
		))
	}
	if p.cfg.Ingester.DeniedCacheCapacity > 0 {
		ingesterOpts = append(ingesterOpts, ingester.WithDeniedCacheConfig(
			p.cfg.Ingester.DeniedCacheCapacity,
			time.Duration(p.cfg.Ingester.DeniedCacheTTLSec)*time.Second,
		))
	}
	if p.repos.IndexedBlock != nil {
		ingesterOpts = append(ingesterOpts, ingester.WithIndexedBlockRepo(p.repos.IndexedBlock))
	}
	if p.replayService != nil {
		ingesterOpts = append(ingesterOpts, ingester.WithReplayService(p.replayService))
	}
	if isBlockScanAdapter {
		addrIdx := addressindex.NewTieredIndex(p.repos.WatchedAddr, addressindex.TieredIndexConfig{
			BloomExpectedItems: p.cfg.AddressIndex.BloomExpectedItems,
			BloomFPR:           p.cfg.AddressIndex.BloomFPR,
			LRUCapacity:        p.cfg.AddressIndex.LRUCapacity,
			LRUTTL:             time.Duration(p.cfg.AddressIndex.LRUTTLSec) * time.Second,
		})
		if err := addrIdx.Reload(ctx, p.cfg.Chain, p.cfg.Network); err != nil {
			p.logger.Warn("address index initial reload failed, continuing with empty index",
				"chain", p.cfg.Chain, "network", p.cfg.Network, "error", err)
		}
		ingesterOpts = append(ingesterOpts, ingester.WithAddressIndex(addrIdx))
		// Keep watchedAddrRepo as fallback
		ingesterOpts = append(ingesterOpts, ingester.WithWatchedAddressRepo(p.repos.WatchedAddr))
	}

	ingest := ingester.New(
		p.db,
		p.repos.Transaction, p.repos.BalanceEvent,
		p.repos.Balance, p.repos.Token,
		p.repos.Cursor, p.repos.Config,
		normalizedCh, p.logger,
		ingesterOpts...,
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

	// Start config watcher if runtime config repository is available.
	if p.repos.RuntimeConfig != nil {
		watcher := NewConfigWatcher(
			p.cfg.Chain, p.cfg.Network,
			p.repos.RuntimeConfig, coord,
			p.logger,
			time.Duration(p.cfg.ConfigWatcher.IntervalSec)*time.Second,
		).WithActivationController(p)
		g.Go(func() error {
			return watcher.Run(gCtx)
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

	// Start ReorgDetector + Finalizer if adapter supports reorg detection
	if reorgAdapter, ok := p.adapter.(chain.ReorgAwareAdapter); ok && p.repos.IndexedBlock != nil {
		detector := reorgdetector.New(
			p.cfg.Chain, p.cfg.Network,
			reorgAdapter, p.repos.IndexedBlock,
			reorgCh, p.cfg.ReorgDetectorInterval,
			p.logger,
		)
		if p.cfg.ReorgDetectorMaxCheckDepth > 0 {
			detector = detector.WithMaxCheckDepth(p.cfg.ReorgDetectorMaxCheckDepth)
		}
		if p.cfg.Alerter != nil {
			detector = detector.WithAlerter(p.cfg.Alerter)
		}
		g.Go(func() error {
			return detector.Run(gCtx)
		})

		var finOpts []finalizer.Option
		if p.cfg.IndexedBlocksRetention > 0 {
			finOpts = append(finOpts, finalizer.WithRetentionBlocks(p.cfg.IndexedBlocksRetention))
		}
		fin := finalizer.New(
			p.cfg.Chain, p.cfg.Network,
			reorgAdapter, p.repos.IndexedBlock,
			finalityCh, p.cfg.FinalizerInterval,
			p.logger,
			finOpts...,
		)
		g.Go(func() error {
			return fin.Run(gCtx)
		})

		p.logger.Info("reorg detector and finalizer started",
			"reorg_interval", p.cfg.ReorgDetectorInterval,
			"finalizer_interval", p.cfg.FinalizerInterval,
		)
	}

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
