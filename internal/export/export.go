package export

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/insights"
	"github.com/arclighteng/mrdn/internal/score"
)

var safeFilename = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Run exports all dashboard data from the store as JSON files under outDir.
func Run(ctx context.Context, store *db.Store, outDir string) error {
	log.Println("[export] starting export...")

	// --- Dashboard main view ---
	if err := exportMovers(ctx, store, outDir); err != nil {
		return fmt.Errorf("movers: %w", err)
	}
	if err := exportRankings(ctx, store, outDir); err != nil {
		return fmt.Errorf("rankings: %w", err)
	}
	if err := exportLatestEvents(ctx, store, outDir); err != nil {
		return fmt.Errorf("events: %w", err)
	}
	if err := exportSources(ctx, store, outDir); err != nil {
		return fmt.Errorf("sources: %w", err)
	}
	if err := exportStats(ctx, store, outDir); err != nil {
		return fmt.Errorf("stats: %w", err)
	}
	if err := exportHeatmaps(ctx, store, outDir); err != nil {
		return fmt.Errorf("heatmaps: %w", err)
	}

	// --- List views ---
	if err := exportCompanyList(ctx, store, outDir); err != nil {
		return fmt.Errorf("companies: %w", err)
	}
	if err := exportPersonList(ctx, store, outDir); err != nil {
		return fmt.Errorf("persons: %w", err)
	}

	// --- Signals ---
	if err := exportSignals(ctx, store, outDir); err != nil {
		return fmt.Errorf("signals: %w", err)
	}

	// --- Insights ---
	if err := exportInsights(ctx, store, outDir); err != nil {
		return fmt.Errorf("insights: %w", err)
	}

	// --- Tickers ---
	if err := exportTickers(ctx, store, outDir); err != nil {
		return fmt.Errorf("tickers: %w", err)
	}

	// --- Co-trader network graph ---
	network, err := store.CoTraderNetwork(ctx, 2)
	if err != nil {
		log.Printf("export: co-trader network: %v", err)
	} else {
		if err := writeJSON(filepath.Join(outDir, "network.json"), network); err != nil {
			return fmt.Errorf("writing network.json: %w", err)
		}
	}

	// --- Accountability scoreboard ---
	if err := exportScoreboard(ctx, store, outDir); err != nil {
		return fmt.Errorf("scoreboard: %w", err)
	}

	// --- Per-entity detail pages ---
	if err := exportCompanyDetails(ctx, store, outDir); err != nil {
		return fmt.Errorf("company details: %w", err)
	}
	if err := exportPersonDetails(ctx, store, outDir); err != nil {
		return fmt.Errorf("person details: %w", err)
	}

	// --- Query index for MQL autocomplete ---
	if err := exportQueryIndex(ctx, store, outDir); err != nil {
		return fmt.Errorf("query index: %w", err)
	}

	// --- Data metadata for staleness detection ---
	if err := exportDataMeta(outDir); err != nil {
		return fmt.Errorf("data meta: %w", err)
	}

	log.Println("[export] done")
	return nil
}

func exportMovers(ctx context.Context, store *db.Store, outDir string) error {
	data, err := store.GetScoreMovers(ctx, 24, 10)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(outDir, "scores-movers.json"), envelope(data))
}

func exportRankings(ctx context.Context, store *db.Store, outDir string) error {
	data, err := store.GetScoreRankings(ctx, 500)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(outDir, "scores-rankings.json"), envelope(data))
}

func exportLatestEvents(ctx context.Context, store *db.Store, outDir string) error {
	data, err := store.ListEvents(ctx, db.EventFilter{Limit: 100})
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(outDir, "events-latest.json"), envelope(data))
}

func exportSources(ctx context.Context, store *db.Store, outDir string) error {
	data, err := store.ListSourceMeta(ctx)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(outDir, "sources.json"), envelope(data))
}

