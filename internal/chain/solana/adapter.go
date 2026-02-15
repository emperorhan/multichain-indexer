package solana

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/solana/rpc"
)

const (
	maxPageSize      = 1000
	maxConcurrentTxs = 10
)

type Adapter struct {
	client rpc.RPCClient
	logger *slog.Logger
}

var _ chain.ChainAdapter = (*Adapter)(nil)

func NewAdapter(rpcURL string, logger *slog.Logger) *Adapter {
	return &Adapter{
		client: rpc.NewClient(rpcURL, logger),
		logger: logger.With("chain", "solana"),
	}
}

func (a *Adapter) Chain() string {
	return "solana"
}

func (a *Adapter) GetHeadSequence(ctx context.Context) (int64, error) {
	return a.client.GetSlot(ctx, "confirmed")
}

// FetchNewSignatures fetches signatures since the cursor, returning oldest-first.
// Uses pagination to collect up to batchSize signatures.
func (a *Adapter) FetchNewSignatures(ctx context.Context, address string, cursor *string, batchSize int) ([]chain.SignatureInfo, error) {
	return a.FetchNewSignaturesWithCutoff(ctx, address, cursor, batchSize, 0)
}

// FetchNewSignaturesWithCutoff fetches signatures in a closed range where sequence <= cutoffSeq.
// cutoffSeq <= 0 disables the upper bound and preserves legacy behavior.
func (a *Adapter) FetchNewSignaturesWithCutoff(ctx context.Context, address string, cursor *string, batchSize int, cutoffSeq int64) ([]chain.SignatureInfo, error) {
	var allSigs []rpc.SignatureInfo
	var before string

	// Collect signatures page by page (newest-first from RPC).
	// If we have a cursor (last processed signature), use it as the "until" param
	// so we only get signatures newer than the cursor.
	until := ""
	if cursor != nil {
		until = *cursor
	}

	remaining := batchSize
	for remaining > 0 {
		pageSize := remaining
		if pageSize > maxPageSize {
			pageSize = maxPageSize
		}

		opts := &rpc.GetSignaturesOpts{
			Limit: pageSize,
			Until: until,
		}
		if before != "" {
			opts.Before = before
		}

		sigs, err := a.client.GetSignaturesForAddress(ctx, address, opts)
		if err != nil {
			return nil, fmt.Errorf("fetch signatures page: %w", err)
		}

		if len(sigs) == 0 {
			break
		}

		accepted := 0
		for _, sig := range sigs {
			if cutoffSeq > 0 && sig.Slot > cutoffSeq {
				continue
			}
			allSigs = append(allSigs, sig)
			accepted++
		}
		remaining -= accepted
		if remaining <= 0 {
			break
		}

		// If we got fewer than requested, we've reached the end
		if len(sigs) < pageSize {
			break
		}

		// Set before to the oldest signature in this page for next iteration
		before = sigs[len(sigs)-1].Signature
	}

	// Reverse to oldest-first order
	result := make([]chain.SignatureInfo, len(allSigs))
	for i, sig := range allSigs {
		var t *time.Time
		if sig.BlockTime != nil {
			bt := time.Unix(*sig.BlockTime, 0)
			t = &bt
		}
		result[len(allSigs)-1-i] = chain.SignatureInfo{
			Hash:     sig.Signature,
			Sequence: sig.Slot,
			Time:     t,
		}
	}

	a.logger.Info("fetched signatures",
		"address", address,
		"count", len(result),
		"cursor", cursor,
		"cutoff_seq", cutoffSeq,
	)

	return result, nil
}

// FetchTransactions fetches raw transaction data for given signatures in parallel.
func (a *Adapter) FetchTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error) {
	if len(signatures) == 0 {
		return []json.RawMessage{}, nil
	}

	if batchClient, ok := a.client.(interface {
		GetTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error)
	}); ok {
		results, err := batchClient.GetTransactions(ctx, signatures)
		if err == nil {
			a.logger.Info("fetched transactions (batch)", "count", len(results))
			return results, nil
		}
		a.logger.Warn("batch transaction fetch failed, falling back", "error", err)
	}

	results := make([]json.RawMessage, len(signatures))
	var mu sync.Mutex
	var firstErr error

	sem := make(chan struct{}, maxConcurrentTxs)
	var wg sync.WaitGroup

	for i, sig := range signatures {
		wg.Add(1)
		go func(idx int, signature string) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			result, err := a.client.GetTransaction(ctx, signature)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("fetch tx %s: %w", signature, err)
				}
				mu.Unlock()
				return
			}

			mu.Lock()
			results[idx] = result
			mu.Unlock()
		}(i, sig)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	a.logger.Info("fetched transactions", "count", len(results))
	return results, nil
}
