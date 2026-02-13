package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/kodax/koda-custody-indexer/internal/domain/model"
)

type TokenRepo struct {
	db *DB
}

func NewTokenRepo(db *DB) *TokenRepo {
	return &TokenRepo{db: db}
}

// UpsertTx inserts a token, returning the ID (existing or new).
func (r *TokenRepo) UpsertTx(ctx context.Context, tx *sql.Tx, t *model.Token) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRowContext(ctx, `
		INSERT INTO tokens (chain, network, contract_address, symbol, name, decimals, token_type, chain_data)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (chain, network, contract_address) DO UPDATE SET
			chain = tokens.chain
		RETURNING id
	`, t.Chain, t.Network, t.ContractAddress, t.Symbol, t.Name, t.Decimals, t.TokenType, t.ChainData,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert token: %w", err)
	}
	return id, nil
}

func (r *TokenRepo) FindByContractAddress(ctx context.Context, chain model.Chain, network model.Network, contractAddress string) (*model.Token, error) {
	var t model.Token
	err := r.db.QueryRowContext(ctx, `
		SELECT id, chain, network, contract_address, symbol, name, decimals, token_type, is_denied, chain_data, created_at, updated_at
		FROM tokens
		WHERE chain = $1 AND network = $2 AND contract_address = $3
	`, chain, network, contractAddress).Scan(
		&t.ID, &t.Chain, &t.Network, &t.ContractAddress,
		&t.Symbol, &t.Name, &t.Decimals, &t.TokenType,
		&t.IsDenied, &t.ChainData, &t.CreatedAt, &t.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find token: %w", err)
	}
	return &t, nil
}
