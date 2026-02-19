package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emperorhan/multichain-indexer/internal/chain"
	"github.com/emperorhan/multichain-indexer/internal/config"
	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	storemocks "github.com/emperorhan/multichain-indexer/internal/store/mocks"
	redispkg "github.com/emperorhan/multichain-indexer/internal/store/redis"
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

func TestResolveStreamBackend_FailsWhenStreamEnabledAndRedisUnavailable(t *testing.T) {
	cfg := &config.Config{
		Pipeline: config.PipelineConfig{
			StreamTransportEnabled: true,
		},
		Redis: config.RedisConfig{
			URL: "redis://invalid:6379",
		},
	}

	inMemoryCalled := false
	streamFactoryCalled := false
	originalNewStream := newStreamFactory
	originalNewInMemoryStream := newInMemoryStreamFactory
	newStreamFactory = func(_ string) (redispkg.MessageTransport, error) {
		streamFactoryCalled = true
		return nil, errors.New("redis unavailable")
	}
	newInMemoryStreamFactory = func() redispkg.MessageTransport {
		inMemoryCalled = true
		return redispkg.NewInMemoryStream()
	}
	defer func() { newStreamFactory = originalNewStream }()
	defer func() { newInMemoryStreamFactory = originalNewInMemoryStream }()

	streamBackend, streamEnabled, err := resolveStreamBackend(cfg, "memory-fallback", slog.Default())
	require.Error(t, err)
	assert.True(t, streamEnabled)
	assert.True(t, streamFactoryCalled)
	assert.False(t, inMemoryCalled)
	assert.Nil(t, streamBackend)
	assert.Contains(t, err.Error(), "initialize redis stream transport")
}

func TestResolveStreamBackend_FailsWhenStreamEnabledAndRedisURLEmpty(t *testing.T) {
	cfg := &config.Config{
		Pipeline: config.PipelineConfig{
			StreamTransportEnabled: true,
		},
		Redis: config.RedisConfig{
			URL: "   ",
		},
	}

	streamFactoryCalled := false
	inMemoryCalled := false
	originalNewStream := newStreamFactory
	originalNewInMemoryStream := newInMemoryStreamFactory
	newStreamFactory = func(_ string) (redispkg.MessageTransport, error) {
		streamFactoryCalled = true
		return nil, errors.New("should not be called when redis URL is empty")
	}
	newInMemoryStreamFactory = func() redispkg.MessageTransport {
		inMemoryCalled = true
		return redispkg.NewInMemoryStream()
	}
	defer func() { newStreamFactory = originalNewStream }()
	defer func() { newInMemoryStreamFactory = originalNewInMemoryStream }()

	streamBackend, streamEnabled, err := resolveStreamBackend(cfg, "memory-fallback", slog.Default())
	require.Error(t, err)
	assert.True(t, streamEnabled)
	assert.False(t, streamFactoryCalled)
	assert.False(t, inMemoryCalled)
	assert.Nil(t, streamBackend)
	assert.Contains(t, err.Error(), "initialize redis stream transport: redis URL is empty")
}

func TestResolveStreamBackend_UsesInMemoryTransportWhenStreamDisabled(t *testing.T) {
	cfg := &config.Config{
		Pipeline: config.PipelineConfig{
			StreamTransportEnabled: false,
		},
	}

	streamFactoryCalled := false
	originalNewInMemoryStream := newInMemoryStreamFactory
	originalNewStream := newStreamFactory
	newInMemoryStreamFactory = func() redispkg.MessageTransport {
		return redispkg.NewInMemoryStream()
	}
	newStreamFactory = func(_ string) (redispkg.MessageTransport, error) {
		streamFactoryCalled = true
		return redispkg.NewInMemoryStream(), nil
	}
	defer func() { newInMemoryStreamFactory = originalNewInMemoryStream }()
	defer func() { newStreamFactory = originalNewStream }()

	streamBackend, streamEnabled, err := resolveStreamBackend(cfg, "memory-fallback", slog.Default())
	require.NoError(t, err)
	require.NotNil(t, streamBackend)
	assert.False(t, streamEnabled)
	assert.False(t, streamFactoryCalled)
	assert.NoError(t, streamBackend.Close())
}

func TestResolveStreamBackend_UsesRedisStreamWhenEnabled(t *testing.T) {
	cfg := &config.Config{
		Pipeline: config.PipelineConfig{
			StreamTransportEnabled: true,
		},
		Redis: config.RedisConfig{
			URL: "redis://127.0.0.1:6379/0",
		},
	}

	inMemoryCalled := false
	originalNewStream := newStreamFactory
	originalNewInMemoryStream := newInMemoryStreamFactory
	expectedBackend := redispkg.NewInMemoryStream()
	streamFactoryCalled := false

	newStreamFactory = func(_ string) (redispkg.MessageTransport, error) {
		streamFactoryCalled = true
		return expectedBackend, nil
	}
	newInMemoryStreamFactory = func() redispkg.MessageTransport {
		inMemoryCalled = true
		return redispkg.NewInMemoryStream()
	}
	defer func() {
		newStreamFactory = originalNewStream
		newInMemoryStreamFactory = originalNewInMemoryStream
	}()

	streamBackend, streamEnabled, err := resolveStreamBackend(cfg, "stream-session", slog.Default())
	require.NoError(t, err)
	assert.True(t, streamEnabled)
	assert.True(t, streamFactoryCalled)
	assert.False(t, inMemoryCalled)
	assert.Same(t, expectedBackend, streamBackend)
	assert.NoError(t, streamBackend.Close())
}

