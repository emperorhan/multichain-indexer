package ingester

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/addressindex"
	"github.com/emperorhan/multichain-indexer/internal/cache"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator/autotune"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/replay"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/emperorhan/multichain-indexer/internal/tracing"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelTrace "go.opentelemetry.io/otel/trace"
)

const (
	defaultProcessRetryMaxAttempts = 3
	defaultRetryDelayInitial       = 100 * time.Millisecond
	defaultRetryDelayMax           = 1 * time.Second
	defaultDeniedCacheCapacity     = 10000
	defaultDeniedCacheTTL          = 5 * time.Minute
)

// Ingester is a single-writer that processes NormalizedBatches into the database.
type Ingester struct {
	db                store.TxBeginner
	txRepo            store.TransactionRepository
	balanceEventRepo  store.BalanceEventRepository
	balanceRepo       store.BalanceRepository
	tokenRepo         store.TokenRepository
	cursorRepo        store.CursorRepository
	configRepo        store.IndexerConfigRepository
	normalizedCh      <-chan event.NormalizedBatch
	reorgCh           <-chan event.ReorgEvent
	finalityCh        <-chan event.FinalityPromotion
	blockRepo         store.IndexedBlockRepository
	logger            *slog.Logger
	autoTuneSignals   autotune.AutoTuneSignalSink
	commitInterleaver CommitInterleaver
	reorgHandler      func(context.Context, *sql.Tx, event.NormalizedBatch) error
	retryMaxAttempts  int
	retryDelayStart   time.Duration
	retryDelayMax     time.Duration
	sleepFn           func(context.Context, time.Duration) error
	deniedCache       *cache.LRU[string, bool]
	replayService     *replay.Service
	watchedAddrRepo   store.WatchedAddressRepository
	addressIndex      addressindex.Index
}

type Option func(*Ingester)

func WithCommitInterleaver(interleaver CommitInterleaver) Option {
	return func(ing *Ingester) {
		ing.commitInterleaver = interleaver
	}
}

func WithAutoTuneSignalSink(sink autotune.AutoTuneSignalSink) Option {
	return func(ing *Ingester) {
		ing.autoTuneSignals = sink
	}
}

func WithReorgHandler(handler func(context.Context, *sql.Tx, event.NormalizedBatch) error) Option {
	return func(ing *Ingester) {
		ing.reorgHandler = handler
	}
}

func WithReorgChannel(ch <-chan event.ReorgEvent) Option {
	return func(ing *Ingester) {
		ing.reorgCh = ch
	}
}

func WithFinalityChannel(ch <-chan event.FinalityPromotion) Option {
	return func(ing *Ingester) {
		ing.finalityCh = ch
	}
}

func WithIndexedBlockRepo(repo store.IndexedBlockRepository) Option {
	return func(ing *Ingester) {
		ing.blockRepo = repo
	}
}

func WithReplayService(svc *replay.Service) Option {
	return func(ing *Ingester) {
		ing.replayService = svc
	}
}

func WithWatchedAddressRepo(repo store.WatchedAddressRepository) Option {
	return func(ing *Ingester) {
		ing.watchedAddrRepo = repo
	}
}

func WithAddressIndex(idx addressindex.Index) Option {
	return func(ing *Ingester) {
		ing.addressIndex = idx
	}
}

func WithRetryConfig(maxAttempts int, delayInitial, delayMax time.Duration) Option {
	return func(ing *Ingester) {
		ing.retryMaxAttempts = maxAttempts
		ing.retryDelayStart = delayInitial
		ing.retryDelayMax = delayMax
	}
}

func WithDeniedCacheConfig(capacity int, ttl time.Duration) Option {
	return func(ing *Ingester) {
		ing.deniedCache = cache.NewLRU[string, bool](capacity, ttl)
	}
}

func New(
	db store.TxBeginner,
	txRepo store.TransactionRepository,
	balanceEventRepo store.BalanceEventRepository,
	balanceRepo store.BalanceRepository,
	tokenRepo store.TokenRepository,
	cursorRepo store.CursorRepository,
	configRepo store.IndexerConfigRepository,
	normalizedCh <-chan event.NormalizedBatch,
	logger *slog.Logger,
	opts ...Option,
) *Ingester {
	ing := &Ingester{
		db:               db,
		txRepo:           txRepo,
		balanceEventRepo: balanceEventRepo,
		balanceRepo:      balanceRepo,
		tokenRepo:        tokenRepo,
		cursorRepo:       cursorRepo,
		configRepo:       configRepo,
		normalizedCh:     normalizedCh,
		logger:           logger.With("component", "ingester"),
		reorgHandler:     nil,
		retryMaxAttempts: defaultProcessRetryMaxAttempts,
		retryDelayStart:  defaultRetryDelayInitial,
		retryDelayMax:    defaultRetryDelayMax,
		sleepFn:          sleepContext,
		deniedCache:      cache.NewLRU[string, bool](defaultDeniedCacheCapacity, defaultDeniedCacheTTL),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(ing)
		}
	}
	return ing
}

func (ing *Ingester) Run(ctx context.Context) error {
	ing.logger.Info("ingester started")

	// Use nil channels if not configured (select will never match nil channels)
	reorgCh := ing.reorgCh
	finalityCh := ing.finalityCh

	for {
		select {
		case <-ctx.Done():
			ing.logger.Info("ingester stopping")
			return ctx.Err()
		case batch, ok := <-ing.normalizedCh:
			if !ok {
				return nil
			}
			spanCtx, span := tracing.Tracer("ingester").Start(ctx, "ingester.processBatch",
				otelTrace.WithAttributes(
					attribute.String("chain", batch.Chain.String()),
					attribute.String("network", batch.Network.String()),
					attribute.String("address", batch.Address),
					attribute.Int("tx_count", len(batch.Transactions)),
				),
			)
			start := time.Now()
			if err := ing.processBatchWithRetry(spanCtx, batch); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.End()
				metrics.IngesterErrors.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
				metrics.IngesterLatency.WithLabelValues(batch.Chain.String(), batch.Network.String()).Observe(time.Since(start).Seconds())
				ing.logger.Error("process batch failed",
					"address", batch.Address,
					"error", err,
				)
				// Fail-fast: return error so errgroup cancels the entire pipeline.
				// The process will restart from the last committed cursor.
				return fmt.Errorf("ingester process batch failed: address=%s: %w", batch.Address, err)
			}
			span.End()
			metrics.IngesterBatchesProcessed.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
			metrics.IngesterLatency.WithLabelValues(batch.Chain.String(), batch.Network.String()).Observe(time.Since(start).Seconds())
		case reorg, ok := <-reorgCh:
			if !ok {
				reorgCh = nil
				continue
			}
			if err := ing.handleReorg(ctx, reorg); err != nil {
				ing.logger.Error("handle reorg failed",
					"chain", reorg.Chain,
					"network", reorg.Network,
					"fork_block", reorg.ForkBlockNumber,
					"error", err,
				)
				return fmt.Errorf("ingester handle reorg failed: %w", err)
			}
		case promo, ok := <-finalityCh:
			if !ok {
				finalityCh = nil
				continue
			}
			if err := ing.handleFinalityPromotion(ctx, promo); err != nil {
				ing.logger.Error("handle finality promotion failed",
					"chain", promo.Chain,
					"network", promo.Network,
					"new_finalized_block", promo.NewFinalizedBlock,
					"error", err,
				)
				return fmt.Errorf("ingester handle finality promotion failed: %w", err)
			}
		}
	}
}

func (ing *Ingester) processBatchWithRetry(ctx context.Context, batch event.NormalizedBatch) error {
	const stage = "ingester.process_batch"

	maxAttempts := ing.effectiveRetryMaxAttempts()
	var lastErr error
	lastDecision := retry.Decision{
		Class:  retry.ClassTerminal,
		Reason: "unset",
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ing.processBatch(ctx, batch); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			lastDecision = retry.Classify(err)
			if !lastDecision.IsTransient() {
				return fmt.Errorf("terminal_failure stage=%s attempt=%d reason=%s: %w", stage, attempt, lastDecision.Reason, err)
			}
			if attempt == maxAttempts {
				break
			}

			ing.logger.Warn("process batch attempt failed; retrying",
				"stage", stage,
				"classification", lastDecision.Class,
				"classification_reason", lastDecision.Reason,
				"address", batch.Address,
				"attempt", attempt,
				"max_attempts", maxAttempts,
				"error", err,
			)
			if err := ing.sleep(ctx, ing.retryDelay(attempt)); err != nil {
				return err
			}
			continue
		}
		return nil
	}

	return fmt.Errorf("transient_recovery_exhausted stage=%s attempts=%d reason=%s: %w", stage, maxAttempts, lastDecision.Reason, lastErr)
}

