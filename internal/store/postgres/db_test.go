package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveStatementTimeoutMS_ConfigOverride(t *testing.T) {
	resolved, err := resolveStatementTimeoutMS(Config{
		StatementTimeoutMS: 45000,
	})

	require.NoError(t, err)
	assert.Equal(t, 45000, resolved)
}

func TestResolveStatementTimeoutMS_ConfigInvalidValue(t *testing.T) {
	_, err := resolveStatementTimeoutMS(Config{
		StatementTimeoutMS: -1,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of allowed range")
}

func TestResolveStatementTimeoutMS_EnvFallback(t *testing.T) {
	t.Setenv("DB_STATEMENT_TIMEOUT_MS", "45000")

	resolved, err := resolveStatementTimeoutMS(Config{})
	require.NoError(t, err)
	assert.Equal(t, 45000, resolved)
}

func TestResolveStatementTimeoutMS_EnvInvalidValue(t *testing.T) {
	t.Setenv("DB_STATEMENT_TIMEOUT_MS", "invalid")

	_, err := resolveStatementTimeoutMS(Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_STATEMENT_TIMEOUT_MS")
}
