package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/addressindex"
	"github.com/emperorhan/multichain-indexer/internal/alert"
	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/circuitbreaker"
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
	"golang.org/x/sync/errgroup"
)

type Config struct {
	Chain                      model.Chain
	Network                    model.Network
	BatchSize                  int
	IndexingInterval           time.Duration
	CoordinatorAutoTune        CoordinatorAutoTuneConfig
	FetchWorkers               int
	NormalizerWorkers          int
	ChannelBufferSize          int
	JobChBufferSize            int // Coordinator -> Fetcher (default: 20)
	RawBatchChBufferSize       int // Fetcher -> Normalizer (default: 10)
	NormalizedChBufferSize     int // Normalizer -> Ingester (default: 5)
	SidecarAddr                string
	SidecarTimeout             time.Duration
	SidecarMaxMsgSizeMB        int
	SidecarTLSEnabled          bool
	SidecarTLSCert             string
	SidecarTLSKey              string
	SidecarTLSCA               string
	CommitInterleaver          ingester.CommitInterleaver
	ReorgDetectorInterval      time.Duration
	FinalizerInterval          time.Duration
	IndexedBlocksRetention     int64
	AddressIndex               config.AddressIndexConfig
	Fetcher                    config.FetcherStageConfig
	Normalizer                 config.NormalizerStageConfig
	Ingester                   config.IngesterStageConfig
	Health                     config.HealthStageConfig
	ConfigWatcher              config.ConfigWatcherStageConfig
	ReorgDetectorMaxCheckDepth int
	MaxInitialLookbackBlocks   int
	Alerter                    alert.Alerter
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
	backfillCh    chan coordinator.BackfillRequest
	health        *PipelineHealth

	// Reusable channels between stages (created once in New).
	jobCh        chan event.FetchJob
	rawBatchCh   chan event.RawBatch
	normalizedCh chan event.NormalizedBatch

	// Activation control: uses sync.Cond to avoid signal loss.
	active    atomic.Bool
	activeMu  sync.Mutex
	stateCond *sync.Cond
	// stateFlag: 0=inactive, 1=active, -1=deactivation requested
	stateFlag  atomic.Int32
	deactiveCh chan struct{} // signaled when deactivated

	// Auto-restart: exponential backoff on consecutive failures.
	consecutiveFailures int
}

