package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"database/sql"
)

// GET /api/v1/connections/company/{ticker}?depth=2&limit=200
func (s *Server) handleConnectionsByCompany(w http.ResponseWriter, r *http.Request) {
	ticker := chi.URLParam(r, "ticker")
	if !validTicker(ticker) {
		writeError(w, 400, "BAD_REQUEST", "invalid ticker format")
		return
	}

	company, err := s.store.GetCompanyByTicker(r.Context(), ticker)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "company not found")
			return
		}
		writeError(w, 500, "INTERNAL_ERROR", "failed to get company")
		return
	}

	depth, err := parseInt(r, "depth", 2)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 4 {
		depth = 4
	}

	limit, err := parseInt(r, "limit", 200)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}

	graph, err := s.store.BFSGraph(r.Context(), company.ID, "company", depth, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to build connection graph")
		return
	}

	writeJSON(w, 200, DetailResponse{Data: graph, Freshness: nil})
}

// GET /api/v1/connections/person/{slug}?depth=2&limit=200
func (s *Server) handleConnectionsByPerson(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !validSlug(slug) {
		writeError(w, 400, "BAD_REQUEST", "invalid slug format")
		return
	}

	person, err := s.store.GetPersonBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "person not found")
			return
		}
		writeError(w, 500, "INTERNAL_ERROR", "failed to get person")
		return
	}

	depth, err := parseInt(r, "depth", 2)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 4 {
		depth = 4
	}

	limit, err := parseInt(r, "limit", 200)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}

	graph, err := s.store.BFSGraph(r.Context(), person.ID, "person", depth, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to build connection graph")
		return
	}

	writeJSON(w, 200, DetailResponse{Data: graph, Freshness: nil})
}
