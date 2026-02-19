package normalizer

import (
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	sidecarv1 "github.com/emperorhan/multichain-indexer/pkg/generated/sidecar/v1"
	"github.com/stretchr/testify/assert"
)

func TestEvmNativeToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		chain          model.Chain
		expectedSymbol string
		expectedName   string
	}{
		{model.ChainEthereum, "ETH", "Ether"},
		{model.ChainBase, "ETH", "Ether"},
		{model.ChainArbitrum, "ETH", "Ether"},
		{model.ChainPolygon, "POL", "POL"},
		{model.ChainBSC, "BNB", "BNB"},
	}

	for _, tc := range tests {
		t.Run(string(tc.chain), func(t *testing.T) {
			token := evmNativeToken(tc.chain)
			assert.Equal(t, tc.expectedSymbol, token.Symbol)
			assert.Equal(t, tc.expectedName, token.Name)
			assert.Equal(t, 18, token.Decimals)
		})
	}
}

func TestIsEVML1Chain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		chain    model.Chain
		expected bool
	}{
		{model.ChainEthereum, true},
		{model.ChainPolygon, true},
		{model.ChainBSC, true},
		{model.ChainBase, false},
		{model.ChainArbitrum, false},
		{model.ChainSolana, false},
		{model.ChainBTC, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.chain), func(t *testing.T) {
			assert.Equal(t, tc.expected, isEVML1Chain(tc.chain))
		})
	}
}

func TestEvmDecoderVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		chain    model.Chain
		expected string
	}{
		{model.ChainEthereum, "evm-l1-decoder-v1"},
		{model.ChainPolygon, "evm-l1-decoder-v1"},
		{model.ChainBSC, "evm-l1-decoder-v1"},
		{model.ChainBase, "base-decoder-v1"},
		{model.ChainArbitrum, "base-decoder-v1"},
	}

	for _, tc := range tests {
		t.Run(string(tc.chain), func(t *testing.T) {
			assert.Equal(t, tc.expected, evmDecoderVersion(tc.chain))
		})
	}
}

func TestBuildCanonicalBaseBalanceEvents_PolygonL1Fee(t *testing.T) {
	t.Parallel()

	events := buildCanonicalBaseBalanceEvents(
		model.ChainPolygon, model.NetworkMainnet,
		"0xabc", "SUCCESS", "0xfee_payer", "1000000", "finalized",
		nil,
		"0xfee_payer",
	)

	// Polygon is L1-like, so should produce a single FEE event
	assert.Len(t, events, 1)
	assert.Equal(t, model.ActivityFee, events[0].ActivityType)
	assert.Equal(t, "POL", events[0].TokenSymbol)
	assert.Equal(t, "POL", events[0].AssetID)
	assert.Equal(t, "-1000000", events[0].Delta)
	assert.Equal(t, "evm-l1-decoder-v1", events[0].DecoderVersion)
}

func TestBuildCanonicalBaseBalanceEvents_BSCFee(t *testing.T) {
	t.Parallel()

	events := buildCanonicalBaseBalanceEvents(
		model.ChainBSC, model.NetworkMainnet,
		"0xdef", "SUCCESS", "0xfee_payer", "500000", "finalized",
		nil,
		"0xfee_payer",
	)

	assert.Len(t, events, 1)
	assert.Equal(t, model.ActivityFee, events[0].ActivityType)
	assert.Equal(t, "BNB", events[0].TokenSymbol)
	assert.Equal(t, "BNB", events[0].AssetID)
	assert.Equal(t, "-500000", events[0].Delta)
	assert.Equal(t, "evm-l1-decoder-v1", events[0].DecoderVersion)
}

func TestBuildCanonicalBaseBalanceEvents_ArbitrumL2Fee(t *testing.T) {
	t.Parallel()

	events := buildCanonicalBaseBalanceEvents(
		model.ChainArbitrum, model.NetworkMainnet,
		"0x123", "SUCCESS", "0xfee_payer", "2000000", "finalized",
		nil,
		"0xfee_payer",
	)

	// Arbitrum is L2-like, should produce at least fee_execution_l2 event
	feeEvents := make([]string, 0)
	for _, e := range events {
		feeEvents = append(feeEvents, string(e.ActivityType))
	}

	assert.Contains(t, feeEvents, string(model.ActivityFeeExecutionL2))
	// All Arbitrum events should use ETH
	for _, e := range events {
		assert.Equal(t, "ETH", e.TokenSymbol)
	}
	assert.Equal(t, "base-decoder-v1", events[0].DecoderVersion)
}

