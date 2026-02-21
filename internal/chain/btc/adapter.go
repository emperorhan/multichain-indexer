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

func (a *Adapter) GetHeadSequence(ctx context.Context) (int64, error) {
	return a.client.GetBlockCount(ctx)
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
	head, err := a.client.GetBlockCount(ctx)
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
		head, err = a.client.GetBlockCount(ctx)
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

	for blockNum := startBlock; blockNum <= head; blockNum++ {
		blockHash, err := a.client.GetBlockHash(ctx, blockNum)
		if err != nil {
			return nil, fmt.Errorf("get block hash %d: %w", blockNum, err)
		}
		block, err := a.client.GetBlock(ctx, blockHash, blockVerbosityTxObjects)
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

	for blockNum := startBlock; blockNum <= endBlock; blockNum++ {
		blockHash, err := a.client.GetBlockHash(ctx, blockNum)
		if err != nil {
			return nil, fmt.Errorf("scan get block hash %d: %w", blockNum, err)
		}
		// Use verbosity=3 to get vin.prevout inline (Bitcoin Core 25.0+),
		// falling back to verbosity=2 if the node doesn't support it.
		block, err := a.client.GetBlock(ctx, blockHash, blockVerbosityPrevout)
		if err != nil {
			// Fallback: older Bitcoin Core versions return an error for verbosity=3.
			block, err = a.client.GetBlock(ctx, blockHash, blockVerbosityTxObjects)
			if err != nil {
				return nil, fmt.Errorf("scan get block %d: %w", blockNum, err)
			}
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

	cacheKey := fmt.Sprintf("%s:%d", sourceTxID, vin.Vout)
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
