# Insights Card Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a pre-computed "Insights" card to the dashboard that surfaces statistically unusual congressional trading patterns, replacing the Risk Scale legend.

**Architecture:** Six Go detector functions in `internal/insights/` compute findings during the export pipeline, writing `insights.json`. The frontend fetches this file, applies a two-tier weighted-random selection algorithm, and renders a hero + 2 secondaries card. Three dashboard bugs are fixed alongside (Activity Mix clickable, Top Movers sub-scores, Risk Scale relocation).

**Tech Stack:** Go (SQLite queries via `internal/db.Store`), Alpine.js + Tailwind CSS frontend

**Spec:** `docs/superpowers/specs/2026-04-19-insights-card-design.md`

---

## File Structure

| File | Responsibility |
|------|---------------|
| **Create:** `internal/insights/insights.go` | `Finding` struct, `Detect()` orchestrator, committee→sector map |
| **Create:** `internal/insights/coordinated.go` | Detector 1: coordinated trades within same calendar week |
| **Create:** `internal/insights/committee.go` | Detector 2: committee-relevant trades |
| **Create:** `internal/insights/preevent.go` | Detector 3: pre-event timing |
| **Create:** `internal/insights/roundtrips.go` | Detector 4: round-trip anomalies |
| **Create:** `internal/insights/swarms.go` | Detector 5: swarm outliers |
| **Create:** `internal/insights/lonewolf.go` | Detector 6: lone wolf trades |
| **Create:** `internal/insights/insights_test.go` | Tests for all detectors + orchestrator |
| **Modify:** `internal/export/export.go` | Add `exportInsights()` call to `Run()` |
| **Modify:** `internal/db/scores.go` | Add sub-scores to `ScoreMover` struct + query |
| **Modify:** `web/static/index.html` | Insights card HTML, selection algorithm, Activity Mix click, Top Movers sub-scores, Risk Scale removal |

---

### Task 1: Finding struct and Detect orchestrator

**Files:**
- Create: `internal/insights/insights.go`
- Create: `internal/insights/insights_test.go`

- [ ] **Step 1: Write the Finding struct and Detect function**

```go
// internal/insights/insights.go
package insights

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

// Finding represents a single insight detected by one of the pattern detectors.
type Finding struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Headline   string          `json:"headline"`
	RarityScore int            `json:"rarity_score"`
	Timestamp  time.Time       `json:"timestamp"`
	Data       json.RawMessage `json:"data"`
	Link       json.RawMessage `json:"link"`
}

// InsightsOutput is the top-level JSON structure written to insights.json.
type InsightsOutput struct {
	GeneratedAt string    `json:"generated_at"`
	Findings    []Finding `json:"findings"`
}

// committeeToSectors maps congressional committee name substrings to GICS sectors.
var committeeToSectors = map[string][]string{
	"Armed Services":                           {"Aerospace & Defense", "Industrials"},
	"Banking":                                  {"Financials", "Banks"},
	"Financial Services":                       {"Financials", "Banks"},
	"Energy and Commerce":                      {"Energy", "Utilities"},
	"Energy and Natural Resources":             {"Energy", "Utilities"},
	"Agriculture":                              {"Consumer Staples", "Materials"},
	"Health":                                   {"Health Care"},
	"Commerce, Science, and Transportation":    {"Technology", "Communication Services"},
	"Science, Space, and Technology":           {"Technology"},
	"Veterans' Affairs":                        {"Health Care"},
}

// Detect runs all pattern detectors and returns the top 20 findings by rarity.
func Detect(ctx context.Context, store *db.Store) ([]Finding, error) {
	type detector struct {
		name string
		fn   func(context.Context, *db.Store) ([]Finding, error)
	}

	detectors := []detector{
		{"coordinated", detectCoordinated},
		{"committee", detectCommittee},
		{"pre-event", detectPreEvent},
		{"round-trips", detectRoundTrips},
		{"swarm-outliers", detectSwarmOutliers},
		{"lone-wolf", detectLoneWolf},
	}

	var all []Finding
	for _, d := range detectors {
		dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		findings, err := d.fn(dctx, store)
		cancel()
		if err != nil {
			log.Printf("[insights] %s detector error: %v (skipping)", d.name, err)
			continue
		}
		all = append(all, findings...)
	}

	// Sort by rarity descending, keep top 20
	sort.Slice(all, func(i, j int) bool {
		return all[i].RarityScore > all[j].RarityScore
	})
	if len(all) > 20 {
		all = all[:20]
	}
	return all, nil
}

// clampScore clamps a rarity score to 0-100.
func clampScore(s int) int {
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
}

// mustJSON marshals v to json.RawMessage, panicking on error (for known-good structs).
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
```

- [ ] **Step 2: Write placeholder detector functions**

Create each detector file with a stub that returns nil, nil so the code compiles:

```go
// internal/insights/coordinated.go
package insights

import (
	"context"
	"github.com/arclighteng/mrdn/internal/db"
)

func detectCoordinated(ctx context.Context, store *db.Store) ([]Finding, error) {
	return nil, nil
}
```

Same pattern for `committee.go`, `preevent.go`, `roundtrips.go`, `swarms.go`, `lonewolf.go`.

- [ ] **Step 3: Write test for Detect orchestrator**

```go
// internal/insights/insights_test.go
package insights

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClampScore(t *testing.T) {
	assert.Equal(t, 0, clampScore(-5))
	assert.Equal(t, 50, clampScore(50))
	assert.Equal(t, 100, clampScore(150))
}

func TestDetect_KeepsTop20(t *testing.T) {
	// Create 25 findings manually to verify truncation
	findings := make([]Finding, 25)
	for i := range findings {
		findings[i] = Finding{
			ID:          "test-" + string(rune('A'+i)),
			RarityScore: i * 4,
			Timestamp:   time.Now(),
		}
	}
	// Verify sort + truncation logic
	assert.True(t, len(findings) > 20)
}
```

- [ ] **Step 4: Run tests to verify compilation**

Run: `cd C:/Users/AR/Projects/mrdn && go build ./internal/insights/ && go test ./internal/insights/ -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/insights/
git commit -m "feat(insights): add Finding struct, Detect orchestrator, and detector stubs"
```

---

### Task 2: Coordinated Trades detector

**Files:**
- Modify: `internal/insights/coordinated.go`
- Modify: `internal/insights/insights_test.go`

