package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type TokenDenyLog struct {
	ID              uuid.UUID       `db:"id"`
	TokenID         uuid.UUID       `db:"token_id"`
	Chain           Chain           `db:"chain"`
	Network         Network         `db:"network"`
	ContractAddress string          `db:"contract_address"`
	Action          string          `db:"action"` // "deny" or "allow"
	Reason          string          `db:"reason"`
	Source          string          `db:"source"`
	Metadata        json.RawMessage `db:"metadata"`
	CreatedAt       time.Time       `db:"created_at"`
}
