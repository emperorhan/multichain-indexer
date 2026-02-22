package replay

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/store"
	"github.com/google/uuid"
)

// defaultQueryTimeout mirrors the postgres package's DefaultQueryTimeout
// for read-only count queries executed outside a managed transaction.
const defaultQueryTimeout = 30 * time.Second

// Service provides shared purge/rollback logic used by both the Admin API
// (replay) and the Ingester (reorg handling).
type Service struct {
	db          store.TxBeginner
	balanceRepo store.BalanceRepository
	wmRepo      store.WatermarkRepository
	blockRepo   store.IndexedBlockRepository
	logger      *slog.Logger
}

// NewService creates a new replay Service. blockRepo may be nil if
// indexed_blocks tracking is not enabled.
func NewService(
	db store.TxBeginner,
	balanceRepo store.BalanceRepository,
	wmRepo store.WatermarkRepository,
	blockRepo store.IndexedBlockRepository,
	logger *slog.Logger,
) *Service {
	return &Service{
		db:          db,
		balanceRepo: balanceRepo,
		wmRepo:      wmRepo,
		blockRepo:   blockRepo,
		logger:      logger.With("component", "replay"),
	}
}

// PurgeRequest describes a replay/purge operation.
type PurgeRequest struct {
	Chain         model.Chain
	Network       model.Network
	FromBlock     int64
	DryRun        bool
	Force         bool
	Reason        string
	BlockScanMode bool // skip per-address cursor rewind for block-based chains
}

// PurgeResult describes the outcome of a purge operation.
type PurgeResult struct {
	PurgedEvents       int64 `json:"purged_events"`
	PurgedTransactions int64 `json:"purged_transactions"`
	PurgedBlocks       int64 `json:"purged_blocks"`
	ReversedBalances   int64 `json:"reversed_balances"`
	NewWatermark       int64 `json:"new_watermark"`
	CursorsRewound     int64 `json:"cursors_rewound"`
	DryRun             bool  `json:"dry_run"`
	DurationMs         int64 `json:"duration_ms"`
}

// ErrFinalizedBlock is returned when attempting to purge a finalized block
// without Force=true.
var ErrFinalizedBlock = fmt.Errorf("target block is finalized; set force=true to override")

// PurgeFromBlock removes all indexed data from fromBlock onward for the
// given chain/network, reverting balance deltas and rewinding cursors.
func (s *Service) PurgeFromBlock(ctx context.Context, req PurgeRequest) (*PurgeResult, error) {
	start := time.Now()

	s.logger.Info("purge requested",
		"chain", req.Chain,
		"network", req.Network,
		"from_block", req.FromBlock,
		"dry_run", req.DryRun,
		"force", req.Force,
		"reason", req.Reason,
	)

	// Step 0: Finality safety check
	if !req.Force && s.blockRepo != nil {
		block, err := s.blockRepo.GetByBlockNumber(ctx, req.Chain, req.Network, req.FromBlock)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("finality check: %w", err)
		}
		if block != nil && block.FinalityState == "finalized" {
			return nil, ErrFinalizedBlock
		}
	}

	if req.DryRun {
		return s.dryRun(ctx, req, start)
	}

	return s.executePurge(ctx, req, start)
}

func (s *Service) dryRun(ctx context.Context, req PurgeRequest, start time.Time) (*PurgeResult, error) {
	result := &PurgeResult{DryRun: true}

	var eventCount, txCount, blockCount int64

	dryCtx, cancel := context.WithTimeout(ctx, defaultQueryTimeout)
	defer cancel()

	dryTx, err := s.db.BeginTx(dryCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("dry run begin tx: %w", err)
	}
	defer dryTx.Rollback()

	err = dryTx.QueryRowContext(dryCtx, `
		SELECT
			(SELECT COUNT(*) FROM balance_events WHERE chain = $1 AND network = $2 AND block_cursor >= $3),
			(SELECT COUNT(*) FROM transactions WHERE chain = $1 AND network = $2 AND block_cursor >= $3),
			(SELECT COUNT(*) FROM indexed_blocks WHERE chain = $1 AND network = $2 AND block_number >= $3)
	`, req.Chain, req.Network, req.FromBlock).Scan(&eventCount, &txCount, &blockCount)
	if err != nil {
		return nil, fmt.Errorf("dry run counts: %w", err)
	}

	result.PurgedEvents = eventCount
	result.PurgedTransactions = txCount
	result.PurgedBlocks = blockCount
	result.ReversedBalances = eventCount // approximate
	result.NewWatermark = req.FromBlock - 1
	if result.NewWatermark < 0 {
		result.NewWatermark = 0
	}
	result.DurationMs = time.Since(start).Milliseconds()

	metrics.ReplayDryRunsTotal.WithLabelValues(string(req.Chain), string(req.Network)).Inc()

	s.logger.Info("dry run completed",
		"chain", req.Chain,
		"network", req.Network,
		"from_block", req.FromBlock,
		"events", eventCount,
		"transactions", txCount,
		"blocks", blockCount,
	)

	return result, nil
}

