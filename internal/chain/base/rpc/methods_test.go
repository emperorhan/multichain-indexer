package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func methodTestClient(handler func(*http.Request) (*http.Response, error)) *Client {
	client := NewClient("http://rpc.local", nil)
	client.httpClient = &http.Client{
		Transport: roundTripFunc(handler),
	}
	return client
}

func TestGetBlockNumber(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req Request
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "eth_blockNumber", req.Method)

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`"0x10"`),
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	block, err := client.GetBlockNumber(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(16), block)
}

func TestGetBlockByNumber(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req Request
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "eth_getBlockByNumber", req.Method)
		assert.Equal(t, "0x2a", req.Params[0])
		assert.Equal(t, true, req.Params[1])

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: json.RawMessage(`{
				"number":"0x2a",
				"hash":"0xblock",
				"timestamp":"0x65",
				"transactions":[
					{"hash":"0xtx1","blockNumber":"0x2a","transactionIndex":"0x0","from":"0x1","to":"0x2","value":"0x0","gasPrice":"0x1"}
				]
			}`),
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	block, err := client.GetBlockByNumber(context.Background(), 42, true)
	require.NoError(t, err)
	require.NotNil(t, block)
	assert.Equal(t, "0x2a", block.Number)
	require.Len(t, block.Transactions, 1)
	assert.Equal(t, "0xtx1", block.Transactions[0].Hash)
}

func TestGetTransactionByHash_Null(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		resp := Response{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`null`),
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	tx, err := client.GetTransactionByHash(context.Background(), "0xmissing")
	require.NoError(t, err)
	assert.Nil(t, tx)
}

func TestGetTransactionReceipt(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		resp := Response{
			JSONRPC: "2.0",
			ID:      1,
			Result: json.RawMessage(`{
				"transactionHash":"0xtx1",
				"blockNumber":"0x2a",
				"transactionIndex":"0x1",
				"status":"0x1",
				"from":"0x1",
				"to":"0x2",
				"gasUsed":"0x5208",
				"effectiveGasPrice":"0x10"
			}`),
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	receipt, err := client.GetTransactionReceipt(context.Background(), "0xtx1")
	require.NoError(t, err)
	require.NotNil(t, receipt)
	assert.Equal(t, "0xtx1", receipt.TransactionHash)
	assert.Equal(t, "0x5208", receipt.GasUsed)
}

func TestGetLogs(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req Request
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "eth_getLogs", req.Method)
		require.Len(t, req.Params, 1)

		result := json.RawMessage(`[
			{"blockNumber":"0x10","transactionHash":"0xtx1","transactionIndex":"0x1","address":"0xabc","topics":["0x1"],"data":"0x","logIndex":"0x0","removed":false}
		]`)
		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	logs, err := client.GetLogs(context.Background(), LogFilter{
		FromBlock: "0x10",
		ToBlock:   "0x11",
		Topics:    []interface{}{nil, "0xtopic"},
	})
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "0xtx1", logs[0].TransactionHash)
	assert.Equal(t, "0x10", logs[0].BlockNumber)
}

func TestGetTransactionsByHash(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var reqs []Request
		require.NoError(t, json.Unmarshal(body, &reqs))
		require.Len(t, reqs, 2)
		assert.Equal(t, "eth_getTransactionByHash", reqs[0].Method)
		assert.Equal(t, "eth_getTransactionByHash", reqs[1].Method)

		resp := []Response{
			{
				JSONRPC: "2.0",
				ID:      reqs[0].ID,
				Result:  json.RawMessage(`{"hash":"0x1","blockNumber":"0x10","transactionIndex":"0x0"}`),
			},
			{
				JSONRPC: "2.0",
				ID:      reqs[1].ID,
				Result:  json.RawMessage(`null`),
			},
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	txs, err := client.GetTransactionsByHash(context.Background(), []string{"0x1", "0x2"})
	require.NoError(t, err)
	require.Len(t, txs, 2)
	require.NotNil(t, txs[0])
	assert.Equal(t, "0x1", txs[0].Hash)
	assert.Nil(t, txs[1])
}

func TestGetTransactionReceiptsByHash(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var reqs []Request
		require.NoError(t, json.Unmarshal(body, &reqs))
		require.Len(t, reqs, 2)
		assert.Equal(t, "eth_getTransactionReceipt", reqs[0].Method)
		assert.Equal(t, "eth_getTransactionReceipt", reqs[1].Method)

		resp := []Response{
			{
				JSONRPC: "2.0",
				ID:      reqs[0].ID,
				Result:  json.RawMessage(`{"transactionHash":"0x1","blockNumber":"0x10","transactionIndex":"0x0","status":"0x1","from":"0x1","to":"0x2","gasUsed":"0x5208","effectiveGasPrice":"0x10"}`),
			},
			{
				JSONRPC: "2.0",
				ID:      reqs[1].ID,
				Result:  json.RawMessage(`null`),
			},
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	receipts, err := client.GetTransactionReceiptsByHash(context.Background(), []string{"0x1", "0x2"})
	require.NoError(t, err)
	require.Len(t, receipts, 2)
	require.NotNil(t, receipts[0])
	assert.Equal(t, "0x1", receipts[0].TransactionHash)
	assert.Nil(t, receipts[1])
}
