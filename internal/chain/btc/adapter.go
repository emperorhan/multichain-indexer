package btc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/btc/rpc"
	"github.com/emperorhan/multichain-indexer/internal/chain/ratelimit"
)

const (
	maxInitialLookbackBlocks = 200
	blockVerbosityTxObjects  = 2
	blockVerbosityPrevout   = 3 // includes vin.prevout (Bitcoin Core 25.0+)
	btcChainName            = "btc"
	satoshiPerBTC            = 100_000_000
)

type Adapter struct {
	client                   rpc.RPCClient
	logger                   *slog.Logger
	maxInitialLookbackBlocks int64
	// verbosity3Supported caches whether the node supports verbosity=3.
	// 0 = unknown, 1 = supported, -1 = unsupported.
	verbosity3Supported int32

	// headBlockCache caches the latest block count to avoid redundant RPC calls.
	headBlockCache    int64
	headBlockCacheAt  time.Time
	headBlockCacheTTL time.Duration
}

type AdapterOption func(*Adapter)

func WithMaxInitialLookbackBlocks(n int) AdapterOption {
	return func(a *Adapter) { a.maxInitialLookbackBlocks = int64(n) }
}

type candidateSignature struct {
	hash      string
	blockNum  int64
	txIndex   int
	blockTime *time.Time
}

type resolvedPrevout struct {
	found    bool
	address  string
	valueSat int64
}

type txEnvelope struct {
	Chain         string          `json:"chain"`
	Txid          string          `json:"txid"`
	BlockHeight   int64           `json:"block_height"`
	BlockHash     string          `json:"block_hash,omitempty"`
	BlockTime     int64           `json:"block_time,omitempty"`
	Confirmations int64           `json:"confirmations,omitempty"`
	Vin           []txEnvelopeIn  `json:"vin"`
	Vout          []txEnvelopeOut `json:"vout"`
	FeeSat        string          `json:"fee_sat"`
	FeePayer      string          `json:"fee_payer,omitempty"`
}

type txEnvelopeIn struct {
	Index    int    `json:"index"`
	Txid     string `json:"txid,omitempty"`
	Vout     int    `json:"vout,omitempty"`
	Address  string `json:"address,omitempty"`
	ValueSat string `json:"value_sat,omitempty"`
	Coinbase bool   `json:"coinbase,omitempty"`
}

type txEnvelopeOut struct {
	Index    int    `json:"index"`
	Address  string `json:"address,omitempty"`
	ValueSat string `json:"value_sat"`
}

const defaultBTCFinalityConfirmations = 6

var _ chain.ChainAdapter = (*Adapter)(nil)
var _ chain.ReorgAwareAdapter = (*Adapter)(nil)
var _ chain.BlockScanAdapter = (*Adapter)(nil)

func NewAdapter(rpcURL string, logger *slog.Logger, opts ...AdapterOption) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	a := &Adapter{
		client:                   rpc.NewClient(rpcURL, logger),
		logger:                   logger.With("chain", "btc"),
		maxInitialLookbackBlocks: maxInitialLookbackBlocks,
		headBlockCacheTTL:        5 * time.Second,
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
	return btcChainName
}

// getBlockWithVerbosity fetches a block, trying verbosity=3 first for inline
// prevout data. Caches the result so older nodes only try verbosity=2.
func (a *Adapter) getBlockWithVerbosity(ctx context.Context, blockHash string) (*rpc.Block, error) {
	if a.verbosity3Supported != -1 {
		block, err := a.client.GetBlock(ctx, blockHash, blockVerbosityPrevout)
		if err == nil {
			a.verbosity3Supported = 1
			return block, nil
		}
		// If we already know it's supported, propagate the error.
		if a.verbosity3Supported == 1 {
			return nil, err
		}
		// First failure: mark as unsupported and fallback.
		a.verbosity3Supported = -1
		a.logger.Info("verbosity=3 not supported, falling back to verbosity=2")
	}
	return a.client.GetBlock(ctx, blockHash, blockVerbosityTxObjects)
}

// getCachedHeadBlock returns the latest block count, using a short TTL cache
// to avoid redundant RPC calls when multiple methods need the head block.
func (a *Adapter) getCachedHeadBlock(ctx context.Context) (int64, error) {
	if time.Since(a.headBlockCacheAt) < a.headBlockCacheTTL {
		return a.headBlockCache, nil
	}
	head, err := a.client.GetBlockCount(ctx)
	if err != nil {
		return 0, err
	}
	a.headBlockCache = head
	a.headBlockCacheAt = time.Now()
	return head, nil
}