func TestBuildCanonicalBaseBalanceEvents_SelfTransfer_NativeETH(t *testing.T) {
	t.Parallel()

	watched := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rawEvents := []*sidecarv1.BalanceEventInfo{
		{
			OuterInstructionIndex: 0,
			InnerInstructionIndex: -1,
			EventCategory:        string(model.EventCategoryTransfer),
			EventAction:           "eth_transfer",
			ProgramId:             "0x0000000000000000000000000000000000000000000000000000000000000000",
			Address:               watched,
			ContractAddress:       "ETH",
			CounterpartyAddress:   watched, // self-transfer
			Delta:                 "-1000000000000000000",
			TokenSymbol:           "ETH",
			TokenName:             "Ether",
			TokenDecimals:         18,
			TokenType:             string(model.TokenTypeNative),
			Metadata:              map[string]string{},
		},
		{
			OuterInstructionIndex: 0,
			InnerInstructionIndex: -1,
			EventCategory:        string(model.EventCategoryTransfer),
			EventAction:           "eth_transfer",
			ProgramId:             "0x0000000000000000000000000000000000000000000000000000000000000000",
			Address:               watched,
			ContractAddress:       "ETH",
			CounterpartyAddress:   watched, // self-transfer
			Delta:                 "1000000000000000000",
			TokenSymbol:           "ETH",
			TokenName:             "Ether",
			TokenDecimals:         18,
			TokenType:             string(model.TokenTypeNative),
			Metadata:              map[string]string{},
		},
	}

	events := buildCanonicalBaseBalanceEvents(
		model.ChainEthereum, model.NetworkMainnet,
		"0xself_eth", "SUCCESS", watched, "21000000000000", "finalized",
		rawEvents, watched,
	)

	// Both raw events become SELF_TRANSFER(delta=0); dedup merges them into 1. Plus 1 fee = 2 events.
	activityTypes := map[model.ActivityType]int{}
	for _, e := range events {
		activityTypes[e.ActivityType]++
		if e.ActivityType == model.ActivitySelfTransfer {
			assert.Equal(t, "0", e.Delta, "self-transfer delta should be 0")
			assert.Equal(t, "self_transfer", e.EventAction)
		}
	}
	assert.Len(t, events, 2)
	assert.Equal(t, 1, activityTypes[model.ActivitySelfTransfer], "should have 1 SELF_TRANSFER event")
	assert.Equal(t, 1, activityTypes[model.ActivityFee], "should have 1 fee event")
}

func TestBuildCanonicalBaseBalanceEvents_SelfTransfer_ERC20(t *testing.T) {
	t.Parallel()

	watched := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	rawEvents := []*sidecarv1.BalanceEventInfo{
		{
			OuterInstructionIndex: 0,
			InnerInstructionIndex: -1,
			EventCategory:        string(model.EventCategoryTransfer),
			EventAction:           "erc20_transfer",
			ProgramId:             "0x0000000000000000000000000000000000000000000000000000000000000000",
			Address:               watched,
			ContractAddress:       "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", // USDC
			CounterpartyAddress:   watched,                                       // self-transfer
			Delta:                 "-1000000",
			TokenSymbol:           "USDC",
			TokenName:             "USD Coin",
			TokenDecimals:         6,
			TokenType:             string(model.TokenTypeFungible),
			Metadata:              map[string]string{},
		},
		{
			OuterInstructionIndex: 0,
			InnerInstructionIndex: -1,
			EventCategory:        string(model.EventCategoryTransfer),
			EventAction:           "erc20_transfer",
			ProgramId:             "0x0000000000000000000000000000000000000000000000000000000000000000",
			Address:               watched,
			ContractAddress:       "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
			CounterpartyAddress:   watched, // self-transfer
			Delta:                 "1000000",
			TokenSymbol:           "USDC",
			TokenName:             "USD Coin",
			TokenDecimals:         6,
			TokenType:             string(model.TokenTypeFungible),
			Metadata:              map[string]string{},
		},
	}

	events := buildCanonicalBaseBalanceEvents(
		model.ChainEthereum, model.NetworkMainnet,
		"0xself_erc20", "SUCCESS", watched, "50000000000000", "finalized",
		rawEvents, watched,
	)

	// Both raw events become SELF_TRANSFER(delta=0); dedup merges them into 1. Plus 1 fee = 2 events.
	activityTypes := map[model.ActivityType]int{}
	for _, e := range events {
		activityTypes[e.ActivityType]++
		if e.ActivityType == model.ActivitySelfTransfer {
			assert.Equal(t, "0", e.Delta, "self-transfer delta should be 0")
			assert.Equal(t, "self_transfer", e.EventAction)
		}
	}
	assert.Len(t, events, 2)
	assert.Equal(t, 1, activityTypes[model.ActivitySelfTransfer], "should have 1 SELF_TRANSFER event")
	assert.Equal(t, 1, activityTypes[model.ActivityFee], "should have 1 fee event")
}

func TestIsEVMChain_AllEVMChains(t *testing.T) {
	t.Parallel()

	evmChains := []model.Chain{
		model.ChainBase,
		model.ChainEthereum,
		model.ChainPolygon,
		model.ChainArbitrum,
		model.ChainBSC,
	}

	nonEVMChains := []model.Chain{
		model.ChainSolana,
		model.ChainBTC,
	}

	for _, chain := range evmChains {
		assert.True(t, isEVMChain(chain), "expected %s to be EVM chain", chain)
	}

	for _, chain := range nonEVMChains {
		assert.False(t, isEVMChain(chain), "expected %s to NOT be EVM chain", chain)
	}
}
