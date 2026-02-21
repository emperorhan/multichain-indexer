package rpc

import "encoding/json"

type Request struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return e.Message
}

type Block struct {
	Hash              string         `json:"hash"`
	Height            int64          `json:"height"`
	Time              int64          `json:"time"`
	PreviousBlockHash string         `json:"previousblockhash"`
	Tx                []*Transaction `json:"tx"`
}

type BlockHeader struct {
	Hash              string `json:"hash"`
	Height            int64  `json:"height"`
	PreviousBlockHash string `json:"previousblockhash"`
	Time              int64  `json:"time"`
}

type Transaction struct {
	Txid          string  `json:"txid"`
	Hash          string  `json:"hash"`
	Blockhash     string  `json:"blockhash"`
	Blocktime     int64   `json:"blocktime"`
	Time          int64   `json:"time"`
	Confirmations int64   `json:"confirmations"`
	Vin           []*Vin  `json:"vin"`
	Vout          []*Vout `json:"vout"`
}

type Vin struct {
	Txid     string  `json:"txid"`
	Vout     int     `json:"vout"`
	Coinbase string  `json:"coinbase"`
	Prevout  *Prevout `json:"prevout,omitempty"` // populated when verbosity=3 (Bitcoin Core 25.0+)
}

// Prevout contains the previous output details, available with getblock verbosity=3.
type Prevout struct {
	Value        json.Number  `json:"value"`
	ScriptPubKey ScriptPubKey `json:"scriptPubKey"`
}

type Vout struct {
	Value        json.Number  `json:"value"`
	N            int          `json:"n"`
	ScriptPubKey ScriptPubKey `json:"scriptPubKey"`
}

type ScriptPubKey struct {
	Address   string   `json:"address"`
	Addresses []string `json:"addresses"`
	Type      string   `json:"type"`
}