func (a *Adapter) GetHeadSequence(ctx context.Context) (int64, error) {
	return a.getCachedHeadBlock(ctx)
}

func (a *Adapter) GetBlockHash(ctx context.Context, blockNumber int64) (string, string, error) {
	hash, err := a.client.GetBlockHash(ctx, blockNumber)
	if err != nil {
		return "", "", fmt.Errorf("get block hash %d: %w", blockNumber, err)
	}
	header, err := a.client.GetBlockHeader(ctx, hash)
	if err != nil {
		return hash, "", fmt.Errorf("get block header %s: %w", hash, err)
	}
	if header == nil {
		return hash, "", nil
	}
	return hash, header.PreviousBlockHash, nil
}

func (a *Adapter) GetFinalizedBlockNumber(ctx context.Context) (int64, error) {
	head, err := a.getCachedHeadBlock(ctx)
	if err != nil {
		return 0, fmt.Errorf("get block count: %w", err)
	}
	finalized := head - defaultBTCFinalityConfirmations
	if finalized < 0 {
		finalized = 0
	}
	return finalized, nil
}

func (a *Adapter) FetchNewSignatures(ctx context.Context, address string, cursor *string, batchSize int) ([]chain.SignatureInfo, error) {
	return a.FetchNewSignaturesWithCutoff(ctx, address, cursor, batchSize, 0)
}

func (a *Adapter) FetchNewSignaturesWithCutoff(ctx context.Context, address string, cursor *string, batchSize int, cutoffSeq int64) ([]chain.SignatureInfo, error) {
	if batchSize <= 0 {
		return []chain.SignatureInfo{}, nil
	}

	head := cutoffSeq
	if head <= 0 {
		var err error
		head, err = a.getCachedHeadBlock(ctx)
		if err != nil {
			return nil, fmt.Errorf("get head block: %w", err)
		}
	}

	startBlock := head - (a.maxInitialLookbackBlocks - 1)
	if startBlock < 0 {
		startBlock = 0
	}

	cursorBlock := int64(-1)
	cursorTxIndex := -1
	if cursor != nil && strings.TrimSpace(*cursor) != "" {
		var err error
		cursorBlock, cursorTxIndex, err = a.resolveCursorPosition(ctx, *cursor)
		if err != nil {
			return nil, fmt.Errorf("resolve cursor tx %s: %w", *cursor, err)
		}
		if cursorBlock >= 0 && cursorBlock < startBlock {
			startBlock = cursorBlock
		}
	}

	watchedAddress := normalizeAddressIdentity(address)
	candidates := make(map[string]candidateSignature, batchSize)
	prevoutCache := map[string]resolvedPrevout{}

	upsertCandidate := func(hash string, blockNum int64, txIndex int, blockTime *time.Time) {
		if hash == "" {
			return
		}
		existing, exists := candidates[hash]
		if !exists {
			candidates[hash] = candidateSignature{
				hash:      hash,
				blockNum:  blockNum,
				txIndex:   txIndex,
				blockTime: blockTime,
			}
			return
		}
		if existing.blockNum > blockNum || (existing.blockNum == blockNum && existing.txIndex > txIndex) {
			existing.blockNum = blockNum
			existing.txIndex = txIndex
		}
		if existing.blockTime == nil && blockTime != nil {
			existing.blockTime = blockTime
		}
		candidates[hash] = existing
	}

	// Batch-fetch all block hashes in a single RPC call.
	heights := make([]int64, 0, head-startBlock+1)
	for n := startBlock; n <= head; n++ {
		heights = append(heights, n)
	}
	blockHashes, err := a.client.GetBlockHashes(ctx, heights)
	if err != nil {
		return nil, fmt.Errorf("batch get block hashes %d-%d: %w", startBlock, head, err)
	}

	for idx, blockHash := range blockHashes {
		blockNum := heights[idx]
		block, err := a.getBlockWithVerbosity(ctx, blockHash)
		if err != nil {
			return nil, fmt.Errorf("get block %d: %w", blockNum, err)
		}
		if block == nil {
			continue
		}

		height := blockNum
		if block.Height > 0 || blockNum == 0 {
			height = block.Height
		}
		blockTime := unixTimePtr(block.Time)

		for txIndex, tx := range block.Tx {
			if tx == nil {
				continue
			}
			txid := canonicalTxID(tx.Txid)
			if txid == "" {
				continue
			}
			if !isAfterCursor(height, txIndex, cursorBlock, cursorTxIndex) {
				continue
			}
			touches, err := a.txTouchesWatchedAddress(ctx, tx, watchedAddress, prevoutCache)
			if err != nil {
				return nil, fmt.Errorf("inspect tx %s in block %d: %w", txid, height, err)
			}
			if !touches {
				continue
			}
			txTime := txTimePointer(tx, blockTime)
			upsertCandidate(txid, height, txIndex, txTime)
		}
	}

	ordered := make([]candidateSignature, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].blockNum != ordered[j].blockNum {
			return ordered[i].blockNum < ordered[j].blockNum
		}
		if ordered[i].txIndex != ordered[j].txIndex {
			return ordered[i].txIndex < ordered[j].txIndex
		}
		return ordered[i].hash < ordered[j].hash
	})

	signatures := make([]chain.SignatureInfo, 0, batchSize)
	for _, candidate := range ordered {
		signatures = append(signatures, chain.SignatureInfo{
			Hash:     candidate.hash,
			Sequence: candidate.blockNum,
			Time:     candidate.blockTime,
		})
		if len(signatures) >= batchSize {
			break
		}
	}

	a.logger.Info("fetched signatures",
		"address", address,
		"count", len(signatures),
		"cursor", cursor,
		"start_block", startBlock,
		"head_block", head,
		"cutoff_seq", cutoffSeq,
		"candidate_count", len(candidates),
	)

	return signatures, nil
}

