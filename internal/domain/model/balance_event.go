package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type EventCategory string

const (
	EventCategoryTransfer EventCategory = "TRANSFER"
	EventCategoryStake    EventCategory = "STAKE"
	EventCategorySwap     EventCategory = "SWAP"
	EventCategoryMint     EventCategory = "MINT"
	EventCategoryBurn     EventCategory = "BURN"
	EventCategoryReward   EventCategory = "REWARD"
	EventCategoryFee      EventCategory = "FEE"
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
	WatchedAddress        *string         `db:"watched_address"`
	WalletID              *string         `db:"wallet_id"`
	OrganizationID        *string         `db:"organization_id"`
	BlockCursor           int64           `db:"block_cursor"`
	BlockTime             *time.Time      `db:"block_time"`
	ChainData             json.RawMessage `db:"chain_data"`
	CreatedAt             time.Time       `db:"created_at"`
}
