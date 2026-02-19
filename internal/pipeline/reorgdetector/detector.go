package reorgdetector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

const defaultInterval = 30 * time.Second

// Detector periodically compares indexed block hashes against on-chain hashes
// to detect block reorganizations.
type Detector struct {
	chain     model.Chain
	network   model.Network
	adapter   chain.ReorgAwareAdapter
	blockRepo store.IndexedBlockRepository
	reorgCh   chan<- event.ReorgEvent
	interval  time.Duration
	logger    *slog.Logger
}

func New(
	chainID model.Chain,
	network model.Network,
	adapter chain.ReorgAwareAdapter,
	blockRepo store.IndexedBlockRepository,
	reorgCh chan<- event.ReorgEvent,
	interval time.Duration,
	logger *slog.Logger,
) *Detector {
	if interval <= 0 {
		interval = defaultInterval
	}
	return &Detector{
		chain:     chainID,
		network:   network,
		adapter:   adapter,
		blockRepo: blockRepo,
		reorgCh:   reorgCh,
		interval:  interval,
		logger:    logger.With("component", "reorg_detector", "chain", chainID, "network", network),
	}
}

func (d *Detector) Run(ctx context.Context) error {
	d.logger.Info("reorg detector started", "interval", d.interval)

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("reorg detector stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := d.check(ctx); err != nil {
				d.logger.Warn("reorg detector check failed", "error", err)
			}
		}
	}
}

func (d *Detector) check(ctx context.Context) error {
	start := time.Now()
	defer func() {
		metrics.ReorgDetectorCheckLatency.WithLabelValues(d.chain.String(), d.network.String()).Observe(time.Since(start).Seconds())
	}()

	unfinalizedBlocks, err := d.blockRepo.GetUnfinalized(ctx, d.chain, d.network)
	if err != nil {
		return fmt.Errorf("get unfinalized blocks: %w", err)
	}

	metrics.ReorgDetectorUnfinalizedBlocks.WithLabelValues(d.chain.String(), d.network.String()).Set(float64(len(unfinalizedBlocks)))

	if len(unfinalizedBlocks) == 0 {
		return nil
	}

	for _, block := range unfinalizedBlocks {
		onchainHash, _, err := d.adapter.GetBlockHash(ctx, block.BlockNumber)
		if err != nil {
			d.logger.Warn("failed to get on-chain block hash",
				"block_number", block.BlockNumber,
				"error", err,
			)
			continue
		}

		if onchainHash != block.BlockHash {
			d.logger.Warn("reorg detected: block hash mismatch",
				"block_number", block.BlockNumber,
				"expected_hash", block.BlockHash,
				"actual_hash", onchainHash,
			)

			metrics.ReorgDetectedTotal.WithLabelValues(d.chain.String(), d.network.String()).Inc()

			reorgEvt := event.ReorgEvent{
				Chain:           d.chain,
				Network:         d.network,
				ForkBlockNumber: block.BlockNumber,
				ExpectedHash:    block.BlockHash,
				ActualHash:      onchainHash,
				DetectedAt:      time.Now(),
			}

			select {
			case d.reorgCh <- reorgEvt:
				d.logger.Info("reorg event sent to ingester",
					"fork_block", block.BlockNumber,
				)
			case <-ctx.Done():
				return ctx.Err()
			}

			// Process one reorg per tick to avoid cascading effects
			return nil
		}
	}

	return nil
}