// ScanBlocks scans a block range for transactions touching any of the watched addresses.
// Returns all matching signatures in oldest-first order without per-address cursor logic.
func (a *Adapter) ScanBlocks(ctx context.Context, startBlock, endBlock int64, watchedAddresses []string) ([]chain.SignatureInfo, error) {
	if startBlock > endBlock || len(watchedAddresses) == 0 {
		return []chain.SignatureInfo{}, nil
	}

	addrSet := make(map[string]struct{}, len(watchedAddresses))
	for _, addr := range watchedAddresses {
		normalized := normalizeAddressIdentity(addr)
		if normalized != "" {
			addrSet[normalized] = struct{}{}
		}
	}

	candidates := make(map[string]candidateSignature)
	prevoutCache := map[string]resolvedPrevout{}

	// Batch-fetch all block hashes in a single RPC call.
	heights := make([]int64, 0, endBlock-startBlock+1)
	for n := startBlock; n <= endBlock; n++ {
		heights = append(heights, n)
	}
	blockHashes, err := a.client.GetBlockHashes(ctx, heights)
	if err != nil {
		return nil, fmt.Errorf("scan batch get block hashes %d-%d: %w", startBlock, endBlock, err)
	}

	for idx, blockHash := range blockHashes {
		blockNum := heights[idx]
		block, err := a.getBlockWithVerbosity(ctx, blockHash)
		if err != nil {
			return nil, fmt.Errorf("scan get block %d: %w", blockNum, err)
		}
		if block == nil {
			continue
		}

		height := blockNum
		if block.Height > 0 || blockNum == 0 {
			height = block.Height
		}
		blockTime := unixTimePtr(block.Time)

		for txIndex, tx := range block.Tx {
			if tx == nil {
				continue
			}
			txid := canonicalTxID(tx.Txid)
			if txid == "" {
				continue
			}

			touches := false
			// Check outputs
			for _, vout := range tx.Vout {
				if vout == nil {
					continue
				}
				for _, address := range allOutputAddresses(vout.ScriptPubKey) {
					if _, ok := addrSet[normalizeAddressIdentity(address)]; ok {
						touches = true
						break
					}
				}
				if touches {
					break
				}
			}

			// Check inputs
			if !touches {
				for _, vin := range tx.Vin {
					if vin == nil || strings.TrimSpace(vin.Coinbase) != "" {
						continue
					}
					prevout, ok, err := a.resolveInputPrevout(ctx, vin, prevoutCache)
					if err != nil {
						return nil, fmt.Errorf("scan resolve prevout tx=%s: %w", txid, err)
					}
					if ok {
						if _, match := addrSet[normalizeAddressIdentity(prevout.address)]; match {
							touches = true
							break
						}
					}
				}
			}

			if !touches {
				continue
			}

			txTime := txTimePointer(tx, blockTime)
			if _, exists := candidates[txid]; !exists {
				candidates[txid] = candidateSignature{
					hash:      txid,
					blockNum:  height,
					txIndex:   txIndex,
					blockTime: txTime,
				}
			}
		}
	}

	ordered := make([]candidateSignature, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].blockNum != ordered[j].blockNum {
			return ordered[i].blockNum < ordered[j].blockNum
		}
		if ordered[i].txIndex != ordered[j].txIndex {
			return ordered[i].txIndex < ordered[j].txIndex
		}
		return ordered[i].hash < ordered[j].hash
	})

	signatures := make([]chain.SignatureInfo, 0, len(ordered))
	for _, candidate := range ordered {
		signatures = append(signatures, chain.SignatureInfo{
			Hash:     candidate.hash,
			Sequence: candidate.blockNum,
			Time:     candidate.blockTime,
		})
	}

	a.logger.Info("scanned blocks",
		"start_block", startBlock,
		"end_block", endBlock,
		"address_count", len(watchedAddresses),
		"signatures", len(signatures),
	)

	return signatures, nil
}

