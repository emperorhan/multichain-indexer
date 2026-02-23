package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	autotune "github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator/autotune"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/emperorhan/multichain-indexer/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelTrace "go.opentelemetry.io/otel/trace"
)

// Coordinator iterates over watched addresses and creates FetchJobs.
type Coordinator struct {
	chain           model.Chain
	network         model.Network
	watchedAddrRepo store.WatchedAddressRepository
	batchSize       atomic.Int32
	intervalNs      atomic.Int64 // nanoseconds, accessed atomically
	jobCh           chan<- event.FetchJob
	logger          *slog.Logger
	headProvider    headSequenceProvider
	autoTune        *autotune.Controller
	autoTuneSignals autotune.AutoTuneSignalSource

	wmRepo                   store.WatermarkRepository
	maxInitialLookbackBlocks int64
	intervalResetCh          chan struct{}

	// Dedup: track last enqueued block range to avoid duplicate in-flight jobs.
	hasEnqueued       bool
	lastEnqueuedStart int64
	lastEnqueuedEnd   int64

	// Backfill: channel to signal the pipeline that a backfill is needed.
	backfillCh chan<- BackfillRequest
}

// BackfillRequest is sent from the coordinator to the pipeline when newly
// added addresses require historical data backfill.
type BackfillRequest struct {
	Chain     model.Chain
	Network   model.Network
	FromBlock int64
}

type headSequenceProvider interface {
	GetHeadSequence(ctx context.Context) (int64, error)
}

type AutoTuneRestartState struct {
	Chain                     model.Chain
	Network                   model.Network
	BatchSize                 int
	OverrideManualActive      bool
	OverrideReleaseRemaining  int
	PolicyVersion             string
	PolicyManifestDigest      string
	PolicyEpoch               int64
	PolicyActivationRemaining int
}

func New(
	chain model.Chain,
	network model.Network,
	watchedAddrRepo store.WatchedAddressRepository,
	batchSize int,
	interval time.Duration,
	jobCh chan<- event.FetchJob,
	logger *slog.Logger,
) *Coordinator {
	c := &Coordinator{
		chain:           chain,
		network:         network,
		watchedAddrRepo: watchedAddrRepo,
		jobCh:           jobCh,
		logger:          logger.With("component", "coordinator"),
		intervalResetCh: make(chan struct{}, 1),
	}
	c.batchSize.Store(int32(batchSize))
	c.intervalNs.Store(int64(interval))
	return c
}

func (c *Coordinator) WithHeadProvider(provider headSequenceProvider) *Coordinator {
	c.headProvider = provider
	return c
}

// WithBlockScanMode configures the indexer config repository used for
// reading the pipeline watermark. All chains use block-scan mode exclusively.
func (c *Coordinator) WithBlockScanMode(wmRepo store.WatermarkRepository) *Coordinator {
	c.wmRepo = wmRepo
	return c
}

// WithMaxInitialLookbackBlocks sets the maximum number of blocks to look back
// from the chain head when no watermark exists (first run). Prevents scanning
// from genesis on mainnet chains.
func (c *Coordinator) WithMaxInitialLookbackBlocks(n int64) *Coordinator {
	c.maxInitialLookbackBlocks = n
	return c
}

// WithBackfillChannel sets the channel for signaling backfill requests to the pipeline.
func (c *Coordinator) WithBackfillChannel(ch chan<- BackfillRequest) *Coordinator {
	c.backfillCh = ch
	return c
}

func (c *Coordinator) WithAutoTune(cfg AutoTuneConfig) *Coordinator {
	return c.withAutoTune(cfg, nil)
}

func (c *Coordinator) WithAutoTuneWarmStart(cfg AutoTuneConfig, state *AutoTuneRestartState) *Coordinator {
	return c.withAutoTune(cfg, state)
}

func (c *Coordinator) ExportAutoTuneRestartState() *AutoTuneRestartState {
	if c.autoTune == nil {
		return nil
	}
	override := c.autoTune.ExportOverrideTransition()
	policy := c.autoTune.ExportPolicyTransition()
	return &AutoTuneRestartState{
		Chain:                     c.chain,
		Network:                   c.network,
		BatchSize:                 c.autoTune.CurrentBatch(),
		OverrideManualActive:      override.WasManualOverride,
		OverrideReleaseRemaining:  override.ReleaseHoldRemaining,
		PolicyVersion:             policy.Version,
		PolicyManifestDigest:      policy.ManifestDigest,
		PolicyEpoch:               policy.Epoch,
		PolicyActivationRemaining: policy.ActivationHoldRemaining,
	}
}

func (c *Coordinator) WithAutoTuneSignalSource(source autotune.AutoTuneSignalSource) *Coordinator {
	c.autoTuneSignals = source
	return c
}

