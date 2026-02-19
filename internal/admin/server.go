package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/emperorhan/multichain-indexer/internal/domain/model"
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

// Server provides an HTTP-based admin API for operational management.
type Server struct {
	watchedAddrRepo store.WatchedAddressRepository
	configRepo      store.IndexerConfigRepository
	logger          *slog.Logger
}

// NewServer creates a new admin API server.
func NewServer(
	watchedAddrRepo store.WatchedAddressRepository,
	configRepo store.IndexerConfigRepository,
	logger *slog.Logger,
) *Server {
	return &Server{
		watchedAddrRepo: watchedAddrRepo,
		configRepo:      configRepo,
		logger:          logger.With("component", "admin"),
	}
}

// Handler returns the HTTP handler for the admin API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/watched-addresses", s.handleListWatchedAddresses)
	mux.HandleFunc("POST /admin/v1/watched-addresses", s.handleAddWatchedAddress)
	mux.HandleFunc("GET /admin/v1/status", s.handleGetStatus)
	return mux
}

func validateChainNetwork(chain model.Chain, network model.Network) bool {
	return allowedChains[chain] && allowedNetworks[network]
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
	chain := model.Chain(r.URL.Query().Get("chain"))
	network := model.Network(r.URL.Query().Get("network"))

	if chain == "" || network == "" {
		http.Error(w, `{"error":"chain and network query params required"}`, http.StatusBadRequest)
		return
	}

	if !validateChainNetwork(chain, network) {
		http.Error(w, `{"error":"invalid chain or network value"}`, http.StatusBadRequest)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type addWatchedAddressRequest struct {
	Chain   string `json:"chain"`
	Network string `json:"network"`
	Address string `json:"address"`
}

func (s *Server) handleAddWatchedAddress(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	var req addWatchedAddressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

type chainStatusResponse struct {
	Chain     string `json:"chain"`
	Network   string `json:"network"`
	Watermark int64  `json:"watermark"`
}

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	chain := model.Chain(r.URL.Query().Get("chain"))
	network := model.Network(r.URL.Query().Get("network"))

	if chain == "" || network == "" {
		http.Error(w, `{"error":"chain and network query params required"}`, http.StatusBadRequest)
		return
	}

	if !validateChainNetwork(chain, network) {
		http.Error(w, `{"error":"invalid chain or network value"}`, http.StatusBadRequest)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
