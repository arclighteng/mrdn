package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/go-chi/chi/v5"
	"database/sql"
)

// GET /api/v1/events?source=X&type=X&since=T&limit=N&offset=N
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	since, until, err := parseTimeRange(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	f := db.EventFilter{
		Source:    parseString(r, "source", ""),
		EventType: parseString(r, "type", ""),
		Since:     since,
		Until:     until,
		Limit:     limit,
		Offset:    offset,
	}

	events, err := s.store.ListEvents(r.Context(), f)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to list events")
		return
	}

	total, err := s.store.CountEvents(r.Context(), f)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to count events")
		return
	}

	writeJSON(w, 200, ListResponse{
		Data:       events,
		Pagination: &Pagination{Limit: limit, Offset: offset, Total: total},
		Freshness:  nil,
	})
}

// GET /api/v1/events/{id}
func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", "invalid event ID")
		return
	}

	event, err := s.store.GetEvent(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "event not found")
			return
		}
		writeError(w, 500, "INTERNAL_ERROR", "failed to get event")
		return
	}

	writeJSON(w, 200, DetailResponse{
		Data:      event,
		Freshness: nil,
	})
}

// GET /api/v1/events/latest?limit=N
func (s *Server) handleLatestEvents(w http.ResponseWriter, r *http.Request) {
	limit, err := parseInt(r, "limit", 20)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit > 100 {
		limit = 100
	}

	since, until, err := parseTimeRange(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	events, err := s.store.ListEvents(r.Context(), db.EventFilter{
		Limit: limit,
		Since: since,
		Until: until,
	})
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to list events")
		return
	}

	writeJSON(w, 200, ListResponse{
		Data:      events,
		Freshness: nil,
	})
}
