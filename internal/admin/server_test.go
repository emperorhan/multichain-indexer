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

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
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
	getFunc             func(ctx context.Context, chain model.Chain, network model.Network) (*model.IndexerConfig, error)
	upsertFunc          func(ctx context.Context, c *model.IndexerConfig) error
	updateWatermarkFunc func(ctx context.Context, tx *sql.Tx, chain model.Chain, network model.Network, ingestedSequence int64) error
	getWatermarkFunc    func(ctx context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error)
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

func (m *mockIndexerConfigRepo) GetWatermark(ctx context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error) {
	return m.getWatermarkFunc(ctx, chain, network)
}

// --- Helper ---

func newTestServer(watchedRepo *mockWatchedAddressRepo, configRepo *mockIndexerConfigRepo) *Server {
	logger := slog.Default()
	return NewServer(watchedRepo, configRepo, logger)
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