func (a *Adapter) FetchTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error) {
	if len(signatures) == 0 {
		return []json.RawMessage{}, nil
	}

	// Canonicalize tx IDs.
	txids := make([]string, len(signatures))
	for i, raw := range signatures {
		txids[i] = canonicalTxID(raw)
		if txids[i] == "" {
			return nil, fmt.Errorf("empty tx id in signatures[%d]", i)
		}
	}

	// Batch-fetch all transactions in a single RPC call.
	txs, err := a.client.GetRawTransactionsVerbose(ctx, txids)
	if err != nil {
		return nil, fmt.Errorf("batch fetch transactions: %w", err)
	}

	results := make([]json.RawMessage, len(signatures))
	prevoutCache := map[string]resolvedPrevout{}
	blockCache := map[string]*rpc.Block{}

	// Pre-fetch all unique blocks referenced by the transactions.
	if err := a.prefetchBlocks(ctx, txs, blockCache); err != nil {
		a.logger.Warn("prefetch blocks failed, falling back to on-demand fetch", "err", err)
	}

	// Pre-populate prevoutCache with a single batch RPC call so that the
	// per-vin resolveInputPrevout calls below hit the cache instead of
	// issuing individual RPCs.
	if err := a.prefetchPrevouts(ctx, txs, prevoutCache); err != nil {
		a.logger.Warn("prefetch prevouts failed, falling back to individual lookups", "err", err)
	}

	for i, tx := range txs {
		txid := txids[i]
		if tx == nil {
			return nil, fmt.Errorf("transaction %s not found", txid)
		}

		blockHeight := int64(0)
		blockHash := canonicalTxID(tx.Blockhash)
		if blockHash != "" {
			block := blockCache[blockHash]
			if block == nil {
				block, err = a.client.GetBlock(ctx, blockHash, blockVerbosityTxObjects)
				if err != nil {
					return nil, fmt.Errorf("fetch block %s for tx %s: %w", blockHash, txid, err)
				}
				blockCache[blockHash] = block
			}
			if block != nil {
				blockHeight = block.Height
			}
		}

		totalIn := int64(0)
		totalOut := int64(0)
		inputAddresses := make([]string, 0, len(tx.Vin))

		vins := make([]txEnvelopeIn, 0, len(tx.Vin))
		for vinIndex, vin := range tx.Vin {
			if vin == nil {
				continue
			}
			in := txEnvelopeIn{
				Index: vinIndex,
				Txid:  canonicalTxID(vin.Txid),
				Vout:  vin.Vout,
			}
			if strings.TrimSpace(vin.Coinbase) != "" {
				in.Coinbase = true
				vins = append(vins, in)
				continue
			}

			prevout, ok, err := a.resolveInputPrevout(ctx, vin, prevoutCache)
			if err != nil {
				return nil, fmt.Errorf("resolve input prevout tx=%s vin=%d: %w", txid, vinIndex, err)
			}
			if ok {
				in.Address = normalizeAddressIdentity(prevout.address)
				if in.Address != "" {
					inputAddresses = append(inputAddresses, in.Address)
				}
				in.ValueSat = strconv.FormatInt(prevout.valueSat, 10)
				totalIn += prevout.valueSat
			}
			vins = append(vins, in)
		}

		vouts := make([]txEnvelopeOut, 0, len(tx.Vout))
		for _, vout := range tx.Vout {
			if vout == nil {
				continue
			}
			valueSat, err := parseBTCValueToSatoshi(vout.Value)
			if err != nil {
				return nil, fmt.Errorf("parse tx=%s vout=%d value: %w", txid, vout.N, err)
			}
			totalOut += valueSat
			vouts = append(vouts, txEnvelopeOut{
				Index:    vout.N,
				Address:  firstOutputAddress(vout.ScriptPubKey),
				ValueSat: strconv.FormatInt(valueSat, 10),
			})
		}
		sort.SliceStable(vouts, func(i, j int) bool {
			if vouts[i].Index != vouts[j].Index {
				return vouts[i].Index < vouts[j].Index
			}
			return vouts[i].Address < vouts[j].Address
		})

		feeSat := int64(0)
		if totalIn > 0 && totalIn >= totalOut {
			feeSat = totalIn - totalOut
		}

		envelope := txEnvelope{
			Chain:         "btc",
			Txid:          canonicalTxID(tx.Txid),
			BlockHeight:   blockHeight,
			BlockHash:     blockHash,
			BlockTime:     pickBlockTime(tx),
			Confirmations: tx.Confirmations,
			Vin:           vins,
			Vout:          vouts,
			FeeSat:        strconv.FormatInt(feeSat, 10),
			FeePayer:      deterministicAddress(inputAddresses),
		}
		if envelope.Txid == "" {
			envelope.Txid = txid
		}

		payload, err := json.Marshal(envelope)
		if err != nil {
			return nil, fmt.Errorf("marshal payload %s: %w", txid, err)
		}
		results[i] = payload
	}

	a.logger.Info("fetched transactions (batch)", "count", len(results))
	return results, nil
}