func (s *Service) executePurge(ctx context.Context, req PurgeRequest, start time.Time) (*PurgeResult, error) {
	dbTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin purge tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if rbErr := dbTx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
				s.logger.Warn("purge rollback tx rollback failed", "error", rbErr)
			}
		}
	}()

	result := &PurgeResult{}

	// Step 1: Fetch balance events to be rolled back
	rollbackEvents, err := s.fetchRollbackEvents(ctx, dbTx, req.Chain, req.Network, req.FromBlock)
	if err != nil {
		return nil, fmt.Errorf("fetch rollback events: %w", err)
	}

	// Step 2: Reverse applied balance deltas (including staking) using bulk adjust
	var liquidItems []store.BulkAdjustItem
	var stakedItems []store.BulkAdjustItem
	for _, be := range rollbackEvents {
		if !be.balanceApplied {
			continue
		}
		invertedDelta, err := identity.NegateDecimalString(be.delta)
		if err != nil {
			return nil, fmt.Errorf("negate delta for %s: %w", be.txHash, err)
		}
		liquidItems = append(liquidItems, store.BulkAdjustItem{
			Address:  be.address,
			TokenID:  be.tokenID,
			WalletID: be.walletID,
			OrgID:    be.organizationID,
			Delta:    invertedDelta,
			Cursor:   be.blockCursor,
			TxHash:   be.txHash,
		})

		if identity.IsStakingActivity(be.activityType) {
			stakedItems = append(stakedItems, store.BulkAdjustItem{
				Address:     be.address,
				TokenID:     be.tokenID,
				WalletID:    be.walletID,
				OrgID:       be.organizationID,
				Delta:       be.delta,
				Cursor:      be.blockCursor,
				TxHash:      be.txHash,
				BalanceType: "staked",
			})
		}
	}
	if len(liquidItems) > 0 {
		if err := s.balanceRepo.BulkAdjustBalanceTx(ctx, dbTx, req.Chain, req.Network, liquidItems); err != nil {
			return nil, fmt.Errorf("revert balances: %w", err)
		}
	}
	if len(stakedItems) > 0 {
		if err := s.balanceRepo.BulkAdjustBalanceTx(ctx, dbTx, req.Chain, req.Network, stakedItems); err != nil {
			return nil, fmt.Errorf("revert staked balances: %w", err)
		}
	}
	result.ReversedBalances = int64(len(liquidItems))

	// Step 3: Delete balance events from fromBlock onward
	evRes, err := dbTx.ExecContext(ctx, `
		DELETE FROM balance_events
		WHERE chain = $1 AND network = $2 AND block_cursor >= $3
	`, req.Chain, req.Network, req.FromBlock)
	if err != nil {
		return nil, fmt.Errorf("delete balance events: %w", err)
	}
	result.PurgedEvents, _ = evRes.RowsAffected()

	// Step 4: Delete transactions from fromBlock onward
	txRes, err := dbTx.ExecContext(ctx, `
		DELETE FROM transactions
		WHERE chain = $1 AND network = $2 AND block_cursor >= $3
	`, req.Chain, req.Network, req.FromBlock)
	if err != nil {
		return nil, fmt.Errorf("delete transactions: %w", err)
	}
	result.PurgedTransactions, _ = txRes.RowsAffected()

	// Step 5: Delete indexed blocks from fromBlock onward
	if s.blockRepo != nil {
		blocksDeleted, err := s.blockRepo.DeleteFromBlockTx(ctx, dbTx, req.Chain, req.Network, req.FromBlock)
		if err != nil {
			return nil, fmt.Errorf("delete indexed blocks: %w", err)
		}
		result.PurgedBlocks = blocksDeleted
	}

	// Step 6: Rewind watermark
	newWatermark := req.FromBlock - 1
	if newWatermark < 0 {
		newWatermark = 0
	}
	if err := s.wmRepo.RewindWatermarkTx(ctx, dbTx, req.Chain, req.Network, newWatermark); err != nil {
		return nil, fmt.Errorf("rewind watermark: %w", err)
	}
	result.NewWatermark = newWatermark

	if err := dbTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit purge: %w", err)
	}
	committed = true

	result.DurationMs = time.Since(start).Milliseconds()

	// Record metrics
	chainStr := string(req.Chain)
	networkStr := string(req.Network)
	metrics.ReplayPurgesTotal.WithLabelValues(chainStr, networkStr).Inc()
	metrics.ReplayPurgedEventsTotal.WithLabelValues(chainStr, networkStr).Add(float64(result.PurgedEvents))
	metrics.ReplayBalancesReversedTotal.WithLabelValues(chainStr, networkStr).Add(float64(result.ReversedBalances))
	metrics.ReplayTransactionsDeletedTotal.WithLabelValues(chainStr, networkStr).Add(float64(result.PurgedTransactions))
	metrics.ReplayBlocksDeletedTotal.WithLabelValues(chainStr, networkStr).Add(float64(result.PurgedBlocks))
	metrics.ReplayCursorsRewoundTotal.WithLabelValues(chainStr, networkStr).Add(float64(result.CursorsRewound))
	metrics.ReplayPurgeDurationSeconds.WithLabelValues(chainStr, networkStr).Observe(time.Since(start).Seconds())

	s.logger.Info("purge completed",
		"chain", req.Chain,
		"network", req.Network,
		"from_block", req.FromBlock,
		"events_purged", result.PurgedEvents,
		"transactions_purged", result.PurgedTransactions,
		"reversed_balances", result.ReversedBalances,
		"cursors_rewound", result.CursorsRewound,
		"new_watermark", result.NewWatermark,
		"duration_ms", result.DurationMs,
		"reason", req.Reason,
	)

	return result, nil
}

