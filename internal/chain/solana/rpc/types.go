package rpc

import "encoding/json"

// JSON-RPC request/response types

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

// getSlot response
type GetSlotResult int64

// getSignaturesForAddress response
type SignatureInfo struct {
	Signature        string  `json:"signature"`
	Slot             int64   `json:"slot"`
	BlockTime        *int64  `json:"blockTime"`
	Err              interface{} `json:"err"`
	Memo             *string `json:"memo"`
	ConfirmationStatus *string `json:"confirmationStatus"`
}

// getTransaction response (jsonParsed)
type TransactionResponse struct {
	Slot        int64              `json:"slot"`
	BlockTime   *int64             `json:"blockTime"`
	Transaction json.RawMessage    `json:"transaction"`
	Meta        *TransactionMeta   `json:"meta"`
}

type TransactionMeta struct {
	Err               interface{}   `json:"err"`
	Fee               uint64        `json:"fee"`
	PreBalances       []int64       `json:"preBalances"`
	PostBalances      []int64       `json:"postBalances"`
	PreTokenBalances  []TokenBalance `json:"preTokenBalances"`
	PostTokenBalances []TokenBalance `json:"postTokenBalances"`
	InnerInstructions []InnerInstruction `json:"innerInstructions"`
	LogMessages       []string      `json:"logMessages"`
}

type TokenBalance struct {
	AccountIndex  int    `json:"accountIndex"`
	Mint          string `json:"mint"`
	Owner         string `json:"owner"`
	UITokenAmount struct {
		UIAmount *float64 `json:"uiAmount"`
		Decimals int      `json:"decimals"`
		Amount   string   `json:"amount"`
	} `json:"uiTokenAmount"`
	ProgramID string `json:"programId"`
}

type InnerInstruction struct {
	Index        int                    `json:"index"`
	Instructions []json.RawMessage      `json:"instructions"`
}
