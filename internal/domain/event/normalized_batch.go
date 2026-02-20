package event

import (
	"encoding/json"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// NormalizedBatch contains decoded/normalized transaction data ready for DB ingestion.
type NormalizedBatch struct {
	Chain                  model.Chain
	Network                model.Network
	Address                string
	WalletID               *string
	OrgID                  *string
	PreviousCursorValue    *string
	PreviousCursorSequence int64
	Transactions           []NormalizedTransaction
	NewCursorValue         *string
	NewCursorSequence      int64
	BlockScanMode          bool // true when produced by block-scan path
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
	BlockHash     string
	ParentHash    string
	BalanceEvents []NormalizedBalanceEvent
}

type NormalizedBalanceEvent struct {
	OuterInstructionIndex int
	InnerInstructionIndex int
	ActivityType          model.ActivityType
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

	EventID        string `json:"event_id" db:"event_id"`
	BlockHash      string `json:"block_hash" db:"block_hash"`
	TxIndex        int64  `json:"tx_index" db:"tx_index"`
	EventPath      string `json:"event_path" db:"event_path"`
	EventPathType  string `json:"event_path_type" db:"event_path_type"`
	ActorAddress   string `json:"actor_address" db:"actor_address"`
	AssetType      string `json:"asset_type" db:"asset_type"`
	AssetID        string `json:"asset_id" db:"asset_id"`
	FinalityState  string `json:"finality_state" db:"finality_state"`
	DecoderVersion string `json:"decoder_version" db:"decoder_version"`
	SchemaVersion  string `json:"schema_version" db:"schema_version"`
}
