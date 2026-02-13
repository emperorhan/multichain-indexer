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

// SignatureInfo represents a transaction reference from the chain.
type SignatureInfo struct {
	Hash     string     // tx_hash (Solana: signature, EVM: hash)
	Sequence int64      // block_cursor (Solana: slot, EVM: block_number)
	Time     *time.Time // block time if available
}
