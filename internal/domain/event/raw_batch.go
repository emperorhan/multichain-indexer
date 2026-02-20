package event

import (
	"encoding/json"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// RawBatch contains raw transaction data fetched from the chain.
type RawBatch struct {
	Chain                  model.Chain
	Network                model.Network
	Address                string
	WalletID               *string
	OrgID                  *string
	PreviousCursorValue    *string
	PreviousCursorSequence int64
	RawTransactions        []json.RawMessage // raw JSON from RPC
	Signatures             []SignatureInfo
	NewCursorValue         *string // newest signature in this batch
	NewCursorSequence      int64   // newest slot/block in this batch
	BlockScanMode          bool    // true when produced by block-scan path
	WatchedAddresses       []string // populated in block-scan mode
}

type SignatureInfo struct {
	Hash     string
	Sequence int64      // slot or block number
	Time     *time.Time // block time if available (used as fallback when sidecar omits it)
}
