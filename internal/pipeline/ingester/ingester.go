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

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/emperorhan/multichain-indexer/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelTrace "go.opentelemetry.io/otel/trace"
	"github.com/google/uuid"
)

const (
	defaultProcessRetryMaxAttempts = 3
	defaultRetryDelayInitial       = 100 * time.Millisecond
	defaultRetryDelayMax           = 1 * time.Second
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
	logger            *slog.Logger
	commitInterleaver CommitInterleaver
	reorgHandler      func(context.Context, *sql.Tx, event.NormalizedBatch) error
	retryMaxAttempts  int
	retryDelayStart   time.Duration
	retryDelayMax     time.Duration
	sleepFn           func(context.Context, time.Duration) error
}

type Option func(*Ingester)

func WithCommitInterleaver(interleaver CommitInterleaver) Option {
	return func(ing *Ingester) {
		ing.commitInterleaver = interleaver
	}
}

func WithReorgHandler(handler func(context.Context, *sql.Tx, event.NormalizedBatch) error) Option {
	return func(ing *Ingester) {
		ing.reorgHandler = handler
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
	prevIdentity := canonicalSignatureIdentity(batch.Chain, *batch.PreviousCursorValue)
	if prevIdentity == "" {
		prevIdentity = strings.TrimSpace(*batch.PreviousCursorValue)
	}
	newIdentity := canonicalSignatureIdentity(batch.Chain, *batch.NewCursorValue)
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

	previousCursor := canonicalizeCursorValue(batch.Chain, batch.PreviousCursorValue)
	newCursor := canonicalizeCursorValue(batch.Chain, batch.NewCursorValue)
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
	batch.PreviousCursorValue = canonicalizeCursorValue(batch.Chain, batch.PreviousCursorValue)
	batch.NewCursorValue = canonicalizeCursorValue(batch.Chain, batch.NewCursorValue)
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
			if err := dbTx.Commit(); err != nil {
				return fmt.Errorf("commit rollback path: %w", err)
			}
			committed = true

			ing.logger.Info("rollback path executed",
				"address", batch.Address,
				"previous_cursor", batch.PreviousCursorValue,
				"new_cursor", batch.NewCursorValue,
				"fork_cursor_sequence", rollbackBatch.PreviousCursorSequence,
			)
			return nil
		}
	}

	var totalEvents int
	deniedCache := make(map[string]bool) // "chain:network:contract" → denied

	for _, ntx := range batch.Transactions {
		canonicalTxHash := canonicalSignatureIdentity(batch.Chain, ntx.TxHash)
		if canonicalTxHash == "" {
			canonicalTxHash = strings.TrimSpace(ntx.TxHash)
		}

		// 1. Upsert transaction
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
		}
		if txModel.ChainData == nil {
			txModel.ChainData = json.RawMessage("{}")
		}

		txID, err := ing.txRepo.UpsertTx(ctx, dbTx, txModel)
		if err != nil {
			return fmt.Errorf("upsert tx %s: %w", ntx.TxHash, err)
		}

		// 2. Process balance events
		for _, be := range ntx.BalanceEvents {
			// 2a. Upsert token
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
			tokenID, err := ing.tokenRepo.UpsertTx(ctx, dbTx, tokenModel)
			if err != nil {
				return fmt.Errorf("upsert token %s: %w", be.ContractAddress, err)
			}

			// CHECK 1: Blacklist enforcement (with per-batch cache)
			deniedKey := fmt.Sprintf("%s:%s:%s", batch.Chain, batch.Network, be.ContractAddress)
			denied, cached := deniedCache[deniedKey]
			if !cached {
				denied, err = ing.tokenRepo.IsDeniedTx(ctx, dbTx, batch.Chain, batch.Network, be.ContractAddress)
				if err != nil {
					return fmt.Errorf("check token denied %s: %w", be.ContractAddress, err)
				}
				deniedCache[deniedKey] = denied
			}
			if denied {
				ing.logger.Debug("skipping denied token event",
					"contract", be.ContractAddress,
					"address", be.Address,
					"delta", be.Delta,
				)
				metrics.IngesterDeniedEventsSkipped.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
				continue
			}

			balanceBefore, balanceExists, balanceAfter, err := ing.computeBalanceTransition(ctx, dbTx, batch, be, tokenID)
			if err != nil {
				return fmt.Errorf("compute balance transition: %w", err)
			}

			// CHECK 2: Auto-detection of scam signals
			if signal := detectScamSignal(batch.Chain, be, balanceBefore, balanceExists); signal != "" {
				ing.logger.Warn("scam token detected, auto-denying",
					"signal", signal,
					"contract", be.ContractAddress,
					"address", be.Address,
					"delta", be.Delta,
					"balance_before", balanceBefore,
					"balance_exists", balanceExists,
				)
				if err := ing.tokenRepo.DenyTokenTx(
					ctx, dbTx,
					batch.Chain, batch.Network, be.ContractAddress,
					fmt.Sprintf("auto-detected: %s", signal),
					"ingester_auto",
					100,
					[]string{signal},
				); err != nil {
					return fmt.Errorf("deny scam token %s: %w", be.ContractAddress, err)
				}
				deniedCache[deniedKey] = true
				metrics.IngesterScamTokensDetected.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
				metrics.IngesterDeniedEventsSkipped.WithLabelValues(batch.Chain.String(), batch.Network.String()).Inc()
				continue
			}

			// 2b. Upsert balance event
			chainData := be.ChainData
			if chainData == nil {
				chainData = json.RawMessage("{}")
			}
			beModel := &model.BalanceEvent{
				Chain:                 batch.Chain,
				Network:               batch.Network,
				TransactionID:         txID,
				TxHash:                canonicalTxHash,
				OuterInstructionIndex: be.OuterInstructionIndex,
				InnerInstructionIndex: be.InnerInstructionIndex,
				TokenID:               tokenID,
				EventCategory:         be.EventCategory,
				EventAction:           be.EventAction,
				ProgramID:             be.ProgramID,
				Address:               be.Address,
				CounterpartyAddress:   be.CounterpartyAddress,
				Delta:                 be.Delta,
				BalanceBefore:         &balanceBefore,
				BalanceAfter:          &balanceAfter,
				WatchedAddress:        &batch.Address,
				WalletID:              batch.WalletID,
				OrganizationID:        batch.OrgID,
				BlockCursor:           ntx.BlockCursor,
				BlockTime:             ntx.BlockTime,
				ChainData:             chainData,
				EventID:               be.EventID,
				BlockHash:             be.BlockHash,
				TxIndex:               be.TxIndex,
				EventPath:             be.EventPath,
				EventPathType:         be.EventPathType,
				ActorAddress:          be.ActorAddress,
				AssetType:             be.AssetType,
				AssetID:               be.AssetID,
				FinalityState:         be.FinalityState,
				DecoderVersion:        be.DecoderVersion,
				SchemaVersion:         be.SchemaVersion,
			}
			inserted, err := ing.balanceEventRepo.UpsertTx(ctx, dbTx, beModel)
			if err != nil {
				return fmt.Errorf("upsert balance event: %w", err)
			}

			if inserted {
				// 2c. Update balance (delta is already signed)
				if err := ing.balanceRepo.AdjustBalanceTx(
					ctx, dbTx,
					batch.Chain, batch.Network, be.Address,
					tokenID, batch.WalletID, batch.OrgID,
					be.Delta, ntx.BlockCursor, canonicalTxHash,
				); err != nil {
					return fmt.Errorf("adjust balance: %w", err)
				}

				totalEvents++
			}
		}
	}

	// 3. Update cursor
	if batch.NewCursorValue != nil {
		if err := ing.cursorRepo.UpsertTx(
			ctx, dbTx,
			batch.Chain, batch.Network, batch.Address,
			batch.NewCursorValue, batch.NewCursorSequence,
			int64(len(batch.Transactions)),
		); err != nil {
			return fmt.Errorf("update cursor: %w", err)
		}
	}

	// 4. Update watermark
	if err := ing.configRepo.UpdateWatermarkTx(
		ctx, dbTx,
		batch.Chain, batch.Network, batch.NewCursorSequence,
	); err != nil {
		return fmt.Errorf("update watermark: %w", err)
	}

	// 5. Commit
	if err := dbTx.Commit(); err != nil {
		reconciled, reconcileErr := ing.reconcileAmbiguousCommitOutcome(ctx, batch, err)
		if reconcileErr != nil {
			return reconcileErr
		}
		if reconciled {
			committed = true
			ing.logger.Warn("commit outcome reconciled as committed",
				"chain", batch.Chain,
				"network", batch.Network,
				"address", batch.Address,
				"cursor_sequence", batch.NewCursorSequence,
				"cursor_value", batch.NewCursorValue,
				"commit_error", err,
			)
			return nil
		}
		return fmt.Errorf("commit: %w", err)
	}
	committed = true

	metrics.IngesterBalanceEventsWritten.WithLabelValues(batch.Chain.String(), batch.Network.String()).Add(float64(totalEvents))
	metrics.PipelineCursorSequence.WithLabelValues(batch.Chain.String(), batch.Network.String(), batch.Address).Set(float64(batch.NewCursorSequence))

	ing.logger.Info("batch ingested",
		"address", batch.Address,
		"txs", len(batch.Transactions),
		"balance_events", totalEvents,
		"cursor", batch.NewCursorValue,
	)

	return nil
}

