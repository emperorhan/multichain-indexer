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
	BulkUpsertTx(ctx context.Context, tx *sql.Tx, txns []*model.Transaction) (map[string]uuid.UUID, error)
}

// UpsertResult describes the outcome of a BalanceEvent upsert.
type UpsertResult struct {
	Inserted        bool // First insertion of this event.
	FinalityCrossed bool // Finality threshold crossed (balance_applied false → true).
}

// BulkUpsertEventResult describes the outcome of a bulk balance event upsert.
type BulkUpsertEventResult struct {
	InsertedCount       int
	FinalityCrossedCount int
}

// BalanceEventRepository provides access to balance event data.
type BalanceEventRepository interface {
	UpsertTx(ctx context.Context, tx *sql.Tx, be *model.BalanceEvent) (UpsertResult, error)
	BulkUpsertTx(ctx context.Context, tx *sql.Tx, events []*model.BalanceEvent) (BulkUpsertEventResult, error)
}

// BalanceKey uniquely identifies a balance record.
type BalanceKey struct {
	Address     string
	TokenID     uuid.UUID
	BalanceType string
}

// BalanceInfo holds the balance amount and existence flag.
type BalanceInfo struct {
	Amount string
	Exists bool
}

// BulkAdjustItem represents a single balance adjustment in a bulk operation.
type BulkAdjustItem struct {
	Address     string
	TokenID     uuid.UUID
	WalletID    *string
	OrgID       *string
	Delta       string
	Cursor      int64
	TxHash      string
	BalanceType string
}

// BalanceRepository provides access to balance data.
type BalanceRepository interface {
	AdjustBalanceTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, tokenID uuid.UUID, walletID *string, orgID *string, delta string, cursor int64, txHash string, balanceType string) error
	GetAmountTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, tokenID uuid.UUID, balanceType string) (string, error)
	GetAmountWithExistsTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, address string, tokenID uuid.UUID, balanceType string) (amount string, exists bool, err error)
	GetByAddress(ctx context.Context, chain model.Chain, network model.Network, address string) ([]model.Balance, error)
	BulkGetAmountWithExistsTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, keys []BalanceKey) (map[BalanceKey]BalanceInfo, error)
	BulkAdjustBalanceTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, items []BulkAdjustItem) error
}

// TokenRepository provides access to token data.
type TokenRepository interface {
	UpsertTx(ctx context.Context, tx *sql.Tx, t *model.Token) (uuid.UUID, error)
	BulkUpsertTx(ctx context.Context, tx *sql.Tx, tokens []*model.Token) (map[string]uuid.UUID, error)
	FindByContractAddress(ctx context.Context, chain model.Chain, network model.Network, contractAddress string) (*model.Token, error)
	IsDeniedTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddress string) (bool, error)
	BulkIsDeniedTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddresses []string) (map[string]bool, error)
	DenyTokenTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddress string, reason string, source string, score int16, signals []string) error
	AllowTokenTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, contractAddress string, reason string) error
}

// IndexerConfigRepository provides access to indexer configuration and watermark data.
type IndexerConfigRepository interface {
	Get(ctx context.Context, chain model.Chain, network model.Network) (*model.IndexerConfig, error)
	Upsert(ctx context.Context, c *model.IndexerConfig) error
	UpdateWatermarkTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error
	GetWatermark(ctx context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error)
}

// IndexedBlockRepository provides access to indexed block metadata for reorg detection.
type IndexedBlockRepository interface {
	UpsertTx(ctx context.Context, tx *sql.Tx, block *model.IndexedBlock) error
	BulkUpsertTx(ctx context.Context, tx *sql.Tx, blocks []*model.IndexedBlock) error
	GetUnfinalized(ctx context.Context, chain model.Chain, network model.Network) ([]model.IndexedBlock, error)
	GetByBlockNumber(ctx context.Context, chain model.Chain, network model.Network, blockNumber int64) (*model.IndexedBlock, error)
	UpdateFinalityTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, upToBlock int64, newState string) error
	DeleteFromBlockTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, fromBlock int64) error
	PurgeFinalizedBefore(ctx context.Context, chain model.Chain, network model.Network, beforeBlock int64) (int64, error)
}

// RuntimeConfigRepository provides access to runtime-overridable configuration.
type RuntimeConfigRepository interface {
	GetActive(ctx context.Context, chain model.Chain, network model.Network) (map[string]string, error)
}