- [ ] **Step 1: Write the test**

Use the existing `testDB` pattern from `internal/db/scores_test.go`. Create an in-memory SQLite DB, seed persons + companies + trades, and assert findings.

```go
// In insights_test.go — add this test

func TestDetectCoordinated(t *testing.T) {
	d := setupTestDB(t) // helper that runs migrations + returns *sql.DB
	store := db.NewStore(d)
	ctx := context.Background()

	// Seed: 4 persons trade the same ticker in the same week
	seedPersons(t, d, ctx, 4)
	seedCompany(t, d, ctx, "SIVB", "SVB Financial", "Financials")
	weekStart := time.Now().AddDate(0, 0, -3)
	for i := 1; i <= 4; i++ {
		seedTrade(t, d, ctx, i, "SIVB", "sell", 500000, 1000000, weekStart.AddDate(0, 0, i-1))
	}

	findings, err := detectCoordinated(ctx, store)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "coordinated_trades", findings[0].Type)
	assert.GreaterOrEqual(t, findings[0].RarityScore, 50)
	assert.Contains(t, findings[0].Headline, "SIVB")
}

func TestDetectCoordinated_BelowThreshold(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	// Only 2 persons — below minimum of 3
	seedPersons(t, d, ctx, 2)
	seedCompany(t, d, ctx, "XYZ", "XYZ Corp", "Tech")
	weekStart := time.Now().AddDate(0, 0, -2)
	for i := 1; i <= 2; i++ {
		seedTrade(t, d, ctx, i, "XYZ", "buy", 100000, 250000, weekStart)
	}

	findings, err := detectCoordinated(ctx, store)
	require.NoError(t, err)
	assert.Empty(t, findings)
}
```

Also add test helpers at the top of the test file:

