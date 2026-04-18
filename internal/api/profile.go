package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"database/sql"
)

// GET /api/v1/persons/{slug}/profile
//
// Deep stat sheet for a single representative — counts, $ volume, latency,
// top tickers, solo plays, biggest trade, monthly timeline, swarm participation.
func (s *Server) handlePersonProfile(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !validSlug(slug) {
		writeError(w, 400, "BAD_REQUEST", "invalid slug format")
		return
	}

	prof, err := s.store.GetPersonProfile(r.Context(), slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "person not found")
			return
		}
		writeError(w, 500, "INTERNAL_ERROR", "failed to load profile")
		return
	}

	writeJSON(w, 200, DetailResponse{Data: prof})
}

// GET /api/v1/signals/first-movers
//
// For each ticker with ≥min_buyers congressional buyers, returns the first
// rep to buy and the next followers chronologically.
func (s *Server) handleFirstMovers(w http.ResponseWriter, r *http.Request) {
	limit, _, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	minBuyers, err := parseInt(r, "min_buyers", 3)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	rows, err := s.store.FirstMovers(r.Context(), minBuyers, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to compute first movers: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}