func (c *Coordinator) withAutoTune(cfg AutoTuneConfig, warmState *AutoTuneRestartState) *Coordinator {
	transitionMode := "cold_start"
	var seedBatch *int
	seedReason := "none"
	previousAutoTune := c.autoTune
	overrideTransition := autotune.OverrideTransition{}
	policyTransition := autotune.PolicyTransition{}

	if warmState != nil {
		switch {
		case warmState.Chain != c.chain:
			seedReason = "warm_state_chain_mismatch"
			c.logger.Warn("coordinator auto-tune warm-start rejected",
				"chain", c.chain,
				"network", c.network,
				"state_chain", warmState.Chain,
				"state_network", warmState.Network,
				"reason", seedReason,
			)
		case warmState.Network != c.network:
			seedReason = "warm_state_network_mismatch"
			c.logger.Warn("coordinator auto-tune warm-start rejected",
				"chain", c.chain,
				"network", c.network,
				"state_chain", warmState.Chain,
				"state_network", warmState.Network,
				"reason", seedReason,
			)
		case warmState.BatchSize <= 0:
			seedReason = "warm_state_empty_batch"
		default:
			seed := warmState.BatchSize
			seedBatch = &seed
			transitionMode = "warm_start"
			seedReason = "warm_state_adopted"
			overrideTransition = autotune.OverrideTransition{
				WasManualOverride:    warmState.OverrideManualActive,
				ReleaseHoldRemaining: maxIntValue(warmState.OverrideReleaseRemaining, 0),
			}
			if strings.TrimSpace(warmState.PolicyVersion) != "" ||
				strings.TrimSpace(warmState.PolicyManifestDigest) != "" ||
				warmState.PolicyEpoch > 0 ||
				warmState.PolicyActivationRemaining > 0 {
				policyEpoch := warmState.PolicyEpoch
				if policyEpoch < 0 {
					policyEpoch = 0
				}
				policyTransition = autotune.PolicyTransition{
					HasState:                true,
					Version:                 warmState.PolicyVersion,
					ManifestDigest:          warmState.PolicyManifestDigest,
					Epoch:                   policyEpoch,
					ActivationHoldRemaining: maxIntValue(warmState.PolicyActivationRemaining, 0),
					FromWarmCheckpoint:      true,
				}
			}
		}
	} else if previousAutoTune != nil {
		seed := previousAutoTune.CurrentBatch()
		seedBatch = &seed
		transitionMode = "profile_transition"
		seedReason = "profile_transition_seed"
		overrideTransition = previousAutoTune.ExportOverrideTransition()
		policyTransition = previousAutoTune.ExportPolicyTransition()
	}

	useBoundedWarmStart := transitionMode == "warm_start" &&
		!overrideTransition.WasManualOverride &&
		overrideTransition.ReleaseHoldRemaining == 0 &&
		!policyTransition.HasState
	if transitionMode == "warm_start" && !useBoundedWarmStart {
		if overrideTransition.WasManualOverride || overrideTransition.ReleaseHoldRemaining > 0 {
			seedReason = "warm_state_override_boundary_seed"
		} else if policyTransition.HasState {
			seedReason = "warm_state_policy_boundary_seed"
		}
	}
	if useBoundedWarmStart {
		c.autoTune = autotune.NewWithRestartSeed(int(c.batchSize.Load()), cfg, seedBatch)
	} else {
		c.autoTune = autotune.NewWithSeed(int(c.batchSize.Load()), cfg, seedBatch)
	}
	if c.autoTune != nil {
		c.autoTune.ReconcileOverrideTransition(overrideTransition)
		c.autoTune.ReconcilePolicyTransition(policyTransition)
		snapshot := c.autoTune.Snapshot()
		c.logger.Info("coordinator auto-tune enabled",
			"chain", c.chain,
			"network", c.network,
			"min_batch", snapshot.MinBatchSize,
			"max_batch", snapshot.MaxBatchSize,
			"step_up", snapshot.StepUp,
			"step_down", snapshot.StepDown,
			"lag_high", snapshot.LagHighWatermark,
			"lag_low", snapshot.LagLowWatermark,
			"queue_high_pct", snapshot.QueueHighWatermarkPct,
			"queue_low_pct", snapshot.QueueLowWatermarkPct,
			"hysteresis_ticks", snapshot.HysteresisTicks,
			"cooldown_ticks", snapshot.CooldownTicks,
			"telemetry_stale_ticks", snapshot.TelemetryStaleTicks,
			"telemetry_recovery_ticks", snapshot.TelemetryRecoveryTicks,
			"operator_override_batch", snapshot.OperatorOverrideBatch,
			"operator_release_hold_ticks", snapshot.OperatorReleaseHold,
			"operator_release_remaining", snapshot.OverrideReleaseLeft,
			"policy_version", snapshot.PolicyVersion,
			"policy_manifest_digest", snapshot.PolicyManifestDigest,
			"policy_epoch", snapshot.PolicyEpoch,
			"policy_activation_hold_ticks", snapshot.PolicyActivationHold,
			"policy_activation_remaining", snapshot.PolicyActivationLeft,
			"profile_transition", transitionMode == "profile_transition",
			"transition_mode", transitionMode,
			"seed_reason", seedReason,
			"seed_batch", snapshot.CurrentBatch,
		)
	}
	return c
}

