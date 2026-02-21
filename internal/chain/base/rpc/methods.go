package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (c *Client) GetBlockNumber(ctx context.Context) (int64, error) {
	result, err := c.call(ctx, "eth_blockNumber", []interface{}{})
	if err != nil {
		return 0, fmt.Errorf("eth_blockNumber: %w", err)
	}

	var hexNum string
	if err := json.Unmarshal(result, &hexNum); err != nil {
		return 0, fmt.Errorf("unmarshal block number: %w", err)
	}

	blockNumber, err := ParseHexInt64(hexNum)
	if err != nil {
		return 0, fmt.Errorf("parse block number: %w", err)
	}
	return blockNumber, nil
}

func (c *Client) GetBlockByNumber(ctx context.Context, blockNumber int64, includeFullTx bool) (*Block, error) {
	params := []interface{}{formatHexInt64(blockNumber), includeFullTx}
	result, err := c.call(ctx, "eth_getBlockByNumber", params)
	if err != nil {
		return nil, fmt.Errorf("eth_getBlockByNumber(%d): %w", blockNumber, err)
	}
	if string(result) == "null" {
		return nil, nil
	}

	var block Block
	if err := json.Unmarshal(result, &block); err != nil {
		return nil, fmt.Errorf("unmarshal block: %w", err)
	}

	return &block, nil
}

func (c *Client) GetBlockByTag(ctx context.Context, tag string, includeFullTx bool) (*Block, error) {
	params := []interface{}{tag, includeFullTx}
	result, err := c.call(ctx, "eth_getBlockByNumber", params)
	if err != nil {
		return nil, fmt.Errorf("eth_getBlockByNumber(%s): %w", tag, err)
	}
	if string(result) == "null" {
		return nil, nil
	}

	var block Block
	if err := json.Unmarshal(result, &block); err != nil {
		return nil, fmt.Errorf("unmarshal block: %w", err)
	}
	return &block, nil
}

func (c *Client) GetFinalizedBlockNumber(ctx context.Context) (int64, error) {
	block, err := c.GetBlockByTag(ctx, "finalized", false)
	if err != nil {
		return 0, fmt.Errorf("get finalized block: %w", err)
	}
	if block == nil {
		return 0, fmt.Errorf("finalized block not available")
	}
	blockNumber, err := ParseHexInt64(block.Number)
	if err != nil {
		return 0, fmt.Errorf("parse finalized block number: %w", err)
	}
	return blockNumber, nil
}

func (c *Client) GetTransactionByHash(ctx context.Context, hash string) (*Transaction, error) {
	result, err := c.call(ctx, "eth_getTransactionByHash", []interface{}{hash})
	if err != nil {
		return nil, fmt.Errorf("eth_getTransactionByHash(%s): %w", hash, err)
	}
	if string(result) == "null" {
		return nil, nil
	}

	var tx Transaction
	if err := json.Unmarshal(result, &tx); err != nil {
		return nil, fmt.Errorf("unmarshal transaction: %w", err)
	}

	return &tx, nil
}

func (c *Client) GetTransactionReceipt(ctx context.Context, hash string) (*TransactionReceipt, error) {
	result, err := c.call(ctx, "eth_getTransactionReceipt", []interface{}{hash})
	if err != nil {
		return nil, fmt.Errorf("eth_getTransactionReceipt(%s): %w", hash, err)
	}
	if string(result) == "null" {
		return nil, nil
	}

	var receipt TransactionReceipt
	if err := json.Unmarshal(result, &receipt); err != nil {
		return nil, fmt.Errorf("unmarshal transaction receipt: %w", err)
	}

	return &receipt, nil
}

func (c *Client) GetLogs(ctx context.Context, filter LogFilter) ([]*Log, error) {
	result, err := c.call(ctx, "eth_getLogs", []interface{}{filter})
	if err != nil {
		return nil, fmt.Errorf("eth_getLogs: %w", err)
	}

	var logs []*Log
	if err := json.Unmarshal(result, &logs); err != nil {
		return nil, fmt.Errorf("unmarshal logs: %w", err)
	}

	return logs, nil
}

