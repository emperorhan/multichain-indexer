package redis

import (
	"context"
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

func (s *Stream) Client() *redis.Client {
	return s.client
}
