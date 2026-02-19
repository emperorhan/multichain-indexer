package base

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
	"github.com/emperorhan/multichain-indexer/internal/chain/base/rpc"
	"github.com/emperorhan/multichain-indexer/internal/chain/ratelimit"
)

const (
	maxInitialLookbackBlocks = 200
	maxConcurrentTxs         = 10
)

type Adapter struct {
	client    rpc.RPCClient
	logger    *slog.Logger
	chainName string
	evmLayer  string // "l1" or "l2"
}

var _ chain.ChainAdapter = (*Adapter)(nil)
var _ chain.ReorgAwareAdapter = (*Adapter)(nil)

func NewAdapter(rpcURL string, logger *slog.Logger) *Adapter {
	return NewAdapterWithChain("base", rpcURL, logger)
}

func inferEVMLayer(chainName string) string {
	switch chainName {
	case "ethereum", "polygon", "bsc":
		return "l1"
	default:
		return "l2"
	}
}

// NewAdapterWithChain creates an EVM adapter for any chain name.
func NewAdapterWithChain(chainName, rpcURL string, logger *slog.Logger) *Adapter {
	return &Adapter{
		client:    rpc.NewClient(rpcURL, logger),
		logger:    logger.With("chain", chainName),
		chainName: chainName,
		evmLayer:  inferEVMLayer(chainName),
	}
}

// SetRateLimiter applies a rate limiter to the underlying RPC client.
func (a *Adapter) SetRateLimiter(l *ratelimit.Limiter) {
	if c, ok := a.client.(*rpc.Client); ok {
		c.SetRateLimiter(l)
	}
}

func (a *Adapter) Chain() string {
	return a.chainName
}

func (a *Adapter) GetHeadSequence(ctx context.Context) (int64, error) {
	return a.client.GetBlockNumber(ctx)
}

// FetchNewSignatures fetches Base tx hashes for a watched address.
// Cursor is the last ingested tx hash; scanning resumes from its block/tx index.
func (a *Adapter) FetchNewSignatures(ctx context.Context, address string, cursor *string, batchSize int) ([]chain.SignatureInfo, error) {
	return a.FetchNewSignaturesWithCutoff(ctx, address, cursor, batchSize, 0)
}

