package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/cache"
	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/identity"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/coordinator/autotune"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/retry"
	"github.com/emperorhan/multichain-indexer/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelTrace "go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

const (
	defaultRetryMaxAttempts  = 4
	defaultBackoffInitial    = 200 * time.Millisecond
	defaultBackoffMax        = 3 * time.Second
	defaultAdaptiveMinBatch  = 1
	boundaryOverlapLookahead = 1
)

// Fetcher consumes FetchJobs, calls ChainAdapter, and produces RawBatches.
type Fetcher struct {
	adapter         chain.ChainAdapter
	jobCh           <-chan event.FetchJob
	rawBatchCh      chan<- event.RawBatch
	workerCount     int
	logger          *slog.Logger
	autoTuneSignals autotune.AutoTuneSignalSink

	retryMaxAttempts          int
	backoffInitial            time.Duration
	backoffMax                time.Duration
	adaptiveMinBatch          int
	boundaryOverlapLookahead  int
	sleepFn                   func(ctx context.Context, d time.Duration) error

	batchSizeByAddress *cache.LRU[string, int]
}

type Option func(*Fetcher)

func WithAutoTuneSignalSink(sink autotune.AutoTuneSignalSink) Option {
	return func(f *Fetcher) {
		f.autoTuneSignals = sink
	}
}

func WithRetryConfig(maxAttempts int, backoffInitial, backoffMax time.Duration) Option {
	return func(f *Fetcher) {
		f.retryMaxAttempts = maxAttempts
		f.backoffInitial = backoffInitial
		f.backoffMax = backoffMax
	}
}

func WithAdaptiveMinBatch(minBatch int) Option {
	return func(f *Fetcher) {
		f.adaptiveMinBatch = minBatch
	}
}

func WithBoundaryOverlapLookahead(n int) Option {
	return func(f *Fetcher) {
		f.boundaryOverlapLookahead = n
	}
}

type cutoffAwareAdapter interface {
	FetchNewSignaturesWithCutoff(ctx context.Context, address string, cursor *string, batchSize int, cutoffSeq int64) ([]chain.SignatureInfo, error)
}

type blockScanAdapter interface {
	ScanBlocks(ctx context.Context, startBlock, endBlock int64, watchedAddresses []string) ([]chain.SignatureInfo, error)
}

func New(
	adapter chain.ChainAdapter,
	jobCh <-chan event.FetchJob,
	rawBatchCh chan<- event.RawBatch,
	workerCount int,
	logger *slog.Logger,
	opts ...Option,
) *Fetcher {
	if workerCount <= 0 {
		workerCount = 1
	}
	if logger == nil {
		logger = slog.Default()
	}

	f := &Fetcher{
		adapter:            adapter,
		jobCh:              jobCh,
		rawBatchCh:         rawBatchCh,
		workerCount:        workerCount,
		logger:             logger.With("component", "fetcher"),
		retryMaxAttempts:   defaultRetryMaxAttempts,
		backoffInitial:     defaultBackoffInitial,
		backoffMax:         defaultBackoffMax,
		adaptiveMinBatch:   defaultAdaptiveMinBatch,
		batchSizeByAddress: cache.NewLRU[string, int](10000, time.Hour),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(f)
		}
	}
	return f
}

func (f *Fetcher) Run(ctx context.Context) error {
	f.logger.Info("fetcher started", "workers", f.workerCount)

	// Use errgroup so that a worker error propagates up and cancels all
	// sibling workers via gCtx, achieving fail-fast without panic.
	g, gCtx := errgroup.WithContext(ctx)
	for i := 0; i < f.workerCount; i++ {
		workerID := i
		g.Go(func() error {
			return f.worker(gCtx, workerID)
		})
	}

	err := g.Wait()
	f.logger.Info("fetcher stopped")
	return err
}