// UpdateBatchSize updates the base batch size at runtime.
// This takes effect on the next tick.
func (c *Coordinator) UpdateBatchSize(newBatchSize int) {
	if newBatchSize <= 0 {
		return
	}
	old := c.batchSize.Swap(int32(newBatchSize))
	if int(old) != newBatchSize {
		c.logger.Info("coordinator batch size updated", "old", old, "new", newBatchSize)
	}
}

// UpdateInterval updates the indexing interval at runtime.
// Returns true if the interval actually changed.
func (c *Coordinator) UpdateInterval(newInterval time.Duration) bool {
	if newInterval <= 0 {
		return false
	}
	old := time.Duration(c.intervalNs.Swap(int64(newInterval)))
	if old != newInterval {
		c.logger.Info("coordinator interval updated", "old", old, "new", newInterval)
		select {
		case c.intervalResetCh <- struct{}{}:
		default:
		}
		return true
	}
	return false
}

func (c *Coordinator) Run(ctx context.Context) error {
	interval := time.Duration(c.intervalNs.Load())
	c.logger.Info("coordinator started", "chain", c.chain, "network", c.network, "interval", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	chainLabel := c.chain.String()
	networkLabel := c.network.String()

	runTick := func() error {
		metrics.CoordinatorTicksTotal.WithLabelValues(chainLabel, networkLabel).Inc()
		tickStart := time.Now()
		if err := c.tick(ctx); err != nil {
			metrics.CoordinatorTickErrors.WithLabelValues(chainLabel, networkLabel).Inc()
			metrics.CoordinatorTickLatency.WithLabelValues(chainLabel, networkLabel).Observe(time.Since(tickStart).Seconds())
			return fmt.Errorf("coordinator tick failed: %w", err)
		}
		metrics.CoordinatorTickLatency.WithLabelValues(chainLabel, networkLabel).Observe(time.Since(tickStart).Seconds())
		return nil
	}

	// Run immediately on start, then on interval.
	// Fail-fast: any tick error halts the coordinator so errgroup cancels the
	// entire pipeline and the process restarts from the last committed cursor.
	if err := runTick(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("coordinator stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := runTick(); err != nil {
				return err
			}
		case <-c.intervalResetCh:
			ticker.Reset(time.Duration(c.intervalNs.Load()))
			continue
		}
	}
}

func (c *Coordinator) tick(ctx context.Context) error {
	ctx, span := tracing.Tracer("coordinator").Start(ctx, "coordinator.tick",
		otelTrace.WithAttributes(
			attribute.String("chain", string(c.chain)),
			attribute.String("network", string(c.network)),
		),
	)
	defer span.End()

	return c.tickBlockScan(ctx, span)
}

