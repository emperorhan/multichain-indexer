package rpc

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestClient(handler func(*http.Request) (*http.Response, error)) *Client {
	client := NewClient("http://rpc.local", slog.Default())
	client.httpClient = &http.Client{
		Transport: roundTripFunc(handler),
	}
	return client
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestCall_Success(t *testing.T) {
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req Request
		require.NoError(t, json.Unmarshal(body, &req))

		assert.Equal(t, "2.0", req.JSONRPC)
		assert.Equal(t, "eth_testMethod", req.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`"0x2a"`),
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	result, err := client.call(context.Background(), "eth_testMethod", []interface{}{"p1"})
	require.NoError(t, err)

	var value string
	require.NoError(t, json.Unmarshal(result, &value))
	assert.Equal(t, "0x2a", value)
}

func TestCall_RPCError(t *testing.T) {
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		resp := Response{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &RPCError{Code: -32000, Message: "upstream unavailable"},
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	_, err := client.call(context.Background(), "eth_testMethod", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upstream unavailable")
}

func TestCall_HTTPError(t *testing.T) {
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(http.StatusBadGateway, "bad gateway"), nil
	})

	_, err := client.call(context.Background(), "eth_testMethod", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http status 502")
}

func TestParseHexInt64(t *testing.T) {
	value, err := ParseHexInt64("0x2a")
	require.NoError(t, err)
	assert.Equal(t, int64(42), value)

	zero, err := ParseHexInt64("0x")
	require.NoError(t, err)
	assert.Zero(t, zero)

	_, err = ParseHexInt64("nope")
	require.Error(t, err)
}

func TestCallBatch_Success(t *testing.T) {
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var reqs []Request
		require.NoError(t, json.Unmarshal(body, &reqs))
		require.Len(t, reqs, 2)

		// Return reversed to verify ID-based reordering.
		resp := []Response{
			{JSONRPC: "2.0", ID: reqs[1].ID, Result: json.RawMessage(`"b"`)},
			{JSONRPC: "2.0", ID: reqs[0].ID, Result: json.RawMessage(`"a"`)},
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	requests := []Request{
		client.newRequest("m1", []interface{}{}),
		client.newRequest("m2", []interface{}{}),
	}
	results, err := client.callBatch(context.Background(), requests)
	require.NoError(t, err)
	require.Len(t, results, 2)

	var first string
	require.NoError(t, json.Unmarshal(results[0].Result, &first))
	assert.Equal(t, "a", first)
	var second string
	require.NoError(t, json.Unmarshal(results[1].Result, &second))
	assert.Equal(t, "b", second)
}

func TestCallBatch_MissingResponse(t *testing.T) {
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var reqs []Request
		require.NoError(t, json.Unmarshal(body, &reqs))
		require.Len(t, reqs, 2)

		resp := []Response{
			{JSONRPC: "2.0", ID: reqs[0].ID, Result: json.RawMessage(`null`)},
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	requests := []Request{
		client.newRequest("m1", []interface{}{}),
		client.newRequest("m2", []interface{}{}),
	}
	_, err := client.callBatch(context.Background(), requests)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing batch response")
}