// FetchNewSignaturesWithCutoff fetches Base tx hashes with an inclusive upper sequence bound.
// cutoffSeq <= 0 disables the upper bound and preserves legacy behavior.
func (a *Adapter) FetchNewSignaturesWithCutoff(ctx context.Context, address string, cursor *string, batchSize int, cutoffSeq int64) ([]chain.SignatureInfo, error) {
	if batchSize <= 0 {
		return []chain.SignatureInfo{}, nil
	}

	head := cutoffSeq
	if head <= 0 {
		liveHead, err := a.client.GetBlockNumber(ctx)
		if err != nil {
			return nil, fmt.Errorf("get head block: %w", err)
		}
		head = liveHead
	}

	startBlock := head - (maxInitialLookbackBlocks - 1)
	if startBlock < 0 {
		startBlock = 0
	}
	cursorBlock := int64(-1)
	cursorTxIndex := int64(-1)

	if cursor != nil && strings.TrimSpace(*cursor) != "" {
		cursorTx, txErr := a.client.GetTransactionByHash(ctx, *cursor)
		if txErr != nil {
			return nil, fmt.Errorf("resolve cursor tx %s: %w", *cursor, txErr)
		}
		if cursorTx != nil {
			if parsedBlock, parseErr := parseHexInt64(cursorTx.BlockNumber); parseErr == nil {
				cursorBlock = parsedBlock
				startBlock = parsedBlock
			}
			if parsedIndex, parseErr := parseHexInt64(cursorTx.TransactionIndex); parseErr == nil {
				cursorTxIndex = parsedIndex
			}
		}
	}

	addr := strings.ToLower(strings.TrimSpace(address))
	candidates := make(map[string]candidateSignature, batchSize)
	upsertCandidate := func(hash string, blockNum, txIndex int64, blockTime *time.Time) {
		if strings.TrimSpace(hash) == "" {
			return
		}
		existing, ok := candidates[hash]
		if !ok {
			candidates[hash] = candidateSignature{
				hash:      hash,
				blockNum:  blockNum,
				txIndex:   txIndex,
				blockTime: blockTime,
			}
			return
		}

		if existing.blockNum > blockNum || (existing.blockNum == blockNum && compareTxIndex(existing.txIndex, txIndex) > 0) {
			existing.blockNum = blockNum
			existing.txIndex = txIndex
		}
		if existing.blockTime == nil && blockTime != nil {
			existing.blockTime = blockTime
		}
		candidates[hash] = existing
	}

	for blockNum := startBlock; blockNum <= head; blockNum++ {
		block, err := a.client.GetBlockByNumber(ctx, blockNum, true)
		if err != nil {
			return nil, fmt.Errorf("get block %d: %w", blockNum, err)
		}
		if block == nil {
			continue
		}

		var blockTime *time.Time
		if ts, err := parseHexInt64(block.Timestamp); err == nil && ts > 0 {
			parsedTime := time.Unix(ts, 0)
			blockTime = &parsedTime
		}

		for _, tx := range block.Transactions {
			if tx == nil || strings.TrimSpace(tx.Hash) == "" {
				continue
			}
			txIndex, err := parseHexInt64(tx.TransactionIndex)
			if err != nil {
				txIndex = -1
			}
			if !isAfterCursor(blockNum, txIndex, cursorBlock, cursorTxIndex) {
				continue
			}

			from := strings.ToLower(strings.TrimSpace(tx.From))
			to := strings.ToLower(strings.TrimSpace(tx.To))
			if from != addr && to != addr {
				continue
			}

			upsertCandidate(tx.Hash, blockNum, txIndex, blockTime)
		}
	}

	if topic := addressTopic(addr); topic != "" {
		logs, err := a.fetchAddressTopicLogs(ctx, startBlock, head, topic)
		if err != nil {
			return nil, fmt.Errorf("eth_getLogs failed for address %s: %w", address, err)
		}
		for _, entry := range logs {
			if entry == nil || strings.TrimSpace(entry.TransactionHash) == "" {
				continue
			}

			blockNum, err := parseHexInt64(entry.BlockNumber)
			if err != nil {
				continue
			}
			txIndex, err := parseHexInt64(entry.TransactionIndex)
			if err != nil {
				txIndex = -1
			}
			if !isAfterCursor(blockNum, txIndex, cursorBlock, cursorTxIndex) {
				continue
			}

			upsertCandidate(entry.TransactionHash, blockNum, txIndex, nil)
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
		cmp := compareTxIndex(ordered[i].txIndex, ordered[j].txIndex)
		if cmp != 0 {
			return cmp < 0
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

func (a *Adapter) GetBlockHash(ctx context.Context, blockNumber int64) (string, string, error) {
	block, err := a.client.GetBlockByNumber(ctx, blockNumber, false)
	if err != nil {
		return "", "", fmt.Errorf("get block %d: %w", blockNumber, err)
	}
	if block == nil {
		return "", "", fmt.Errorf("block %d not found", blockNumber)
	}
	return block.Hash, block.ParentHash, nil
}

func (a *Adapter) GetFinalizedBlockNumber(ctx context.Context) (int64, error) {
	return a.client.GetFinalizedBlockNumber(ctx)
}

func (a *Adapter) FetchTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error) {
	if len(signatures) == 0 {
		return []json.RawMessage{}, nil
	}

	txs, err := a.client.GetTransactionsByHash(ctx, signatures)
	if err != nil {
		a.logger.Warn("batch transaction fetch failed, falling back", "error", err)
		return a.fetchTransactionsOneByOne(ctx, signatures)
	}
	receipts, err := a.client.GetTransactionReceiptsByHash(ctx, signatures)
	if err != nil {
		a.logger.Warn("batch receipt fetch failed, falling back", "error", err)
		return a.fetchTransactionsOneByOne(ctx, signatures)
	}

	results := make([]json.RawMessage, len(signatures))
	for i, txHash := range signatures {
		tx := txs[i]
		if tx == nil {
			return nil, fmt.Errorf("transaction %s not found", txHash)
		}
		receipt := receipts[i]
		if receipt == nil {
			return nil, fmt.Errorf("receipt %s not found", txHash)
		}

		payload, err := json.Marshal(map[string]interface{}{
			"chain":     a.chainName,
			"evm_layer": a.evmLayer,
			"tx":        tx,
			"receipt":   receipt,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal payload %s: %w", txHash, err)
		}
		results[i] = payload
	}

	a.logger.Info("fetched transactions", "count", len(results))
	return results, nil
}

func (a *Adapter) fetchTransactionsOneByOne(ctx context.Context, signatures []string) ([]json.RawMessage, error) {
	results := make([]json.RawMessage, len(signatures))
	var mu sync.Mutex
	var firstErr error

	sem := make(chan struct{}, maxConcurrentTxs)
	var wg sync.WaitGroup

	for i, hash := range signatures {
		wg.Add(1)
		go func(idx int, txHash string) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			tx, err := a.client.GetTransactionByHash(ctx, txHash)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("fetch transaction %s: %w", txHash, err)
				}
				mu.Unlock()
				return
			}
			if tx == nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("transaction %s not found", txHash)
				}
				mu.Unlock()
				return
			}

			receipt, err := a.client.GetTransactionReceipt(ctx, txHash)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("fetch receipt %s: %w", txHash, err)
				}
				mu.Unlock()
				return
			}
			if receipt == nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("receipt %s not found", txHash)
				}
				mu.Unlock()
				return
			}

			payload, err := json.Marshal(map[string]interface{}{
				"chain":     a.chainName,
				"evm_layer": a.evmLayer,
				"tx":        tx,
				"receipt":   receipt,
			})
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("marshal payload %s: %w", txHash, err)
				}
				mu.Unlock()
				return
			}

			mu.Lock()
			results[idx] = payload
			mu.Unlock()
		}(i, hash)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	a.logger.Info("fetched transactions (fallback)", "count", len(results))
	return results, nil
}