// prefetchPrevouts collects all uncached source txids from the transactions'
// vins and batch-fetches them in a single RPC call. The results are stored
// in the prevoutCache so that subsequent resolveInputPrevout calls hit the
// cache instead of issuing individual RPCs.
func (a *Adapter) prefetchPrevouts(ctx context.Context, txs []*rpc.Transaction, cache map[string]resolvedPrevout) error {
	var missingTxIDs []string
	seen := make(map[string]struct{})
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		for _, vin := range tx.Vin {
			if vin == nil || vin.Prevout != nil {
				continue // has inline prevout (verbosity=3)
			}
			if strings.TrimSpace(vin.Coinbase) != "" {
				continue // coinbase input
			}
			sourceTxID := canonicalTxID(vin.Txid)
			if sourceTxID == "" {
				continue
			}
			cacheKey := sourceTxID + ":" + strconv.Itoa(vin.Vout)
			if _, ok := cache[cacheKey]; ok {
				continue // already cached
			}
			if _, exists := seen[sourceTxID]; exists {
				continue
			}
			seen[sourceTxID] = struct{}{}
			missingTxIDs = append(missingTxIDs, sourceTxID)
		}
	}
	if len(missingTxIDs) == 0 {
		return nil
	}

	prevTxs, err := a.client.GetRawTransactionsVerbose(ctx, missingTxIDs)
	if err != nil {
		return err
	}

	for i, prevTx := range prevTxs {
		if prevTx == nil {
			continue
		}
		txid := canonicalTxID(missingTxIDs[i])
		for _, vout := range prevTx.Vout {
			if vout == nil {
				continue
			}
			valueSat, parseErr := parseBTCValueToSatoshi(vout.Value)
			if parseErr != nil {
				valueSat = 0
			}
			key := txid + ":" + strconv.Itoa(vout.N)
			cache[key] = resolvedPrevout{
				found:    true,
				address:  firstOutputAddress(vout.ScriptPubKey),
				valueSat: valueSat,
			}
		}
	}
	return nil
}