func isCanonicalityDrift(batch event.NormalizedBatch) bool {
	if batch.PreviousCursorValue == nil || batch.NewCursorValue == nil {
		return false
	}
	if batch.PreviousCursorSequence == 0 {
		return false
	}
	prevIdentity := identity.CanonicalSignatureIdentity(batch.Chain, *batch.PreviousCursorValue)
	if prevIdentity == "" {
		prevIdentity = strings.TrimSpace(*batch.PreviousCursorValue)
	}
	newIdentity := identity.CanonicalSignatureIdentity(batch.Chain, *batch.NewCursorValue)
	if newIdentity == "" {
		newIdentity = strings.TrimSpace(*batch.NewCursorValue)
	}
	if batch.NewCursorSequence < batch.PreviousCursorSequence {
		return true
	}
	if batch.NewCursorSequence == batch.PreviousCursorSequence &&
		prevIdentity != newIdentity {
		// Same-sequence cursor identity changes can occur during legitimate
		// live/backfill overlap reconciliation. Treat these as reorg drift
		// only for explicit rollback marker batches (no decoded transactions).
		return len(batch.Transactions) == 0
	}
	return false
}

func isBTCRestartAnchorReplay(batch event.NormalizedBatch) bool {
	if batch.Chain != model.ChainBTC {
		return false
	}
	if batch.PreviousCursorValue == nil || batch.NewCursorValue == nil {
		return false
	}
	if batch.PreviousCursorSequence != batch.NewCursorSequence || batch.NewCursorSequence <= 0 {
		return false
	}
	if len(batch.Transactions) != 0 {
		return false
	}

	previousCursor := identity.CanonicalSignatureIdentity(batch.Chain, *batch.PreviousCursorValue)
	if previousCursor == "" {
		previousCursor = strings.TrimSpace(*batch.PreviousCursorValue)
	}
	newCursor := identity.CanonicalSignatureIdentity(batch.Chain, *batch.NewCursorValue)
	if newCursor == "" {
		newCursor = strings.TrimSpace(*batch.NewCursorValue)
	}
	if previousCursor == "" || newCursor == "" {
		return false
	}

	return previousCursor != newCursor
}

func rollbackForkCursorSequence(batch event.NormalizedBatch) int64 {
	if batch.NewCursorSequence < batch.PreviousCursorSequence {
		if btcFloor, ok := rollbackBTCEarliestCompetingSequence(batch); ok {
			return btcFloor
		}
		return batch.NewCursorSequence
	}
	return batch.PreviousCursorSequence
}

func withRollbackForkCursor(batch event.NormalizedBatch) event.NormalizedBatch {
	adjusted := batch
	adjusted.PreviousCursorSequence = rollbackForkCursorSequence(batch)
	return adjusted
}

func rollbackBTCEarliestCompetingSequence(batch event.NormalizedBatch) (int64, bool) {
	if batch.Chain != model.ChainBTC || len(batch.Transactions) == 0 {
		return 0, false
	}

	var earliest int64
	for _, tx := range batch.Transactions {
		if tx.BlockCursor <= 0 {
			continue
		}
		if earliest == 0 || tx.BlockCursor < earliest {
			earliest = tx.BlockCursor
		}
	}
	if earliest == 0 || earliest >= batch.NewCursorSequence {
		return 0, false
	}
	return earliest, true
}

func shouldContinueCompetingBranchReplay(batch event.NormalizedBatch) bool {
	return batch.Chain == model.ChainBTC && len(batch.Transactions) > 0
}

func shouldAdvanceCommitCheckpoint(batch event.NormalizedBatch) bool {
	if len(batch.Transactions) > 0 {
		return true
	}
	if batch.NewCursorSequence != batch.PreviousCursorSequence {
		return true
	}

	previousCursor := identity.CanonicalizeCursorValue(batch.Chain, batch.PreviousCursorValue)
	newCursor := identity.CanonicalizeCursorValue(batch.Chain, batch.NewCursorValue)
	if previousCursor == nil && newCursor == nil {
		return false
	}
	if previousCursor == nil || newCursor == nil {
		return true
	}
	return *previousCursor != *newCursor
}

