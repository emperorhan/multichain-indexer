package identity

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// CanonicalSignatureIdentity normalises a transaction hash into its
// canonical form so that different representations of the same hash
// (e.g. mixed-case hex, 0x prefix vs bare) compare as equal.
func CanonicalSignatureIdentity(chainID model.Chain, hash string) string {
	trimmed := strings.TrimSpace(hash)
	if trimmed == "" {
		return ""
	}
	if chainID == model.ChainBTC {
		withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
		if withoutPrefix == "" {
			return ""
		}
		return strings.ToLower(withoutPrefix)
	}
	if !IsEVMChain(chainID) {
		return trimmed
	}

	withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
	if withoutPrefix == "" {
		return ""
	}
	if IsHexString(withoutPrefix) {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	return trimmed
}

// CanonicalizeCursorValue applies CanonicalSignatureIdentity to a cursor
// pointer, returning nil when the value is empty or nil.
func CanonicalizeCursorValue(chainID model.Chain, cursor *string) *string {
	if cursor == nil {
		return nil
	}
	id := CanonicalSignatureIdentity(chainID, *cursor)
	if id == "" {
		return nil
	}
	value := id
	return &value
}

// CanonicalAddressIdentity normalises a blockchain address into its
// canonical form. For EVM chains this lowercases and ensures 0x prefix;
// for other chains the trimmed value is returned as-is.
func CanonicalAddressIdentity(chainID model.Chain, address string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return ""
	}
	if !IsEVMChain(chainID) {
		return trimmed
	}
	// Inline EVM canonicalization to avoid redundant IsEVMChain check
	// in CanonicalSignatureIdentity.
	withoutPrefix := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
	if withoutPrefix == "" {
		return trimmed
	}
	if IsHexString(withoutPrefix) {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		return "0x" + strings.ToLower(withoutPrefix)
	}
	return trimmed
}

// IsEVMChain returns true for EVM-compatible chains.
func IsEVMChain(chainID model.Chain) bool {
	switch chainID {
	case model.ChainBase, model.ChainEthereum, model.ChainPolygon, model.ChainArbitrum, model.ChainBSC:
		return true
	default:
		return false
	}
}

// IsHexString reports whether v consists solely of hexadecimal characters.
func IsHexString(v string) bool {
	for _, ch := range v {
		switch {
		case ch >= '0' && ch <= '9':
		case ch >= 'a' && ch <= 'f':
		case ch >= 'A' && ch <= 'F':
		default:
			return false
		}
	}
	return true
}

// NegateDecimalString negates a base-10 integer string.
func NegateDecimalString(value string) (string, error) {
	var delta big.Int
	if _, ok := delta.SetString(value, 10); !ok {
		return "", fmt.Errorf("invalid decimal value: %s", value)
	}
	delta.Neg(&delta)
	return delta.String(), nil
}

// IsStakingActivity returns true for stake/unstake activity types.
func IsStakingActivity(activityType model.ActivityType) bool {
	return activityType == model.ActivityStake || activityType == model.ActivityUnstake
}
