package postgres

import (
	"context"
	"database/sql"

	"github.com/emperorhan/multichain-indexer/internal/admin"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// AddressBookRepo implements admin.AddressBookRepo using PostgreSQL.
type AddressBookRepo struct {
	db *sql.DB
}

// NewAddressBookRepo creates a new address book repository.
func NewAddressBookRepo(db *sql.DB) *AddressBookRepo {
	return &AddressBookRepo{db: db}
}

// List returns all address book entries for the given chain/network.
func (r *AddressBookRepo) List(ctx context.Context, chain model.Chain, network model.Network) ([]admin.AddressBookEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT chain, network, org_id, address, name, status
		 FROM address_books
		 WHERE chain = $1 AND network = $2
		 ORDER BY name`,
		string(chain), string(network))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []admin.AddressBookEntry
	for rows.Next() {
		var e admin.AddressBookEntry
		if err := rows.Scan(&e.Chain, &e.Network, &e.OrgID, &e.Address, &e.Name, &e.Status); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Upsert inserts or updates an address book entry.
func (r *AddressBookRepo) Upsert(ctx context.Context, entry *admin.AddressBookEntry) error {
	status := entry.Status
	if status == "" {
		status = string(model.AddressBookStatusActive)
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO address_books (chain, network, org_id, address, name, status)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (chain, network, org_id, address)
		 DO UPDATE SET name = EXCLUDED.name, status = EXCLUDED.status, updated_at = NOW()`,
		string(entry.Chain), string(entry.Network), entry.OrgID, entry.Address, entry.Name, status)
	return err
}

// Delete removes an address book entry.
func (r *AddressBookRepo) Delete(ctx context.Context, chain model.Chain, network model.Network, address string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM address_books WHERE chain = $1 AND network = $2 AND address = $3`,
		string(chain), string(network), address)
	return err
}

// FindByAddress looks up an address book entry by address.
func (r *AddressBookRepo) FindByAddress(ctx context.Context, chain model.Chain, network model.Network, address string) (*admin.AddressBookEntry, error) {
	var e admin.AddressBookEntry
	err := r.db.QueryRowContext(ctx,
		`SELECT chain, network, org_id, address, name, status
		 FROM address_books
		 WHERE chain = $1 AND network = $2 AND address = $3 AND status = 'ACTIVE'
		 LIMIT 1`,
		string(chain), string(network), address).
		Scan(&e.Chain, &e.Network, &e.OrgID, &e.Address, &e.Name, &e.Status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}
