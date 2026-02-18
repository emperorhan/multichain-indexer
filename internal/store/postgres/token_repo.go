package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
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
		SELECT id, chain, network, contract_address, symbol, name, decimals, token_type,
		       is_denied, denied_reason, denied_at, scam_score, scam_signals, denied_source,
		       chain_data, created_at, updated_at
		FROM tokens
		WHERE chain = $1 AND network = $2 AND contract_address = $3
	`, chain, network, contractAddress).Scan(
		&t.ID, &t.Chain, &t.Network, &t.ContractAddress,
		&t.Symbol, &t.Name, &t.Decimals, &t.TokenType,
		&t.IsDenied, &t.DeniedReason, &t.DeniedAt, &t.ScamScore, &t.ScamSignals, &t.DeniedSource,
		&t.ChainData, &t.CreatedAt, &t.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find token: %w", err)
	}
	return &t, nil
}

// IsDeniedTx checks if a token is denied within a transaction.
func (r *TokenRepo) IsDeniedTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddress string) (bool, error) {
	var denied bool
	err := tx.QueryRowContext(ctx, `
		SELECT is_denied FROM tokens
		WHERE chain = $1 AND network = $2 AND contract_address = $3
	`, chain, network, contractAddress).Scan(&denied)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("check token denied: %w", err)
	}
	return denied, nil
}

// DenyTokenTx marks a token as denied with reason, source, score, and signals.
// Also inserts an audit log entry in token_deny_log.
func (r *TokenRepo) DenyTokenTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddress string, reason string, source string, score int16, signals []string) error {
	signalsJSON, err := json.Marshal(signals)
	if err != nil {
		return fmt.Errorf("marshal scam signals: %w", err)
	}
	now := time.Now()

	var tokenID uuid.UUID
	err = tx.QueryRowContext(ctx, `
		UPDATE tokens SET
			is_denied = true,
			denied_reason = $4,
			denied_at = $5,
			scam_score = $6,
			scam_signals = $7,
			denied_source = $8,
			updated_at = now()
		WHERE chain = $1 AND network = $2 AND contract_address = $3
		RETURNING id
	`, chain, network, contractAddress, reason, now, score, signalsJSON, source).Scan(&tokenID)
	if err != nil {
		return fmt.Errorf("deny token: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO token_deny_log (token_id, chain, network, contract_address, action, reason, source, metadata)
		VALUES ($1, $2, $3, $4, 'deny', $5, $6, $7)
	`, tokenID, chain, network, contractAddress, reason, source, signalsJSON)
	if err != nil {
		return fmt.Errorf("insert deny log: %w", err)
	}

	return nil
}

// AllowTokenTx removes the denied flag from a token and inserts an audit log entry.
func (r *TokenRepo) AllowTokenTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddress string, reason string) error {
	var tokenID uuid.UUID
	err := tx.QueryRowContext(ctx, `
		UPDATE tokens SET
			is_denied = false,
			denied_reason = '',
			denied_at = NULL,
			scam_score = 0,
			scam_signals = '[]',
			denied_source = '',
			updated_at = now()
		WHERE chain = $1 AND network = $2 AND contract_address = $3
		RETURNING id
	`, chain, network, contractAddress).Scan(&tokenID)
	if err != nil {
		return fmt.Errorf("allow token: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO token_deny_log (token_id, chain, network, contract_address, action, reason, source)
		VALUES ($1, $2, $3, $4, 'allow', $5, 'manual')
	`, tokenID, chain, network, contractAddress, reason)
	if err != nil {
		return fmt.Errorf("insert allow log: %w", err)
	}

	return nil
}
