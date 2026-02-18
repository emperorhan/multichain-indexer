package redis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Stream provides Redis Streams abstraction for future process separation.
type Stream struct {
	client *redis.Client
}

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
