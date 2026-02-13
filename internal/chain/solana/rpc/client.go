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
)

// RPCClient abstracts the Solana JSON-RPC interface for testing.
type RPCClient interface {
	GetSlot(ctx context.Context, commitment string) (int64, error)
	GetSignaturesForAddress(ctx context.Context, address string, opts *GetSignaturesOpts) ([]SignatureInfo, error)
	GetTransaction(ctx context.Context, signature string) (json.RawMessage, error)
}

type Client struct {
	httpClient *http.Client
	rpcURL     string
	requestID  atomic.Int64
	logger     *slog.Logger
}

func NewClient(rpcURL string, logger *slog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		rpcURL: rpcURL,
		logger: logger,
	}
}

func (c *Client) call(ctx context.Context, method string, params []interface{}) (json.RawMessage, error) {
	id := int(c.requestID.Add(1))
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

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
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp Response
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	return rpcResp.Result, nil
}