func parseHexInt64(value string) (int64, error) {
	return rpc.ParseHexInt64(value)
}

func formatHexInt64(value int64) string {
	return fmt.Sprintf("0x%x", value)
}

func isAfterCursor(blockNum, txIndex, cursorBlock, cursorTxIndex int64) bool {
	if cursorBlock < 0 {
		return true
	}
	if blockNum < cursorBlock {
		return false
	}
	if blockNum > cursorBlock {
		return true
	}
	if cursorTxIndex >= 0 && txIndex >= 0 && txIndex <= cursorTxIndex {
		return false
	}
	return true
}

func compareTxIndex(a, b int64) int {
	if a == b {
		return 0
	}
	if a < 0 && b < 0 {
		return 0
	}
	if a < 0 {
		return 1
	}
	if b < 0 {
		return -1
	}
	if a < b {
		return -1
	}
	return 1
}

func addressTopic(address string) string {
	raw := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(address)), "0x")
	if len(raw) != 40 {
		return ""
	}
	return "0x000000000000000000000000" + raw
}

func (a *Adapter) fetchAddressTopicLogs(ctx context.Context, fromBlock, toBlock int64, topic string) ([]*rpc.Log, error) {
	var allLogs []*rpc.Log
	topicFilters := [][]interface{}{
		{nil, topic},
		{nil, nil, topic},
		{nil, nil, nil, topic},
	}
	for _, topics := range topicFilters {
		logs, err := a.fetchLogsWithFallback(ctx, fromBlock, toBlock, topics)
		if err != nil {
			return nil, err
		}
		allLogs = append(allLogs, logs...)
	}

	return allLogs, nil
}

