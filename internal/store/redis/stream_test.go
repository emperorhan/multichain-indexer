package redis

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStreamOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		expected  int64
		expectErr bool
	}{
		{name: "empty string", input: "", expected: 0},
		{name: "zero", input: "0", expected: 0},
		{name: "positive integer", input: "123", expected: 123},
		{name: "compound id", input: "123-0", expected: 123},
		{name: "negative clamps to zero", input: "-5", expected: 0},
		{name: "non-numeric", input: "abc", expectErr: true},
		{name: "whitespace trimmed", input: "  42  ", expected: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := parseStreamOffset(tt.input)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateStreamOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		expectErr bool
	}{
		{name: "empty string", input: "", expectErr: false},
		{name: "zero", input: "0", expectErr: false},
		{name: "positive integer", input: "42", expectErr: false},
		{name: "compound id", input: "100-0", expectErr: false},
		{name: "non-numeric", input: "abc", expectErr: true},
		{name: "negative", input: "-1", expectErr: true},
		{name: "trailing dash", input: "100-", expectErr: true},
		{name: "negative compound", input: "-100", expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateStreamOffset(tt.input)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type testStringer struct{ value string }

func (s testStringer) String() string { return s.value }

func TestStreamPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     any
		expected  []byte
		expectErr bool
	}{
		{name: "string", input: "hello", expected: []byte("hello")},
		{name: "bytes", input: []byte("world"), expected: []byte("world")},
		{name: "stringer", input: testStringer{value: "from-stringer"}, expected: []byte("from-stringer")},
		{name: "unsupported type", input: 42, expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := streamPayload(tt.input)
			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "not supported")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInMemoryStream_PublishReadRoundtrip(t *testing.T) {
	t.Parallel()

	stream := NewInMemoryStream()
	defer stream.Close()

	ctx := context.Background()
	type msg struct {
		Value string `json:"value"`
	}

	id, err := stream.PublishJSON(ctx, "test-stream", msg{Value: "hello"})
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	var dst msg
	nextID, err := stream.ReadJSON(ctx, "test-stream", "0", &dst)
	require.NoError(t, err)
	assert.Equal(t, "hello", dst.Value)
	assert.NotEmpty(t, nextID)
}

func TestInMemoryStream_ReadJSON_BlocksUntilMessage(t *testing.T) {
	t.Parallel()

	stream := NewInMemoryStream()
	defer stream.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	type msg struct {
		Value string `json:"value"`
	}

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		_, _ = stream.PublishJSON(ctx, "blocking-stream", msg{Value: "delayed"})
	}()

	var dst msg
	_, err := stream.ReadJSON(ctx, "blocking-stream", "0", &dst)
	require.NoError(t, err)
	assert.Equal(t, "delayed", dst.Value)

	wg.Wait()
}

func TestInMemoryStream_ReadJSON_ContextCancellation(t *testing.T) {
	t.Parallel()

	stream := NewInMemoryStream()
	defer stream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	var dst struct{}
	_, err := stream.ReadJSON(ctx, "empty-stream", "0", &dst)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestInMemoryStream_CheckpointRoundtrip(t *testing.T) {
	t.Parallel()

	stream := NewInMemoryStream()
	defer stream.Close()

	ctx := context.Background()

	// Load non-existent checkpoint
	value, err := stream.LoadStreamCheckpoint(ctx, "my-checkpoint")
	require.NoError(t, err)
	assert.Empty(t, value)

	// Persist checkpoint
	err = stream.PersistStreamCheckpoint(ctx, "my-checkpoint", "42")
	require.NoError(t, err)

	// Load persisted checkpoint
	value, err = stream.LoadStreamCheckpoint(ctx, "my-checkpoint")
	require.NoError(t, err)
	assert.Equal(t, "42", value)
}

func TestInMemoryStream_Checkpoint_EmptyKey(t *testing.T) {
	t.Parallel()

	stream := NewInMemoryStream()
	defer stream.Close()

	ctx := context.Background()

	// Empty key should be no-op
	err := stream.PersistStreamCheckpoint(ctx, "", "42")
	require.NoError(t, err)

	value, err := stream.LoadStreamCheckpoint(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, value)
}

func TestInMemoryStream_Checkpoint_InvalidOffset(t *testing.T) {
	t.Parallel()

	stream := NewInMemoryStream()
	defer stream.Close()

	ctx := context.Background()

	err := stream.PersistStreamCheckpoint(ctx, "ck", "abc")
	require.Error(t, err)
}

func TestInMemoryStream_Close(t *testing.T) {
	t.Parallel()

	stream := NewInMemoryStream()

	ctx := context.Background()
	_, _ = stream.PublishJSON(ctx, "s1", map[string]string{"k": "v"})
	_ = stream.PersistStreamCheckpoint(ctx, "ck", "1")

	err := stream.Close()
	require.NoError(t, err)

	// After close, streams and checkpoints should be reset
	stream.mu.Lock()
	assert.Empty(t, stream.streams)
	assert.Empty(t, stream.checkpoints)
	stream.mu.Unlock()
}

func TestInMemoryStream_MultipleMessages_OrderPreserved(t *testing.T) {
	t.Parallel()

	stream := NewInMemoryStream()
	defer stream.Close()

	ctx := context.Background()
	type msg struct {
		Seq int `json:"seq"`
	}

	for i := 1; i <= 3; i++ {
		_, err := stream.PublishJSON(ctx, "ordered-stream", msg{Seq: i})
		require.NoError(t, err)
	}

	lastID := "0"
	for i := 1; i <= 3; i++ {
		var dst msg
		nextID, err := stream.ReadJSON(ctx, "ordered-stream", lastID, &dst)
		require.NoError(t, err, fmt.Sprintf("read message %d", i))
		assert.Equal(t, i, dst.Seq)
		lastID = nextID
	}
}
