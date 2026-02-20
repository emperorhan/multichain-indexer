package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testAlert() Alert {
	return Alert{
		Type:    AlertTypeUnhealthy,
		Chain:   "solana",
		Network: "devnet",
		Title:   "Node unreachable",
		Message: "RPC endpoint is not responding",
		Fields: map[string]string{
			"endpoint": "https://api.devnet.solana.com",
			"downtime": "5m",
		},
	}
}

// TestMultiAlerter_Send_AllChannels verifies that MultiAlerter fans out to
// every registered alerter (Slack + webhook) on a single Send call.
func TestMultiAlerter_Send_AllChannels(t *testing.T) {
	var slackReceived atomic.Int32
	var webhookReceived atomic.Int32

	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slackReceived.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer slackSrv.Close()

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookReceived.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookSrv.Close()

	slack := NewSlackAlerter(slackSrv.URL)
	webhook := NewWebhookAlerter(webhookSrv.URL)

	multi := NewMultiAlerter(time.Hour, testLogger(), slack, webhook)

	err := multi.Send(context.Background(), testAlert())
	require.NoError(t, err)

	assert.Equal(t, int32(1), slackReceived.Load(), "Slack server should receive exactly 1 request")
	assert.Equal(t, int32(1), webhookReceived.Load(), "Webhook server should receive exactly 1 request")
}

// TestMultiAlerter_CooldownDedup verifies that sending the same alert twice
// within the cooldown window only dispatches one actual request.
func TestMultiAlerter_CooldownDedup(t *testing.T) {
	var received atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	webhook := NewWebhookAlerter(srv.URL)
	multi := NewMultiAlerter(time.Second, testLogger(), webhook)

	alert := testAlert()

	err := multi.Send(context.Background(), alert)
	require.NoError(t, err)

	// Send the same alert again immediately; should be suppressed.
	err = multi.Send(context.Background(), alert)
	require.NoError(t, err)

	assert.Equal(t, int32(1), received.Load(), "Only the first send should go through; second should be deduped by cooldown")
}

// TestMultiAlerter_CooldownExpiry verifies that after the cooldown window
// expires, a duplicate alert is dispatched again.
func TestMultiAlerter_CooldownExpiry(t *testing.T) {
	var received atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	webhook := NewWebhookAlerter(srv.URL)
	// Use a very short cooldown so the test runs fast.
	multi := NewMultiAlerter(time.Millisecond, testLogger(), webhook)

	alert := testAlert()

	err := multi.Send(context.Background(), alert)
	require.NoError(t, err)

	// Wait for the cooldown to expire.
	time.Sleep(5 * time.Millisecond)

	err = multi.Send(context.Background(), alert)
	require.NoError(t, err)

	assert.Equal(t, int32(2), received.Load(), "Both sends should go through after cooldown expires")
}

// TestMultiAlerter_PartialFailure verifies that when one alerter fails,
// the MultiAlerter returns an error but the working alerter still receives
// the alert.
func TestMultiAlerter_PartialFailure(t *testing.T) {
	var goodReceived atomic.Int32

	// Failing server returns 500.
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	// Good server returns 200.
	goodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodReceived.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer goodSrv.Close()

	failAlerter := NewWebhookAlerter(failSrv.URL)
	goodAlerter := NewWebhookAlerter(goodSrv.URL)

	multi := NewMultiAlerter(time.Hour, testLogger(), failAlerter, goodAlerter)

	err := multi.Send(context.Background(), testAlert())
	assert.Error(t, err, "MultiAlerter should return error when one alerter fails")
	assert.Equal(t, int32(1), goodReceived.Load(), "Good alerter should still receive the alert despite partial failure")
}

