package api

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

var tickerRe = regexp.MustCompile(`^[A-Z]{1,5}$`)

func validTicker(ticker string) bool {
	return tickerRe.MatchString(ticker)
}

// GET /api/v1/companies?sector=X&ticker=X&min_score=N&max_score=N&limit=N&offset=N
func (s *Server) handleListCompanies(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	sector := parseString(r, "sector", "")
	ticker := parseString(r, "ticker", "")

	var minComposite, maxComposite *float64
	if r.URL.Query().Has("min_score") {
		v, err := parseFloat(r, "min_score", 0)
		if err != nil {
			writeError(w, 400, "BAD_REQUEST", err.Error())
			return
		}
		minComposite = &v
	}
	if r.URL.Query().Has("max_score") {
		v, err := parseFloat(r, "max_score", 0)
		if err != nil {
			writeError(w, 400, "BAD_REQUEST", err.Error())
			return
		}
		maxComposite = &v
	}

	f := db.CompanyFilter{
		Sector:       sector,
		Ticker:       ticker,
		MinComposite: minComposite,
		MaxComposite: maxComposite,
		Limit:        limit,
		Offset:       offset,
	}

	companies, err := s.store.ListCompanies(r.Context(), f)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to list companies")
		return
	}

	total, err := s.store.CountCompanies(r.Context(), f)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to count companies")
		return
	}

	writeJSON(w, 200, ListResponse{
		Data:       companies,
		Pagination: &Pagination{Limit: limit, Offset: offset, Total: total},
		Freshness:  nil,
	})
}

// GET /api/v1/companies/{ticker}
func (s *Server) handleGetCompany(w http.ResponseWriter, r *http.Request) {
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

	// Get latest score for this company
	score, err := s.store.GetLatestScore(r.Context(), company.ID)
	var scoreData any
	if err == nil {
		scoreData = map[string]any{
			"market":    score.MarketScore,
			"policy":    score.PolicyScore,
			"insider":   score.InsiderScore,
			"composite": score.CompositeScore,
		}
	}

	data := map[string]any{
		"id":      company.ID,
		"ticker":  company.Ticker,
		"name":    company.Name,
		"sector":  company.Sector,
		"scores":  scoreData,
	}
	if score.ID != 0 {
		data["weight_version"] = score.WeightVersion
		data["computed_at"] = score.ComputedAt
	}

	writeJSON(w, 200, DetailResponse{
		Data:      data,
		Freshness: nil,
	})
}

// GET /api/v1/companies/{ticker}/scores?limit=N
func (s *Server) handleCompanyScores(w http.ResponseWriter, r *http.Request) {
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

	scores, err := s.store.GetScoreHistory(r.Context(), company.ID, limit)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to get scores")
		return
	}

	writeJSON(w, 200, ListResponse{
		Data:      scores,
		Freshness: nil,
	})
}

// GET /api/v1/companies/{ticker}/events?source=X&type=X&since=T&limit=N&offset=N
func (s *Server) handleCompanyEvents(w http.ResponseWriter, r *http.Request) {
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

	limit, offset, err := parsePagination(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	since, err := parseTime(r, "since")
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	f := db.EventFilter{
		Source:    parseString(r, "source", ""),
		EventType: parseString(r, "type", ""),
		CompanyID: &company.ID,
		Since:     since,
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
