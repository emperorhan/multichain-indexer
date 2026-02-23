package rpc

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newTestLogger returns a no-op logger suitable for tests.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// rpcOK returns a properly formatted JSON-RPC 2.0 success response.
func rpcOK(result interface{}) []byte {
	raw, _ := json.Marshal(result)
	resp := Response{
		JSONRPC: "2.0",
		ID:      1,
		Result:  raw,
	}
	b, _ := json.Marshal(resp)
	return b
}

// rpcError returns a properly formatted JSON-RPC 2.0 error response.
func rpcError(code int, msg string) []byte {
	resp := struct {
		JSONRPC string    `json:"jsonrpc"`
		ID      int       `json:"id"`
		Error   *RPCError `json:"error"`
	}{
		JSONRPC: "2.0",
		ID:      1,
		Error:   &RPCError{Code: code, Message: msg},
	}
	b, _ := json.Marshal(resp)
	return b
}

// newMockServer creates an httptest.Server that responds with the given handler.
// Caller should defer ts.Close().
func newMockServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

// ---------------------------------------------------------------------------
// TestNewClient_Construction
// ---------------------------------------------------------------------------

func TestNewClient_Construction(t *testing.T) {
	logger := newTestLogger()
	url := "http://localhost:8332"

	client := NewClient(url, logger)

	require.NotNil(t, client)
	assert.Equal(t, url, client.rpcURL)
	assert.NotNil(t, client.httpClient)
	assert.NotNil(t, client.logger)
	assert.Nil(t, client.limiter, "limiter should be nil by default")
	assert.Equal(t, int64(0), client.requestID.Load(), "requestID should start at 0")
}

// ---------------------------------------------------------------------------
// TestClient_CallSuccess
// ---------------------------------------------------------------------------

func TestClient_CallSuccess(t *testing.T) {
	var receivedReq Request

	ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		// Validate the incoming HTTP request.
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedReq))

		w.WriteHeader(http.StatusOK)
		_, err = w.Write(rpcOK(int64(840000)))
		require.NoError(t, err)
	})
	defer ts.Close()

	client := NewClient(ts.URL, newTestLogger())

	result, err := client.call(context.Background(), "getblockcount", []interface{}{})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify the JSON-RPC request was properly formatted.
	assert.Equal(t, "2.0", receivedReq.JSONRPC)
	assert.Equal(t, "getblockcount", receivedReq.Method)
	assert.Equal(t, 1, receivedReq.ID)

	// Verify the result can be decoded.
	var count int64
	require.NoError(t, json.Unmarshal(result, &count))
	assert.Equal(t, int64(840000), count)
}

// ---------------------------------------------------------------------------
// TestClient_CallRPCError
// ---------------------------------------------------------------------------

func TestClient_CallRPCError(t *testing.T) {
	ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(rpcError(-1, "Block not found"))
		require.NoError(t, err)
	})
	defer ts.Close()

	client := NewClient(ts.URL, newTestLogger())

	result, err := client.call(context.Background(), "getblock", []interface{}{"badhash", 2})
	require.Error(t, err)
	assert.Nil(t, result)

	// The error should be an *RPCError (returned directly from the call method).
	var rpcErr *RPCError
	assert.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, -1, rpcErr.Code)
	assert.Equal(t, "Block not found", rpcErr.Message)
	assert.Contains(t, rpcErr.Error(), "Block not found")
}

// ---------------------------------------------------------------------------
// TestClient_CallHTTPError
// ---------------------------------------------------------------------------