func (ing *Ingester) processBatch(ctx context.Context, batch event.NormalizedBatch) error {
	if ing.reorgHandler == nil {
		ing.reorgHandler = ing.rollbackCanonicalityDrift
	}
	batch.PreviousCursorValue = identity.CanonicalizeCursorValue(batch.Chain, batch.PreviousCursorValue)
	batch.NewCursorValue = identity.CanonicalizeCursorValue(batch.Chain, batch.NewCursorValue)
	advanceCommitCheckpoint := shouldAdvanceCommitCheckpoint(batch)
	committed := false

	releaseInterleave := func(bool) {}
	if ing.commitInterleaver != nil {
		release, err := ing.commitInterleaver.Acquire(ctx, batch.Chain, batch.Network)
		if err != nil {
			return err
		}
		releaseInterleave = release
	}
	defer func() {
		releaseInterleave(committed && advanceCommitCheckpoint)
	}()

	dbTx, err := ing.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if committed {
			return
		}
		if rbErr := dbTx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
			ing.logger.Warn("rollback failed", "error", rbErr)
		}
	}()

	if isCanonicalityDrift(batch) {
		if isBTCRestartAnchorReplay(batch) {
			ing.logger.Info("rollback-anchor marker consumed without rewind",
				"address", batch.Address,
				"previous_cursor", batch.PreviousCursorValue,
				"new_cursor", batch.NewCursorValue,
			)
		} else {
			rollbackBatch := withRollbackForkCursor(batch)
			if err := ing.reorgHandler(ctx, dbTx, rollbackBatch); err != nil {
				return fmt.Errorf("handle reorg path: %w", err)
			}

			if shouldContinueCompetingBranchReplay(batch) {
				ing.logger.Info("rollback path executed; continuing with competing branch replay",
					"address", batch.Address,
					"previous_cursor", batch.PreviousCursorValue,
					"new_cursor", batch.NewCursorValue,
					"fork_cursor_sequence", rollbackBatch.PreviousCursorSequence,
					"replacement_txs", len(batch.Transactions),
				)
			} else {
				commitStart := time.Now()
				if err := dbTx.Commit(); err != nil {
					return fmt.Errorf("commit rollback path: %w", err)
				}
				committed = true
				ing.recordCommitLatencyMs(batch.Chain.String(), batch.Network.String(), time.Since(commitStart).Milliseconds())

				ing.logger.Info("rollback path executed",
					"address", batch.Address,
					"previous_cursor", batch.PreviousCursorValue,
					"new_cursor", batch.NewCursorValue,
					"fork_cursor_sequence", rollbackBatch.PreviousCursorSequence,
				)
				return nil
			}
		}
	}

	// Block-scan mode: resolve per-address wallet/org mapping.
	// Prefer addressIndex (O(1) bloom → LRU → DB) over full table load.
	type addrMeta struct {
		walletID *string
		orgID    *string
	}
	var blockScanAddrMap map[string]addrMeta
	if batch.BlockScanMode && ing.addressIndex == nil && ing.watchedAddrRepo != nil {
		// Fallback: load all active addresses (legacy path)
		activeAddrs, err := ing.watchedAddrRepo.GetActive(ctx, batch.Chain, batch.Network)
		if err != nil {
			return fmt.Errorf("fetch watched addresses for block-scan: %w", err)
		}
		blockScanAddrMap = make(map[string]addrMeta, len(activeAddrs))
		for _, wa := range activeAddrs {
			blockScanAddrMap[wa.Address] = addrMeta{
				walletID: wa.WalletID,
				orgID:    wa.OrganizationID,
			}
		}
	}

	// ========== PHASE 1: Memory Collect ==========
	_, phase1Span := tracing.Tracer("ingester").Start(ctx, "ingester.phase1_collect")
	// Deduplicate transactions and tokens, collect balance event metadata.

	txModelsByHash := make(map[string]*model.Transaction, len(batch.Transactions))
	var txModels []*model.Transaction
	tokenModelsByContract := make(map[string]*model.Token)
	var tokenModels []*model.Token
	contractsToCheck := make(map[string]struct{})

	type eventContext struct {
		ntx             event.NormalizedTransaction
		be              event.NormalizedBalanceEvent
		canonicalTxHash string
	}
	var allEvents []eventContext

	for _, ntx := range batch.Transactions {
		canonicalTxHash := identity.CanonicalSignatureIdentity(batch.Chain, ntx.TxHash)
		if canonicalTxHash == "" {
			canonicalTxHash = strings.TrimSpace(ntx.TxHash)
		}

		if _, exists := txModelsByHash[canonicalTxHash]; !exists {
			txModel := &model.Transaction{
				Chain:       batch.Chain,
				Network:     batch.Network,
				TxHash:      canonicalTxHash,
				BlockCursor: ntx.BlockCursor,
				BlockTime:   ntx.BlockTime,
				FeeAmount:   ntx.FeeAmount,
				FeePayer:    ntx.FeePayer,
				Status:      ntx.Status,
				Err:         ntx.Err,
				ChainData:   ntx.ChainData,
				BlockHash:   ntx.BlockHash,
				ParentHash:  ntx.ParentHash,
			}
			if txModel.ChainData == nil {
				txModel.ChainData = json.RawMessage("{}")
			}
			txModelsByHash[canonicalTxHash] = txModel
			txModels = append(txModels, txModel)
		}

		for _, be := range ntx.BalanceEvents {
			if _, exists := tokenModelsByContract[be.ContractAddress]; !exists {
				tokenModel := &model.Token{
					Chain:           batch.Chain,
					Network:         batch.Network,
					ContractAddress: be.ContractAddress,
					Symbol:          defaultTokenSymbol(be),
					Name:            defaultTokenName(be),
					Decimals:        be.TokenDecimals,
					TokenType:       be.TokenType,
					ChainData:       json.RawMessage("{}"),
				}
				tokenModelsByContract[be.ContractAddress] = tokenModel
				tokenModels = append(tokenModels, tokenModel)
			}
			contractsToCheck[be.ContractAddress] = struct{}{}
			allEvents = append(allEvents, eventContext{ntx: ntx, be: be, canonicalTxHash: canonicalTxHash})
		}

		// GAP-6: For failed transactions with no balance events but a non-zero fee,
		// generate a synthetic fee-only withdrawal event. This ensures gas costs
		// from reverted transactions are tracked in the balance ledger.
		if ntx.Status == model.TxStatusFailed && len(ntx.BalanceEvents) == 0 &&
			ntx.FeeAmount != "" && ntx.FeeAmount != "0" && ntx.FeePayer != "" {
			feeOnlyEvent := buildFeeOnlyEvent(ntx, batch)
			nativeContract := nativeTokenContract(batch.Chain)
			if _, exists := tokenModelsByContract[nativeContract]; !exists {
				tokenModel := &model.Token{
					Chain:           batch.Chain,
					Network:         batch.Network,
					ContractAddress: nativeContract,
					Symbol:          nativeTokenSymbol(batch.Chain),
					Name:            nativeTokenName(batch.Chain),
					Decimals:        nativeTokenDecimals(batch.Chain),
					TokenType:       model.TokenTypeNative,
					ChainData:       json.RawMessage("{}"),
				}
				tokenModelsByContract[nativeContract] = tokenModel
				tokenModels = append(tokenModels, tokenModel)
			}
			contractsToCheck[nativeContract] = struct{}{}
			allEvents = append(allEvents, eventContext{ntx: ntx, be: feeOnlyEvent, canonicalTxHash: canonicalTxHash})
		}
	}

	phase1Span.SetAttributes(
		attribute.Int("unique_transactions", len(txModels)),
		attribute.Int("unique_tokens", len(tokenModels)),
		attribute.Int("total_events", len(allEvents)),
	)
	phase1Span.End()

	// ========== PHASE 2: Bulk Pre-fetch (2-3 DB queries) ==========
	_, phase2Span := tracing.Tracer("ingester").Start(ctx, "ingester.phase2_prefetch")

	// 2a. Bulk upsert transactions → txID map
	txIDMap, err := ing.txRepo.BulkUpsertTx(ctx, dbTx, txModels)
	if err != nil {
		phase2Span.RecordError(err)
		phase2Span.SetStatus(codes.Error, err.Error())
		phase2Span.End()
		return fmt.Errorf("bulk upsert transactions: %w", err)
	}

	// 2b. Bulk upsert tokens → tokenID map
	tokenIDMap, err := ing.tokenRepo.BulkUpsertTx(ctx, dbTx, tokenModels)
	if err != nil {
		phase2Span.RecordError(err)
		phase2Span.SetStatus(codes.Error, err.Error())
		phase2Span.End()
		return fmt.Errorf("bulk upsert tokens: %w", err)
	}

	// 2c. Bulk denied check (LRU cache first, then DB for misses)
	uncachedContracts := make([]string, 0)
	cachedDenied := make(map[string]bool)
	for contract := range contractsToCheck {
		deniedKey := fmt.Sprintf("%s:%s:%s", batch.Chain, batch.Network, contract)
		if denied, ok := ing.deniedCache.Get(deniedKey); ok {
			cachedDenied[contract] = denied
			metrics.DeniedCacheHits.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
		} else {
			uncachedContracts = append(uncachedContracts, contract)
			metrics.DeniedCacheMisses.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
		}
	}
	var dbDenied map[string]bool
	if len(uncachedContracts) > 0 {
		dbDenied, err = ing.tokenRepo.BulkIsDeniedTx(ctx, dbTx, batch.Chain, batch.Network, uncachedContracts)
		if err != nil {
			phase2Span.RecordError(err)
			phase2Span.SetStatus(codes.Error, err.Error())
			phase2Span.End()
			return fmt.Errorf("bulk check denied tokens: %w", err)
		}
		for _, contract := range uncachedContracts {
			denied := dbDenied[contract]
			deniedKey := fmt.Sprintf("%s:%s:%s", batch.Chain, batch.Network, contract)
			ing.deniedCache.Put(deniedKey, denied)
			cachedDenied[contract] = denied
		}
	}

	// 2d. Bulk pre-fetch balances for all events
	balanceKeys := make([]store.BalanceKey, 0, len(allEvents))
	balanceKeySet := make(map[store.BalanceKey]struct{})
	for _, ec := range allEvents {
		tokenID, ok := tokenIDMap[ec.be.ContractAddress]
		if !ok {
			continue
		}
		bk := store.BalanceKey{Address: ec.be.Address, TokenID: tokenID, BalanceType: ""}
		if _, exists := balanceKeySet[bk]; !exists {
			balanceKeySet[bk] = struct{}{}
			balanceKeys = append(balanceKeys, bk)
		}
	}
	var balanceMap map[store.BalanceKey]store.BalanceInfo
	if len(balanceKeys) > 0 {
		balanceMap, err = ing.balanceRepo.BulkGetAmountWithExistsTx(ctx, dbTx, batch.Chain, batch.Network, balanceKeys)
		if err != nil {
			phase2Span.RecordError(err)
			phase2Span.SetStatus(codes.Error, err.Error())
			phase2Span.End()
			return fmt.Errorf("bulk get balances: %w", err)
		}
	} else {
		balanceMap = make(map[store.BalanceKey]store.BalanceInfo)
	}

	phase2Span.SetAttributes(
		attribute.Int("cached_denied", len(cachedDenied)),
		attribute.Int("balance_keys", len(balanceKeys)),
	)
	phase2Span.End()

	// ========== PHASE 3: In-Memory Processing (zero DB calls for normal path) ==========
	_, phase3Span := tracing.Tracer("ingester").Start(ctx, "ingester.phase3_process")

	// Track in-memory balance accumulation for accurate balance_before/after across events in same batch
	type balanceAccum struct {
		amount string
		exists bool
	}
	inMemoryBalances := make(map[store.BalanceKey]*balanceAccum, len(balanceMap))
	for bk, bi := range balanceMap {
		inMemoryBalances[bk] = &balanceAccum{amount: bi.Amount, exists: bi.Exists}
	}

	type adjustmentKey struct {
		store.BalanceKey
		walletID *string
		orgID    *string
	}
	type adjustmentAccum struct {
		delta   *big.Int
		cursor  int64
		txHash  string
	}
	aggregatedDeltas := make(map[adjustmentKey]*adjustmentAccum)

	var eventModels []*model.BalanceEvent
	var totalEvents int

	for _, ec := range allEvents {
		tokenID, ok := tokenIDMap[ec.be.ContractAddress]
		if !ok {
			continue
		}
		txID, ok := txIDMap[ec.canonicalTxHash]
		if !ok {
			continue
		}

		// Check denied
		if cachedDenied[ec.be.ContractAddress] {
			ing.logger.Debug("skipping denied token event",
				"contract", ec.be.ContractAddress,
				"address", ec.be.Address,
				"delta", ec.be.Delta,
			)
			metrics.IngesterDeniedEventsSkipped.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
			continue
		}

		// Get in-memory balance
		bk := store.BalanceKey{Address: ec.be.Address, TokenID: tokenID, BalanceType: ""}
		accum, ok := inMemoryBalances[bk]
		if !ok {
			accum = &balanceAccum{amount: "0", exists: false}
			inMemoryBalances[bk] = accum
		}
		balanceBefore := accum.amount
		balanceExists := accum.exists

		// Scam detection
		if signal := detectScamSignal(batch.Chain, ec.be, balanceBefore, balanceExists); signal != "" {
			ing.logger.Warn("scam token detected, auto-denying",
				"signal", signal,
				"contract", ec.be.ContractAddress,
				"address", ec.be.Address,
				"delta", ec.be.Delta,
				"balance_before", balanceBefore,
				"balance_exists", balanceExists,
			)
			if err := ing.tokenRepo.DenyTokenTx(
				ctx, dbTx,
				batch.Chain, batch.Network, ec.be.ContractAddress,
				fmt.Sprintf("auto-detected: %s", signal),
				"ingester_auto",
				100,
				[]string{signal},
			); err != nil {
				phase3Span.RecordError(err)
				phase3Span.SetStatus(codes.Error, err.Error())
				phase3Span.End()
				return fmt.Errorf("deny scam token %s: %w", ec.be.ContractAddress, err)
			}
			deniedKey := fmt.Sprintf("%s:%s:%s", batch.Chain, batch.Network, ec.be.ContractAddress)
			ing.deniedCache.Put(deniedKey, true)
			cachedDenied[ec.be.ContractAddress] = true
			metrics.IngesterScamTokensDetected.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
			metrics.IngesterDeniedEventsSkipped.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
			continue
		}

		// Compute balance after
		balanceAfter, calcErr := addDecimalStrings(balanceBefore, ec.be.Delta)
		if calcErr != nil {
			phase3Span.RecordError(calcErr)
			phase3Span.SetStatus(codes.Error, calcErr.Error())
			phase3Span.End()
			return fmt.Errorf("apply delta %s to balance %s: %w", ec.be.Delta, balanceBefore, calcErr)
		}

		// Build balance event model
		chainData := ec.be.ChainData
		if chainData == nil {
			chainData = json.RawMessage("{}")
		}
		// Resolve wallet/org: block-scan mode uses per-event address lookup
		eventWalletID := batch.WalletID
		eventOrgID := batch.OrgID
		eventWatchedAddr := &batch.Address
		if batch.BlockScanMode {
			addr := ec.be.Address
			eventWatchedAddr = &addr
			if ing.addressIndex != nil {
				// Fast path: 3-tier index (bloom → LRU → DB)
				if wa := ing.addressIndex.Lookup(ctx, batch.Chain, batch.Network, ec.be.Address); wa != nil {
					eventWalletID = wa.WalletID
					eventOrgID = wa.OrganizationID
				}
			} else if blockScanAddrMap != nil {
				// Legacy fallback: full address map
				if meta, ok := blockScanAddrMap[ec.be.Address]; ok {
					eventWalletID = meta.walletID
					eventOrgID = meta.orgID
				}
			}
		}

		beModel := &model.BalanceEvent{
			Chain:                 batch.Chain,
			Network:               batch.Network,
			TransactionID:         txID,
			TxHash:                ec.canonicalTxHash,
			OuterInstructionIndex: ec.be.OuterInstructionIndex,
			InnerInstructionIndex: ec.be.InnerInstructionIndex,
			TokenID:               tokenID,
			ActivityType:          ec.be.ActivityType,
			EventAction:           ec.be.EventAction,
			ProgramID:             ec.be.ProgramID,
			Address:               ec.be.Address,
			CounterpartyAddress:   ec.be.CounterpartyAddress,
			Delta:                 ec.be.Delta,
			WatchedAddress:        eventWatchedAddr,
			WalletID:              eventWalletID,
			OrganizationID:        eventOrgID,
			BlockCursor:           ec.ntx.BlockCursor,
			BlockTime:             ec.ntx.BlockTime,
			ChainData:             chainData,
			EventID:               ec.be.EventID,
			BlockHash:             ec.be.BlockHash,
			TxIndex:               ec.be.TxIndex,
			EventPath:             ec.be.EventPath,
			EventPathType:         ec.be.EventPathType,
			ActorAddress:          ec.be.ActorAddress,
			AssetType:             ec.be.AssetType,
			AssetID:               ec.be.AssetID,
			FinalityState:         ec.be.FinalityState,
			DecoderVersion:        ec.be.DecoderVersion,
			SchemaVersion:         ec.be.SchemaVersion,
		}
		beModel.BalanceApplied = meetsBalanceThreshold(batch.Chain, beModel.FinalityState)
		if beModel.BalanceApplied {
			beModel.BalanceBefore = &balanceBefore
			beModel.BalanceAfter = &balanceAfter
		}
		eventModels = append(eventModels, beModel)

		// Update in-memory balance for subsequent events in same batch
		if beModel.BalanceApplied {
			accum.amount = balanceAfter
			accum.exists = true

			// Aggregate delta for bulk adjust
			ak := adjustmentKey{
				BalanceKey: store.BalanceKey{Address: ec.be.Address, TokenID: tokenID, BalanceType: ""},
				walletID:   eventWalletID,
				orgID:      eventOrgID,
			}
			delta := new(big.Int)
			if _, ok := delta.SetString(strings.TrimSpace(ec.be.Delta), 10); !ok {
				err := fmt.Errorf("invalid delta value %q for event %s", ec.be.Delta, ec.be.EventID)
				phase3Span.RecordError(err)
				phase3Span.SetStatus(codes.Error, err.Error())
				phase3Span.End()
				return err
			}
			if existing, ok := aggregatedDeltas[ak]; ok {
				existing.delta.Add(existing.delta, delta)
				if ec.ntx.BlockCursor > existing.cursor {
					existing.cursor = ec.ntx.BlockCursor
					existing.txHash = ec.canonicalTxHash
				}
			} else {
				aggregatedDeltas[ak] = &adjustmentAccum{
					delta:  delta,
					cursor: ec.ntx.BlockCursor,
					txHash: ec.canonicalTxHash,
				}
			}

			// Staking balance tracking
			if identity.IsStakingActivity(beModel.ActivityType) {
				invertedDelta, negErr := identity.NegateDecimalString(ec.be.Delta)
				if negErr != nil {
					phase3Span.RecordError(negErr)
					phase3Span.SetStatus(codes.Error, negErr.Error())
					phase3Span.End()
					return fmt.Errorf("negate staking delta: %w", negErr)
				}
				sak := adjustmentKey{
					BalanceKey: store.BalanceKey{Address: ec.be.Address, TokenID: tokenID, BalanceType: "staked"},
					walletID:   eventWalletID,
					orgID:      eventOrgID,
				}
				stakeDelta := new(big.Int)
				if _, ok := stakeDelta.SetString(strings.TrimSpace(invertedDelta), 10); !ok {
					err := fmt.Errorf("invalid staking delta value %q for event %s", invertedDelta, ec.be.EventID)
					phase3Span.RecordError(err)
					phase3Span.SetStatus(codes.Error, err.Error())
					phase3Span.End()
					return err
				}
				if existing, ok := aggregatedDeltas[sak]; ok {
					existing.delta.Add(existing.delta, stakeDelta)
					if ec.ntx.BlockCursor > existing.cursor {
						existing.cursor = ec.ntx.BlockCursor
						existing.txHash = ec.canonicalTxHash
					}
				} else {
					aggregatedDeltas[sak] = &adjustmentAccum{
						delta:  stakeDelta,
						cursor: ec.ntx.BlockCursor,
						txHash: ec.canonicalTxHash,
					}
				}
			}
		}
	}

	phase3Span.SetAttributes(
		attribute.Int("event_models_built", len(eventModels)),
		attribute.Int("aggregated_deltas", len(aggregatedDeltas)),
	)
	phase3Span.End()

	// ========== PHASE 4: Bulk Write ==========
	_, phase4Span := tracing.Tracer("ingester").Start(ctx, "ingester.phase4_write")

	// 4a. Bulk upsert balance events
	if len(eventModels) > 0 {
		bulkResult, err := ing.balanceEventRepo.BulkUpsertTx(ctx, dbTx, eventModels)
		if err != nil {
			phase4Span.RecordError(err)
			phase4Span.SetStatus(codes.Error, err.Error())
			phase4Span.End()
			return fmt.Errorf("bulk upsert balance events: %w", err)
		}
		totalEvents = bulkResult.InsertedCount + bulkResult.FinalityCrossedCount
	}

	// 4b. Bulk adjust balances (aggregated deltas)
	if len(aggregatedDeltas) > 0 {
		adjustItems := make([]store.BulkAdjustItem, 0, len(aggregatedDeltas))
		for ak, acc := range aggregatedDeltas {
			adjustItems = append(adjustItems, store.BulkAdjustItem{
				Address:     ak.Address,
				TokenID:     ak.TokenID,
				WalletID:    ak.walletID,
				OrgID:       ak.orgID,
				Delta:       acc.delta.String(),
				Cursor:      acc.cursor,
				TxHash:      acc.txHash,
				BalanceType: ak.BalanceType,
			})
		}
		if err := ing.balanceRepo.BulkAdjustBalanceTx(ctx, dbTx, batch.Chain, batch.Network, adjustItems); err != nil {
			phase4Span.RecordError(err)
			phase4Span.SetStatus(codes.Error, err.Error())
			phase4Span.End()
			return fmt.Errorf("bulk adjust balances: %w", err)
		}
	}

	// 4c. Bulk upsert indexed blocks (for reorg detection)
	if ing.blockRepo != nil && len(batch.Transactions) > 0 {
		blockMap := make(map[int64]*model.IndexedBlock)
		for _, ntx := range batch.Transactions {
			if ntx.BlockHash == "" {
				continue
			}
			if _, exists := blockMap[ntx.BlockCursor]; !exists {
				finalityState := "pending"
				if len(ntx.BalanceEvents) > 0 && ntx.BalanceEvents[0].FinalityState != "" {
					finalityState = ntx.BalanceEvents[0].FinalityState
				}
				blockMap[ntx.BlockCursor] = &model.IndexedBlock{
					Chain:         batch.Chain,
					Network:       batch.Network,
					BlockNumber:   ntx.BlockCursor,
					BlockHash:     ntx.BlockHash,
					ParentHash:    ntx.ParentHash,
					FinalityState: finalityState,
					BlockTime:     ntx.BlockTime,
				}
			}
		}
		if len(blockMap) > 0 {
			blocks := make([]*model.IndexedBlock, 0, len(blockMap))
			for _, b := range blockMap {
				blocks = append(blocks, b)
			}
			if err := ing.blockRepo.BulkUpsertTx(ctx, dbTx, blocks); err != nil {
				phase4Span.RecordError(err)
				phase4Span.SetStatus(codes.Error, err.Error())
				phase4Span.End()
				return fmt.Errorf("bulk upsert indexed blocks: %w", err)
			}
		}
	}

	// 3. Update cursor (skip for block-scan mode — watermark is the only checkpoint)
	if !batch.BlockScanMode && batch.NewCursorValue != nil {
		if err := ing.cursorRepo.UpsertTx(
			ctx, dbTx,
			batch.Chain, batch.Network, batch.Address,
			batch.NewCursorValue, batch.NewCursorSequence,
			int64(len(batch.Transactions)),
		); err != nil {
			phase4Span.RecordError(err)
			phase4Span.SetStatus(codes.Error, err.Error())
			phase4Span.End()
			return fmt.Errorf("update cursor: %w", err)
		}
	}

	// 4. Update watermark
	if err := ing.configRepo.UpdateWatermarkTx(
		ctx, dbTx,
		batch.Chain, batch.Network, batch.NewCursorSequence,
	); err != nil {
		phase4Span.RecordError(err)
		phase4Span.SetStatus(codes.Error, err.Error())
		phase4Span.End()
		return fmt.Errorf("update watermark: %w", err)
	}

	// 5. Commit
	commitStart := time.Now()
	if err := dbTx.Commit(); err != nil {
		reconciled, reconcileErr := ing.reconcileAmbiguousCommitOutcome(ctx, batch, err)
		if reconcileErr != nil {
			phase4Span.RecordError(reconcileErr)
			phase4Span.SetStatus(codes.Error, reconcileErr.Error())
			phase4Span.End()
			return reconcileErr
		}
		if reconciled {
			committed = true
			ing.recordCommitLatencyMs(batch.Chain.String(), batch.Network.String(), time.Since(commitStart).Milliseconds())
			ing.logger.Warn("commit outcome reconciled as committed",
				"chain", batch.Chain,
				"network", batch.Network,
				"address", batch.Address,
				"cursor_sequence", batch.NewCursorSequence,
				"cursor_value", batch.NewCursorValue,
				"commit_error", err,
			)
			phase4Span.End()
			return nil
		}
		phase4Span.RecordError(err)
		phase4Span.SetStatus(codes.Error, err.Error())
		phase4Span.End()
		return fmt.Errorf("commit: %w", err)
	}
	ing.recordCommitLatencyMs(batch.Chain.String(), batch.Network.String(), time.Since(commitStart).Milliseconds())
	committed = true
	phase4Span.End()

	metrics.IngesterBalanceEventsWritten.WithLabelValues(batch.Chain.String(), batch.Network.String()).Add(float64(totalEvents))
	metrics.PipelineCursorSequence.WithLabelValues(batch.Chain.String(), batch.Network.String(), batch.Address).Set(float64(batch.NewCursorSequence))

	// E2E latency: oldest block_time in batch → now
	if oldest := oldestBlockTime(batch); oldest != nil {
		e2e := time.Since(*oldest).Seconds()
		metrics.PipelineE2ELatencySeconds.WithLabelValues(batch.Chain.String(), batch.Network.String()).Observe(e2e)
	}

	ing.logger.Info("batch ingested",
		"address", batch.Address,
		"txs", len(batch.Transactions),
		"balance_events", totalEvents,
		"cursor", batch.NewCursorValue,
	)

	return nil
}

