package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/metrics"
)

// AlertType categorizes the kind of alert.
type AlertType string

const (
	AlertTypeUnhealthy    AlertType = "UNHEALTHY"
	AlertTypeRecovery     AlertType = "RECOVERY"
	AlertTypeReorg        AlertType = "REORG"
	AlertTypeScamToken    AlertType = "SCAM_TOKEN"
	AlertTypeReconcileErr AlertType = "RECONCILE_MISMATCH"
	AlertTypeDBPool       AlertType = "DB_POOL"
)

// Alert represents a single alert event.
type Alert struct {
	Type    AlertType
	Chain   string
	Network string
	Title   string
	Message string
	Fields  map[string]string
}

// Alerter is the interface for sending alerts.
type Alerter interface {
	Send(ctx context.Context, alert Alert) error
}

// MultiAlerter fans out alerts to multiple channels.
type MultiAlerter struct {
	alerters []Alerter
	cooldown time.Duration
	logger   *slog.Logger

	mu            sync.Mutex
	lastSent      map[string]time.Time
	lastAlertType map[string]AlertType // key: "chain:network" -> last alert type sent
}

// NewMultiAlerter creates a new multi-channel alerter with cooldown.
func NewMultiAlerter(cooldown time.Duration, logger *slog.Logger, alerters ...Alerter) *MultiAlerter {
	return &MultiAlerter{
		alerters:      alerters,
		cooldown:      cooldown,
		logger:        logger.With("component", "alerter"),
		lastSent:      make(map[string]time.Time),
		lastAlertType: make(map[string]AlertType),
	}
}

// cooldownKey generates a dedup key for cooldown tracking.
func cooldownKey(a Alert) string {
	return fmt.Sprintf("%s:%s:%s", a.Type, a.Chain, a.Network)
}

// stateKey generates a key for state-transition tracking (type-agnostic).
func stateKey(a Alert) string {
	return fmt.Sprintf("%s:%s", a.Chain, a.Network)
}

// Send dispatches alert to all channels, respecting cooldown.
// State-transition awareness: if the alert type changes for a given chain:network
// (e.g., UNHEALTHY -> RECOVERY), the cooldown timer is bypassed so that
// RECOVERY alerts are sent immediately after UNHEALTHY alerts.
func (m *MultiAlerter) Send(ctx context.Context, alert Alert) error {
	key := cooldownKey(alert)
	sk := stateKey(alert)

	m.mu.Lock()
	// Check for state transition: if the alert type changed for this chain:network,
	// bypass cooldown to allow immediate delivery (e.g., RECOVERY after UNHEALTHY).
	prevType, hasPrev := m.lastAlertType[sk]
	stateChanged := hasPrev && prevType != alert.Type

	if !stateChanged {
		if last, ok := m.lastSent[key]; ok && time.Since(last) < m.cooldown {
			m.mu.Unlock()
			m.logger.Debug("alert suppressed by cooldown", "key", key)
			for _, a := range m.alerters {
				channelName := alerterName(a)
				metrics.AlertsCooldownSkipped.WithLabelValues(channelName, string(alert.Type)).Inc()
			}
			return nil
		}
	}
	m.lastSent[key] = time.Now()
	m.lastAlertType[sk] = alert.Type
	m.mu.Unlock()

	if stateChanged {
		m.logger.Info("alert state transition detected, bypassing cooldown",
			"chain", alert.Chain,
			"network", alert.Network,
			"previous_type", prevType,
			"new_type", alert.Type,
		)
	}

	var firstErr error
	for _, a := range m.alerters {
		if err := a.Send(ctx, alert); err != nil {
			m.logger.Warn("alert send failed",
				"channel", alerterName(a),
				"type", alert.Type,
				"error", err,
			)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			metrics.AlertsSentTotal.WithLabelValues(alerterName(a), string(alert.Type)).Inc()
		}
	}
	return firstErr
}

func alerterName(a Alerter) string {
	switch a.(type) {
	case *SlackAlerter:
		return "slack"
	case *WebhookAlerter:
		return "webhook"
	default:
		return "unknown"
	}
}

// SlackAlerter sends alerts to a Slack webhook.
type SlackAlerter struct {
	webhookURL string
	client     *http.Client
}

// NewSlackAlerter creates a Slack alerter with the given webhook URL.
func NewSlackAlerter(webhookURL string) *SlackAlerter {
	return &SlackAlerter{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				MaxConnsPerHost:     10,
				IdleConnTimeout:     30 * time.Second,
			},
		},
	}
}

// Send sends an alert to Slack.
func (s *SlackAlerter) Send(ctx context.Context, alert Alert) error {
	emoji := ":warning:"
	switch alert.Type {
	case AlertTypeRecovery:
		emoji = ":white_check_mark:"
	case AlertTypeReorg:
		emoji = ":rotating_light:"
	case AlertTypeScamToken:
		emoji = ":no_entry:"
	case AlertTypeReconcileErr:
		emoji = ":scales:"
	case AlertTypeDBPool:
		emoji = ":hourglass_flowing_sand:"
	}

	text := fmt.Sprintf("%s *[%s]* %s/%s: %s\n%s",
		emoji, alert.Type, alert.Chain, alert.Network, alert.Title, alert.Message)

	if len(alert.Fields) > 0 {
		text += "\n"
		for k, v := range alert.Fields {
			text += fmt.Sprintf("- *%s*: %s\n", k, v)
		}
	}

	payload := map[string]string{"text": text}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send slack alert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack returned status %d", resp.StatusCode)
	}
	return nil
}

// WebhookAlerter sends alerts to a generic HTTP webhook.
type WebhookAlerter struct {
	url    string
	client *http.Client
}

// NewWebhookAlerter creates a generic webhook alerter.
func NewWebhookAlerter(url string) *WebhookAlerter {
	return &WebhookAlerter{
		url: url,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				MaxConnsPerHost:     10,
				IdleConnTimeout:     30 * time.Second,
			},
		},
	}
}

// Send sends an alert to the webhook endpoint.
func (w *WebhookAlerter) Send(ctx context.Context, alert Alert) error {
	payload := map[string]any{
		"type":    string(alert.Type),
		"chain":   alert.Chain,
		"network": alert.Network,
		"title":   alert.Title,
		"message": alert.Message,
		"fields":  alert.Fields,
		"time":    time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook alert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// NoopAlerter does nothing. Used when no alert channels are configured.
type NoopAlerter struct{}

func (n *NoopAlerter) Send(_ context.Context, _ Alert) error { return nil }
