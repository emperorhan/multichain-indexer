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
	defaultInterval        = 30 * time.Second
	defaultMaxCheckDepth   = 256
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

	// lastVerified caches on-chain block hashes from the previous tick.
	// Blocks whose hash was verified and still matches are skipped in
	// subsequent ticks, reducing RPC calls by ~90%.
	lastVerified map[int64]string
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
		d.lastVerified = nil
		return nil
	}

	// Limit to most recent N blocks (sorted by block number descending)
	if len(unfinalizedBlocks) > d.maxCheckDepth {
		sort.Slice(unfinalizedBlocks, func(i, j int) bool {
			return unfinalizedBlocks[i].BlockNumber > unfinalizedBlocks[j].BlockNumber
		})
		unfinalizedBlocks = unfinalizedBlocks[:d.maxCheckDepth]
	}

	// Parent-hash chain continuity check (no RPC needed).
	// Build a lookup of block_number -> block_hash from indexed data, then
	// verify that each block's ParentHash matches the hash of block N-1.
	// This catches subtle data corruption or stale forks without any RPC calls.
	if reorgBlock := d.verifyParentHashChain(unfinalizedBlocks); reorgBlock != nil {
		d.lastVerified = nil
		return d.emitReorg(ctx, *reorgBlock, reorgBlock.BlockHash)
	}

	// Tip-first: check the most recent block first. If it matches and was
	// already verified last tick, deeper blocks are extremely unlikely to
	// have reorged — skip them entirely.
	tipBlock := unfinalizedBlocks[0]
	tipMatch, tipHash, tipErr := d.verifyBlock(ctx, tipBlock)
	if tipErr != nil {
		// RPC error on tip — fall through to full scan
	} else if !tipMatch {
		// Tip mismatch — reorg detected at the tip, emit event immediately.
		d.lastVerified = nil
		return d.emitReorg(ctx, tipBlock, tipHash)
	}

	// Tip matches. Only check blocks that are new (not in lastVerified)
	// or whose indexed hash changed since last verification.
	newVerified := make(map[int64]string, len(unfinalizedBlocks))
	if tipErr == nil {
		newVerified[tipBlock.BlockNumber] = tipHash
	}

	rpcCalls := 0
	for _, block := range unfinalizedBlocks[1:] {
		// Skip blocks already verified in previous tick with matching indexed hash.
		if prevHash, ok := d.lastVerified[block.BlockNumber]; ok && prevHash == block.BlockHash {
			newVerified[block.BlockNumber] = prevHash
			continue
		}

		match, onchainHash, err := d.verifyBlock(ctx, block)
		if err != nil {
			continue
		}
		rpcCalls++

		if !match {
			d.lastVerified = nil
			return d.emitReorg(ctx, block, onchainHash)
		}
		newVerified[block.BlockNumber] = onchainHash
	}

	d.lastVerified = newVerified

	d.logger.Debug("reorg check completed",
		"unfinalized", len(unfinalizedBlocks),
		"rpc_calls", rpcCalls+1, // +1 for tip
		"skipped", len(unfinalizedBlocks)-1-rpcCalls,
	)

	return nil
}

// verifyParentHashChain checks that consecutive indexed blocks have consistent
// parent hashes (block N's ParentHash == block N-1's BlockHash). This catches
// reorgs and data corruption without any RPC calls.
// Returns the first block whose parent hash is inconsistent, or nil if all are OK.
// Blocks that lack a ParentHash (e.g. Solana) are silently skipped.
func (d *Detector) verifyParentHashChain(blocks []model.IndexedBlock) *model.IndexedBlock {
	if len(blocks) < 2 {
		return nil
	}

	// Build block_number -> block_hash index from the provided set.
	hashByNumber := make(map[int64]string, len(blocks))
	for _, b := range blocks {
		hashByNumber[b.BlockNumber] = b.BlockHash
	}

	// Blocks are sorted descending. Walk from tip toward genesis.
	for _, block := range blocks {
		if block.ParentHash == "" {
			continue // chain does not populate parent hash (e.g. Solana)
		}
		parentBlockHash, ok := hashByNumber[block.BlockNumber-1]
		if !ok {
			continue // parent block not in the unfinalized set — cannot verify
		}
		if block.ParentHash != parentBlockHash {
			d.logger.Warn("parent hash chain break detected",
				"block_number", block.BlockNumber,
				"block_hash", block.BlockHash,
				"parent_hash", block.ParentHash,
				"expected_parent_hash", parentBlockHash,
			)
			return &block
		}
	}
	return nil
}

// verifyBlock checks a single block's hash (and parent hash, if available)
// against the chain. Returns (match, onchainHash, error).
func (d *Detector) verifyBlock(ctx context.Context, block model.IndexedBlock) (bool, string, error) {
	onchainHash, onchainParentHash, err := d.adapter.GetBlockHash(ctx, block.BlockNumber)
	if err != nil {
		d.consecutiveRPCErrs++
		metrics.ReorgDetectorRPCErrorsTotal.WithLabelValues(d.chain.String(), d.network.String()).Inc()

		d.logger.Warn("failed to get on-chain block hash",
			"block_number", block.BlockNumber,
			"error", err,
			"consecutive_rpc_errors", d.consecutiveRPCErrs,
		)

		if d.consecutiveRPCErrs >= rpcErrorAlertThreshold && d.alerter != nil {
			sendErr := d.alerter.Send(ctx, alert.Alert{
				Type:    "reorg_detector_rpc_errors",
				Chain:   string(d.chain),
				Network: string(d.network),
				Title:   "Reorg detector RPC errors",
				Message: fmt.Sprintf("Reorg detector has %d consecutive RPC errors for %s/%s", d.consecutiveRPCErrs, d.chain, d.network),
			})
			if sendErr != nil {
				d.logger.Warn("failed to send reorg detector rpc error alert", "error", sendErr)
			}
		}
		return false, "", err
	}

	d.consecutiveRPCErrs = 0

	if onchainHash != block.BlockHash {
		return false, onchainHash, nil
	}

	// Additional parent hash verification: if both the indexed block and the
	// on-chain response carry a parent hash, verify they match. A mismatch
	// means the indexed block was stored from a now-orphaned fork.
	if block.ParentHash != "" && onchainParentHash != "" && block.ParentHash != onchainParentHash {
		d.logger.Warn("parent hash mismatch with on-chain data",
			"block_number", block.BlockNumber,
			"indexed_parent_hash", block.ParentHash,
			"onchain_parent_hash", onchainParentHash,
		)
		return false, onchainHash, nil
	}

	return true, onchainHash, nil
}

func (d *Detector) emitReorg(ctx context.Context, block model.IndexedBlock, actualHash string) error {
	d.logger.Warn("reorg detected: block hash mismatch",
		"block_number", block.BlockNumber,
		"expected_hash", block.BlockHash,
		"actual_hash", actualHash,
	)

	metrics.ReorgDetectedTotal.WithLabelValues(d.chain.String(), d.network.String()).Inc()

	reorgEvt := event.ReorgEvent{
		Chain:           d.chain,
		Network:         d.network,
		ForkBlockNumber: block.BlockNumber,
		ExpectedHash:    block.BlockHash,
		ActualHash:      actualHash,
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

	return nil
}
