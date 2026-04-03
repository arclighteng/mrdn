package api

import "net/http"

// GET /api/v1/scores/rankings?limit=N
func (s *Server) handleScoreRankings(w http.ResponseWriter, r *http.Request) {
	limit, err := parseInt(r, "limit", 100)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit > 500 {
		limit = 500
	}

	rankings, err := s.store.GetScoreRankings(r.Context(), limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to get rankings")
		return
	}

	writeJSON(w, 200, ListResponse{
		Data:      rankings,
		Freshness: nil,
	})
}

// GET /api/v1/scores/movers?hours=N&limit=N
func (s *Server) handleScoreMovers(w http.ResponseWriter, r *http.Request) {
	hours, err := parseInt(r, "hours", 24)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if hours > 168 {
		hours = 168
	}

	limit, err := parseInt(r, "limit", 20)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}
	if limit > 100 {
		limit = 100
	}

	movers, err := s.store.GetScoreMovers(r.Context(), hours, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to get movers")
		return
	}

	writeJSON(w, 200, ListResponse{
		Data:      movers,
		Freshness: nil,
	})
}