func exportStats(ctx context.Context, store *db.Store, outDir string) error {
	data, err := store.GetActivityStats(ctx)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(outDir, "stats-activity.json"), envelope(data))
}

func exportHeatmaps(ctx context.Context, store *db.Store, outDir string) error {
	activity, err := store.GetActivityHeatmap(ctx, 3650)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "stats-activity-heatmap.json"),
		map[string]any{"data": activity, "days": 3650}); err != nil {
		return err
	}

	partySector, err := store.GetPartySectorHeatmap(ctx)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "stats-party-sector.json"), envelope(partySector)); err != nil {
		return err
	}

	repMonth, err := store.GetRepMonthHeatmap(ctx, 15)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "stats-rep-month.json"),
		map[string]any{"data": repMonth, "limit": 15}); err != nil {
		return err
	}

	heatmap, err := store.GetScoreHeatmap(ctx)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(outDir, "scores-heatmap.json"), envelope(heatmap))
}

func exportCompanyList(ctx context.Context, store *db.Store, outDir string) error {
	companies, err := store.ListCompanies(ctx, db.CompanyFilter{Limit: 10000})
	if err != nil {
		return err
	}
	total, err := store.CountCompanies(ctx, db.CompanyFilter{})
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(outDir, "companies.json"), map[string]any{
		"data":       companies,
		"pagination": map[string]any{"total": total, "limit": total, "offset": 0},
	})
}

func exportPersonList(ctx context.Context, store *db.Store, outDir string) error {
	persons, err := store.ListPersons(ctx, db.PersonFilter{Limit: 10000})
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(outDir, "persons.json"), envelope(persons))
}

func exportSignals(ctx context.Context, store *db.Store, outDir string) error {
	dir := filepath.Join(outDir, "signals")

	latency, err := store.LatencyLeaderboard(ctx, 10, 50)
	if err != nil {
		return err
	}
	summary, err := store.LatencySummaryAll(ctx)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "latency.json"),
		map[string]any{"data": latency, "summary": summary}); err != nil {
		return err
	}

	swarms, err := store.SwarmDetector(ctx, 4, 100)
	if err != nil {
		return err
	}
	enrichedSwarms, err := store.EnrichSwarms(ctx, swarms)
	if err != nil {
		log.Printf("export: swarm enrichment failed, exporting raw: %v", err)
		if err := writeJSON(filepath.Join(dir, "swarms.json"), envelope(swarms)); err != nil {
			return err
		}
	} else {
		if err := writeJSON(filepath.Join(dir, "swarms.json"), envelope(enrichedSwarms)); err != nil {
			return err
		}
	}

	consensus, err := store.PartisanTickers(ctx, "consensus", 4, 50)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "partisan-consensus.json"),
		map[string]any{"data": consensus, "mode": "consensus"}); err != nil {
		return err
	}

	contrarian, err := store.PartisanTickers(ctx, "contrarian", 4, 50)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "partisan-contrarian.json"),
		map[string]any{"data": contrarian, "mode": "contrarian"}); err != nil {
		return err
	}

	firstMovers, err := store.FirstMovers(ctx, 4, 40)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "first-movers.json"), envelope(firstMovers)); err != nil {
		return err
	}

	roundTrips, err := store.RoundTrips(ctx, 90, 1000, 100)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "round-trips.json"), envelope(roundTrips))
}

func exportInsights(ctx context.Context, store *db.Store, outDir string) error {
	findings, err := insights.Detect(ctx, store)
	if err != nil {
		return fmt.Errorf("insights: %w", err)
	}
	if findings == nil {
		findings = []insights.Finding{}
	}
	enriched := make([]insights.EnrichedFinding, len(findings))
	for i, f := range findings {
		enriched[i] = insights.EnrichedFinding{
			Finding:  f,
			Timeline: insights.BuildTimeline(f),
		}
	}
	out := struct {
		GeneratedAt string                     `json:"generated_at"`
		Findings    []insights.EnrichedFinding `json:"findings"`
	}{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Findings:    enriched,
	}
	return writeJSON(filepath.Join(outDir, "insights.json"), out)
}

