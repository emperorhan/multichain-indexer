package ingester

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/google/uuid"
)

// Ingester is a single-writer that processes NormalizedBatches into the database.
type Ingester struct {
	db               store.TxBeginner
	txRepo           store.TransactionRepository
	balanceEventRepo store.BalanceEventRepository
	balanceRepo      store.BalanceRepository
	tokenRepo        store.TokenRepository
	cursorRepo       store.CursorRepository
	configRepo       store.IndexerConfigRepository
	normalizedCh     <-chan event.NormalizedBatch
	logger           *slog.Logger
	reorgHandler     func(context.Context, *sql.Tx, event.NormalizedBatch) error
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
) *Ingester {
	return &Ingester{
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
	}
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
			if err := ing.processBatch(ctx, batch); err != nil {
				ing.logger.Error("process batch failed",
					"address", batch.Address,
					"error", err,
				)
			}
		}
	}
}

func isCanonicalityDrift(batch event.NormalizedBatch) bool {
	if batch.PreviousCursorValue == nil || batch.NewCursorValue == nil {
		return false
	}
	if batch.PreviousCursorSequence == 0 {
		return false
	}
	if batch.NewCursorSequence < batch.PreviousCursorSequence {
		return true
	}
	if batch.NewCursorSequence == batch.PreviousCursorSequence &&
		*batch.PreviousCursorValue != *batch.NewCursorValue {
		return true
	}
	return false
}

func (ing *Ingester) processBatch(ctx context.Context, batch event.NormalizedBatch) error {
	if ing.reorgHandler == nil {
		ing.reorgHandler = ing.rollbackCanonicalityDrift
	}

	dbTx, err := ing.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rbErr := dbTx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
			ing.logger.Warn("rollback failed", "error", rbErr)
		}
	}()

	if isCanonicalityDrift(batch) {
		if err := ing.reorgHandler(ctx, dbTx, batch); err != nil {
			return fmt.Errorf("handle reorg path: %w", err)
		}

		if err := dbTx.Commit(); err != nil {
			return fmt.Errorf("commit rollback path: %w", err)
		}
		committed = true

		ing.logger.Info("rollback path executed",
			"address", batch.Address,
			"previous_cursor", batch.PreviousCursorValue,
			"new_cursor", batch.NewCursorValue,
			"fork_cursor_sequence", batch.PreviousCursorSequence,
		)
		return nil
	}

	var totalEvents int

	for _, ntx := range batch.Transactions {
		// 1. Upsert transaction
		txModel := &model.Transaction{
			Chain:       batch.Chain,
			Network:     batch.Network,
			TxHash:      ntx.TxHash,
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

			// 2b. Upsert balance event
			chainData := be.ChainData
			if chainData == nil {
				chainData = json.RawMessage("{}")
			}
			beModel := &model.BalanceEvent{
				Chain:                 batch.Chain,
				Network:               batch.Network,
				TransactionID:         txID,
				TxHash:                ntx.TxHash,
				OuterInstructionIndex: be.OuterInstructionIndex,
				InnerInstructionIndex: be.InnerInstructionIndex,
				TokenID:               tokenID,
				EventCategory:         be.EventCategory,
				EventAction:           be.EventAction,
				ProgramID:             be.ProgramID,
				Address:               be.Address,
				CounterpartyAddress:   be.CounterpartyAddress,
				Delta:                 be.Delta,
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
					be.Delta, ntx.BlockCursor, ntx.TxHash,
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
		return fmt.Errorf("commit: %w", err)
	}
	committed = true

	ing.logger.Info("batch ingested",
		"address", batch.Address,
		"txs", len(batch.Transactions),
		"balance_events", totalEvents,
		"cursor", batch.NewCursorValue,
	)

	return nil
}

func (ing *Ingester) rollbackCanonicalityDrift(ctx context.Context, dbTx *sql.Tx, batch event.NormalizedBatch) error {
	rollbackEvents, err := ing.fetchRollbackEvents(ctx, dbTx, batch.Chain, batch.Network, batch.Address, batch.PreviousCursorSequence)
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
	`, batch.Chain, batch.Network, batch.Address, batch.PreviousCursorSequence); err != nil {
		return fmt.Errorf("delete rollback balance events: %w", err)
	}

	rewindCursorValue, rewindCursorSequence, err := ing.findRewindCursor(
		ctx, dbTx, batch.Chain, batch.Network, batch.Address, batch.PreviousCursorSequence,
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