// rollbackEvent holds the fields needed to reverse a balance delta.
type rollbackEvent struct {
	id             uuid.UUID
	tokenID        uuid.UUID
	address        string
	delta          string
	blockCursor    int64
	txHash         string
	walletID       *string
	organizationID *string
	activityType   model.ActivityType
	balanceApplied bool
}

const rollbackBatchSize = 1000

func (s *Service) fetchRollbackEvents(
	ctx context.Context,
	tx *sql.Tx,
	chain model.Chain,
	network model.Network,
	fromBlock int64,
) ([]rollbackEvent, error) {
	var all []rollbackEvent
	var lastID uuid.UUID
	for {
		batch, err := s.fetchRollbackBatch(ctx, tx, chain, network, fromBlock, lastID, rollbackBatchSize)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < rollbackBatchSize {
			break
		}
		lastID = batch[len(batch)-1].id
	}
	return all, nil
}

func (s *Service) fetchRollbackBatch(
	ctx context.Context,
	tx *sql.Tx,
	chain model.Chain,
	network model.Network,
	fromBlock int64,
	afterID uuid.UUID,
	limit int,
) ([]rollbackEvent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, token_id, address, delta, block_cursor, tx_hash, wallet_id, organization_id, activity_type, balance_applied
		FROM balance_events
		WHERE chain = $1 AND network = $2 AND block_cursor >= $3 AND id > $4
		ORDER BY id ASC
		LIMIT $5
	`, chain, network, fromBlock, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("query rollback events: %w", err)
	}
	defer rows.Close()

	var events []rollbackEvent
	for rows.Next() {
		var e rollbackEvent
		var walletID sql.NullString
		var organizationID sql.NullString
		if err := rows.Scan(
			&e.id, &e.tokenID, &e.address, &e.delta, &e.blockCursor,
			&e.txHash, &walletID, &organizationID,
			&e.activityType, &e.balanceApplied,
		); err != nil {
			return nil, fmt.Errorf("scan rollback event: %w", err)
		}
		if walletID.Valid {
			e.walletID = &walletID.String
		}
		if organizationID.Valid {
			e.organizationID = &organizationID.String
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

