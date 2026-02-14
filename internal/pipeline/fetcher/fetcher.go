package fetcher

import (
	"context"
	"log/slog"
	"sync"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/domain/event"
)

// Fetcher consumes FetchJobs, calls ChainAdapter, and produces RawBatches.
type Fetcher struct {
	adapter     chain.ChainAdapter
	jobCh       <-chan event.FetchJob
	rawBatchCh  chan<- event.RawBatch
	workerCount int
	logger      *slog.Logger
}

func New(
	adapter chain.ChainAdapter,
	jobCh <-chan event.FetchJob,
	rawBatchCh chan<- event.RawBatch,
	workerCount int,
	logger *slog.Logger,
) *Fetcher {
	return &Fetcher{
		adapter:     adapter,
		jobCh:       jobCh,
		rawBatchCh:  rawBatchCh,
		workerCount: workerCount,
		logger:      logger.With("component", "fetcher"),
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
			}
		}
	}
}

func (f *Fetcher) processJob(ctx context.Context, log *slog.Logger, job event.FetchJob) error {
	// 1. Fetch new signatures
	sigs, err := f.adapter.FetchNewSignatures(ctx, job.Address, job.CursorValue, job.BatchSize)
	if err != nil {
		return err
	}

	if len(sigs) == 0 {
		log.Debug("no new signatures", "address", job.Address)
		return nil
	}

	// 2. Extract signature hashes
	sigHashes := make([]string, len(sigs))
	sigInfos := make([]event.SignatureInfo, len(sigs))
	for i, sig := range sigs {
		sigHashes[i] = sig.Hash
		sigInfos[i] = event.SignatureInfo{
			Hash:     sig.Hash,
			Sequence: sig.Sequence,
		}
	}

	// 3. Fetch raw transactions
	rawTxs, err := f.adapter.FetchTransactions(ctx, sigHashes)
	if err != nil {
		return err
	}

	// 4. Determine new cursor (newest = last in oldest-first list)
	newest := sigs[len(sigs)-1]
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
		)
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}
