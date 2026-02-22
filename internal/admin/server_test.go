package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"errors"
	"time"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/replay"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

// --- Mock repositories ---

type mockWatchedAddressRepo struct {
	getActiveFunc     func(ctx context.Context, chain model.Chain, network model.Network) ([]model.WatchedAddress, error)
	upsertFunc        func(ctx context.Context, addr *model.WatchedAddress) error
	findByAddressFunc func(ctx context.Context, chain model.Chain, network model.Network, address string) (*model.WatchedAddress, error)
}

func (m *mockWatchedAddressRepo) GetActive(ctx context.Context, chain model.Chain, network model.Network) ([]model.WatchedAddress, error) {
	return m.getActiveFunc(ctx, chain, network)
}

func (m *mockWatchedAddressRepo) Upsert(ctx context.Context, addr *model.WatchedAddress) error {
	return m.upsertFunc(ctx, addr)
}

func (m *mockWatchedAddressRepo) FindByAddress(ctx context.Context, chain model.Chain, network model.Network, address string) (*model.WatchedAddress, error) {
	return m.findByAddressFunc(ctx, chain, network, address)
}

type mockIndexerConfigRepo struct {
	getFunc              func(ctx context.Context, chain model.Chain, network model.Network) (*model.IndexerConfig, error)
	upsertFunc           func(ctx context.Context, c *model.IndexerConfig) error
	updateWatermarkFunc  func(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error
	rewindWatermarkFunc  func(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error
	getWatermarkFunc     func(ctx context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error)
}

func (m *mockIndexerConfigRepo) Get(ctx context.Context, chain model.Chain, network model.Network) (*model.IndexerConfig, error) {
	return m.getFunc(ctx, chain, network)
}

func (m *mockIndexerConfigRepo) Upsert(ctx context.Context, c *model.IndexerConfig) error {
	return m.upsertFunc(ctx, c)
}

func (m *mockIndexerConfigRepo) UpdateWatermarkTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error {
	return m.updateWatermarkFunc(ctx, tx, chain, network, ingestedSequence)
}

func (m *mockIndexerConfigRepo) RewindWatermarkTx(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error {
	if m.rewindWatermarkFunc != nil {
		return m.rewindWatermarkFunc(ctx, tx, chain, network, ingestedSequence)
	}
	return nil
}

func (m *mockIndexerConfigRepo) GetWatermark(ctx context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error) {
	return m.getWatermarkFunc(ctx, chain, network)
}

// --- Mock replay requester ---

type mockReplayRequester struct {
	hasPipeline    bool
	requestResult  *replay.PurgeResult
	requestErr     error
	dryRunResult   *replay.PurgeResult
	dryRunErr      error
	watermark      *model.PipelineWatermark
	watermarkErr   error
}

func (m *mockReplayRequester) HasPipeline(_ model.Chain, _ model.Network) bool {
	return m.hasPipeline
}

func (m *mockReplayRequester) RequestReplay(_ context.Context, _ replay.PurgeRequest) (*replay.PurgeResult, error) {
	return m.requestResult, m.requestErr
}

func (m *mockReplayRequester) DryRunPurge(_ context.Context, _ replay.PurgeRequest) (*replay.PurgeResult, error) {
	return m.dryRunResult, m.dryRunErr
}

func (m *mockReplayRequester) GetWatermark(_ context.Context, _ model.Chain, _ model.Network) (*model.PipelineWatermark, error) {
	return m.watermark, m.watermarkErr
}

// --- Helper ---

func newTestServer(watchedRepo *mockWatchedAddressRepo, configRepo *mockIndexerConfigRepo, opts ...ServerOption) *Server {
	logger := slog.Default()
	return NewServer(watchedRepo, configRepo, logger, opts...)
}

// --- Tests: ListWatchedAddresses ---

func TestHandleListWatchedAddresses_Success(t *testing.T) {
	walletID := "wallet-123"
	watchedRepo := &mockWatchedAddressRepo{
		getActiveFunc: func(_ context.Context, chain model.Chain, network model.Network) ([]model.WatchedAddress, error) {
			return []model.WatchedAddress{
				{
					Chain:    chain,
					Network:  network,
					Address:  "addr1",
					IsActive: true,
					WalletID: &walletID,
				},
			}, nil
		},
	}
	srv := newTestServer(watchedRepo, &mockIndexerConfigRepo{})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/watched-addresses?chain=solana&network=devnet", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp []watchedAddressResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp) != 1 {
		t.Fatalf("expected 1 address, got %d", len(resp))
	}
	if resp[0].Address != "addr1" {
		t.Errorf("expected address 'addr1', got %q", resp[0].Address)
	}
	if resp[0].Chain != "solana" {
		t.Errorf("expected chain 'solana', got %q", resp[0].Chain)
	}
	if resp[0].Network != "devnet" {
		t.Errorf("expected network 'devnet', got %q", resp[0].Network)
	}
	if !resp[0].Active {
		t.Error("expected active to be true")
	}
	if resp[0].WalletID == nil || *resp[0].WalletID != "wallet-123" {
		t.Errorf("expected wallet_id 'wallet-123', got %v", resp[0].WalletID)
	}
}

func TestHandleListWatchedAddresses_MissingParams(t *testing.T) {
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{})

	tests := []struct {
		name string
		url  string
	}{
		{"missing both", "/admin/v1/watched-addresses"},
		{"missing network", "/admin/v1/watched-addresses?chain=solana"},
		{"missing chain", "/admin/v1/watched-addresses?network=devnet"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			rec := httptest.NewRecorder()

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d", rec.Code)
			}
		})
	}
}

