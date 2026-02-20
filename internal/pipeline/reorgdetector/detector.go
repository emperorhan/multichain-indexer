package reorgdetector

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/alert"
	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

const (
	defaultInterval      = 30 * time.Second
	defaultMaxCheckDepth = 256
	rpcErrorAlertThreshold = 5
)

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

	maxCheckDepth      int
	consecutiveRPCErrs int
	alerter            alert.Alerter
	checkNowCh         chan struct{}
	running            atomic.Bool
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
		chain:         chainID,
		network:       network,
		adapter:       adapter,
		blockRepo:     blockRepo,
		reorgCh:       reorgCh,
		interval:      interval,
		logger:        logger.With("component", "reorg_detector", "chain", chainID, "network", network),
		maxCheckDepth: defaultMaxCheckDepth,
		checkNowCh:    make(chan struct{}, 1),
	}
}

// WithMaxCheckDepth sets the maximum number of unfinalized blocks to check per tick.
func (d *Detector) WithMaxCheckDepth(depth int) *Detector {
	if depth > 0 {
		d.maxCheckDepth = depth
	}
	return d
}

// WithAlerter sets the alerter for RPC error alerts.
func (d *Detector) WithAlerter(a alert.Alerter) *Detector {
	d.alerter = a
	return d
}

// CheckNow triggers an immediate reorg check (non-blocking).
func (d *Detector) CheckNow() {
	select {
	case d.checkNowCh <- struct{}{}:
	default:
	}
}

func (d *Detector) Run(ctx context.Context) error {
	d.running.Store(true)
	defer d.running.Store(false)

	d.logger.Info("reorg detector started", "interval", d.interval, "max_check_depth", d.maxCheckDepth)

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
		case <-d.checkNowCh:
			if err := d.check(ctx); err != nil {
				d.logger.Warn("reorg detector immediate check failed", "error", err)
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

	// Limit to most recent N blocks (sorted by block number descending)
	if len(unfinalizedBlocks) > d.maxCheckDepth {
		sort.Slice(unfinalizedBlocks, func(i, j int) bool {
			return unfinalizedBlocks[i].BlockNumber > unfinalizedBlocks[j].BlockNumber
		})
		unfinalizedBlocks = unfinalizedBlocks[:d.maxCheckDepth]
	}

	for _, block := range unfinalizedBlocks {
		onchainHash, _, err := d.adapter.GetBlockHash(ctx, block.BlockNumber)
		if err != nil {
			d.consecutiveRPCErrs++
			metrics.ReorgDetectorRPCErrorsTotal.WithLabelValues(d.chain.String(), d.network.String()).Inc()

			d.logger.Warn("failed to get on-chain block hash",
				"block_number", block.BlockNumber,
				"error", err,
				"consecutive_rpc_errors", d.consecutiveRPCErrs,
			)

			if d.consecutiveRPCErrs >= rpcErrorAlertThreshold && d.alerter != nil {
				d.alerter.Send(ctx, alert.Alert{
					Type:    "reorg_detector_rpc_errors",
					Chain:   string(d.chain),
					Network: string(d.network),
					Title:   "Reorg detector RPC errors",
					Message: fmt.Sprintf("Reorg detector has %d consecutive RPC errors for %s/%s", d.consecutiveRPCErrs, d.chain, d.network),
				})
			}
			continue
		}

		// Reset on success
		d.consecutiveRPCErrs = 0

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