// handleReorg processes a ReorgEvent by rolling back all data from the fork block onward.
// If a replayService is configured, the rollback is delegated to it; otherwise the
// inline implementation is used as a fallback.
func (ing *Ingester) handleReorg(ctx context.Context, reorg event.ReorgEvent) error {
	ing.logger.Warn("processing reorg rollback",
		"chain", reorg.Chain,
		"network", reorg.Network,
		"fork_block", reorg.ForkBlockNumber,
		"expected_hash", reorg.ExpectedHash,
		"actual_hash", reorg.ActualHash,
	)

	// Delegate to replay.Service when available
	if ing.replayService != nil {
		_, err := ing.replayService.PurgeFromBlock(ctx, replay.PurgeRequest{
			Chain:     reorg.Chain,
			Network:   reorg.Network,
			FromBlock: reorg.ForkBlockNumber,
			Force:     true, // reorg bypasses finality check
			Reason:    fmt.Sprintf("reorg: expected=%s actual=%s", reorg.ExpectedHash, reorg.ActualHash),
		})
		if err != nil {
			return fmt.Errorf("reorg via replay service: %w", err)
		}
		metrics.IngesterReorgRollbacksTotal.WithLabelValues(reorg.Chain.String(), reorg.Network.String()).Inc()
		return nil
	}

	// Fallback: inline rollback (kept for backward compatibility)
	dbTx, err := ing.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reorg tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if rbErr := dbTx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
				ing.logger.Warn("reorg rollback tx rollback failed", "error", rbErr)
			}
		}
	}()

	// 1. Fetch balance events that will be rolled back
	rollbackEvents, err := ing.fetchReorgRollbackEvents(ctx, dbTx, reorg.Chain, reorg.Network, reorg.ForkBlockNumber)
	if err != nil {
		return fmt.Errorf("fetch reorg rollback events: %w", err)
	}

	// 2. Reverse applied balance deltas
	for _, be := range rollbackEvents {
		if !be.BalanceApplied {
			continue
		}
		invertedDelta, err := identity.NegateDecimalString(be.Delta)
		if err != nil {
			return fmt.Errorf("negate delta for %s: %w", be.TxHash, err)
		}
		if err := ing.balanceRepo.AdjustBalanceTx(
			ctx, dbTx,
			reorg.Chain, reorg.Network, be.Address,
			be.TokenID, be.WalletID, be.OrganizationID,
			invertedDelta, be.BlockCursor, be.TxHash, "",
		); err != nil {
			return fmt.Errorf("revert balance: %w", err)
		}
		if identity.IsStakingActivity(be.ActivityType) {
			if err := ing.balanceRepo.AdjustBalanceTx(
				ctx, dbTx,
				reorg.Chain, reorg.Network, be.Address,
				be.TokenID, be.WalletID, be.OrganizationID,
				be.Delta, be.BlockCursor, be.TxHash, "staked",
			); err != nil {
				return fmt.Errorf("revert staked balance: %w", err)
			}
		}
	}

	// 3. Delete balance events from fork block onward
	if _, err := dbTx.ExecContext(ctx, `
		DELETE FROM balance_events
		WHERE chain = $1 AND network = $2 AND block_cursor >= $3
	`, reorg.Chain, reorg.Network, reorg.ForkBlockNumber); err != nil {
		return fmt.Errorf("delete reorg balance events: %w", err)
	}

	// 4. Delete transactions from fork block onward
	if _, err := dbTx.ExecContext(ctx, `
		DELETE FROM transactions
		WHERE chain = $1 AND network = $2 AND block_cursor >= $3
	`, reorg.Chain, reorg.Network, reorg.ForkBlockNumber); err != nil {
		return fmt.Errorf("delete reorg transactions: %w", err)
	}

	// 5. Delete indexed blocks from fork block onward
	if ing.blockRepo != nil {
		if _, err := ing.blockRepo.DeleteFromBlockTx(ctx, dbTx, reorg.Chain, reorg.Network, reorg.ForkBlockNumber); err != nil {
			return fmt.Errorf("delete reorg indexed blocks: %w", err)
		}
	}

	// 6. Rewind cursors for affected addresses
	if err := ing.rewindCursorsForReorg(ctx, dbTx, reorg.Chain, reorg.Network, reorg.ForkBlockNumber); err != nil {
		return fmt.Errorf("rewind cursors: %w", err)
	}

	// 7. Rewind watermark
	rewindSequence := reorg.ForkBlockNumber - 1
	if rewindSequence < 0 {
		rewindSequence = 0
	}
	if err := ing.configRepo.UpdateWatermarkTx(ctx, dbTx, reorg.Chain, reorg.Network, rewindSequence); err != nil {
		return fmt.Errorf("rewind watermark: %w", err)
	}

	if err := dbTx.Commit(); err != nil {
		return fmt.Errorf("commit reorg rollback: %w", err)
	}
	committed = true

	metrics.IngesterReorgRollbacksTotal.WithLabelValues(reorg.Chain.String(), reorg.Network.String()).Inc()

	ing.logger.Info("reorg rollback completed",
		"chain", reorg.Chain,
		"network", reorg.Network,
		"fork_block", reorg.ForkBlockNumber,
		"events_rolled_back", len(rollbackEvents),
	)
	return nil
}

