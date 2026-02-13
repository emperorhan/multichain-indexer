package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChainString(t *testing.T) {
	assert.Equal(t, "solana", ChainSolana.String())
	assert.Equal(t, "ethereum", ChainEthereum.String())
}

func TestNetworkString(t *testing.T) {
	assert.Equal(t, "mainnet", NetworkMainnet.String())
	assert.Equal(t, "devnet", NetworkDevnet.String())
	assert.Equal(t, "testnet", NetworkTestnet.String())
}

func TestChainConstants(t *testing.T) {
	assert.Equal(t, Chain("solana"), ChainSolana)
	assert.Equal(t, Chain("ethereum"), ChainEthereum)
}

func TestNetworkConstants(t *testing.T) {
	assert.Equal(t, Network("mainnet"), NetworkMainnet)
	assert.Equal(t, Network("devnet"), NetworkDevnet)
	assert.Equal(t, Network("testnet"), NetworkTestnet)
}

func TestTxStatusConstants(t *testing.T) {
	assert.Equal(t, TxStatus("SUCCESS"), TxStatusSuccess)
	assert.Equal(t, TxStatus("FAILED"), TxStatusFailed)
}

func TestTransferDirectionConstants(t *testing.T) {
	assert.Equal(t, TransferDirection("DEPOSIT"), DirectionDeposit)
	assert.Equal(t, TransferDirection("WITHDRAWAL"), DirectionWithdrawal)
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
