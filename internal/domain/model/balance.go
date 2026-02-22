package model

import (
	"time"

	"github.com/google/uuid"
)

type Balance struct {
	ID                      uuid.UUID `db:"id"`
	Chain                   Chain     `db:"chain"`
	Network                 Network   `db:"network"`
	Address                 string    `db:"address"`
	TokenID                 uuid.UUID `db:"token_id"`
	BalanceType             string    `db:"balance_type"`
	WalletID                *string   `db:"wallet_id"`
	OrganizationID          *string   `db:"organization_id"`
	Amount            string `db:"amount"`
	LastUpdatedCursor int64  `db:"last_updated_cursor"`
	LastUpdatedTxHash       *string   `db:"last_updated_tx_hash"`
	CreatedAt               time.Time `db:"created_at"`
	UpdatedAt               time.Time `db:"updated_at"`
}
