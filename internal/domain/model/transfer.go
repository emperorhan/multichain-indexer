package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Transfer struct {
	ID               uuid.UUID        `db:"id"`
	Chain            Chain            `db:"chain"`
	Network          Network          `db:"network"`
	TransactionID    uuid.UUID        `db:"transaction_id"`
	TxHash           string           `db:"tx_hash"`
	InstructionIndex int              `db:"instruction_index"`
	TokenID          uuid.UUID        `db:"token_id"`
	FromAddress      string           `db:"from_address"`
	ToAddress        string           `db:"to_address"`
	Amount           string           `db:"amount"` // NUMERIC(78,0) as string
	Direction        *TransferDirection `db:"direction"`
	WatchedAddress   *string          `db:"watched_address"`
	WalletID         *string          `db:"wallet_id"`
	OrganizationID   *string          `db:"organization_id"`
	BlockCursor      int64            `db:"block_cursor"`
	BlockTime        *time.Time       `db:"block_time"`
	ChainData        json.RawMessage  `db:"chain_data"`
	CreatedAt        time.Time        `db:"created_at"`
}
