package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/chain/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers: method-level mock server
// ---------------------------------------------------------------------------

// methodRouter dispatches RPC requests by method name, returning canned responses.
type methodRouter struct {
	handlers map[string]func(params []interface{}) (interface{}, *RPCError)
	calls    atomic.Int64 // total call count (for rate limiter verification)
}

func newMethodRouter() *methodRouter {
	return &methodRouter{
		handlers: make(map[string]func(params []interface{}) (interface{}, *RPCError)),
	}
}

func (mr *methodRouter) handle(method string, fn func(params []interface{}) (interface{}, *RPCError)) {
	mr.handlers[method] = fn
}

func (mr *methodRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mr.calls.Add(1)
	body, _ := io.ReadAll(r.Body)

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	handler, ok := mr.handlers[req.Method]
	if !ok {
		w.WriteHeader(http.StatusOK)
		resp, _ := json.Marshal(struct {
			JSONRPC string    `json:"jsonrpc"`
			ID      int       `json:"id"`
			Error   *RPCError `json:"error"`
		}{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: "Method not found"},
		})
		_, _ = w.Write(resp)
		return
	}

	// Convert params from []interface{} (they arrive as json decoded values).
	params := append([]interface{}(nil), req.Params...)

	result, rpcErr := handler(params)

	if rpcErr != nil {
		resp, _ := json.Marshal(struct {
			JSONRPC string    `json:"jsonrpc"`
			ID      int       `json:"id"`
			Error   *RPCError `json:"error"`
		}{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   rpcErr,
		})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(resp)
		return
	}

	raw, _ := json.Marshal(result)
	resp, _ := json.Marshal(Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  raw,
	})
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
}

// newClientWithRouter creates a test server + Client wired to it.
// Returns the client, the router (for asserting calls), and a cleanup func.
func newClientWithRouter() (*Client, *methodRouter, func()) {
	router := newMethodRouter()
	ts := httptest.NewServer(router)
	client := NewClient(ts.URL, newTestLogger())
	return client, router, ts.Close
}

// ===========================================================================
// GetBlockCount
// ===========================================================================

func TestGetBlockCount_Success(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getblockcount", func(_ []interface{}) (interface{}, *RPCError) {
		return int64(840000), nil
	})

	count, err := client.GetBlockCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(840000), count)
}

func TestGetBlockCount_Error(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getblockcount", func(_ []interface{}) (interface{}, *RPCError) {
		return nil, &RPCError{Code: -28, Message: "Loading block index..."}
	})

	count, err := client.GetBlockCount(context.Background())
	require.Error(t, err)
	assert.Equal(t, int64(0), count)
	assert.Contains(t, err.Error(), "getblockcount")
	assert.Contains(t, err.Error(), "Loading block index")
}

// ===========================================================================
// GetBlockHash
// ===========================================================================

func TestGetBlockHash_Success(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	expectedHash := "00000000000000000002a7c4c1e48d76c5a37902165a270156b7a8d72f6e4b0a"

	router.handle("getblockhash", func(params []interface{}) (interface{}, *RPCError) {
		// Verify height parameter was passed.
		require.Len(t, params, 1)
		// JSON numbers arrive as float64 after unmarshalling.
		height, ok := params[0].(float64)
		require.True(t, ok, "height param should be float64, got %T", params[0])
		assert.Equal(t, float64(840000), height)
		return expectedHash, nil
	})

	hash, err := client.GetBlockHash(context.Background(), 840000)
	require.NoError(t, err)
	assert.Equal(t, expectedHash, hash)
}

func TestGetBlockHash_Error(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getblockhash", func(_ []interface{}) (interface{}, *RPCError) {
		return nil, &RPCError{Code: -8, Message: "Block height out of range"}
	})

	hash, err := client.GetBlockHash(context.Background(), 999999999)
	require.Error(t, err)
	assert.Empty(t, hash)
	assert.Contains(t, err.Error(), "getblockhash")
	assert.Contains(t, err.Error(), "Block height out of range")
}

// ===========================================================================
// GetBlock
// ===========================================================================

