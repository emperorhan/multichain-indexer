package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type EventCategory string

const (
	EventCategoryTransfer       EventCategory = "TRANSFER"
	EventCategoryStake          EventCategory = "STAKE"
	EventCategorySwap           EventCategory = "SWAP"
	EventCategoryMint           EventCategory = "MINT"
	EventCategoryBurn           EventCategory = "BURN"
	EventCategoryReward         EventCategory = "REWARD"
	EventCategoryFee            EventCategory = "FEE"
	EventCategoryFeeExecutionL2 EventCategory = "fee_execution_l2"
	EventCategoryFeeDataL1      EventCategory = "fee_data_l1"
)

type BalanceEvent struct {
	ID                    uuid.UUID       `db:"id"`
	Chain                 Chain           `db:"chain"`
	Network               Network         `db:"network"`
	TransactionID         uuid.UUID       `db:"transaction_id"`
	TxHash                string          `db:"tx_hash"`
	OuterInstructionIndex int             `db:"outer_instruction_index"`
	InnerInstructionIndex int             `db:"inner_instruction_index"`
	TokenID               uuid.UUID       `db:"token_id"`
	EventCategory         EventCategory   `db:"event_category"`
	EventAction           string          `db:"event_action"`
	ProgramID             string          `db:"program_id"`
	Address               string          `db:"address"`
	CounterpartyAddress   string          `db:"counterparty_address"`
	Delta                 string          `db:"delta"`
	BalanceBefore         *string         `json:"balance_before" db:"balance_before"`
	BalanceAfter          *string         `json:"balance_after" db:"balance_after"`
	WatchedAddress        *string         `db:"watched_address"`
	WalletID              *string         `db:"wallet_id"`
	OrganizationID        *string         `db:"organization_id"`
	BlockCursor           int64           `db:"block_cursor"`
	BlockTime             *time.Time      `db:"block_time"`
	ChainData             json.RawMessage `db:"chain_data"`
	EventID               string          `json:"event_id" db:"event_id"`
	BlockHash             string          `json:"block_hash" db:"block_hash"`
	TxIndex               int64           `json:"tx_index" db:"tx_index"`
	EventPath             string          `json:"event_path" db:"event_path"`
	EventPathType         string          `json:"event_path_type" db:"event_path_type"`
	ActorAddress          string          `json:"actor_address" db:"actor_address"`
	AssetType             string          `json:"asset_type" db:"asset_type"`
	AssetID               string          `json:"asset_id" db:"asset_id"`
	FinalityState         string          `json:"finality_state" db:"finality_state"`
	DecoderVersion        string          `json:"decoder_version" db:"decoder_version"`
	SchemaVersion         string          `json:"schema_version" db:"schema_version"`
	CreatedAt             time.Time       `db:"created_at"`
}
