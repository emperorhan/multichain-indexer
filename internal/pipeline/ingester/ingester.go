package ingester

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"math/rand/v2"
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

// blockScanAddrCacheTTL is how long the block-scan watched address cache is valid.
const blockScanAddrCacheTTL = 30 * time.Second

// Ingester is a single-writer that processes NormalizedBatches into the database.
type Ingester struct {
	db                store.TxBeginner
	txRepo            store.TransactionRepository
	balanceEventRepo  store.BalanceEventRepository
	balanceRepo       store.BalanceRepository
	tokenRepo         store.TokenRepository
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
	deniedCache       cache.Cache[string, bool]
	replayService     *replay.Service
	watchedAddrRepo   store.WatchedAddressRepository
	addressIndex      addressindex.Index

	// Block-scan watched address cache (single-writer, no mutex needed).
	blockScanAddrCache   map[string]map[string]addrMeta // key: "chain:network"
	blockScanAddrCacheAt map[string]time.Time
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
		ing.deniedCache = cache.NewShardedLRU[string, bool](capacity, ttl, func(k string) string { return k })
	}
}

func New(
	db store.TxBeginner,
	txRepo store.TransactionRepository,
	balanceEventRepo store.BalanceEventRepository,
	balanceRepo store.BalanceRepository,
	tokenRepo store.TokenRepository,
	configRepo store.IndexerConfigRepository,
	normalizedCh <-chan event.NormalizedBatch,
	logger *slog.Logger,
	opts ...Option,
) *Ingester {
	ing := &Ingester{
		db:                   db,
		txRepo:               txRepo,
		balanceEventRepo:     balanceEventRepo,
		balanceRepo:          balanceRepo,
		tokenRepo:            tokenRepo,
		configRepo:           configRepo,
		normalizedCh:         normalizedCh,
		logger:               logger.With("component", "ingester"),
		reorgHandler:         nil,
		retryMaxAttempts:     defaultProcessRetryMaxAttempts,
		retryDelayStart:      defaultRetryDelayInitial,
		retryDelayMax:        defaultRetryDelayMax,
		sleepFn:              sleepContext,
		deniedCache:          cache.NewShardedLRU[string, bool](defaultDeniedCacheCapacity, defaultDeniedCacheTTL, func(k string) string { return k }),
		blockScanAddrCache:   make(map[string]map[string]addrMeta),
		blockScanAddrCacheAt: make(map[string]time.Time),
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

// batchContext holds intermediate state shared across processBatch phases.
type batchContext struct {
	batch            event.NormalizedBatch
	dbTx             *sql.Tx
	blockScanAddrMap map[string]addrMeta

	// Phase 1 outputs
	txModels         []*model.Transaction
	tokenModels      []*model.Token
	contractsToCheck map[string]struct{}
	allEvents        []eventContext

	// Phase 2 outputs
	txIDMap      map[string]uuid.UUID
	tokenIDMap   map[string]uuid.UUID
	cachedDenied map[string]bool
	balanceMap   map[store.BalanceKey]store.BalanceInfo

	// Phase 3 outputs
	eventModels      []*model.BalanceEvent
	aggregatedDeltas map[adjustmentKey]*adjustmentAccum
	totalEvents      int
}

type addrMeta struct {
	walletID *string
	orgID    *string
}

type eventContext struct {
	ntx             event.NormalizedTransaction
	be              event.NormalizedBalanceEvent
	canonicalTxHash string
}

type adjustmentKey struct {
	store.BalanceKey
	walletID string
	orgID    string
}

type adjustmentAccum struct {
	delta  *big.Int
	cursor int64
	txHash string
}

type balanceAccum struct {
	amount string
	exists bool
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

	bc := &batchContext{batch: batch, dbTx: dbTx}

	// Block-scan mode: resolve per-address wallet/org mapping (cached)
	if batch.BlockScanMode && ing.addressIndex == nil && ing.watchedAddrRepo != nil {
		addrMap, err := ing.getBlockScanAddrMap(ctx, batch.Chain, batch.Network)
		if err != nil {
			return fmt.Errorf("fetch watched addresses for block-scan: %w", err)
		}
		bc.blockScanAddrMap = addrMap
	}

	if err := ing.collectEvents(ctx, bc); err != nil {
		return err
	}
	if err := ing.prefetchBulkData(ctx, bc); err != nil {
		return err
	}
	if err := ing.buildEventModels(ctx, bc); err != nil {
		return err
	}

	totalEvents, err := ing.writeBulkAndCommit(ctx, bc)
	if err != nil {
		return err
	}
	committed = true

	metrics.IngesterBalanceEventsWritten.WithLabelValues(batch.Chain.String(), batch.Network.String()).Add(float64(totalEvents))
	metrics.PipelineCursorSequence.WithLabelValues(batch.Chain.String(), batch.Network.String()).Set(float64(batch.NewCursorSequence))

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

// collectEvents deduplicates transactions/tokens and collects all balance event contexts (Phase 1).
func (ing *Ingester) collectEvents(ctx context.Context, bc *batchContext) error {
	_, span := tracing.Tracer("ingester").Start(ctx, "ingester.phase1_collect")
	defer span.End()

	batch := bc.batch
	txModelsByHash := make(map[string]*model.Transaction, len(batch.Transactions))
	tokenModelsByContract := make(map[string]*model.Token)
	bc.contractsToCheck = make(map[string]struct{})
	bc.allEvents = make([]eventContext, 0, len(batch.Transactions)*3)

	for _, ntx := range batch.Transactions {
		canonicalTxHash := identity.CanonicalSignatureIdentity(batch.Chain, ntx.TxHash)
		if canonicalTxHash == "" {
			canonicalTxHash = ntx.TxHash
		}

		if _, exists := txModelsByHash[canonicalTxHash]; !exists {
			txModel := &model.Transaction{
				Chain: batch.Chain, Network: batch.Network,
				TxHash: canonicalTxHash, BlockCursor: ntx.BlockCursor, BlockTime: ntx.BlockTime,
				FeeAmount: ntx.FeeAmount, FeePayer: ntx.FeePayer, Status: ntx.Status,
				Err: ntx.Err, ChainData: ntx.ChainData, BlockHash: ntx.BlockHash, ParentHash: ntx.ParentHash,
			}
			if txModel.ChainData == nil {
				txModel.ChainData = json.RawMessage("{}")
			}
			txModelsByHash[canonicalTxHash] = txModel
			bc.txModels = append(bc.txModels, txModel)
		}

		for _, be := range ntx.BalanceEvents {
			if _, exists := tokenModelsByContract[be.ContractAddress]; !exists {
				tokenModel := &model.Token{
					Chain: batch.Chain, Network: batch.Network,
					ContractAddress: be.ContractAddress, Symbol: defaultTokenSymbol(be),
					Name: defaultTokenName(be), Decimals: be.TokenDecimals,
					TokenType: be.TokenType, ChainData: json.RawMessage("{}"),
				}
				tokenModelsByContract[be.ContractAddress] = tokenModel
				bc.tokenModels = append(bc.tokenModels, tokenModel)
			}
			bc.contractsToCheck[be.ContractAddress] = struct{}{}
			bc.allEvents = append(bc.allEvents, eventContext{ntx: ntx, be: be, canonicalTxHash: canonicalTxHash})
		}

		// GAP-6: synthetic fee-only withdrawal for failed txs
		if ntx.Status == model.TxStatusFailed && len(ntx.BalanceEvents) == 0 &&
			ntx.FeeAmount != "" && ntx.FeeAmount != "0" && ntx.FeePayer != "" {
			feeOnlyEvent := buildFeeOnlyEvent(ntx, batch)
			nativeContract := nativeTokenContract(batch.Chain)
			if _, exists := tokenModelsByContract[nativeContract]; !exists {
				tokenModel := &model.Token{
					Chain: batch.Chain, Network: batch.Network,
					ContractAddress: nativeContract, Symbol: nativeTokenSymbol(batch.Chain),
					Name: nativeTokenName(batch.Chain), Decimals: nativeTokenDecimals(batch.Chain),
					TokenType: model.TokenTypeNative, ChainData: json.RawMessage("{}"),
				}
				tokenModelsByContract[nativeContract] = tokenModel
				bc.tokenModels = append(bc.tokenModels, tokenModel)
			}
			bc.contractsToCheck[nativeContract] = struct{}{}
			bc.allEvents = append(bc.allEvents, eventContext{ntx: ntx, be: feeOnlyEvent, canonicalTxHash: canonicalTxHash})
		}
	}

	span.SetAttributes(
		attribute.Int("unique_transactions", len(bc.txModels)),
		attribute.Int("unique_tokens", len(bc.tokenModels)),
		attribute.Int("total_events", len(bc.allEvents)),
	)
	return nil
}

// prefetchBulkData performs bulk DB upserts/queries for transactions, tokens, denied checks, and balances (Phase 2).
func (ing *Ingester) prefetchBulkData(ctx context.Context, bc *batchContext) error {
	_, span := tracing.Tracer("ingester").Start(ctx, "ingester.phase2_prefetch")
	defer span.End()

	batch := bc.batch
	var err error

	bc.txIDMap, err = ing.txRepo.BulkUpsertTx(ctx, bc.dbTx, bc.txModels)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("bulk upsert transactions: %w", err)
	}

	bc.tokenIDMap, err = ing.tokenRepo.BulkUpsertTx(ctx, bc.dbTx, bc.tokenModels)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("bulk upsert tokens: %w", err)
	}

	// Denied check (LRU cache first, then DB for misses)
	uncachedContracts := make([]string, 0)
	bc.cachedDenied = make(map[string]bool)
	for contract := range bc.contractsToCheck {
		deniedKey := fmt.Sprintf("%s:%s:%s", batch.Chain, batch.Network, contract)
		if denied, ok := ing.deniedCache.Get(deniedKey); ok {
			bc.cachedDenied[contract] = denied
			metrics.DeniedCacheHits.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
		} else {
			uncachedContracts = append(uncachedContracts, contract)
			metrics.DeniedCacheMisses.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
		}
	}
	if len(uncachedContracts) > 0 {
		dbDenied, err := ing.tokenRepo.BulkIsDeniedTx(ctx, bc.dbTx, batch.Chain, batch.Network, uncachedContracts)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("bulk check denied tokens: %w", err)
		}
		for _, contract := range uncachedContracts {
			denied := dbDenied[contract]
			deniedKey := fmt.Sprintf("%s:%s:%s", batch.Chain, batch.Network, contract)
			ing.deniedCache.Put(deniedKey, denied)
			bc.cachedDenied[contract] = denied
		}
	}

	// Pre-fetch balances
	balanceKeys := make([]store.BalanceKey, 0, len(bc.allEvents))
	balanceKeySet := make(map[store.BalanceKey]struct{})
	for _, ec := range bc.allEvents {
		tokenID, ok := bc.tokenIDMap[ec.be.ContractAddress]
		if !ok {
			continue
		}
		bk := store.BalanceKey{Address: ec.be.Address, TokenID: tokenID, BalanceType: ""}
		if _, exists := balanceKeySet[bk]; !exists {
			balanceKeySet[bk] = struct{}{}
			balanceKeys = append(balanceKeys, bk)
		}
	}
	if len(balanceKeys) > 0 {
		bc.balanceMap, err = ing.balanceRepo.BulkGetAmountWithExistsTx(ctx, bc.dbTx, batch.Chain, batch.Network, balanceKeys)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("bulk get balances: %w", err)
		}
	} else {
		bc.balanceMap = make(map[store.BalanceKey]store.BalanceInfo)
	}

	span.SetAttributes(
		attribute.Int("cached_denied", len(bc.cachedDenied)),
		attribute.Int("balance_keys", len(balanceKeys)),
	)
	return nil
}

