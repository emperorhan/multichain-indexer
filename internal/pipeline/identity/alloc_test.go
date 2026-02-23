package identity

import (
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Allocation regression tests for identity hot-path functions.
// Uses testing.AllocsPerRun to enforce upper bounds so regressions fail CI.
// ---------------------------------------------------------------------------

func TestAllocRegression_NegateDecimalString(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = NegateDecimalString("999999999999999999999")
	})
	// big.Int parse + negate + String() → allow up to 6
	assert.LessOrEqual(t, allocs, float64(6), "NegateDecimalString allocs should stay bounded")
}

func TestAllocRegression_NegateDecimalString_Small(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = NegateDecimalString("5000")
	})
	assert.LessOrEqual(t, allocs, float64(6), "NegateDecimalString small value allocs should stay bounded")
}

func TestAllocRegression_IsStakingActivity(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		_ = IsStakingActivity(model.ActivityStake)
		_ = IsStakingActivity(model.ActivityUnstake)
		_ = IsStakingActivity(model.ActivityDeposit)
	})
	// Pure comparison — must be zero-alloc.
	assert.Equal(t, float64(0), allocs, "IsStakingActivity should be zero-alloc")
}

func TestAllocRegression_IsEVMChain(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		_ = IsEVMChain(model.ChainBase)
		_ = IsEVMChain(model.ChainSolana)
		_ = IsEVMChain(model.ChainBTC)
	})
	assert.Equal(t, float64(0), allocs, "IsEVMChain should be zero-alloc")
}

func TestAllocRegression_CanonicalSignatureIdentity_Solana(t *testing.T) {
	// Solana: TrimSpace only (no hex lowering) → expect minimal allocs.
	allocs := testing.AllocsPerRun(100, func() {
		_ = CanonicalSignatureIdentity(model.ChainSolana, "5VERv8NMvzbJMEkV8xnrLkEaWRtSz9CosKDYjCJjBRnbJLgp8uirBgmQpjKhoR4tJ")
	})
	assert.LessOrEqual(t, allocs, float64(1), "Solana sig identity should be near-zero alloc")
}

func TestAllocRegression_CanonicalSignatureIdentity_EVM(t *testing.T) {
	// EVM: 0x prefix + ToLower → expect limited allocs.
	allocs := testing.AllocsPerRun(100, func() {
		_ = CanonicalSignatureIdentity(model.ChainBase, "0xABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890")
	})
	// TrimPrefix + ToLower + concat → allow up to 5
	assert.LessOrEqual(t, allocs, float64(5), "EVM sig identity allocs should stay bounded")
}

func TestAllocRegression_CanonicalAddressIdentity_Solana(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		_ = CanonicalAddressIdentity(model.ChainSolana, "7xKXtg2CW87d97TXJSDpbD5jBkheTqA83TZRuJosgAsU")
	})
	assert.LessOrEqual(t, allocs, float64(1), "Solana addr identity should be near-zero alloc")
}

func TestAllocRegression_CanonicalAddressIdentity_EVM(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		_ = CanonicalAddressIdentity(model.ChainBase, "0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	})
	assert.LessOrEqual(t, allocs, float64(5), "EVM addr identity allocs should stay bounded")
}
