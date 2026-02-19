package model

import (
	"time"

	"github.com/google/uuid"
)

// AddressBookStatus represents the status of an address book entry.
type AddressBookStatus string

const (
	AddressBookStatusActive   AddressBookStatus = "ACTIVE"
	AddressBookStatusInactive AddressBookStatus = "INACTIVE"
)

// AddressBook maps a known counterparty address to a human-readable name
// within an organization context.
type AddressBook struct {
	ID        uuid.UUID         `db:"id"`
	Chain     Chain             `db:"chain"`
	Network   Network           `db:"network"`
	OrgID     string            `db:"org_id"`
	Address   string            `db:"address"`
	Name      string            `db:"name"`
	Status    AddressBookStatus `db:"status"`
	CreatedAt time.Time         `db:"created_at"`
	UpdatedAt time.Time         `db:"updated_at"`
}
