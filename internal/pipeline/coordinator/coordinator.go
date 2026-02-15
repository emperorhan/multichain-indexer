package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

// Coordinator iterates over watched addresses and creates FetchJobs.
type Coordinator struct {
	chain           model.Chain
	network         model.Network
	watchedAddrRepo store.WatchedAddressRepository
	cursorRepo      store.CursorRepository
	batchSize       int
	interval        time.Duration
	jobCh           chan<- event.FetchJob
	logger          *slog.Logger
	headProvider    headSequenceProvider
	autoTune        *autoTuneController
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
	return &Coordinator{
		chain:           chain,
		network:         network,
		watchedAddrRepo: watchedAddrRepo,
		cursorRepo:      cursorRepo,
		batchSize:       batchSize,
		interval:        interval,
		jobCh:           jobCh,
		logger:          logger.With("component", "coordinator"),
	}
}

func (c *Coordinator) WithHeadProvider(provider headSequenceProvider) *Coordinator {
	c.headProvider = provider
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
	override := c.autoTune.exportOverrideTransition()
	policy := c.autoTune.exportPolicyTransition()
	return &AutoTuneRestartState{
		Chain:                     c.chain,
		Network:                   c.network,
		BatchSize:                 c.autoTune.currentBatch,
		OverrideManualActive:      override.WasManualOverride,
		OverrideReleaseRemaining:  override.ReleaseHoldRemaining,
		PolicyVersion:             policy.Version,
		PolicyManifestDigest:      policy.ManifestDigest,
		PolicyEpoch:               policy.Epoch,
		PolicyActivationRemaining: policy.ActivationHoldRemaining,
	}
}

func (c *Coordinator) withAutoTune(cfg AutoTuneConfig, warmState *AutoTuneRestartState) *Coordinator {
	transitionMode := "cold_start"
	var seedBatch *int
	seedReason := "none"
	previousAutoTune := c.autoTune
	overrideTransition := autoTuneOverrideTransition{}
	policyTransition := autoTunePolicyTransition{}

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
			overrideTransition = autoTuneOverrideTransition{
				WasManualOverride:    warmState.OverrideManualActive,
				ReleaseHoldRemaining: maxInt(warmState.OverrideReleaseRemaining, 0),
			}
			if strings.TrimSpace(warmState.PolicyVersion) != "" ||
				strings.TrimSpace(warmState.PolicyManifestDigest) != "" ||
				warmState.PolicyEpoch > 0 ||
				warmState.PolicyActivationRemaining > 0 {
				policyEpoch := warmState.PolicyEpoch
				if policyEpoch < 0 {
					policyEpoch = 0
				}
				policyTransition = autoTunePolicyTransition{
					HasState:                true,
					Version:                 warmState.PolicyVersion,
					ManifestDigest:          warmState.PolicyManifestDigest,
					Epoch:                   policyEpoch,
					ActivationHoldRemaining: maxInt(warmState.PolicyActivationRemaining, 0),
				}
			}
		}
	} else if previousAutoTune != nil {
		seed := previousAutoTune.currentBatch
		seedBatch = &seed
		transitionMode = "profile_transition"
		seedReason = "profile_transition_seed"
		overrideTransition = previousAutoTune.exportOverrideTransition()
		policyTransition = previousAutoTune.exportPolicyTransition()
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
		c.autoTune = newAutoTuneControllerWithRestartSeed(c.batchSize, cfg, seedBatch)
	} else {
		c.autoTune = newAutoTuneControllerWithSeed(c.batchSize, cfg, seedBatch)
	}
	if c.autoTune != nil {
		c.autoTune.reconcileOverrideTransition(overrideTransition)
		c.autoTune.reconcilePolicyTransition(policyTransition)
		c.logger.Info("coordinator auto-tune enabled",
			"chain", c.chain,
			"network", c.network,
			"min_batch", c.autoTune.minBatchSize,
			"max_batch", c.autoTune.maxBatchSize,
			"step_up", c.autoTune.stepUp,
			"step_down", c.autoTune.stepDown,
			"lag_high", c.autoTune.lagHighWatermark,
			"lag_low", c.autoTune.lagLowWatermark,
			"queue_high_pct", c.autoTune.queueHighWatermarkPct,
			"queue_low_pct", c.autoTune.queueLowWatermarkPct,
			"hysteresis_ticks", c.autoTune.hysteresisTicks,
			"cooldown_ticks", c.autoTune.cooldownTicks,
			"telemetry_stale_ticks", c.autoTune.telemetryStaleTicks,
			"telemetry_recovery_ticks", c.autoTune.telemetryRecoveryTicks,
			"operator_override_batch", c.autoTune.operatorOverrideBatch,
			"operator_release_hold_ticks", c.autoTune.operatorReleaseHold,
			"operator_release_remaining", c.autoTune.overrideReleaseLeft,
			"policy_version", c.autoTune.policyVersion,
			"policy_manifest_digest", c.autoTune.policyManifestDigest,
			"policy_epoch", c.autoTune.policyEpoch,
			"policy_activation_hold_ticks", c.autoTune.policyActivationHold,
			"policy_activation_remaining", c.autoTune.policyActivationLeft,
			"profile_transition", transitionMode == "profile_transition",
			"transition_mode", transitionMode,
			"seed_reason", seedReason,
			"seed_batch", c.autoTune.currentBatch,
		)
	}
	return c
}