// fetchReorgRollbackEvents fetches all balance events from fork block onward across all addresses.
func (ing *Ingester) fetchReorgRollbackEvents(
	ctx context.Context,
	tx *sql.Tx,
	chain model.Chain,
	network model.Network,
	forkBlock int64,
) ([]rollbackBalanceEvent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT token_id, address, delta, block_cursor, tx_hash, wallet_id, organization_id, activity_type, balance_applied
		FROM balance_events
		WHERE chain = $1 AND network = $2 AND block_cursor >= $3
		ORDER BY block_cursor DESC, id DESC
	`, chain, network, forkBlock)
	if err != nil {
		return nil, fmt.Errorf("query reorg rollback events: %w", err)
	}
	defer rows.Close()

	var events []rollbackBalanceEvent
	for rows.Next() {
		var be rollbackBalanceEvent
		var walletID sql.NullString
		var organizationID sql.NullString
		if err := rows.Scan(&be.TokenID, &be.Address, &be.Delta, &be.BlockCursor, &be.TxHash, &walletID, &organizationID, &be.ActivityType, &be.BalanceApplied); err != nil {
			return nil, fmt.Errorf("scan reorg rollback event: %w", err)
		}
		if walletID.Valid {
			be.WalletID = &walletID.String
		}
		if organizationID.Valid {
			be.OrganizationID = &organizationID.String
		}
		events = append(events, be)
	}
	return events, rows.Err()
}

// rewindCursorsForReorg rewinds all address cursors that point at or beyond forkBlock.
func (ing *Ingester) rewindCursorsForReorg(
	ctx context.Context,
	tx *sql.Tx,
	chain model.Chain,
	network model.Network,
	forkBlock int64,
) error {
	rewindSequence := forkBlock - 1
	if rewindSequence < 0 {
		rewindSequence = 0
	}

	_, err := tx.ExecContext(ctx, `
		UPDATE address_cursors
		SET cursor_sequence = $4, items_processed = 0, updated_at = now()
		WHERE chain = $1 AND network = $2 AND cursor_sequence >= $3
	`, chain, network, forkBlock, rewindSequence)
	if err != nil {
		return fmt.Errorf("rewind cursors for reorg: %w", err)
	}
	return nil
}

// handleFinalityPromotion promotes balance events and indexed blocks to finalized state.
func (ing *Ingester) handleFinalityPromotion(ctx context.Context, promo event.FinalityPromotion) error {
	ing.logger.Info("processing finality promotion",
		"chain", promo.Chain,
		"network", promo.Network,
		"new_finalized_block", promo.NewFinalizedBlock,
	)

	dbTx, err := ing.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finality tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if rbErr := dbTx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
				ing.logger.Warn("finality rollback failed", "error", rbErr)
			}
		}
	}()

	// 1. Update indexed_blocks finality state
	if ing.blockRepo != nil {
		if err := ing.blockRepo.UpdateFinalityTx(ctx, dbTx, promo.Chain, promo.Network, promo.NewFinalizedBlock, "finalized"); err != nil {
			return fmt.Errorf("update indexed blocks finality: %w", err)
		}
	}

	// 2. Update balance_events finality_state and promote balance_applied
	promotedEvents, err := ing.promoteBalanceEvents(ctx, dbTx, promo.Chain, promo.Network, promo.NewFinalizedBlock)
	if err != nil {
		return fmt.Errorf("promote balance events: %w", err)
	}

	// 3. Apply newly promoted deltas to balances
	if len(promotedEvents) > 0 {
		adjustItems := make([]store.BulkAdjustItem, 0, len(promotedEvents))
		for _, pe := range promotedEvents {
			adjustItems = append(adjustItems, store.BulkAdjustItem{
				Address:     pe.Address,
				TokenID:     pe.TokenID,
				WalletID:    pe.WalletID,
				OrgID:       pe.OrganizationID,
				Delta:       pe.Delta,
				Cursor:      pe.BlockCursor,
				TxHash:      pe.TxHash,
				BalanceType: "",
			})

			if identity.IsStakingActivity(pe.ActivityType) {
				invertedDelta, err := identity.NegateDecimalString(pe.Delta)
				if err != nil {
					return fmt.Errorf("negate staking delta: %w", err)
				}
				adjustItems = append(adjustItems, store.BulkAdjustItem{
					Address:     pe.Address,
					TokenID:     pe.TokenID,
					WalletID:    pe.WalletID,
					OrgID:       pe.OrganizationID,
					Delta:       invertedDelta,
					Cursor:      pe.BlockCursor,
					TxHash:      pe.TxHash,
					BalanceType: "staked",
				})
			}
		}
		if err := ing.balanceRepo.BulkAdjustBalanceTx(ctx, dbTx, promo.Chain, promo.Network, adjustItems); err != nil {
			return fmt.Errorf("bulk adjust balances for finality promotion: %w", err)
		}
	}

	if err := dbTx.Commit(); err != nil {
		return fmt.Errorf("commit finality promotion: %w", err)
	}
	committed = true

	metrics.IngesterFinalityPromotionsTotal.WithLabelValues(promo.Chain.String(), promo.Network.String()).Inc()

	ing.logger.Info("finality promotion completed",
		"chain", promo.Chain,
		"network", promo.Network,
		"new_finalized_block", promo.NewFinalizedBlock,
		"events_promoted", len(promotedEvents),
	)
	return nil
}

type promotedEvent struct {
	TokenID        uuid.UUID
	Address        string
	Delta          string
	BlockCursor    int64
	TxHash         string
	WalletID       *string
	OrganizationID *string
	ActivityType   model.ActivityType
}

// promoteBalanceEvents updates finality_state and balance_applied for events
// that are now finalized, and returns the events that were newly promoted.
func (ing *Ingester) promoteBalanceEvents(
	ctx context.Context,
	tx *sql.Tx,
	chain model.Chain,
	network model.Network,
	upToBlock int64,
) ([]promotedEvent, error) {
	// Update finality_state for all events up to the finalized block
	if _, err := tx.ExecContext(ctx, `
		UPDATE balance_events
		SET finality_state = 'finalized'
		WHERE chain = $1 AND network = $2 AND block_cursor <= $3
		  AND finality_state != 'finalized' AND finality_state != ''
	`, chain, network, upToBlock); err != nil {
		return nil, fmt.Errorf("update balance events finality: %w", err)
	}

	// Find events that need balance_applied promotion (were pending, now finalized)
	rows, err := tx.QueryContext(ctx, `
		SELECT token_id, address, delta, block_cursor, tx_hash, wallet_id, organization_id, activity_type
		FROM balance_events
		WHERE chain = $1 AND network = $2 AND block_cursor <= $3
		  AND balance_applied = false AND finality_state = 'finalized'
	`, chain, network, upToBlock)
	if err != nil {
		return nil, fmt.Errorf("query promotable events: %w", err)
	}
	defer rows.Close()

	var events []promotedEvent
	for rows.Next() {
		var pe promotedEvent
		var walletID sql.NullString
		var orgID sql.NullString
		if err := rows.Scan(&pe.TokenID, &pe.Address, &pe.Delta, &pe.BlockCursor, &pe.TxHash, &walletID, &orgID, &pe.ActivityType); err != nil {
			return nil, fmt.Errorf("scan promotable event: %w", err)
		}
		if walletID.Valid {
			pe.WalletID = &walletID.String
		}
		if orgID.Valid {
			pe.OrganizationID = &orgID.String
		}
		events = append(events, pe)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read promotable events: %w", err)
	}

	// Set balance_applied=true for promoted events
	if len(events) > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE balance_events
			SET balance_applied = true
			WHERE chain = $1 AND network = $2 AND block_cursor <= $3
			  AND balance_applied = false AND finality_state = 'finalized'
		`, chain, network, upToBlock); err != nil {
			return nil, fmt.Errorf("update balance_applied: %w", err)
		}
	}

	return events, nil
}

