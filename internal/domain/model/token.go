package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Token struct {
	ID              uuid.UUID       `db:"id"`
	Chain           Chain           `db:"chain"`
	Network         Network         `db:"network"`
	ContractAddress string          `db:"contract_address"`
	Symbol          string          `db:"symbol"`
	Name            string          `db:"name"`
	Decimals        int             `db:"decimals"`
	TokenType       TokenType       `db:"token_type"`
	IsDenied        bool            `db:"is_denied"`
	ChainData       json.RawMessage `db:"chain_data"`
	CreatedAt       time.Time       `db:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at"`
}

// Solana native SOL mint address
const SolanaNativeMint = "11111111111111111111111111111111"