func TestGetBlock_Success(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	expectedBlock := &Block{
		Hash:              "00000000000000000002a7c4c1e48d76c5a37902165a270156b7a8d72f6e4b0a",
		Height:            840000,
		Time:              1713571767,
		PreviousBlockHash: "0000000000000000000172014ba58d66455762add0512355ad651207918494ab",
		Tx: []*Transaction{
			{
				Txid:      "abc123",
				Hash:      "abc123hash",
				Blockhash: "00000000000000000002a7c4c1e48d76c5a37902165a270156b7a8d72f6e4b0a",
				Blocktime: 1713571767,
				Vin: []*Vin{
					{Coinbase: "0350cd0c..."},
				},
				Vout: []*Vout{
					{
						Value: "3.125",
						N:     0,
						ScriptPubKey: ScriptPubKey{
							Address: "bc1qxyz...",
							Type:    "witness_v1_taproot",
						},
					},
				},
			},
		},
	}

	router.handle("getblock", func(params []interface{}) (interface{}, *RPCError) {
		require.Len(t, params, 2)
		hash, ok := params[0].(string)
		require.True(t, ok)
		assert.Equal(t, expectedBlock.Hash, hash)
		verbosity, ok := params[1].(float64)
		require.True(t, ok)
		assert.Equal(t, float64(2), verbosity)
		return expectedBlock, nil
	})

	block, err := client.GetBlock(context.Background(), expectedBlock.Hash, 2)
	require.NoError(t, err)
	require.NotNil(t, block)
	assert.Equal(t, expectedBlock.Hash, block.Hash)
	assert.Equal(t, expectedBlock.Height, block.Height)
	assert.Equal(t, expectedBlock.Time, block.Time)
	assert.Equal(t, expectedBlock.PreviousBlockHash, block.PreviousBlockHash)
	require.Len(t, block.Tx, 1)
	assert.Equal(t, "abc123", block.Tx[0].Txid)
	require.Len(t, block.Tx[0].Vout, 1)
	assert.Equal(t, "bc1qxyz...", block.Tx[0].Vout[0].ScriptPubKey.Address)
}

func TestGetBlock_Null(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getblock", func(_ []interface{}) (interface{}, *RPCError) {
		// Return nil to produce a JSON "null" result.
		return nil, nil
	})

	block, err := client.GetBlock(context.Background(), "nonexistent", 2)
	require.NoError(t, err)
	assert.Nil(t, block, "should return nil for null result")
}

func TestGetBlock_Error(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getblock", func(_ []interface{}) (interface{}, *RPCError) {
		return nil, &RPCError{Code: -5, Message: "Block not found"}
	})

	block, err := client.GetBlock(context.Background(), "badhash", 2)
	require.Error(t, err)
	assert.Nil(t, block)
	assert.Contains(t, err.Error(), "getblock")
	assert.Contains(t, err.Error(), "Block not found")
}

// ===========================================================================
// GetBlockHeader
// ===========================================================================

func TestGetBlockHeader_Success(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	expectedHeader := &BlockHeader{
		Hash:              "00000000000000000002a7c4c1e48d76c5a37902165a270156b7a8d72f6e4b0a",
		Height:            840000,
		PreviousBlockHash: "0000000000000000000172014ba58d66455762add0512355ad651207918494ab",
		Time:              1713571767,
	}

	router.handle("getblockheader", func(params []interface{}) (interface{}, *RPCError) {
		require.Len(t, params, 2)
		hash, ok := params[0].(string)
		require.True(t, ok)
		assert.Equal(t, expectedHeader.Hash, hash)
		verbose, ok := params[1].(bool)
		require.True(t, ok)
		assert.True(t, verbose, "verbose should be true")
		return expectedHeader, nil
	})

	header, err := client.GetBlockHeader(context.Background(), expectedHeader.Hash)
	require.NoError(t, err)
	require.NotNil(t, header)
	assert.Equal(t, expectedHeader.Hash, header.Hash)
	assert.Equal(t, expectedHeader.Height, header.Height)
	assert.Equal(t, expectedHeader.PreviousBlockHash, header.PreviousBlockHash)
	assert.Equal(t, expectedHeader.Time, header.Time)
}

func TestGetBlockHeader_Null(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getblockheader", func(_ []interface{}) (interface{}, *RPCError) {
		return nil, nil
	})

	header, err := client.GetBlockHeader(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, header)
}

func TestGetBlockHeader_Error(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getblockheader", func(_ []interface{}) (interface{}, *RPCError) {
		return nil, &RPCError{Code: -5, Message: "Block not found"}
	})

	header, err := client.GetBlockHeader(context.Background(), "badhash")
	require.Error(t, err)
	assert.Nil(t, header)
	assert.Contains(t, err.Error(), "getblockheader")
}

// ===========================================================================
// GetRawTransactionVerbose
// ===========================================================================

