package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/event"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

// Coordinator iterates over watched addresses and creates FetchJobs.
type Coordinator struct {
	chain           model.Chain
	network         model.Network
	watchedAddrRepo store.WatchedAddressRepository
	cursorRepo      store.CursorRepository
	batchSize       int
	interval        time.Duration
	jobCh           chan<- event.FetchJob
	logger          *slog.Logger
}

func New(
	chain model.Chain,
	network model.Network,
	watchedAddrRepo store.WatchedAddressRepository,
	cursorRepo store.CursorRepository,
	batchSize int,
	interval time.Duration,
	jobCh chan<- event.FetchJob,
	logger *slog.Logger,
) *Coordinator {
	return &Coordinator{
		chain:           chain,
		network:         network,
		watchedAddrRepo: watchedAddrRepo,
		cursorRepo:      cursorRepo,
		batchSize:       batchSize,
		interval:        interval,
		jobCh:           jobCh,
		logger:          logger.With("component", "coordinator"),
	}
}

func (c *Coordinator) Run(ctx context.Context) error {
	c.logger.Info("coordinator started", "chain", c.chain, "network", c.network, "interval", c.interval)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Run immediately on start, then on interval
	if err := c.tick(ctx); err != nil {
		panic(fmt.Sprintf("coordinator tick failed: %v", err))
	}

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("coordinator stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := c.tick(ctx); err != nil {
				panic(fmt.Sprintf("coordinator tick failed: %v", err))
			}
		}
	}
}

func (c *Coordinator) tick(ctx context.Context) error {
	addresses, err := c.watchedAddrRepo.GetActive(ctx, c.chain, c.network)
	if err != nil {
		return err
	}

	c.logger.Debug("creating fetch jobs", "address_count", len(addresses))

	for _, addr := range addresses {
		cursor, err := c.cursorRepo.Get(ctx, c.chain, c.network, addr.Address)
		if err != nil {
			return fmt.Errorf("get cursor %s: %w", addr.Address, err)
		}

		var cursorValue *string
		var cursorSequence int64
		if cursor != nil {
			cursorValue = cursor.CursorValue
			cursorSequence = cursor.CursorSequence
		}

		job := event.FetchJob{
			Chain:          c.chain,
			Network:        c.network,
			Address:        addr.Address,
			CursorValue:    cursorValue,
			CursorSequence: cursorSequence,
			BatchSize:      c.batchSize,
			WalletID:       addr.WalletID,
			OrgID:          addr.OrganizationID,
		}

		select {
		case c.jobCh <- job:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}
