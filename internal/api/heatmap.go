package api

import "net/http"

// GET /api/v1/scores/heatmap
func (s *Server) handleScoreHeatmap(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.GetScoreHeatmap(r.Context())
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to get heatmap")
		return
	}
	writeJSON(w, 200, ListResponse{Data: entries, Freshness: nil})
}
