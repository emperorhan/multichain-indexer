package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
)

const (
	defaultRetryMaxAttempts = 4
	defaultBackoffInitial   = 200 * time.Millisecond
	defaultBackoffMax       = 3 * time.Second
	defaultAdaptiveMinBatch = 1
)

// Fetcher consumes FetchJobs, calls ChainAdapter, and produces RawBatches.
type Fetcher struct {
	adapter     chain.ChainAdapter
	jobCh       <-chan event.FetchJob
	rawBatchCh  chan<- event.RawBatch
	workerCount int
	logger      *slog.Logger

	retryMaxAttempts int
	backoffInitial   time.Duration
	backoffMax       time.Duration
	adaptiveMinBatch int
	sleepFn          func(ctx context.Context, d time.Duration) error

	batchStateMu       sync.Mutex
	batchSizeByAddress map[string]int
}

func New(
	adapter chain.ChainAdapter,
	jobCh <-chan event.FetchJob,
	rawBatchCh chan<- event.RawBatch,
	workerCount int,
	logger *slog.Logger,
) *Fetcher {
	if workerCount <= 0 {
		workerCount = 1
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Fetcher{
		adapter:            adapter,
		jobCh:              jobCh,
		rawBatchCh:         rawBatchCh,
		workerCount:        workerCount,
		logger:             logger.With("component", "fetcher"),
		retryMaxAttempts:   defaultRetryMaxAttempts,
		backoffInitial:     defaultBackoffInitial,
		backoffMax:         defaultBackoffMax,
		adaptiveMinBatch:   defaultAdaptiveMinBatch,
		batchSizeByAddress: make(map[string]int),
	}
}

func (f *Fetcher) Run(ctx context.Context) error {
	f.logger.Info("fetcher started", "workers", f.workerCount)

	var wg sync.WaitGroup
	for i := 0; i < f.workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			f.worker(ctx, workerID)
		}(i)
	}

	wg.Wait()
	f.logger.Info("fetcher stopped")
	return ctx.Err()
}

func (f *Fetcher) worker(ctx context.Context, workerID int) {
	log := f.logger.With("worker", workerID)

	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-f.jobCh:
			if !ok {
				return
			}
			if err := f.processJob(ctx, log, job); err != nil {
				log.Error("process job failed",
					"address", job.Address,
					"error", err,
				)
				panic(fmt.Sprintf("fetcher process job failed: address=%s err=%v", job.Address, err))
			}
		}
	}
}

func (f *Fetcher) processJob(ctx context.Context, log *slog.Logger, job event.FetchJob) error {
	requestedBatch := f.resolveBatchSize(job.Address, job.BatchSize)

	// 1. Fetch new signatures with retry/backoff and adaptive batch size reduction.
	sigs, sigBatchSize, err := f.fetchSignaturesWithRetry(ctx, log, job, requestedBatch)
	if err != nil {
		f.setAdaptiveBatchSize(job.Address, sigBatchSize)
		return err
	}

	if len(sigs) == 0 {
		log.Debug("no new signatures", "address", job.Address)
		return nil
	}

	// Canonicalize provider-returned ordering and suppress overlap duplicates.
	sigs = canonicalizeSignatures(sigs)
	if len(sigs) == 0 {
		log.Debug("no canonical signatures after overlap suppression", "address", job.Address)
		return nil
	}

	// 2. Fetch raw transactions with retry/backoff.
	selectedSigs, rawTxs, txBatchSize, err := f.fetchTransactionsWithRetry(ctx, log, sigs)
	if err != nil {
		f.setAdaptiveBatchSize(job.Address, txBatchSize)
		return err
	}

	// 3. Extract signature hashes for the selected subset.
	sigInfos := make([]event.SignatureInfo, len(selectedSigs))
	for i, sig := range selectedSigs {
		sigInfos[i] = event.SignatureInfo{
			Hash:     sig.Hash,
			Sequence: sig.Sequence,
		}
	}

	// 4. Determine new cursor (newest = last in oldest-first list).
	newest := selectedSigs[len(selectedSigs)-1]
	cursorValue := newest.Hash

	batch := event.RawBatch{
		Chain:                  job.Chain,
		Network:                job.Network,
		Address:                job.Address,
		WalletID:               job.WalletID,
		OrgID:                  job.OrgID,
		PreviousCursorValue:    job.CursorValue,
		PreviousCursorSequence: job.CursorSequence,
		RawTransactions:        rawTxs,
		Signatures:             sigInfos,
		NewCursorValue:         &cursorValue,
		NewCursorSequence:      newest.Sequence,
	}

	select {
	case f.rawBatchCh <- batch:
		log.Info("raw batch sent",
			"address", job.Address,
			"tx_count", len(rawTxs),
			"new_cursor", cursorValue,
			"requested_batch", requestedBatch,
			"used_signature_batch", sigBatchSize,
			"used_transaction_batch", txBatchSize,
		)
	case <-ctx.Done():
		return ctx.Err()
	}

	f.updateAdaptiveBatchSize(job.Address, job.BatchSize, requestedBatch, sigBatchSize, txBatchSize, len(selectedSigs))
	return nil
}

