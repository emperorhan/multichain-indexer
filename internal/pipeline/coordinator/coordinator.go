package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
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
	cursorRepo      store.CursorRepository
	batchSize       atomic.Int32
	interval        time.Duration
	jobCh           chan<- event.FetchJob
	logger          *slog.Logger
	headProvider    headSequenceProvider
	autoTune        *autotune.Controller
	autoTuneSignals autotune.AutoTuneSignalSource

	// Block-scan mode: used for block-based chains (EVM, BTC) where a single
	// scan per block range replaces per-address cursor iteration.
	blockScanMode bool
	configRepo    store.IndexerConfigRepository
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
	cursorRepo store.CursorRepository,
	batchSize int,
	interval time.Duration,
	jobCh chan<- event.FetchJob,
	logger *slog.Logger,
) *Coordinator {
	c := &Coordinator{
		chain:           chain,
		network:         network,
		watchedAddrRepo: watchedAddrRepo,
		cursorRepo:      cursorRepo,
		interval:        interval,
		jobCh:           jobCh,
		logger:          logger.With("component", "coordinator"),
	}
	c.batchSize.Store(int32(batchSize))
	return c
}

func (c *Coordinator) WithHeadProvider(provider headSequenceProvider) *Coordinator {
	c.headProvider = provider
	return c
}