// buildEventModels performs in-memory balance accumulation, scam detection, and model construction (Phase 3).
func (ing *Ingester) buildEventModels(ctx context.Context, bc *batchContext) error {
	_, span := tracing.Tracer("ingester").Start(ctx, "ingester.phase3_process")
	defer span.End()

	batch := bc.batch
	inMemoryBalances := make(map[store.BalanceKey]*balanceAccum, len(bc.balanceMap))
	for bk, bi := range bc.balanceMap {
		inMemoryBalances[bk] = &balanceAccum{amount: bi.Amount, exists: bi.Exists}
	}
	bc.aggregatedDeltas = make(map[adjustmentKey]*adjustmentAccum)

	for _, ec := range bc.allEvents {
		tokenID, ok := bc.tokenIDMap[ec.be.ContractAddress]
		if !ok {
			continue
		}
		txID, ok := bc.txIDMap[ec.canonicalTxHash]
		if !ok {
			continue
		}

		if bc.cachedDenied[ec.be.ContractAddress] {
			ing.logger.Debug("skipping denied token event",
				"contract", ec.be.ContractAddress, "address", ec.be.Address, "delta", ec.be.Delta,
			)
			metrics.IngesterDeniedEventsSkipped.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
			continue
		}

		bk := store.BalanceKey{Address: ec.be.Address, TokenID: tokenID, BalanceType: ""}
		accum, ok := inMemoryBalances[bk]
		if !ok {
			accum = &balanceAccum{amount: "0", exists: false}
			inMemoryBalances[bk] = accum
		}
		balanceBefore := accum.amount
		balanceExists := accum.exists

		if signal := detectScamSignal(batch.Chain, ec.be, balanceBefore, balanceExists); signal != "" {
			ing.logger.Warn("scam token detected, auto-denying",
				"signal", signal, "contract", ec.be.ContractAddress,
				"address", ec.be.Address, "delta", ec.be.Delta,
				"balance_before", balanceBefore, "balance_exists", balanceExists,
			)
			if err := ing.tokenRepo.DenyTokenTx(
				ctx, bc.dbTx, batch.Chain, batch.Network, ec.be.ContractAddress,
				fmt.Sprintf("auto-detected: %s", signal), "ingester_auto", 100, []string{signal},
			); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return fmt.Errorf("deny scam token %s: %w", ec.be.ContractAddress, err)
			}
			deniedKey := fmt.Sprintf("%s:%s:%s", batch.Chain, batch.Network, ec.be.ContractAddress)
			ing.deniedCache.Put(deniedKey, true)
			bc.cachedDenied[ec.be.ContractAddress] = true
			metrics.IngesterScamTokensDetected.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
			metrics.IngesterDeniedEventsSkipped.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
			continue
		}

		balanceAfter, calcErr := addDecimalStrings(balanceBefore, ec.be.Delta)
		if calcErr != nil {
			span.RecordError(calcErr)
			span.SetStatus(codes.Error, calcErr.Error())
			return fmt.Errorf("apply delta %s to balance %s: %w", ec.be.Delta, balanceBefore, calcErr)
		}

		chainData := ec.be.ChainData
		if chainData == nil {
			chainData = json.RawMessage("{}")
		}
		eventWalletID := batch.WalletID
		eventOrgID := batch.OrgID
		eventWatchedAddr := &batch.Address
		if batch.BlockScanMode {
			// Try to resolve watched address from be.Address first, then counterparty
			resolved := false
			candidates := []string{ec.be.Address, ec.be.CounterpartyAddress}
			for _, candidate := range candidates {
				if candidate == "" {
					continue
				}
				if ing.addressIndex != nil {
					if wa := ing.addressIndex.Lookup(ctx, batch.Chain, batch.Network, candidate); wa != nil {
						c := candidate
						eventWatchedAddr = &c
						eventWalletID = wa.WalletID
						eventOrgID = wa.OrganizationID
						resolved = true
						break
					}
				} else if bc.blockScanAddrMap != nil {
					if meta, ok := bc.blockScanAddrMap[candidate]; ok {
						c := candidate
						eventWatchedAddr = &c
						eventWalletID = meta.walletID
						eventOrgID = meta.orgID
						resolved = true
						break
					}
				}
			}
			if !resolved {
				addr := ec.be.Address
				eventWatchedAddr = &addr
			}
		}

		beModel := &model.BalanceEvent{
			Chain: batch.Chain, Network: batch.Network,
			TransactionID: txID, TxHash: ec.canonicalTxHash,
			OuterInstructionIndex: ec.be.OuterInstructionIndex,
			InnerInstructionIndex: ec.be.InnerInstructionIndex,
			TokenID: tokenID, ActivityType: ec.be.ActivityType,
			EventAction: ec.be.EventAction, ProgramID: ec.be.ProgramID,
			Address: ec.be.Address, CounterpartyAddress: ec.be.CounterpartyAddress,
			Delta: ec.be.Delta, WatchedAddress: eventWatchedAddr,
			WalletID: eventWalletID, OrganizationID: eventOrgID,
			BlockCursor: ec.ntx.BlockCursor, BlockTime: ec.ntx.BlockTime,
			ChainData: chainData, EventID: ec.be.EventID,
			BlockHash: ec.be.BlockHash, TxIndex: ec.be.TxIndex,
			EventPath: ec.be.EventPath, EventPathType: ec.be.EventPathType,
			ActorAddress: ec.be.ActorAddress, AssetType: ec.be.AssetType,
			AssetID: ec.be.AssetID, FinalityState: ec.be.FinalityState,
			DecoderVersion: ec.be.DecoderVersion, SchemaVersion: ec.be.SchemaVersion,
		}
		beModel.BalanceApplied = meetsBalanceThreshold(batch.Chain, beModel.FinalityState)
		if beModel.BalanceApplied {
			beModel.BalanceBefore = &balanceBefore
			beModel.BalanceAfter = &balanceAfter
		}
		bc.eventModels = append(bc.eventModels, beModel)

		if beModel.BalanceApplied {
			accum.amount = balanceAfter
			accum.exists = true

			ak := adjustmentKey{
				BalanceKey: store.BalanceKey{Address: ec.be.Address, TokenID: tokenID, BalanceType: ""},
				walletID: derefStr(eventWalletID), orgID: derefStr(eventOrgID),
			}
			delta := new(big.Int)
			if _, ok := delta.SetString(strings.TrimSpace(ec.be.Delta), 10); !ok {
				err := fmt.Errorf("invalid delta value %q for event %s", ec.be.Delta, ec.be.EventID)
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return err
			}
			if existing, ok := bc.aggregatedDeltas[ak]; ok {
				existing.delta.Add(existing.delta, delta)
				if ec.ntx.BlockCursor > existing.cursor {
					existing.cursor = ec.ntx.BlockCursor
					existing.txHash = ec.canonicalTxHash
				}
			} else {
				bc.aggregatedDeltas[ak] = &adjustmentAccum{delta: delta, cursor: ec.ntx.BlockCursor, txHash: ec.canonicalTxHash}
			}

			if identity.IsStakingActivity(beModel.ActivityType) {
				invertedDelta, negErr := identity.NegateDecimalString(ec.be.Delta)
				if negErr != nil {
					span.RecordError(negErr)
					span.SetStatus(codes.Error, negErr.Error())
					return fmt.Errorf("negate staking delta: %w", negErr)
				}
				sak := adjustmentKey{
					BalanceKey: store.BalanceKey{Address: ec.be.Address, TokenID: tokenID, BalanceType: "staked"},
					walletID: derefStr(eventWalletID), orgID: derefStr(eventOrgID),
				}
				stakeDelta := new(big.Int)
				if _, ok := stakeDelta.SetString(strings.TrimSpace(invertedDelta), 10); !ok {
					err := fmt.Errorf("invalid staking delta value %q for event %s", invertedDelta, ec.be.EventID)
					span.RecordError(err)
					span.SetStatus(codes.Error, err.Error())
					return err
				}
				if existing, ok := bc.aggregatedDeltas[sak]; ok {
					existing.delta.Add(existing.delta, stakeDelta)
					if ec.ntx.BlockCursor > existing.cursor {
						existing.cursor = ec.ntx.BlockCursor
						existing.txHash = ec.canonicalTxHash
					}
				} else {
					bc.aggregatedDeltas[sak] = &adjustmentAccum{delta: stakeDelta, cursor: ec.ntx.BlockCursor, txHash: ec.canonicalTxHash}
				}
			}
		}
	}

	span.SetAttributes(
		attribute.Int("event_models_built", len(bc.eventModels)),
		attribute.Int("aggregated_deltas", len(bc.aggregatedDeltas)),
	)
	return nil
}