// detectScamSignal checks for scam token signals.
// Returns the signal name if suspicious, or empty string if clean.
func detectScamSignal(chain model.Chain, be event.NormalizedBalanceEvent, balanceBefore string, balanceExists bool) string {
	// Skip native tokens — fee deductions from zero balance are normal
	if be.TokenType == model.TokenTypeNative {
		return ""
	}

	// Skip BTC — UTXO model doesn't produce fake transfer patterns
	if chain == model.ChainBTC {
		return ""
	}

	// Check sidecar-propagated signal from metadata
	if be.ChainData != nil {
		var metadata map[string]string
		if err := json.Unmarshal(be.ChainData, &metadata); err == nil {
			if signal, ok := metadata["scam_signal"]; ok && signal != "" {
				return signal
			}
		}
	}

	// Go-level detection: zero_balance_withdrawal
	// Token never held (no balance record) but negative delta
	delta := new(big.Int)
	if _, ok := delta.SetString(strings.TrimSpace(be.Delta), 10); ok {
		if delta.Sign() < 0 && !balanceExists {
			return "zero_balance_withdrawal"
		}
	}

	return ""
}

func addDecimalStrings(a, b string) (string, error) {
	var left big.Int
	if _, ok := left.SetString(strings.TrimSpace(a), 10); !ok {
		return "", fmt.Errorf("invalid decimal value: %s", a)
	}

	var right big.Int
	if _, ok := right.SetString(strings.TrimSpace(b), 10); !ok {
		return "", fmt.Errorf("invalid decimal value: %s", b)
	}

	result := new(big.Int).Add(&left, &right)
	return result.String(), nil
}

