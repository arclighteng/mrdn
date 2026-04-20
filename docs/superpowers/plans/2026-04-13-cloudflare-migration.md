# Cloudflare Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate MRDN from Railway + Neon to Cloudflare Pages + R2, with GitHub Actions cron-based ingestion. Drop Finnhub. Total hosting cost: $0.

**Architecture:** Ingestion Go binary runs as a one-shot GitHub Actions cron job (every 6 hours), polls all public data sources against Neon (free tier, aggressively pruned), computes scores, then exports pre-computed JSON to a `dist/` directory and deploys to Cloudflare Pages. The dashboard fetches static JSON files instead of calling a live API. SSE streaming is dropped. Finnhub live trade stream is dropped (Polygon daily bars suffice for market scoring).

**Tech Stack:** Go (version from go.mod), Cloudflare Pages (free), Neon Postgres (free 0.5 GB), GitHub Actions (free 2000 min/mo), wrangler CLI

**Intentionally dropped features:** SSE live streaming, real-time API, API key authentication, heatmap drill-down endpoints (activity/heatmap/drill, party-sector-heatmap/drill, rep-month-heatmap/drill, rep-ticker-heatmap/drill), time-range filtering on events, server-side company/person filtering. The static site serves pre-computed snapshots — dynamic queries are replaced by client-side filtering where feasible.

---

## File Structure

### New files
```
internal/cli/ingest_once.go        — One-shot poll-all-sources CLI command
internal/cli/export.go             — Export pre-computed JSON to directory
internal/cli/prune.go              — Delete old data to keep Neon under budget
internal/export/export.go          — Core export logic (Store → JSON files)
internal/export/export_test.go     — Tests for export
.github/workflows/ingest-deploy.yml — Cron workflow: ingest → export → deploy
```

### Modified files
```
internal/ingestion/supervisor.go   — Remove Finnhub stream + rebalancer launch
internal/config/config.go          — FinnhubAPIKey no longer required
web/static/index.html              — Fetch from /data/*.json, client-side filtering
```

### Unchanged (but context for workers)
```
internal/db/*.go                   — All Store methods reused by export
internal/parser/*.go               — All parsers except finnhub still used
internal/score/*.go                — Score engine reused by score-backfill
internal/resolver/*.go             — Resolver reused by ingest-once
```

---

## Task 1: Drop Finnhub from Ingestion

**Files:**
- Modify: `internal/ingestion/supervisor.go:102-123`
- Modify: `internal/config/config.go:77-82`

- [ ] **Step 1: Remove Finnhub stream + rebalancer from supervisor**

In `supervisor.go`, remove the block at lines 102-123 that launches the Finnhub stream worker and rebalancer. The supervisor should only launch poll workers and the score worker.

```go
// DELETE this entire block from Start():
//
//	// Launch Finnhub stream worker if key is present...
//	if !s.sourcesSet && s.cfg != nil && s.cfg.FinnhubAPIKey != "" {
//		finnhub := parser.NewFinnhubSource(s.cfg.FinnhubAPIKey, nil)
//		...
//		rebalancer := NewRebalancer(...)
//		...
//	}
```

- [ ] **Step 2: Remove FinnhubAPIKey from ValidateIngestion**

In `config.go`, remove the FinnhubAPIKey check from `ValidateIngestion()`:

```go
func (c *Config) ValidateIngestion() error {
	var missing []string
	// REMOVE: if c.FinnhubAPIKey == "" { missing = append(missing, "MRDN_FINNHUB_API_KEY") }
	if c.PolygonAPIKey == "" {
		missing = append(missing, "MRDN_POLYGON_API_KEY")
	}
	if c.FECAPIKey == "" {
		missing = append(missing, "MRDN_FEC_API_KEY")
	}
	if len(missing) > 0 {
		return fmt.Errorf("ingestion requires environment variables: %v", missing)
	}
	return nil
}
```

- [ ] **Step 3: Run tests to verify nothing breaks**

Run: `go test ./internal/ingestion/... -v -count=1`
Expected: All existing tests pass. Supervisor tests use `WithSources()` so they bypass `registerSources`.

- [ ] **Step 4: Run full test suite**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingestion/supervisor.go internal/config/config.go
git commit -m "feat: drop Finnhub live stream from ingestion