func (f *Fetcher) worker(ctx context.Context, workerID int) error {
	log := f.logger.With("worker", workerID)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case job, ok := <-f.jobCh:
			if !ok {
				return nil
			}
			spanCtx, span := tracing.Tracer("fetcher").Start(ctx, "fetcher.processJob",
				otelTrace.WithAttributes(
					attribute.String("chain", job.Chain.String()),
					attribute.String("network", job.Network.String()),
					attribute.String("address", job.Address),
				),
			)
			start := time.Now()
			if err := f.processJob(spanCtx, log, job); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.End()
				metrics.FetcherErrors.WithLabelValues(job.Chain.String(), job.Network.String()).Inc()
				metrics.FetcherLatency.WithLabelValues(job.Chain.String(), job.Network.String()).Observe(time.Since(start).Seconds())
				log.Error("process job failed",
					"address", job.Address,
					"error", err,
				)
				// Fail-fast: return error so errgroup cancels all workers and
				// the pipeline restarts from the last committed cursor.
				return fmt.Errorf("fetcher process job failed: address=%s: %w", job.Address, err)
			}
			span.End()
			metrics.FetcherBatchesProcessed.WithLabelValues(job.Chain.String(), job.Network.String()).Inc()
			metrics.FetcherLatency.WithLabelValues(job.Chain.String(), job.Network.String()).Observe(time.Since(start).Seconds())
		}
	}
}

