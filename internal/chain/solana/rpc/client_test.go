package rpc

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(handler http.HandlerFunc) (*Client, *httptest.Server) {
	server := httptest.NewServer(handler)
	client := NewClient(server.URL, slog.Default())
	return client, server
}

func TestCall_Success(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req Request
		json.Unmarshal(body, &req)

		assert.Equal(t, "2.0", req.JSONRPC)
		assert.Equal(t, "testMethod", req.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`42`),
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	result, err := client.call(context.Background(), "testMethod", []interface{}{"param1"})
	require.NoError(t, err)

	var val int
	json.Unmarshal(result, &val)
	assert.Equal(t, 42, val)
}

func TestCall_RPCError(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		resp := Response{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &RPCError{Code: -32600, Message: "Invalid Request"},
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	_, err := client.call(context.Background(), "testMethod", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid Request")
}

func TestCall_HTTPError(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	})
	defer server.Close()

	_, err := client.call(context.Background(), "testMethod", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http status 500")
}

func TestCall_InvalidJSON(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	defer server.Close()

	_, err := client.call(context.Background(), "testMethod", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal response")
}

func TestCall_ContextCanceled(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		// Never respond
		<-r.Context().Done()
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.call(ctx, "testMethod", nil)
	require.Error(t, err)
}

func TestCall_RequestIDIncrement(t *testing.T) {
	var receivedIDs []int
	var mu atomic.Int64
	_ = mu

	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req Request
		json.Unmarshal(body, &req)
		receivedIDs = append(receivedIDs, req.ID)

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`null`),
		}
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	client.call(context.Background(), "m1", nil)
	client.call(context.Background(), "m2", nil)
	client.call(context.Background(), "m3", nil)

	require.Len(t, receivedIDs, 3)
	assert.Equal(t, 1, receivedIDs[0])
	assert.Equal(t, 2, receivedIDs[1])
	assert.Equal(t, 3, receivedIDs[2])
}
