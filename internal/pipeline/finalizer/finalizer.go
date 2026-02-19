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
type Finalizer struct {
	chain         model.Chain
	network       model.Network
	adapter       chain.ReorgAwareAdapter
	blockRepo     store.IndexedBlockRepository
	finalityCh    chan<- event.FinalityPromotion
	interval      time.Duration
	lastFinalized int64
	logger        *slog.Logger
}

func New(
	chainID model.Chain,
	network model.Network,
	adapter chain.ReorgAwareAdapter,
	blockRepo store.IndexedBlockRepository,
	finalityCh chan<- event.FinalityPromotion,
	interval time.Duration,
	logger *slog.Logger,
) *Finalizer {
	if interval <= 0 {
		interval = defaultInterval
	}
	return &Finalizer{
		chain:      chainID,
		network:    network,
		adapter:    adapter,
		blockRepo:  blockRepo,
		finalityCh: finalityCh,
		interval:   interval,
		logger:     logger.With("component", "finalizer", "chain", chainID, "network", network),
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

	return nil
}