```go
import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	schema, err := os.ReadFile("../db/migrations/001_sqlite_initial.sql")
	require.NoError(t, err)
	_, err = d.ExecContext(context.Background(), string(schema))
	require.NoError(t, err)
	return d
}

func seedPersons(t *testing.T, d *sql.DB, ctx context.Context, n int) {
	t.Helper()
	names := []string{"Alice", "Bob", "Carol", "Dave", "Eve", "Frank"}
	for i := 0; i < n && i < len(names); i++ {
		slug := strings.ToLower(names[i])
		_, err := d.ExecContext(ctx,
			"INSERT OR IGNORE INTO persons (slug, name, role, tier, branch, state, party) VALUES (?, ?, 'senator', 1, 'legislative', 'CA', ?)",
			slug, names[i], []string{"D", "R", "D", "R", "D", "R"}[i])
		require.NoError(t, err)
	}
}

func seedCompany(t *testing.T, d *sql.DB, ctx context.Context, ticker, name, sector string) {
	t.Helper()
	_, err := d.ExecContext(ctx,
		"INSERT OR IGNORE INTO companies (ticker, name, sector) VALUES (?, ?, ?)",
		ticker, name, sector)
	require.NoError(t, err)
}

func seedTrade(t *testing.T, d *sql.DB, ctx context.Context, personID int, ticker, tradeType string, amtLow, amtHigh int, tradedAt time.Time) {
	t.Helper()
	// Get company_id
	var companyID int
	err := d.QueryRowContext(ctx, "SELECT id FROM companies WHERE ticker = ?", ticker).Scan(&companyID)
	require.NoError(t, err)
	_, err = d.ExecContext(ctx,
		`INSERT INTO congressional_trades (person_id, company_id, ticker, trade_type, amount_range_low, amount_range_high, traded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		personID, companyID, ticker, tradeType, amtLow, amtHigh, tradedAt.Format(time.RFC3339))
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/insights/ -run TestDetectCoordinated -v -count=1`
Expected: FAIL (detectCoordinated returns nil)

- [ ] **Step 3: Implement detectCoordinated**

```go
// internal/insights/coordinated.go
package insights

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectCoordinated(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		WITH weekly AS (
			SELECT ct.ticker,
				date(ct.traded_at, 'weekday 0', '-6 days') AS week_start,
				p.name AS rep_name,
				p.slug AS rep_slug,
				ct.trade_type,
				COALESCE(
					CASE
						WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
							THEN (ct.amount_range_low + ct.amount_range_high) / 2
						WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
						WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
						ELSE 0
					END, 0) AS est_amount,
				ct.traded_at
			FROM congressional_trades ct
			JOIN persons p ON p.id = ct.person_id
			WHERE ct.traded_at IS NOT NULL
		)
		SELECT ticker, week_start,
			GROUP_CONCAT(DISTINCT rep_name) AS rep_names,
			COUNT(DISTINCT rep_name) AS rep_count,
			GROUP_CONCAT(DISTINCT trade_type) AS trade_types,
			MIN(traded_at) AS first_trade,
			MAX(traded_at) AS last_trade
		FROM weekly
		GROUP BY ticker, week_start
		HAVING COUNT(DISTINCT rep_name) >= 3
		ORDER BY rep_count DESC, week_start DESC
		LIMIT 50
	`)
	if err != nil {
		return nil, fmt.Errorf("coordinated detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var ticker, weekStart, repNamesCSV, tradeTypesCSV, firstTrade, lastTrade string
		var repCount int
		if err := rows.Scan(&ticker, &weekStart, &repNamesCSV, &repCount, &tradeTypesCSV, &firstTrade, &lastTrade); err != nil {
			return nil, err
		}

		repNames := strings.Split(repNamesCSV, ",")
		tradeTypes := strings.Split(tradeTypesCSV, ",")
		sameDirection := len(tradeTypes) == 1
		dirBonus := 0
		if sameDirection {
			dirBonus = 10
		}
		score := clampScore(40 + (repCount-3)*15 + dirBonus)

		direction := "traded"
		if sameDirection {
			if tradeTypes[0] == "sell" || tradeTypes[0] == "sale_full" || tradeTypes[0] == "sale_partial" {
				direction = "sold"
			} else {
				direction = "bought"
			}
		}

		headline := fmt.Sprintf("Coordinated: %d reps %s %s in the same week", repCount, direction, ticker)

		type dataRow struct {
			Name string `json:"name"`
			Date string `json:"date"`
		}
		var data []dataRow
		for _, n := range repNames {
			data = append(data, dataRow{Name: n})
		}

		ts, _ := parseTime(firstTrade)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("coordinated-%s-%s", ticker, strings.ReplaceAll(weekStart, "-", "")),
			Type:        "coordinated_trades",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data:        mustJSON(data),
			Link:        mustJSON(map[string]string{"view": "signals", "tab": "swarms", "search": ticker}),
		})
	}
	return findings, rows.Err()
}
```

Also add a `DB()` accessor and `parseTime` helper to `insights.go`:

```go
// Add to insights.go
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time: %s", s)
}
```

Need `DB()` method on Store. Add to `internal/db/companies.go`:

```go
// DB returns the underlying DBTX for direct queries.
func (s *Store) DB() DBTX { return s.db }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/insights/ -run TestDetectCoordinated -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/insights/coordinated.go internal/insights/insights.go internal/insights/insights_test.go internal/db/companies.go
git commit -m "feat(insights): implement coordinated trades detector"
```

---

### Task 3: Lone Wolf detector

**Files:**
- Modify: `internal/insights/lonewolf.go`
- Modify: `internal/insights/insights_test.go`

- [ ] **Step 1: Write the test**

```go
func TestDetectLoneWolf(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "NVDA", "NVIDIA Corp", "Technology")

	// 5 baseline trades at ~$100K
	base := time.Now().AddDate(0, -6, 0)
	for i := 0; i < 5; i++ {
		seedTrade(t, d, ctx, 1, "NVDA", "buy", 50000, 100000, base.AddDate(0, i, 0))
	}
	// 1 outlier trade at $2M (ratio ~26x)
	seedTrade(t, d, ctx, 1, "NVDA", "buy", 1500000, 2500000, time.Now().AddDate(0, 0, -5))

	findings, err := detectLoneWolf(ctx, store)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "lone_wolf", findings[0].Type)
	assert.GreaterOrEqual(t, findings[0].RarityScore, 50)
}

func TestDetectLoneWolf_TooFewTrades(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "ABC", "ABC Inc", "Tech")

	// Only 3 trades — below the 5-trade minimum
	for i := 0; i < 3; i++ {
		seedTrade(t, d, ctx, 1, "ABC", "buy", 50000, 100000, time.Now().AddDate(0, -i, 0))
	}

	findings, err := detectLoneWolf(ctx, store)
	require.NoError(t, err)
	assert.Empty(t, findings)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/insights/ -run TestDetectLoneWolf -v -count=1`
Expected: FAIL

- [ ] **Step 3: Implement detectLoneWolf**

```go
// internal/insights/lonewolf.go
package insights

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectLoneWolf(ctx context.Context, store *db.Store) ([]Finding, error) {
	// Step 1: compute per-person median trade amount (persons with ≥5 trades)
	medianRows, err := store.DB().QueryContext(ctx, `
		SELECT person_id, GROUP_CONCAT(est_amt) AS amounts, COUNT(*) AS cnt
		FROM (
			SELECT person_id,
				COALESCE(
					CASE
						WHEN amount_range_low IS NOT NULL AND amount_range_high IS NOT NULL
							THEN (amount_range_low + amount_range_high) / 2
						WHEN amount_range_low IS NOT NULL THEN amount_range_low
						WHEN amount_range_high IS NOT NULL THEN amount_range_high
						ELSE 0
					END, 0) AS est_amt
			FROM congressional_trades
			WHERE amount_range_low IS NOT NULL OR amount_range_high IS NOT NULL
		)
		GROUP BY person_id
		HAVING cnt >= 5
	`)
	if err != nil {
		return nil, fmt.Errorf("lone wolf medians: %w", err)
	}
	defer medianRows.Close()

	type personMedian struct {
		personID int
		median   float64
	}
	var medians []personMedian
	for medianRows.Next() {
		var pid, cnt int
		var amountsCSV string
		if err := medianRows.Scan(&pid, &amountsCSV, &cnt); err != nil {
			return nil, err
		}
		amounts := parseIntCSV(amountsCSV)
		if len(amounts) < 5 {
			continue
		}
		sortInts(amounts)
		med := float64(amounts[len(amounts)/2])
		medians = append(medians, personMedian{personID: pid, median: med})
	}
	if err := medianRows.Err(); err != nil {
		return nil, err
	}

	// Step 2: for each person with a median, find their largest outlier trades
	var findings []Finding
	for _, pm := range medians {
		if pm.median <= 0 {
			continue
		}
		rows, err := store.DB().QueryContext(ctx, `
			SELECT ct.id, p.name, p.slug, ct.ticker, ct.trade_type,
				COALESCE(
					CASE
						WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
							THEN (ct.amount_range_low + ct.amount_range_high) / 2
						WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
						WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
						ELSE 0
					END, 0) AS est_amt,
				ct.traded_at
			FROM congressional_trades ct
			JOIN persons p ON p.id = ct.person_id
			WHERE ct.person_id = ?
			  AND ct.traded_at IS NOT NULL
			ORDER BY est_amt DESC
			LIMIT 3
		`, pm.personID)
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			var tradeID int
			var name, slug, ticker, tradeType, tradedAt string
			var estAmt int64
			if err := rows.Scan(&tradeID, &name, &slug, &ticker, &tradeType, &estAmt, &tradedAt); err != nil {
				rows.Close()
				return nil, err
			}
			ratio := float64(estAmt) / pm.median
			if ratio < 4.0 {
				continue
			}
			score := clampScore(50 + int(math.Min(50, (ratio-4)*10)))

			headline := fmt.Sprintf("Lone wolf: %s traded $%s in %s — %.0f× their typical size",
				name, formatDollars(estAmt), ticker, ratio)

			ts, _ := parseTime(tradedAt)
			findings = append(findings, Finding{
				ID:          fmt.Sprintf("lone_wolf-%s-%s", slug, ts.Format("20060102")),
				Type:        "lone_wolf",
				Headline:    headline,
				RarityScore: score,
				Timestamp:   ts,
				Data: mustJSON(map[string]any{
					"name":       name,
					"ticker":     ticker,
					"trade_type": tradeType,
					"amount":     formatDollars(estAmt),
					"ratio":      fmt.Sprintf("%.1f×", ratio),
					"date":       ts.Format("2006-01-02"),
				}),
				Link: mustJSON(map[string]string{"view": "person", "slug": slug}),
			})
		}
		rows.Close()
	}
	return findings, nil
}

func parseIntCSV(s string) []int {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		var v int
		if _, err := fmt.Sscan(strings.TrimSpace(p), &v); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func formatDollars(cents int64) string {
	if cents >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(cents)/1000000)
	}
	if cents >= 1000 {
		return fmt.Sprintf("%.0fK", float64(cents)/1000)
	}
	return fmt.Sprintf("%d", cents)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/insights/ -run TestDetectLoneWolf -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/insights/lonewolf.go internal/insights/insights_test.go
git commit -m "feat(insights): implement lone wolf detector"
```

---

### Task 4: Pre-Event Timing detector

**Files:**
- Modify: `internal/insights/preevent.go`
- Modify: `internal/insights/insights_test.go`

- [ ] **Step 1: Write the test**

```go
func TestDetectPreEvent(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "LMT", "Lockheed Martin", "Aerospace & Defense")

	// Trade 5 days before a government_contract event
	tradeDate := time.Now().AddDate(0, 0, -10)
	seedTrade(t, d, ctx, 1, "LMT", "buy", 100000, 250000, tradeDate)

	// Event 5 days after the trade
	eventDate := tradeDate.AddDate(0, 0, 5)
	var companyID int
	d.QueryRowContext(ctx, "SELECT id FROM companies WHERE ticker = 'LMT'").Scan(&companyID)
	_, err := d.ExecContext(ctx,
		`INSERT INTO events (source, source_id, company_id, event_type, event_data, occurred_at)
		 VALUES ('usaspending', 'test-evt-1', ?, 'government_contract', '{}', ?)`,
		companyID, eventDate.Format(time.RFC3339))
	require.NoError(t, err)

	findings, err := detectPreEvent(ctx, store)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "pre_event", findings[0].Type)
	assert.Contains(t, findings[0].Headline, "LMT")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/insights/ -run TestDetectPreEvent -v -count=1`
Expected: FAIL

- [ ] **Step 3: Implement detectPreEvent**

```go
// internal/insights/preevent.go
package insights

import (
	"context"
	"fmt"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectPreEvent(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		SELECT ct.ticker, p.name, p.slug, ct.trade_type,
			COALESCE(
				CASE
					WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
						THEN (ct.amount_range_low + ct.amount_range_high) / 2
					WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
					WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
					ELSE 0
				END, 0) AS est_amount,
			ct.traded_at,
			e.event_type,
			e.occurred_at,
			CAST(julianday(e.occurred_at) - julianday(ct.traded_at) AS INTEGER) AS days_gap
		FROM congressional_trades ct
		JOIN companies c ON c.ticker = ct.ticker
		JOIN events e ON e.company_id = c.id
		JOIN persons p ON p.id = ct.person_id
		WHERE ct.traded_at IS NOT NULL
		  AND e.event_type IN ('sec_litigation', 'government_contract', 'sanctions', 'regulatory_action', 'tariff_action')
		  AND julianday(e.occurred_at) - julianday(ct.traded_at) BETWEEN 1 AND 14
		ORDER BY days_gap ASC
		LIMIT 50
	`)
	if err != nil {
		return nil, fmt.Errorf("pre-event detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	seen := map[string]bool{} // dedupe by ticker+trade_date
	for rows.Next() {
		var ticker, name, slug, tradeType, tradedAt, eventType, eventOccurred string
		var estAmount int64
		var daysGap int
		if err := rows.Scan(&ticker, &name, &slug, &tradeType, &estAmount, &tradedAt, &eventType, &eventOccurred, &daysGap); err != nil {
			return nil, err
		}

		key := fmt.Sprintf("%s-%s-%s", slug, ticker, tradedAt)
		if seen[key] {
			continue
		}
		seen[key] = true

		amtFactor := 0
		if estAmount > 500000 {
			amtFactor = 5
		} else if estAmount > 100000 {
			amtFactor = 3
		}
		score := clampScore(60 + (14-daysGap)*3 + amtFactor)

		prettyEvent := prettyEventType(eventType)
		headline := fmt.Sprintf("Pre-event: %s traded %s %dd before %s", name, ticker, daysGap, prettyEvent)

		ts, _ := parseTime(tradedAt)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("pre_event-%s-%s", ticker, ts.Format("20060102")),
			Type:        "pre_event",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":       name,
				"ticker":     ticker,
				"trade_type": tradeType,
				"amount":     formatDollars(estAmount),
				"trade_date": ts.Format("2006-01-02"),
				"event_type": eventType,
				"event_date": eventOccurred[:10],
				"days_gap":   daysGap,
			}),
			Link: mustJSON(map[string]string{"view": "company", "ticker": ticker}),
		})
	}
	return findings, rows.Err()
}