func TestGetRawTransaction_Success(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	expectedTx := &Transaction{
		Txid:          "d5ada064c6596e4e0a65e6bcf9ef5d6e24be8c8d2c4f791ddbd0aef0bfee0e0e",
		Hash:          "d5ada064c6596e4e0a65e6bcf9ef5d6e24be8c8d2c4f791ddbd0aef0bfee0e0e",
		Blockhash:     "00000000000000000002a7c4c1e48d76c5a37902165a270156b7a8d72f6e4b0a",
		Blocktime:     1713571767,
		Time:          1713571767,
		Confirmations: 100,
		Vin: []*Vin{
			{
				Txid: "prevtxid123",
				Vout: 0,
			},
		},
		Vout: []*Vout{
			{
				Value: "0.5",
				N:     0,
				ScriptPubKey: ScriptPubKey{
					Address: "bc1qsender...",
					Type:    "witness_v0_keyhash",
				},
			},
			{
				Value: "1.23456789",
				N:     1,
				ScriptPubKey: ScriptPubKey{
					Address: "bc1qreceiver...",
					Type:    "witness_v0_keyhash",
				},
			},
		},
	}

	router.handle("getrawtransaction", func(params []interface{}) (interface{}, *RPCError) {
		require.Len(t, params, 2)
		txid, ok := params[0].(string)
		require.True(t, ok)
		assert.Equal(t, expectedTx.Txid, txid)
		verbose, ok := params[1].(bool)
		require.True(t, ok)
		assert.True(t, verbose, "verbose should be true")
		return expectedTx, nil
	})

	tx, err := client.GetRawTransactionVerbose(context.Background(), expectedTx.Txid)
	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.Equal(t, expectedTx.Txid, tx.Txid)
	assert.Equal(t, expectedTx.Blockhash, tx.Blockhash)
	assert.Equal(t, expectedTx.Confirmations, tx.Confirmations)
	require.Len(t, tx.Vin, 1)
	assert.Equal(t, "prevtxid123", tx.Vin[0].Txid)
	require.Len(t, tx.Vout, 2)
	assert.Equal(t, "bc1qsender...", tx.Vout[0].ScriptPubKey.Address)
	assert.Equal(t, "bc1qreceiver...", tx.Vout[1].ScriptPubKey.Address)
}

func TestGetRawTransaction_Null(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getrawtransaction", func(_ []interface{}) (interface{}, *RPCError) {
		return nil, nil
	})

	tx, err := client.GetRawTransactionVerbose(context.Background(), "nonexistent_txid")
	require.NoError(t, err)
	assert.Nil(t, tx, "should return nil for null result")
}

func TestGetRawTransaction_Error(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getrawtransaction", func(_ []interface{}) (interface{}, *RPCError) {
		return nil, &RPCError{Code: -5, Message: "No such mempool or blockchain transaction"}
	})

	tx, err := client.GetRawTransactionVerbose(context.Background(), "badtxid")
	require.Error(t, err)
	assert.Nil(t, tx)
	assert.Contains(t, err.Error(), "getrawtransaction")
	assert.Contains(t, err.Error(), "No such mempool or blockchain transaction")
}

// ===========================================================================
// Rate limiter integration verification
// ===========================================================================

func TestRateLimiter_Integration(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getblockcount", func(_ []interface{}) (interface{}, *RPCError) {
		return int64(840000), nil
	})

	// Create a rate limiter with a generous limit so the test does not take long,
	// but low enough that we can verify it is being invoked.
	// 100 rps with burst of 10 is sufficient for testing.
	limiter := ratelimit.NewLimiter(100, 10, "btc")
	client.SetRateLimiter(limiter)

	// Make several calls and verify they all succeed through the rate limiter.
	for i := 0; i < 5; i++ {
		count, err := client.GetBlockCount(context.Background())
		require.NoError(t, err)
		assert.Equal(t, int64(840000), count)
	}

	// Verify the server received all 5 requests.
	assert.Equal(t, int64(5), router.calls.Load())
}

func TestRateLimiter_ContextCancellation(t *testing.T) {
	// Create a very restrictive rate limiter: 1 rps, burst of 1.
	// First call consumes the burst, second call must wait.
	limiter := ratelimit.NewLimiter(1, 1, "btc")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		resp, _ := json.Marshal(Response{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`840000`),
		})
		_, _ = w.Write(resp)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, newTestLogger())
	client.SetRateLimiter(limiter)

	// First call should succeed (uses burst token).
	count, err := client.GetBlockCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(840000), count)

	// Second call with an already-cancelled context should fail on the rate limiter.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.GetBlockCount(ctx)
	require.Error(t, err)
	// The error message should come from the rate limiter or context cancellation.
	// Depending on timing, it could be "rate limiter: context canceled"
	// or the context error could surface in the HTTP request.
}

// ===========================================================================
// Edge cases
// ===========================================================================