func (c *Coordinator) Run(ctx context.Context) error {
	c.logger.Info("coordinator started", "chain", c.chain, "network", c.network, "interval", c.interval)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Run immediately on start, then on interval
	if err := c.tick(ctx); err != nil {
		panic(fmt.Sprintf("coordinator tick failed: %v", err))
	}

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("coordinator stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := c.tick(ctx); err != nil {
				panic(fmt.Sprintf("coordinator tick failed: %v", err))
			}
		}
	}
}

func (c *Coordinator) tick(ctx context.Context) error {
	addresses, err := c.watchedAddrRepo.GetActive(ctx, c.chain, c.network)
	if err != nil {
		return err
	}
	if len(addresses) == 0 {
		return nil
	}

	fetchCutoffSeq := int64(0)
	if c.headProvider != nil {
		fetchCutoffSeq, err = c.headProvider.GetHeadSequence(ctx)
		if err != nil {
			return fmt.Errorf("resolve tick cutoff head: %w", err)
		}
		if fetchCutoffSeq < 0 {
			fetchCutoffSeq = 0
		}
	}

	groups := groupWatchedAddresses(c.chain, addresses)
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

		representative, cursorValue, cursorSequence := resolveLagAwareCandidate(c.chain, group.identity, candidates)

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

	batchSize := c.batchSize
	if c.autoTune != nil && len(jobs) > 0 {
		resolved, diagnostics := c.autoTune.Resolve(autoTuneInputs{
			HasHeadSignal:      c.headProvider != nil,
			HeadSequence:       fetchCutoffSeq,
			HasMinCursorSignal: hasMinCursor,
			MinCursorSequence:  minCursorSequence,
			QueueDepth:         len(c.jobCh),
			QueueCapacity:      cap(c.jobCh),
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
		)
	}

	for _, job := range jobs {
		job.BatchSize = batchSize
		select {
		case c.jobCh <- job:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

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
	reconciledCursorValue := canonicalizeCursorValue(chain, cursor.CursorValue)

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

func groupWatchedAddresses(chain model.Chain, addresses []model.WatchedAddress) []watchedAddressGroup {
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
			leftKey := stableAddressOrderKey(chain, members[i].Address)
			rightKey := stableAddressOrderKey(chain, members[j].Address)
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

func resolveLagAwareCandidate(
	chain model.Chain,
	identity string,
	candidates []watchedAddressCandidate,
) (watchedAddressCandidate, *string, int64) {
	if len(candidates) == 0 {
		return watchedAddressCandidate{}, nil, 0
	}

	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if shouldReplaceLagAwareCandidate(chain, identity, best, candidate) {
			best = candidate
		}
	}

	cursorValue, cursorSequence := resolveCandidateCursor(chain, best)
	return best, cursorValue, cursorSequence
}

func shouldReplaceLagAwareCandidate(
	chain model.Chain,
	identity string,
	existing watchedAddressCandidate,
	incoming watchedAddressCandidate,
) bool {
	existingSeq := lagAwareCursorSequence(existing.cursor)
	incomingSeq := lagAwareCursorSequence(incoming.cursor)
	if existingSeq != incomingSeq {
		return incomingSeq < existingSeq
	}

	existingCursor := lagAwareCursorValue(chain, existing.cursor)
	incomingCursor := lagAwareCursorValue(chain, incoming.cursor)
	if cmp := compareLagAwareCursorValue(incomingCursor, existingCursor); cmp != 0 {
		return cmp < 0
	}

	existingCanonical := isCanonicalAddressForm(chain, identity, existing.address.Address)
	incomingCanonical := isCanonicalAddressForm(chain, identity, incoming.address.Address)
	if existingCanonical != incomingCanonical {
		return incomingCanonical
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
	return canonicalizeCursorValue(chain, cursor.CursorValue)
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

func stableAddressOrderKey(chain model.Chain, address string) string {
	trimmed := strings.TrimSpace(address)
	if isEVMChain(chain) {
		return strings.ToLower(trimmed)
	}
	return trimmed
}

func canonicalWatchedAddressIdentity(chain model.Chain, address string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return ""
	}
	if !isEVMChain(chain) {
		return trimmed
	}

	withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
	if withoutPrefix == "" {
		return ""
	}
	if isHexString(withoutPrefix) {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	return strings.ToLower(trimmed)
}

func canonicalizeCursorValue(chain model.Chain, cursor *string) *string {
	if cursor == nil {
		return nil
	}
	value := canonicalSignatureIdentity(chain, *cursor)
	if value == "" {
		return nil
	}
	return &value
}

func canonicalSignatureIdentity(chain model.Chain, hash string) string {
	trimmed := strings.TrimSpace(hash)
	if trimmed == "" {
		return ""
	}
	if !isEVMChain(chain) {
		return trimmed
	}

	withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
	if withoutPrefix == "" {
		return ""
	}
	if isHexString(withoutPrefix) {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	return trimmed
}

func isEVMChain(chain model.Chain) bool {
	return chain == model.ChainBase || chain == model.ChainEthereum
}

func isHexString(v string) bool {
	for _, ch := range v {
		switch {
		case ch >= '0' && ch <= '9':
		case ch >= 'a' && ch <= 'f':
		case ch >= 'A' && ch <= 'F':
		default:
			return false
		}
	}
	return true
}

func isCrossChainCursorValueShape(chain model.Chain, cursor string) bool {
	trimmed := strings.TrimSpace(cursor)
	if trimmed == "" {
		return false
	}

	if isEVMChain(chain) {
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
	return hexPortion != "" && isHexString(hexPortion)
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
