package solana

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/ratelimit"
	"github.com/emperorhan/multichain-indexer/internal/chain/solana/rpc"
	"github.com/emperorhan/multichain-indexer/internal/metrics"
	"golang.org/x/sync/errgroup"
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
	allResults := make([]slotResult, numSlots)

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(a.maxConcurrentTxs)

	for slot := startSlot; slot <= endSlot; slot++ {
		s := slot
		idx := int(s - startSlot)
		g.Go(func() error {
			block, err := a.client.GetBlock(gCtx, s, nil)
			if err != nil {
				return fmt.Errorf("scan slot %d: %w", s, err)
			}
			if block == nil {
				allResults[idx] = slotResult{slot: s, skipped: true}
				return nil
			}

			var blockTime *time.Time
			if block.BlockTime != nil {
				bt := time.Unix(*block.BlockTime, 0)
				blockTime = &bt
			}

			var sigs []chain.SignatureInfo
			for _, btx := range block.Transactions {
				sig, accounts := extractSignatureAndKeys(btx)
				if sig == "" {
					continue
				}

				// Check account keys first (cheap). Only extract token
				// balance owners (expensive JSON parse) when account keys
				// did not already produce a match.
				matched := anyAddressMatches(accounts, watchedSet)
				if !matched {
					owners := extractTokenBalanceOwners(btx.Meta)
					matched = anyAddressMatches(owners, watchedSet)
				}

				if matched {
					sigs = append(sigs, chain.SignatureInfo{
						Hash:     sig,
						Sequence: s,
						Time:     blockTime,
					})
				}
			}
			allResults[idx] = slotResult{slot: s, sigs: sigs}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

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

// blockTxParsed is a combined struct for extracting both signature and account
// keys from a block transaction in a single JSON parse.
type blockTxParsed struct {
	Signatures []string `json:"signatures"`
	Message    struct {
		AccountKeys []json.RawMessage `json:"accountKeys"`
	} `json:"message"`
}

// extractSignatureAndKeys parses a block transaction once to extract both the
// first signature and all account keys, avoiding duplicate JSON unmarshalling.
// Uses single-pass parsing for account keys: checks the first byte of each raw
// JSON element to determine whether it's a string or object.
func extractSignatureAndKeys(btx rpc.BlockTransaction) (string, []string) {
	var parsed blockTxParsed
	if err := json.Unmarshal(btx.Transaction, &parsed); err != nil {
		return "", nil
	}

	// Extract signature.
	sig := ""
	if len(parsed.Signatures) > 0 {
		sig = parsed.Signatures[0]
	}

	// Extract account keys.
	keys := make([]string, 0, len(parsed.Message.AccountKeys))
	for _, raw := range parsed.Message.AccountKeys {
		if len(raw) == 0 {
			continue
		}
		// Fast path: check first byte to determine format
		// jsonParsed format: either a string or {"pubkey":"...", "signer":..., "writable":...}
		switch raw[0] {
		case '"':
			// String format — direct unmarshal
			var str string
			if err := json.Unmarshal(raw, &str); err == nil {
				keys = append(keys, str)
			}
		case '{':
			// Object format
			var obj struct {
				Pubkey string `json:"pubkey"`
			}
			if err := json.Unmarshal(raw, &obj); err == nil && obj.Pubkey != "" {
				keys = append(keys, obj.Pubkey)
			}
		}
	}

	return sig, keys
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

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(a.maxConcurrentTxs)

	for i, sig := range signatures {
		idx, signature := i, sig
		g.Go(func() error {
			result, err := a.client.GetTransaction(gCtx, signature)
			if err != nil {
				return fmt.Errorf("fetch tx %s: %w", signature, err)
			}
			results[idx] = result
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	a.logger.Info("fetched transactions", "count", len(results))
	return results, nil
}
