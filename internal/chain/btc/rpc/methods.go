package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

func (c *Client) GetBlockCount(ctx context.Context) (int64, error) {
	result, err := c.call(ctx, "getblockcount", []interface{}{})
	if err != nil {
		return 0, fmt.Errorf("getblockcount: %w", err)
	}

	var count int64
	if err := json.Unmarshal(result, &count); err != nil {
		return 0, fmt.Errorf("unmarshal block count: %w", err)
	}
	return count, nil
}

func (c *Client) GetBlockHash(ctx context.Context, height int64) (string, error) {
	result, err := c.call(ctx, "getblockhash", []interface{}{height})
	if err != nil {
		return "", fmt.Errorf("getblockhash(%d): %w", height, err)
	}

	var hash string
	if err := json.Unmarshal(result, &hash); err != nil {
		return "", fmt.Errorf("unmarshal block hash: %w", err)
	}
	return hash, nil
}

func (c *Client) GetBlock(ctx context.Context, hash string, verbosity int) (*Block, error) {
	result, err := c.call(ctx, "getblock", []interface{}{hash, verbosity})
	if err != nil {
		return nil, fmt.Errorf("getblock(%s): %w", hash, err)
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

func (c *Client) GetBlockHeader(ctx context.Context, hash string) (*BlockHeader, error) {
	result, err := c.call(ctx, "getblockheader", []interface{}{hash, true})
	if err != nil {
		return nil, fmt.Errorf("getblockheader(%s): %w", hash, err)
	}
	if string(result) == "null" {
		return nil, nil
	}

	var header BlockHeader
	if err := json.Unmarshal(result, &header); err != nil {
		return nil, fmt.Errorf("unmarshal block header: %w", err)
	}
	return &header, nil
}

func (c *Client) GetRawTransactionVerbose(ctx context.Context, txid string) (*Transaction, error) {
	result, err := c.call(ctx, "getrawtransaction", []interface{}{txid, true})
	if err != nil {
		return nil, fmt.Errorf("getrawtransaction(%s): %w", txid, err)
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

// GetRawTransactionsVerbose fetches multiple transactions in a single JSON-RPC
// batch call. Results are returned in the same order as the input txids.
func (c *Client) GetRawTransactionsVerbose(ctx context.Context, txids []string) ([]*Transaction, error) {
	if len(txids) == 0 {
		return []*Transaction{}, nil
	}

	requests := make([]Request, len(txids))
	for i, txid := range txids {
		requests[i] = c.newRequest("getrawtransaction", []interface{}{txid, true})
	}

	responses, err := c.callBatch(ctx, requests)
	if err != nil {
		return nil, fmt.Errorf("getrawtransaction batch: %w", err)
	}

	results := make([]*Transaction, len(txids))
	for i, resp := range responses {
		if resp.Error != nil {
			return nil, fmt.Errorf("getrawtransaction(%s): %w", txids[i], resp.Error)
		}
		if string(resp.Result) == "null" {
			continue
		}
		var tx Transaction
		if err := json.Unmarshal(resp.Result, &tx); err != nil {
			return nil, fmt.Errorf("unmarshal transaction %s: %w", txids[i], err)
		}
		results[i] = &tx
	}
	return results, nil
}