func TestResolveStreamSessionID_DefaultsToDefault(t *testing.T) {
	assert.Equal(t, "default", resolveStreamSessionID(""))
	assert.Equal(t, "default", resolveStreamSessionID("   "))
	assert.Equal(t, "explicit-session", resolveStreamSessionID(" explicit-session "))
}

func TestResolveStreamSessionID_StartupRestart_StableDefaultCheckpointKeysForMandatoryChains(t *testing.T) {
	sessionA := resolveStreamSessionID("")
	sessionB := resolveStreamSessionID("   ")
	assert.Equal(t, "default", sessionA)
	assert.Equal(t, sessionA, sessionB)

	testCases := []struct {
		name     string
		chain    model.Chain
		network  model.Network
		expected string
	}{
		{
			name:     "solana-devnet",
			chain:    model.ChainSolana,
			network:  model.NetworkDevnet,
			expected: "stream-checkpoint:chain=solana:network=devnet:session=default:boundary=fetcher-normalizer",
		},
		{
			name:     "base-sepolia",
			chain:    model.ChainBase,
			network:  model.NetworkSepolia,
			expected: "stream-checkpoint:chain=base:network=sepolia:session=default:boundary=fetcher-normalizer",
		},
		{
			name:     "btc-testnet",
			chain:    model.ChainBTC,
			network:  model.NetworkTestnet,
			expected: "stream-checkpoint:chain=btc:network=testnet:session=default:boundary=fetcher-normalizer",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			firstKey := startupBoundaryCheckpointKey(tc.chain, tc.network, sessionA)
			secondKey := startupBoundaryCheckpointKey(tc.chain, tc.network, sessionB)

			assert.Equal(t, tc.expected, firstKey)
			assert.Equal(t, tc.expected, secondKey)
			assert.Equal(t, firstKey, secondKey)
		})
	}
}

