package model

import (
	"time"

	"github.com/google/uuid"
)

type WatchedAddress struct {
	ID             uuid.UUID     `db:"id"`
	Chain          Chain         `db:"chain"`
	Network        Network       `db:"network"`
	Address        string        `db:"address"`
	WalletID       *string       `db:"wallet_id"`
	OrganizationID *string       `db:"organization_id"`
	Label          *string       `db:"label"`
	IsActive       bool          `db:"is_active"`
	Source         AddressSource `db:"source"`
	CreatedAt      time.Time     `db:"created_at"`
	UpdatedAt      time.Time     `db:"updated_at"`
}
