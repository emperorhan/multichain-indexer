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
	TxHash        string
	BlockCursor   int64
	BlockTime     *time.Time
	FeeAmount     string
	FeePayer      string
	Status        model.TxStatus
	Err           *string
	ChainData     json.RawMessage
	BalanceEvents []NormalizedBalanceEvent
}

type NormalizedBalanceEvent struct {
	OuterInstructionIndex int
	InnerInstructionIndex int
	EventCategory         model.EventCategory
	EventAction           string
	ProgramID             string
	ContractAddress       string // mint address or contract address
	Address               string // account whose balance changed
	CounterpartyAddress   string
	Delta                 string // SIGNED: positive = inflow, negative = outflow
	ChainData             json.RawMessage
	TokenSymbol           string
	TokenName             string
	TokenDecimals         int
	TokenType             model.TokenType
}
