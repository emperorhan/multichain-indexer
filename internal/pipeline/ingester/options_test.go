package ingester

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWithRetryConfig_AppliesValues(t *testing.T) {
	ing := &Ingester{}
	opt := WithRetryConfig(5, 200*time.Millisecond, 10*time.Second)
	opt(ing)
	assert.Equal(t, 5, ing.retryMaxAttempts)
	assert.Equal(t, 200*time.Millisecond, ing.retryDelayStart)
	assert.Equal(t, 10*time.Second, ing.retryDelayMax)
}

func TestWithDeniedCacheConfig_AppliesCache(t *testing.T) {
	ing := &Ingester{}
	opt := WithDeniedCacheConfig(5000, 2*time.Minute)
	opt(ing)
	assert.NotNil(t, ing.deniedCache, "denied cache should be initialized")
}

func TestWithBlockScanAddrCacheTTL_AppliesValue(t *testing.T) {
	ing := &Ingester{}
	opt := WithBlockScanAddrCacheTTL(60 * time.Second)
	opt(ing)
	assert.Equal(t, 60*time.Second, ing.blockScanAddrCacheTTL)
}

func TestWithBlockScanAddrCacheTTL_Zero(t *testing.T) {
	ing := &Ingester{blockScanAddrCacheTTL: 30 * time.Second}
	opt := WithBlockScanAddrCacheTTL(0)
	opt(ing)
	assert.Equal(t, time.Duration(0), ing.blockScanAddrCacheTTL,
		"zero TTL should be accepted (disables caching)")
}
