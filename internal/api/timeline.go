package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// GET /api/v1/companies/{ticker}/timeline?limit=50
func (s *Server) handleCompanyTimeline(w http.ResponseWriter, r *http.Request) {
	ticker := chi.URLParam(r, "ticker")
	if !validTicker(ticker) {
		writeError(w, 400, "BAD_REQUEST", "invalid ticker format")
		return
	}

	company, err := s.store.GetCompanyByTicker(r.Context(), ticker)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "company not found")
			return
		}
		writeError(w, 500, "INTERNAL_ERROR", "failed to get company")
		return
	}

	limit, err := parseInt(r, "limit", 50)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}

	entries, err := s.store.GetCompanyTimeline(r.Context(), company.ID, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to get timeline")
		return
	}

	writeJSON(w, 200, ListResponse{Data: entries, Freshness: nil})
}