func (ing *Ingester) rollbackCanonicalityDrift(ctx context.Context, dbTx *sql.Tx, batch event.NormalizedBatch) error {
	forkCursor := rollbackForkCursorSequence(batch)

	rollbackEvents, err := ing.fetchRollbackEvents(ctx, dbTx, batch.Chain, batch.Network, batch.Address, forkCursor)
	if err != nil {
		return fmt.Errorf("fetch rollback events: %w", err)
	}

	for _, be := range rollbackEvents {
		if !be.BalanceApplied {
			continue
		}
		invertedDelta, err := identity.NegateDecimalString(be.Delta)
		if err != nil {
			return fmt.Errorf("negate delta for %s: %w", be.TxHash, err)
		}

		if err := ing.balanceRepo.AdjustBalanceTx(
			ctx, dbTx,
			batch.Chain, batch.Network, be.Address,
			be.TokenID, be.WalletID, be.OrganizationID,
			invertedDelta, be.BlockCursor, be.TxHash, "",
		); err != nil {
			return fmt.Errorf("revert balance: %w", err)
		}

		// Reverse staking balance if applicable
		if identity.IsStakingActivity(be.ActivityType) {
			if err := ing.balanceRepo.AdjustBalanceTx(
				ctx, dbTx,
				batch.Chain, batch.Network, be.Address,
				be.TokenID, be.WalletID, be.OrganizationID,
				be.Delta, be.BlockCursor, be.TxHash, "staked",
			); err != nil {
				return fmt.Errorf("revert staked balance: %w", err)
			}
		}
	}

	if _, err := dbTx.ExecContext(ctx, `
		DELETE FROM balance_events
		WHERE chain = $1 AND network = $2 AND watched_address = $3 AND block_cursor >= $4
	`, batch.Chain, batch.Network, batch.Address, forkCursor); err != nil {
		return fmt.Errorf("delete rollback balance events: %w", err)
	}

	// Compute rewind sequence for watermark update
	rewindWatermarkSequence := forkCursor - 1
	if rewindWatermarkSequence < 0 {
		rewindWatermarkSequence = 0
	}

	// Skip per-address cursor rewind for block-scan mode (watermark is the only checkpoint)
	if !batch.BlockScanMode {
		rewindCursorValue, rewindCursorSequence, err := ing.findRewindCursor(
			ctx, dbTx, batch.Chain, batch.Network, batch.Address, forkCursor,
			batch.NewCursorValue, batch.NewCursorSequence,
		)
		if err != nil {
			return fmt.Errorf("find rewind cursor: %w", err)
		}

		if err := ing.cursorRepo.UpsertTx(
			ctx, dbTx,
			batch.Chain, batch.Network, batch.Address,
			rewindCursorValue, rewindCursorSequence, 0,
		); err != nil {
			return fmt.Errorf("rewind cursor: %w", err)
		}
		rewindWatermarkSequence = rewindCursorSequence
	}

	if err := ing.configRepo.UpdateWatermarkTx(
		ctx, dbTx,
		batch.Chain, batch.Network, rewindWatermarkSequence,
	); err != nil {
		return fmt.Errorf("update watermark after rewind: %w", err)
	}

	return nil
}

type rollbackBalanceEvent struct {
	TokenID        uuid.UUID
	Address        string
	Delta          string
	BlockCursor    int64
	TxHash         string
	WalletID       *string
	OrganizationID *string
	ActivityType   model.ActivityType
	BalanceApplied bool
}