func exportTickers(ctx context.Context, store *db.Store, outDir string) error {
	dir := filepath.Join(outDir, "tickers")

	top, err := store.TopTickers(ctx, 100)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "_top.json"), envelope(top)); err != nil {
		return err
	}

	// Per-ticker detail for each top ticker.
	for _, t := range top {
		if !safeFilename.MatchString(t.Ticker) {
			log.Printf("[export] ticker %q has unsafe characters, skipping", t.Ticker)
			continue
		}
		detail, err := store.GetTickerDetail(ctx, t.Ticker, 100)
		if err != nil {
			log.Printf("[export] ticker %s detail error: %v", t.Ticker, err)
			continue
		}
		if err := writeJSON(filepath.Join(dir, t.Ticker+".json"), envelope(detail)); err != nil {
			return err
		}
	}
	return nil
}

func exportCompanyDetails(ctx context.Context, store *db.Store, outDir string) error {
	dir := filepath.Join(outDir, "companies")

	// Get all companies that have scores (the ones worth showing detail for).
	rankings, err := store.GetScoreRankings(ctx, 10000)
	if err != nil {
		return err
	}

	for _, r := range rankings {
		if !safeFilename.MatchString(r.Ticker) {
			log.Printf("[export] company ticker %q has unsafe characters, skipping", r.Ticker)
			continue
		}
		company, err := store.GetCompanyByTicker(ctx, r.Ticker)
		if err != nil {
			log.Printf("[export] company %s error: %v", r.Ticker, err)
			continue
		}

		latestScore, err := store.GetLatestScore(ctx, company.ID)
		if err != nil {
			log.Printf("[export] company %s has no scores, skipping detail: %v", r.Ticker, err)
			continue
		}
		if latestScore.ComputedAt.IsZero() {
			continue
		}

		scoreHistory, err := store.GetScoreHistory(ctx, company.ID, 50)
		if err != nil {
			log.Printf("[export] company %s score history: %v", r.Ticker, err)
			scoreHistory = nil
		}
		timeline, err := store.GetCompanyTimeline(ctx, company.ID, 50)
		if err != nil {
			log.Printf("[export] company %s timeline: %v", r.Ticker, err)
			timeline = nil
		}
		graph, err := store.BFSGraph(ctx, company.ID, "company", 2, 200)
		if err != nil {
			log.Printf("[export] company %s connections: %v", r.Ticker, err)
			graph = nil
		}

		// Score breakdown contributors.
		now := latestScore.ComputedAt
		marketSince := now.AddDate(0, 0, -30)
		policySince := now.AddDate(0, 0, -90)

		insiderTrades, _ := store.GetInsiderTradesRange(ctx, company.ID, policySince, now)
		sanctions, _ := store.GetSanctionsRange(ctx, company.ID, policySince, now)
		contracts, _ := store.GetContractsRange(ctx, company.ID, policySince, now)
		donations, _ := store.GetDonationsRange(ctx, company.ID, policySince, now)
		marketData, _ := store.GetMarketDataRange(ctx, company.ID, marketSince, now)

		// Trim to top 5 each.
		if len(insiderTrades) > 5 {
			insiderTrades = insiderTrades[:5]
		}
		if len(sanctions) > 5 {
			sanctions = sanctions[:5]
		}
		if len(contracts) > 5 {
			contracts = contracts[:5]
		}
		if len(donations) > 5 {
			donations = donations[:5]
		}
		if len(marketData) > 5 {
			marketData = marketData[:5]
		}

		// Events for the company.
		events, _ := store.ListEvents(ctx, db.EventFilter{CompanyID: &company.ID, Limit: 50})

		bundle := map[string]any{
			"company": map[string]any{
				"id":        company.ID,
				"ticker":    company.Ticker,
				"name":      company.Name,
				"sector":    company.Sector,
				"subsector": company.Subsector,
				"scores":    latestScore,
			},
			"timeline":     emptyIfNil(timeline),
			"scoreHistory": emptyIfNil(scoreHistory),
			"connections":  graph,
			"breakdown": map[string]any{
				"insider_trades": emptyIfNil(insiderTrades),
				"sanctions":      emptyIfNil(sanctions),
				"contracts":      emptyIfNil(contracts),
				"donations":      emptyIfNil(donations),
				"market_data":    emptyIfNil(marketData),
			},
			"events": emptyIfNil(events),
		}

		if err := writeJSON(filepath.Join(dir, r.Ticker+".json"), envelope(bundle)); err != nil {
			return err
		}
	}

	log.Printf("[export] exported %d company detail pages", len(rankings))
	return nil
}

