package ingester

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

// Ingester is a single-writer that processes NormalizedBatches into the database.
type Ingester struct {
	db              store.TxBeginner
	txRepo          store.TransactionRepository
	transferRepo    store.TransferRepository
	balanceRepo     store.BalanceRepository
	tokenRepo       store.TokenRepository
	cursorRepo      store.CursorRepository
	configRepo      store.IndexerConfigRepository
	normalizedCh    <-chan event.NormalizedBatch
	logger          *slog.Logger
}

func New(
	db store.TxBeginner,
	txRepo store.TransactionRepository,
	transferRepo store.TransferRepository,
	balanceRepo store.BalanceRepository,
	tokenRepo store.TokenRepository,
	cursorRepo store.CursorRepository,
	configRepo store.IndexerConfigRepository,
	normalizedCh <-chan event.NormalizedBatch,
	logger *slog.Logger,
) *Ingester {
	return &Ingester{
		db:           db,
		txRepo:       txRepo,
		transferRepo: transferRepo,
		balanceRepo:  balanceRepo,
		tokenRepo:    tokenRepo,
		cursorRepo:   cursorRepo,
		configRepo:   configRepo,
		normalizedCh: normalizedCh,
		logger:       logger.With("component", "ingester"),
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
	defer dbTx.Rollback()

	var totalTransfers int

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

		// 2. Process transfers
		for _, nt := range ntx.Transfers {
			// 2a. Upsert token
			tokenModel := &model.Token{
				Chain:           batch.Chain,
				Network:         batch.Network,
				ContractAddress: nt.ContractAddress,
				Symbol:          defaultTokenSymbol(nt),
				Name:            defaultTokenName(nt),
				Decimals:        nt.TokenDecimals,
				TokenType:       nt.TokenType,
				ChainData:       json.RawMessage("{}"),
			}
			tokenID, err := ing.tokenRepo.UpsertTx(ctx, dbTx, tokenModel)
			if err != nil {
				return fmt.Errorf("upsert token %s: %w", nt.ContractAddress, err)
			}

			// 2b. Determine direction
			var direction *model.TransferDirection
			if batch.Address == nt.ToAddress {
				d := model.DirectionDeposit
				direction = &d
			} else if batch.Address == nt.FromAddress {
				d := model.DirectionWithdrawal
				direction = &d
			}

			// 2c. Upsert transfer
			chainData := nt.ChainData
			if chainData == nil {
				chainData = json.RawMessage("{}")
			}
			transferModel := &model.Transfer{
				Chain:            batch.Chain,
				Network:          batch.Network,
				TransactionID:    txID,
				TxHash:           ntx.TxHash,
				InstructionIndex: nt.InstructionIndex,
				TokenID:          tokenID,
				FromAddress:      nt.FromAddress,
				ToAddress:        nt.ToAddress,
				Amount:           nt.Amount,
				Direction:        direction,
				WatchedAddress:   &batch.Address,
				WalletID:         batch.WalletID,
				OrganizationID:   batch.OrgID,
				BlockCursor:      ntx.BlockCursor,
				BlockTime:        ntx.BlockTime,
				ChainData:        chainData,
			}
			if err := ing.transferRepo.UpsertTx(ctx, dbTx, transferModel); err != nil {
				return fmt.Errorf("upsert transfer: %w", err)
			}

			// 2d. Update balance
			if direction != nil {
				delta := nt.Amount
				if *direction == model.DirectionWithdrawal {
					delta = negateAmount(delta)
				}
				if err := ing.balanceRepo.AdjustBalanceTx(
					ctx, dbTx,
					batch.Chain, batch.Network, batch.Address,
					tokenID, batch.WalletID, batch.OrgID,
					delta, ntx.BlockCursor, ntx.TxHash,
				); err != nil {
					return fmt.Errorf("adjust balance: %w", err)
				}
			}

			totalTransfers++
		}

		// 3. Deduct fee from fee_payer if fee_payer is the watched address
		if ntx.FeePayer == batch.Address && ntx.FeeAmount != "0" && ntx.Status == model.TxStatusSuccess {
			// Ensure native token exists
			nativeToken := &model.Token{
				Chain:           batch.Chain,
				Network:         batch.Network,
				ContractAddress: model.SolanaNativeMint,
				Symbol:          "SOL",
				Name:            "Solana",
				Decimals:        9,
				TokenType:       model.TokenTypeNative,
				ChainData:       json.RawMessage("{}"),
			}
			nativeTokenID, err := ing.tokenRepo.UpsertTx(ctx, dbTx, nativeToken)
			if err != nil {
				return fmt.Errorf("upsert native token: %w", err)
			}

			feeDelta := negateAmount(ntx.FeeAmount)
			if err := ing.balanceRepo.AdjustBalanceTx(
				ctx, dbTx,
				batch.Chain, batch.Network, batch.Address,
				nativeTokenID, batch.WalletID, batch.OrgID,
				feeDelta, ntx.BlockCursor, ntx.TxHash,
			); err != nil {
				return fmt.Errorf("adjust fee balance: %w", err)
			}
		}
	}

	// 4. Update cursor
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

	// 5. Update watermark
	if err := ing.configRepo.UpdateWatermarkTx(
		ctx, dbTx,
		batch.Chain, batch.Network, batch.NewCursorSequence,
	); err != nil {
		return fmt.Errorf("update watermark: %w", err)
	}

	// 6. Commit
	if err := dbTx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	ing.logger.Info("batch ingested",
		"address", batch.Address,
		"txs", len(batch.Transactions),
		"transfers", totalTransfers,
		"cursor", batch.NewCursorValue,
	)

	return nil
}

func negateAmount(amount string) string {
	n := new(big.Int)
	n.SetString(amount, 10)
	n.Neg(n)
	return n.String()
}

func defaultTokenSymbol(nt event.NormalizedTransfer) string {
	if nt.TokenSymbol != "" {
		return nt.TokenSymbol
	}
	if nt.TokenType == model.TokenTypeNative {
		return "SOL"
	}
	return "UNKNOWN"
}

func defaultTokenName(nt event.NormalizedTransfer) string {
	if nt.TokenName != "" {
		return nt.TokenName
	}
	if nt.TokenType == model.TokenTypeNative {
		return "Solana"
	}
	return "Unknown Token"
}
