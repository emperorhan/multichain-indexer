package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/config"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type staticChainAdapter struct {
	chain string
}

func (a *staticChainAdapter) Chain() string { return a.chain }

func (a *staticChainAdapter) GetHeadSequence(context.Context) (int64, error) { return 0, nil }

func (a *staticChainAdapter) FetchNewSignatures(context.Context, string, *string, int) ([]chain.SignatureInfo, error) {
	return nil, nil
}

func (a *staticChainAdapter) FetchTransactions(context.Context, []string) ([]json.RawMessage, error) {
	return nil, nil
}

func TestBuildRuntimeTargets_IncludesMandatoryChainsDeterministically(t *testing.T) {
	cfg := &config.Config{
		Solana: config.SolanaConfig{
			RPCURL:  "https://solana.example",
			Network: string(model.NetworkDevnet),
		},
		Base: config.BaseConfig{
			RPCURL:  "https://base.example",
			Network: string(model.NetworkSepolia),
		},
		BTC: config.BTCConfig{
			RPCURL:  "https://btc.example",
			Network: string(model.NetworkTestnet),
		},
		Pipeline: config.PipelineConfig{
			SolanaWatchedAddresses: []string{"sol-1", "sol-2"},
			BaseWatchedAddresses:   []string{"0xabc", "0xdef"},
			BTCWatchedAddresses:    []string{"tb1abc", "tb1def"},
		},
	}

	targets := buildRuntimeTargets(cfg, slog.Default())
	require.Len(t, targets, 3)

	assert.Equal(t, model.ChainSolana, targets[0].chain)
	assert.Equal(t, model.NetworkDevnet, targets[0].network)
	assert.Equal(t, config.RuntimeLikeGroupSolana, targets[0].group)
	assert.Equal(t, []string{"sol-1", "sol-2"}, targets[0].watched)
	assert.Equal(t, "https://solana.example", targets[0].rpcURL)
	assert.Equal(t, "solana", targets[0].adapter.Chain())

	assert.Equal(t, model.ChainBase, targets[1].chain)
	assert.Equal(t, model.NetworkSepolia, targets[1].network)
	assert.Equal(t, config.RuntimeLikeGroupEVM, targets[1].group)
	assert.Equal(t, []string{"0xabc", "0xdef"}, targets[1].watched)
	assert.Equal(t, "https://base.example", targets[1].rpcURL)
	assert.Equal(t, "base", targets[1].adapter.Chain())

	assert.Equal(t, model.ChainBTC, targets[2].chain)
	assert.Equal(t, model.NetworkTestnet, targets[2].network)
	assert.Equal(t, config.RuntimeLikeGroupBTC, targets[2].group)
	assert.Equal(t, []string{"tb1abc", "tb1def"}, targets[2].watched)
	assert.Equal(t, "https://btc.example", targets[2].rpcURL)
	assert.Equal(t, "btc", targets[2].adapter.Chain())
	require.NoError(t, validateRuntimeWiring(targets))
}

func TestValidateRuntimeWiring_AllowsSingleTarget(t *testing.T) {
	targets := []runtimeTarget{
		{
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			group:   config.RuntimeLikeGroupSolana,
			adapter: &staticChainAdapter{chain: model.ChainSolana.String()},
		},
	}

	require.NoError(t, validateRuntimeWiring(targets))
}

func TestValidateRuntimeWiring_FailsWhenAdapterChainMismatched(t *testing.T) {
	targets := []runtimeTarget{
		{
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			group:   config.RuntimeLikeGroupSolana,
			adapter: &staticChainAdapter{chain: model.ChainSolana.String()},
		},
		{
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			group:   config.RuntimeLikeGroupEVM,
			adapter: &staticChainAdapter{chain: model.ChainEthereum.String()},
		},
	}

	err := validateRuntimeWiring(targets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "adapter mismatch for base-sepolia")
}

