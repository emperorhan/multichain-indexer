package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
	"github.com/emperorhan/multichain-indexer/internal/pipeline/replay"
	"github.com/emperorhan/multichain-indexer/internal/store"
)

const maxRequestBodyBytes = 1 << 20 // 1 MB

// allowedChains defines the valid chain values for admin API input validation.
var allowedChains = map[model.Chain]bool{
	model.ChainSolana:   true,
	model.ChainEthereum: true,
	model.ChainBase:     true,
	model.ChainBTC:      true,
	model.ChainPolygon:  true,
	model.ChainArbitrum: true,
	model.ChainBSC:      true,
}

// allowedNetworks defines the valid network values for admin API input validation.
var allowedNetworks = map[model.Network]bool{
	model.NetworkMainnet: true,
	model.NetworkDevnet:  true,
	model.NetworkTestnet: true,
	model.NetworkSepolia: true,
	model.NetworkAmoy:    true,
}

// ReplayRequester is the interface that the admin server uses to interact
// with pipeline instances for replay operations. In production this is
// satisfied by *pipeline.Registry, but tests can provide a simple mock.
type ReplayRequester interface {
	RequestReplay(ctx context.Context, req replay.PurgeRequest) (*replay.PurgeResult, error)
	DryRunPurge(ctx context.Context, req replay.PurgeRequest) (*replay.PurgeResult, error)
	GetWatermark(ctx context.Context, chain model.Chain, network model.Network) (*model.PipelineWatermark, error)
	HasPipeline(chain model.Chain, network model.Network) bool
}

// HealthProvider returns per-pipeline health snapshots as JSON-encodable data.
type HealthProvider interface {
	HealthSnapshots() any
}

// ReconcileRequester triggers balance reconciliation.
type ReconcileRequester interface {
	ReconcileAny(ctx context.Context, chain model.Chain, network model.Network) (any, error)
	HasAdapter(chain model.Chain, network model.Network) bool
}

// DashboardDataProvider provides read-only data for the admin dashboard UI.
type DashboardDataProvider interface {
	GetBalanceSummary(ctx context.Context, chain model.Chain, network model.Network) ([]store.DashboardAddressBalance, error)
	GetRecentEvents(ctx context.Context, chain model.Chain, network model.Network, address string, limit, offset int) ([]store.DashboardEvent, int, error)
	GetAllWatermarks(ctx context.Context) ([]model.PipelineWatermark, error)
	CountWatchedAddresses(ctx context.Context) (int, error)
}

// Server provides an HTTP-based admin API for operational management.
type Server struct {
	watchedAddrRepo store.WatchedAddressRepository
	configRepo      store.IndexerConfigRepository
	replayReq       ReplayRequester
	healthProvider  HealthProvider
	reconcileReq    ReconcileRequester
	addressBookRepo AddressBookRepo
	dashboardRepo   DashboardDataProvider
	logger          *slog.Logger
}

