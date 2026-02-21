package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/google/uuid"
	"github.com/lib/pq"
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

// FindByID returns the token with the given ID.
func (r *TokenRepo) FindByID(ctx context.Context, id uuid.UUID) (*model.Token, error) {
	ctx, cancel := withTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	var t model.Token
	err := r.db.QueryRowContext(ctx, `
		SELECT id, chain, network, contract_address, symbol, name, decimals, token_type,
		       is_denied, denied_reason, denied_at, scam_score, scam_signals, denied_source,
		       chain_data, created_at, updated_at
		FROM tokens
		WHERE id = $1
	`, id).Scan(
		&t.ID, &t.Chain, &t.Network, &t.ContractAddress,
		&t.Symbol, &t.Name, &t.Decimals, &t.TokenType,
		&t.IsDenied, &t.DeniedReason, &t.DeniedAt, &t.ScamScore, &t.ScamSignals, &t.DeniedSource,
		&t.ChainData, &t.CreatedAt, &t.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find token by id: %w", err)
	}
	return &t, nil
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

// BulkUpsertTx inserts multiple tokens in a single multi-VALUES INSERT...ON CONFLICT RETURNING id.
// Returns a map of contractAddress -> uuid.UUID for all upserted tokens.
func (r *TokenRepo) BulkUpsertTx(ctx context.Context, tx *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error) {
	if len(tokens) == 0 {
		return make(map[string]uuid.UUID), nil
	}

	const cols = 8 // number of columns per row
	args := make([]interface{}, 0, len(tokens)*cols)
	valuesClauses := make([]string, 0, len(tokens))

	for i, t := range tokens {
		base := i * cols
		valuesClauses = append(valuesClauses, fmt.Sprintf(
			"($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			base+1, base+2, base+3, base+4, base+5,
			base+6, base+7, base+8,
		))
		args = append(args,
			t.Chain, t.Network, t.ContractAddress, t.Symbol,
			t.Name, t.Decimals, t.TokenType, t.ChainData,
		)
	}

	query := fmt.Sprintf(`
		INSERT INTO tokens (chain, network, contract_address, symbol, name, decimals, token_type, chain_data)
		VALUES %s
		ON CONFLICT (chain, network, contract_address) DO UPDATE SET
			chain = tokens.chain
		RETURNING contract_address, id
	`, strings.Join(valuesClauses, ", "))

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("bulk upsert tokens: %w", err)
	}
	defer rows.Close()

	result := make(map[string]uuid.UUID, len(tokens))
	for rows.Next() {
		var contractAddr string
		var id uuid.UUID
		if err := rows.Scan(&contractAddr, &id); err != nil {
			return nil, fmt.Errorf("bulk upsert tokens scan: %w", err)
		}
		result[contractAddr] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bulk upsert tokens rows: %w", err)
	}

	return result, nil
}

// BulkIsDeniedTx checks if multiple tokens are denied in a single query.
// Returns a map of contractAddress -> isDenied for all found tokens.
// Tokens not found in the DB are not included in the result map.
func (r *TokenRepo) BulkIsDeniedTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddresses []string) (map[string]bool, error) {
	if len(contractAddresses) == 0 {
		return make(map[string]bool), nil
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT contract_address, is_denied FROM tokens
		WHERE chain = $1 AND network = $2 AND contract_address = ANY($3)
	`, chain, network, pq.Array(contractAddresses))
	if err != nil {
		return nil, fmt.Errorf("bulk check tokens denied: %w", err)
	}
	defer rows.Close()

	result := make(map[string]bool, len(contractAddresses))
	for rows.Next() {
		var addr string
		var denied bool
		if err := rows.Scan(&addr, &denied); err != nil {
			return nil, fmt.Errorf("bulk check tokens denied scan: %w", err)
		}
		result[addr] = denied
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bulk check tokens denied rows: %w", err)
	}

	return result, nil
}