// TestSlackAlerter_PayloadFormat verifies the JSON payload sent to the Slack
// webhook contains the expected "text" field with emoji, type, chain, network,
// title, and message.
func TestSlackAlerter_PayloadFormat(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	slack := NewSlackAlerter(srv.URL)

	alert := Alert{
		Type:    AlertTypeReorg,
		Chain:   "ethereum",
		Network: "mainnet",
		Title:   "Block reorganization detected",
		Message: "Depth: 3 blocks",
		Fields: map[string]string{
			"from_block": "1000",
			"to_block":   "1003",
		},
	}

	err := slack.Send(context.Background(), alert)
	require.NoError(t, err)
	require.NotEmpty(t, capturedBody, "Server should have received a request body")

	var payload map[string]string
	err = json.Unmarshal(capturedBody, &payload)
	require.NoError(t, err, "Payload should be valid JSON")

	text, ok := payload["text"]
	require.True(t, ok, "Payload must have a 'text' field")

	// Verify expected content in the text field.
	assert.Contains(t, text, ":rotating_light:", "Reorg alert should use rotating_light emoji")
	assert.Contains(t, text, string(AlertTypeReorg), "Text should contain the alert type")
	assert.Contains(t, text, "ethereum", "Text should contain the chain")
	assert.Contains(t, text, "mainnet", "Text should contain the network")
	assert.Contains(t, text, "Block reorganization detected", "Text should contain the title")
	assert.Contains(t, text, "Depth: 3 blocks", "Text should contain the message")

	// Verify all emoji mappings.
	emojiTests := []struct {
		alertType AlertType
		emoji     string
	}{
		{AlertTypeUnhealthy, ":warning:"},
		{AlertTypeRecovery, ":white_check_mark:"},
		{AlertTypeReorg, ":rotating_light:"},
		{AlertTypeScamToken, ":no_entry:"},
		{AlertTypeReconcileErr, ":scales:"},
	}
	for _, tc := range emojiTests {
		t.Run(fmt.Sprintf("emoji_%s", tc.alertType), func(t *testing.T) {
			var body []byte
			emojiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				body = b
				w.WriteHeader(http.StatusOK)
			}))
			defer emojiSrv.Close()

			s := NewSlackAlerter(emojiSrv.URL)
			a := Alert{Type: tc.alertType, Chain: "sol", Network: "dev", Title: "t", Message: "m"}
			err := s.Send(context.Background(), a)
			require.NoError(t, err)

			var p map[string]string
			require.NoError(t, json.Unmarshal(body, &p))
			assert.True(t, strings.HasPrefix(p["text"], tc.emoji),
				"Alert type %s should start with emoji %s, got: %s", tc.alertType, tc.emoji, p["text"])
		})
	}
}

// TestWebhookAlerter_PayloadFormat verifies the JSON payload sent to the
// generic webhook contains type, chain, network, title, message, fields,
// and time fields.
func TestWebhookAlerter_PayloadFormat(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	webhook := NewWebhookAlerter(srv.URL)

	alert := Alert{
		Type:    AlertTypeReconcileErr,
		Chain:   "solana",
		Network: "devnet",
		Title:   "Balance mismatch",
		Message: "On-chain balance differs from DB by 1.5 SOL",
		Fields: map[string]string{
			"address":    "ABC123",
			"on_chain":   "10.5",
			"db_balance": "9.0",
		},
	}

	beforeSend := time.Now().UTC().Truncate(time.Second)
	err := webhook.Send(context.Background(), alert)
	require.NoError(t, err)
	require.NotEmpty(t, capturedBody, "Server should have received a request body")

	var payload map[string]any
	err = json.Unmarshal(capturedBody, &payload)
	require.NoError(t, err, "Payload should be valid JSON")

	// Verify top-level string fields.
	assert.Equal(t, string(AlertTypeReconcileErr), payload["type"])
	assert.Equal(t, "solana", payload["chain"])
	assert.Equal(t, "devnet", payload["network"])
	assert.Equal(t, "Balance mismatch", payload["title"])
	assert.Equal(t, "On-chain balance differs from DB by 1.5 SOL", payload["message"])

	// Verify fields map.
	fields, ok := payload["fields"].(map[string]any)
	require.True(t, ok, "Payload must have a 'fields' object")
	assert.Equal(t, "ABC123", fields["address"])
	assert.Equal(t, "10.5", fields["on_chain"])
	assert.Equal(t, "9.0", fields["db_balance"])

	// Verify time field is a valid RFC3339 timestamp close to now.
	timeStr, ok := payload["time"].(string)
	require.True(t, ok, "Payload must have a 'time' string field")
	parsedTime, err := time.Parse(time.RFC3339, timeStr)
	require.NoError(t, err, "Time field must be valid RFC3339")
	assert.False(t, parsedTime.Before(beforeSend), "Timestamp should not be before the send call")
	assert.WithinDuration(t, time.Now().UTC(), parsedTime, 5*time.Second, "Timestamp should be close to now")
}