func (ing *Ingester) computeBalanceTransition(
	ctx context.Context,
	tx *sql.Tx,
	batch event.NormalizedBatch,
	be event.NormalizedBalanceEvent,
	tokenID uuid.UUID,
) (string, bool, string, error) {
	beforeAmount, exists, err := ing.balanceRepo.GetAmountWithExistsTx(ctx, tx, batch.Chain, batch.Network, be.Address, tokenID)
	if err != nil {
		return "", false, "", fmt.Errorf("get current balance: %w", err)
	}

	afterAmount, err := addDecimalStrings(beforeAmount, be.Delta)
	if err != nil {
		return "", false, "", fmt.Errorf("apply delta %s to balance %s: %w", be.Delta, beforeAmount, err)
	}

	return beforeAmount, exists, afterAmount, nil
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
		invertedDelta, err := negateDecimalString(be.Delta)
		if err != nil {
			return fmt.Errorf("negate delta for %s: %w", be.TxHash, err)
		}

		if err := ing.balanceRepo.AdjustBalanceTx(
			ctx, dbTx,
			batch.Chain, batch.Network, be.Address,
			be.TokenID, be.WalletID, be.OrganizationID,
			invertedDelta, be.BlockCursor, be.TxHash,
		); err != nil {
			return fmt.Errorf("revert balance: %w", err)
		}
	}

	if _, err := dbTx.ExecContext(ctx, `
		DELETE FROM balance_events
		WHERE chain = $1 AND network = $2 AND watched_address = $3 AND block_cursor >= $4
	`, batch.Chain, batch.Network, batch.Address, forkCursor); err != nil {
		return fmt.Errorf("delete rollback balance events: %w", err)
	}

	rewindCursorValue, rewindCursorSequence, err := ing.findRewindCursor(
		ctx, dbTx, batch.Chain, batch.Network, batch.Address, forkCursor,
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

	if err := ing.configRepo.UpdateWatermarkTx(
		ctx, dbTx,
		batch.Chain, batch.Network, rewindCursorSequence,
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
		SELECT token_id, address, delta, block_cursor, tx_hash, wallet_id, organization_id
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
		if err := rows.Scan(&be.TokenID, &be.Address, &be.Delta, &be.BlockCursor, &be.TxHash, &walletID, &organizationID); err != nil {
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
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("query rewind cursor: %w", err)
	}

	return &cursorValue, cursorSequence, nil
}

func negateDecimalString(value string) (string, error) {
	var delta big.Int
	if _, ok := delta.SetString(value, 10); !ok {
		return "", fmt.Errorf("invalid decimal value: %s", value)
	}
	delta.Neg(&delta)
	return delta.String(), nil
}

func canonicalizeCursorValue(chainID model.Chain, cursor *string) *string {
	if cursor == nil {
		return nil
	}
	identity := canonicalSignatureIdentity(chainID, *cursor)
	if identity == "" {
		return nil
	}
	value := identity
	return &value
}

func (ing *Ingester) reconcileAmbiguousCommitOutcome(
	ctx context.Context,
	batch event.NormalizedBatch,
	commitErr error,
) (bool, error) {
	if !isAmbiguousCommitAckError(commitErr) {
		return false, nil
	}

	expectedCursor := canonicalizeCursorValue(batch.Chain, batch.NewCursorValue)
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
		if normalized := canonicalizeCursorValue(batch.Chain, cursor.CursorValue); normalized != nil {
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

	observedCursor := canonicalizeCursorValue(chain, cursor.CursorValue)
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

func canonicalSignatureIdentity(chainID model.Chain, hash string) string {
	trimmed := strings.TrimSpace(hash)
	if trimmed == "" {
		return ""
	}
	if chainID == model.ChainBTC {
		withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
		if withoutPrefix == "" {
			return ""
		}
		return strings.ToLower(withoutPrefix)
	}
	if !isEVMChain(chainID) {
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

func isEVMChain(chainID model.Chain) bool {
	return chainID == model.ChainBase || chainID == model.ChainEthereum
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
