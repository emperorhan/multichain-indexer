package normalizer

import (
	"crypto/sha256"
	"context"
	"encoding/hex"
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

		for _, be := range result.BalanceEvents {
			// Convert metadata map to JSON for chain_data
			chainData, _ := json.Marshal(be.Metadata)
			assetType := mapTokenTypeToAssetType(model.TokenType(be.TokenType))
			eventPath := balanceEventPath(be.OuterInstructionIndex, be.InnerInstructionIndex)
			eventID := buildCanonicalEventID(
				batch.Chain, batch.Network,
				result.TxHash, eventPath,
				be.Address, be.ContractAddress, model.EventCategory(be.EventCategory),
			)

			balanceEvent := event.NormalizedBalanceEvent{
				OuterInstructionIndex: int(be.OuterInstructionIndex),
				InnerInstructionIndex: int(be.InnerInstructionIndex),
				EventCategory:         model.EventCategory(be.EventCategory),
				EventAction:           be.EventAction,
				ProgramID:             be.ProgramId,
				ContractAddress:       be.ContractAddress,
				Address:               be.Address,
				CounterpartyAddress:   be.CounterpartyAddress,
				Delta:                 be.Delta,
				ChainData:             chainData,
				TokenSymbol:           be.TokenSymbol,
				TokenName:             be.TokenName,
				TokenDecimals:         int(be.TokenDecimals),
				TokenType:             model.TokenType(be.TokenType),
				EventID:               eventID,
				BlockHash:             "",
				TxIndex:               -1,
				EventPath:             eventPath,
				EventPathType:         "solana_instruction",
				ActorAddress:          be.Address,
				AssetType:             assetType,
				AssetID:               be.ContractAddress,
				FinalityState:         "finalized",
				DecoderVersion:        "solana-decoder-v1",
				SchemaVersion:         "v2",
			}
			tx.BalanceEvents = append(tx.BalanceEvents, balanceEvent)
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

func buildCanonicalEventID(chain model.Chain, network model.Network, txHash, eventPath, actorAddress, assetID string, category model.EventCategory) string {
	canonical := fmt.Sprintf("chain=%s|network=%s|tx=%s|path=%s|actor=%s|asset=%s|category=%s", chain, network, txHash, eventPath, actorAddress, assetID, category)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func mapTokenTypeToAssetType(tokenType model.TokenType) string {
	switch tokenType {
	case model.TokenTypeNative:
		return "native"
	case model.TokenTypeNFT:
		return "nft"
	case model.TokenTypeFungible:
		return "fungible_token"
	default:
		return "unknown"
	}
}

func balanceEventPath(outerInstructionIndex, innerInstructionIndex int32) string {
	return fmt.Sprintf("outer:%d|inner:%d", outerInstructionIndex, innerInstructionIndex)
}