func TestClient_CallHTTPError(t *testing.T) {
	t.Run("non-200 status code", func(t *testing.T) {
		ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte("internal error"))
			require.NoError(t, err)
		})
		defer ts.Close()

		client := NewClient(ts.URL, newTestLogger())

		result, err := client.call(context.Background(), "getblockcount", []interface{}{})
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "http status 500")
		assert.Contains(t, err.Error(), "internal error")
	})

	t.Run("connection refused", func(t *testing.T) {
		// Use an address that is guaranteed to refuse connections.
		client := NewClient("http://127.0.0.1:1", newTestLogger())

		result, err := client.call(context.Background(), "getblockcount", []interface{}{})
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "http request")
	})

	t.Run("context cancelled", func(t *testing.T) {
		ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
			// This handler should never be reached because context is cancelled.
			w.WriteHeader(http.StatusOK)
			_, err := w.Write(rpcOK(int64(100)))
			require.NoError(t, err)
		})
		defer ts.Close()

		client := NewClient(ts.URL, newTestLogger())

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately.

		result, err := client.call(ctx, "getblockcount", []interface{}{})
		require.Error(t, err)
		assert.Nil(t, result)
	})
}

// ---------------------------------------------------------------------------
// TestClient_CallMalformedJSON
// ---------------------------------------------------------------------------

func TestClient_CallMalformedJSON(t *testing.T) {
	t.Run("completely invalid JSON", func(t *testing.T) {
		ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("this is not json at all"))
			require.NoError(t, err)
		})
		defer ts.Close()

		client := NewClient(ts.URL, newTestLogger())

		result, err := client.call(context.Background(), "getblockcount", []interface{}{})
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "unmarshal response")
	})

	t.Run("truncated JSON", func(t *testing.T) {
		ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":`))
			require.NoError(t, err)
		})
		defer ts.Close()

		client := NewClient(ts.URL, newTestLogger())

		result, err := client.call(context.Background(), "getblockcount", []interface{}{})
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "unmarshal response")
	})

	t.Run("valid JSON but wrong structure", func(t *testing.T) {
		ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"foo":"bar"}`))
			require.NoError(t, err)
		})
		defer ts.Close()

		client := NewClient(ts.URL, newTestLogger())

		result, err := client.call(context.Background(), "getblockcount", []interface{}{})
		// This succeeds because json.Unmarshal tolerates missing fields;
		// Result will be nil (zero-value for json.RawMessage).
		require.NoError(t, err)
		assert.Nil(t, result)
	})
}

// ---------------------------------------------------------------------------
// TestClient_RequestIDIncrement
// ---------------------------------------------------------------------------

func TestClient_RequestIDIncrement(t *testing.T) {
	var ids []int

	ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req Request
		_ = json.Unmarshal(body, &req)
		ids = append(ids, req.ID)

		w.WriteHeader(http.StatusOK)
		_, err := w.Write(rpcOK(int64(1)))
		require.NoError(t, err)
	})
	defer ts.Close()

	client := NewClient(ts.URL, newTestLogger())

	for i := 0; i < 3; i++ {
		_, _ = client.call(context.Background(), "getblockcount", []interface{}{})
	}

	require.Len(t, ids, 3)
	assert.Equal(t, 1, ids[0])
	assert.Equal(t, 2, ids[1])
	assert.Equal(t, 3, ids[2])
}

// ---------------------------------------------------------------------------
// TestClient_SetRateLimiter
// ---------------------------------------------------------------------------

func TestClient_SetRateLimiter(t *testing.T) {
	client := NewClient("http://localhost:8332", newTestLogger())
	assert.Nil(t, client.limiter)

	// We cannot easily construct a ratelimit.Limiter without the metrics
	// dependency, so we just verify the setter does not panic and sets the field.
	// The full rate-limiter integration is tested in methods_test.go.
	client.SetRateLimiter(nil)
	assert.Nil(t, client.limiter)
}

// ---------------------------------------------------------------------------
// TestClient_NewRequest
// ---------------------------------------------------------------------------

func TestClient_NewRequest(t *testing.T) {
	client := NewClient("http://localhost:8332", newTestLogger())

	req := client.newRequest("getblock", []interface{}{"abc123", 2})
	assert.Equal(t, "2.0", req.JSONRPC)
	assert.Equal(t, 1, req.ID)
	assert.Equal(t, "getblock", req.Method)
	require.Len(t, req.Params, 2)
	assert.Equal(t, "abc123", req.Params[0])
	assert.Equal(t, 2, req.Params[1])

	// Second request should have incremented ID.
	req2 := client.newRequest("getblockhash", []interface{}{42})
	assert.Equal(t, 2, req2.ID)
}
