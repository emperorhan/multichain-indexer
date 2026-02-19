package postgres

import (
	"context"
	"fmt"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

type RuntimeConfigRepo struct {
	db *DB
}

func NewRuntimeConfigRepo(db *DB) *RuntimeConfigRepo {
	return &RuntimeConfigRepo{db: db}
}

func (r *RuntimeConfigRepo) GetActive(ctx context.Context, chain model.Chain, network model.Network) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT config_key, config_value
		FROM runtime_configs
		WHERE chain = $1 AND network = $2 AND is_active = true
	`, chain, network)
	if err != nil {
		return nil, fmt.Errorf("get active runtime configs: %w", err)
	}
	defer rows.Close()

	configs := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan runtime config: %w", err)
		}
		configs[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read runtime config rows: %w", err)
	}

	return configs, nil
}
