package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

// DashboardRepo implements store.DashboardRepository using PostgreSQL.
type DashboardRepo struct {
	db *sql.DB
}

// NewDashboardRepo creates a new dashboard repository.
func NewDashboardRepo(db *sql.DB) *DashboardRepo {
	return &DashboardRepo{db: db}
}

// GetBalanceSummary returns watched addresses with their token balances for a chain/network.
func (r *DashboardRepo) GetBalanceSummary(ctx context.Context, chain model.Chain, network model.Network) ([]store.DashboardAddressBalance, error) {
	ctx, cancel := withTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	rows, err := r.db.QueryContext(ctx, `
		SELECT
			wa.address,
			wa.label,
			wa.wallet_id,
			COALESCE(t.symbol, '') AS token_symbol,
			COALESCE(t.name, '')   AS token_name,
			COALESCE(t.decimals, 0) AS decimals,
			COALESCE(b.amount, '0') AS amount,
			COALESCE(b.balance_type, '') AS balance_type,
			COALESCE(b.updated_at, wa.created_at) AS updated_at
		FROM watched_addresses wa
		LEFT JOIN balances b
			ON b.chain = wa.chain AND b.network = wa.network AND b.address = wa.address
		LEFT JOIN tokens t
			ON t.id = b.token_id
		WHERE wa.chain = $1 AND wa.network = $2 AND wa.is_active = true
		ORDER BY wa.address, t.symbol
	`, string(chain), string(network))
	if err != nil {
		return nil, fmt.Errorf("query balance summary: %w", err)
	}
	defer rows.Close()

	addrMap := make(map[string]*store.DashboardAddressBalance)
	var order []string

	for rows.Next() {
		var (
			address     string
			label       *string
			walletID    *string
			tokenSymbol string
			tokenName   string
			decimals    int
			amount      string
			balanceType string
			updatedAt   sql.NullTime
		)
		if err := rows.Scan(&address, &label, &walletID, &tokenSymbol, &tokenName, &decimals, &amount, &balanceType, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan balance summary row: %w", err)
		}

		ab, exists := addrMap[address]
		if !exists {
			ab = &store.DashboardAddressBalance{
				Address:  address,
				Label:    label,
				WalletID: walletID,
			}
			addrMap[address] = ab
			order = append(order, address)
		}

		if tokenSymbol != "" {
			tb := store.DashboardTokenBalance{
				TokenSymbol: tokenSymbol,
				TokenName:   tokenName,
				Decimals:    decimals,
				Amount:      amount,
				BalanceType: balanceType,
			}
			if updatedAt.Valid {
				tb.UpdatedAt = updatedAt.Time
			}
			ab.Balances = append(ab.Balances, tb)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate balance summary rows: %w", err)
	}

	result := make([]store.DashboardAddressBalance, 0, len(order))
	for _, addr := range order {
		ab := addrMap[addr]
		if ab.Balances == nil {
			ab.Balances = []store.DashboardTokenBalance{}
		}
		result = append(result, *ab)
	}
	return result, nil
}

// GetRecentEvents returns recent balance events for a chain/network, optionally filtered by address.
func (r *DashboardRepo) GetRecentEvents(ctx context.Context, chain model.Chain, network model.Network, address string, limit, offset int) ([]store.DashboardEvent, int, error) {
	ctx, cancel := withTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	baseWhere := `be.chain = $1 AND be.network = $2`
	args := []any{string(chain), string(network)}
	paramIdx := 3

	if address != "" {
		baseWhere += fmt.Sprintf(` AND be.address = $%d`, paramIdx)
		args = append(args, address)
		paramIdx++
	}

	// Count query
	var total int
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM balance_events be WHERE %s`, baseWhere)
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count events: %w", err)
	}

	// Data query
	dataQuery := fmt.Sprintf(`
		SELECT
			be.tx_hash,
			be.address,
			COALESCE(be.counterparty_address, '') AS counterparty_address,
			be.activity_type,
			be.delta,
			COALESCE(t.symbol, '') AS token_symbol,
			COALESCE(t.decimals, 0) AS decimals,
			be.block_cursor,
			be.block_time,
			COALESCE(be.finality_state, '') AS finality_state,
			be.created_at
		FROM balance_events be
		LEFT JOIN tokens t ON t.id = be.token_id
		WHERE %s
		ORDER BY be.block_cursor DESC, be.created_at DESC
		LIMIT $%d OFFSET $%d
	`, baseWhere, paramIdx, paramIdx+1)

	dataArgs := append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []store.DashboardEvent
	for rows.Next() {
		var e store.DashboardEvent
		if err := rows.Scan(
			&e.TxHash, &e.Address, &e.CounterpartyAddress,
			&e.ActivityType, &e.Delta, &e.TokenSymbol, &e.Decimals,
			&e.BlockCursor, &e.BlockTime, &e.FinalityState, &e.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan event row: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate event rows: %w", err)
	}
	if events == nil {
		events = []store.DashboardEvent{}
	}
	return events, total, nil
}

// GetAllWatermarks returns all pipeline watermarks.
func (r *DashboardRepo) GetAllWatermarks(ctx context.Context) ([]model.PipelineWatermark, error) {
	ctx, cancel := withTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, chain, network, head_sequence, ingested_sequence, last_heartbeat_at, created_at, updated_at
		FROM pipeline_watermarks
		ORDER BY chain, network
	`)
	if err != nil {
		return nil, fmt.Errorf("query watermarks: %w", err)
	}
	defer rows.Close()

	var watermarks []model.PipelineWatermark
	for rows.Next() {
		var w model.PipelineWatermark
		if err := rows.Scan(&w.ID, &w.Chain, &w.Network, &w.HeadSequence, &w.IngestedSequence, &w.LastHeartbeatAt, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan watermark row: %w", err)
		}
		watermarks = append(watermarks, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate watermark rows: %w", err)
	}
	if watermarks == nil {
		watermarks = []model.PipelineWatermark{}
	}
	return watermarks, nil
}

// CountWatchedAddresses returns the number of active watched addresses.
func (r *DashboardRepo) CountWatchedAddresses(ctx context.Context) (int, error) {
	ctx, cancel := withTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM watched_addresses WHERE is_active = true`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count watched addresses: %w", err)
	}
	return count, nil
}