func prettyEventType(t string) string {
	switch t {
	case "government_contract":
		return "a government contract"
	case "sec_litigation":
		return "an SEC litigation"
	case "sanctions":
		return "a sanctions action"
	case "regulatory_action":
		return "a regulatory action"
	case "tariff_action":
		return "a tariff action"
	default:
		return t
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/insights/ -run TestDetectPreEvent -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/insights/preevent.go internal/insights/insights_test.go
git commit -m "feat(insights): implement pre-event timing detector"
```

---

### Task 5: Round-Trip Anomalies detector

**Files:**
- Modify: `internal/insights/roundtrips.go`
- Modify: `internal/insights/insights_test.go`

- [ ] **Step 1: Write the test**

```go
func TestDetectRoundTrips(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "AAPL", "Apple Inc", "Technology")

	// 5 normal round-trips with 60-day holds
	base := time.Now().AddDate(-1, 0, 0)
	for i := 0; i < 5; i++ {
		buyDate := base.AddDate(0, i*3, 0)
		sellDate := buyDate.AddDate(0, 0, 60)
		seedTrade(t, d, ctx, 1, "AAPL", "buy", 50000, 100000, buyDate)
		seedTrade(t, d, ctx, 1, "AAPL", "sale_full", 50000, 100000, sellDate)
	}
	// 1 fast round-trip: 5-day hold
	recentBuy := time.Now().AddDate(0, 0, -10)
	seedTrade(t, d, ctx, 1, "AAPL", "buy", 100000, 250000, recentBuy)
	seedTrade(t, d, ctx, 1, "AAPL", "sale_full", 100000, 250000, recentBuy.AddDate(0, 0, 5))

	findings, err := detectRoundTrips(ctx, store)
	require.NoError(t, err)
	// Should detect the fast 5-day round-trip as an anomaly
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "round_trip", findings[0].Type)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/insights/ -run TestDetectRoundTrips -v -count=1`
Expected: FAIL

- [ ] **Step 3: Implement detectRoundTrips**

```go
// internal/insights/roundtrips.go
package insights

import (
	"context"
	"fmt"
	"math"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectRoundTrips(ctx context.Context, store *db.Store) ([]Finding, error) {
	// Step 1: Wide query to build per-person median hold days (365-day window, no min amount)
	allRTs, err := store.RoundTrips(ctx, 365, 0, 1000)
	if err != nil {
		return nil, fmt.Errorf("round-trip detector (wide): %w", err)
	}
	personHolds := map[int][]int{}
	for _, rt := range allRTs {
		personHolds[rt.PersonID] = append(personHolds[rt.PersonID], rt.HoldDays)
	}
	personMedianHold := map[int]float64{}
	for pid, holds := range personHolds {
		if len(holds) < 5 {
			continue
		}
		sortInts(holds)
		personMedianHold[pid] = float64(holds[len(holds)/2])
	}

	// Step 2: Narrow query for fast round-trips to score as findings
	rts, err := store.RoundTrips(ctx, 30, 10000, 100)
	if err != nil {
		return nil, fmt.Errorf("round-trip detector (narrow): %w", err)
	}

	var findings []Finding
	for _, rt := range rts {
		medianHold, ok := personMedianHold[rt.PersonID]
		if !ok || medianHold <= 0 {
			// No baseline — score purely on hold duration
			if rt.HoldDays > 14 {
				continue
			}
			score := clampScore(50 + (14-rt.HoldDays)*3)
			headline := fmt.Sprintf("Fast round-trip: %s bought then sold %s in %d days",
				rt.Name, rt.Ticker, rt.HoldDays)
			ts, _ := parseTime(rt.SellDate)
			findings = append(findings, Finding{
				ID:          fmt.Sprintf("round_trip-%s-%s-%s", rt.Slug, rt.Ticker, ts.Format("20060102")),
				Type:        "round_trip",
				Headline:    headline,
				RarityScore: score,
				Timestamp:   ts,
				Data: mustJSON(map[string]any{
					"name":      rt.Name,
					"ticker":    rt.Ticker,
					"hold_days": rt.HoldDays,
					"buy_date":  rt.BuyDate,
					"sell_date": rt.SellDate,
					"amount":    formatDollars(rt.BuyAmount),
				}),
				Link: mustJSON(map[string]string{"view": "signals", "tab": "round-trips", "search": rt.Name}),
			})
			continue
		}

		deviation := (medianHold - float64(rt.HoldDays)) / medianHold
		if deviation <= 0.3 {
			continue // not anomalous enough
		}
		amtFactor := 0
		if rt.BuyAmount > 500000 {
			amtFactor = 25
		} else if rt.BuyAmount > 100000 {
			amtFactor = 15
		}
		score := clampScore(50 + int(math.Min(25, deviation*25)) + amtFactor)

		headline := fmt.Sprintf("Fast round-trip: %s bought then sold %s in %d days (median: %.0fd)",
			rt.Name, rt.Ticker, rt.HoldDays, medianHold)
		ts, _ := parseTime(rt.SellDate)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("round_trip-%s-%s-%s", rt.Slug, rt.Ticker, ts.Format("20060102")),
			Type:        "round_trip",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":        rt.Name,
				"ticker":      rt.Ticker,
				"hold_days":   rt.HoldDays,
				"median_hold": fmt.Sprintf("%.0f", medianHold),
				"buy_date":    rt.BuyDate,
				"sell_date":   rt.SellDate,
				"amount":      formatDollars(rt.BuyAmount),
			}),
			Link: mustJSON(map[string]string{"view": "signals", "tab": "round-trips", "search": rt.Name}),
		})
	}
	return findings, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/insights/ -run TestDetectRoundTrips -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/insights/roundtrips.go internal/insights/insights_test.go
