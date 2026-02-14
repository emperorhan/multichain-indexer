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

func TestGetSlot_Success(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req Request
		require.NoError(t, json.Unmarshal(body, &req))

		assert.Equal(t, "getSlot", req.Method)
		assert.Len(t, req.Params, 1)

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`123456789`),
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	slot, err := client.GetSlot(context.Background(), "confirmed")
	require.NoError(t, err)
	assert.Equal(t, int64(123456789), slot)
}

func TestGetSlot_Error(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		resp := Response{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &RPCError{Code: -32000, Message: "slot not available"},
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	_, err := client.GetSlot(context.Background(), "confirmed")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slot not available")
}

func TestGetSignaturesForAddress_Success(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req Request
		require.NoError(t, json.Unmarshal(body, &req))

		assert.Equal(t, "getSignaturesForAddress", req.Method)
		assert.Len(t, req.Params, 2)
		assert.Equal(t, "testAddr", req.Params[0])

		sigs := []SignatureInfo{
			{Signature: "sig1", Slot: 100},
			{Signature: "sig2", Slot: 101},
		}
		result, err := json.Marshal(sigs)
		require.NoError(t, err)
		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

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
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req Request
		require.NoError(t, json.Unmarshal(body, &req))

		config := req.Params[1].(map[string]interface{})
		assert.Equal(t, float64(50), config["limit"])
		assert.Equal(t, "beforeSig", config["before"])
		assert.Equal(t, "untilSig", config["until"])

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`[]`),
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

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
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req Request
		require.NoError(t, json.Unmarshal(body, &req))

		config := req.Params[1].(map[string]interface{})
		assert.Equal(t, "confirmed", config["commitment"])
		_, hasLimit := config["limit"]
		assert.False(t, hasLimit)

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`[]`),
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	sigs, err := client.GetSignaturesForAddress(context.Background(), "addr", nil)
	require.NoError(t, err)
	assert.Empty(t, sigs)
}

func TestGetTransaction_Success(t *testing.T) {
	expectedResult := json.RawMessage(`{"slot":100,"transaction":{"message":{}},"meta":{}}`)

	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req Request
		require.NoError(t, json.Unmarshal(body, &req))

		assert.Equal(t, "getTransaction", req.Method)
		assert.Equal(t, "testSig", req.Params[0])

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  expectedResult,
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	result, err := client.GetTransaction(context.Background(), "testSig")
	require.NoError(t, err)
	assert.JSONEq(t, string(expectedResult), string(result))
}

func TestGetTransactions_Batch(t *testing.T) {
	client := methodTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var reqs []Request
		require.NoError(t, json.Unmarshal(body, &reqs))
		require.Len(t, reqs, 2)
		assert.Equal(t, "getTransaction", reqs[0].Method)
		assert.Equal(t, "getTransaction", reqs[1].Method)

		resp := []Response{
			{
				JSONRPC: "2.0",
				ID:      reqs[0].ID,
				Result:  json.RawMessage(`{"slot":100}`),
			},
			{
				JSONRPC: "2.0",
				ID:      reqs[1].ID,
				Result:  json.RawMessage(`{"slot":200}`),
			},
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	results, err := client.GetTransactions(context.Background(), []string{"sig1", "sig2"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.JSONEq(t, `{"slot":100}`, string(results[0]))
	assert.JSONEq(t, `{"slot":200}`, string(results[1]))
}
