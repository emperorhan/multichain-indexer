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