Finnhub market_trade events were the primary storage consumer but only
fed the score engine — not surfaced in the UI. Polygon daily bars
provide sufficient market data for scoring at a fraction of the storage."
```

---

## Task 2: Create `mrdn ingest-once` Command

One-shot command that polls every registered source exactly once, resolves entities, then exits. Designed for GitHub Actions cron. Reuses `supervisor.registerSources()` to avoid source-list drift.

**Files:**
- Modify: `internal/ingestion/supervisor.go` — export `RegisterSources` as a public method
- Create: `internal/cli/ingest_once.go`

- [ ] **Step 1: Export RegisterSources from Supervisor**

In `supervisor.go`, rename `registerSources` → `RegisterSources` (capitalize) so the CLI can call it:

```go
// RegisterSources returns the set of poll-based Sources to supervise.
// Exported so that ingest-once can reuse the same source list.
func (s *Supervisor) RegisterSources() []Source {
```

Also update the call in `Start()`:
```go
	if !s.sourcesSet {
		srcs = s.RegisterSources()
	}
```

- [ ] **Step 2: Write the CLI command**

```go
package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/ingestion"
	"github.com/arclighteng/mrdn/internal/resolver"
	"github.com/spf13/cobra"
)

var ingestOnceCmd = &cobra.Command{
	Use:   "ingest-once",
	Short: "Poll all sources once and exit",
	Long: `One-shot ingestion: polls every registered source exactly once,
resolves entities, and exits. Designed for cron-based ingestion in CI.
Does NOT run the score engine — run score-backfill separately.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		store := db.NewStore(pool)
		res, err := resolver.New(ctx, store)
		if err != nil {
			return fmt.Errorf("initializing resolver: %w", err)
		}

		// Reuse the supervisor's source registry to avoid source-list drift.
		sup := ingestion.NewSupervisor(cfg, store, nil, ingestion.RealClock())
		sources := sup.RegisterSources()

		var totalNew int
		for _, src := range sources {
			log.Printf("[ingest-once] polling %s...", src.Name())
			started := time.Now()
			events, err := src.Poll(ctx)
			dur := time.Since(started)
			if err != nil {
				log.Printf("[ingest-once] %s error (%s): %v", src.Name(), dur, err)
				_ = store.RecordIngestAttempt(ctx, db.IngestAttempt{
					Source:     src.Name(),
					Success:    false,
					Error:      err.Error(),
					DurationMs: int(dur.Milliseconds()),
				})
				continue
			}

			ids, berr := store.InsertEventsBatch(ctx, events)
			if berr != nil {
				log.Printf("[ingest-once] %s batch insert error: %v", src.Name(), berr)
			}

			newCount := 0
			for i, evt := range events {
				id := 0
				if i < len(ids) {
					id = ids[i]
				}
				if id == 0 {
					continue
				}
				newCount++
				evt.ID = id
				// Resolve() persists the company link internally via
				// store.UpdateEventCompanyID — no need to capture the return.
				res.Resolve(ctx, evt)
			}
			totalNew += newCount

			_ = store.RecordIngestAttempt(ctx, db.IngestAttempt{
				Source:     src.Name(),
				Success:    true,
				Records:    len(events),
				DurationMs: int(dur.Milliseconds()),
				HasNewData: newCount > 0,
			})
			log.Printf("[ingest-once] %s: %d events (%d new) in %s", src.Name(), len(events), newCount, dur)
		}

		log.Printf("[ingest-once] done — %d new events total", totalNew)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(ingestOnceCmd)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./cmd/mrdn`
Expected: Clean build

- [ ] **Step 3: Run the full test suite**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/cli/ingest_once.go
git commit -m "feat(cli): add ingest-once command for cron-based ingestion

One-shot poll of all registered sources with entity resolution.
Designed for GitHub Actions cron. Does not run the score engine —
run score-backfill separately after ingest-once."
```

---

## Task 3: Create `mrdn prune` Command

Deletes old events, market_data, and scores beyond a retention window to keep Neon under 0.5 GB.

**Files:**
- Create: `internal/cli/prune.go`

- [ ] **Step 1: Write the prune command**

```go
package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var pruneKeepDays int

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Delete old data to keep storage under budget",
	Long: `Removes events, market_data, and old scores beyond the retention window.
Keeps the most recent score per company regardless of age.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		cutoff := time.Now().AddDate(0, 0, -pruneKeepDays)
		log.Printf("[prune] deleting data older than %s (%d days)", cutoff.Format("2006-01-02"), pruneKeepDays)

		// Delete old market_data (biggest table after Finnhub removal).
		tag, err := pool.Exec(ctx, "DELETE FROM market_data WHERE recorded_at < $1", cutoff)
		if err != nil {
			return fmt.Errorf("pruning market_data: %w", err)
		}
		log.Printf("[prune] market_data: deleted %d rows", tag.RowsAffected())

		// Delete old scores but keep the latest per company.
		tag, err = pool.Exec(ctx, `
			DELETE FROM scores WHERE id IN (
				SELECT s.id FROM scores s
				WHERE s.computed_at < $1
				AND s.id NOT IN (
					SELECT DISTINCT ON (company_id) id
					FROM scores ORDER BY company_id, computed_at DESC
				)
			)`, cutoff)
		if err != nil {
			return fmt.Errorf("pruning scores: %w", err)
		}
		log.Printf("[prune] scores: deleted %d rows", tag.RowsAffected())

		// Delete old events. Must handle FK references first.
		// Typed tables reference events via event_id.
		// NOTE: Raw pool.Exec used here because Store has no bulk-delete methods.
		// Table names are compile-time constants, not user input — no injection risk.
		typedTables := []string{
			"congressional_trades", "contracts", "sanctions", "insider_trades",
			"donations", "lobbying", "court_filings", "warn_filings",
		}
		for _, table := range typedTables {
			tag, err = pool.Exec(ctx, fmt.Sprintf(
				"DELETE FROM %s WHERE event_id IN (SELECT id FROM events WHERE occurred_at < $1)", table), cutoff)
			if err != nil {
				return fmt.Errorf("pruning %s: %w", table, err)
			}
			if tag.RowsAffected() > 0 {
				log.Printf("[prune] %s: deleted %d rows", table, tag.RowsAffected())
			}
		}

		tag, err = pool.Exec(ctx, "DELETE FROM events WHERE occurred_at < $1", cutoff)
		if err != nil {
			return fmt.Errorf("pruning events: %w", err)
		}
		log.Printf("[prune] events: deleted %d rows", tag.RowsAffected())

		log.Println("[prune] done")
		return nil
	},
}

func init() {
	pruneCmd.Flags().IntVar(&pruneKeepDays, "keep-days", 90, "number of days of data to retain")
	rootCmd.AddCommand(pruneCmd)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./cmd/mrdn`
Expected: Clean build

- [ ] **Step 3: Run the full test suite**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/cli/prune.go
git commit -m "feat(cli): add prune command for data retention

Deletes events, market_data, typed records, and old scores beyond the
retention window (default 90 days). Keeps the latest score per company.
Designed to keep Neon free tier under 0.5 GB."
```

---

## Task 4: Create Export Engine

The core logic that reads from the DB and writes pre-computed JSON files matching the dashboard's data needs. Every JSON file preserves the existing `{"data": [...]}` response envelope so dashboard changes are minimal.

**Files:**
- Create: `internal/export/export.go`
- Create: `internal/export/export_test.go`

- [ ] **Step 1: Write the export test**

```go
package export

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "test.json")

	data := map[string]any{"data": []string{"a", "b"}}
	if err := writeJSON(path, data); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if len(got) == 0 {
		t.Fatal("empty file")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/export/... -v -run TestWriteJSON`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Write the export engine**

```go
package export

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/arclighteng/mrdn/internal/db"
)

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

	// --- Tickers ---
	if err := exportTickers(ctx, store, outDir); err != nil {
		return fmt.Errorf("tickers: %w", err)
	}

	// --- Per-entity detail pages ---
	if err := exportCompanyDetails(ctx, store, outDir); err != nil {
		return fmt.Errorf("company details: %w", err)
	}
	if err := exportPersonDetails(ctx, store, outDir); err != nil {
		return fmt.Errorf("person details: %w", err)
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
	// Export ALL companies (no pagination — client-side filter/paginate).
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
	if err := writeJSON(filepath.Join(dir, "swarms.json"), envelope(swarms)); err != nil {
		return err
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

	roundTrips, err := store.RoundTrips(ctx, 60, 15000, 100)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "round-trips.json"), envelope(roundTrips))
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
		}
		timeline, err := store.GetCompanyTimeline(ctx, company.ID, 50)
		if err != nil {
			log.Printf("[export] company %s timeline: %v", r.Ticker, err)
		}
		graph, err := store.BFSGraph(ctx, company.ID, "company", 2, 200)
		if err != nil {
			log.Printf("[export] company %s connections: %v", r.Ticker, err)
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
		if len(insiderTrades) > 5 { insiderTrades = insiderTrades[:5] }
		if len(sanctions) > 5 { sanctions = sanctions[:5] }
		if len(contracts) > 5 { contracts = contracts[:5] }
		if len(donations) > 5 { donations = donations[:5] }
		if len(marketData) > 5 { marketData = marketData[:5] }

		// Events for the company.
		events, _ := store.ListEvents(ctx, db.EventFilter{CompanyID: &company.ID, Limit: 50})

		bundle := map[string]any{
			"company": map[string]any{
				"id":         company.ID,
				"ticker":     company.Ticker,
				"name":       company.Name,
				"sector":     company.Sector,
				"subsector":  company.Subsector,
				"scores":     latestScore,
			},
			"timeline":     timeline,
			"scoreHistory": scoreHistory,
			"connections":  graph,
			"breakdown": map[string]any{
				"insider_trades": insiderTrades,
				"sanctions":      sanctions,
				"contracts":      contracts,
				"donations":      donations,
				"market_data":    marketData,
			},
			"events": events,
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

// writeJSON marshals data to a JSON file, creating parent directories as needed.
func writeJSON(path string, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(data)
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/export/... -v -run TestWriteJSON`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/export/export.go internal/export/export_test.go
git commit -m "feat: add export engine for static JSON generation

Reads all dashboard data from the Store and writes pre-computed JSON
files preserving the existing {data: [...]} response envelope. Exports
dashboard views, signals, per-company detail bundles, and per-person
profile bundles."
```

---

## Task 5: Create `mrdn export` CLI Command

**Files:**
- Create: `internal/cli/export.go`

- [ ] **Step 1: Write the CLI command**

```go
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/export"
	"github.com/spf13/cobra"
)

var exportOutDir string

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export all dashboard data as static JSON files",
	Long: `Reads from the database and writes pre-computed JSON files to the
output directory. These files are deployed to Cloudflare Pages to serve
the dashboard without a live API.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		store := db.NewStore(pool)
		return export.Run(ctx, store, exportOutDir)
	},
}

func init() {
	exportCmd.Flags().StringVar(&exportOutDir, "out", "dist/data", "output directory for JSON files")
	rootCmd.AddCommand(exportCmd)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./cmd/mrdn`
Expected: Clean build

- [ ] **Step 3: Commit**

```bash
git add internal/cli/export.go
git commit -m "feat(cli): add export command

Wires the export engine to a CLI command with --out flag
(default dist/data)."
```

---

## Task 6: Modify Dashboard for Static JSON

Replace live API fetches with static JSON file fetches. Add client-side pagination for companies and client-side filtering for persons.

**Files:**
- Modify: `web/static/index.html`

- [ ] **Step 1: Replace the `api()` method**

Replace the existing `api(path)` method with one that maps API paths to static JSON files:

```javascript
async api(path) {
  try {
    const file = this.apiPathToFile(path);
    const resp = await fetch('/data/' + file);
    if (!resp.ok) return null;
    this.connected = true;
    return await resp.json();
  } catch (e) {
    this.connected = false;
    return null;
  }
},

apiPathToFile(path) {
  // Strip leading slash
  path = path.replace(/^\//, '');

  // Static dashboard endpoints
  const staticMap = {
    'scores/movers': 'scores-movers.json',
    'scores/rankings': 'scores-rankings.json',
    'scores/heatmap': 'scores-heatmap.json',
    'events/latest': 'events-latest.json',
    'sources': 'sources.json',
    'stats/activity': 'stats-activity.json',
    'stats/activity/heatmap': 'stats-activity-heatmap.json',
    'stats/party-sector-heatmap': 'stats-party-sector.json',
    'stats/rep-month-heatmap': 'stats-rep-month.json',
    'compliance/latency': 'signals/latency.json',
    'signals/swarms': 'signals/swarms.json',
    'signals/first-movers': 'signals/first-movers.json',
    'signals/round-trips': 'signals/round-trips.json',
    'tickers/top': 'tickers/_top.json',
  };

  // Strip query params for mapping
  const bare = path.split('?')[0];

  if (staticMap[bare]) return staticMap[bare];

  // Partisan: map mode param to file
  if (bare === 'signals/partisan') {
    const mode = new URLSearchParams(path.split('?')[1] || '').get('mode') || 'consensus';
    return 'signals/partisan-' + mode + '.json';
  }

  // Per-entity detail
  const companyMatch = bare.match(/^companies\/([A-Z0-9]+)$/);
  if (companyMatch) return 'companies/' + companyMatch[1] + '.json';

  const personMatch = bare.match(/^persons\/([a-z0-9-]+)\/profile$/);
  if (personMatch) return 'persons/' + personMatch[1] + '.json';

  const coTradersMatch = bare.match(/^persons\/([a-z0-9-]+)\/co-traders$/);
  if (coTradersMatch) return 'persons/' + coTradersMatch[1] + '.json';

  const tickerMatch = bare.match(/^tickers\/([A-Z0-9]+)$/);
  if (tickerMatch) return 'tickers/' + tickerMatch[1] + '.json';

  // Fallback: try the path as-is
  return bare.replace(/\//g, '-') + '.json';
},
```

- [ ] **Step 2: Modify `loadDashboard()` to strip query params**

The static files already contain the pre-computed data with the right limits, so query params are ignored. No changes needed to `loadDashboard()` — the `apiPathToFile` mapping handles stripping params.

- [ ] **Step 3: Add `allCompanies` property and modify `fetchCompanies()`**

Add `allCompanies: [],` to the Alpine data properties (near `companies: [],` at line 1458).

Then replace the `fetchCompanies()` method (line 1622-1627, starts with `async fetchCompanies() {` and ends with the closing `},`):

```javascript
    async fetchCompanies() {
      if (this.allCompanies.length === 0) {
        const res = await this.api('/companies');
        this.allCompanies = res?.data || [];
      }
      let filtered = this.allCompanies;
      if (this.companySearch) {
        const q = this.companySearch.toLowerCase();
        filtered = this.allCompanies.filter(c =>
          (c.sector || '').toLowerCase().includes(q) ||
          (c.ticker || '').toLowerCase().includes(q) ||
          (c.name || '').toLowerCase().includes(q)
        );
      }
      this.companyTotal = filtered.length;
      const start = this.companyPage * 30;
      this.companies = filtered.slice(start, start + 30);
    },
```

- [ ] **Step 4: Modify `fetchPersons()` for client-side branch filtering**

Replace `fetchPersons()` (line 1629-1633, starts with `async fetchPersons() {`):

```javascript
    async fetchPersons() {
      if (!this._allPersons) {
        const res = await this.api('/persons');
        this._allPersons = res?.data || [];
      }
      if (this.personBranch) {
        this.persons = this._allPersons.filter(p => p.branch === this.personBranch);
      } else {
        this.persons = this._allPersons;
      }
    },
```

- [ ] **Step 5: Modify `openPerson()` to unpack bundled JSON**

Replace `openPerson()` (line 1684-1691, starts with `async openPerson(slug) {`):

```javascript
    async openPerson(slug) {
      this.view = 'person';
      this.personProfile = null;
      this.coTraders = [];
      const res = await this.api(`/persons/${slug}/profile`);
      if (res?.data?.profile) {
        // Static mode: bundled {profile, coTraders}
        this.personProfile = res.data.profile;
        this.coTraders = res.data.coTraders || [];
      } else {
        // Fallback: live API mode
        this.personProfile = res?.data || null;
      }
      this.$nextTick(() => this.renderPersonChart());
    },
```

Also replace `fetchCoTraders()` (line 1693-1697) to check the cache first:

```javascript
    async fetchCoTraders(slug) {
      if (!slug || this.coTraders.length > 0) return;
      const res = await this.api(`/persons/${slug}/co-traders`);
      this.coTraders = res?.data?.coTraders || res?.data || [];
    },
```

- [ ] **Step 6: Modify `openCompany()` to unpack bundled JSON**

Replace `openCompany()` (line 1752-1775, starts with `async openCompany(ticker) {`):

```javascript
    async openCompany(ticker) {
      this.view = 'company';
      this.detail = {};
      this.timeline = [];
      this.scoreHistory = [];

      const res = await this.api(`/companies/${ticker}`);
      const d = res?.data || {};

      if (d.company) {
        // Static mode: bundled {company, timeline, scoreHistory, connections, breakdown, events}
        this.detail = d.company;
        this.timeline = d.timeline || [];
        this.scoreHistory = d.scoreHistory || [];
        this.scoreBreakdown = d.breakdown || null;
        this.$nextTick(() => {
          this.renderScoreHistory();
          if (d.connections) this.renderConnections(d.connections);
        });
      } else {
        // Fallback: live API mode (5 parallel calls)
        const [timelineRes, scoresRes, connRes, breakdownRes] = await Promise.all([
          this.api(`/companies/${ticker}/timeline?limit=50`),
          this.api(`/companies/${ticker}/scores?limit=50`),
          this.api(`/connections/company/${ticker}?depth=2`),
          this.api(`/companies/${ticker}/score-breakdown?limit=5`),
        ]);
        this.detail = d;
        this.timeline = timelineRes?.data || [];
        this.scoreHistory = scoresRes?.data || [];
        this.scoreBreakdown = breakdownRes?.data?.contributors || null;
        this.$nextTick(() => {
          this.renderScoreHistory();
          if (connRes?.data) this.renderConnections(connRes.data);
        });
      }
    },
```

- [ ] **Step 7: Disable heatmap drill-down handlers**

Drill-down endpoints (activity/heatmap/drill, party-sector-heatmap/drill, rep-month-heatmap/drill) are not exported as static JSON. Find all `@click` handlers that call drill functions on heatmap cells (search for `drillOpen`, `openDrill`, or drill-related click handlers in the heatmap rendering code). Replace the drill click behavior with a no-op or remove the `cursor-pointer` styling. The cells should still display their heatmap values but not open a drill modal.

If the drill functions are invoked from ECharts click event handlers (inside `chart.on('click', ...)`), comment out or remove those event handler registrations.

- [ ] **Step 8: Remove API key input from UI**

The static site doesn't need API key authentication. Remove the API key input field and the `X-API-Key` header from the (now-unused) api config.

- [ ] **Step 9: Test locally**

Run: `go run ./cmd/mrdn export --out web/static/data` (against local/Neon DB)
Then `go run ./cmd/mrdn serve` and open `http://localhost:8080`.
Expected: Dashboard loads with real data from static JSON files.

**Note on local development:** After this change, `mrdn serve` still works but the dashboard fetches from `/data/` which is served from `web/static/data/`. Run `mrdn export --out web/static/data` to populate the data before running `mrdn serve` locally.

- [ ] **Step 10: Commit**

```bash
git add web/static/index.html
git commit -m "feat(web): switch dashboard to static JSON consumption

Replace live API fetches with static JSON file reads. Add client-side
pagination for companies list and client-side filtering for persons.
Company and person detail views now read from bundled JSON files.
Removes API key requirement from the UI."
```

---

## Task 7: GitHub Actions Cron Workflow + Cloudflare Pages Deploy

**Files:**
- Create: `.github/workflows/ingest-deploy.yml`

- [ ] **Step 1: Write the workflow**

```yaml
name: Ingest & Deploy to Cloudflare Pages

on:
  schedule:
    # Every 6 hours
    - cron: '0 */6 * * *'
  workflow_dispatch: {}

jobs:
  ingest-and-deploy:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Build
        run: go build -o mrdn ./cmd/mrdn

      - name: Ingest (one-shot poll all sources)
        run: ./mrdn ingest-once
        env:
          DATABASE_URL: ${{ secrets.DATABASE_URL }}
          MRDN_POLYGON_API_KEY: ${{ secrets.MRDN_POLYGON_API_KEY }}
          MRDN_FEC_API_KEY: ${{ secrets.MRDN_FEC_API_KEY }}

      - name: Compute scores
        run: ./mrdn score-backfill --workers 4
        env:
          DATABASE_URL: ${{ secrets.DATABASE_URL }}

      - name: Export static JSON
        run: ./mrdn export --out dist/data
        env:
          DATABASE_URL: ${{ secrets.DATABASE_URL }}

      - name: Copy frontend assets
        run: cp web/static/* dist/

      - name: Deploy to Cloudflare Pages
        uses: cloudflare/wrangler-action@v3
        with:
          apiToken: ${{ secrets.CF_API_TOKEN }}
          command: pages deploy dist --project-name=mrdn

      - name: Prune old data
        run: ./mrdn prune --keep-days 90
        env:
          DATABASE_URL: ${{ secrets.DATABASE_URL }}
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ingest-deploy.yml
git commit -m "ci: add cron workflow for ingest + Cloudflare Pages deploy

Runs every 6 hours: ingest-once → score-backfill → export → deploy →
prune. Replaces the Railway-based deployment pipeline."
```

---

## Task 8: Update Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/OPERATIONS.md`
- Modify: `.env.example`

- [ ] **Step 1: Update README.md Stack section**

Replace the Stack section to reflect the new architecture:

```markdown
## Stack

- **Backend** — Go 1.25, [pgx](https://github.com/jackc/pgx) for Postgres access.
- **Database** — Neon Postgres (serverless, free tier) — used for ingestion only.
- **Frontend** — Single-file dashboard: Alpine.js + Tailwind (CDN) + ECharts.
  Fetches pre-computed JSON from Cloudflare Pages.
- **Hosting** — Cloudflare Pages (free). Static JSON + HTML, no server.
- **Ingestion** — GitHub Actions cron (every 6 hours). One-shot poll + score + export.
```

- [ ] **Step 2: Update OPERATIONS.md**

Add new CLI commands section:

```markdown
### Static export & deployment

| Command | Purpose |
|---------|---------|
| `mrdn ingest-once` | One-shot poll all sources, resolve entities, exit. For CI cron. |
| `mrdn export --out dist/data` | Export all dashboard data as static JSON files. |
| `mrdn prune --keep-days 90` | Delete data older than N days to keep Neon under budget. |
```

Add new deployment section explaining the Cloudflare Pages pipeline.

- [ ] **Step 3: Update .env.example**

Remove `MRDN_FINNHUB_API_KEY` from the example:

```
# External API keys — required when running `mrdn ingest` or `mrdn ingest-once`
# MRDN_POLYGON_API_KEY=your_polygon_api_key
# MRDN_FEC_API_KEY=your_fec_api_key
# Note: MRDN_FINNHUB_API_KEY removed — Finnhub live stream no longer used
```

- [ ] **Step 4: Commit**

```bash
git add README.md docs/OPERATIONS.md .env.example
git commit -m "docs: update for Cloudflare Pages architecture

Reflect the move from Railway + live API to Cloudflare Pages + static
JSON. Document new CLI commands (ingest-once, export, prune). Remove
Finnhub API key from examples."
```

---

## Post-Migration Checklist

After all tasks are complete and the first successful cron run deploys to Cloudflare Pages:

- [ ] Verify the dashboard loads at the Cloudflare Pages URL
- [ ] Verify all views render: dashboard, rankings, companies, persons, signals, tickers
- [ ] Verify company detail and person detail pages load
- [ ] Set up Cloudflare custom domain (mrdn.arclighteng.com → Pages)
- [ ] Disable Railway services (mrdn-api and mrdn-ingest)
- [ ] Remove Railway secrets from GitHub (RAILWAY_TOKEN, etc.)
- [ ] Monitor Neon storage stays under 0.5 GB after a few cron cycles
- [ ] Add required GitHub secrets: `CF_API_TOKEN`, `DATABASE_URL`

---

## Required GitHub Secrets (New)

| Secret | Where to get it |
|--------|----------------|
| `CF_API_TOKEN` | Cloudflare dashboard → My Profile → API Tokens → Create Token (needs Pages edit permission) |
| `DATABASE_URL` | Neon dashboard → Connection Details (the same one Railway uses today) |
| `MRDN_POLYGON_API_KEY` | Existing (move from Railway env vars) |
| `MRDN_FEC_API_KEY` | Existing (move from Railway env vars) |
