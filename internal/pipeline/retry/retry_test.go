package retry

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClassify_ExplicitMarkers(t *testing.T) {
	transient := Classify(Transient(errors.New("rpc timed out")))
	assert.Equal(t, ClassTransient, transient.Class)
	assert.Equal(t, "explicit_transient", transient.Reason)

	terminal := Classify(Terminal(errors.New("invalid params")))
	assert.Equal(t, ClassTerminal, terminal.Class)
	assert.Equal(t, "explicit_terminal", terminal.Reason)
}

func TestClassify_RepresentativeRuntimeErrors(t *testing.T) {
	testCases := []struct {
		name          string
		err           error
		expectedClass Class
	}{
		{
			name:          "grpc unavailable transient",
			err:           status.Error(codes.Unavailable, "sidecar unavailable"),
			expectedClass: ClassTransient,
		},
		{
			name:          "context deadline transient",
			err:           context.DeadlineExceeded,
			expectedClass: ClassTransient,
		},
		{
			name:          "decode blocked terminal",
			err:           errors.New("decode blocked at signature sig-1 after 0 successful txs: parse error"),
			expectedClass: ClassTerminal,
		},
		{
			name:          "unknown defaults terminal",
			err:           errors.New("unexpected failure"),
			expectedClass: ClassTerminal,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			decision := Classify(tc.err)
			assert.Equal(t, tc.expectedClass, decision.Class)
		})
	}
}
