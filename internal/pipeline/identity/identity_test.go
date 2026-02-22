package identity

import (
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
)

// ---------------------------------------------------------------------------
// CanonicalSignatureIdentity
// ---------------------------------------------------------------------------

func TestCanonicalSignatureIdentity(t *testing.T) {
	tests := []struct {
		name     string
		chain    model.Chain
		input    string
		expected string
	}{
		// --- EVM chains ---
		{
			name:     "EVM/ethereum lowercase hex with 0x",
			chain:    model.ChainEthereum,
			input:    "0xabcdef1234567890",
			expected: "0xabcdef1234567890",
		},
		{
			name:     "EVM/ethereum mixed case hex with 0x",
			chain:    model.ChainEthereum,
			input:    "0xABcdEF1234567890",
			expected: "0xabcdef1234567890",
		},
		{
			name:     "EVM/base uppercase hex with 0X prefix",
			chain:    model.ChainBase,
			input:    "0XABCDEF",
			expected: "0xabcdef",
		},
		{
			name:     "EVM/polygon bare hex (no prefix)",
			chain:    model.ChainPolygon,
			input:    "ABCDEF1234",
			expected: "0xabcdef1234",
		},
		{
			name:     "EVM/arbitrum with 0x prefix already lowercase",
			chain:    model.ChainArbitrum,
			input:    "0xdeadbeef",
			expected: "0xdeadbeef",
		},
		{
			name:     "EVM/bsc with 0x prefix mixed case",
			chain:    model.ChainBSC,
			input:    "0xDeAdBeEf",
			expected: "0xdeadbeef",
		},
		{
			name:     "EVM empty string",
			chain:    model.ChainEthereum,
			input:    "",
			expected: "",
		},
		{
			name:     "EVM whitespace only",
			chain:    model.ChainEthereum,
			input:    "   ",
			expected: "",
		},
		{
			name:     "EVM 0x prefix only",
			chain:    model.ChainEthereum,
			input:    "0x",
			expected: "",
		},
		{
			name:     "EVM 0X prefix only",
			chain:    model.ChainEthereum,
			input:    "0X",
			expected: "",
		},
		{
			name:     "EVM with leading/trailing whitespace",
			chain:    model.ChainEthereum,
			input:    "  0xABCDEF  ",
			expected: "0xabcdef",
		},
		{
			name:     "EVM non-hex string (passthrough with 0x prefix check)",
			chain:    model.ChainEthereum,
			input:    "notahexstring",
			expected: "notahexstring",
		},
		{
			name:     "EVM 0x prefixed non-hex content",
			chain:    model.ChainEthereum,
			input:    "0xGHIJKL",
			expected: "0xghijkl",
		},

		// --- Solana ---
		{
			name:     "Solana base58 preserves case",
			chain:    model.ChainSolana,
			input:    "5K4bK8mFQziw3aXnJJmKGYwTKqPdVHFbZGSvYm7Jy3rS",
			expected: "5K4bK8mFQziw3aXnJJmKGYwTKqPdVHFbZGSvYm7Jy3rS",
		},
		{
			name:     "Solana whitespace trimmed",
			chain:    model.ChainSolana,
			input:    "  SolanaHash123  ",
			expected: "SolanaHash123",
		},
		{
			name:     "Solana empty",
			chain:    model.ChainSolana,
			input:    "",
			expected: "",
		},
		{
			name:     "Solana preserves mixed case fully",
			chain:    model.ChainSolana,
			input:    "AbCdEfGhIjKlMnOp",
			expected: "AbCdEfGhIjKlMnOp",
		},

		// --- BTC ---
		{
			name:     "BTC lowercase hex",
			chain:    model.ChainBTC,
			input:    "abcdef1234567890",
			expected: "abcdef1234567890",
		},
		{
			name:     "BTC mixed case hex",
			chain:    model.ChainBTC,
			input:    "ABCDEF1234567890",
			expected: "abcdef1234567890",
		},
		{
			name:     "BTC strips 0x prefix and lowercases",
			chain:    model.ChainBTC,
			input:    "0xABCDEF",
			expected: "abcdef",
		},
		{
			name:     "BTC strips 0X prefix and lowercases",
			chain:    model.ChainBTC,
			input:    "0XABCDEF",
			expected: "abcdef",
		},
		{
			name:     "BTC empty",
			chain:    model.ChainBTC,
			input:    "",
			expected: "",
		},
		{
			name:     "BTC 0x only returns empty",
			chain:    model.ChainBTC,
			input:    "0x",
			expected: "",
		},
		{
			name:     "BTC whitespace trimmed then lowercased",
			chain:    model.ChainBTC,
			input:    "  0xDEADBEEF  ",
			expected: "deadbeef",
		},

		// --- Unknown chain (non-EVM, non-BTC) ---
		{
			name:     "Unknown chain preserves value",
			chain:    model.Chain("unknown"),
			input:    "SomeValue",
			expected: "SomeValue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanonicalSignatureIdentity(tt.chain, tt.input)
			if got != tt.expected {
				t.Errorf("CanonicalSignatureIdentity(%s, %q) = %q, want %q",
					tt.chain, tt.input, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// CanonicalAddressIdentity
// ---------------------------------------------------------------------------

func TestCanonicalAddressIdentity(t *testing.T) {
	tests := []struct {
		name     string
		chain    model.Chain
		input    string
		expected string
	}{
		// --- EVM addresses ---
		{
			name:     "EVM/ethereum mixed case with 0x",
			chain:    model.ChainEthereum,
			input:    "0xABcdEF1234567890abcdef1234567890ABcdEF12",
			expected: "0xabcdef1234567890abcdef1234567890abcdef12",
		},
		{
			name:     "EVM/base uppercase 0X prefix",
			chain:    model.ChainBase,
			input:    "0X1234ABCD",
			expected: "0x1234abcd",
		},
		{
			name:     "EVM/polygon bare hex address",
			chain:    model.ChainPolygon,
			input:    "ABCDEF1234567890",
			expected: "0xabcdef1234567890",
		},
		{
			name:     "EVM/arbitrum already lowercase with 0x",
			chain:    model.ChainArbitrum,
			input:    "0xdeadbeef",
			expected: "0xdeadbeef",
		},
		{
			name:     "EVM/bsc whitespace trimmed",
			chain:    model.ChainBSC,
			input:    "  0xABCDEF  ",
			expected: "0xabcdef",
		},
		{
			name:     "EVM empty string",
			chain:    model.ChainEthereum,
			input:    "",
			expected: "",
		},
		{
			name:     "EVM whitespace only",
			chain:    model.ChainEthereum,
			input:    "   ",
			expected: "",
		},
		{
			name:     "EVM 0x prefix only returns original trimmed",
			chain:    model.ChainEthereum,
			input:    "0x",
			expected: "0x",
		},
		{
			name:     "EVM non-hex string (passthrough)",
			chain:    model.ChainEthereum,
			input:    "notahexaddr",
			expected: "notahexaddr",
		},
		{
			name:     "EVM 0x prefixed non-hex returns lowercased",
			chain:    model.ChainEthereum,
			input:    "0xGHIJKL",
			expected: "0xghijkl",
		},

		// --- Solana ---
		{
			name:     "Solana preserves case",
			chain:    model.ChainSolana,
			input:    "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM",
			expected: "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM",
		},
		{
			name:     "Solana whitespace trimmed",
			chain:    model.ChainSolana,
			input:    "  SolAddress123  ",
			expected: "SolAddress123",
		},
		{
			name:     "Solana empty",
			chain:    model.ChainSolana,
			input:    "",
			expected: "",
		},

		// --- BTC ---
		{
			name:     "BTC address preserves case (non-EVM)",
			chain:    model.ChainBTC,
			input:    "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4",
			expected: "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4",
		},
		{
			name:     "BTC address trimmed",
			chain:    model.ChainBTC,
			input:    "  1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa  ",
			expected: "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanonicalAddressIdentity(tt.chain, tt.input)
			if got != tt.expected {
				t.Errorf("CanonicalAddressIdentity(%s, %q) = %q, want %q",
					tt.chain, tt.input, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// CanonicalizeCursorValue
// ---------------------------------------------------------------------------

func TestCanonicalizeCursorValue(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name     string
		chain    model.Chain
		cursor   *string
		expected *string
	}{
		{
			name:     "nil cursor returns nil",
			chain:    model.ChainEthereum,
			cursor:   nil,
			expected: nil,
		},
		{
			name:     "empty cursor returns nil",
			chain:    model.ChainEthereum,
			cursor:   strPtr(""),
			expected: nil,
		},
		{
			name:     "whitespace-only cursor returns nil",
			chain:    model.ChainEthereum,
			cursor:   strPtr("   "),
			expected: nil,
		},
		{
			name:     "EVM cursor canonicalized",
			chain:    model.ChainEthereum,
			cursor:   strPtr("0xABCDEF"),
			expected: strPtr("0xabcdef"),
		},
		{
			name:     "Solana cursor preserves case",
			chain:    model.ChainSolana,
			cursor:   strPtr("SolHash123"),
			expected: strPtr("SolHash123"),
		},
		{
			name:     "BTC cursor lowercased and stripped",
			chain:    model.ChainBTC,
			cursor:   strPtr("0xABCDEF"),
			expected: strPtr("abcdef"),
		},
		{
			name:     "EVM 0x-only cursor returns nil",
			chain:    model.ChainEthereum,
			cursor:   strPtr("0x"),
			expected: nil,
		},
		{
			name:     "BTC 0x-only cursor returns nil",
			chain:    model.ChainBTC,
			cursor:   strPtr("0x"),
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanonicalizeCursorValue(tt.chain, tt.cursor)
			if tt.expected == nil {
				if got != nil {
					t.Errorf("CanonicalizeCursorValue(%s, %v) = %q, want nil",
						tt.chain, tt.cursor, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("CanonicalizeCursorValue(%s, %q) = nil, want %q",
					tt.chain, *tt.cursor, *tt.expected)
			}
			if *got != *tt.expected {
				t.Errorf("CanonicalizeCursorValue(%s, %q) = %q, want %q",
					tt.chain, *tt.cursor, *got, *tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsEVMChain
// ---------------------------------------------------------------------------

func TestIsEVMChain(t *testing.T) {
	tests := []struct {
		chain    model.Chain
		expected bool
	}{
		{model.ChainEthereum, true},
		{model.ChainBase, true},
		{model.ChainPolygon, true},
		{model.ChainArbitrum, true},
		{model.ChainBSC, true},
		{model.ChainSolana, false},
		{model.ChainBTC, false},
		{model.Chain("unknown"), false},
		{model.Chain(""), false},
		{model.Chain("ETHEREUM"), false}, // case-sensitive
	}

	for _, tt := range tests {
		t.Run(string(tt.chain), func(t *testing.T) {
			got := IsEVMChain(tt.chain)
			if got != tt.expected {
				t.Errorf("IsEVMChain(%q) = %v, want %v", tt.chain, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsHexString
// ---------------------------------------------------------------------------

func TestIsHexString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"lowercase hex", "abcdef0123456789", true},
		{"uppercase hex", "ABCDEF0123456789", true},
		{"mixed case hex", "aAbBcCdDeEfF0123", true},
		{"digits only", "0123456789", true},
		{"single char a", "a", true},
		{"single char F", "F", true},
		{"single char 0", "0", true},
		{"empty string is hex", "", true}, // no non-hex chars found
		{"contains g", "abcg", false},
		{"contains space", "abc def", false},
		{"contains 0x prefix", "0xabc", false}, // 'x' is non-hex
		{"special chars", "abc!@#", false},
		{"newline char", "abc\ndef", false},
		{"unicode char", "abc\u00e9", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsHexString(tt.input)
			if got != tt.expected {
				t.Errorf("IsHexString(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NegateDecimalString
// ---------------------------------------------------------------------------

func TestNegateDecimalString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{"positive to negative", "100", "-100", false},
		{"negative to positive", "-50", "50", false},
		{"zero stays zero", "0", "0", false},
		{"very large positive", "999999999999999999999999999999999999", "-999999999999999999999999999999999999", false},
		{"very large negative", "-999999999999999999999999999999999999", "999999999999999999999999999999999999", false},
		{"one", "1", "-1", false},
		{"negative one", "-1", "1", false},
		{"leading zeros", "00100", "-100", false},
		{"negative zero", "-0", "0", false},
		{"positive with plus sign", "+100", "-100", false},
		{"invalid: alphabetic", "abc", "", true},
		{"invalid: empty string", "", "", true},
		{"invalid: hex string", "0xff", "", true},
		{"invalid: float", "3.14", "", true},
		{"invalid: spaces", "1 000", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NegateDecimalString(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NegateDecimalString(%q) expected error, got %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NegateDecimalString(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("NegateDecimalString(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsStakingActivity
// ---------------------------------------------------------------------------

func TestIsStakingActivity(t *testing.T) {
	tests := []struct {
		name     string
		activity model.ActivityType
		expected bool
	}{
		{"STAKE is staking", model.ActivityStake, true},
		{"UNSTAKE is staking", model.ActivityUnstake, true},
		{"DEPOSIT is not staking", model.ActivityDeposit, false},
		{"WITHDRAWAL is not staking", model.ActivityWithdrawal, false},
		{"FEE is not staking", model.ActivityFee, false},
		{"CLAIM_REWARD is not staking", model.ActivityClaimReward, false},
		{"SWAP is not staking", model.ActivitySwap, false},
		{"MINT is not staking", model.ActivityMint, false},
		{"BURN is not staking", model.ActivityBurn, false},
		{"OTHER is not staking", model.ActivityOther, false},
		{"SELF_TRANSFER is not staking", model.ActivitySelfTransfer, false},
		{"empty activity is not staking", model.ActivityType(""), false},
		{"unknown activity is not staking", model.ActivityType("UNKNOWN"), false},
		{"FEE_EXECUTION_L2 is not staking", model.ActivityFeeExecutionL2, false},
		{"FEE_DATA_L1 is not staking", model.ActivityFeeDataL1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsStakingActivity(tt.activity)
			if got != tt.expected {
				t.Errorf("IsStakingActivity(%q) = %v, want %v", tt.activity, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Cross-chain consistency tests
// ---------------------------------------------------------------------------

func TestCanonicalSignatureIdentity_CrossChainConsistency(t *testing.T) {
	// The same hex hash should canonicalize differently per chain.
	hash := "0xABCDEF1234"

	ethResult := CanonicalSignatureIdentity(model.ChainEthereum, hash)
	btcResult := CanonicalSignatureIdentity(model.ChainBTC, hash)
	solResult := CanonicalSignatureIdentity(model.ChainSolana, hash)

	// EVM: 0x-prefixed lowercase
	if ethResult != "0xabcdef1234" {
		t.Errorf("ETH: expected 0xabcdef1234, got %q", ethResult)
	}
	// BTC: stripped prefix, lowercase
	if btcResult != "abcdef1234" {
		t.Errorf("BTC: expected abcdef1234, got %q", btcResult)
	}
	// Solana: trimmed only (preserves case and prefix)
	if solResult != "0xABCDEF1234" {
		t.Errorf("SOL: expected 0xABCDEF1234, got %q", solResult)
	}
}

func TestCanonicalAddressIdentity_EVMVsNonEVM(t *testing.T) {
	addr := "0xABCDef1234567890"

	// EVM chains should lowercase
	ethResult := CanonicalAddressIdentity(model.ChainEthereum, addr)
	if ethResult != "0xabcdef1234567890" {
		t.Errorf("ETH address: expected 0xabcdef1234567890, got %q", ethResult)
	}

	// Non-EVM (Solana) should preserve case (only trim)
	solResult := CanonicalAddressIdentity(model.ChainSolana, addr)
	if solResult != "0xABCDef1234567890" {
		t.Errorf("SOL address: expected 0xABCDef1234567890, got %q", solResult)
	}

	// Non-EVM (BTC) should preserve case (only trim)
	btcResult := CanonicalAddressIdentity(model.ChainBTC, addr)
	if btcResult != "0xABCDef1234567890" {
		t.Errorf("BTC address: expected 0xABCDef1234567890, got %q", btcResult)
	}
}

// ---------------------------------------------------------------------------
// Idempotency tests
// ---------------------------------------------------------------------------

func TestCanonicalSignatureIdentity_Idempotent(t *testing.T) {
	chains := []model.Chain{
		model.ChainEthereum, model.ChainBase, model.ChainPolygon,
		model.ChainArbitrum, model.ChainBSC, model.ChainSolana, model.ChainBTC,
	}
	inputs := []string{"0xABCDEF", "SomeHash123", "deadbeef"}

	for _, chain := range chains {
		for _, input := range inputs {
			first := CanonicalSignatureIdentity(chain, input)
			second := CanonicalSignatureIdentity(chain, first)
			if first != second {
				t.Errorf("Not idempotent for chain=%s input=%q: first=%q second=%q",
					chain, input, first, second)
			}
		}
	}
}

func TestCanonicalAddressIdentity_Idempotent(t *testing.T) {
	chains := []model.Chain{
		model.ChainEthereum, model.ChainBase, model.ChainPolygon,
		model.ChainArbitrum, model.ChainBSC, model.ChainSolana, model.ChainBTC,
	}
	inputs := []string{"0xABCDEF1234", "9WzDXwBbmkg8", "bc1qw508d6q"}

	for _, chain := range chains {
		for _, input := range inputs {
			first := CanonicalAddressIdentity(chain, input)
			second := CanonicalAddressIdentity(chain, first)
			if first != second {
				t.Errorf("Not idempotent for chain=%s input=%q: first=%q second=%q",
					chain, input, first, second)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// NegateDecimalString symmetry: negate(negate(x)) == x
// ---------------------------------------------------------------------------

func TestNegateDecimalString_DoubleNegateIsIdentity(t *testing.T) {
	values := []string{"0", "1", "-1", "100", "-999999999999999999999"}
	for _, v := range values {
		first, err := NegateDecimalString(v)
		if err != nil {
			t.Fatalf("first negate(%q): %v", v, err)
		}
		second, err := NegateDecimalString(first)
		if err != nil {
			t.Fatalf("second negate(%q): %v", first, err)
		}
		// Normalize original for comparison (e.g. "+100" -> "100")
		orig, _ := NegateDecimalString(first)
		negOrig, _ := NegateDecimalString(orig)
		_ = negOrig
		if second != v && second != "0" {
			// Handle special case: "-0" becomes "0"
			t.Errorf("NegateDecimalString(NegateDecimalString(%q)) = %q, want %q", v, second, v)
		}
	}
}
