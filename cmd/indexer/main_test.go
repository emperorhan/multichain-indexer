package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/config"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestBuildRuntimeTargets_IncludesSolanaAndBaseDeterministically(t *testing.T) {
	cfg := &config.Config{
		Solana: config.SolanaConfig{
			RPCURL:  "https://solana.example",
			Network: string(model.NetworkDevnet),
		},
		Base: config.BaseConfig{
			RPCURL:  "https://base.example",
			Network: string(model.NetworkSepolia),
		},
		Pipeline: config.PipelineConfig{
			SolanaWatchedAddresses: []string{"sol-1", "sol-2"},
			BaseWatchedAddresses:   []string{"0xabc", "0xdef"},
		},
	}

	targets := buildRuntimeTargets(cfg, slog.Default())
	require.Len(t, targets, 2)

	assert.Equal(t, model.ChainSolana, targets[0].chain)
	assert.Equal(t, model.NetworkDevnet, targets[0].network)
	assert.Equal(t, []string{"sol-1", "sol-2"}, targets[0].watched)
	assert.Equal(t, "https://solana.example", targets[0].rpcURL)
	assert.Equal(t, "solana", targets[0].adapter.Chain())

	assert.Equal(t, model.ChainBase, targets[1].chain)
	assert.Equal(t, model.NetworkSepolia, targets[1].network)
	assert.Equal(t, []string{"0xabc", "0xdef"}, targets[1].watched)
	assert.Equal(t, "https://base.example", targets[1].rpcURL)
	assert.Equal(t, "base", targets[1].adapter.Chain())
}

func TestSyncWatchedAddresses_UpsertsAndInitializesCursors(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedRepo := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursorRepo := storemocks.NewMockCursorRepository(ctrl)

	chain := model.ChainBase
	network := model.NetworkSepolia
	addresses := []string{
		"0x1111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222",
	}

	seen := make([]model.WatchedAddress, 0, len(addresses))
	mockWatchedRepo.EXPECT().
		Upsert(gomock.Any(), gomock.Any()).
		Times(len(addresses)).
		DoAndReturn(func(_ context.Context, addr *model.WatchedAddress) error {
			seen = append(seen, *addr)
			return nil
		})

	for _, addr := range addresses {
		mockCursorRepo.EXPECT().
			EnsureExists(gomock.Any(), chain, network, addr).
			Return(nil).
			Times(1)
	}

	err := syncWatchedAddresses(context.Background(), mockWatchedRepo, mockCursorRepo, chain, network, addresses)
	require.NoError(t, err)
	require.Len(t, seen, len(addresses))

	assert.Equal(t, chain, seen[0].Chain)
	assert.Equal(t, network, seen[0].Network)
	assert.Equal(t, addresses[0], seen[0].Address)
	assert.True(t, seen[0].IsActive)
	assert.Equal(t, model.AddressSourceEnv, seen[0].Source)

	assert.Equal(t, chain, seen[1].Chain)
	assert.Equal(t, network, seen[1].Network)
	assert.Equal(t, addresses[1], seen[1].Address)
	assert.True(t, seen[1].IsActive)
	assert.Equal(t, model.AddressSourceEnv, seen[1].Source)
}

func TestSyncWatchedAddresses_StopsOnUpsertFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedRepo := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursorRepo := storemocks.NewMockCursorRepository(ctrl)

	chain := model.ChainSolana
	network := model.NetworkDevnet
	addresses := []string{"sol-1", "sol-2"}

	mockWatchedRepo.EXPECT().
		Upsert(gomock.Any(), gomock.Any()).
		Return(errors.New("upsert failed")).
		Times(1)

	err := syncWatchedAddresses(context.Background(), mockWatchedRepo, mockCursorRepo, chain, network, addresses)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert watched address")
}

func TestSyncWatchedAddresses_StopsOnEnsureExistsFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockWatchedRepo := storemocks.NewMockWatchedAddressRepository(ctrl)
	mockCursorRepo := storemocks.NewMockCursorRepository(ctrl)

	chain := model.ChainBase
	network := model.NetworkSepolia
	addresses := []string{"0xabc"}

	mockWatchedRepo.EXPECT().
		Upsert(gomock.Any(), gomock.Any()).
		Return(nil).
		Times(1)
	mockCursorRepo.EXPECT().
		EnsureExists(gomock.Any(), chain, network, addresses[0]).
		Return(errors.New("cursor failed")).
		Times(1)

	err := syncWatchedAddresses(context.Background(), mockWatchedRepo, mockCursorRepo, chain, network, addresses)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ensure cursor")
}
