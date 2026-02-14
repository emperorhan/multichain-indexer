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
		assert.Equal(t, "testMethod", req.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`42`),
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	result, err := client.call(context.Background(), "testMethod", []interface{}{"param1"})
	require.NoError(t, err)

	var val int
	require.NoError(t, json.Unmarshal(result, &val))
	assert.Equal(t, 42, val)
}

func TestCall_RPCError(t *testing.T) {
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		resp := Response{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &RPCError{Code: -32600, Message: "Invalid Request"},
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	_, err := client.call(context.Background(), "testMethod", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid Request")
}

func TestCall_HTTPError(t *testing.T) {
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(http.StatusInternalServerError, "internal server error"), nil
	})

	_, err := client.call(context.Background(), "testMethod", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http status 500")
}

func TestCall_InvalidJSON(t *testing.T) {
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(http.StatusOK, "not json"), nil
	})

	_, err := client.call(context.Background(), "testMethod", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal response")
}

func TestCall_ContextCanceled(t *testing.T) {
	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.call(ctx, "testMethod", nil)
	require.Error(t, err)
}

func TestCall_RequestIDIncrement(t *testing.T) {
	var receivedIDs []int

	client := newTestClient(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req Request
		require.NoError(t, json.Unmarshal(body, &req))
		receivedIDs = append(receivedIDs, req.ID)

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`null`),
		}
		rawResp, err := json.Marshal(resp)
		require.NoError(t, err)
		return jsonHTTPResponse(http.StatusOK, string(rawResp)), nil
	})

	_, err := client.call(context.Background(), "m1", nil)
	require.NoError(t, err)
	_, err = client.call(context.Background(), "m2", nil)
	require.NoError(t, err)
	_, err = client.call(context.Background(), "m3", nil)
	require.NoError(t, err)

	require.Len(t, receivedIDs, 3)
	assert.Equal(t, 1, receivedIDs[0])
	assert.Equal(t, 2, receivedIDs[1])
	assert.Equal(t, 3, receivedIDs[2])
}
