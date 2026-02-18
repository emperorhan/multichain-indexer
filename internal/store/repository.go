package store

import (
	"context"
	"database/sql"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/google/uuid"
)

// TxBeginner abstracts the ability to begin a database transaction.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// WatchedAddressRepository provides access to watched address data.
type WatchedAddressRepository interface {
	GetActive(ctx context.Context, chain model.Chain, network model.Network) ([]model.WatchedAddress, error)
	Upsert(ctx context.Context, addr *model.WatchedAddress) error
	FindByAddress(ctx context.Context, chain model.Chain, network model.Network, address string) (*model.WatchedAddress, error)
}

// CursorRepository provides access to address cursor data.
type CursorRepository interface {
	Get(ctx context.Context, chain model.Chain, network model.Network, address string) (*model.AddressCursor, error)
	UpsertTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, cursorValue *string, cursorSequence int64, itemsProcessed int64) error
	EnsureExists(ctx context.Context, chain model.Chain, network model.Network, address string) error
}

// TransactionRepository provides access to transaction data.
type TransactionRepository interface {
	UpsertTx(ctx context.Context, tx *sql.Tx, t *model.Transaction) (uuid.UUID, error)
}

// BalanceEventRepository provides access to balance event data.
type BalanceEventRepository interface {
	UpsertTx(ctx context.Context, tx *sql.Tx, be *model.BalanceEvent) (bool, error)
}

// BalanceRepository provides access to balance data.
type BalanceRepository interface {
	AdjustBalanceTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, tokenID uuid.UUID, walletID *string, orgID *string, delta string, cursor int64, txHash string) error
	GetAmountTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, tokenID uuid.UUID) (string, error)
	GetAmountWithExistsTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, tokenID uuid.UUID) (amount string, exists bool, err error)
	GetByAddress(ctx context.Context, chain model.Chain, network model.Network, address string) ([]model.Balance, error)
}

// TokenRepository provides access to token data.
type TokenRepository interface {
	UpsertTx(ctx context.Context, tx *sql.Tx, t *model.Token) (uuid.UUID, error)
	FindByContractAddress(ctx context.Context, chain model.Chain, network model.Network, contractAddress string) (*model.Token, error)
	IsDeniedTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddress string) (bool, error)
	DenyTokenTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddress string, reason string, source string, score int16, signals []string) error
	AllowTokenTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddress string, reason string) error
}

// IndexerConfigRepository provides access to indexer configuration and watermark data.
type IndexerConfigRepository interface {
	Get(ctx context.Context, chain model.Chain, network model.Network) (*model.IndexerConfig, error)
	Upsert(ctx context.Context, c *model.IndexerConfig) error
	UpdateWatermarkTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error
}