// WithBlockScanMode enables block-scan mode for block-based chains.
// In this mode, the coordinator reads the pipeline watermark and emits a
// single FetchJob covering the block range for all watched addresses.
func (c *Coordinator) WithBlockScanMode(configRepo store.IndexerConfigRepository) *Coordinator {
	c.blockScanMode = true
	c.configRepo = configRepo
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
// Returns true if the caller should reset the ticker.
func (c *Coordinator) UpdateInterval(newInterval time.Duration) bool {
	if newInterval <= 0 {
		return false
	}
	old := c.interval
	c.interval = newInterval
	if old != newInterval {
		c.logger.Info("coordinator interval updated", "old", old, "new", newInterval)
		return true
	}
	return false
}

func (c *Coordinator) Run(ctx context.Context) error {
	c.logger.Info("coordinator started", "chain", c.chain, "network", c.network, "interval", c.interval)

	ticker := time.NewTicker(c.interval)
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

	if c.blockScanMode {
		return c.tickBlockScan(ctx, span)
	}

	addresses, err := c.watchedAddrRepo.GetActive(ctx, c.chain, c.network)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if len(addresses) == 0 {
		return nil
	}

	fetchCutoffSeq := int64(0)
	if c.headProvider != nil {
		fetchCutoffSeq, err = c.headProvider.GetHeadSequence(ctx)
		if err != nil {
			err = fmt.Errorf("resolve tick cutoff head: %w", err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		if fetchCutoffSeq < 0 {
			fetchCutoffSeq = 0
		}
	}

	groups := groupWatchedAddresses(c.chain, c.network, addresses)
	c.logger.Debug("creating fetch jobs", "address_count", len(addresses), "fan_in_group_count", len(groups))

	jobs := make([]event.FetchJob, 0, len(groups))
	minCursorSequence := int64(0)
	hasMinCursor := false
	for _, group := range groups {
		candidates := make([]watchedAddressCandidate, 0, len(group.members))
		for _, member := range group.members {
			cursor, err := c.cursorRepo.Get(ctx, c.chain, c.network, member.Address)
			if err != nil {
				return fmt.Errorf("get cursor %s: %w", member.Address, err)
			}
			reconciledCursor, recoveryMode, err := reconcileCheckpointCursor(c.chain, c.network, member.Address, cursor)
			if err != nil {
				return fmt.Errorf("checkpoint integrity validation failed for %s: %w", member.Address, err)
			}
			if recoveryMode != "" {
				c.logger.Warn("checkpoint integrity recovery applied",
					"mode", recoveryMode,
					"chain", c.chain,
					"network", c.network,
					"address", member.Address,
				)
			}
			candidates = append(candidates, watchedAddressCandidate{
				address: member,
				cursor:  reconciledCursor,
			})
		}

		representative, cursorValue, cursorSequence := resolveLagAwareCandidate(c.chain, c.network, group.identity, candidates)
		if len(group.members) > 1 {
			c.logger.Warn("coordinator fan-in overlap collision resolved deterministically",
				"chain", c.chain,
				"network", c.network,
				"identity", group.identity,
				"candidate_count", len(candidates),
				"candidate_addresses", fanInCandidateAddresses(candidates),
				"candidate_scope_keys", fanInCandidateScopeKeys(c.chain, c.network, candidates),
				"candidate_cursor_sequences", fanInCandidateCursorSequences(candidates),
				"candidate_cursor_values", fanInCandidateCursorValues(c.chain, candidates),
				"selected_address", representative.address.Address,
				"selected_scope_key", stableAddressScopeOrderKey(c.chain, c.network, representative.address.Address),
				"selected_cursor_sequence", cursorSequence,
				"selected_cursor_value", derefCursorValue(cursorValue),
			)
		}

		if !hasMinCursor || cursorSequence < minCursorSequence {
			minCursorSequence = cursorSequence
			hasMinCursor = true
		}

		jobs = append(jobs, event.FetchJob{
			Chain:          c.chain,
			Network:        c.network,
			Address:        representative.address.Address,
			CursorValue:    cursorValue,
			CursorSequence: cursorSequence,
			FetchCutoffSeq: fetchCutoffSeq,
			WalletID:       representative.address.WalletID,
			OrgID:          representative.address.OrganizationID,
		})
	}

	batchSize := int(c.batchSize.Load())
	if c.autoTune != nil && len(jobs) > 0 {
		rpcErrorRateBps := 0
		dbCommitLatencyP95Ms := 0
		if c.autoTuneSignals != nil {
			snapshot := c.autoTuneSignals.Snapshot(c.chain.String(), c.network.String())
			rpcErrorRateBps = snapshot.RPCErrorRateBps
			dbCommitLatencyP95Ms = snapshot.DBCommitLatencyP95Ms
		}

		resolved, diagnostics := c.autoTune.Resolve(autotune.Inputs{
			Chain:                c.chain.String(),
			Network:              c.network.String(),
			HasHeadSignal:        c.headProvider != nil,
			HeadSequence:         fetchCutoffSeq,
			HasMinCursorSignal:   hasMinCursor,
			MinCursorSequence:    minCursorSequence,
			QueueDepth:           len(c.jobCh),
			QueueCapacity:        cap(c.jobCh),
			RPCErrorRateBps:      rpcErrorRateBps,
			DBCommitLatencyP95Ms: dbCommitLatencyP95Ms,
			DecisionEpochMs:      time.Now().UnixMilli(),
		})
		batchSize = resolved
		c.logger.Debug("coordinator auto-tune decision",
			"chain", c.chain,
			"network", c.network,
			"lag_sequence", diagnostics.LagSequence,
			"queue_depth", diagnostics.QueueDepth,
			"queue_capacity", diagnostics.QueueCapacity,
			"telemetry_state", diagnostics.TelemetryState,
			"override_state", diagnostics.OverrideState,
			"signal", diagnostics.Signal,
			"decision", diagnostics.Decision,
			"batch_before", diagnostics.BatchBefore,
			"batch_after", diagnostics.BatchAfter,
			"streak", diagnostics.Streak,
			"cooldown", diagnostics.Cooldown,
			"telemetry_stale_ticks", diagnostics.TelemetryStaleTicks,
			"telemetry_recovery_ticks", diagnostics.TelemetryRecoveryTicks,
			"override_release_ticks", diagnostics.OverrideReleaseTicks,
			"policy_version", diagnostics.PolicyVersion,
			"policy_manifest_digest", diagnostics.PolicyManifestDigest,
			"policy_epoch", diagnostics.PolicyEpoch,
			"policy_activation_ticks", diagnostics.PolicyActivationTicks,
			"decision_inputs_hash", diagnostics.DecisionInputsHash,
			"local_inputs_digest", diagnostics.LocalInputsDigest,
			"decision_inputs_chain_scoped", diagnostics.DecisionInputsChainScoped,
			"decision_scope", diagnostics.DecisionScope,
			"cross_chain_reads", diagnostics.CrossChainReads,
			"cross_chain_writes", diagnostics.CrossChainWrites,
			"changed_peer_cursor", diagnostics.ChangedPeerCursor,
			"changed_peer_watermark", diagnostics.ChangedPeerWatermark,
			"decision_outputs", diagnostics.DecisionOutputs,
			"decision_epoch_ms", diagnostics.DecisionEpochMs,
			"decision_sequence", diagnostics.DecisionSequence,
		)
	}

	for _, job := range jobs {
		job.BatchSize = batchSize
		select {
		case c.jobCh <- job:
			metrics.CoordinatorJobsCreated.WithLabelValues(c.chain.String(), c.network.String()).Inc()
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	span.SetAttributes(
		attribute.Int("address_count", len(addresses)),
		attribute.Int("group_count", len(groups)),
	)

	return nil
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
	if c.configRepo != nil {
		wm, wmErr := c.configRepo.GetWatermark(ctx, c.chain, c.network)
		if wmErr != nil {
			c.logger.Warn("block-scan watermark read failed, starting from 0", "error", wmErr)
		} else if wm != nil && wm.IngestedSequence > 0 {
			startBlock = wm.IngestedSequence + 1
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
	case <-ctx.Done():
		return ctx.Err()
	}

	span.SetAttributes(
		attribute.Int("address_count", len(addresses)),
		attribute.Int64("start_block", startBlock),
		attribute.Int64("end_block", endBlock),
	)

	return nil
}

func reconcileCheckpointCursor(
	chain model.Chain,
	network model.Network,
	address string,
	cursor *model.AddressCursor,
) (*model.AddressCursor, string, error) {
	if cursor == nil {
		return nil, "", nil
	}

	if cursor.Chain != "" && cursor.Chain != chain {
		return nil, "", fmt.Errorf(
			"checkpoint_integrity_failure mode=cross_chain_checkpoint_mixup chain=%s network=%s address=%s detail=chain_mismatch cursor_chain=%s",
			chain, network, address, cursor.Chain,
		)
	}
	if cursor.Network != "" && cursor.Network != network {
		return nil, "", fmt.Errorf(
			"checkpoint_integrity_failure mode=cross_chain_checkpoint_mixup chain=%s network=%s address=%s detail=network_mismatch cursor_network=%s",
			chain, network, address, cursor.Network,
		)
	}
	if strings.TrimSpace(cursor.Address) != "" {
		expectedIdentity := canonicalWatchedAddressIdentity(chain, address)
		actualIdentity := canonicalWatchedAddressIdentity(chain, cursor.Address)
		if expectedIdentity != "" && actualIdentity != "" && expectedIdentity != actualIdentity {
			return nil, "", fmt.Errorf(
				"checkpoint_integrity_failure mode=cross_chain_checkpoint_mixup chain=%s network=%s address=%s detail=address_mismatch cursor_address=%s",
				chain, network, address, strings.TrimSpace(cursor.Address),
			)
		}
	}
	if cursor.CursorValue != nil && isCrossChainCursorValueShape(chain, *cursor.CursorValue) {
		return nil, "", fmt.Errorf(
			"checkpoint_integrity_failure mode=cross_chain_checkpoint_mixup chain=%s network=%s address=%s detail=cursor_shape_mismatch cursor_value=%q",
			chain, network, address, strings.TrimSpace(*cursor.CursorValue),
		)
	}

	reconciled := *cursor
	reconciledCursorValue := identity.CanonicalizeCursorValue(chain, cursor.CursorValue)

	if cursor.CursorSequence < 0 {
		reconciled.CursorValue = nil
		reconciled.CursorSequence = 0
		return &reconciled, "invalid_cursor_sequence", nil
	}

	if cursor.CursorSequence > 0 && reconciledCursorValue == nil {
		reconciled.CursorValue = nil
		reconciled.CursorSequence = 0
		return &reconciled, "truncated_payload", nil
	}

	// A persisted positive sequence with non-zero processed count but nil fetch timestamp
	// indicates a stale/partially-written checkpoint snapshot.
	if cursor.CursorSequence > 0 && cursor.ItemsProcessed > 0 && cursor.LastFetchedAt == nil {
		reconciled.CursorValue = nil
		reconciled.CursorSequence = 0
		return &reconciled, "stale_cursor_snapshot", nil
	}

	reconciled.CursorValue = reconciledCursorValue
	return &reconciled, "", nil
}

type watchedAddressGroup struct {
	identity string
	members  []model.WatchedAddress
}

type watchedAddressCandidate struct {
	address model.WatchedAddress
	cursor  *model.AddressCursor
}

func groupWatchedAddresses(chain model.Chain, network model.Network, addresses []model.WatchedAddress) []watchedAddressGroup {
	if len(addresses) == 0 {
		return nil
	}

	groupsByIdentity := make(map[string][]model.WatchedAddress, len(addresses))
	for _, addr := range addresses {
		identity := canonicalWatchedAddressIdentity(chain, addr.Address)
		if identity == "" {
			continue
		}
		groupsByIdentity[identity] = append(groupsByIdentity[identity], addr)
	}

	identities := make([]string, 0, len(groupsByIdentity))
	for identity := range groupsByIdentity {
		identities = append(identities, identity)
	}
	sort.Strings(identities)

	groups := make([]watchedAddressGroup, 0, len(identities))
	for _, identity := range identities {
		members := append([]model.WatchedAddress(nil), groupsByIdentity[identity]...)
		sort.Slice(members, func(i, j int) bool {
			leftKey := stableAddressScopeOrderKey(chain, network, members[i].Address)
			rightKey := stableAddressScopeOrderKey(chain, network, members[j].Address)
			if leftKey != rightKey {
				return leftKey < rightKey
			}
			leftTrimmed := strings.TrimSpace(members[i].Address)
			rightTrimmed := strings.TrimSpace(members[j].Address)
			if leftTrimmed != rightTrimmed {
				return leftTrimmed < rightTrimmed
			}
			return members[i].Address < members[j].Address
		})
		groups = append(groups, watchedAddressGroup{
			identity: identity,
			members:  members,
		})
	}

	return groups
}

// fanInCandidateAddresses returns raw candidate addresses in deterministic candidate order.
func fanInCandidateAddresses(candidates []watchedAddressCandidate) []string {
	addresses := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		addresses = append(addresses, candidate.address.Address)
	}
	return addresses
}

func fanInCandidateScopeKeys(chain model.Chain, network model.Network, candidates []watchedAddressCandidate) []string {
	keys := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		keys = append(keys, stableAddressScopeOrderKey(chain, network, candidate.address.Address))
	}
	return keys
}

func fanInCandidateCursorSequences(candidates []watchedAddressCandidate) []int64 {
	sequences := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		sequences = append(sequences, lagAwareCursorSequence(candidate.cursor))
	}
	return sequences
}

func fanInCandidateCursorValues(chain model.Chain, candidates []watchedAddressCandidate) []string {
	cursorValues := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		cursorValue := lagAwareCursorValue(chain, candidate.cursor)
		if cursorValue == nil {
			cursorValues = append(cursorValues, "")
			continue
		}
		cursorValues = append(cursorValues, *cursorValue)
	}
	return cursorValues
}

func derefCursorValue(cursor *string) string {
	if cursor == nil {
		return ""
	}
	return *cursor
}

func resolveLagAwareCandidate(
	chain model.Chain,
	network model.Network,
	identity string,
	candidates []watchedAddressCandidate,
) (watchedAddressCandidate, *string, int64) {
	if len(candidates) == 0 {
		return watchedAddressCandidate{}, nil, 0
	}

	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if shouldReplaceLagAwareCandidate(chain, network, identity, best, candidate) {
			best = candidate
		}
	}

	cursorValue, cursorSequence := resolveCandidateCursor(chain, best)
	return best, cursorValue, cursorSequence
}

func shouldReplaceLagAwareCandidate(
	chain model.Chain,
	network model.Network,
	identity string,
	existing watchedAddressCandidate,
	incoming watchedAddressCandidate,
) bool {
	existingSeq := lagAwareCursorSequence(existing.cursor)
	incomingSeq := lagAwareCursorSequence(incoming.cursor)
	if existingSeq != incomingSeq {
		return incomingSeq < existingSeq
	}

	existingCanonical := isCanonicalAddressForm(chain, identity, existing.address.Address)
	incomingCanonical := isCanonicalAddressForm(chain, identity, incoming.address.Address)
	if existingCanonical != incomingCanonical {
		return incomingCanonical
	}

	existingExactCanonical := isCanonicalAddressExactForm(identity, existing.address.Address)
	incomingExactCanonical := isCanonicalAddressExactForm(identity, incoming.address.Address)
	if existingExactCanonical != incomingExactCanonical {
		return incomingExactCanonical
	}

	existingCursor := lagAwareCursorValue(chain, existing.cursor)
	incomingCursor := lagAwareCursorValue(chain, incoming.cursor)
	if cmp := compareLagAwareCursorValue(incomingCursor, existingCursor); cmp != 0 {
		return cmp < 0
	}

	existingScopeKey := stableAddressScopeOrderKey(chain, network, existing.address.Address)
	incomingScopeKey := stableAddressScopeOrderKey(chain, network, incoming.address.Address)
	if existingScopeKey != incomingScopeKey {
		return incomingScopeKey < existingScopeKey
	}

	existingKey := stableAddressOrderKey(chain, existing.address.Address)
	incomingKey := stableAddressOrderKey(chain, incoming.address.Address)
	if existingKey != incomingKey {
		return incomingKey < existingKey
	}

	existingTrimmed := strings.TrimSpace(existing.address.Address)
	incomingTrimmed := strings.TrimSpace(incoming.address.Address)
	if existingTrimmed != incomingTrimmed {
		return incomingTrimmed < existingTrimmed
	}

	existingWhitespace := len(existing.address.Address) - len(existingTrimmed)
	incomingWhitespace := len(incoming.address.Address) - len(incomingTrimmed)
	if existingWhitespace != incomingWhitespace {
		return incomingWhitespace < existingWhitespace
	}

	if len(incoming.address.Address) != len(existing.address.Address) {
		return len(incoming.address.Address) < len(existing.address.Address)
	}

	return incoming.address.Address < existing.address.Address
}

func lagAwareCursorSequence(cursor *model.AddressCursor) int64 {
	if cursor == nil || cursor.CursorSequence < 0 {
		return 0
	}
	return cursor.CursorSequence
}

func lagAwareCursorValue(chain model.Chain, cursor *model.AddressCursor) *string {
	if cursor == nil {
		return nil
	}
	return identity.CanonicalizeCursorValue(chain, cursor.CursorValue)
}

func resolveCandidateCursor(chain model.Chain, candidate watchedAddressCandidate) (*string, int64) {
	return lagAwareCursorValue(chain, candidate.cursor), lagAwareCursorSequence(candidate.cursor)
}

func compareLagAwareCursorValue(left, right *string) int {
	switch {
	case left == nil && right == nil:
		return 0
	case left == nil:
		return -1
	case right == nil:
		return 1
	case *left < *right:
		return -1
	case *left > *right:
		return 1
	default:
		return 0
	}
}

func isCanonicalAddressForm(chain model.Chain, identity, address string) bool {
	return canonicalWatchedAddressIdentity(chain, address) == identity &&
		strings.TrimSpace(address) == identity
}

func isCanonicalAddressExactForm(identity, address string) bool {
	trimmed := strings.TrimSpace(address)
	return trimmed == identity && trimmed == address
}

func stableAddressScopeOrderKey(chain model.Chain, network model.Network, address string) string {
	return fmt.Sprintf("%s|%s|%s", chain, network, stableAddressOrderKey(chain, address))
}

func stableAddressOrderKey(chain model.Chain, address string) string {
	trimmed := strings.TrimSpace(address)
	if identity.IsEVMChain(chain) {
		return strings.ToLower(trimmed)
	}
	return trimmed
}

func canonicalWatchedAddressIdentity(chain model.Chain, address string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return ""
	}
	if !identity.IsEVMChain(chain) {
		return trimmed
	}

	withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
	if withoutPrefix == "" {
		return ""
	}
	if identity.IsHexString(withoutPrefix) {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	return strings.ToLower(trimmed)
}

func isCrossChainCursorValueShape(chain model.Chain, cursor string) bool {
	trimmed := strings.TrimSpace(cursor)
	if trimmed == "" {
		return false
	}

	if identity.IsEVMChain(chain) {
		if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
			return false
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "sig-") {
			return true
		}
		return len(trimmed) >= 32 && len(trimmed) <= 128 && isBase58String(trimmed)
	}

	return looksLikeEVMHash(trimmed)
}

func looksLikeEVMHash(value string) bool {
	if !(strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X")) {
		return false
	}
	hexPortion := strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	return hexPortion != "" && identity.IsHexString(hexPortion)
}

func isBase58String(v string) bool {
	for _, ch := range v {
		switch {
		case ch >= '1' && ch <= '9':
		case ch >= 'A' && ch <= 'H':
		case ch >= 'J' && ch <= 'N':
		case ch >= 'P' && ch <= 'Z':
		case ch >= 'a' && ch <= 'k':
		case ch >= 'm' && ch <= 'z':
		default:
			return false
		}
	}
	return true
}

func maxIntValue(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
