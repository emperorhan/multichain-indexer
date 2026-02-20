package solana

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/ratelimit"
	"github.com/emperorhan/multichain-indexer/internal/chain/solana/rpc"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
)

const (
	maxPageSize      = 1000
	maxConcurrentTxs = 10
)

type Adapter struct {
	client           rpc.RPCClient
	logger           *slog.Logger
	maxPageSize      int
	maxConcurrentTxs int
	network          string
}

type AdapterOption func(*Adapter)

func WithMaxPageSize(n int) AdapterOption {
	return func(a *Adapter) { a.maxPageSize = n }
}

func WithMaxConcurrentTxs(n int) AdapterOption {
	return func(a *Adapter) { a.maxConcurrentTxs = n }
}

func WithNetwork(network string) AdapterOption {
	return func(a *Adapter) { a.network = network }
}

var _ chain.ChainAdapter = (*Adapter)(nil)
var _ chain.BlockScanAdapter = (*Adapter)(nil)

func NewAdapter(rpcURL string, logger *slog.Logger, opts ...AdapterOption) *Adapter {
	a := &Adapter{
		client:           rpc.NewClient(rpcURL, logger),
		logger:           logger.With("chain", "solana"),
		maxPageSize:      maxPageSize,
		maxConcurrentTxs: maxConcurrentTxs,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return a
}

// SetRateLimiter applies a rate limiter to the underlying RPC client.
func (a *Adapter) SetRateLimiter(l *ratelimit.Limiter) {
	if c, ok := a.client.(*rpc.Client); ok {
		c.SetRateLimiter(l)
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
		if pageSize > a.maxPageSize {
			pageSize = a.maxPageSize
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

// slotResult holds the result of scanning a single slot.
type slotResult struct {
	slot    int64
	sigs    []chain.SignatureInfo
	skipped bool
	err     error
}

// ScanBlocks scans a range of slots and returns signatures for transactions
// that involve any of the watched addresses. Slots are fetched concurrently
// using a semaphore to limit parallelism.
func (a *Adapter) ScanBlocks(ctx context.Context, startSlot, endSlot int64, watchedAddresses []string) ([]chain.SignatureInfo, error) {
	if len(watchedAddresses) == 0 || startSlot > endSlot {
		return nil, nil
	}

	watchedSet := make(map[string]struct{}, len(watchedAddresses))
	for _, addr := range watchedAddresses {
		watchedSet[addr] = struct{}{}
	}

	numSlots := int(endSlot - startSlot + 1)
	sem := make(chan struct{}, a.maxConcurrentTxs)
	resultCh := make(chan slotResult, numSlots)
	var wg sync.WaitGroup

	for slot := startSlot; slot <= endSlot; slot++ {
		wg.Add(1)
		go func(s int64) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			block, err := a.client.GetBlock(ctx, s, nil)
			if err != nil {
				resultCh <- slotResult{slot: s, err: fmt.Errorf("scan slot %d: %w", s, err)}
				return
			}
			if block == nil {
				resultCh <- slotResult{slot: s, skipped: true}
				return
			}

			var blockTime *time.Time
			if block.BlockTime != nil {
				bt := time.Unix(*block.BlockTime, 0)
				blockTime = &bt
			}

			var sigs []chain.SignatureInfo
			for _, btx := range block.Transactions {
				sig := extractSignature(btx)
				if sig == "" {
					continue
				}

				accounts := extractAccountKeys(btx)
				owners := extractTokenBalanceOwners(btx.Meta)
				allAddrs := append(accounts, owners...)

				if anyAddressMatches(allAddrs, watchedSet) {
					sigs = append(sigs, chain.SignatureInfo{
						Hash:     sig,
						Sequence: s,
						Time:     blockTime,
					})
				}
			}
			resultCh <- slotResult{slot: s, sigs: sigs}
		}(slot)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results
	var allResults []slotResult
	for r := range resultCh {
		if r.err != nil {
			return nil, r.err
		}
		allResults = append(allResults, r)
	}

	// Sort by slot to maintain deterministic order
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].slot < allResults[j].slot
	})

	var results []chain.SignatureInfo
	for _, r := range allResults {
		if r.skipped {
			metrics.SolanaBlockScanSkippedSlots.WithLabelValues(a.network).Inc()
			continue
		}
		results = append(results, r.sigs...)
	}

	a.logger.Info("scanned blocks",
		"start_slot", startSlot,
		"end_slot", endSlot,
		"watched_count", len(watchedAddresses),
		"matches", len(results),
	)

	return results, nil
}

// extractSignature parses the first signature from a block transaction.
func extractSignature(btx rpc.BlockTransaction) string {
	var parsed struct {
		Signatures []string `json:"signatures"`
	}
	if err := json.Unmarshal(btx.Transaction, &parsed); err != nil {
		return ""
	}
	if len(parsed.Signatures) == 0 {
		return ""
	}
	return parsed.Signatures[0]
}

// extractAccountKeys parses account keys from a block transaction.
func extractAccountKeys(btx rpc.BlockTransaction) []string {
	var parsed struct {
		Message struct {
			AccountKeys []json.RawMessage `json:"accountKeys"`
		} `json:"message"`
	}
	if err := json.Unmarshal(btx.Transaction, &parsed); err != nil {
		return nil
	}

	keys := make([]string, 0, len(parsed.Message.AccountKeys))
	for _, raw := range parsed.Message.AccountKeys {
		// jsonParsed format: either a string or {"pubkey":"...", "signer":..., "writable":...}
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			keys = append(keys, str)
			continue
		}
		var obj struct {
			Pubkey string `json:"pubkey"`
		}
		if err := json.Unmarshal(raw, &obj); err == nil && obj.Pubkey != "" {
			keys = append(keys, obj.Pubkey)
		}
	}
	return keys
}

// extractTokenBalanceOwners extracts unique owner addresses from pre/postTokenBalances in tx meta.
func extractTokenBalanceOwners(meta json.RawMessage) []string {
	if len(meta) == 0 {
		return nil
	}

	var parsed struct {
		PreTokenBalances  []struct{ Owner string `json:"owner"` } `json:"preTokenBalances"`
		PostTokenBalances []struct{ Owner string `json:"owner"` } `json:"postTokenBalances"`
	}
	if err := json.Unmarshal(meta, &parsed); err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	var owners []string
	for _, tb := range parsed.PreTokenBalances {
		if tb.Owner != "" {
			if _, exists := seen[tb.Owner]; !exists {
				seen[tb.Owner] = struct{}{}
				owners = append(owners, tb.Owner)
			}
		}
	}
	for _, tb := range parsed.PostTokenBalances {
		if tb.Owner != "" {
			if _, exists := seen[tb.Owner]; !exists {
				seen[tb.Owner] = struct{}{}
				owners = append(owners, tb.Owner)
			}
		}
	}
	return owners
}

// anyAddressMatches checks if any address is in the watched set.
func anyAddressMatches(addrs []string, watchedSet map[string]struct{}) bool {
	for _, addr := range addrs {
		if _, ok := watchedSet[addr]; ok {
			return true
		}
	}
	return false
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

	sem := make(chan struct{}, a.maxConcurrentTxs)
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
