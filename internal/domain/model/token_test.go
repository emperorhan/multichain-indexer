package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSolanaNativeMint(t *testing.T) {
	assert.Equal(t, "11111111111111111111111111111111", SolanaNativeMint)
	assert.Len(t, SolanaNativeMint, 32)
}