// prefetchBlocks collects all unique block hashes from the transactions and
// fetches them into the blockCache before the main processing loop. This
// avoids interleaving block and transaction RPC calls.
func (a *Adapter) prefetchBlocks(ctx context.Context, txs []*rpc.Transaction, cache map[string]*rpc.Block) error {
	seen := make(map[string]struct{})
	var hashes []string
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		blockHash := canonicalTxID(tx.Blockhash)
		if blockHash == "" {
			continue
		}
		if _, exists := seen[blockHash]; exists {
			continue
		}
		seen[blockHash] = struct{}{}
		hashes = append(hashes, blockHash)
	}
	if len(hashes) == 0 {
		return nil
	}

	blocks, err := a.client.GetBlocks(ctx, hashes, blockVerbosityTxObjects)
	if err != nil {
		return fmt.Errorf("batch getblock: %w", err)
	}
	for i, block := range blocks {
		if block != nil {
			cache[hashes[i]] = block
		}
	}
	return nil
}

func (a *Adapter) resolveCursorPosition(ctx context.Context, cursorHash string) (int64, int, error) {
	txid := canonicalTxID(cursorHash)
	if txid == "" {
		return -1, -1, nil
	}

	tx, err := a.client.GetRawTransactionVerbose(ctx, txid)
	if err != nil {
		return -1, -1, err
	}
	if tx == nil || strings.TrimSpace(tx.Blockhash) == "" {
		return -1, -1, nil
	}

	block, err := a.client.GetBlock(ctx, tx.Blockhash, blockVerbosityTxObjects)
	if err != nil {
		return -1, -1, err
	}
	if block == nil {
		return -1, -1, nil
	}

	cursorBlock := block.Height
	if cursorBlock < 0 {
		cursorBlock = -1
	}
	cursorTxIndex := -1
	for idx, blockTx := range block.Tx {
		if blockTx == nil {
			continue
		}
		if canonicalTxID(blockTx.Txid) == txid {
			cursorTxIndex = idx
			break
		}
	}
	if cursorTxIndex < 0 && len(block.Tx) > 0 {
		// If the cursor tx cannot be resolved to a tx index, resume from the next tx slot deterministically.
		cursorTxIndex = int(^uint(0) >> 1)
	}
	return cursorBlock, cursorTxIndex, nil
}

func (a *Adapter) txTouchesWatchedAddress(
	ctx context.Context,
	tx *rpc.Transaction,
	watchedAddress string,
	prevoutCache map[string]resolvedPrevout,
) (bool, error) {
	if watchedAddress == "" || tx == nil {
		return false, nil
	}

	for _, vout := range tx.Vout {
		if vout == nil {
			continue
		}
		for _, address := range allOutputAddresses(vout.ScriptPubKey) {
			if normalizeAddressIdentity(address) == watchedAddress {
				return true, nil
			}
		}
	}

	for _, vin := range tx.Vin {
		if vin == nil || strings.TrimSpace(vin.Coinbase) != "" {
			continue
		}
		prevout, ok, err := a.resolveInputPrevout(ctx, vin, prevoutCache)
		if err != nil {
			return false, err
		}
		if ok && normalizeAddressIdentity(prevout.address) == watchedAddress {
			return true, nil
		}
	}

	return false, nil
}

func (a *Adapter) resolveInputPrevout(
	ctx context.Context,
	vin *rpc.Vin,
	cache map[string]resolvedPrevout,
) (resolvedPrevout, bool, error) {
	if vin == nil {
		return resolvedPrevout{}, false, nil
	}
	sourceTxID := canonicalTxID(vin.Txid)
	if sourceTxID == "" || vin.Vout < 0 {
		return resolvedPrevout{}, false, nil
	}

	cacheKey := sourceTxID + ":" + strconv.Itoa(vin.Vout)
	if cached, ok := cache[cacheKey]; ok {
		return cached, cached.found, nil
	}

	// Fast path: use inline prevout from verbosity=3 (no RPC needed).
	if vin.Prevout != nil {
		valueSat, err := parseBTCValueToSatoshi(vin.Prevout.Value)
		if err != nil {
			valueSat = 0
		}
		resolved := resolvedPrevout{
			found:    true,
			address:  firstOutputAddress(vin.Prevout.ScriptPubKey),
			valueSat: valueSat,
		}
		cache[cacheKey] = resolved
		return resolved, true, nil
	}

	// Slow path: fetch the previous transaction via RPC (verbosity=2 fallback).
	prevTx, err := a.client.GetRawTransactionVerbose(ctx, sourceTxID)
	if err != nil {
		return resolvedPrevout{}, false, err
	}
	if prevTx == nil {
		cache[cacheKey] = resolvedPrevout{found: false}
		return resolvedPrevout{}, false, nil
	}

	for _, prevVout := range prevTx.Vout {
		if prevVout == nil || prevVout.N != vin.Vout {
			continue
		}
		valueSat, err := parseBTCValueToSatoshi(prevVout.Value)
		if err != nil {
			valueSat = 0
		}
		resolved := resolvedPrevout{
			found:    true,
			address:  firstOutputAddress(prevVout.ScriptPubKey),
			valueSat: valueSat,
		}
		cache[cacheKey] = resolved
		return resolved, true, nil
	}

	cache[cacheKey] = resolvedPrevout{found: false}
	return resolvedPrevout{}, false, nil
}

