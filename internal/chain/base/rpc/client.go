package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/chain/ratelimit"
)

type RPCClient interface {
	GetBlockNumber(ctx context.Context) (int64, error)
	GetBlockByNumber(ctx context.Context, blockNumber int64, includeFullTx bool) (*Block, error)
	GetBlocksByNumber(ctx context.Context, blockNumbers []int64, includeFullTx bool) ([]*Block, error)
	GetBlockByTag(ctx context.Context, tag string, includeFullTx bool) (*Block, error)
	GetFinalizedBlockNumber(ctx context.Context) (int64, error)
	GetTransactionByHash(ctx context.Context, hash string) (*Transaction, error)
	GetTransactionReceipt(ctx context.Context, hash string) (*TransactionReceipt, error)
	GetLogs(ctx context.Context, filter LogFilter) ([]*Log, error)
	GetTransactionsByHash(ctx context.Context, hashes []string) ([]*Transaction, error)
	GetTransactionReceiptsByHash(ctx context.Context, hashes []string) ([]*TransactionReceipt, error)
	TraceBlockByNumber(ctx context.Context, blockNumber int64) ([]*BlockTrace, error)
}

type Client struct {
	httpClient *http.Client
	rpcURL     string
	requestID  atomic.Int64
	logger     *slog.Logger
	limiter    *ratelimit.Limiter
}

func NewClient(rpcURL string, logger *slog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				MaxConnsPerHost:     50,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		rpcURL:     rpcURL,
		logger:     logger,
	}
}

// SetRateLimiter sets the RPC rate limiter for this client.
func (c *Client) SetRateLimiter(l *ratelimit.Limiter) {
	c.limiter = l
}

func (c *Client) call(ctx context.Context, method string, params []interface{}) (json.RawMessage, error) {
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			ratelimit.RecordRPCCall("evm", method, err)
			return nil, fmt.Errorf("rate limiter: %w", err)
		}
	}

	req := c.newRequest(method, params)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		ratelimit.RecordRPCCall("evm", method, err)
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		ratelimit.RecordRPCCall("evm", method, err)
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		callErr := fmt.Errorf("http status %d: %s", resp.StatusCode, string(respBody))
		ratelimit.RecordRPCCall("evm", method, callErr)
		return nil, callErr
	}

	var rpcResp Response
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		ratelimit.RecordRPCCall("evm", method, err)
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if rpcResp.Error != nil {
		ratelimit.RecordRPCCall("evm", method, rpcResp.Error)
		return nil, rpcResp.Error
	}

	ratelimit.RecordRPCCall("evm", method, nil)
	return rpcResp.Result, nil
}

func (c *Client) callBatch(ctx context.Context, requests []Request) ([]Response, error) {
	if len(requests) == 0 {
		return []Response{}, nil
	}

	body, err := json.Marshal(requests)
	if err != nil {
		return nil, fmt.Errorf("marshal batch request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create batch request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("batch http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read batch response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResps []Response
	if err := json.Unmarshal(respBody, &rpcResps); err != nil {
		return nil, fmt.Errorf("unmarshal batch response: %w", err)
	}

	responseByID := make(map[int]Response, len(rpcResps))
	for _, rpcResp := range rpcResps {
		responseByID[rpcResp.ID] = rpcResp
	}

	ordered := make([]Response, len(requests))
	for i, req := range requests {
		rpcResp, ok := responseByID[req.ID]
		if !ok {
			return nil, fmt.Errorf("missing batch response id=%d method=%s", req.ID, req.Method)
		}
		ordered[i] = rpcResp
	}

	return ordered, nil
}

func (c *Client) newRequest(method string, params []interface{}) Request {
	id := int(c.requestID.Add(1))
	return Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
}