func (f *Fetcher) fetchSignaturesWithRetry(ctx context.Context, log *slog.Logger, job event.FetchJob, batchSize int) ([]chain.SignatureInfo, int, error) {
	const stage = "fetcher.fetch_signatures"

	currentBatch := batchSize
	attempts := f.effectiveRetryMaxAttempts()

	var lastErr error
	lastDecision := retry.Decision{
		Class:  retry.ClassTerminal,
		Reason: "unset",
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		sigs, err := f.adapter.FetchNewSignatures(ctx, job.Address, job.CursorValue, currentBatch)
		if err == nil {
			return sigs, currentBatch, nil
		}
		lastErr = err
		lastDecision = retry.Classify(err)

		if ctx.Err() != nil {
			return nil, currentBatch, ctx.Err()
		}
		if !lastDecision.IsTransient() {
			return nil, currentBatch, fmt.Errorf("terminal_failure stage=%s attempt=%d reason=%s: %w", stage, attempt, lastDecision.Reason, err)
		}
		if attempt == attempts {
			break
		}

		nextBatch := f.reduceBatchSize(currentBatch)
		if nextBatch < currentBatch {
			log.Warn("signature fetch failed; reducing batch",
				"stage", stage,
				"classification", lastDecision.Class,
				"classification_reason", lastDecision.Reason,
				"address", job.Address,
				"attempt", attempt,
				"from", currentBatch,
				"to", nextBatch,
				"error", err,
			)
			currentBatch = nextBatch
		} else {
			log.Warn("signature fetch failed; retrying",
				"stage", stage,
				"classification", lastDecision.Class,
				"classification_reason", lastDecision.Reason,
				"address", job.Address,
				"attempt", attempt,
				"batch_size", currentBatch,
				"error", err,
			)
		}

		delay := f.retryDelay(attempt)
		if sleepErr := f.sleep(ctx, delay); sleepErr != nil {
			return nil, currentBatch, sleepErr
		}
	}

	return nil, currentBatch, fmt.Errorf("transient_recovery_exhausted stage=%s attempts=%d reason=%s: %w", stage, attempts, lastDecision.Reason, lastErr)
}

func (f *Fetcher) fetchTransactionsWithRetry(ctx context.Context, log *slog.Logger, sigs []chain.SignatureInfo) ([]chain.SignatureInfo, []json.RawMessage, int, error) {
	const stage = "fetcher.fetch_transactions"

	currentBatch := len(sigs)
	attempts := f.effectiveRetryMaxAttempts()

	var lastErr error
	lastDecision := retry.Decision{
		Class:  retry.ClassTerminal,
		Reason: "unset",
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		selected := sigs[:currentBatch]
		sigHashes := make([]string, len(selected))
		for i, sig := range selected {
			sigHashes[i] = sig.Hash
		}

		rawTxs, err := f.adapter.FetchTransactions(ctx, sigHashes)
		if err == nil {
			return selected, rawTxs, currentBatch, nil
		}
		lastErr = err
		lastDecision = retry.Classify(err)

		if ctx.Err() != nil {
			return nil, nil, currentBatch, ctx.Err()
		}
		if !lastDecision.IsTransient() {
			return nil, nil, currentBatch, fmt.Errorf("terminal_failure stage=%s attempt=%d reason=%s: %w", stage, attempt, lastDecision.Reason, err)
		}
		if attempt == attempts {
			break
		}

		nextBatch := f.reduceBatchSize(currentBatch)
		if nextBatch < currentBatch {
			log.Warn("transaction fetch failed; reducing batch",
				"stage", stage,
				"classification", lastDecision.Class,
				"classification_reason", lastDecision.Reason,
				"attempt", attempt,
				"from", currentBatch,
				"to", nextBatch,
				"error", err,
			)
			currentBatch = nextBatch
		} else {
			log.Warn("transaction fetch failed; retrying",
				"stage", stage,
				"classification", lastDecision.Class,
				"classification_reason", lastDecision.Reason,
				"attempt", attempt,
				"batch_size", currentBatch,
				"error", err,
			)
		}

		delay := f.retryDelay(attempt)
		if sleepErr := f.sleep(ctx, delay); sleepErr != nil {
			return nil, nil, currentBatch, sleepErr
		}
	}

	return nil, nil, currentBatch, fmt.Errorf("transient_recovery_exhausted stage=%s attempts=%d reason=%s: %w", stage, attempts, lastDecision.Reason, lastErr)
}

func (f *Fetcher) resolveBatchSize(address string, hardCap int) int {
	if hardCap <= 0 {
		hardCap = 1
	}

	f.batchStateMu.Lock()
	defer f.batchStateMu.Unlock()

	if f.batchSizeByAddress == nil {
		f.batchSizeByAddress = make(map[string]int)
	}

	size, ok := f.batchSizeByAddress[address]
	if !ok || size <= 0 {
		f.batchSizeByAddress[address] = hardCap
		return hardCap
	}
	if size > hardCap {
		size = hardCap
		f.batchSizeByAddress[address] = size
	}
	if size < f.effectiveAdaptiveMinBatch() {
		size = f.effectiveAdaptiveMinBatch()
		f.batchSizeByAddress[address] = size
	}
	return size
}

