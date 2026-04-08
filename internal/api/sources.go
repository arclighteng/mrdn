package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// GET /api/v1/sources
func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.store.ListSourceMeta(r.Context())
	if err != nil {
		log.Printf("handleListSources: %v", err)
		writeError(w, 500, "INTERNAL_ERROR", "failed to list sources")
		return
	}

	freshness := make([]Freshness, len(sources))
	for i, sm := range sources {
		freshness[i] = freshnessFromSource(sm)
	}

	writeJSON(w, 200, ListResponse{
		Data:      sources,
		Freshness: freshness,
	})
}

// GET /api/v1/sources/{name}
func (s *Server) handleGetSource(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	source, err := s.store.GetSourceMeta(r.Context(), name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "source not found")
			return
		}
		writeError(w, 500, "INTERNAL_ERROR", "failed to get source")
		return
	}

	writeJSON(w, 200, DetailResponse{
		Data:      source,
		Freshness: freshnessFromSource(source),
	})
}
