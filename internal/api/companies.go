package api

import (
	"errors"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/score"
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

// GET /api/v1/companies/{ticker}/score-breakdown?limit=N
// Returns top-N contributing rows per category over each sub-score's window.
func (s *Server) handleCompanyScoreBreakdown(w http.ResponseWriter, r *http.Request) {
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

	limit, err := parseInt(r, "limit", 5)
	if err != nil || limit <= 0 || limit > 50 {
		writeError(w, 400, "BAD_REQUEST", "limit must be 1..50")
		return
	}

	now := time.Now().UTC()
	ctx := r.Context()

	// Insider trades — rank by |shares * price| DESC (market window).
	insiderSince := now.Add(-score.MarketWindow)
	trades, err := s.store.GetInsiderTradesRange(ctx, company.ID, insiderSince, now)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load insider trades")
		return
	}
	sort.SliceStable(trades, func(i, j int) bool {
		return insiderValueCents(trades[i]) > insiderValueCents(trades[j])
	})
	trades = topN(trades, limit)

	// Sanctions — rank by AddedAt DESC (policy window).
	policySince := now.Add(-score.PolicyWindow)
	sanctions, err := s.store.GetSanctionsRange(ctx, company.ID, policySince, now)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load sanctions")
		return
	}
	sort.SliceStable(sanctions, func(i, j int) bool {
		ai, aj := sanctions[i].AddedAt, sanctions[j].AddedAt
		if ai == nil {
			return false
		}
		if aj == nil {
			return true
		}
		return ai.After(*aj)
	})
	sanctions = topN(sanctions, limit)

	// Contracts — rank by AmountCents DESC (policy window).
	contracts, err := s.store.GetContractsRange(ctx, company.ID, policySince, now)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load contracts")
		return
	}
	sort.SliceStable(contracts, func(i, j int) bool {
		return derefInt64(contracts[i].AmountCents) > derefInt64(contracts[j].AmountCents)
	})
	contracts = topN(contracts, limit)

	// Donations — rank by AmountCents DESC (policy window).
	donations, err := s.store.GetDonationsRange(ctx, company.ID, policySince, now)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load donations")
		return
	}
	sort.SliceStable(donations, func(i, j int) bool {
		return derefInt64(donations[i].AmountCents) > derefInt64(donations[j].AmountCents)
	})
	donations = topN(donations, limit)

	// Market data — rank by |ChangePct| DESC (market window).
	mkt, err := s.store.GetMarketDataRange(ctx, company.ID, insiderSince, now)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "failed to load market data")
		return
	}
	sort.SliceStable(mkt, func(i, j int) bool {
		return absFloat(derefFloat64(mkt[i].ChangePct)) > absFloat(derefFloat64(mkt[j].ChangePct))
	})
	mkt = topN(mkt, limit)

	// Include the weight_version of the latest persisted score (may be stale).
	latest, _ := s.store.GetLatestScore(ctx, company.ID)
	var weightVersion any
	var computedAt any
	if latest.ID != 0 {
		weightVersion = latest.WeightVersion
		computedAt = latest.ComputedAt
	}

	data := map[string]any{
		"ticker": company.Ticker,
		"windows": map[string]string{
			"market":  score.MarketWindow.String(),
			"policy":  score.PolicyWindow.String(),
			"insider": score.InsiderWindow.String(),
		},
		"weight_version": weightVersion,
		"computed_at":    computedAt,
		"contributors": map[string]any{
			"insider_trades": trades,
			"sanctions":      sanctions,
			"contracts":      contracts,
			"donations":      donations,
			"market_data":    mkt,
		},
	}
	writeJSON(w, 200, DetailResponse{Data: data, Freshness: nil})
}

func insiderValueCents(t db.InsiderTrade) int64 {
	if t.Shares == nil || t.PriceCents == nil {
		return 0
	}
	v := int64(*t.Shares) * (*t.PriceCents)
	if v < 0 {
		return -v
	}
	return v
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefFloat64(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func topN[T any](xs []T, n int) []T {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
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

	since, until, err := parseTimeRange(r)
	if err != nil {
		writeError(w, 400, "BAD_REQUEST", err.Error())
		return
	}

	f := db.EventFilter{
		Source:    parseString(r, "source", ""),
		EventType: parseString(r, "type", ""),
		CompanyID: &company.ID,
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
