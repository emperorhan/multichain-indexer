package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kodax/koda-custody-indexer/internal/domain/model"
)

type WatchedAddressRepo struct {
	db *DB
}

func NewWatchedAddressRepo(db *DB) *WatchedAddressRepo {
	return &WatchedAddressRepo{db: db}
}

func (r *WatchedAddressRepo) GetActive(ctx context.Context, chain model.Chain, network model.Network) ([]model.WatchedAddress, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, chain, network, address, wallet_id, organization_id, label, is_active, source, created_at, updated_at
		FROM watched_addresses
		WHERE chain = $1 AND network = $2 AND is_active = true
		ORDER BY created_at
	`, chain, network)
	if err != nil {
		return nil, fmt.Errorf("query watched addresses: %w", err)
	}
	defer rows.Close()

	var addresses []model.WatchedAddress
	for rows.Next() {
		var a model.WatchedAddress
		if err := rows.Scan(
			&a.ID, &a.Chain, &a.Network, &a.Address,
			&a.WalletID, &a.OrganizationID, &a.Label,
			&a.IsActive, &a.Source, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan watched address: %w", err)
		}
		addresses = append(addresses, a)
	}
	return addresses, rows.Err()
}

func (r *WatchedAddressRepo) Upsert(ctx context.Context, addr *model.WatchedAddress) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO watched_addresses (chain, network, address, wallet_id, organization_id, label, is_active, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (chain, network, address) DO UPDATE SET
			is_active = EXCLUDED.is_active,
			updated_at = now()
	`, addr.Chain, addr.Network, addr.Address, addr.WalletID, addr.OrganizationID, addr.Label, addr.IsActive, addr.Source)
	if err != nil {
		return fmt.Errorf("upsert watched address: %w", err)
	}
	return nil
}

func (r *WatchedAddressRepo) FindByAddress(ctx context.Context, chain model.Chain, network model.Network, address string) (*model.WatchedAddress, error) {
	var a model.WatchedAddress
	err := r.db.QueryRowContext(ctx, `
		SELECT id, chain, network, address, wallet_id, organization_id, label, is_active, source, created_at, updated_at
		FROM watched_addresses
		WHERE chain = $1 AND network = $2 AND address = $3
	`, chain, network, address).Scan(
		&a.ID, &a.Chain, &a.Network, &a.Address,
		&a.WalletID, &a.OrganizationID, &a.Label,
		&a.IsActive, &a.Source, &a.CreatedAt, &a.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find watched address: %w", err)
	}
	return &a, nil
}
