package event

import (
	"encoding/json"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// NormalizedBatch contains decoded/normalized transaction data ready for DB ingestion.
type NormalizedBatch struct {
	Chain             model.Chain
	Network           model.Network
	Address           string
	WalletID          *string
	OrgID             *string
	Transactions      []NormalizedTransaction
	NewCursorValue    *string
	NewCursorSequence int64
}

type NormalizedTransaction struct {
	TxHash      string
	BlockCursor int64
	BlockTime   *time.Time
	FeeAmount   string
	FeePayer    string
	Status      model.TxStatus
	Err         *string
	ChainData   json.RawMessage
	Transfers   []NormalizedTransfer
}

type NormalizedTransfer struct {
	InstructionIndex int
	ContractAddress  string // mint address or contract address
	FromAddress      string
	ToAddress        string
	Amount           string
	TransferType     string // "transfer", "transferChecked", etc.
	ChainData        json.RawMessage
	TokenSymbol      string
	TokenName        string
	TokenDecimals    int
	TokenType        model.TokenType
}
