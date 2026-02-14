package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChainString(t *testing.T) {
	assert.Equal(t, "solana", ChainSolana.String())
	assert.Equal(t, "ethereum", ChainEthereum.String())
	assert.Equal(t, "base", ChainBase.String())
}

func TestNetworkString(t *testing.T) {
	assert.Equal(t, "mainnet", NetworkMainnet.String())
	assert.Equal(t, "devnet", NetworkDevnet.String())
	assert.Equal(t, "testnet", NetworkTestnet.String())
	assert.Equal(t, "sepolia", NetworkSepolia.String())
}

func TestChainConstants(t *testing.T) {
	assert.Equal(t, Chain("solana"), ChainSolana)
	assert.Equal(t, Chain("ethereum"), ChainEthereum)
	assert.Equal(t, Chain("base"), ChainBase)
}

func TestNetworkConstants(t *testing.T) {
	assert.Equal(t, Network("mainnet"), NetworkMainnet)
	assert.Equal(t, Network("devnet"), NetworkDevnet)
	assert.Equal(t, Network("testnet"), NetworkTestnet)
	assert.Equal(t, Network("sepolia"), NetworkSepolia)
}

func TestTxStatusConstants(t *testing.T) {
	assert.Equal(t, TxStatus("SUCCESS"), TxStatusSuccess)
	assert.Equal(t, TxStatus("FAILED"), TxStatusFailed)
}

func TestEventCategoryConstants(t *testing.T) {
	assert.Equal(t, EventCategory("TRANSFER"), EventCategoryTransfer)
	assert.Equal(t, EventCategory("STAKE"), EventCategoryStake)
	assert.Equal(t, EventCategory("SWAP"), EventCategorySwap)
	assert.Equal(t, EventCategory("fee_execution_l2"), EventCategoryFeeExecutionL2)
	assert.Equal(t, EventCategory("fee_data_l1"), EventCategoryFeeDataL1)
	assert.Equal(t, EventCategory("FEE"), EventCategoryFee)
}

func TestTokenTypeConstants(t *testing.T) {
	assert.Equal(t, TokenType("NATIVE"), TokenTypeNative)
	assert.Equal(t, TokenType("FUNGIBLE"), TokenTypeFungible)
	assert.Equal(t, TokenType("NFT"), TokenTypeNFT)
}

func TestAddressSourceConstants(t *testing.T) {
	assert.Equal(t, AddressSource("db"), AddressSourceDB)
	assert.Equal(t, AddressSource("env"), AddressSourceEnv)
}