func TestValidateRuntimeWiring_FailsWhenRuntimeGroupMismatched(t *testing.T) {
	targets := []runtimeTarget{
		{
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			group:   config.RuntimeLikeGroupSolana,
			adapter: &staticChainAdapter{chain: model.ChainBase.String()},
		},
	}

	err := validateRuntimeWiring(targets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime group mismatch for base-sepolia")
	assert.Contains(t, err.Error(), fmt.Sprintf("expected=%s", config.RuntimeLikeGroupEVM))
}

func TestValidateRuntimeWiring_AllowsNonMandatoryNetworkWhenAdapterMatches(t *testing.T) {
	targets := []runtimeTarget{
		{
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			group:   config.RuntimeLikeGroupSolana,
			adapter: &staticChainAdapter{chain: model.ChainSolana.String()},
		},
		{
			chain:   model.ChainBase,
			network: model.NetworkMainnet,
			group:   config.RuntimeLikeGroupEVM,
			adapter: &staticChainAdapter{chain: model.ChainBase.String()},
		},
	}

	require.NoError(t, validateRuntimeWiring(targets))
}

func TestValidateRuntimeWiring_FailsWhenMandatoryTargetHasDuplicateEntries(t *testing.T) {
	targets := []runtimeTarget{
		{
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			group:   config.RuntimeLikeGroupSolana,
			adapter: &staticChainAdapter{chain: model.ChainSolana.String()},
		},
		{
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			group:   config.RuntimeLikeGroupSolana,
			adapter: &staticChainAdapter{chain: model.ChainSolana.String()},
		},
		{
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			group:   config.RuntimeLikeGroupEVM,
			adapter: &staticChainAdapter{chain: model.ChainBase.String()},
		},
	}

	err := validateRuntimeWiring(targets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate target solana-devnet")
}

func TestValidateRuntimeWiring_FailsWhenMandatoryTargetAdapterIsNil(t *testing.T) {
	targets := []runtimeTarget{
		{
			chain:   model.ChainSolana,
			network: model.NetworkDevnet,
			group:   config.RuntimeLikeGroupSolana,
			adapter: nil,
		},
		{
			chain:   model.ChainBase,
			network: model.NetworkSepolia,
			group:   config.RuntimeLikeGroupEVM,
			adapter: &staticChainAdapter{chain: model.ChainBase.String()},
		},
	}

	err := validateRuntimeWiring(targets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil adapter for target solana-devnet")
}

func TestValidateRuntimeWiring_FailsWhenNoTargetsSelected(t *testing.T) {
	err := validateRuntimeWiring(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no runtime targets selected")
}

func TestSelectRuntimeTargets_LikeGroupDefaultSelectsAll(t *testing.T) {
	all := []runtimeTarget{
		{chain: model.ChainSolana, network: model.NetworkDevnet, group: config.RuntimeLikeGroupSolana},
		{chain: model.ChainBase, network: model.NetworkSepolia, group: config.RuntimeLikeGroupEVM},
		{chain: model.ChainBTC, network: model.NetworkTestnet, group: config.RuntimeLikeGroupBTC},
	}

	selected, err := selectRuntimeTargets(all, config.RuntimeConfig{DeploymentMode: config.RuntimeDeploymentModeLikeGroup})
	require.NoError(t, err)
	require.Len(t, selected, 3)
	assert.Equal(t, model.ChainSolana, selected[0].chain)
	assert.Equal(t, model.ChainBase, selected[1].chain)
	assert.Equal(t, model.ChainBTC, selected[2].chain)
}

func TestSelectRuntimeTargets_LikeGroupFilter(t *testing.T) {
	all := []runtimeTarget{
		{chain: model.ChainSolana, network: model.NetworkDevnet, group: config.RuntimeLikeGroupSolana},
		{chain: model.ChainBase, network: model.NetworkSepolia, group: config.RuntimeLikeGroupEVM},
		{chain: model.ChainBTC, network: model.NetworkTestnet, group: config.RuntimeLikeGroupBTC},
	}

	selected, err := selectRuntimeTargets(all, config.RuntimeConfig{
		DeploymentMode: config.RuntimeDeploymentModeLikeGroup,
		LikeGroup:      config.RuntimeLikeGroupEVM,
	})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	assert.Equal(t, model.ChainBase, selected[0].chain)
}

func TestSelectRuntimeTargets_LikeGroupFilterBTC(t *testing.T) {
	all := []runtimeTarget{
		{chain: model.ChainSolana, network: model.NetworkDevnet, group: config.RuntimeLikeGroupSolana},
		{chain: model.ChainBase, network: model.NetworkSepolia, group: config.RuntimeLikeGroupEVM},
		{chain: model.ChainBTC, network: model.NetworkTestnet, group: config.RuntimeLikeGroupBTC},
	}

	selected, err := selectRuntimeTargets(all, config.RuntimeConfig{
		DeploymentMode: config.RuntimeDeploymentModeLikeGroup,
		LikeGroup:      config.RuntimeLikeGroupBTC,
	})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	assert.Equal(t, model.ChainBTC, selected[0].chain)
	assert.Equal(t, model.NetworkTestnet, selected[0].network)
}

func TestSelectRuntimeTargets_IndependentMode(t *testing.T) {
	all := []runtimeTarget{
		{chain: model.ChainSolana, network: model.NetworkDevnet, group: config.RuntimeLikeGroupSolana},
		{chain: model.ChainBase, network: model.NetworkSepolia, group: config.RuntimeLikeGroupEVM},
		{chain: model.ChainBTC, network: model.NetworkTestnet, group: config.RuntimeLikeGroupBTC},
	}

	selected, err := selectRuntimeTargets(all, config.RuntimeConfig{
		DeploymentMode: config.RuntimeDeploymentModeIndependent,
		ChainTargets:   []string{"base-sepolia"},
	})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	assert.Equal(t, model.ChainBase, selected[0].chain)
	assert.Equal(t, model.NetworkSepolia, selected[0].network)
}

func TestSelectRuntimeTargets_IndependentModeBTC(t *testing.T) {
	all := []runtimeTarget{
		{chain: model.ChainSolana, network: model.NetworkDevnet, group: config.RuntimeLikeGroupSolana},
		{chain: model.ChainBase, network: model.NetworkSepolia, group: config.RuntimeLikeGroupEVM},
		{chain: model.ChainBTC, network: model.NetworkTestnet, group: config.RuntimeLikeGroupBTC},
	}

	selected, err := selectRuntimeTargets(all, config.RuntimeConfig{
		DeploymentMode: config.RuntimeDeploymentModeIndependent,
		ChainTargets:   []string{"btc-testnet"},
	})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	assert.Equal(t, model.ChainBTC, selected[0].chain)
	assert.Equal(t, model.NetworkTestnet, selected[0].network)
}

func TestSelectRuntimeTargets_FailsWhenUnknownTargetRequested(t *testing.T) {
	all := []runtimeTarget{
		{chain: model.ChainSolana, network: model.NetworkDevnet, group: config.RuntimeLikeGroupSolana},
		{chain: model.ChainBase, network: model.NetworkSepolia, group: config.RuntimeLikeGroupEVM},
		{chain: model.ChainBTC, network: model.NetworkTestnet, group: config.RuntimeLikeGroupBTC},
	}

	_, err := selectRuntimeTargets(all, config.RuntimeConfig{
		DeploymentMode: config.RuntimeDeploymentModeLikeGroup,
		ChainTargets:   []string{"ethereum-mainnet"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requested runtime chain targets not found")
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