func (a *Adapter) fetchLogsWithFallback(ctx context.Context, fromBlock, toBlock int64, topics []interface{}) ([]*rpc.Log, error) {
	if fromBlock > toBlock {
		return []*rpc.Log{}, nil
	}

	filter := rpc.LogFilter{
		FromBlock: formatHexInt64(fromBlock),
		ToBlock:   formatHexInt64(toBlock),
		Topics:    topics,
	}

	logs, err := a.client.GetLogs(ctx, filter)
	if err == nil {
		return logs, nil
	}

	if fromBlock < toBlock && isLikelyLogRangeLimitError(err) {
		mid := fromBlock + (toBlock-fromBlock)/2
		left, leftErr := a.fetchLogsWithFallback(ctx, fromBlock, mid, topics)
		if leftErr != nil {
			return nil, leftErr
		}
		right, rightErr := a.fetchLogsWithFallback(ctx, mid+1, toBlock, topics)
		if rightErr != nil {
			return nil, rightErr
		}
		return append(left, right...), nil
	}

	// Some providers still reject single-block getLogs when log density is extreme.
	// Fallback to receipt scan for this block to preserve coverage.
	if fromBlock == toBlock {
		receiptLogs, fallbackErr := a.scanBlockReceiptsForTopics(ctx, fromBlock, topics)
		if fallbackErr == nil {
			return receiptLogs, nil
		}
		return nil, fmt.Errorf("eth_getLogs block %d failed (%w), and receipt fallback failed: %w", fromBlock, err, fallbackErr)
	}

	return nil, err
}

func (a *Adapter) scanBlockReceiptsForTopics(ctx context.Context, blockNum int64, topics []interface{}) ([]*rpc.Log, error) {
	block, err := a.client.GetBlockByNumber(ctx, blockNum, true)
	if err != nil {
		return nil, fmt.Errorf("get block %d for receipt fallback: %w", blockNum, err)
	}
	if block == nil || len(block.Transactions) == 0 {
		return []*rpc.Log{}, nil
	}

	txHashes := make([]string, 0, len(block.Transactions))
	for _, tx := range block.Transactions {
		if tx == nil || strings.TrimSpace(tx.Hash) == "" {
			continue
		}
		txHashes = append(txHashes, tx.Hash)
	}
	if len(txHashes) == 0 {
		return []*rpc.Log{}, nil
	}

	receipts, err := a.client.GetTransactionReceiptsByHash(ctx, txHashes)
	if err != nil || len(receipts) != len(txHashes) {
		receipts = make([]*rpc.TransactionReceipt, len(txHashes))
		for i, txHash := range txHashes {
			receipt, singleErr := a.client.GetTransactionReceipt(ctx, txHash)
			if singleErr != nil {
				return nil, fmt.Errorf("receipt fallback %s: %w", txHash, singleErr)
			}
			receipts[i] = receipt
		}
	}

	filteredLogs := make([]*rpc.Log, 0)
	for _, receipt := range receipts {
		if receipt == nil {
			continue
		}
		for _, entry := range receipt.Logs {
			if entry == nil || !logMatchesTopics(entry, topics) {
				continue
			}
			logCopy := *entry
			if strings.TrimSpace(logCopy.TransactionHash) == "" {
				logCopy.TransactionHash = receipt.TransactionHash
			}
			if strings.TrimSpace(logCopy.BlockNumber) == "" {
				logCopy.BlockNumber = receipt.BlockNumber
			}
			if strings.TrimSpace(logCopy.TransactionIndex) == "" {
				logCopy.TransactionIndex = receipt.TransactionIndex
			}
			filteredLogs = append(filteredLogs, &logCopy)
		}
	}

	return filteredLogs, nil
}

func logMatchesTopics(log *rpc.Log, topics []interface{}) bool {
	if log == nil {
		return false
	}

	for idx, expected := range topics {
		if expected == nil {
			continue
		}
		expectedStr, ok := expected.(string)
		if !ok {
			continue
		}
		if idx >= len(log.Topics) {
			return false
		}
		if !strings.EqualFold(strings.TrimSpace(log.Topics[idx]), strings.TrimSpace(expectedStr)) {
			return false
		}
	}
	return true
}

func isLikelyLogRangeLimitError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	patterns := []string{
		"10000",
		"10,000",
		"query returned more than",
		"too many results",
		"response size exceeded",
		"exceeds max results",
		"block range is too wide",
		"limit exceeded",
		"-32005",
	}
	for _, pattern := range patterns {
		if strings.Contains(message, pattern) {
			return true
		}
	}
	return false
}

type candidateSignature struct {
	hash      string
	blockNum  int64
	txIndex   int64
	blockTime *time.Time
}