func (f *Fetcher) setAdaptiveBatchSize(address string, size int) {
	if size <= 0 {
		return
	}
	if size < f.effectiveAdaptiveMinBatch() {
		size = f.effectiveAdaptiveMinBatch()
	}

	f.batchStateMu.Lock()
	defer f.batchStateMu.Unlock()

	if f.batchSizeByAddress == nil {
		f.batchSizeByAddress = make(map[string]int)
	}
	f.batchSizeByAddress[address] = size
}

func (f *Fetcher) updateAdaptiveBatchSize(address string, hardCap, requested, usedSigBatch, usedTxBatch, selectedCount int) {
	usedBatch := usedSigBatch
	if usedTxBatch < usedBatch {
		usedBatch = usedTxBatch
	}
	if usedBatch <= 0 {
		return
	}

	if usedBatch < requested {
		f.setAdaptiveBatchSize(address, usedBatch)
		return
	}

	if hardCap <= 0 {
		hardCap = requested
	}
	if hardCap <= 0 {
		hardCap = 1
	}

	if selectedCount == requested && requested < hardCap {
		next := requested * 2
		if next > hardCap {
			next = hardCap
		}
		f.setAdaptiveBatchSize(address, next)
	}
}

func (f *Fetcher) reduceBatchSize(current int) int {
	minBatch := f.effectiveAdaptiveMinBatch()
	if current <= minBatch {
		return current
	}
	next := current / 2
	if next < minBatch {
		next = minBatch
	}
	return next
}

func (f *Fetcher) retryDelay(attempt int) time.Duration {
	base := f.effectiveBackoffInitial()
	max := f.effectiveBackoffMax()
	if base <= 0 {
		base = defaultBackoffInitial
	}
	if max <= 0 || max < base {
		max = base
	}

	delay := base
	for i := 1; i < attempt; i++ {
		if delay >= max/2 {
			return max
		}
		delay *= 2
	}
	if delay > max {
		return max
	}
	return delay
}

func (f *Fetcher) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if f.sleepFn != nil {
		return f.sleepFn(ctx, d)
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *Fetcher) effectiveRetryMaxAttempts() int {
	if f.retryMaxAttempts <= 0 {
		return defaultRetryMaxAttempts
	}
	return f.retryMaxAttempts
}

func (f *Fetcher) effectiveBackoffInitial() time.Duration {
	if f.backoffInitial <= 0 {
		return defaultBackoffInitial
	}
	return f.backoffInitial
}

func (f *Fetcher) effectiveBackoffMax() time.Duration {
	if f.backoffMax <= 0 {
		return defaultBackoffMax
	}
	return f.backoffMax
}

func (f *Fetcher) effectiveAdaptiveMinBatch() int {
	if f.adaptiveMinBatch <= 0 {
		return defaultAdaptiveMinBatch
	}
	return f.adaptiveMinBatch
}

func canonicalizeSignatures(sigs []chain.SignatureInfo) []chain.SignatureInfo {
	if len(sigs) == 0 {
		return []chain.SignatureInfo{}
	}

	byIdentity := make(map[string]chain.SignatureInfo, len(sigs))
	for _, sig := range sigs {
		identity := canonicalSignatureIdentity(sig.Hash)
		if identity == "" {
			continue
		}

		candidate := chain.SignatureInfo{
			Hash:     identity,
			Sequence: sig.Sequence,
			Time:     sig.Time,
		}

		existing, ok := byIdentity[identity]
		if !ok || shouldReplaceCanonicalSignature(existing, candidate) {
			byIdentity[identity] = candidate
		}
	}

	ordered := make([]chain.SignatureInfo, 0, len(byIdentity))
	for _, sig := range byIdentity {
		ordered = append(ordered, sig)
	}

	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Sequence != ordered[j].Sequence {
			return ordered[i].Sequence < ordered[j].Sequence
		}
		return ordered[i].Hash < ordered[j].Hash
	})

	return ordered
}

func shouldReplaceCanonicalSignature(existing, incoming chain.SignatureInfo) bool {
	if existing.Sequence != incoming.Sequence {
		return incoming.Sequence > existing.Sequence
	}

	if existing.Time == nil && incoming.Time != nil {
		return true
	}
	if existing.Time != nil && incoming.Time == nil {
		return false
	}
	if existing.Time != nil && incoming.Time != nil && !existing.Time.Equal(*incoming.Time) {
		return incoming.Time.After(*existing.Time)
	}

	return incoming.Hash < existing.Hash
}

func canonicalSignatureIdentity(hash string) string {
	trimmed := strings.TrimSpace(hash)
	if trimmed == "" {
		return ""
	}
	// EVM tx hashes are hex and case-insensitive; normalize to suppress case-only duplicates.
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return strings.ToLower(trimmed)
	}
	return trimmed
}
