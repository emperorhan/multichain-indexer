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

func methodTestClient(handler http.HandlerFunc) (*Client, *httptest.Server) {
	server := httptest.NewServer(handler)
	client := NewClient(server.URL, slog.Default())
	return client, server
}

func TestGetSlot_Success(t *testing.T) {
	client, server := methodTestClient(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req Request
		json.Unmarshal(body, &req)

		assert.Equal(t, "getSlot", req.Method)
		assert.Len(t, req.Params, 1)

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`123456789`),
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	slot, err := client.GetSlot(context.Background(), "confirmed")
	require.NoError(t, err)
	assert.Equal(t, int64(123456789), slot)
}

func TestGetSlot_Error(t *testing.T) {
	client, server := methodTestClient(func(w http.ResponseWriter, r *http.Request) {
		resp := Response{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &RPCError{Code: -32000, Message: "slot not available"},
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	_, err := client.GetSlot(context.Background(), "confirmed")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slot not available")
}

func TestGetSignaturesForAddress_Success(t *testing.T) {
	client, server := methodTestClient(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req Request
		json.Unmarshal(body, &req)

		assert.Equal(t, "getSignaturesForAddress", req.Method)
		assert.Len(t, req.Params, 2)
		assert.Equal(t, "testAddr", req.Params[0])

		sigs := []SignatureInfo{
			{Signature: "sig1", Slot: 100},
			{Signature: "sig2", Slot: 101},
		}
		result, _ := json.Marshal(sigs)
		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	opts := &GetSignaturesOpts{Limit: 10}
	sigs, err := client.GetSignaturesForAddress(context.Background(), "testAddr", opts)
	require.NoError(t, err)
	require.Len(t, sigs, 2)
	assert.Equal(t, "sig1", sigs[0].Signature)
	assert.Equal(t, int64(100), sigs[0].Slot)
	assert.Equal(t, "sig2", sigs[1].Signature)
	assert.Equal(t, int64(101), sigs[1].Slot)
}

func TestGetSignaturesForAddress_OptsPassedCorrectly(t *testing.T) {
	client, server := methodTestClient(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req Request
		json.Unmarshal(body, &req)

		config := req.Params[1].(map[string]interface{})
		assert.Equal(t, float64(50), config["limit"])
		assert.Equal(t, "beforeSig", config["before"])
		assert.Equal(t, "untilSig", config["until"])

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`[]`),
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	opts := &GetSignaturesOpts{
		Limit:  50,
		Before: "beforeSig",
		Until:  "untilSig",
	}
	sigs, err := client.GetSignaturesForAddress(context.Background(), "addr", opts)
	require.NoError(t, err)
	assert.Empty(t, sigs)
}

func TestGetSignaturesForAddress_NilOpts(t *testing.T) {
	client, server := methodTestClient(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req Request
		json.Unmarshal(body, &req)

		config := req.Params[1].(map[string]interface{})
		assert.Equal(t, "confirmed", config["commitment"])
		_, hasLimit := config["limit"]
		assert.False(t, hasLimit)

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`[]`),
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	sigs, err := client.GetSignaturesForAddress(context.Background(), "addr", nil)
	require.NoError(t, err)
	assert.Empty(t, sigs)
}

func TestGetTransaction_Success(t *testing.T) {
	expectedResult := json.RawMessage(`{"slot":100,"transaction":{"message":{}},"meta":{}}`)

	client, server := methodTestClient(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req Request
		json.Unmarshal(body, &req)

		assert.Equal(t, "getTransaction", req.Method)
		assert.Equal(t, "testSig", req.Params[0])

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  expectedResult,
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	result, err := client.GetTransaction(context.Background(), "testSig")
	require.NoError(t, err)
	assert.JSONEq(t, string(expectedResult), string(result))
}
