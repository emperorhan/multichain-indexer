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
	Number       string         `json:"number"`
	Hash         string         `json:"hash"`
	ParentHash   string         `json:"parentHash"`
	Timestamp    string         `json:"timestamp"`
	Transactions []*Transaction `json:"transactions"`
}

type Transaction struct {
	Hash             string `json:"hash"`
	BlockNumber      string `json:"blockNumber"`
	TransactionIndex string `json:"transactionIndex"`
	From             string `json:"from"`
	To               string `json:"to"`
	Value            string `json:"value"`
	GasPrice         string `json:"gasPrice"`
}

type TransactionReceipt struct {
	TransactionHash   string  `json:"transactionHash"`
	BlockNumber       string  `json:"blockNumber"`
	TransactionIndex  string  `json:"transactionIndex"`
	Status            string  `json:"status"`
	From              string  `json:"from"`
	To                string  `json:"to"`
	GasUsed           string  `json:"gasUsed"`
	EffectiveGasPrice string  `json:"effectiveGasPrice"`
	L1Fee             *string `json:"l1Fee"`
	L1GasUsed         *string `json:"l1GasUsed"`
	L1GasPrice        *string `json:"l1GasPrice"`
	Logs              []*Log  `json:"logs"`
}

type Log struct {
	Address  string   `json:"address"`
	Topics   []string `json:"topics"`
	Data     string   `json:"data"`
	LogIndex string   `json:"logIndex"`
	Removed  bool     `json:"removed"`
}
