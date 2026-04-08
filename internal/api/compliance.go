package api

import (
	"net/http"
)

// GET /api/v1/compliance/latency
//
// Returns the STOCK Act disclosure-latency leaderboard plus a dataset-wide
// summary, both computed directly from congressional_trades. No fabrication,
// no inference — just (filed_at - traded_at) per real disclosure.
func (s *Server) handleComplianceLatency(w http.ResponseWriter, r *http.Request) {
	limit, _, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	minTrades, err := parseInt(r, "min_trades", 5)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	rows, err := s.store.LatencyLeaderboard(r.Context(), minTrades, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to compute latency leaderboard")
		return
	}
	summary, err := s.store.LatencySummaryAll(r.Context())
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to compute latency summary")
		return
	}

	writeJSON(w, 200, map[string]any{
		"data":    rows,
		"summary": summary,
	})
}

// GET /api/v1/signals/swarms
//
// Weekly clusters of same-ticker congressional trading. Returns every
// (ticker, week) bucket with at least min_reps distinct representatives.
func (s *Server) handleSwarms(w http.ResponseWriter, r *http.Request) {
	limit, _, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	minReps, err := parseInt(r, "min_reps", 4)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	rows, err := s.store.SwarmDetector(r.Context(), minReps, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to compute swarms")
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}

// GET /api/v1/signals/partisan?mode=consensus|contrarian|all
//
// Tickers grouped by how the two parties trade them. Surfaces:
//   - consensus: both R and D piling into the same side
//   - contrarian: R buying while D selling (or vice versa)
func (s *Server) handlePartisan(w http.ResponseWriter, r *http.Request) {
	limit, _, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	minReps, err := parseInt(r, "min_reps", 4)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	mode := parseString(r, "mode", "consensus")
	if mode != "consensus" && mode != "contrarian" && mode != "all" {
		writeError(w, 400, "BAD_REQUEST", "mode must be consensus, contrarian, or all")
		return
	}

	rows, err := s.store.PartisanTickers(r.Context(), mode, minReps, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to compute partisan tickers")
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "mode": mode})
}
