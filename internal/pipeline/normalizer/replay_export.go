package normalizer

import (
	"encoding/json"
	"time"

	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
)

// ReplayNormalizeTxResult converts a sidecar TransactionResult into a
// NormalizedTransaction with BalanceEvents. This is the same logic as the
// live normalizer's normalizedTxFromResult(), extracted as a standalone
// function so the replay verifier can call it without a Normalizer instance.
func ReplayNormalizeTxResult(
	chain model.Chain,
	network model.Network,
	result *sidecarv1.TransactionResult,
	watchedAddrs map[string]struct{},
) event.NormalizedTransaction {
	txHash := identity.CanonicalSignatureIdentity(chain, result.TxHash)
	if txHash == "" {
		txHash = result.TxHash
	}
	finalityState := resolveResultFinalityState(chain, result)
	tx := event.NormalizedTransaction{
		TxHash:      txHash,
		BlockCursor: result.BlockCursor,
		FeeAmount:   result.FeeAmount,
		FeePayer:    result.FeePayer,
		Status:      model.TxStatus(result.Status),
		ChainData:   json.RawMessage(`{}`),
	}

	if result.BlockTime != 0 {
		bt := time.Unix(result.BlockTime, 0)
		tx.BlockTime = &bt
	}
	if result.Error != nil {
		tx.Err = result.Error
	}

	isBaseChain := identity.IsEVMChain(chain)
	isBTCChain := chain == model.ChainBTC
	if isBaseChain {
		tx.BalanceEvents = buildCanonicalBaseBalanceEventsMulti(
			chain, network, txHash, result.Status,
			result.FeePayer, result.FeeAmount, finalityState,
			result.BalanceEvents, watchedAddrs,
		)
	} else if isBTCChain {
		tx.BalanceEvents = buildCanonicalBTCBalanceEventsMulti(
			chain, network, txHash, result.Status,
			result.FeePayer, result.FeeAmount, finalityState,
			result.BalanceEvents, watchedAddrs,
		)
	} else {
		tx.BalanceEvents = buildCanonicalSolanaBalanceEventsMulti(
			chain, network, txHash, result.Status,
			result.FeePayer, result.FeeAmount, finalityState,
			result.BalanceEvents, watchedAddrs,
		)
	}

	return tx
}