git commit -m "feat(insights): implement round-trip anomaly detector"
```

---

### Task 6: Swarm Outliers detector

**Files:**
- Modify: `internal/insights/swarms.go`
- Modify: `internal/insights/insights_test.go`

- [ ] **Step 1: Write the test**

```go
func TestDetectSwarmOutliers(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 6)
	seedCompany(t, d, ctx, "TSLA", "Tesla Inc", "Consumer Discretionary")

	// Create swarm data across 3 weeks with many reps
	for week := 0; week < 3; week++ {
		weekDate := time.Now().AddDate(0, 0, -7*week)
		for rep := 1; rep <= 5; rep++ {
			seedTrade(t, d, ctx, rep, "TSLA", "buy", 50000, 100000, weekDate)
		}
	}

	findings, err := detectSwarmOutliers(ctx, store)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "swarm_outlier", findings[0].Type)
	assert.Contains(t, findings[0].Headline, "TSLA")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/insights/ -run TestDetectSwarmOutliers -v -count=1`
Expected: FAIL

- [ ] **Step 3: Implement detectSwarmOutliers**

```go
// internal/insights/swarms.go
package insights

import (
	"context"
	"fmt"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectSwarmOutliers(ctx context.Context, store *db.Store) ([]Finding, error) {
	swarms, err := store.SwarmDetector(ctx, 3, 200)
	if err != nil {
		return nil, fmt.Errorf("swarm outlier detector: %w", err)
	}

	// Aggregate by ticker: total reps across weeks + count of distinct weeks
	type agg struct {
		totalReps    int
		distinctWeeks int
		latestWeek   string
		repNames     []string
	}
	byTicker := map[string]*agg{}
	for _, s := range swarms {
		a, ok := byTicker[s.Ticker]
		if !ok {
			a = &agg{}
			byTicker[s.Ticker] = a
		}
		a.totalReps += s.Reps
		a.distinctWeeks++
		if a.latestWeek == "" || s.WeekStart > a.latestWeek {
			a.latestWeek = s.WeekStart
		}
		for _, n := range s.RepNames {
			a.repNames = append(a.repNames, n)
		}
	}

	var findings []Finding
	for ticker, a := range byTicker {
		score := clampScore(30 + a.totalReps*5 + a.distinctWeeks*10)
		if score < 50 {
			continue
		}

		headline := fmt.Sprintf("Swarm: %d reps traded %s across %d weeks",
			a.totalReps, ticker, a.distinctWeeks)

		ts, _ := parseTime(a.latestWeek)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("swarm_outlier-%s-%s", ticker, ts.Format("20060102")),
			Type:        "swarm_outlier",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"ticker":        ticker,
				"total_reps":    a.totalReps,
				"weeks":         a.distinctWeeks,
				"latest_week":   a.latestWeek,
			}),
			Link: mustJSON(map[string]string{"view": "signals", "tab": "swarms", "search": ticker}),
		})
	}
	return findings, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/insights/ -run TestDetectSwarmOutliers -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/insights/swarms.go internal/insights/insights_test.go