func startupBoundaryCheckpointKey(chain model.Chain, network model.Network, sessionID string) string {
	normalizedSessionID := resolveStreamSessionID(sessionID)
	return fmt.Sprintf(
		"stream-checkpoint:chain=%s:network=%s:session=%s:boundary=%s",
		chain,
		network,
		normalizedSessionID,
		"fetcher-normalizer",
	)
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

func TestBuildRuntimeTargets_IncludesEthereumMainnetWhenRequested(t *testing.T) {
	cfg := &config.Config{
		Solana: config.SolanaConfig{
			RPCURL:  "https://solana.example",
			Network: string(model.NetworkDevnet),
		},
		Base: config.BaseConfig{
			RPCURL:  "https://base.example",
			Network: string(model.NetworkSepolia),
		},
		Ethereum: config.EthereumConfig{
			RPCURL:  "https://eth.example",
			Network: "mainnet",
		},
		BTC: config.BTCConfig{
			RPCURL:  "https://btc.example",
			Network: string(model.NetworkTestnet),
		},
		Pipeline: config.PipelineConfig{
			EthereumWatchedAddresses: []string{"0xeth1", "0xeth2"},
		},
		Runtime: config.RuntimeConfig{
			DeploymentMode: config.RuntimeDeploymentModeIndependent,
			ChainTargets:   []string{"ethereum-mainnet"},
		},
	}

	targets := buildRuntimeTargets(cfg, slog.Default())
	require.Len(t, targets, 4)

	var eth *runtimeTarget
	for idx := range targets {
		if targets[idx].chain == model.ChainEthereum {
			eth = &targets[idx]
			break
		}
	}
	require.NotNil(t, eth)
	assert.Equal(t, model.NetworkMainnet, eth.network)
	assert.Equal(t, config.RuntimeLikeGroupEVM, eth.group)
	assert.Equal(t, "https://eth.example", eth.rpcURL)
	assert.Equal(t, []string{"0xeth1", "0xeth2"}, eth.watched)
	assert.Equal(t, model.ChainEthereum.String(), eth.adapter.Chain())
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

func TestFilterRuntimeTargetsByKeys_DeterministicAcceptanceOrderAndDedup(t *testing.T) {
	all := []runtimeTarget{
		{chain: model.ChainSolana, network: model.NetworkDevnet, group: config.RuntimeLikeGroupSolana},
		{chain: model.ChainBase, network: model.NetworkSepolia, group: config.RuntimeLikeGroupEVM},
		{chain: model.ChainBTC, network: model.NetworkTestnet, group: config.RuntimeLikeGroupBTC},
	}

	selected, missing := filterRuntimeTargetsByKeys(all, []string{
		"dogecoin-mainnet",
		"base-sepolia",
		"solana-devnet",
		"base-sepolia",
		"",
		"  ",
	})

	require.Len(t, selected, 2)
	assert.Equal(t, model.ChainSolana, selected[0].chain)
	assert.Equal(t, model.ChainBase, selected[1].chain)
	assert.Equal(t, []string{"dogecoin-mainnet"}, missing)
}

func TestSelectRuntimeTargets_IndependentModeEthereumMainnet(t *testing.T) {
	all := []runtimeTarget{
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
		{
			chain:   model.ChainEthereum,
			network: model.NetworkMainnet,
			group:   config.RuntimeLikeGroupEVM,
			adapter: &staticChainAdapter{chain: model.ChainEthereum.String()},
		},
		{
			chain:   model.ChainBTC,
			network: model.NetworkTestnet,
			group:   config.RuntimeLikeGroupBTC,
			adapter: &staticChainAdapter{chain: model.ChainBTC.String()},
		},
	}

	selected, err := selectRuntimeTargets(all, config.RuntimeConfig{
		DeploymentMode: config.RuntimeDeploymentModeIndependent,
		ChainTargets:   []string{"ethereum-mainnet"},
	})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	assert.Equal(t, model.ChainEthereum, selected[0].chain)
	assert.Equal(t, model.NetworkMainnet, selected[0].network)
}

func TestSelectRuntimeTargets_LikeGroupMissingTargetsStableOrder(t *testing.T) {
	all := []runtimeTarget{
		{chain: model.ChainSolana, network: model.NetworkDevnet, group: config.RuntimeLikeGroupSolana},
		{chain: model.ChainBase, network: model.NetworkSepolia, group: config.RuntimeLikeGroupEVM},
		{chain: model.ChainBTC, network: model.NetworkTestnet, group: config.RuntimeLikeGroupBTC},
	}

	_, err := selectRuntimeTargets(all, config.RuntimeConfig{
		DeploymentMode: config.RuntimeDeploymentModeLikeGroup,
		ChainTargets:   []string{" dogecoin-mainnet ", "btc-mainnet", "base-sepolia"},
	})
	require.Error(t, err)
	assert.EqualError(t, err, "requested runtime chain targets not found: dogecoin-mainnet,btc-mainnet")
}

func TestSelectRuntimeTargets_FailsWhenUnknownTargetRequested(t *testing.T) {
	all := []runtimeTarget{
		{chain: model.ChainSolana, network: model.NetworkDevnet, group: config.RuntimeLikeGroupSolana},
		{chain: model.ChainBase, network: model.NetworkSepolia, group: config.RuntimeLikeGroupEVM},
		{chain: model.ChainBTC, network: model.NetworkTestnet, group: config.RuntimeLikeGroupBTC},
	}

	_, err := selectRuntimeTargets(all, config.RuntimeConfig{
		DeploymentMode: config.RuntimeDeploymentModeLikeGroup,
		ChainTargets:   []string{"dogecoin-mainnet"},
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

func TestMaskCredentials(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"with credentials", "redis://user:pass@host:6379", "redis://***@host:6379"},
		{"without credentials", "redis://host:6379", "redis://host:6379"},
		{"empty string", "", ""},
		{"complex password", "redis://admin:p%40ssw0rd@redis.example.com:6380/0", "redis://***@redis.example.com:6380/0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, maskCredentials(tt.input))
		})
	}
}

func TestBasicAuthMiddleware_RejectsWithoutCredentials(t *testing.T) {
	handler := basicAuthMiddleware("admin", "secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), `Basic realm="metrics"`)
}

func TestBasicAuthMiddleware_RejectsWrongCredentials(t *testing.T) {
	handler := basicAuthMiddleware("admin", "secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.SetBasicAuth("admin", "wrong")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestBasicAuthMiddleware_AcceptsValidCredentials(t *testing.T) {
	handler := basicAuthMiddleware("admin", "secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}

func TestHealthChecker_ReturnsErrorWhenDBNil(t *testing.T) {
	checker := &healthChecker{db: nil}
	err := checker.check(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database not initialized")
}

func TestHealthChecker_ReturnsReadyWhenDBAlive(t *testing.T) {
	// Use a sql.DB that we know will fail (no real DB), to test the error path
	db, err := sql.Open("postgres", "postgres://invalid:invalid@localhost:1/nonexistent?sslmode=disable&connect_timeout=1")
	require.NoError(t, err)
	defer db.Close()

	checker := &healthChecker{db: db}
	// This will fail because there's no real DB, but it tests the code path
	checkErr := checker.check(context.Background())
	assert.Error(t, checkErr)
	assert.Contains(t, checkErr.Error(), "database")
}