// NewServer creates a new admin API server. replayReq may be nil if replay
// functionality is not needed.
func NewServer(
	watchedAddrRepo store.WatchedAddressRepository,
	configRepo store.IndexerConfigRepository,
	logger *slog.Logger,
	opts ...ServerOption,
) *Server {
	s := &Server{
		watchedAddrRepo: watchedAddrRepo,
		configRepo:      configRepo,
		logger:          logger.With("component", "admin"),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ServerOption configures optional dependencies for the admin server.
type ServerOption func(*Server)

// WithReplayRequester sets the replay requester on the admin server.
func WithReplayRequester(rr ReplayRequester) ServerOption {
	return func(s *Server) { s.replayReq = rr }
}

// WithHealthProvider sets the health provider on the admin server.
func WithHealthProvider(hp HealthProvider) ServerOption {
	return func(s *Server) { s.healthProvider = hp }
}

// WithReconcileRequester sets the reconciliation requester on the admin server.
func WithReconcileRequester(rr ReconcileRequester) ServerOption {
	return func(s *Server) { s.reconcileReq = rr }
}

// WithAddressBookRepo sets the address book repository on the admin server.
func WithAddressBookRepo(repo AddressBookRepo) ServerOption {
	return func(s *Server) { s.addressBookRepo = repo }
}

// WithDashboardRepo sets the dashboard data provider on the admin server.
func WithDashboardRepo(repo DashboardDataProvider) ServerOption {
	return func(s *Server) { s.dashboardRepo = repo }
}

// Handler returns the HTTP handler for the admin API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/watched-addresses", s.handleListWatchedAddresses)
	mux.HandleFunc("POST /admin/v1/watched-addresses", s.handleAddWatchedAddress)
	mux.HandleFunc("GET /admin/v1/status", s.handleGetStatus)
	mux.HandleFunc("POST /admin/v1/replay", s.handleReplay)
	mux.HandleFunc("GET /admin/v1/replay/status", s.handleReplayStatus)
	mux.HandleFunc("GET /admin/v1/health", s.handleHealth)
	mux.HandleFunc("POST /admin/v1/reconcile", s.handleReconcile)
	mux.HandleFunc("GET /admin/v1/address-books", s.handleListAddressBooks)
	mux.HandleFunc("POST /admin/v1/address-books", s.handleAddAddressBook)
	mux.HandleFunc("DELETE /admin/v1/address-books", s.handleDeleteAddressBook)

	// Dashboard API + static files
	mux.HandleFunc("GET /admin/v1/dashboard/overview", s.handleDashboardOverview)
	mux.HandleFunc("GET /admin/v1/dashboard/balances", s.handleDashboardBalances)
	mux.HandleFunc("GET /admin/v1/dashboard/events", s.handleDashboardEvents)
	mux.Handle("/dashboard/", http.StripPrefix("/dashboard/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/dashboard", s.handleDashboardIndex)

	return mux
}

func validateChainNetwork(chain model.Chain, network model.Network) bool {
	return allowedChains[chain] && allowedNetworks[network]
}

// writeJSON writes v as JSON with the given HTTP status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// requireChainNetworkQuery extracts and validates chain/network from query params.
// Returns false (and writes an error response) if validation fails.
func requireChainNetworkQuery(w http.ResponseWriter, r *http.Request) (model.Chain, model.Network, bool) {
	chain := model.Chain(r.URL.Query().Get("chain"))
	network := model.Network(r.URL.Query().Get("network"))
	if chain == "" || network == "" {
		http.Error(w, `{"error":"chain and network query params required"}`, http.StatusBadRequest)
		return "", "", false
	}
	if !validateChainNetwork(chain, network) {
		http.Error(w, `{"error":"invalid chain or network value"}`, http.StatusBadRequest)
		return "", "", false
	}
	return chain, network, true
}

// decodeJSONBody reads and decodes a JSON request body into v.
// Returns false (and writes an error response) if decoding fails.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return false
	}
	return true
}

type watchedAddressResponse struct {
	Address        string  `json:"address"`
	Chain          string  `json:"chain"`
	Network        string  `json:"network"`
	Active         bool    `json:"active"`
	WalletID       *string `json:"wallet_id,omitempty"`
	OrganizationID *string `json:"organization_id,omitempty"`
}

