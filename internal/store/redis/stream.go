package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// MessageTransport abstracts stream backends used by the pipeline transport boundary.
type MessageTransport interface {
	PublishJSON(ctx context.Context, streamName string, payload any) (string, error)
	ReadJSON(ctx context.Context, streamName, lastID string, dst any) (string, error)
	Close() error
}

// Stream provides Redis Streams abstraction for future process separation.
type Stream struct {
	client *redis.Client
}

var _ MessageTransport = (*Stream)(nil)

func NewStream(url string) (*Stream, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(opts)

	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &Stream{client: client}, nil
}

func (s *Stream) Close() error {
	return s.client.Close()
}

func (s *Stream) PublishJSON(ctx context.Context, streamName string, payload interface{}) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal stream payload: %w", err)
	}

	messageID, err := s.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamName,
		Values: map[string]any{
			"payload": data,
		},
	}).Result()
	if err != nil {
		return "", fmt.Errorf("publish stream message: %w", err)
	}
	return messageID, nil
}

func (s *Stream) ReadJSON(ctx context.Context, streamName, lastID string, dst any) (string, error) {
	streams := []string{streamName, lastID}
	msgs, err := s.client.XRead(ctx, &redis.XReadArgs{
		Streams: streams,
		Count:   1,
		Block:   0,
	}).Result()
	if err != nil {
		return "", fmt.Errorf("read stream message: %w", err)
	}
	if len(msgs) == 0 || len(msgs[0].Messages) == 0 {
		return "", fmt.Errorf("stream read returned empty batch for %s", streamName)
	}

	msg := msgs[0].Messages[0]
	payload, err := streamPayload(msg.Values["payload"])
	if err != nil {
		return "", fmt.Errorf("stream payload decode: %w", err)
	}
	if err := json.Unmarshal(payload, dst); err != nil {
		return "", fmt.Errorf("stream payload unmarshal: %w", err)
	}
	return msg.ID, nil
}

func streamPayload(raw any) ([]byte, error) {
	switch value := raw.(type) {
	case string:
		return []byte(value), nil
	case []byte:
		return value, nil
	case fmt.Stringer:
		return []byte(value.String()), nil
	default:
		return nil, fmt.Errorf("stream payload type %T not supported", raw)
	}
}

func (s *Stream) Client() *redis.Client {
	return s.client
}

// InMemoryStream is a deterministic fallback transport used when Redis is unavailable.
type InMemoryStream struct {
	mu      sync.Mutex
	streams map[string]*inMemoryStreamState
}

type inMemoryStreamState struct {
	lastID   int64
	records  [][]byte
	notifyCh chan struct{}
}

var _ MessageTransport = (*InMemoryStream)(nil)

func NewInMemoryStream() *InMemoryStream {
	return &InMemoryStream{streams: map[string]*inMemoryStreamState{}}
}

func (s *InMemoryStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.streams = map[string]*inMemoryStreamState{}
	return nil

}

func (s *InMemoryStream) ensureState(streamName string) *inMemoryStreamState {
	if state, ok := s.streams[streamName]; ok {
		return state
	}

	state := &inMemoryStreamState{
		notifyCh: make(chan struct{}, 1),
	}
	s.streams[streamName] = state
	return state
}

func (s *InMemoryStream) PublishJSON(ctx context.Context, streamName string, payload any) (string, error) {
	_ = ctx

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal stream payload: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.ensureState(streamName)
	state.lastID++
	state.records = append(state.records, data)
	select {
	case state.notifyCh <- struct{}{}:
	default:
	}

	return strconv.FormatInt(state.lastID, 10), nil
}

func (s *InMemoryStream) ReadJSON(ctx context.Context, streamName, lastID string, dst any) (string, error) {
	stateIndex, err := parseStreamOffset(lastID)
	if err != nil {
		return "", err
	}

	for {
		s.mu.Lock()
		state := s.ensureState(streamName)
		if stateIndex < int64(len(state.records)) {
			msg := state.records[stateIndex]
			nextID := stateIndex + 1
			s.mu.Unlock()

			if err := json.Unmarshal(msg, dst); err != nil {
				return "", fmt.Errorf("stream payload unmarshal: %w", err)
			}
			return strconv.FormatInt(nextID, 10), nil
	}

	notifyCh := state.notifyCh
	s.mu.Unlock()

		if err := ctx.Err(); err != nil {
			return "", err
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-notifyCh:
			if len(notifyCh) > 0 {
				// keep channel warm for bursty wakeups.
				select {
				case <-notifyCh:
				default:
				}
			}
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func parseStreamOffset(lastID string) (int64, error) {
	trimmed := strings.TrimSpace(lastID)
	if trimmed == "" || trimmed == "0" {
		return 0, nil
	}

	if strings.Contains(trimmed, "-") {
		trimmed = strings.SplitN(trimmed, "-", 2)[0]
	}

	if trimmed == "" {
		return 0, nil
	}

	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid stream offset %q: %w", lastID, err)
	}

	if parsed < 0 {
		return 0, nil
	}

	return parsed, nil
}