func exportPersonDetails(ctx context.Context, store *db.Store, outDir string) error {
	dir := filepath.Join(outDir, "persons")

	persons, err := store.ListPersons(ctx, db.PersonFilter{Limit: 10000})
	if err != nil {
		return err
	}

	exported := 0
	for _, p := range persons {
		if !safeFilename.MatchString(p.Slug) {
			log.Printf("[export] person slug %q has unsafe characters, skipping", p.Slug)
			continue
		}
		profile, err := store.GetPersonProfile(ctx, p.Slug)
		if err != nil {
			continue
		}
		coTraders, _ := store.CoTraders(ctx, p.Slug, 14, 25)

		bundle := map[string]any{
			"profile":   profile,
			"coTraders": coTraders,
		}

		if err := writeJSON(filepath.Join(dir, p.Slug+".json"), envelope(bundle)); err != nil {
			return err
		}
		exported++
	}

	log.Printf("[export] exported %d person detail pages", exported)
	return nil
}

// envelope wraps data in the standard {"data": ...} response shape.
func envelope(data any) map[string]any {
	return map[string]any{"data": data}
}

// emptyIfNil returns an empty slice (marshals as []) if the input is nil.
func emptyIfNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func exportQueryIndex(ctx context.Context, store *db.Store, outDir string) error {
	traders, err := store.ListActiveTraders(ctx, 500)
	if err != nil {
		log.Printf("[export] query index: active traders: %v", err)
		traders = nil
	}

	type tickerEntry struct {
		Ticker string `json:"ticker"`
		Sector string `json:"sector,omitempty"`
	}
	topTickers, err := store.TopTickers(ctx, 500)
	if err != nil {
		log.Printf("[export] query index: top tickers: %v", err)
		topTickers = nil
	}
	var tickers []tickerEntry
	for _, t := range topTickers {
		sector := ""
		if t.Sector != nil {
			sector = *t.Sector
		}
		tickers = append(tickers, tickerEntry{
			Ticker: t.Ticker,
			Sector: sector,
		})
	}

	agencies, err := store.DistinctAgencies(ctx)
	if err != nil {
		log.Printf("[export] query index: agencies: %v", err)
	}

	sectors, err := store.DistinctSectors(ctx)
	if err != nil {
		log.Printf("[export] query index: sectors: %v", err)
	}

	programs, err := store.DistinctPrograms(ctx)
	if err != nil {
		log.Printf("[export] query index: programs: %v", err)
	}

	committees, err := store.ListCommittees(ctx)
	if err != nil {
		log.Printf("[export] query index: committees: %v", err)
	}

	index := map[string]any{
		"version": time.Now().UTC().Format(time.RFC3339),
		"keys": []map[string]any{
			{"key": "type:", "values": []string{"trade", "contract", "sanction", "donation", "lobbying", "insider", "court", "warn", "tariff"}, "description": "Event type"},
			{"key": "action:", "values": []string{"buy", "sell", "exchange", "10b5-1", "option", "gift", "award", "modification", "cancellation"}, "description": "Action type"},
			{"key": "party:", "values": []string{"D", "R", "I"}, "description": "Political party"},
			{"key": "branch:", "values": []string{"senate", "house"}, "description": "Chamber"},
			{"key": "owner:", "values": []string{"self", "spouse", "dependent"}, "description": "Trade ownership"},
			{"key": "sort:", "values": []string{"recent", "score", "amount"}, "description": "Sort order"},
			{"key": "group:", "values": []string{"type", "company", "person", "sector", "week", "month"}, "description": "Group by"},
			{"key": "market-cap:", "values": []string{"large", "mid", "small"}, "description": "Company size"},
			{"key": "signal:", "values": []string{"swarm", "first-mover", "round-trip", "partisan"}, "description": "Signal membership"},
		},
		"persons":    emptyIfNil(traders),
		"tickers":    emptyIfNil(tickers),
		"agencies":   emptyIfNil(agencies),
		"sectors":    emptyIfNil(sectors),
		"programs":   emptyIfNil(programs),
		"committees": emptyIfNil(committees),
	}

	return writeJSON(filepath.Join(outDir, "query-index.json"), index)
}

