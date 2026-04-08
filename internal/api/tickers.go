package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// GET /api/v1/tickers/top
//
// Most-actively-traded tickers across Congress, ranked by distinct rep count.
func (s *Server) handleTopTickers(w http.ResponseWriter, r *http.Request) {
	limit, _, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.store.TopTickers(r.Context(), limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load top tickers: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}

// GET /api/v1/tickers/{symbol}
//
// Per-ticker drilldown: summary, every rep who touched it, recent event feed.
func (s *Server) handleTickerDetail(w http.ResponseWriter, r *http.Request) {
	sym := strings.ToUpper(chi.URLParam(r, "symbol"))
	if !validTicker(sym) {
		writeError(w, 400, "BAD_REQUEST", "invalid ticker symbol")
		return
	}
	d, err := s.store.GetTickerDetail(r.Context(), sym, 100)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load ticker: "+err.Error())
		return
	}
	if d.Summary.Trades == 0 {
		writeError(w, 404, "NOT_FOUND", "no congressional trades for this ticker")
		return
	}
	writeJSON(w, 200, DetailResponse{Data: d})
}

// GET /api/v1/persons/{slug}/co-traders
//
// Other reps whose trades cluster in time with this person's trades on the
// same ticker. Pure time proximity — no inference of coordination.
func (s *Server) handleCoTraders(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		writeError(w, 400, "BAD_REQUEST", "missing slug")
		return
	}
	window, err := parseInt(r, "window_days", 14)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	limit, _, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.store.CoTraders(r.Context(), slug, window, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load co-traders: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}

// GET /api/v1/signals/round-trips
//
// Fastest buy → sell turnarounds in the dataset. Pure data, no inference of intent.
func (s *Server) handleRoundTrips(w http.ResponseWriter, r *http.Request) {
	limit, _, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	maxHold, err := parseInt(r, "max_hold_days", 60)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	minAmt, err := parseInt(r, "min_amount", 15000)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	rows, err := s.store.RoundTrips(r.Context(), maxHold, minAmt, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load round trips: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}
