package normalizer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Normalizer receives RawBatches, calls sidecar gRPC for decoding,
// and produces NormalizedBatches.
type Normalizer struct {
	sidecarAddr    string
	sidecarTimeout time.Duration
	rawBatchCh     <-chan event.RawBatch
	normalizedCh   chan<- event.NormalizedBatch
	workerCount    int
	logger         *slog.Logger
}

func New(
	sidecarAddr string,
	sidecarTimeout time.Duration,
	rawBatchCh <-chan event.RawBatch,
	normalizedCh chan<- event.NormalizedBatch,
	workerCount int,
	logger *slog.Logger,
) *Normalizer {
	return &Normalizer{
		sidecarAddr:    sidecarAddr,
		sidecarTimeout: sidecarTimeout,
		rawBatchCh:     rawBatchCh,
		normalizedCh:   normalizedCh,
		workerCount:    workerCount,
		logger:         logger.With("component", "normalizer"),
	}
}

func (n *Normalizer) Run(ctx context.Context) error {
	n.logger.Info("normalizer started", "sidecar_addr", n.sidecarAddr, "workers", n.workerCount)

	conn, err := grpc.NewClient(
		n.sidecarAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("connect sidecar: %w", err)
	}
	defer conn.Close()

	client := sidecarv1.NewChainDecoderClient(conn)

	var wg sync.WaitGroup
	for i := 0; i < n.workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			n.worker(ctx, workerID, client)
		}(i)
	}

	wg.Wait()
	n.logger.Info("normalizer stopped")
	return ctx.Err()
}

func (n *Normalizer) worker(ctx context.Context, workerID int, client sidecarv1.ChainDecoderClient) {
	log := n.logger.With("worker", workerID)

	for {
		select {
		case <-ctx.Done():
			return
		case batch, ok := <-n.rawBatchCh:
			if !ok {
				return
			}
			if err := n.processBatch(ctx, log, client, batch); err != nil {
				log.Error("process batch failed",
					"address", batch.Address,
					"error", err,
				)
			}
		}
	}
}

func (n *Normalizer) processBatch(ctx context.Context, log *slog.Logger, client sidecarv1.ChainDecoderClient, batch event.RawBatch) error {
	// Build gRPC request
	rawTxs := make([]*sidecarv1.RawTransaction, len(batch.RawTransactions))
	for i, rawJSON := range batch.RawTransactions {
		rawTxs[i] = &sidecarv1.RawTransaction{
			Signature: batch.Signatures[i].Hash,
			RawJson:   rawJSON,
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, n.sidecarTimeout)
	defer cancel()

	resp, err := client.DecodeSolanaTransactionBatch(callCtx, &sidecarv1.DecodeSolanaTransactionBatchRequest{
		Transactions:     rawTxs,
		WatchedAddresses: []string{batch.Address},
	})
	if err != nil {
		return fmt.Errorf("sidecar decode: %w", err)
	}

	// Log decode errors
	for _, decErr := range resp.Errors {
		log.Warn("sidecar decode error", "signature", decErr.Signature, "error", decErr.Error)
	}

	// Convert to NormalizedBatch
	normalized := event.NormalizedBatch{
		Chain:             batch.Chain,
		Network:           batch.Network,
		Address:           batch.Address,
		WalletID:          batch.WalletID,
		OrgID:             batch.OrgID,
		NewCursorValue:    batch.NewCursorValue,
		NewCursorSequence: batch.NewCursorSequence,
	}

	for _, result := range resp.Results {
		tx := event.NormalizedTransaction{
			TxHash:      result.TxHash,
			BlockCursor: result.BlockCursor,
			FeeAmount:   result.FeeAmount,
			FeePayer:    result.FeePayer,
			Status:      model.TxStatus(result.Status),
			ChainData:   json.RawMessage("{}"),
		}

		if result.BlockTime != 0 {
			bt := time.Unix(result.BlockTime, 0)
			tx.BlockTime = &bt
		}
		if result.Error != nil {
			tx.Err = result.Error
		}

		for _, t := range result.Transfers {
			chainData, _ := json.Marshal(map[string]string{
				"from_ata":      t.FromAta,
				"to_ata":        t.ToAta,
				"transfer_type": t.TransferType,
			})

			transfer := event.NormalizedTransfer{
				InstructionIndex: int(t.InstructionIndex),
				ContractAddress:  t.ContractAddress,
				FromAddress:      t.FromAddress,
				ToAddress:        t.ToAddress,
				Amount:           t.Amount,
				TransferType:     t.TransferType,
				ChainData:        chainData,
				TokenSymbol:      t.TokenSymbol,
				TokenName:        t.TokenName,
				TokenDecimals:    int(t.TokenDecimals),
				TokenType:        model.TokenType(t.TokenType),
			}
			tx.Transfers = append(tx.Transfers, transfer)
		}

		normalized.Transactions = append(normalized.Transactions, tx)
	}

	select {
	case n.normalizedCh <- normalized:
		log.Info("normalized batch sent",
			"address", batch.Address,
			"tx_count", len(normalized.Transactions),
		)
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}
