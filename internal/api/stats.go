package api

import "net/http"

// GET /api/v1/stats/activity
func (s *Server) handleActivityStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetActivityStats(r.Context())
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load activity stats")
		return
	}
	writeJSON(w, 200, DetailResponse{Data: stats})
}

// GET /api/v1/stats/activity/heatmap?days=365
func (s *Server) handleActivityHeatmap(w http.ResponseWriter, r *http.Request) {
	days, err := parseInt(r, "days", 365)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	cells, err := s.store.GetActivityHeatmap(r.Context(), days)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load activity heatmap: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": cells, "days": days})
}

// GET /api/v1/stats/rep-ticker-heatmap/drill?slug=pelosi-nancy&ticker=NVDA&limit=200
func (s *Server) handleRepTickerDrill(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	ticker := r.URL.Query().Get("ticker")
	if slug == "" || ticker == "" {
		writeError(w, 400, "BAD_REQUEST", "slug and ticker are required")
		return
	}
	limit, _ := parseInt(r, "limit", 200)
	rows, err := s.store.TradesByPersonTicker(r.Context(), slug, ticker, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load trades: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "slug": slug, "ticker": ticker})
}

// GET /api/v1/stats/rep-ticker-heatmap?limit=25
func (s *Server) handleRepTickerHeatmap(w http.ResponseWriter, r *http.Request) {
	limit, err := parseInt(r, "limit", 25)
	if err != nil || limit <= 0 || limit > 100 {
		limit = 25
	}
	cells, err := s.store.GetRepTickerHeatmap(r.Context(), limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load rep-ticker heatmap: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": cells, "limit": limit})
}

// GET /api/v1/stats/party-sector-heatmap
func (s *Server) handlePartySectorHeatmap(w http.ResponseWriter, r *http.Request) {
	cells, err := s.store.GetPartySectorHeatmap(r.Context())
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load party-sector heatmap: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": cells})
}

// GET /api/v1/stats/party-sector-heatmap/drill?party=D&sector=Health%20Care&limit=200
func (s *Server) handlePartySectorDrill(w http.ResponseWriter, r *http.Request) {
	party := r.URL.Query().Get("party")
	sector := r.URL.Query().Get("sector")
	if party == "" || sector == "" {
		writeError(w, 400, "BAD_REQUEST", "party and sector are required")
		return
	}
	limit, _ := parseInt(r, "limit", 200)
	rows, err := s.store.TradesByPartySector(r.Context(), party, sector, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load trades: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "party": party, "sector": sector})
}

// GET /api/v1/stats/rep-month-heatmap?limit=15
func (s *Server) handleRepMonthHeatmap(w http.ResponseWriter, r *http.Request) {
	limit, err := parseInt(r, "limit", 15)
	if err != nil || limit <= 0 || limit > 50 {
		limit = 15
	}
	cells, err := s.store.GetRepMonthHeatmap(r.Context(), limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load rep-month heatmap: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": cells, "limit": limit})
}

// GET /api/v1/stats/rep-month-heatmap/drill?slug=pelosi-nancy&month=2026-03&limit=200
func (s *Server) handleRepMonthDrill(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	month := r.URL.Query().Get("month")
	if slug == "" || month == "" {
		writeError(w, 400, "BAD_REQUEST", "slug and month are required")
		return
	}
	limit, _ := parseInt(r, "limit", 200)
	rows, err := s.store.TradesByPersonMonth(r.Context(), slug, month, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load trades: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "slug": slug, "month": month})
}

// GET /api/v1/stats/activity/heatmap/drill?dow=1&month=3&days=3650&limit=100
func (s *Server) handleActivityHeatmapDrill(w http.ResponseWriter, r *http.Request) {
	dow, err := parseInt(r, "dow", -1)
	if err != nil || dow < 0 || dow > 6 {
		writeError(w, 400, "BAD_REQUEST", "dow must be 0..6")
		return
	}
	month, err := parseInt(r, "month", -1)
	if err != nil || month < 1 || month > 12 {
		writeError(w, 400, "BAD_REQUEST", "month must be 1..12")
		return
	}
	days, _ := parseInt(r, "days", 3650)
	limit, _, _ := parsePagination(r)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.store.TradesByDowMonth(r.Context(), dow, month, days, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load drill: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "dow": dow, "month": month})
}
