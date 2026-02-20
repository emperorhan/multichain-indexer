package store

import (
	"context"
	"database/sql"
	"time"

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
	RecalculateBalanceFieldsTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, fromBlock int64) error
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

// AdjustRequest holds the parameters for a single balance adjustment.
type AdjustRequest struct {
	Chain       model.Chain
	Network     model.Network
	Address     string
	TokenID     uuid.UUID
	WalletID    *string
	OrgID       *string
	Delta       string
	Cursor      int64
	TxHash      string
	BalanceType string // "" for liquid, "staked" for staked
}

// BalanceRepository provides access to balance data.
type BalanceRepository interface {
	AdjustBalanceTx(ctx context.Context, tx *sql.Tx, req AdjustRequest) error
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
	FindByID(ctx context.Context, id uuid.UUID) (*model.Token, error)
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
	RewindWatermarkTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error
	GetWatermark(ctx context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error)
}

// IndexedBlockRepository provides access to indexed block metadata for reorg detection.
type IndexedBlockRepository interface {
	UpsertTx(ctx context.Context, tx *sql.Tx, block *model.IndexedBlock) error
	BulkUpsertTx(ctx context.Context, tx *sql.Tx, blocks []*model.IndexedBlock) error
	GetUnfinalized(ctx context.Context, chain model.Chain, network model.Network) ([]model.IndexedBlock, error)
	GetByBlockNumber(ctx context.Context, chain model.Chain, network model.Network, blockNumber int64) (*model.IndexedBlock, error)
	UpdateFinalityTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, upToBlock int64, newState string) error
	DeleteFromBlockTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, fromBlock int64) (int64, error)
	PurgeFinalizedBefore(ctx context.Context, chain model.Chain, network model.Network, beforeBlock int64) (int64, error)
}

// RuntimeConfigRepository provides access to runtime-overridable configuration.
type RuntimeConfigRepository interface {
	GetActive(ctx context.Context, chain model.Chain, network model.Network) (map[string]string, error)
}

// DashboardTokenBalance represents a single token balance for a watched address.
type DashboardTokenBalance struct {
	TokenSymbol string    `json:"token_symbol"`
	TokenName   string    `json:"token_name"`
	Decimals    int       `json:"decimals"`
	Amount      string    `json:"amount"`
	BalanceType string    `json:"balance_type"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DashboardAddressBalance groups balances by watched address.
type DashboardAddressBalance struct {
	Address  string                  `json:"address"`
	Label    *string                 `json:"label,omitempty"`
	WalletID *string                 `json:"wallet_id,omitempty"`
	Balances []DashboardTokenBalance `json:"balances"`
}

// DashboardEvent represents a recent balance event for the dashboard.
type DashboardEvent struct {
	TxHash              string     `json:"tx_hash"`
	Address             string     `json:"address"`
	CounterpartyAddress string     `json:"counterparty_address"`
	ActivityType        string     `json:"activity_type"`
	Delta               string     `json:"delta"`
	TokenSymbol         string     `json:"token_symbol"`
	Decimals            int        `json:"decimals"`
	BlockCursor         int64      `json:"block_cursor"`
	BlockTime           *time.Time `json:"block_time"`
	FinalityState       string     `json:"finality_state"`
	CreatedAt           time.Time  `json:"created_at"`
}

// DashboardRepository provides read-only access to dashboard data.
type DashboardRepository interface {
	GetBalanceSummary(ctx context.Context, chain model.Chain, network model.Network) ([]DashboardAddressBalance, error)
	GetRecentEvents(ctx context.Context, chain model.Chain, network model.Network, address string, limit, offset int) ([]DashboardEvent, int, error)
	GetAllWatermarks(ctx context.Context) ([]model.PipelineWatermark, error)
	CountWatchedAddresses(ctx context.Context) (int, error)
}