func exportDataMeta(outDir string) error {
	meta := map[string]any{
		"exported_at": time.Now().UTC().Format(time.RFC3339),
	}
	return writeJSON(filepath.Join(outDir, "meta.json"), meta)
}

// ScoreboardEntry is one politician's row in the accountability scoreboard JSON.
type ScoreboardEntry struct {
	PersonID            int     `json:"person_id"`
	Slug                string  `json:"slug"`
	Name                string  `json:"name"`
	Party               *string `json:"party,omitempty"`
	State               *string `json:"state,omitempty"`
	Score               int     `json:"score"`
	TradeCount          int     `json:"trade_count"`
	MedianLatencyDays   int     `json:"median_latency_days"`
	LatePct             float64 `json:"late_pct"`
	CommitteeTradeCount int     `json:"committee_trades"`
	RoundTripCount      int     `json:"round_trips"`
	PreEventCount       int     `json:"pre_event_trades"`
}

// buildScoreboardEntries converts raw accountability rows into scored, sorted
// scoreboard entries. It is a pure function with no I/O side-effects.
func buildScoreboardEntries(rows []db.AccountabilityRow) []ScoreboardEntry {
	entries := make([]ScoreboardEntry, 0, len(rows))
	for _, r := range rows {
		ratio := 0.0
		if r.TradeCount > 0 {
			ratio = float64(r.CommitteeTradeCount) / float64(r.TradeCount)
		}
		s := score.AccountabilityScore(score.AccountabilityInput{
			MedianLatencyDays:   r.MedianLatencyDays,
			LatePct:             r.LatePct,
			CommitteeTradeRatio: ratio,
			RoundTripCount:      r.RoundTripCount,
			PreEventCount:       r.PreEventCount,
			TradeCount:          r.TradeCount,
		})
		entries = append(entries, ScoreboardEntry{
			PersonID:            r.PersonID,
			Slug:                r.Slug,
			Name:                r.Name,
			Party:               r.Party,
			State:               r.State,
			Score:               int(s),
			TradeCount:          r.TradeCount,
			MedianLatencyDays:   r.MedianLatencyDays,
			LatePct:             r.LatePct,
			CommitteeTradeCount: r.CommitteeTradeCount,
			RoundTripCount:      r.RoundTripCount,
			PreEventCount:       r.PreEventCount,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Score > entries[j].Score })
	return entries
}

func exportScoreboard(ctx context.Context, store *db.Store, outDir string) error {
	accRows, err := store.AccountabilityInputs(ctx, 5)
	if err != nil {
		log.Printf("export: accountability inputs: %v", err)
		return nil // non-fatal
	}
	entries := buildScoreboardEntries(accRows)
	return writeJSON(filepath.Join(outDir, "scoreboard.json"), entries)
}

// writeJSON marshals data to a JSON file, creating parent directories as needed.
func writeJSON(path string, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