func TestHandleListWatchedAddresses_InvalidChain(t *testing.T) {
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/watched-addresses?chain=invalid_chain&network=devnet", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}

// --- Tests: AddWatchedAddress ---

func TestHandleAddWatchedAddress_Success(t *testing.T) {
	var upsertedAddr *model.WatchedAddress
	watchedRepo := &mockWatchedAddressRepo{
		upsertFunc: func(_ context.Context, addr *model.WatchedAddress) error {
			upsertedAddr = addr
			return nil
		},
	}
	srv := newTestServer(watchedRepo, &mockIndexerConfigRepo{})

	body := `{"chain":"solana","network":"devnet","address":"SomeAddr123"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/watched-addresses", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp["success"] {
		t.Error("expected success: true in response")
	}

	if upsertedAddr == nil {
		t.Fatal("expected Upsert to be called")
	}
	if upsertedAddr.Chain != model.ChainSolana {
		t.Errorf("expected chain solana, got %q", upsertedAddr.Chain)
	}
	if upsertedAddr.Network != model.NetworkDevnet {
		t.Errorf("expected network devnet, got %q", upsertedAddr.Network)
	}
	if upsertedAddr.Address != "SomeAddr123" {
		t.Errorf("expected address 'SomeAddr123', got %q", upsertedAddr.Address)
	}
	if !upsertedAddr.IsActive {
		t.Error("expected is_active to be true")
	}
	if upsertedAddr.Source != model.AddressSourceAdmin {
		t.Errorf("expected source 'admin', got %q", upsertedAddr.Source)
	}
}

func TestHandleAddWatchedAddress_InvalidJSON(t *testing.T) {
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{})

	body := `{this is not valid json}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/watched-addresses", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}

func TestHandleAddWatchedAddress_MissingFields(t *testing.T) {
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{})

	tests := []struct {
		name string
		body string
	}{
		{"missing address", `{"chain":"solana","network":"devnet"}`},
		{"missing chain", `{"network":"devnet","address":"addr1"}`},
		{"missing network", `{"chain":"solana","address":"addr1"}`},
		{"all empty", `{}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/v1/watched-addresses", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleAddWatchedAddress_InvalidChainNetwork(t *testing.T) {
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{})

	tests := []struct {
		name string
		body string
	}{
		{"invalid chain", `{"chain":"dogecoin","network":"devnet","address":"addr1"}`},
		{"invalid network", `{"chain":"solana","network":"fakenet","address":"addr1"}`},
		{"both invalid", `{"chain":"dogecoin","network":"fakenet","address":"addr1"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/v1/watched-addresses", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// --- Tests: GetStatus ---

func TestHandleGetStatus_Success(t *testing.T) {
	configRepo := &mockIndexerConfigRepo{
		getWatermarkFunc: func(_ context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error) {
			return &model.PipelineWatermark{
				Chain:            chain,
				Network:          network,
				IngestedSequence: 42,
			}, nil
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, configRepo)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/status?chain=solana&network=devnet", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp chainStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Chain != "solana" {
		t.Errorf("expected chain 'solana', got %q", resp.Chain)
	}
	if resp.Network != "devnet" {
		t.Errorf("expected network 'devnet', got %q", resp.Network)
	}
	if resp.Watermark != 42 {
		t.Errorf("expected watermark 42, got %d", resp.Watermark)
	}
}

func TestHandleGetStatus_NoWatermark(t *testing.T) {
	configRepo := &mockIndexerConfigRepo{
		getWatermarkFunc: func(_ context.Context, _ model.Chain, _ model.Network) (*model.PipelineWatermark, error) {
			return nil, nil
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, configRepo)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/status?chain=solana&network=devnet", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp chainStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Watermark != 0 {
		t.Errorf("expected watermark 0 for nil watermark, got %d", resp.Watermark)
	}
	if resp.Chain != "solana" {
		t.Errorf("expected chain 'solana', got %q", resp.Chain)
	}
	if resp.Network != "devnet" {
		t.Errorf("expected network 'devnet', got %q", resp.Network)
	}
}

func TestHandleGetStatus_InvalidChain(t *testing.T) {
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/status?chain=invalid_chain&network=devnet", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}

// --- Tests: Replay ---

func TestHandleReplay_NoReplayRequester(t *testing.T) {
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{})

	body := `{"chain":"base","network":"mainnet","from_block":100}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandleReplay_Success(t *testing.T) {
	rr := &mockReplayRequester{
		hasPipeline: true,
		requestResult: &replay.PurgeResult{
			PurgedEvents:       450,
			PurgedTransactions: 120,
			PurgedBlocks:       50,
			ReversedBalances:   380,
			NewWatermark:       99,
			CursorsRewound:     3,
			DurationMs:         1234,
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithReplayRequester(rr))

	body := `{"chain":"base","network":"mainnet","from_block":100,"reason":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp replay.PurgeResult
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.PurgedEvents != 450 {
		t.Errorf("expected 450 purged events, got %d", resp.PurgedEvents)
	}
	if resp.NewWatermark != 99 {
		t.Errorf("expected watermark 99, got %d", resp.NewWatermark)
	}
}

func TestHandleReplay_DryRun(t *testing.T) {
	rr := &mockReplayRequester{
		hasPipeline: true,
		dryRunResult: &replay.PurgeResult{
			PurgedEvents:       10,
			PurgedTransactions: 5,
			DryRun:             true,
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithReplayRequester(rr))

	body := `{"chain":"base","network":"mainnet","from_block":100,"dry_run":true}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp replay.PurgeResult
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.DryRun {
		t.Error("expected dry_run=true in response")
	}
}

func TestHandleReplay_MissingFields(t *testing.T) {
	rr := &mockReplayRequester{hasPipeline: true}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithReplayRequester(rr))

	tests := []struct {
		name string
		body string
	}{
		{"missing chain", `{"network":"mainnet","from_block":100}`},
		{"missing network", `{"chain":"base","from_block":100}`},
		{"missing from_block", `{"chain":"base","network":"mainnet"}`},
		{"all empty", `{}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", bytes.NewBufferString(tc.body))
			rec := httptest.NewRecorder()

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleReplay_PipelineNotFound(t *testing.T) {
	rr := &mockReplayRequester{hasPipeline: false}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithReplayRequester(rr))

	body := `{"chain":"base","network":"mainnet","from_block":100}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleReplay_FinalizedConflict(t *testing.T) {
	rr := &mockReplayRequester{
		hasPipeline: true,
		requestErr:  replay.ErrFinalizedBlock,
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithReplayRequester(rr))

	body := `{"chain":"base","network":"mainnet","from_block":100}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleReplay_NegativeFromBlock(t *testing.T) {
	rr := &mockReplayRequester{hasPipeline: true}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithReplayRequester(rr))

	body := `{"chain":"base","network":"mainnet","from_block":-1}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleReplay_InvalidChainNetwork(t *testing.T) {
	rr := &mockReplayRequester{hasPipeline: true}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithReplayRequester(rr))

	body := `{"chain":"dogecoin","network":"mainnet","from_block":100}`
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/replay", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// --- Tests: Replay Status ---

func TestHandleReplayStatus_Success(t *testing.T) {
	rr := &mockReplayRequester{
		hasPipeline: true,
		watermark: &model.PipelineWatermark{
			HeadSequence:     8000,
			IngestedSequence: 5234,
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithReplayRequester(rr))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/replay/status?chain=base&network=mainnet", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp replayStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.CurrentWatermark != 5234 {
		t.Errorf("expected watermark 5234, got %d", resp.CurrentWatermark)
	}
	if resp.HeadSequence != 8000 {
		t.Errorf("expected head_sequence 8000, got %d", resp.HeadSequence)
	}
	if resp.Lag != 2766 {
		t.Errorf("expected lag 2766, got %d", resp.Lag)
	}
}

func TestHandleReplayStatus_NoRequester(t *testing.T) {
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/replay/status?chain=base&network=mainnet", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandleReplayStatus_PipelineNotFound(t *testing.T) {
	rr := &mockReplayRequester{hasPipeline: false}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithReplayRequester(rr))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/replay/status?chain=base&network=mainnet", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// --- Mock DashboardDataProvider ---

type mockDashboardRepo struct {
	getBalanceSummaryFunc     func(ctx context.Context, chain model.Chain, network model.Network) ([]store.DashboardAddressBalance, error)
	getRecentEventsFunc       func(ctx context.Context, chain model.Chain, network model.Network, address string, limit, offset int) ([]store.DashboardEvent, int, error)
	getAllWatermarksFunc       func(ctx context.Context) ([]model.PipelineWatermark, error)
	countWatchedAddressesFunc func(ctx context.Context) (int, error)
}

func (m *mockDashboardRepo) GetBalanceSummary(ctx context.Context, chain model.Chain, network model.Network) ([]store.DashboardAddressBalance, error) {
	return m.getBalanceSummaryFunc(ctx, chain, network)
}

func (m *mockDashboardRepo) GetRecentEvents(ctx context.Context, chain model.Chain, network model.Network, address string, limit, offset int) ([]store.DashboardEvent, int, error) {
	return m.getRecentEventsFunc(ctx, chain, network, address, limit, offset)
}

func (m *mockDashboardRepo) GetAllWatermarks(ctx context.Context) ([]model.PipelineWatermark, error) {
	return m.getAllWatermarksFunc(ctx)
}

func (m *mockDashboardRepo) CountWatchedAddresses(ctx context.Context) (int, error) {
	return m.countWatchedAddressesFunc(ctx)
}

// --- Tests: Dashboard Overview ---

func TestHandleDashboardOverview_Success(t *testing.T) {
	now := time.Now().UTC()
	dashRepo := &mockDashboardRepo{
		getAllWatermarksFunc: func(_ context.Context) ([]model.PipelineWatermark, error) {
			return []model.PipelineWatermark{
				{Chain: model.ChainSolana, Network: model.NetworkDevnet, HeadSequence: 1000, IngestedSequence: 950, LastHeartbeatAt: now},
				{Chain: model.ChainBase, Network: model.NetworkMainnet, HeadSequence: 5000, IngestedSequence: 4990, LastHeartbeatAt: now},
			}, nil
		},
		countWatchedAddressesFunc: func(_ context.Context) (int, error) {
			return 42, nil
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithDashboardRepo(dashRepo))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/dashboard/overview", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp dashboardOverviewResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Pipelines) != 2 {
		t.Fatalf("expected 2 pipelines, got %d", len(resp.Pipelines))
	}
	if resp.TotalWatchedAddresses != 42 {
		t.Errorf("expected 42 watched addresses, got %d", resp.TotalWatchedAddresses)
	}
	if resp.ServerTime == "" {
		t.Error("expected server_time to be non-empty")
	}
	// Verify lag calculation.
	if resp.Pipelines[0].Lag != 50 {
		t.Errorf("expected lag 50 for solana pipeline, got %d", resp.Pipelines[0].Lag)
	}
	if resp.Pipelines[1].Lag != 10 {
		t.Errorf("expected lag 10 for base pipeline, got %d", resp.Pipelines[1].Lag)
	}
}

func TestHandleDashboardOverview_RepoError(t *testing.T) {
	dashRepo := &mockDashboardRepo{
		getAllWatermarksFunc: func(_ context.Context) ([]model.PipelineWatermark, error) {
			return nil, errors.New("db connection failed")
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithDashboardRepo(dashRepo))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/dashboard/overview", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestHandleDashboardOverview_NoDashboardRepo(t *testing.T) {
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/dashboard/overview", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

// --- Tests: Dashboard Balances ---

func TestHandleDashboardBalances_Success(t *testing.T) {
	label := "my-wallet"
	dashRepo := &mockDashboardRepo{
		getBalanceSummaryFunc: func(_ context.Context, chain model.Chain, network model.Network) ([]store.DashboardAddressBalance, error) {
			return []store.DashboardAddressBalance{
				{
					Address: "addr1",
					Label:   &label,
					Balances: []store.DashboardTokenBalance{
						{TokenSymbol: "SOL", Amount: "5000000000", Decimals: 9},
					},
				},
			}, nil
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithDashboardRepo(dashRepo))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/dashboard/balances?chain=solana&network=devnet", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string][]store.DashboardAddressBalance
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	addrs := resp["addresses"]
	if len(addrs) != 1 {
		t.Fatalf("expected 1 address, got %d", len(addrs))
	}
	if addrs[0].Address != "addr1" {
		t.Errorf("expected address 'addr1', got %q", addrs[0].Address)
	}
	if len(addrs[0].Balances) != 1 || addrs[0].Balances[0].TokenSymbol != "SOL" {
		t.Error("expected SOL token balance")
	}
}

func TestHandleDashboardBalances_MissingParams(t *testing.T) {
	dashRepo := &mockDashboardRepo{}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithDashboardRepo(dashRepo))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/dashboard/balances?chain=solana", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// --- Tests: Dashboard Events ---

func TestHandleDashboardEvents_Success(t *testing.T) {
	now := time.Now().UTC()
	dashRepo := &mockDashboardRepo{
		getRecentEventsFunc: func(_ context.Context, _ model.Chain, _ model.Network, address string, limit, offset int) ([]store.DashboardEvent, int, error) {
			events := []store.DashboardEvent{
				{TxHash: "tx1", Address: "addr1", ActivityType: "deposit", Delta: "100000", BlockCursor: 200, BlockTime: &now, FinalityState: "confirmed"},
				{TxHash: "tx2", Address: "addr1", ActivityType: "withdrawal", Delta: "-50000", BlockCursor: 199, BlockTime: &now, FinalityState: "confirmed"},
			}
			return events, 2, nil
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithDashboardRepo(dashRepo))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/dashboard/events?chain=base&network=sepolia&limit=10&offset=0", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Events []store.DashboardEvent `json:"events"`
		Total  int                    `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(resp.Events))
	}
	if resp.Total != 2 {
		t.Errorf("expected total 2, got %d", resp.Total)
	}
	if resp.Events[0].TxHash != "tx1" {
		t.Errorf("expected first event tx_hash 'tx1', got %q", resp.Events[0].TxHash)
	}
}

func TestHandleDashboardEvents_WithAddressFilter(t *testing.T) {
	var capturedAddr string
	dashRepo := &mockDashboardRepo{
		getRecentEventsFunc: func(_ context.Context, _ model.Chain, _ model.Network, address string, limit, offset int) ([]store.DashboardEvent, int, error) {
			capturedAddr = address
			return []store.DashboardEvent{}, 0, nil
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithDashboardRepo(dashRepo))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/dashboard/events?chain=solana&network=devnet&address=myaddr", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if capturedAddr != "myaddr" {
		t.Errorf("expected address filter 'myaddr', got %q", capturedAddr)
	}
}

func TestHandleDashboardEvents_MissingParams(t *testing.T) {
	dashRepo := &mockDashboardRepo{}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithDashboardRepo(dashRepo))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/dashboard/events", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleDashboardEvents_RepoError(t *testing.T) {
	dashRepo := &mockDashboardRepo{
		getRecentEventsFunc: func(_ context.Context, _ model.Chain, _ model.Network, _ string, _, _ int) ([]store.DashboardEvent, int, error) {
			return nil, 0, errors.New("query timeout")
		},
	}
	srv := newTestServer(&mockWatchedAddressRepo{}, &mockIndexerConfigRepo{}, WithDashboardRepo(dashRepo))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/dashboard/events?chain=solana&network=devnet", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}