func (c *Client) GetTransactionsByHash(ctx context.Context, hashes []string) ([]*Transaction, error) {
	if len(hashes) == 0 {
		return []*Transaction{}, nil
	}

	requests := make([]Request, len(hashes))
	for i, hash := range hashes {
		requests[i] = c.newRequest("eth_getTransactionByHash", []interface{}{hash})
	}

	responses, err := c.callBatch(ctx, requests)
	if err != nil {
		return nil, fmt.Errorf("eth_getTransactionByHash batch: %w", err)
	}

	results := make([]*Transaction, len(hashes))
	for i, response := range responses {
		if response.Error != nil {
			return nil, fmt.Errorf("eth_getTransactionByHash(%s): %w", hashes[i], response.Error)
		}
		if string(response.Result) == "null" {
			continue
		}

		var tx Transaction
		if err := json.Unmarshal(response.Result, &tx); err != nil {
			return nil, fmt.Errorf("unmarshal transaction %s: %w", hashes[i], err)
		}
		results[i] = &tx
	}

	return results, nil
}

func (c *Client) GetTransactionReceiptsByHash(ctx context.Context, hashes []string) ([]*TransactionReceipt, error) {
	if len(hashes) == 0 {
		return []*TransactionReceipt{}, nil
	}

	requests := make([]Request, len(hashes))
	for i, hash := range hashes {
		requests[i] = c.newRequest("eth_getTransactionReceipt", []interface{}{hash})
	}

	responses, err := c.callBatch(ctx, requests)
	if err != nil {
		return nil, fmt.Errorf("eth_getTransactionReceipt batch: %w", err)
	}

	results := make([]*TransactionReceipt, len(hashes))
	for i, response := range responses {
		if response.Error != nil {
			return nil, fmt.Errorf("eth_getTransactionReceipt(%s): %w", hashes[i], response.Error)
		}
		if string(response.Result) == "null" {
			continue
		}

		var receipt TransactionReceipt
		if err := json.Unmarshal(response.Result, &receipt); err != nil {
			return nil, fmt.Errorf("unmarshal transaction receipt %s: %w", hashes[i], err)
		}
		results[i] = &receipt
	}

	return results, nil
}

// GetBlocksByNumber fetches multiple blocks in a single JSON-RPC batch call.
// Results are returned in the same order as the input block numbers.
// Nil entries indicate blocks that were not found (null response).
func (c *Client) GetBlocksByNumber(ctx context.Context, blockNumbers []int64, includeFullTx bool) ([]*Block, error) {
	if len(blockNumbers) == 0 {
		return []*Block{}, nil
	}

	requests := make([]Request, len(blockNumbers))
	for i, num := range blockNumbers {
		requests[i] = c.newRequest("eth_getBlockByNumber", []interface{}{formatHexInt64(num), includeFullTx})
	}

	responses, err := c.callBatch(ctx, requests)
	if err != nil {
		return nil, fmt.Errorf("eth_getBlockByNumber batch: %w", err)
	}

	results := make([]*Block, len(blockNumbers))
	for i, resp := range responses {
		if resp.Error != nil {
			return nil, fmt.Errorf("eth_getBlockByNumber(%d): %w", blockNumbers[i], resp.Error)
		}
		if string(resp.Result) == "null" {
			continue
		}
		var block Block
		if err := json.Unmarshal(resp.Result, &block); err != nil {
			return nil, fmt.Errorf("unmarshal block %d: %w", blockNumbers[i], err)
		}
		results[i] = &block
	}
	return results, nil
}

func ParseHexInt64(value string) (int64, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, fmt.Errorf("empty hex value")
	}
	raw = strings.TrimPrefix(strings.ToLower(raw), "0x")
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(raw, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse hex %q: %w", value, err)
	}
	return int64(parsed), nil
}

func formatHexInt64(value int64) string {
	return fmt.Sprintf("0x%x", value)
}