// writeBulkAndCommit performs all bulk writes, cursor/watermark updates, and commits the transaction (Phase 4).
// Returns the total number of events written.
func (ing *Ingester) writeBulkAndCommit(ctx context.Context, bc *batchContext) (int, error) {
	_, span := tracing.Tracer("ingester").Start(ctx, "ingester.phase4_write")
	defer span.End()

	batch := bc.batch
	totalEvents := 0

	if len(bc.eventModels) > 0 {
		bulkResult, err := ing.balanceEventRepo.BulkUpsertTx(ctx, bc.dbTx, bc.eventModels)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return 0, fmt.Errorf("bulk upsert balance events: %w", err)
		}
		totalEvents = bulkResult.InsertedCount + bulkResult.FinalityCrossedCount
	}

	if len(bc.aggregatedDeltas) > 0 {
		adjustItems := make([]store.BulkAdjustItem, 0, len(bc.aggregatedDeltas))
		for ak, acc := range bc.aggregatedDeltas {
			adjustItems = append(adjustItems, store.BulkAdjustItem{
				Address: ak.Address, TokenID: ak.TokenID, WalletID: strPtrOrNil(ak.walletID),
				OrgID: strPtrOrNil(ak.orgID), Delta: acc.delta.String(), Cursor: acc.cursor,
				TxHash: acc.txHash, BalanceType: ak.BalanceType,
			})
		}
		if err := ing.balanceRepo.BulkAdjustBalanceTx(ctx, bc.dbTx, batch.Chain, batch.Network, adjustItems); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return 0, fmt.Errorf("bulk adjust balances: %w", err)
		}
	}

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
					Chain: batch.Chain, Network: batch.Network,
					BlockNumber: ntx.BlockCursor, BlockHash: ntx.BlockHash,
					ParentHash: ntx.ParentHash, FinalityState: finalityState, BlockTime: ntx.BlockTime,
				}
			}
		}
		if len(blockMap) > 0 {
			blocks := make([]*model.IndexedBlock, 0, len(blockMap))
			for _, b := range blockMap {
				blocks = append(blocks, b)
			}
			if err := ing.blockRepo.BulkUpsertTx(ctx, bc.dbTx, blocks); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return 0, fmt.Errorf("bulk upsert indexed blocks: %w", err)
			}
		}
	}

	if err := ing.configRepo.UpdateWatermarkTx(ctx, bc.dbTx, batch.Chain, batch.Network, batch.NewCursorSequence); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("update watermark: %w", err)
	}

	commitStart := time.Now()
	if err := bc.dbTx.Commit(); err != nil {
		reconciled, reconcileErr := ing.reconcileAmbiguousCommitOutcome(ctx, batch, err)
		if reconcileErr != nil {
			span.RecordError(reconcileErr)
			span.SetStatus(codes.Error, reconcileErr.Error())
			return 0, reconcileErr
		}
		if reconciled {
			ing.recordCommitLatencyMs(batch.Chain.String(), batch.Network.String(), time.Since(commitStart).Milliseconds())
			ing.logger.Warn("commit outcome reconciled as committed",
				"chain", batch.Chain, "network", batch.Network, "address", batch.Address,
				"cursor_sequence", batch.NewCursorSequence, "cursor_value", batch.NewCursorValue, "commit_error", err,
			)
			return totalEvents, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, fmt.Errorf("commit: %w", err)
	}
	ing.recordCommitLatencyMs(batch.Chain.String(), batch.Network.String(), time.Since(commitStart).Milliseconds())

	return totalEvents, nil
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
	reversalItems, err := buildReversalAdjustItems(rollbackEvents)
	if err != nil {
		return fmt.Errorf("build reorg reversal items: %w", err)
	}
	if len(reversalItems) > 0 {
		if err := ing.balanceRepo.BulkAdjustBalanceTx(ctx, dbTx, reorg.Chain, reorg.Network, reversalItems); err != nil {
			return fmt.Errorf("revert balances: %w", err)
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

	// 6. Rewind watermark (unconditional — intentional reorg rollback)
	rewindSequence := reorg.ForkBlockNumber - 1
	if rewindSequence < 0 {
		rewindSequence = 0
	}
	if err := ing.configRepo.RewindWatermarkTx(ctx, dbTx, reorg.Chain, reorg.Network, rewindSequence); err != nil {
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
		adjustItems, err := buildPromotionAdjustItems(promotedEvents)
		if err != nil {
			return fmt.Errorf("build promotion adjust items: %w", err)
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

	// Check sidecar-propagated signal from metadata.
	// Pre-check with bytes.Contains to avoid unmarshal allocation for 99%+ events.
	if be.ChainData != nil && bytes.Contains(be.ChainData, []byte(`"scam_signal"`)) {
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

	reversalItems, err := buildReversalAdjustItems(rollbackEvents)
	if err != nil {
		return fmt.Errorf("build canonicality drift reversal items: %w", err)
	}
	if len(reversalItems) > 0 {
		if err := ing.balanceRepo.BulkAdjustBalanceTx(ctx, dbTx, batch.Chain, batch.Network, reversalItems); err != nil {
			return fmt.Errorf("revert balances: %w", err)
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

	if err := ing.configRepo.RewindWatermarkTx(
		ctx, dbTx,
		batch.Chain, batch.Network, rewindWatermarkSequence,
	); err != nil {
		return fmt.Errorf("rewind watermark after drift: %w", err)
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

// buildReversalAdjustItems creates bulk adjust items that reverse the balance
// deltas of the given events. For staking activities, an additional adjustment
// item is produced (inverted sign) for the "staked" balance type.
func buildReversalAdjustItems(events []rollbackBalanceEvent) ([]store.BulkAdjustItem, error) {
	items := make([]store.BulkAdjustItem, 0, len(events))
	for _, be := range events {
		if !be.BalanceApplied {
			continue
		}
		invertedDelta, err := identity.NegateDecimalString(be.Delta)
		if err != nil {
			return nil, fmt.Errorf("negate delta for %s: %w", be.TxHash, err)
		}
		items = append(items, store.BulkAdjustItem{
			Address:     be.Address,
			TokenID:     be.TokenID,
			WalletID:    be.WalletID,
			OrgID:       be.OrganizationID,
			Delta:       invertedDelta,
			Cursor:      be.BlockCursor,
			TxHash:      be.TxHash,
			BalanceType: "",
		})
		if identity.IsStakingActivity(be.ActivityType) {
			items = append(items, store.BulkAdjustItem{
				Address:     be.Address,
				TokenID:     be.TokenID,
				WalletID:    be.WalletID,
				OrgID:       be.OrganizationID,
				Delta:       be.Delta, // original (non-inverted) delta reverses the staked balance
				Cursor:      be.BlockCursor,
				TxHash:      be.TxHash,
				BalanceType: "staked",
			})
		}
	}
	return items, nil
}

// buildPromotionAdjustItems creates bulk adjust items that apply forward deltas
// for newly finalized events. For staking activities, an additional inverted
// adjustment is produced for the "staked" balance type.
func buildPromotionAdjustItems(events []promotedEvent) ([]store.BulkAdjustItem, error) {
	items := make([]store.BulkAdjustItem, 0, len(events))
	for _, pe := range events {
		items = append(items, store.BulkAdjustItem{
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
				return nil, fmt.Errorf("negate staking delta: %w", err)
			}
			items = append(items, store.BulkAdjustItem{
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
	return items, nil
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

func (ing *Ingester) reconcileAmbiguousCommitOutcome(
	ctx context.Context,
	batch event.NormalizedBatch,
	commitErr error,
) (bool, error) {
	if !isAmbiguousCommitAckError(commitErr) {
		return false, nil
	}

	return ing.reconcileBlockScanCommit(ctx, batch, commitErr)
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
			delay = ing.retryDelayMax
		}
	} else {
		for i := 1; i < attempt; i++ {
			delay *= 2
			if ing.retryDelayMax > 0 && delay >= ing.retryDelayMax {
				delay = ing.retryDelayMax
				break
			}
		}
	}

	// Add 0-25% random jitter to avoid thundering herd.
	if delay > 0 {
		jitter := time.Duration(rand.Int64N(int64(delay) / 4))
		delay += jitter
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

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// getBlockScanAddrMap returns the watched address map for block-scan mode,
// using a TTL cache to avoid querying the DB on every batch.
func (ing *Ingester) getBlockScanAddrMap(ctx context.Context, chain model.Chain, network model.Network) (map[string]addrMeta, error) {
	cacheKey := string(chain) + ":" + string(network)
	if cached, ok := ing.blockScanAddrCache[cacheKey]; ok {
		if time.Since(ing.blockScanAddrCacheAt[cacheKey]) < blockScanAddrCacheTTL {
			return cached, nil
		}
	}

	activeAddrs, err := ing.watchedAddrRepo.GetActive(ctx, chain, network)
	if err != nil {
		return nil, err
	}
	addrMap := make(map[string]addrMeta, len(activeAddrs))
	for _, wa := range activeAddrs {
		key := identity.CanonicalAddressIdentity(chain, wa.Address)
		addrMap[key] = addrMeta{walletID: wa.WalletID, orgID: wa.OrganizationID}
	}
	ing.blockScanAddrCache[cacheKey] = addrMap
	ing.blockScanAddrCacheAt[cacheKey] = time.Now()
	return addrMap, nil
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