func TestGetBlock_WithDifferentVerbosityLevels(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	var receivedVerbosity float64

	router.handle("getblock", func(params []interface{}) (interface{}, *RPCError) {
		if len(params) >= 2 {
			receivedVerbosity = params[1].(float64)
		}
		return &Block{
			Hash:   "testhash",
			Height: 100,
		}, nil
	})

	// Test verbosity 0.
	_, err := client.GetBlock(context.Background(), "testhash", 0)
	require.NoError(t, err)
	assert.Equal(t, float64(0), receivedVerbosity)

	// Test verbosity 1.
	_, err = client.GetBlock(context.Background(), "testhash", 1)
	require.NoError(t, err)
	assert.Equal(t, float64(1), receivedVerbosity)

	// Test verbosity 2.
	_, err = client.GetBlock(context.Background(), "testhash", 2)
	require.NoError(t, err)
	assert.Equal(t, float64(2), receivedVerbosity)
}

func TestGetBlock_FullTransactionDetails(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getblock", func(_ []interface{}) (interface{}, *RPCError) {
		return &Block{
			Hash:   "blockhash",
			Height: 840000,
			Time:   1713571767,
			Tx: []*Transaction{
				{
					Txid: "coinbase_tx",
					Vin:  []*Vin{{Coinbase: "0350cd0c"}},
					Vout: []*Vout{
						{Value: "3.125", N: 0, ScriptPubKey: ScriptPubKey{Address: "miner_addr", Type: "witness_v1_taproot"}},
					},
				},
				{
					Txid: "regular_tx",
					Vin:  []*Vin{{Txid: "prev_tx", Vout: 1}},
					Vout: []*Vout{
						{Value: "1.0", N: 0, ScriptPubKey: ScriptPubKey{Address: "addr1", Type: "witness_v0_keyhash"}},
						{Value: "0.5", N: 1, ScriptPubKey: ScriptPubKey{Address: "addr2", Type: "witness_v0_keyhash"}},
					},
				},
			},
		}, nil
	})

	block, err := client.GetBlock(context.Background(), "blockhash", 2)
	require.NoError(t, err)
	require.NotNil(t, block)
	require.Len(t, block.Tx, 2)

	// Verify coinbase transaction.
	coinbaseTx := block.Tx[0]
	assert.Equal(t, "coinbase_tx", coinbaseTx.Txid)
	require.Len(t, coinbaseTx.Vin, 1)
	assert.NotEmpty(t, coinbaseTx.Vin[0].Coinbase)

	// Verify regular transaction.
	regularTx := block.Tx[1]
	assert.Equal(t, "regular_tx", regularTx.Txid)
	require.Len(t, regularTx.Vin, 1)
	assert.Equal(t, "prev_tx", regularTx.Vin[0].Txid)
	require.Len(t, regularTx.Vout, 2)
}

func TestGetRawTransaction_CoinbaseTransaction(t *testing.T) {
	client, router, cleanup := newClientWithRouter()
	defer cleanup()

	router.handle("getrawtransaction", func(_ []interface{}) (interface{}, *RPCError) {
		return &Transaction{
			Txid:      "coinbase_txid",
			Blockhash: "blockhash",
			Blocktime: 1713571767,
			Vin: []*Vin{
				{Coinbase: "0350cd0c04..."},
			},
			Vout: []*Vout{
				{
					Value: "6.25",
					N:     0,
					ScriptPubKey: ScriptPubKey{
						Address: "bc1q_miner_address",
						Type:    "witness_v1_taproot",
					},
				},
			},
		}, nil
	})

	tx, err := client.GetRawTransactionVerbose(context.Background(), "coinbase_txid")
	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.Equal(t, "coinbase_txid", tx.Txid)
	require.Len(t, tx.Vin, 1)
	assert.NotEmpty(t, tx.Vin[0].Coinbase)
	assert.Empty(t, tx.Vin[0].Txid, "coinbase input should have no txid")
}

func TestGetBlockHeader_NullResult(t *testing.T) {
	// Use raw httptest server to return explicit null result.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
	}))
	defer ts.Close()

	client := NewClient(ts.URL, newTestLogger())
	header, err := client.GetBlockHeader(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, header)
}

func TestMethods_HTTPErrorPropagation(t *testing.T) {
	// Verify that each method properly wraps HTTP-level errors.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("service unavailable"))
	}))
	defer ts.Close()

	client := NewClient(ts.URL, newTestLogger())
	ctx := context.Background()

	t.Run("GetBlockCount", func(t *testing.T) {
		_, err := client.GetBlockCount(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getblockcount")
	})

	t.Run("GetBlockHash", func(t *testing.T) {
		_, err := client.GetBlockHash(ctx, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getblockhash")
	})

	t.Run("GetBlock", func(t *testing.T) {
		_, err := client.GetBlock(ctx, "hash", 2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getblock")
	})

	t.Run("GetBlockHeader", func(t *testing.T) {
		_, err := client.GetBlockHeader(ctx, "hash")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getblockheader")
	})

	t.Run("GetRawTransactionVerbose", func(t *testing.T) {
		_, err := client.GetRawTransactionVerbose(ctx, "txid")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getrawtransaction")
	})
}
