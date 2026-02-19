package tracing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInit_EmptyEndpoint_ReturnsNoOpProvider(t *testing.T) {
	shutdown, err := Init(context.Background(), "test-svc", "", true, 0.1)
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	err = shutdown(context.Background())
	assert.NoError(t, err)
}

func TestTracer_ReturnsNonNil(t *testing.T) {
	shutdown, err := Init(context.Background(), "test-svc", "", true, 0.1)
	require.NoError(t, err)
	defer shutdown(context.Background())

	tracer := Tracer("test-tracer")
	assert.NotNil(t, tracer)
}

func TestInit_ShutdownIdempotent(t *testing.T) {
	shutdown, err := Init(context.Background(), "test-svc", "", true, 0.1)
	require.NoError(t, err)

	err = shutdown(context.Background())
	assert.NoError(t, err)

	err = shutdown(context.Background())
	assert.NoError(t, err)
}