func (f *Fetcher) processJob(ctx context.Context, log *slog.Logger, job event.FetchJob) error {
	if job.BlockScanMode {
		return f.processBlockScanJob(ctx, log, job)
	}

	fetchAddress := canonicalizeWatchedAddressIdentity(job.Chain, job.Address)
	if fetchAddress == "" {
		fetchAddress = job.Address
	}

	requestedBatch := f.resolveBatchSize(job.Chain, job.Network, fetchAddress, job.BatchSize)
	canonicalCursor := identity.CanonicalizeCursorValue(job.Chain, job.CursorValue)
	signatureBatch := requestedBatch
	if canonicalCursor != nil {
		signatureBatch += f.effectiveBoundaryOverlapLookahead()
	}

	// 1. Fetch new signatures with retry/backoff and adaptive batch size reduction.
	fetchJob := job
	fetchJob.Address = fetchAddress
	sigs, sigBatchSize, err := f.fetchSignaturesWithRetry(ctx, log, fetchJob, canonicalCursor, signatureBatch)
	if err != nil {
		f.setAdaptiveBatchSize(job.Chain, job.Network, fetchAddress, sigBatchSize)
		return err
	}

	if len(sigs) == 0 {
		log.Debug("no new signatures", "address", job.Address)
		return nil
	}

	// Canonicalize, dedup, and filter in a single pass (cutoff + boundary cursor),
	// then handle pre-cursor carryover separately (complex rollback logic).
	sigs = filterAndCanonicalizeSignatures(job.Chain, sigs, job.FetchCutoffSeq, canonicalCursor)
	sigs = suppressPreCursorSequenceCarryover(sigs, job.CursorSequence, requestedBatch)
	if len(sigs) == 0 {
		log.Debug("no canonical signatures after overlap suppression", "address", job.Address)
		return nil
	}
	if len(sigs) > requestedBatch {
		sigs = sigs[:requestedBatch]
	}

	// 2. Fetch raw transactions with retry/backoff.
	selectedSigs, rawTxs, txBatchSize, err := f.fetchTransactionsWithRetry(ctx, log, job, sigs)
	if err != nil {
		f.setAdaptiveBatchSize(job.Chain, job.Network, fetchAddress, txBatchSize)
		return err
	}

	// 3. Extract signature hashes for the selected subset.
	sigInfos := make([]event.SignatureInfo, len(selectedSigs))
	for i, sig := range selectedSigs {
		sigInfos[i] = event.SignatureInfo{
			Hash:     sig.Hash,
			Sequence: sig.Sequence,
			Time:     sig.Time,
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
		PreviousCursorValue:    canonicalCursor,
		PreviousCursorSequence: job.CursorSequence,
		RawTransactions:        rawTxs,
		Signatures:             sigInfos,
		NewCursorValue:         &cursorValue,
		NewCursorSequence:      newest.Sequence,
		CreatedAt:              time.Now(),
	}

	select {
	case f.rawBatchCh <- batch:
		metrics.FetcherTxFetched.WithLabelValues(job.Chain.String(), job.Network.String()).Add(float64(len(rawTxs)))
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

	f.updateAdaptiveBatchSize(job.Chain, job.Network, fetchAddress, job.BatchSize, requestedBatch, sigBatchSize, txBatchSize, len(selectedSigs))
	return nil
}

// processBlockScanJob handles a block-scan mode FetchJob by calling ScanBlocks
// on the adapter and then FetchTransactions for all discovered signatures.
// Transient errors (e.g. HTTP 429) are retried with exponential backoff.
func (f *Fetcher) processBlockScanJob(ctx context.Context, log *slog.Logger, job event.FetchJob) error {
	scanner, ok := f.adapter.(blockScanAdapter)
	if !ok {
		return fmt.Errorf("adapter does not implement BlockScanAdapter for chain %s", job.Chain)
	}

	maxAttempts := f.effectiveRetryMaxAttempts()

	// ScanBlocks with retry.
	var sigs []chain.SignatureInfo
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var err error
		sigs, err = scanner.ScanBlocks(ctx, job.StartBlock, job.EndBlock, job.WatchedAddresses)
		f.recordRPCResult(job.Chain.String(), job.Network.String(), err != nil)
		if err == nil {
			break
		}
		decision := retry.Classify(err)
		if !decision.IsTransient() || attempt == maxAttempts {
			return fmt.Errorf("block-scan ScanBlocks: %w", err)
		}
		delay := f.retryDelay(attempt)
		log.Warn("block-scan ScanBlocks transient error, retrying",
			"attempt", attempt, "delay", delay, "error", err)
		if sleepErr := f.sleep(ctx, delay); sleepErr != nil {
			return sleepErr
		}
	}

	if len(sigs) == 0 {
		log.Debug("block-scan: no signatures in range",
			"start_block", job.StartBlock,
			"end_block", job.EndBlock,
		)
		return nil
	}

	// Canonicalize and sort signatures.
	sigs = canonicalizeSignatures(job.Chain, sigs)
	if len(sigs) == 0 {
		return nil
	}

	// Fetch raw transactions with retry.
	sigHashes := make([]string, len(sigs))
	for i, sig := range sigs {
		sigHashes[i] = sig.Hash
	}

	var rawTxs []json.RawMessage
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var err error
		rawTxs, err = f.adapter.FetchTransactions(ctx, sigHashes)
		f.recordRPCResult(job.Chain.String(), job.Network.String(), err != nil)
		if err == nil {
			break
		}
		decision := retry.Classify(err)
		if !decision.IsTransient() || attempt == maxAttempts {
			return fmt.Errorf("block-scan FetchTransactions: %w", err)
		}
		delay := f.retryDelay(attempt)
		log.Warn("block-scan FetchTransactions transient error, retrying",
			"attempt", attempt, "delay", delay, "error", err)
		if sleepErr := f.sleep(ctx, delay); sleepErr != nil {
			return sleepErr
		}
	}

	sigInfos := make([]event.SignatureInfo, len(sigs))
	for i, sig := range sigs {
		sigInfos[i] = event.SignatureInfo{
			Hash:     sig.Hash,
			Sequence: sig.Sequence,
			Time:     sig.Time,
		}
	}

	newest := sigs[len(sigs)-1]
	cursorValue := newest.Hash

	batch := event.RawBatch{
		Chain:             job.Chain,
		Network:           job.Network,
		RawTransactions:   rawTxs,
		Signatures:        sigInfos,
		NewCursorValue:    &cursorValue,
		NewCursorSequence: newest.Sequence,
		BlockScanMode:     true,
		WatchedAddresses:  job.WatchedAddresses,
		CreatedAt:         time.Now(),
	}

	select {
	case f.rawBatchCh <- batch:
		metrics.FetcherTxFetched.WithLabelValues(job.Chain.String(), job.Network.String()).Add(float64(len(rawTxs)))
		log.Info("block-scan raw batch sent",
			"start_block", job.StartBlock,
			"end_block", job.EndBlock,
			"tx_count", len(rawTxs),
		)
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

func (f *Fetcher) fetchSignaturesWithRetry(
	ctx context.Context,
	log *slog.Logger,
	job event.FetchJob,
	cursor *string,
	batchSize int,
) ([]chain.SignatureInfo, int, error) {
	ctx, span := tracing.Tracer("fetcher").Start(ctx, "fetcher.fetchSignatures",
		otelTrace.WithAttributes(
			attribute.String("address", job.Address),
			attribute.Int("batch_size", batchSize),
		),
	)
	defer span.End()

	const stage = "fetcher.fetch_signatures"

	currentBatch := batchSize
	attempts := f.effectiveRetryMaxAttempts()

	var lastErr error
	lastDecision := retry.Decision{
		Class:  retry.ClassTerminal,
		Reason: "unset",
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		sigs, err := f.fetchNewSignatures(ctx, job.Address, cursor, currentBatch, job.FetchCutoffSeq)
		f.recordRPCResult(job.Chain.String(), job.Network.String(), err != nil)
		if err == nil {
			span.SetAttributes(attribute.Int("signatures_found", len(sigs)))
			return sigs, currentBatch, nil
		}
		lastErr = err
		lastDecision = retry.Classify(err)

		if ctx.Err() != nil {
			span.RecordError(ctx.Err())
			span.SetStatus(codes.Error, ctx.Err().Error())
			return nil, currentBatch, ctx.Err()
		}
		if !lastDecision.IsTransient() {
			termErr := fmt.Errorf("terminal_failure stage=%s attempt=%d reason=%s: %w", stage, attempt, lastDecision.Reason, err)
			span.RecordError(termErr)
			span.SetStatus(codes.Error, termErr.Error())
			return nil, currentBatch, termErr
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
			span.RecordError(sleepErr)
			span.SetStatus(codes.Error, sleepErr.Error())
			return nil, currentBatch, sleepErr
		}
	}

	exhaustedErr := fmt.Errorf("transient_recovery_exhausted stage=%s attempts=%d reason=%s: %w", stage, attempts, lastDecision.Reason, lastErr)
	span.RecordError(exhaustedErr)
	span.SetStatus(codes.Error, exhaustedErr.Error())
	return nil, currentBatch, exhaustedErr
}

func (f *Fetcher) fetchNewSignatures(
	ctx context.Context,
	address string,
	cursor *string,
	batchSize int,
	cutoffSeq int64,
) ([]chain.SignatureInfo, error) {
	if cutoffSeq > 0 {
		if adapter, ok := f.adapter.(cutoffAwareAdapter); ok {
			return adapter.FetchNewSignaturesWithCutoff(ctx, address, cursor, batchSize, cutoffSeq)
		}
	}
	return f.adapter.FetchNewSignatures(ctx, address, cursor, batchSize)
}

func (f *Fetcher) fetchTransactionsWithRetry(
	ctx context.Context,
	log *slog.Logger,
	job event.FetchJob,
	sigs []chain.SignatureInfo,
) ([]chain.SignatureInfo, []json.RawMessage, int, error) {
	ctx, span := tracing.Tracer("fetcher").Start(ctx, "fetcher.fetchTransactions",
		otelTrace.WithAttributes(
			attribute.String("address", job.Address),
			attribute.Int("signature_count", len(sigs)),
		),
	)
	defer span.End()

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
		f.recordRPCResult(job.Chain.String(), job.Network.String(), err != nil)
		if err == nil {
			span.SetAttributes(attribute.Int("transactions_fetched", len(selected)))
			return selected, rawTxs, currentBatch, nil
		}
		lastErr = err
		lastDecision = retry.Classify(err)

		if ctx.Err() != nil {
			span.RecordError(ctx.Err())
			span.SetStatus(codes.Error, ctx.Err().Error())
			return nil, nil, currentBatch, ctx.Err()
		}
		if !lastDecision.IsTransient() {
			termErr := fmt.Errorf("terminal_failure stage=%s attempt=%d reason=%s: %w", stage, attempt, lastDecision.Reason, err)
			span.RecordError(termErr)
			span.SetStatus(codes.Error, termErr.Error())
			return nil, nil, currentBatch, termErr
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
			span.RecordError(sleepErr)
			span.SetStatus(codes.Error, sleepErr.Error())
			return nil, nil, currentBatch, sleepErr
		}
	}

	exhaustedErr := fmt.Errorf("transient_recovery_exhausted stage=%s attempts=%d reason=%s: %w", stage, attempts, lastDecision.Reason, lastErr)
	span.RecordError(exhaustedErr)
	span.SetStatus(codes.Error, exhaustedErr.Error())
	return nil, nil, currentBatch, exhaustedErr
}

func (f *Fetcher) recordRPCResult(chain, network string, isError bool) {
	if f.autoTuneSignals == nil {
		return
	}
	f.autoTuneSignals.RecordRPCResult(chain, network, isError)
}

func (f *Fetcher) resolveBatchSize(chain model.Chain, network model.Network, address string, hardCap int) int {
	if hardCap <= 0 {
		hardCap = 1
	}

	key := f.batchStateKey(chain, network, address)
	size, ok := f.batchSizeByAddress.Get(key)
	if !ok || size <= 0 {
		f.batchSizeByAddress.Put(key, hardCap)
		return hardCap
	}
	if size > hardCap {
		size = hardCap
		f.batchSizeByAddress.Put(key, size)
	}
	if size < f.effectiveAdaptiveMinBatch() {
		size = f.effectiveAdaptiveMinBatch()
		f.batchSizeByAddress.Put(key, size)
	}
	return size
}

func (f *Fetcher) setAdaptiveBatchSize(chain model.Chain, network model.Network, address string, size int) {
	if size <= 0 {
		return
	}
	if size < f.effectiveAdaptiveMinBatch() {
		size = f.effectiveAdaptiveMinBatch()
	}
	f.batchSizeByAddress.Put(f.batchStateKey(chain, network, address), size)
}

func (f *Fetcher) updateAdaptiveBatchSize(
	chain model.Chain,
	network model.Network,
	address string,
	hardCap,
	requested,
	usedSigBatch,
	usedTxBatch,
	selectedCount int,
) {
	usedBatch := usedSigBatch
	if usedTxBatch < usedBatch {
		usedBatch = usedTxBatch
	}
	if usedBatch <= 0 {
		return
	}

	if usedBatch < requested {
		f.setAdaptiveBatchSize(chain, network, address, usedBatch)
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
		f.setAdaptiveBatchSize(chain, network, address, next)
	}
}

func (f *Fetcher) batchStateKey(chain model.Chain, network model.Network, address string) string {
	return string(chain) + "|" + string(network) + "|" + canonicalizeWatchedAddressIdentity(chain, address)
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
			delay = max
			break
		}
		delay *= 2
	}
	if delay > max {
		delay = max
	}

	// Add 0-25% random jitter to avoid thundering herd.
	if delay > 0 {
		jitter := time.Duration(rand.Int64N(int64(delay) / 4))
		delay += jitter
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

func (f *Fetcher) effectiveBoundaryOverlapLookahead() int {
	if f.boundaryOverlapLookahead <= 0 {
		return boundaryOverlapLookahead
	}
	return f.boundaryOverlapLookahead
}

// filterAndCanonicalizeSignatures combines canonicalization, dedup, cutoff filtering,
// and boundary cursor suppression into a single pass with one sort at the end.
func filterAndCanonicalizeSignatures(
	chainID model.Chain,
	sigs []chain.SignatureInfo,
	cutoffSeq int64,
	cursor *string,
) []chain.SignatureInfo {
	if len(sigs) == 0 {
		return []chain.SignatureInfo{}
	}

	cursorIdentity := ""
	if cursor != nil {
		cursorIdentity = identity.CanonicalSignatureIdentity(chainID, *cursor)
	}

	byIdentity := make(map[string]chain.SignatureInfo, len(sigs))
	for _, sig := range sigs {
		id := identity.CanonicalSignatureIdentity(chainID, sig.Hash)
		if id == "" {
			continue
		}
		// Cutoff filter
		if cutoffSeq > 0 && sig.Sequence > cutoffSeq {
			continue
		}
		// Boundary cursor filter
		if cursorIdentity != "" && id == cursorIdentity {
			continue
		}

		candidate := chain.SignatureInfo{
			Hash:     id,
			Sequence: sig.Sequence,
			Time:     sig.Time,
		}
		existing, ok := byIdentity[id]
		if !ok || shouldReplaceCanonicalSignature(existing, candidate) {
			byIdentity[id] = candidate
		}
	}

	ordered := make([]chain.SignatureInfo, 0, len(byIdentity))
	for _, sig := range byIdentity {
		ordered = append(ordered, sig)
	}
	sortSignatureInfosBySequenceThenHash(ordered)
	return ordered
}

func canonicalizeSignatures(chainID model.Chain, sigs []chain.SignatureInfo) []chain.SignatureInfo {
	if len(sigs) == 0 {
		return []chain.SignatureInfo{}
	}

	byIdentity := make(map[string]chain.SignatureInfo, len(sigs))
	for _, sig := range sigs {
		identity := identity.CanonicalSignatureIdentity(chainID, sig.Hash)
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
	sortSignatureInfosBySequenceThenHash(ordered)

	return ordered
}

func suppressBoundaryCursorSignatures(chainID model.Chain, sigs []chain.SignatureInfo, cursor *string, cursorSequence int64) []chain.SignatureInfo {
	_ = cursorSequence
	if len(sigs) == 0 || cursor == nil {
		return sigs
	}
	cursorIdentity := identity.CanonicalSignatureIdentity(chainID, *cursor)
	if cursorIdentity == "" {
		return sigs
	}

	filtered := make([]chain.SignatureInfo, 0, len(sigs))
	for _, sig := range sigs {
		if identity.CanonicalSignatureIdentity(chainID, sig.Hash) == cursorIdentity {
			continue
		}
		filtered = append(filtered, sig)
	}
	return filtered
}

func suppressPostCutoffSignatures(sigs []chain.SignatureInfo, cutoffSeq int64) []chain.SignatureInfo {
	if len(sigs) == 0 || cutoffSeq <= 0 {
		return sigs
	}

	filtered := make([]chain.SignatureInfo, 0, len(sigs))
	for _, sig := range sigs {
		if sig.Sequence > cutoffSeq {
			continue
		}
		filtered = append(filtered, sig)
	}
	return filtered
}

func suppressPreCursorSequenceCarryover(
	sigs []chain.SignatureInfo,
	previousCursorSequence int64,
	requestedBatch int,
) []chain.SignatureInfo {
	// Keep rollback candidates intact when no signatures reach the prior cursor sequence.
	if len(sigs) == 0 || previousCursorSequence <= 0 {
		return sigs
	}

	// Sort in-place: callers reassign sigs to return value, so original order is not needed.
	sortSignatureInfosBySequenceThenHash(sigs)

	if requestedBatch <= 0 {
		requestedBatch = 1
	}

	cursorHits := make([]chain.SignatureInfo, 0, len(sigs))
	cursorMisses := make([]chain.SignatureInfo, 0, len(sigs))
	for _, sig := range sigs {
		if sig.Sequence < previousCursorSequence {
			continue
		}
		if sig.Sequence == previousCursorSequence {
			cursorHits = append(cursorHits, sig)
			continue
		}
		cursorMisses = append(cursorMisses, sig)
	}

	if len(cursorHits) == 0 && len(cursorMisses) == 0 {
		return sigs
	}

	// If we are overflowing the requested batch, prefer newest signatures and
	// deterministically retain only enough cursor-sequence entries to fill the window.
	if len(cursorMisses) >= requestedBatch {
		return cursorMisses[:requestedBatch]
	}
	if len(cursorMisses)+len(cursorHits) <= requestedBatch {
		cursorMisses = append(cursorMisses, cursorHits...)
		sortSignatureInfosBySequenceThenHash(cursorMisses)
		return cursorMisses
	}

	keepAtCursorCount := requestedBatch - len(cursorMisses)
	if keepAtCursorCount > 0 && len(cursorHits) > keepAtCursorCount {
		selectedCursorHits := append([]chain.SignatureInfo(nil), cursorHits...)
		sort.Slice(selectedCursorHits, func(i, j int) bool {
			return selectedCursorHits[i].Hash > selectedCursorHits[j].Hash
		})
		selectedCursorHits = selectedCursorHits[:keepAtCursorCount]
		cursorHits = selectedCursorHits
	}

	composite := append(append(make([]chain.SignatureInfo, 0, len(cursorMisses)+len(cursorHits)), cursorMisses...), cursorHits...)
	sortSignatureInfosBySequenceThenHash(composite)
	return composite
}

func sortSignatureInfosBySequenceThenHash(sigs []chain.SignatureInfo) {
	sort.Slice(sigs, func(i, j int) bool {
		if sigs[i].Sequence != sigs[j].Sequence {
			return sigs[i].Sequence < sigs[j].Sequence
		}
		return sigs[i].Hash < sigs[j].Hash
	})
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

func canonicalizeWatchedAddressIdentity(chainID model.Chain, address string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return ""
	}
	if identity.IsEVMChain(chainID) {
		withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
		if withoutPrefix == "" {
			return ""
		}
		if identity.IsHexString(withoutPrefix) {
			return "0x" + strings.ToLower(withoutPrefix)
		}
		if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
			return "0x" + strings.ToLower(withoutPrefix)
		}
	}

	return trimmed
}

