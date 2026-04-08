package api

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,80}$`)

func validSlug(slug string) bool {
	return slugRe.MatchString(slug)
}

// GET /api/v1/persons?tier=N&branch=X&role=X&state=X&party=X&limit=N&offset=N
func (s *Server) handleListPersons(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	var tierFilter *int
	if r.URL.Query().Has("tier") {
		v, err := parseInt(r, "tier", 0)
		if err != nil {
			writeError(w, 400, "BAD_REQUEST", err.Error())
			return
		}
		tierFilter = &v
	}

	f := db.PersonFilter{
		Tier:   tierFilter,
		Branch: parseString(r, "branch", ""),
		Role:   parseString(r, "role", ""),
		State:  parseString(r, "state", ""),
		Party:  parseString(r, "party", ""),
		Sort:   parseString(r, "sort", "influence"),
		Limit:  limit,
		Offset: offset,
	}

	persons, err := s.store.ListPersons(r.Context(), f)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to list persons")
		return
	}

	total, err := s.store.CountPersons(r.Context(), f)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to count persons")
		return
	}

	// Best-effort freshness from the EFDS Senate disclosure source.
	var freshness any
	if sm, err := s.store.GetSourceMeta(r.Context(), "efds_senate"); err == nil {
		f := freshnessFromSource(sm)
		freshness = f
	}

	writeJSON(w, 200, ListResponse{
		Data:       persons,
		Pagination: &Pagination{Limit: limit, Offset: offset, Total: total},
		Freshness:  freshness,
	})
}

// GET /api/v1/persons/{slug}
func (s *Server) handleGetPerson(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !validSlug(slug) {
		writeError(w, 400, "BAD_REQUEST", "invalid slug format")
		return
	}

	person, err := s.store.GetPersonBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "person not found")
			return
		}
		writeError(w, 500, "INTERNAL_ERROR", "failed to get person")
		return
	}

	writeJSON(w, 200, DetailResponse{
		Data:      person,
		Freshness: nil,
	})
}
