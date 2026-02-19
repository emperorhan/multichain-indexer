package finalizer

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

const defaultInterval = 60 * time.Second

// Finalizer periodically checks the chain's finalized block number and sends
// FinalityPromotion events to the ingester when new blocks become finalized.
// After each promotion it prunes old finalized blocks from indexed_blocks,
// keeping only the most recent retentionBlocks entries.
type Finalizer struct {
	chain           model.Chain
	network         model.Network
	adapter         chain.ReorgAwareAdapter
	blockRepo       store.IndexedBlockRepository
	finalityCh      chan<- event.FinalityPromotion
	interval        time.Duration
	retentionBlocks int64
	lastFinalized   int64
	logger          *slog.Logger
}

func New(
	chainID model.Chain,
	network model.Network,
	adapter chain.ReorgAwareAdapter,
	blockRepo store.IndexedBlockRepository,
	finalityCh chan<- event.FinalityPromotion,
	interval time.Duration,
	logger *slog.Logger,
	opts ...Option,
) *Finalizer {
	if interval <= 0 {
		interval = defaultInterval
	}
	f := &Finalizer{
		chain:      chainID,
		network:    network,
		adapter:    adapter,
		blockRepo:  blockRepo,
		finalityCh: finalityCh,
		interval:   interval,
		logger:     logger.With("component", "finalizer", "chain", chainID, "network", network),
	}
	for _, o := range opts {
		o(f)
	}
	return f
}

// Option configures optional Finalizer behaviour.
type Option func(*Finalizer)

// WithRetentionBlocks sets the number of finalized blocks to retain.
// A value of 0 disables pruning.
func WithRetentionBlocks(n int64) Option {
	return func(f *Finalizer) {
		f.retentionBlocks = n
	}
}

func (f *Finalizer) Run(ctx context.Context) error {
	f.logger.Info("finalizer started", "interval", f.interval)

	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			f.logger.Info("finalizer stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := f.check(ctx); err != nil {
				f.logger.Warn("finalizer check failed", "error", err)
			}
		}
	}
}

func (f *Finalizer) check(ctx context.Context) error {
	currentFinalized, err := f.adapter.GetFinalizedBlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("get finalized block number: %w", err)
	}

	metrics.FinalizerLatestFinalizedBlock.WithLabelValues(f.chain.String(), f.network.String()).Set(float64(currentFinalized))

	if currentFinalized <= f.lastFinalized {
		return nil
	}

	promo := event.FinalityPromotion{
		Chain:             f.chain,
		Network:           f.network,
		NewFinalizedBlock: currentFinalized,
	}

	select {
	case f.finalityCh <- promo:
		f.logger.Info("finality promotion sent",
			"new_finalized_block", currentFinalized,
			"previous_finalized_block", f.lastFinalized,
		)
		metrics.FinalizerPromotionsTotal.WithLabelValues(f.chain.String(), f.network.String()).Inc()
		f.lastFinalized = currentFinalized
	case <-ctx.Done():
		return ctx.Err()
	}

	// Prune old finalized blocks if retention is configured.
	if f.retentionBlocks > 0 {
		cutoff := currentFinalized - f.retentionBlocks
		if cutoff > 0 {
			pruned, err := f.blockRepo.PurgeFinalizedBefore(ctx, f.chain, f.network, cutoff)
			if err != nil {
				f.logger.Warn("pruning finalized blocks failed", "cutoff_block", cutoff, "error", err)
			} else if pruned > 0 {
				metrics.FinalizerPrunedBlocksTotal.WithLabelValues(f.chain.String(), f.network.String()).Add(float64(pruned))
				f.logger.Info("pruned old finalized blocks",
					"cutoff_block", cutoff,
					"pruned_count", pruned,
				)
			}
		}
	}

	return nil
}
