package ingester

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
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

func (ing *Ingester) processBatch(ctx context.Context, batch event.NormalizedBatch) error {
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
