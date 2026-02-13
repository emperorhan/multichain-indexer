package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Transaction struct {
	ID          uuid.UUID       `db:"id"`
	Chain       Chain           `db:"chain"`
	Network     Network         `db:"network"`
	TxHash      string          `db:"tx_hash"`
	BlockCursor int64           `db:"block_cursor"`
	BlockTime   *time.Time      `db:"block_time"`
	FeeAmount   string          `db:"fee_amount"` // NUMERIC(78,0) as string
	FeePayer    string          `db:"fee_payer"`
	Status      TxStatus        `db:"status"`
	Err         *string         `db:"err"`
	ChainData   json.RawMessage `db:"chain_data"`
	CreatedAt   time.Time       `db:"created_at"`
}
