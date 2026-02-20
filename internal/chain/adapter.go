package chain

import (
	"context"
	"encoding/json"
	"time"
)

// ChainAdapter abstracts chain-specific logic so the pipeline core operates chain-agnostically.
type ChainAdapter interface {
	// Chain returns the chain identifier (e.g., "solana", "ethereum").
	Chain() string

	// GetHeadSequence returns the latest block/slot on chain.
	GetHeadSequence(ctx context.Context) (int64, error)

	// FetchNewSignatures fetches new tx signatures for an address since the cursor.
	// Returns signatures in oldest-first order.
	FetchNewSignatures(ctx context.Context, address string, cursor *string, batchSize int) ([]SignatureInfo, error)

	// FetchTransactions fetches raw transaction data for given signatures/hashes.
	FetchTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error)
}

// CutoffAwareChainAdapter extends ChainAdapter with deterministic closed-range fetch support.
// cutoffSeq is an inclusive upper bound for signature/block sequence selection.
type CutoffAwareChainAdapter interface {
	ChainAdapter
	FetchNewSignaturesWithCutoff(ctx context.Context, address string, cursor *string, batchSize int, cutoffSeq int64) ([]SignatureInfo, error)
}

// ReorgAwareAdapter extends ChainAdapter with block hash verification
// and finality tracking for chains that support reorg detection.
type ReorgAwareAdapter interface {
	ChainAdapter
	// GetBlockHash returns hash and parent hash for a given block number.
	GetBlockHash(ctx context.Context, blockNumber int64) (hash, parentHash string, err error)
	// GetFinalizedBlockNumber returns the latest finalized block number.
	GetFinalizedBlockNumber(ctx context.Context) (int64, error)
}

// BlockScanAdapter extends ChainAdapter with block-range scanning.
// EVM and BTC adapters implement this to allow a single scan per block range
// instead of per-address cursor-based fetching.
type BlockScanAdapter interface {
	ChainAdapter
	ScanBlocks(ctx context.Context, startBlock, endBlock int64, watchedAddresses []string) ([]SignatureInfo, error)
}

// BalanceQueryAdapter extends ChainAdapter with on-chain balance queries
// for reconciliation purposes.
type BalanceQueryAdapter interface {
	ChainAdapter
	// GetBalance returns the on-chain balance for the given address and token.
	// tokenContract is empty string for native token.
	GetBalance(ctx context.Context, address string, tokenContract string) (string, error)
}

// SignatureInfo represents a transaction reference from the chain.
type SignatureInfo struct {
	Hash     string     // tx_hash (Solana: signature, EVM: hash)
	Sequence int64      // block_cursor (Solana: slot, EVM: block_number)
	Time     *time.Time // block time if available
}