type Repos struct {
	WatchedAddr   store.WatchedAddressRepository
	Transaction   store.TransactionRepository
	BalanceEvent  store.BalanceEventRepository
	Balance       store.BalanceRepository
	Token         store.TokenRepository
	Watermark     store.WatermarkRepository
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
	// Resolve per-stage buffer sizes with fallback chain:
	// per-stage value > ChannelBufferSize > stage-specific default.
	fallback := cfg.ChannelBufferSize
	jobBuf := cfg.JobChBufferSize
	if jobBuf <= 0 {
		if fallback > 0 {
			jobBuf = fallback
		} else {
			jobBuf = 20
		}
	}
	rawBuf := cfg.RawBatchChBufferSize
	if rawBuf <= 0 {
		if fallback > 0 {
			rawBuf = fallback
		} else {
			rawBuf = 10
		}
	}
	normBuf := cfg.NormalizedChBufferSize
	if normBuf <= 0 {
		if fallback > 0 {
			normBuf = fallback
		} else {
			normBuf = 5
		}
	}
	p := &Pipeline{
		cfg:          cfg,
		adapter:      adapter,
		db:           db,
		repos:        repos,
		logger:       logger.With("component", "pipeline"),
		replayCh:     make(chan replayOp, 1),
		backfillCh:   make(chan coordinator.BackfillRequest, 1),
		health:       health,
		jobCh:        make(chan event.FetchJob, jobBuf),
		rawBatchCh:   make(chan event.RawBatch, rawBuf),
		normalizedCh: make(chan event.NormalizedBatch, normBuf),
		deactiveCh:   make(chan struct{}, 1),
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

const maxRestartBackoff = 5 * time.Minute

// calcRestartBackoff returns an exponential backoff duration: 1s, 2s, 4s, ... capped at maxRestartBackoff.
func (p *Pipeline) calcRestartBackoff() time.Duration {
	d := time.Second << uint(p.consecutiveFailures)
	if d > maxRestartBackoff {
		d = maxRestartBackoff
	}
	return d
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

			// Wait for activation using sync.Cond (no signal loss).
			// A dedicated goroutine watches ctx.Done() and broadcasts to
			// guarantee the Wait() is unblocked even if cancellation races
			// with the select below.
			activated := make(chan struct{})
			go func() {
				p.activeMu.Lock()
				for p.stateFlag.Load() == 0 && ctx.Err() == nil {
					p.stateCond.Wait()
				}
				p.activeMu.Unlock()
				close(activated)
			}()
			// Context watcher: ensures Broadcast fires even if the select
			// below hasn't been reached yet when ctx is cancelled.
			ctxWatchDone := make(chan struct{})
			go func() {
				defer close(ctxWatchDone)
				select {
				case <-ctx.Done():
					p.stateCond.Broadcast()
				case <-activated:
					// activation happened first; no broadcast needed
				}
			}()

			select {
			case <-activated:
				<-ctxWatchDone // ensure watcher goroutine exits
				if ctx.Err() != nil {
					return ctx.Err()
				}
				p.logger.Info("pipeline reactivated",
					"chain", p.cfg.Chain, "network", p.cfg.Network)
				p.health.SetStatus(HealthStatusHealthy)
				continue
			case <-ctx.Done():
				p.stateCond.Broadcast() // redundant but safe
				<-activated             // ensure goroutine exits before returning
				<-ctxWatchDone
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
				p.consecutiveFailures++
				if becameUnhealthy := p.health.RecordFailure(); becameUnhealthy && p.cfg.Alerter != nil {
					sendErr := p.cfg.Alerter.Send(ctx, alert.Alert{
						Type:    alert.AlertTypeUnhealthy,
						Chain:   string(p.cfg.Chain),
						Network: string(p.cfg.Network),
						Title:   "Pipeline became unhealthy",
						Message: fmt.Sprintf("Pipeline %s/%s has become unhealthy after %d consecutive failures", p.cfg.Chain, p.cfg.Network, p.health.Snapshot().ConsecutiveFailures),
					})
					if sendErr != nil {
						p.logger.Warn("failed to send unhealthy alert", "error", sendErr)
					}
				}

				backoff := p.calcRestartBackoff()
				p.logger.Warn("pipeline will restart after backoff",
					"error", err,
					"backoff", backoff,
					"consecutive_failures", p.consecutiveFailures,
					"chain", p.cfg.Chain,
					"network", p.cfg.Network,
				)

				select {
				case <-time.After(backoff):
					continue // restart the pipeline loop
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			// Success path: reset failure counter and check for recovery from unhealthy state.
			p.consecutiveFailures = 0
			if recovered := p.health.RecordSuccessWithRecovery(); recovered && p.cfg.Alerter != nil {
				sendErr := p.cfg.Alerter.Send(ctx, alert.Alert{
					Type:    alert.AlertTypeRecovery,
					Chain:   string(p.cfg.Chain),
					Network: string(p.cfg.Network),
					Title:   "Pipeline recovered",
					Message: fmt.Sprintf("Pipeline %s/%s recovered from unhealthy state", p.cfg.Chain, p.cfg.Network),
				})
				if sendErr != nil {
					p.logger.Warn("failed to send recovery alert", "error", sendErr)
				}
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

		case req := <-p.backfillCh:
			// Backfill requested by coordinator: stop workers, purge, clear flags, restart.
			p.logger.Warn("stopping pipeline for backfill",
				"chain", p.cfg.Chain,
				"network", p.cfg.Network,
				"from_block", req.FromBlock,
			)
			runCancel()
			<-errCh // wait for full shutdown

			// Execute purge to rewind watermark.
			if p.replayService != nil {
				purgeReq := replay.PurgeRequest{
					Chain:     req.Chain,
					Network:   req.Network,
					FromBlock: req.FromBlock,
					Force:     true,
					Reason:    "address backfill",
				}
				result, purgeErr := p.replayService.PurgeFromBlock(ctx, purgeReq)
				if purgeErr != nil {
					p.logger.Error("backfill purge failed, restarting pipeline",
						"error", purgeErr,
						"chain", p.cfg.Chain,
						"network", p.cfg.Network,
					)
				} else {
					p.logger.Info("backfill purge completed",
						"chain", p.cfg.Chain,
						"network", p.cfg.Network,
						"new_watermark", result.NewWatermark,
					)
				}
			}

			// Clear backfill flags so they are not re-triggered.
			if clearErr := p.repos.WatchedAddr.ClearBackfill(ctx, req.Chain, req.Network); clearErr != nil {
				p.logger.Error("failed to clear backfill flags", "error", clearErr)
			}

			// Loop continues → pipeline restarts from rewound watermark.
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

// drainChannels flushes any residual data from the reusable channels.
// Must be called after all stage goroutines have exited.
func (p *Pipeline) drainChannels() {
	for {
		select {
		case <-p.jobCh:
		case <-p.rawBatchCh:
		case <-p.normalizedCh:
		default:
			return
		}
	}
}

// runPipeline contains the original Run() body. Channels are reused across
// restarts; only the context is recreated for cancellation.
func (p *Pipeline) runPipeline(ctx context.Context) error {
	// Drain any residual data from previous run before starting fresh stages.
	p.drainChannels()

	jobCh := p.jobCh
	rawBatchCh := p.rawBatchCh
	normalizedCh := p.normalizedCh
	autoTuneSignalCollector := autotune.NewRuntimeSignalRegistry()

	// Create stages
	// Detect block-scan capable adapter (EVM/BTC)
	_, isBlockScanAdapter := p.adapter.(chain.BlockScanAdapter)

	coord := coordinator.New(
		p.cfg.Chain, p.cfg.Network,
		p.repos.WatchedAddr,
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

	// Enable block-scan mode for EVM/BTC/Solana adapters
	if isBlockScanAdapter {
		coord = coord.WithBlockScanMode(p.repos.Watermark)
		if p.cfg.MaxInitialLookbackBlocks > 0 {
			coord = coord.WithMaxInitialLookbackBlocks(int64(p.cfg.MaxInitialLookbackBlocks))
		}
		coord = coord.WithBackfillChannel(p.backfillCh)
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
	if p.cfg.Fetcher.BlockScanMaxBatchTxs > 0 {
		fetchOpts = append(fetchOpts, fetcher.WithBlockScanMaxBatchTxs(p.cfg.Fetcher.BlockScanMaxBatchTxs))
	}
	fetchOpts = append(fetchOpts, fetcher.WithCircuitBreaker(
		p.cfg.Fetcher.CircuitBreaker.FailureThreshold,
		p.cfg.Fetcher.CircuitBreaker.SuccessThreshold,
		time.Duration(p.cfg.Fetcher.CircuitBreaker.OpenTimeoutMs)*time.Millisecond,
		cbStateChangeCallback("fetcher", p.cfg.Chain.String(), p.cfg.Network.String(), p.logger),
	))
	fetch := fetcher.New(
		p.adapter, jobCh, rawBatchCh,
		p.cfg.FetchWorkers, p.logger,
		fetchOpts...,
	)

	normOpts := []normalizer.Option{
		normalizer.WithTLS(p.cfg.SidecarTLSEnabled, p.cfg.SidecarTLSCA, p.cfg.SidecarTLSCert, p.cfg.SidecarTLSKey),
		normalizer.WithMaxMsgSizeMB(p.cfg.SidecarMaxMsgSizeMB),
	}
	if p.cfg.Normalizer.RetryMaxAttempts > 0 {
		normOpts = append(normOpts, normalizer.WithRetryConfig(
			p.cfg.Normalizer.RetryMaxAttempts,
			time.Duration(p.cfg.Normalizer.RetryDelayInitialMs)*time.Millisecond,
			time.Duration(p.cfg.Normalizer.RetryDelayMaxMs)*time.Millisecond,
		))
	}
	normOpts = append(normOpts, normalizer.WithCircuitBreaker(
		p.cfg.Normalizer.CircuitBreaker.FailureThreshold,
		p.cfg.Normalizer.CircuitBreaker.SuccessThreshold,
		time.Duration(p.cfg.Normalizer.CircuitBreaker.OpenTimeoutMs)*time.Millisecond,
		cbStateChangeCallback("normalizer", p.cfg.Chain.String(), p.cfg.Network.String(), p.logger),
	))
	norm := normalizer.New(
		p.cfg.SidecarAddr, p.cfg.SidecarTimeout,
		rawBatchCh, normalizedCh,
		p.cfg.NormalizerWorkers, p.logger,
		normOpts...,
	)

	// Reorg/finality channels (nil-safe if not used)
	reorgCh := make(chan event.ReorgEvent, 4)
	finalityCh := make(chan event.FinalityPromotion, 4)

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
	if p.cfg.Ingester.BlockScanAddrCacheTTLSec > 0 {
		ingesterOpts = append(ingesterOpts, ingester.WithBlockScanAddrCacheTTL(
			time.Duration(p.cfg.Ingester.BlockScanAddrCacheTTLSec)*time.Second,
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
		p.repos.Watermark,
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
				metrics.PipelineChannelDepth.WithLabelValues(chainStr, networkStr, "raw_batch").Set(float64(len(rawBatchCh)))
				metrics.PipelineChannelDepth.WithLabelValues(chainStr, networkStr, "normalized").Set(float64(len(normalizedCh)))
			}
		}
	})

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

// cbStateChangeCallback returns a circuit breaker state change callback that
// logs the transition and increments the Prometheus counter.
func cbStateChangeCallback(component, chainStr, networkStr string, logger *slog.Logger) func(from, to circuitbreaker.State) {
	return func(from, to circuitbreaker.State) {
		logger.Warn("circuit breaker state change",
			"component", component,
			"chain", chainStr,
			"network", networkStr,
			"from", from.String(),
			"to", to.String(),
		)
		metrics.CircuitBreakerStateChanges.WithLabelValues(
			component, chainStr, networkStr, from.String(), to.String(),
		).Inc()
	}
}