func (ing *Ingester) fetchRollbackEvents(
	ctx context.Context,
	tx *sql.Tx,
	chain model.Chain,
	network model.Network,
	address string,
	forkCursor int64,
) ([]rollbackBalanceEvent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT token_id, address, delta, block_cursor, tx_hash, wallet_id, organization_id, activity_type, balance_applied
		FROM balance_events
		WHERE chain = $1 AND network = $2 AND watched_address = $3 AND block_cursor >= $4
		ORDER BY block_cursor DESC, id DESC
	`, chain, network, address, forkCursor)
	if err != nil {
		return nil, fmt.Errorf("query rollback events: %w", err)
	}
	defer rows.Close()

	events := make([]rollbackBalanceEvent, 0)
	for rows.Next() {
		var be rollbackBalanceEvent
		var walletID sql.NullString
		var organizationID sql.NullString
		if err := rows.Scan(&be.TokenID, &be.Address, &be.Delta, &be.BlockCursor, &be.TxHash, &walletID, &organizationID, &be.ActivityType, &be.BalanceApplied); err != nil {
			return nil, fmt.Errorf("scan rollback event: %w", err)
		}
		if walletID.Valid {
			be.WalletID = &walletID.String
		}
		if organizationID.Valid {
			be.OrganizationID = &organizationID.String
		}
		events = append(events, be)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read rollback event rows: %w", err)
	}

	return events, nil
}

func (ing *Ingester) findRewindCursor(
	ctx context.Context,
	tx *sql.Tx,
	chain model.Chain,
	network model.Network,
	address string,
	forkCursor int64,
	fallbackCursorValue *string,
	fallbackCursorSequence int64,
) (*string, int64, error) {
	const rewindCursorSQL = `
		SELECT t.tx_hash, be.block_cursor
		FROM balance_events be
		JOIN transactions t ON t.id = be.transaction_id
		WHERE be.chain = $1 AND be.network = $2
		  AND be.watched_address = $3
		  AND be.block_cursor < $4
	ORDER BY be.block_cursor DESC, be.id DESC
		LIMIT 1
	`
	var cursorValue string
	var cursorSequence int64
	if err := tx.QueryRowContext(ctx, rewindCursorSQL, chain, network, address, forkCursor).Scan(&cursorValue, &cursorSequence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Fall back to deterministic boundary synthesis below.
		} else {
			return nil, 0, fmt.Errorf("query rewind cursor: %w", err)
		}
	} else {
		return &cursorValue, cursorSequence, nil
	}

	const rollbackCommittedCursorSQL = `
	SELECT cursor_value, cursor_sequence
	FROM address_cursors
	WHERE chain = $1 AND network = $2 AND address = $3
`

	var committedCursorRaw sql.NullString
	var committedCursorSequence int64
	var committedCursorValue *string
	if err := tx.QueryRowContext(ctx, rollbackCommittedCursorSQL, chain, network, address).Scan(&committedCursorRaw, &committedCursorSequence); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, 0, fmt.Errorf("query committed cursor: %w", err)
		}
	} else if committedCursorRaw.Valid {
		value := committedCursorRaw.String
		committedCursorValue = &value
	}

	resolvedValue, resolvedSequence := resolveRewindCursorBoundary(
		chain,
		forkCursor,
		nil,
		0,
		committedCursorValue,
		committedCursorSequence,
		fallbackCursorValue,
		fallbackCursorSequence,
	)

	return resolvedValue, resolvedSequence, nil
}

func resolveRewindCursorBoundary(
	chain model.Chain,
	forkCursor int64,
	foundCursorValue *string,
	foundCursorSequence int64,
	committedCursorValue *string,
	committedCursorSequence int64,
	fallbackCursorValue *string,
	fallbackCursorSequence int64,
) (*string, int64) {
	if foundCursorValue != nil {
		return foundCursorValue, foundCursorSequence
	}

	if committedCursorValue != nil && committedCursorSequence > 0 && committedCursorSequence <= forkCursor {
		if canonicalCommitted := identity.CanonicalizeCursorValue(chain, committedCursorValue); canonicalCommitted != nil {
			return canonicalCommitted, committedCursorSequence
		}
	}

	resolvedCursor := identity.CanonicalizeCursorValue(chain, fallbackCursorValue)
	resolvedSequence := fallbackCursorSequence
	if resolvedSequence <= 0 {
		return resolvedCursor, forkCursor
	}
	if resolvedSequence > forkCursor {
		return resolvedCursor, forkCursor
	}
	return resolvedCursor, resolvedSequence
}


func (ing *Ingester) reconcileAmbiguousCommitOutcome(
	ctx context.Context,
	batch event.NormalizedBatch,
	commitErr error,
) (bool, error) {
	if !isAmbiguousCommitAckError(commitErr) {
		return false, nil
	}

	// Block-scan mode: check watermark instead of per-address cursor
	if batch.BlockScanMode {
		return ing.reconcileBlockScanCommit(ctx, batch, commitErr)
	}

	expectedCursor := identity.CanonicalizeCursorValue(batch.Chain, batch.NewCursorValue)
	if expectedCursor == nil {
		return false, retry.Terminal(fmt.Errorf(
			"commit_ambiguity_unresolved chain=%s network=%s address=%s reason=missing_expected_cursor expected_seq=%d commit_err=%w",
			batch.Chain,
			batch.Network,
			batch.Address,
			batch.NewCursorSequence,
			commitErr,
		))
	}

	cursor, err := ing.cursorRepo.Get(ctx, batch.Chain, batch.Network, batch.Address)
	if err != nil {
		return false, retry.Terminal(fmt.Errorf(
			"commit_ambiguity_unresolved chain=%s network=%s address=%s reason=cursor_probe_failed expected_seq=%d expected_cursor=%s commit_err=%v: %w",
			batch.Chain,
			batch.Network,
			batch.Address,
			batch.NewCursorSequence,
			*expectedCursor,
			commitErr,
			err,
		))
	}

	if cursorMatchesCommitBoundary(batch.Chain, cursor, expectedCursor, batch.NewCursorSequence) {
		return true, nil
	}

	observedSeq := int64(-1)
	observedCursor := "<nil>"
	if cursor != nil {
		observedSeq = cursor.CursorSequence
		if normalized := identity.CanonicalizeCursorValue(batch.Chain, cursor.CursorValue); normalized != nil {
			observedCursor = *normalized
		}
	}
	if cursor != nil && cursor.CursorSequence > batch.NewCursorSequence {
		return false, retry.Terminal(fmt.Errorf(
			"commit_ambiguity_unresolved chain=%s network=%s address=%s reason=cursor_ahead_ambiguous expected_seq=%d expected_cursor=%s observed_seq=%d observed_cursor=%s commit_err=%w",
			batch.Chain,
			batch.Network,
			batch.Address,
			batch.NewCursorSequence,
			*expectedCursor,
			observedSeq,
			observedCursor,
			commitErr,
		))
	}

	return false, retry.Terminal(fmt.Errorf(
		"commit_ambiguity_unresolved chain=%s network=%s address=%s reason=cursor_mismatch expected_seq=%d expected_cursor=%s observed_seq=%d observed_cursor=%s commit_err=%w",
		batch.Chain,
		batch.Network,
		batch.Address,
		batch.NewCursorSequence,
		*expectedCursor,
		observedSeq,
		observedCursor,
		commitErr,
	))
}

// reconcileBlockScanCommit checks the watermark to determine if an ambiguous commit succeeded.
func (ing *Ingester) reconcileBlockScanCommit(
	ctx context.Context,
	batch event.NormalizedBatch,
	commitErr error,
) (bool, error) {
	watermark, err := ing.configRepo.GetWatermark(ctx, batch.Chain, batch.Network)
	if err != nil {
		return false, retry.Terminal(fmt.Errorf(
			"commit_ambiguity_unresolved chain=%s network=%s reason=watermark_probe_failed expected_seq=%d commit_err=%v: %w",
			batch.Chain, batch.Network, batch.NewCursorSequence, commitErr, err,
		))
	}
	if watermark != nil && watermark.IngestedSequence >= batch.NewCursorSequence {
		return true, nil
	}
	observedSeq := int64(-1)
	if watermark != nil {
		observedSeq = watermark.IngestedSequence
	}
	return false, retry.Terminal(fmt.Errorf(
		"commit_ambiguity_unresolved chain=%s network=%s reason=watermark_mismatch expected_seq=%d observed_watermark=%d commit_err=%w",
		batch.Chain, batch.Network, batch.NewCursorSequence, observedSeq, commitErr,
	))
}

func cursorMatchesCommitBoundary(
	chain model.Chain,
	cursor *model.AddressCursor,
	expectedCursor *string,
	expectedSeq int64,
) bool {
	if cursor == nil || expectedCursor == nil {
		return false
	}
	if cursor.CursorSequence != expectedSeq {
		return false
	}

	observedCursor := identity.CanonicalizeCursorValue(chain, cursor.CursorValue)
	if observedCursor == nil {
		return false
	}
	return *observedCursor == *expectedCursor
}

func isAmbiguousCommitAckError(err error) bool {
	if err == nil {
		return false
	}

	if retry.Classify(err).IsTransient() {
		return true
	}

	lower := strings.ToLower(err.Error())
	return containsAnySubstring(lower, []string{
		"driver: bad connection",
		"connection reset",
		"broken pipe",
		"unexpected eof",
		"eof",
		"i/o timeout",
		"timed out",
		"timeout",
		"network is unreachable",
		"connection aborted",
		"connection closed",
		"transport is closing",
	})
}

func containsAnySubstring(value string, tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}


// meetsBalanceThreshold checks whether the finality state is strong enough
// to apply the balance adjustment for the given chain.
func meetsBalanceThreshold(chain model.Chain, finality string) bool {
	switch chain {
	case model.ChainSolana:
		return true // Solana events are delivered as finalized
	default:
		// EVM/BTC: require confirmed or stronger
		return finalityStateRank(finality) >= 2
	}
}

func finalityStateRank(state string) int {
	s := strings.ToLower(strings.TrimSpace(state))
	switch s {
	case "":
		return 4 // No finality tracking → treat as finalized
	case "processed", "pending", "latest", "unsafe":
		return 1
	case "confirmed", "accepted":
		return 2
	case "safe":
		return 3
	case "finalized":
		return 4
	default:
		return 0
	}
}

func defaultTokenSymbol(be event.NormalizedBalanceEvent) string {
	if be.TokenSymbol != "" {
		return be.TokenSymbol
	}
	if be.TokenType == model.TokenTypeNative {
		return "SOL"
	}
	return "UNKNOWN"
}

func defaultTokenName(be event.NormalizedBalanceEvent) string {
	if be.TokenName != "" {
		return be.TokenName
	}
	if be.TokenType == model.TokenTypeNative {
		return "Solana"
	}
	return "Unknown Token"
}

func (ing *Ingester) effectiveRetryMaxAttempts() int {
	if ing.retryMaxAttempts <= 0 {
		return 1
	}
	return ing.retryMaxAttempts
}

func (ing *Ingester) recordCommitLatencyMs(chain, network string, latencyMs int64) {
	if ing.autoTuneSignals == nil {
		return
	}
	ing.autoTuneSignals.RecordDBCommitLatencyMs(chain, network, latencyMs)
}

func (ing *Ingester) retryDelay(attempt int) time.Duration {
	delay := ing.retryDelayStart
	if delay <= 0 {
		return 0
	}
	if attempt <= 1 {
		if ing.retryDelayMax > 0 && delay > ing.retryDelayMax {
			return ing.retryDelayMax
		}
		return delay
	}
	for i := 1; i < attempt; i++ {
		delay *= 2
		if ing.retryDelayMax > 0 && delay >= ing.retryDelayMax {
			return ing.retryDelayMax
		}
	}
	return delay
}

func (ing *Ingester) sleep(ctx context.Context, delay time.Duration) error {
	if ing.sleepFn == nil {
		ing.sleepFn = sleepContext
	}
	return ing.sleepFn(ctx, delay)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// buildFeeOnlyEvent creates a synthetic fee-only withdrawal event for a failed
// transaction. Even when a transaction reverts, the gas fee is still consumed.
func buildFeeOnlyEvent(ntx event.NormalizedTransaction, batch event.NormalizedBatch) event.NormalizedBalanceEvent {
	// Negate the fee amount to make it a withdrawal delta
	negFee := ntx.FeeAmount
	if !strings.HasPrefix(negFee, "-") {
		negFee = "-" + negFee
	}

	return event.NormalizedBalanceEvent{
		OuterInstructionIndex: 0,
		InnerInstructionIndex: 0,
		ActivityType:          model.ActivityFee,
		EventAction:           "fee_only_failed_tx",
		ProgramID:             "",
		ContractAddress:       nativeTokenContract(batch.Chain),
		Address:               ntx.FeePayer,
		CounterpartyAddress:   "",
		Delta:                 negFee,
		ChainData:             json.RawMessage("{}"),
		TokenSymbol:           nativeTokenSymbol(batch.Chain),
		TokenName:             nativeTokenName(batch.Chain),
		TokenDecimals:         nativeTokenDecimals(batch.Chain),
		TokenType:             model.TokenTypeNative,
		EventID:               fmt.Sprintf("fee:%s:%s", batch.Chain, ntx.TxHash),
		BlockHash:             ntx.BlockHash,
		TxIndex:               0,
		EventPath:             "fee_only",
		EventPathType:         "synthetic",
		FinalityState:         "",
	}
}

// nativeTokenContract returns the native token contract identifier for a chain.
func nativeTokenContract(ch model.Chain) string {
	switch ch {
	case model.ChainSolana:
		return "So11111111111111111111111111111111111111112"
	case model.ChainBTC:
		return "btc_native"
	default:
		// EVM chains: native ETH/MATIC/BNB uses zero address convention
		return "0x0000000000000000000000000000000000000000"
	}
}

// nativeTokenSymbol returns the native token symbol for a chain.
func nativeTokenSymbol(ch model.Chain) string {
	switch ch {
	case model.ChainSolana:
		return "SOL"
	case model.ChainEthereum, model.ChainBase, model.ChainArbitrum:
		return "ETH"
	case model.ChainPolygon:
		return "MATIC"
	case model.ChainBSC:
		return "BNB"
	case model.ChainBTC:
		return "BTC"
	default:
		return "NATIVE"
	}
}

// nativeTokenName returns the native token name for a chain.
func nativeTokenName(ch model.Chain) string {
	switch ch {
	case model.ChainSolana:
		return "Solana"
	case model.ChainEthereum:
		return "Ether"
	case model.ChainBase:
		return "Ether"
	case model.ChainArbitrum:
		return "Ether"
	case model.ChainPolygon:
		return "MATIC"
	case model.ChainBSC:
		return "BNB"
	case model.ChainBTC:
		return "Bitcoin"
	default:
		return "Native Token"
	}
}

// nativeTokenDecimals returns the native token decimals for a chain.
func nativeTokenDecimals(ch model.Chain) int {
	switch ch {
	case model.ChainSolana:
		return 9
	case model.ChainBTC:
		return 8
	default:
		return 18 // EVM chains
	}
}

// oldestBlockTime returns the earliest block_time from the batch transactions.
func oldestBlockTime(batch event.NormalizedBatch) *time.Time {
	var oldest *time.Time
	for _, tx := range batch.Transactions {
		if tx.BlockTime == nil {
			continue
		}
		if oldest == nil || tx.BlockTime.Before(*oldest) {
			oldest = tx.BlockTime
		}
	}
	return oldest
}