func (s *Server) handleListWatchedAddresses(w http.ResponseWriter, r *http.Request) {
	chain, network, ok := requireChainNetworkQuery(w, r)
	if !ok {
		return
	}

	addresses, err := s.watchedAddrRepo.GetActive(r.Context(), chain, network)
	if err != nil {
		s.logger.Error("list watched addresses failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	resp := make([]watchedAddressResponse, len(addresses))
	for i, addr := range addresses {
		resp[i] = watchedAddressResponse{
			Address:        addr.Address,
			Chain:          string(addr.Chain),
			Network:        string(addr.Network),
			Active:         addr.IsActive,
			WalletID:       addr.WalletID,
			OrganizationID: addr.OrganizationID,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

type addWatchedAddressRequest struct {
	Chain   string `json:"chain"`
	Network string `json:"network"`
	Address string `json:"address"`
}

func (s *Server) handleAddWatchedAddress(w http.ResponseWriter, r *http.Request) {
	var req addWatchedAddressRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if req.Chain == "" || req.Network == "" || req.Address == "" {
		http.Error(w, `{"error":"chain, network, and address are required"}`, http.StatusBadRequest)
		return
	}

	chain := model.Chain(req.Chain)
	network := model.Network(req.Network)

	if !validateChainNetwork(chain, network) {
		http.Error(w, `{"error":"invalid chain or network value"}`, http.StatusBadRequest)
		return
	}

	addr := &model.WatchedAddress{
		Chain:    chain,
		Network:  network,
		Address:  req.Address,
		IsActive: true,
		Source:   model.AddressSourceAdmin,
	}

	if err := s.watchedAddrRepo.Upsert(r.Context(), addr); err != nil {
		s.logger.Error("add watched address failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	s.logger.Info("watched address added via admin API",
		"chain", req.Chain,
		"network", req.Network,
		"address", req.Address,
	)

	writeJSON(w, http.StatusCreated, map[string]bool{"success": true})
}

type chainStatusResponse struct {
	Chain     string `json:"chain"`
	Network   string `json:"network"`
	Watermark int64  `json:"watermark"`
}

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	chain, network, ok := requireChainNetworkQuery(w, r)
	if !ok {
		return
	}

	watermark, err := s.configRepo.GetWatermark(r.Context(), chain, network)
	if err != nil {
		s.logger.Error("get status failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	resp := chainStatusResponse{
		Chain:   string(chain),
		Network: string(network),
	}
	if watermark != nil {
		resp.Watermark = watermark.IngestedSequence
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Replay endpoints ---

type replayRequest struct {
	Chain        string `json:"chain"`
	Network      string `json:"network"`
	FromBlock    *int64 `json:"from_block"`
	DryRun       bool   `json:"dry_run"`
	Force        bool   `json:"force"`
	ConfirmForce bool   `json:"confirm_force"`
	Reason       string `json:"reason"`
}

func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	if s.replayReq == nil {
		http.Error(w, `{"error":"replay not available"}`, http.StatusServiceUnavailable)
		return
	}

	var req replayRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if req.Chain == "" || req.Network == "" || req.FromBlock == nil {
		http.Error(w, `{"error":"chain, network, and from_block are required"}`, http.StatusBadRequest)
		return
	}

	if *req.FromBlock < 0 {
		http.Error(w, `{"error":"from_block must be >= 0"}`, http.StatusBadRequest)
		return
	}

	chain := model.Chain(req.Chain)
	network := model.Network(req.Network)

	if !validateChainNetwork(chain, network) {
		http.Error(w, `{"error":"invalid chain or network value"}`, http.StatusBadRequest)
		return
	}

	if !s.replayReq.HasPipeline(chain, network) {
		http.Error(w, `{"error":"pipeline not found for chain/network"}`, http.StatusNotFound)
		return
	}

	// Safety check: force + non-dry-run requires explicit confirm_force
	if req.Force && !req.DryRun && !req.ConfirmForce {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"force=true with dry_run=false requires confirm_force=true","warning":"This will permanently delete data from block %d"}`, *req.FromBlock)
		return
	}

	purgeReq := replay.PurgeRequest{
		Chain:     chain,
		Network:   network,
		FromBlock: *req.FromBlock,
		DryRun:    req.DryRun,
		Force:     req.Force,
		Reason:    req.Reason,
	}

	var result *replay.PurgeResult
	var err error

	if req.DryRun {
		result, err = s.replayReq.DryRunPurge(r.Context(), purgeReq)
	} else {
		result, err = s.replayReq.RequestReplay(r.Context(), purgeReq)
	}

	if err != nil {
		if errors.Is(err, replay.ErrFinalizedBlock) {
			http.Error(w, `{"error":"target block is finalized; set force=true to override"}`, http.StatusConflict)
			return
		}
		s.logger.Error("replay failed", "error", err, "chain", req.Chain, "network", req.Network)
		http.Error(w, `{"error":"replay operation failed"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

type replayStatusResponse struct {
	Chain            string `json:"chain"`
	Network          string `json:"network"`
	CurrentWatermark int64  `json:"current_watermark"`
	HeadSequence     int64  `json:"head_sequence"`
	Lag              int64  `json:"lag"`
}

func (s *Server) handleReplayStatus(w http.ResponseWriter, r *http.Request) {
	if s.replayReq == nil {
		http.Error(w, `{"error":"replay not available"}`, http.StatusServiceUnavailable)
		return
	}

	chain, network, ok := requireChainNetworkQuery(w, r)
	if !ok {
		return
	}

	if !s.replayReq.HasPipeline(chain, network) {
		http.Error(w, `{"error":"pipeline not found for chain/network"}`, http.StatusNotFound)
		return
	}

	watermark, err := s.replayReq.GetWatermark(r.Context(), chain, network)
	if err != nil {
		s.logger.Error("replay status failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	resp := replayStatusResponse{
		Chain:   string(chain),
		Network: string(network),
	}
	if watermark != nil {
		resp.CurrentWatermark = watermark.IngestedSequence
		resp.HeadSequence = watermark.HeadSequence
		resp.Lag = watermark.HeadSequence - watermark.IngestedSequence
		if resp.Lag < 0 {
			resp.Lag = 0
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Health endpoint ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.healthProvider == nil {
		http.Error(w, `{"error":"health provider not available"}`, http.StatusServiceUnavailable)
		return
	}

	writeJSON(w, http.StatusOK, s.healthProvider.HealthSnapshots())
}

// --- Reconciliation endpoint ---

type reconcileRequest struct {
	Chain   string `json:"chain"`
	Network string `json:"network"`
}

func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	if s.reconcileReq == nil {
		http.Error(w, `{"error":"reconciliation not available"}`, http.StatusServiceUnavailable)
		return
	}

	var req reconcileRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if req.Chain == "" || req.Network == "" {
		http.Error(w, `{"error":"chain and network are required"}`, http.StatusBadRequest)
		return
	}

	chain := model.Chain(req.Chain)
	network := model.Network(req.Network)

	if !validateChainNetwork(chain, network) {
		http.Error(w, `{"error":"invalid chain or network value"}`, http.StatusBadRequest)
		return
	}

	if !s.reconcileReq.HasAdapter(chain, network) {
		http.Error(w, `{"error":"reconciliation not supported for this chain/network"}`, http.StatusNotFound)
		return
	}

	result, err := s.reconcileReq.ReconcileAny(r.Context(), chain, network)
	if err != nil {
		s.logger.Error("reconciliation failed", "error", err, "chain", req.Chain, "network", req.Network)
		http.Error(w, `{"error":"reconciliation failed"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// --- Address Book endpoints ---

// AddressBookProvider manages address book CRUD operations.
type AddressBookProvider interface {
	ListAddressBooks(ctx context.Context, chain model.Chain, network model.Network) (any, error)
	AddAddressBook(ctx context.Context, entry any) error
	DeleteAddressBook(ctx context.Context, chain model.Chain, network model.Network, address string) error
}

type addressBookRequest struct {
	Chain   string `json:"chain"`
	Network string `json:"network"`
	Address string `json:"address"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	OrgID   string `json:"org_id"`
}

func (s *Server) handleListAddressBooks(w http.ResponseWriter, r *http.Request) {
	if s.addressBookRepo == nil {
		http.Error(w, `{"error":"address book not available"}`, http.StatusServiceUnavailable)
		return
	}

	chain, network, ok := requireChainNetworkQuery(w, r)
	if !ok {
		return
	}

	books, err := s.addressBookRepo.List(r.Context(), chain, network)
	if err != nil {
		s.logger.Error("list address books failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, books)
}

func (s *Server) handleAddAddressBook(w http.ResponseWriter, r *http.Request) {
	if s.addressBookRepo == nil {
		http.Error(w, `{"error":"address book not available"}`, http.StatusServiceUnavailable)
		return
	}

	var req addressBookRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if req.Chain == "" || req.Network == "" || req.Address == "" || req.Name == "" {
		http.Error(w, `{"error":"chain, network, address, and name are required"}`, http.StatusBadRequest)
		return
	}

	chain := model.Chain(req.Chain)
	network := model.Network(req.Network)

	if !validateChainNetwork(chain, network) {
		http.Error(w, `{"error":"invalid chain or network value"}`, http.StatusBadRequest)
		return
	}

	entry := &AddressBookEntry{
		Chain:   chain,
		Network: network,
		Address: req.Address,
		Name:    req.Name,
		Status:  req.Status,
		OrgID:   req.OrgID,
	}

	if err := s.addressBookRepo.Upsert(r.Context(), entry); err != nil {
		s.logger.Error("add address book failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	s.logger.Info("address book entry added", "chain", req.Chain, "network", req.Network, "address", req.Address, "name", req.Name)

	writeJSON(w, http.StatusCreated, map[string]bool{"success": true})
}

func (s *Server) handleDeleteAddressBook(w http.ResponseWriter, r *http.Request) {
	if s.addressBookRepo == nil {
		http.Error(w, `{"error":"address book not available"}`, http.StatusServiceUnavailable)
		return
	}

	chain, network, ok := requireChainNetworkQuery(w, r)
	if !ok {
		return
	}

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, `{"error":"chain, network, and address query params required"}`, http.StatusBadRequest)
		return
	}

	if err := s.addressBookRepo.Delete(r.Context(), chain, network, address); err != nil {
		s.logger.Error("delete address book failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// AddressBookEntry is used as the request model for address book operations.
type AddressBookEntry struct {
	Chain   model.Chain   `json:"chain"`
	Network model.Network `json:"network"`
	Address string        `json:"address"`
	Name    string        `json:"name"`
	Status  string        `json:"status"`
	OrgID   string        `json:"org_id"`
}

// AddressBookRepo is the interface for address book storage.
type AddressBookRepo interface {
	List(ctx context.Context, chain model.Chain, network model.Network) ([]AddressBookEntry, error)
	Upsert(ctx context.Context, entry *AddressBookEntry) error
	Delete(ctx context.Context, chain model.Chain, network model.Network, address string) error
	FindByAddress(ctx context.Context, chain model.Chain, network model.Network, address string) (*AddressBookEntry, error)
}