// tickBlockScan emits a single FetchJob covering a block range for all watched
// addresses. The start block is derived from the pipeline watermark; the end block
// is the current chain head.
func (c *Coordinator) tickBlockScan(ctx context.Context, span otelTrace.Span) error {
	addresses, err := c.watchedAddrRepo.GetActive(ctx, c.chain, c.network)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if len(addresses) == 0 {
		return nil
	}

	head := int64(0)
	if c.headProvider != nil {
		head, err = c.headProvider.GetHeadSequence(ctx)
		if err != nil {
			err = fmt.Errorf("resolve tick cutoff head: %w", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		if head < 0 {
			head = 0
		}
	}

	// Read watermark as the start block.
	startBlock := int64(0)
	hasWatermark := false
	if c.wmRepo != nil {
		wm, wmErr := c.wmRepo.GetWatermark(ctx, c.chain, c.network)
		if wmErr != nil {
			c.logger.Warn("block-scan watermark read failed, starting from 0", "error", wmErr)
		} else if wm != nil && wm.IngestedSequence > 0 {
			startBlock = wm.IngestedSequence + 1
			hasWatermark = true
		}
	}

	// On first run (no watermark), start from near the chain head instead of
	// genesis to avoid scanning millions of historical blocks.
	if !hasWatermark && head > 0 && c.maxInitialLookbackBlocks > 0 {
		tipStart := head - c.maxInitialLookbackBlocks + 1
		if tipStart < 0 {
			tipStart = 0
		}
		if tipStart > startBlock {
			c.logger.Info("block-scan: no watermark, starting near chain head",
				"head", head,
				"lookback_blocks", c.maxInitialLookbackBlocks,
				"start_block", tipStart,
			)
			startBlock = tipStart
		}
	}

	if head > 0 && startBlock > head {
		c.logger.Debug("block-scan: watermark ahead of head, nothing to scan",
			"start_block", startBlock, "head", head)
		return nil
	}

	// Cap the scan range by batchSize (blocks per tick).
	endBlock := head
	batchSize := int(c.batchSize.Load())
	if c.autoTune != nil {
		batchSize, _ = c.autoTune.Resolve(autotune.Inputs{
			Chain:              c.chain.String(),
			Network:            c.network.String(),
			HasHeadSignal:      c.headProvider != nil,
			HeadSequence:       head,
			HasMinCursorSignal: true,
			MinCursorSequence:  startBlock,
			QueueDepth:         len(c.jobCh),
			QueueCapacity:      cap(c.jobCh),
		})
	}
	if batchSize <= 0 {
		batchSize = int(c.batchSize.Load())
	}
	if endBlock > 0 && endBlock-startBlock+1 > int64(batchSize) {
		endBlock = startBlock + int64(batchSize) - 1
	}

	// Skip duplicate enqueue if the same range is already in-flight.
	// Only applies when watermark tracking is active (configRepo set),
	// which is the production block-scan mode where stalled watermarks
	// can cause the coordinator to re-enqueue the same range.
	if c.wmRepo != nil && c.hasEnqueued && startBlock == c.lastEnqueuedStart && endBlock == c.lastEnqueuedEnd {
		c.logger.Debug("block-scan: same range already enqueued, skipping",
			"start_block", startBlock, "end_block", endBlock)
		return nil
	}

	watchedAddrs := make([]string, len(addresses))
	for i, addr := range addresses {
		watchedAddrs[i] = addr.Address
	}

	job := event.FetchJob{
		Chain:            c.chain,
		Network:          c.network,
		FetchCutoffSeq:   endBlock,
		BatchSize:        batchSize,
		BlockScanMode:    true,
		StartBlock:       startBlock,
		EndBlock:         endBlock,
		WatchedAddresses: watchedAddrs,
	}

	select {
	case c.jobCh <- job:
		metrics.CoordinatorJobsCreated.WithLabelValues(c.chain.String(), c.network.String()).Inc()
		c.hasEnqueued = true
		c.lastEnqueuedStart = startBlock
		c.lastEnqueuedEnd = endBlock
	default:
		// Channel full -- log warning and try blocking send with timeout
		c.logger.Warn("job channel full, waiting for capacity",
			"chain", job.Chain, "network", job.Network)
		metrics.CoordinatorJobsDropped.WithLabelValues(string(job.Chain), string(job.Network)).Inc()
		select {
		case c.jobCh <- job:
			metrics.CoordinatorJobsCreated.WithLabelValues(c.chain.String(), c.network.String()).Inc()
			c.hasEnqueued = true
			c.lastEnqueuedStart = startBlock
			c.lastEnqueuedEnd = endBlock
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	span.SetAttributes(
		attribute.Int("address_count", len(addresses)),
		attribute.Int64("start_block", startBlock),
		attribute.Int64("end_block", endBlock),
	)

	// After normal scan, check for pending address backfills.
	// Only trigger when watermark tracking is active and a backfill channel is set.
	if c.backfillCh != nil && c.wmRepo != nil && hasWatermark {
		pendingAddrs, bfErr := c.watchedAddrRepo.GetPendingBackfill(ctx, c.chain, c.network)
		if bfErr != nil {
			c.logger.Warn("backfill check failed", "error", bfErr)
		} else if len(pendingAddrs) > 0 && pendingAddrs[0].BackfillFromBlock != nil {
			minBackfillBlock := *pendingAddrs[0].BackfillFromBlock
			if minBackfillBlock < startBlock {
				c.logger.Info("backfill requested",
					"from_block", minBackfillBlock,
					"current_start", startBlock,
					"pending_addresses", len(pendingAddrs),
				)
				select {
				case c.backfillCh <- BackfillRequest{
					Chain:     c.chain,
					Network:   c.network,
					FromBlock: minBackfillBlock,
				}:
				default:
					c.logger.Warn("backfill channel full, will retry next tick")
				}
			}
		}
	}

	return nil
}

func maxIntValue(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
