package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

type IndexedBlockRepo struct {
	db *sql.DB
}

func NewIndexedBlockRepo(db *sql.DB) *IndexedBlockRepo {
	return &IndexedBlockRepo{db: db}
}

func (r *IndexedBlockRepo) UpsertTx(ctx context.Context, tx *sql.Tx, block *model.IndexedBlock) error {
	const query = `
		INSERT INTO indexed_blocks (chain, network, block_number, block_hash, parent_hash, finality_state, block_time)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (chain, network, block_number)
		DO UPDATE SET block_hash = EXCLUDED.block_hash,
		              parent_hash = EXCLUDED.parent_hash,
		              finality_state = EXCLUDED.finality_state,
		              block_time = EXCLUDED.block_time,
		              updated_at = now()
	`
	_, err := tx.ExecContext(ctx, query,
		block.Chain, block.Network, block.BlockNumber,
		block.BlockHash, block.ParentHash, block.FinalityState, block.BlockTime,
	)
	if err != nil {
		return fmt.Errorf("upsert indexed block %d: %w", block.BlockNumber, err)
	}
	return nil
}

func (r *IndexedBlockRepo) BulkUpsertTx(ctx context.Context, tx *sql.Tx, blocks []*model.IndexedBlock) error {
	if len(blocks) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString(`
		INSERT INTO indexed_blocks (chain, network, block_number, block_hash, parent_hash, finality_state, block_time)
		VALUES `)

	args := make([]interface{}, 0, len(blocks)*7)
	for i, block := range blocks {
		if i > 0 {
			sb.WriteString(", ")
		}
		base := i * 7
		fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7)
		args = append(args,
			block.Chain, block.Network, block.BlockNumber,
			block.BlockHash, block.ParentHash, block.FinalityState, block.BlockTime,
		)
	}

	sb.WriteString(`
		ON CONFLICT (chain, network, block_number)
		DO UPDATE SET block_hash = EXCLUDED.block_hash,
		              parent_hash = EXCLUDED.parent_hash,
		              finality_state = EXCLUDED.finality_state,
		              block_time = EXCLUDED.block_time,
		              updated_at = now()
	`)

	_, err := tx.ExecContext(ctx, sb.String(), args...)
	if err != nil {
		return fmt.Errorf("bulk upsert indexed blocks: %w", err)
	}
	return nil
}

func (r *IndexedBlockRepo) GetUnfinalized(ctx context.Context, chain model.Chain, network model.Network) ([]model.IndexedBlock, error) {
	const query = `
		SELECT chain, network, block_number, block_hash, parent_hash, finality_state, block_time
		FROM indexed_blocks
		WHERE chain = $1 AND network = $2 AND finality_state NOT IN ('finalized')
		ORDER BY block_number
	`
	rows, err := r.db.QueryContext(ctx, query, chain, network)
	if err != nil {
		return nil, fmt.Errorf("query unfinalized blocks: %w", err)
	}
	defer rows.Close()

	var blocks []model.IndexedBlock
	for rows.Next() {
		var b model.IndexedBlock
		if err := rows.Scan(&b.Chain, &b.Network, &b.BlockNumber, &b.BlockHash, &b.ParentHash, &b.FinalityState, &b.BlockTime); err != nil {
			return nil, fmt.Errorf("scan indexed block: %w", err)
		}
		blocks = append(blocks, b)
	}
	return blocks, rows.Err()
}

func (r *IndexedBlockRepo) GetByBlockNumber(ctx context.Context, chain model.Chain, network model.Network, blockNumber int64) (*model.IndexedBlock, error) {
	const query = `
		SELECT chain, network, block_number, block_hash, parent_hash, finality_state, block_time
		FROM indexed_blocks
		WHERE chain = $1 AND network = $2 AND block_number = $3
	`
	var b model.IndexedBlock
	err := r.db.QueryRowContext(ctx, query, chain, network, blockNumber).Scan(
		&b.Chain, &b.Network, &b.BlockNumber, &b.BlockHash, &b.ParentHash, &b.FinalityState, &b.BlockTime,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get indexed block %d: %w", blockNumber, err)
	}
	return &b, nil
}

func (r *IndexedBlockRepo) UpdateFinalityTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, upToBlock int64, newState string) error {
	const query = `
		UPDATE indexed_blocks
		SET finality_state = $4, updated_at = now()
		WHERE chain = $1 AND network = $2 AND block_number <= $3 AND finality_state != $4
	`
	_, err := tx.ExecContext(ctx, query, chain, network, upToBlock, newState)
	if err != nil {
		return fmt.Errorf("update finality state: %w", err)
	}
	return nil
}

func (r *IndexedBlockRepo) DeleteFromBlockTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, fromBlock int64) error {
	const query = `
		DELETE FROM indexed_blocks
		WHERE chain = $1 AND network = $2 AND block_number >= $3
	`
	_, err := tx.ExecContext(ctx, query, chain, network, fromBlock)
	if err != nil {
		return fmt.Errorf("delete indexed blocks from %d: %w", fromBlock, err)
	}
	return nil
}

func (r *IndexedBlockRepo) PurgeFinalizedBefore(ctx context.Context, chain model.Chain, network model.Network, beforeBlock int64) (int64, error) {
	const query = `
		DELETE FROM indexed_blocks
		WHERE chain = $1 AND network = $2
		  AND finality_state = 'finalized'
		  AND block_number < $3
	`
	result, err := r.db.ExecContext(ctx, query, chain, network, beforeBlock)
	if err != nil {
		return 0, fmt.Errorf("purge finalized blocks before %d: %w", beforeBlock, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge finalized rows affected: %w", err)
	}
	return n, nil
}
