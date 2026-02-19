package model

type Chain string

const (
	ChainSolana   Chain = "solana"
	ChainEthereum Chain = "ethereum"
	ChainBase     Chain = "base"
	ChainBTC      Chain = "btc"
	ChainPolygon  Chain = "polygon"
	ChainArbitrum Chain = "arbitrum"
	ChainBSC      Chain = "bsc"
)

func (c Chain) String() string {
	return string(c)
}

type Network string

const (
	NetworkMainnet Network = "mainnet"
	NetworkDevnet  Network = "devnet"
	NetworkTestnet Network = "testnet"
	NetworkSepolia Network = "sepolia"
	NetworkAmoy    Network = "amoy"
)

func (n Network) String() string {
	return string(n)
}

type TxStatus string

const (
	TxStatusSuccess TxStatus = "SUCCESS"
	TxStatusFailed  TxStatus = "FAILED"
)

type TokenType string

const (
	TokenTypeNative   TokenType = "NATIVE"
	TokenTypeFungible TokenType = "FUNGIBLE"
	TokenTypeNFT      TokenType = "NFT"
)

type AddressSource string

const (
	AddressSourceDB  AddressSource = "db"
	AddressSourceEnv AddressSource = "env"
)