func parseBTCValueToSatoshi(value json.Number) (int64, error) {
	raw := strings.TrimSpace(value.String())
	if raw == "" {
		return 0, nil
	}

	amount := new(big.Rat)
	if _, ok := amount.SetString(raw); !ok {
		return 0, fmt.Errorf("invalid btc amount %q", raw)
	}

	amount.Mul(amount, big.NewRat(satoshiPerBTC, 1))
	if !amount.IsInt() {
		return 0, fmt.Errorf("btc amount %q is not satoshi-aligned", raw)
	}

	sats := amount.Num()
	if !sats.IsInt64() {
		return 0, fmt.Errorf("btc amount %q out of range", raw)
	}
	return sats.Int64(), nil
}

func deterministicAddress(values []string) string {
	seen := map[string]struct{}{}
	ordered := make([]string, 0, len(values))
	for _, value := range values {
		candidate := normalizeAddressIdentity(value)
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		ordered = append(ordered, candidate)
	}
	if len(ordered) == 0 {
		return ""
	}
	sort.Strings(ordered)
	return ordered[0]
}

func isAfterCursor(blockNum int64, txIndex int, cursorBlock int64, cursorTxIndex int) bool {
	if cursorBlock < 0 {
		return true
	}
	if blockNum > cursorBlock {
		return true
	}
	if blockNum < cursorBlock {
		return false
	}
	if cursorTxIndex < 0 {
		return true
	}
	return txIndex > cursorTxIndex
}

func txTimePointer(tx *rpc.Transaction, blockTime *time.Time) *time.Time {
	if tx != nil {
		if tx.Blocktime > 0 {
			t := time.Unix(tx.Blocktime, 0)
			return &t
		}
		if tx.Time > 0 {
			t := time.Unix(tx.Time, 0)
			return &t
		}
	}
	return blockTime
}

func unixTimePtr(ts int64) *time.Time {
	if ts <= 0 {
		return nil
	}
	t := time.Unix(ts, 0)
	return &t
}

func pickBlockTime(tx *rpc.Transaction) int64 {
	if tx == nil {
		return 0
	}
	if tx.Blocktime > 0 {
		return tx.Blocktime
	}
	if tx.Time > 0 {
		return tx.Time
	}
	return 0
}

func allOutputAddresses(script rpc.ScriptPubKey) []string {
	addresses := make([]string, 0, 2)
	if candidate := normalizeAddressIdentity(script.Address); candidate != "" {
		addresses = append(addresses, candidate)
	}
	for _, raw := range script.Addresses {
		candidate := normalizeAddressIdentity(raw)
		if candidate == "" {
			continue
		}
		duplicate := false
		for _, existing := range addresses {
			if existing == candidate {
				duplicate = true
				break
			}
		}
		if !duplicate {
			addresses = append(addresses, candidate)
		}
	}
	return addresses
}

func firstOutputAddress(script rpc.ScriptPubKey) string {
	addresses := allOutputAddresses(script)
	if len(addresses) == 0 {
		return ""
	}
	return addresses[0]
}

func normalizeAddressIdentity(address string) string {
	return strings.TrimSpace(address)
}

func canonicalTxID(hash string) string {
	trimmed := strings.TrimSpace(hash)
	if trimmed == "" {
		return ""
	}
	withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
	if withoutPrefix == "" {
		return ""
	}
	return strings.ToLower(withoutPrefix)
}
