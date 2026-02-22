package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"strconv"
	"time"
)

//go:embed static/*
var embeddedStatic embed.FS

var staticFS, _ = fs.Sub(embeddedStatic, "static")

func (s *Server) handleDashboardIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		http.Error(w, "dashboard not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err = w.Write(data); err != nil {
		s.logger.Warn("failed to write dashboard response", "error", err)
	}
}

// --- Dashboard API handlers ---

type dashboardOverviewPipeline struct {
	Chain            string    `json:"chain"`
	Network          string    `json:"network"`
	HeadSequence     int64     `json:"head_sequence"`
	IngestedSequence int64     `json:"ingested_sequence"`
	Lag              int64     `json:"lag"`
	LastHeartbeatAt  time.Time `json:"last_heartbeat_at"`
}

type dashboardOverviewResponse struct {
	Pipelines             []dashboardOverviewPipeline `json:"pipelines"`
	TotalWatchedAddresses int                         `json:"total_watched_addresses"`
	ServerTime            string                      `json:"server_time"`
}

func (s *Server) handleDashboardOverview(w http.ResponseWriter, r *http.Request) {
	if s.dashboardRepo == nil {
		http.Error(w, `{"error":"dashboard not available"}`, http.StatusServiceUnavailable)
		return
	}

	watermarks, err := s.dashboardRepo.GetAllWatermarks(r.Context())
	if err != nil {
		s.logger.Error("dashboard overview: get watermarks failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	totalAddr, err := s.dashboardRepo.CountWatchedAddresses(r.Context())
	if err != nil {
		s.logger.Error("dashboard overview: count addresses failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	pipelines := make([]dashboardOverviewPipeline, 0, len(watermarks))
	for _, wm := range watermarks {
		lag := wm.HeadSequence - wm.IngestedSequence
		if lag < 0 {
			lag = 0
		}
		pipelines = append(pipelines, dashboardOverviewPipeline{
			Chain:            string(wm.Chain),
			Network:          string(wm.Network),
			HeadSequence:     wm.HeadSequence,
			IngestedSequence: wm.IngestedSequence,
			Lag:              lag,
			LastHeartbeatAt:  wm.LastHeartbeatAt,
		})
	}

	writeJSON(w, http.StatusOK, dashboardOverviewResponse{
		Pipelines:             pipelines,
		TotalWatchedAddresses: totalAddr,
		ServerTime:            time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleDashboardBalances(w http.ResponseWriter, r *http.Request) {
	if s.dashboardRepo == nil {
		http.Error(w, `{"error":"dashboard not available"}`, http.StatusServiceUnavailable)
		return
	}

	chain, network, ok := requireChainNetworkQuery(w, r)
	if !ok {
		return
	}

	balances, err := s.dashboardRepo.GetBalanceSummary(r.Context(), chain, network)
	if err != nil {
		s.logger.Error("dashboard balances failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"addresses": balances})
}

func (s *Server) handleDashboardEvents(w http.ResponseWriter, r *http.Request) {
	if s.dashboardRepo == nil {
		http.Error(w, `{"error":"dashboard not available"}`, http.StatusServiceUnavailable)
		return
	}

	chain, network, ok := requireChainNetworkQuery(w, r)
	if !ok {
		return
	}

	address := r.URL.Query().Get("address")
	limit := 50
	offset := 0

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	events, total, err := s.dashboardRepo.GetRecentEvents(r.Context(), chain, network, address, limit, offset)
	if err != nil {
		s.logger.Error("dashboard events failed", "error", err)
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": events, "total": total})
}