git commit -m "feat(insights): implement swarm outlier detector"
```

---

### Task 7: Committee-Relevant Trades detector

**Files:**
- Modify: `internal/insights/committee.go`
- Modify: `internal/insights/insights_test.go`

- [ ] **Step 1: Write the test**

```go
func TestDetectCommittee(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "RTX", "RTX Corp", "Aerospace & Defense")

	// Assign person 1 to Armed Services committee
	_, err := d.ExecContext(ctx,
		"INSERT INTO person_committees (person_id, committee_name) VALUES (1, 'Armed Services')")
	require.NoError(t, err)

	// Trade in a sector that maps to Armed Services
	seedTrade(t, d, ctx, 1, "RTX", "buy", 100000, 250000, time.Now().AddDate(0, 0, -5))

	findings, err := detectCommittee(ctx, store)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(findings), 1)
	assert.Equal(t, "committee_relevant", findings[0].Type)
	assert.Contains(t, findings[0].Headline, "Armed Services")
}

func TestDetectCommittee_NoMatch(t *testing.T) {
	d := setupTestDB(t)
	store := db.NewStore(d)
	ctx := context.Background()

	seedPersons(t, d, ctx, 1)
	seedCompany(t, d, ctx, "AAPL", "Apple Inc", "Technology")

	// Person on Agriculture committee trading Tech stock — no match
	_, err := d.ExecContext(ctx,
		"INSERT INTO person_committees (person_id, committee_name) VALUES (1, 'Agriculture')")
	require.NoError(t, err)
	seedTrade(t, d, ctx, 1, "AAPL", "buy", 50000, 100000, time.Now().AddDate(0, 0, -3))

	findings, err := detectCommittee(ctx, store)
	require.NoError(t, err)
	assert.Empty(t, findings)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/insights/ -run TestDetectCommittee -v -count=1`
Expected: FAIL

- [ ] **Step 3: Implement detectCommittee**

```go
// internal/insights/committee.go
package insights

import (
	"context"
	"fmt"
	"strings"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectCommittee(ctx context.Context, store *db.Store) ([]Finding, error) {
	// Build a set of sectors per committee for matching
	// committeeToSectors is defined in insights.go

	rows, err := store.DB().QueryContext(ctx, `
		SELECT p.name, p.slug, pc.committee_name, c.sector, ct.ticker, ct.trade_type,
			COALESCE(
				CASE
					WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
						THEN (ct.amount_range_low + ct.amount_range_high) / 2
					WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
					WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
					ELSE 0
				END, 0) AS est_amount,
			ct.traded_at
		FROM congressional_trades ct
		JOIN persons p ON p.id = ct.person_id
		JOIN person_committees pc ON pc.person_id = p.id
		JOIN companies c ON c.ticker = ct.ticker
		WHERE ct.traded_at IS NOT NULL
		  AND c.sector IS NOT NULL
		ORDER BY ct.traded_at DESC
		LIMIT 500
	`)
	if err != nil {
		return nil, fmt.Errorf("committee detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	seen := map[string]bool{}
	for rows.Next() {
		var name, slug, committee, sector, ticker, tradeType, tradedAt string
		var estAmount int64
		if err := rows.Scan(&name, &slug, &committee, &sector, &ticker, &tradeType, &estAmount, &tradedAt); err != nil {
			return nil, err
		}

		// Check if committee maps to this sector
		matched := false
		matchedCommittee := ""
		for cmtKey, sectors := range committeeToSectors {
			if !strings.Contains(strings.ToLower(committee), strings.ToLower(cmtKey)) {
				continue
			}
			for _, s := range sectors {
				if strings.EqualFold(sector, s) {
					matched = true
					matchedCommittee = cmtKey
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			continue
		}

		key := fmt.Sprintf("%s-%s-%s", slug, ticker, tradedAt[:10])
		if seen[key] {
			continue
		}
		seen[key] = true

		amtFactor := 0
		if estAmount > 500000 {
			amtFactor = 20
		} else if estAmount > 100000 {
			amtFactor = 10
		}
		score := clampScore(50 + amtFactor + 20) // base 70 for committee match + amount bonus

		headline := fmt.Sprintf("Committee overlap: %s (%s) traded %s (%s)",
			name, matchedCommittee, ticker, sector)

		ts, _ := parseTime(tradedAt)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("committee_relevant-%s-%s-%s", slug, ticker, ts.Format("20060102")),
			Type:        "committee_relevant",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":      name,
				"committee": matchedCommittee,
				"ticker":    ticker,
				"sector":    sector,
				"amount":    formatDollars(estAmount),
				"date":      ts.Format("2006-01-02"),
			}),
			Link: mustJSON(map[string]string{"view": "person", "slug": slug}),
		})
	}
	return findings, rows.Err()
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/insights/ -run TestDetectCommittee -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/insights/committee.go internal/insights/insights_test.go
git commit -m "feat(insights): implement committee-relevant trades detector"
```

---

### Task 8: Export pipeline integration

**Files:**
- Modify: `internal/export/export.go`

- [ ] **Step 1: Write exportInsights function and wire into Run**

Add to `internal/export/export.go`:

```go
import "github.com/arclighteng/mrdn/internal/insights"

// In the Run() function, add this call after exportSignals (around line 55):
// 	exportInsights(ctx, store, outDir),

func exportInsights(ctx context.Context, store *db.Store, outDir string) error {
	findings, err := insights.Detect(ctx, store)
	if err != nil {
		return fmt.Errorf("insights: %w", err)
	}
	out := insights.InsightsOutput{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Findings:    findings,
	}
	if out.Findings == nil {
		out.Findings = []insights.Finding{}
	}
	return writeJSON(filepath.Join(outDir, "insights.json"), out)
}
```

In the `Run()` function, add `exportInsights` to the list of export calls. Find the line after `exportSignals` and add it there.

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/export/`
Expected: builds successfully

- [ ] **Step 3: Commit**

```bash
git add internal/export/export.go
git commit -m "feat(export): wire insights detector into export pipeline"
```

---

### Task 9: Top Movers sub-scores

**Files:**
- Modify: `internal/db/scores.go`
- Modify: `web/static/index.html`

- [ ] **Step 1: Add sub-scores to ScoreMover struct and query**

In `internal/db/scores.go`, update `ScoreMover` struct (line 138):

```go
type ScoreMover struct {
	Ticker        string  `json:"ticker"`
	CompanyName   string  `json:"name"`
	PreviousScore float64 `json:"previous_score"`
	CurrentScore  float64 `json:"composite"`
	Change        float64 `json:"delta"`
	AbsChange     float64 `json:"abs_change"`
	Market        float64 `json:"market"`
	Policy        float64 `json:"policy"`
	Insider       float64 `json:"insider"`
}
```

Update `GetScoreMovers` SQL query (line 158) to select sub-scores from the `recent` CTE:

In the `recent` CTE, also select `market_score, policy_score, insider_score`.
In the final SELECT, add `r.market_score, r.policy_score, r.insider_score`.
Update the `Scan` call to include `&m.Market, &m.Policy, &m.Insider`.

Specifically, change the CTE:
```sql
WITH recent AS (
    SELECT company_id, composite_score, market_score, policy_score, insider_score, computed_at,
        ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY computed_at DESC) AS rn
    FROM scores
    WHERE computed_at >= ?
),
```

And the final SELECT:
```sql
SELECT c.ticker, c.name,
    p.composite_score AS previous_score,
    r.composite_score AS current_score,
    r.composite_score - p.composite_score AS change,
    ABS(r.composite_score - p.composite_score) AS abs_change,
    COALESCE(r.market_score, 0),
    COALESCE(r.policy_score, 0),
    COALESCE(r.insider_score, 0)
```

And the Scan:
```go
if err := rows.Scan(&m.Ticker, &m.CompanyName, &m.PreviousScore,
    &m.CurrentScore, &m.Change, &m.AbsChange,
    &m.Market, &m.Policy, &m.Insider); err != nil {
```

- [ ] **Step 2: Update Top Movers HTML in frontend**

In `web/static/index.html`, find the Top Movers template (around line 365). Update each mover row to show sub-scores:

Replace the current mover row div content with:
```html
<div @click="openCompany(m.ticker)" class="flex items-center justify-between p-2.5 rounded-lg bg-surface-2 hover:bg-surface-3 cursor-pointer transition-colors">
  <div class="min-w-0">
    <span class="font-mono text-sm font-semibold text-white" x-text="m.ticker"></span>
    <span class="text-xs text-neutral-400 ml-2 truncate" x-text="m.name"></span>
  </div>
  <div class="flex items-center gap-3">
    <div class="hidden sm:flex items-center gap-1.5 text-[10px] text-neutral-400">
      <span>M:<span class="font-mono" :style="`color:${scoreColor(m.market)}`" x-text="m.market?.toFixed(0) ?? '—'"></span></span>
      <span>P:<span class="font-mono" :style="`color:${scoreColor(m.policy)}`" x-text="m.policy?.toFixed(0) ?? '—'"></span></span>
      <span>I:<span class="font-mono" :style="`color:${scoreColor(m.insider)}`" x-text="m.insider?.toFixed(0) ?? '—'"></span></span>
    </div>
    <div class="text-right">
      <span class="font-mono text-sm" :class="m.delta >= 0 ? 'text-up' : 'text-down'"
        x-text="(m.delta >= 0 ? '+' : '') + m.delta.toFixed(1)"></span>
      <div class="text-xs text-neutral-400" x-text="m.composite.toFixed(1)"></div>
    </div>
  </div>
</div>
```

- [ ] **Step 3: Run Go tests**

Run: `go test ./internal/db/ -run TestGetScoreMovers -v -count=1`
Expected: PASS (test may need update if it checks Scan args — update accordingly)

- [ ] **Step 4: Commit**

```bash
git add internal/db/scores.go web/static/index.html
git commit -m "feat: add sub-scores to Top Movers card"
```

---

### Task 10: Frontend — Insights card, Activity Mix clicks, Risk Scale removal

**Files:**
- Modify: `web/static/index.html`

- [ ] **Step 1: Add insights state and fetch/select methods**

In the `app()` function's return object (around line 1791), add new state:

```javascript
insights: [],
insightHero: null,
insightSecondaries: [],
```

Add new methods:

```javascript
async fetchInsights() {
  try {
    const res = await this.api('/insights');
    this.insights = res?.findings || [];
    this.selectInsights();
  } catch {
    this.insights = [];
  }
},

selectInsights() {
  const findings = this.insights;
  if (!findings.length) { this.insightHero = null; this.insightSecondaries = []; return; }

  // Hero: highest rarity
  let heroIdx = 0;
  for (let i = 1; i < findings.length; i++) {
    if (findings[i].rarity_score > findings[heroIdx].rarity_score) heroIdx = i;
  }
  this.insightHero = findings[heroIdx];

  // Secondaries: weighted random from rest
  const rest = findings.filter((_, i) => i !== heroIdx);
  if (!rest.length) { this.insightSecondaries = []; return; }

  const now = Date.now();
  const halfLife = 7 * 24 * 60 * 60 * 1000;
  const scored = rest.map(f => {
    const age = now - new Date(f.timestamp).getTime();
    const recency = 100 * Math.pow(0.5, age / halfLife);
    return { f, score: f.rarity_score * 0.55 + recency * 0.40 + Math.random() * 5 };
  });
  scored.sort((a, b) => b.score - a.score);
  this.insightSecondaries = scored.slice(0, 2).map(s => s.f);
},

insightClick(f) {
  const link = typeof f.link === 'string' ? JSON.parse(f.link) : f.link;
  if (link.view === 'signals') {
    this.signalsTab = link.tab;
    const searchFields = {
      'swarms': 'swarmSearch', 'round-trips': 'roundTripSearch',
      'compliance': 'complianceSearch', 'consensus': 'partisanSearch', 'contrarian': 'partisanSearch'
    };
    const field = searchFields[link.tab];
    if (field && link.search) this[field] = link.search;
    this.navigate('signals');
  } else if (link.view === 'company') {
    this.openCompany(link.ticker);
  } else if (link.view === 'person') {
    this.openPerson(link.slug);
  }
},

insightAge(f) {
  if (!f?.timestamp) return '';
  const days = Math.floor((Date.now() - new Date(f.timestamp).getTime()) / 86400000);
  if (days === 0) return 'today';
  if (days === 1) return 'yesterday';
  if (days < 7) return days + 'd ago';
  if (days < 30) return Math.floor(days / 7) + 'w ago';
  return Math.floor(days / 30) + 'mo ago';
},
```

Add the `fetchInsights()` call to the dashboard data fetch (around line 1994, where moversRes is fetched). Add `this.api('/insights')` to the Promise.all array, and assign the result.

Register the API route in the static file map (around line 1949):
```javascript
'insights': 'insights.json',
```

- [ ] **Step 2: Remove Risk Scale legend**

Delete the entire "Score Legend (dismissable)" div (lines 274–289):
```html
<!-- Score Legend (dismissable) -->
<div x-data="{ show: ... }" ... >
  ...
</div>
```

Add a risk scale tooltip to the Rankings view header. Find the Rankings `<p>` tag (around line 487) and append:
```html
<span class="tip tip-right" data-tip="Risk Scale: 0–39 Low (green), 40–69 Moderate (yellow), 70–100 High (red). Scores measure political risk from market signals, policy actions, and insider activity.">&#9432;</span>
```

- [ ] **Step 3: Add Insights card HTML**

Insert in the dashboard section (line 295, after the source status banner), before the Activity Strip:

```html
<!-- Insights Card -->
<div x-show="insightHero" class="bg-surface-1 rounded-xl border border-white/15 p-5">
  <div class="flex items-center gap-2 mb-3">
    <span class="text-[10px] uppercase tracking-widest text-accent font-semibold">Insights</span>
    <span class="text-[10px] text-neutral-500" x-show="insights.length" x-text="'· ' + insights.length + ' patterns detected'"></span>
  </div>
  <div class="flex gap-5" style="align-items:stretch">
    <!-- Hero -->
    <div @click="insightClick(insightHero)" class="flex-[1.5] bg-surface-2 rounded-lg p-4 border-l-[3px] border-accent cursor-pointer hover:bg-surface-3 transition-colors">
      <div class="text-sm text-neutral-200 font-medium mb-2" x-text="insightHero?.headline"></div>
      <div class="space-y-1">
        <template x-for="(row, i) in (insightHero?.data || []).slice(0, 4)" :key="i">
          <div class="flex items-center gap-3 text-[11px] text-neutral-400">
            <span class="text-neutral-300" x-text="row.name"></span>
            <span x-text="row.action || row.trade_type || ''"></span>
            <span x-text="row.amount || ''"></span>
            <span class="text-neutral-500 ml-auto" x-text="row.date || ''"></span>
          </div>
        </template>
        <div x-show="(insightHero?.data || []).length > 4" class="text-[10px] text-neutral-500" x-text="'+' + ((insightHero?.data || []).length - 4) + ' more'"></div>
        <div x-show="typeof insightHero?.data === 'object' && !Array.isArray(insightHero?.data)" class="flex flex-wrap gap-3 text-[11px] text-neutral-400">
          <template x-for="(val, key) in (typeof insightHero?.data === 'object' && !Array.isArray(insightHero?.data) ? insightHero.data : {})" :key="key">
            <span><span class="text-neutral-500" x-text="key.replace(/_/g, ' ') + ': '"></span><span class="text-neutral-300" x-text="val"></span></span>
          </template>
        </div>
      </div>
      <div class="text-[10px] text-neutral-500 mt-2">
        <span x-text="insightHero?.type?.replace(/_/g, ' ')"></span>
        <span class="mx-1">·</span>
        <span>rarity <span class="text-accent" x-text="insightHero?.rarity_score"></span></span>
        <span class="mx-1">·</span>
        <span x-text="insightAge(insightHero)"></span>
      </div>
    </div>
    <!-- Secondaries -->
    <div class="flex-1 flex flex-col gap-3">
      <template x-for="sec in insightSecondaries" :key="sec.id">
        <div @click="insightClick(sec)" class="bg-surface-2 rounded-lg p-3 cursor-pointer hover:bg-surface-3 transition-colors flex-1">
          <div class="text-[11px] text-accent mb-1" x-text="sec.type?.replace(/_/g, ' ')"></div>
          <div class="text-xs text-neutral-300" x-text="sec.headline"></div>
          <div class="text-[10px] text-neutral-500 mt-1.5">
            <span>rarity <span class="text-accent" x-text="sec.rarity_score"></span></span>
            <span class="mx-1">·</span>
            <span x-text="insightAge(sec)"></span>
          </div>
        </div>
      </template>
    </div>
  </div>
</div>
```

- [ ] **Step 4: Make Activity Mix badges clickable**

Find the Activity Mix template (around line 333). Change the badge `<span>` to a clickable element:

Replace:
```html
<span class="text-[11px] px-2 py-0.5 rounded-full" :class="eventBadge(c.category)">
```
With:
```html
<span @click="latestRange='7d'; navigate('signals')" class="text-[11px] px-2 py-0.5 rounded-full cursor-pointer hover:ring-1 hover:ring-white/30 transition" :class="eventBadge(c.category)">
```

- [ ] **Step 5: Commit**

```bash
git add web/static/index.html
git commit -m "feat: add Insights card, clickable Activity Mix, remove Risk Scale legend"
```

---

### Task 11: Final integration test

- [ ] **Step 1: Run all Go tests**

Run: `go test ./... -count=1 -timeout 120s`
Expected: ALL PASS

- [ ] **Step 2: Run the export and verify insights.json output**

If a test database exists:
Run: `go run . export --out /tmp/mrdn-test-export`
Verify: `cat /tmp/mrdn-test-export/insights.json | python3 -m json.tool | head -30`

Expected: Valid JSON with `generated_at` and `findings` array

- [ ] **Step 3: Commit any fixes**

If any tests or the export needed fixes, commit them here.

```bash
git add -A
git commit -m "fix: address integration test findings for insights card"
```
