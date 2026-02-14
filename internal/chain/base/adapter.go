package base

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/chain/base/rpc"
)

const (
	maxInitialLookbackBlocks = 200
	maxConcurrentTxs         = 10
)

type Adapter struct {
	client rpc.RPCClient
	logger *slog.Logger
}

var _ chain.ChainAdapter = (*Adapter)(nil)

func NewAdapter(rpcURL string, logger *slog.Logger) *Adapter {
	return &Adapter{
		client: rpc.NewClient(rpcURL, logger),
		logger: logger.With("chain", "base"),
	}
}

func (a *Adapter) Chain() string {
	return "base"
}

func (a *Adapter) GetHeadSequence(ctx context.Context) (int64, error) {
	return a.client.GetBlockNumber(ctx)
}

// FetchNewSignatures fetches Base tx hashes for a watched address.
// Cursor is the last ingested tx hash; scanning resumes from its block/tx index.
func (a *Adapter) FetchNewSignatures(ctx context.Context, address string, cursor *string, batchSize int) ([]chain.SignatureInfo, error) {
	if batchSize <= 0 {
		return []chain.SignatureInfo{}, nil
	}

	head, err := a.client.GetBlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("get head block: %w", err)
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
	signatures := make([]chain.SignatureInfo, 0, batchSize)

	for blockNum := startBlock; blockNum <= head && len(signatures) < batchSize; blockNum++ {
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
			if cursorBlock == blockNum && cursorTxIndex >= 0 && txIndex >= 0 && txIndex <= cursorTxIndex {
				continue
			}

			from := strings.ToLower(strings.TrimSpace(tx.From))
			to := strings.ToLower(strings.TrimSpace(tx.To))
			if from != addr && to != addr {
				continue
			}

			signatures = append(signatures, chain.SignatureInfo{
				Hash:     tx.Hash,
				Sequence: blockNum,
				Time:     blockTime,
			})
			if len(signatures) >= batchSize {
				break
			}
		}
	}

	a.logger.Info("fetched signatures",
		"address", address,
		"count", len(signatures),
		"cursor", cursor,
		"start_block", startBlock,
		"head_block", head,
	)

	return signatures, nil
}

func (a *Adapter) FetchTransactions(ctx context.Context, signatures []string) ([]json.RawMessage, error) {
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
				"chain":   "base",
				"tx":      tx,
				"receipt": receipt,
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

	a.logger.Info("fetched transactions", "count", len(results))
	return results, nil
}

func parseHexInt64(value string) (int64, error) {
	return rpc.ParseHexInt64(value)
}
