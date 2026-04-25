# Differentiation Features Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three differentiation features — Timeline proof cards, Network graph visualization, and Accountability Scoreboard — to make MRDN uniquely compelling.

**Architecture:** All three features build on existing data (insights, graph, compliance). No new data sources or ingestion. Feature A adds a new export + frontend component. Feature B adds a D3 force-graph wired to existing `/connections` data. Feature C adds a new scoring function + API endpoint + frontend view. All data served as static JSON via the export pipeline.

**Tech Stack:** Go (backend scoring/export), Alpine.js + Tailwind (frontend), D3.js (network graph), existing ECharts (timeline sparklines)

---

## File Structure

### Feature A: The Timeline
| File | Action | Responsibility |
|------|--------|----------------|
| `internal/insights/timeline.go` | Create | Build timeline proof data per finding (trade→event→price sequence) |
| `internal/insights/timeline_test.go` | Create | Unit tests for timeline builder |
| `internal/export/export.go` | Modify | Export `insights-timeline.json` alongside `insights.json` |
| `web/static/index.html` | Modify | Timeline card component in dashboard insights section |

### Feature B: The Network
| File | Action | Responsibility |
|------|--------|----------------|
| `web/static/index.html` | Modify | New "network" tab, D3 force-graph container, controls |
| `internal/export/export.go` | Modify | Export `network-overview.json` (precomputed top co-trader clusters) |
| `internal/db/graph.go` | Modify | Add `CoTraderGraph()` — full co-trader network query |
| `internal/db/graph_test.go` | Modify | Test for new query |

### Feature C: The Scoreboard
| File | Action | Responsibility |
|------|--------|----------------|
| `internal/score/accountability.go` | Create | Per-person accountability score (0-100) |
| `internal/score/accountability_test.go` | Create | Unit tests for scoring formula |
| `internal/db/compliance.go` | Modify | Add `AccountabilityInputs()` query |
| `internal/export/export.go` | Modify | Export `scoreboard.json` |
| `web/static/index.html` | Modify | Scoreboard view in persons tab or new tab |

---

## Task 1: Timeline Proof Data Builder

**Files:**
- Create: `internal/insights/timeline.go`
- Create: `internal/insights/timeline_test.go`

The timeline enriches each Finding with a sequence of dated proof points: the trade, the correlated event, and the price move (if available).

- [ ] **Step 1: Write the TimelineEntry struct and builder test**

```go
// internal/insights/timeline_test.go
package insights

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildTimeline_PreEvent(t *testing.T) {
	f := Finding{
		ID:   "pre_event-AAPL-20250601",
		Type: "pre_event",
		Data: mustJSON(map[string]any{
			"name":       "Nancy Pelosi",
			"slug":       "nancy-pelosi",
			"ticker":     "AAPL",
			"trade_type": "Purchase",
			"est_amount": 50000,
			"traded_at":  "2025-06-01",
			"event_type": "sec_litigation",
			"event_date": "2025-06-08",
			"days_gap":   7,
		}),
		Timestamp:   time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		RarityScore: 75,
	}

	tl := BuildTimeline(f)
	if len(tl) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(tl))
	}
	if tl[0].Kind != "trade" {
		t.Errorf("first entry should be trade, got %s", tl[0].Kind)
	}
	if tl[1].Kind != "event" {
		t.Errorf("second entry should be event, got %s", tl[1].Kind)
	}
	// Verify JSON-serializable
	if _, err := json.Marshal(tl); err != nil {
		t.Fatal(err)
	}
}

func TestBuildTimeline_Coordinated(t *testing.T) {
	f := Finding{
		ID:   "coordinated-NVDA-20250610",
		Type: "coordinated",
		Data: mustJSON(map[string]any{
			"ticker":     "NVDA",
			"reps":       []map[string]any{{"name": "A", "trade_type": "Purchase"}, {"name": "B", "trade_type": "Purchase"}},
			"week_start": "2025-06-09",
			"week_end":   "2025-06-13",
			"rep_count":  2,
		}),
		Timestamp:   time.Date(2025, 6, 9, 0, 0, 0, 0, time.UTC),
		RarityScore: 60,
	}

	tl := BuildTimeline(f)
	if len(tl) == 0 {
		t.Fatal("expected at least 1 entry")
	}
	if tl[0].Kind != "cluster" {
		t.Errorf("expected cluster, got %s", tl[0].Kind)
	}
}

func TestBuildTimeline_UnknownType(t *testing.T) {
	f := Finding{
		ID:        "unknown-test",
		Type:      "some_future_type",
		Data:      mustJSON(map[string]any{"foo": "bar"}),
		Timestamp: time.Now(),
	}
	tl := BuildTimeline(f)
	// Should return at least one generic entry, not panic
	if len(tl) == 0 {
		t.Fatal("expected fallback entry")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/insights/ -run TestBuildTimeline -v`
Expected: FAIL — `BuildTimeline` not defined

- [ ] **Step 3: Implement BuildTimeline**

```go
// internal/insights/timeline.go
package insights

import (
	"encoding/json"
	"fmt"
	"time"
)

// TimelineEntry is one point on a finding's proof timeline.
type TimelineEntry struct {
	Date  string `json:"date"`            // YYYY-MM-DD
	Kind  string `json:"kind"`            // trade, event, cluster, price_move
	Label string `json:"label"`           // human-readable
	Icon  string `json:"icon"`            // emoji for rendering
	Meta  map[string]any `json:"meta,omitempty"` // extra context
}

// EnrichedFinding is a Finding plus its timeline proof.
type EnrichedFinding struct {
	Finding
	Timeline []TimelineEntry `json:"timeline"`
}

// BuildTimeline extracts a chronological proof sequence from a Finding's data.
func BuildTimeline(f Finding) []TimelineEntry {
	var data map[string]any
	_ = json.Unmarshal(f.Data, &data)
	if data == nil {
		data = map[string]any{}
	}

	switch f.Type {
	case "pre_event":
		return buildPreEventTimeline(data)
	case "coordinated":
		return buildCoordinatedTimeline(data)
	case "round_trip":
		return buildRoundTripTimeline(data)
	case "committee_relevant":
		return buildCommitteeTimeline(data)
	case "lone_wolf":
		return buildLoneWolfTimeline(data)
	default:
		return []TimelineEntry{{
			Date:  f.Timestamp.Format("2006-01-02"),
			Kind:  "finding",
			Label: f.Headline,
			Icon:  "📌",
		}}
	}
}

func buildPreEventTimeline(d map[string]any) []TimelineEntry {
	var tl []TimelineEntry
	tradedAt, _ := d["traded_at"].(string)
	eventDate, _ := d["event_date"].(string)
	ticker, _ := d["ticker"].(string)
	name, _ := d["name"].(string)
	tradeType, _ := d["trade_type"].(string)
	eventType, _ := d["event_type"].(string)
	estAmt, _ := d["est_amount"].(float64)

	tl = append(tl, TimelineEntry{
		Date:  tradedAt,
		Kind:  "trade",
		Label: fmt.Sprintf("%s %s %s ($%s)", name, tradeType, ticker, fmtDollar(int64(estAmt))),
		Icon:  "💰",
		Meta:  map[string]any{"ticker": ticker, "person": name, "trade_type": tradeType},
	})
	tl = append(tl, TimelineEntry{
		Date:  eventDate,
		Kind:  "event",
		Label: fmt.Sprintf("%s — %s", ticker, prettyEventType(eventType)),
		Icon:  eventIcon(eventType),
		Meta:  map[string]any{"event_type": eventType},
	})
	return tl
}

func buildCoordinatedTimeline(d map[string]any) []TimelineEntry {
	var tl []TimelineEntry
	ticker, _ := d["ticker"].(string)
	weekStart, _ := d["week_start"].(string)
	weekEnd, _ := d["week_end"].(string)
	repCount := 0
	if reps, ok := d["reps"].([]any); ok {
		repCount = len(reps)
	}
	if rc, ok := d["rep_count"].(float64); ok && repCount == 0 {
		repCount = int(rc)
	}

	tl = append(tl, TimelineEntry{
		Date:  weekStart,
		Kind:  "cluster",
		Label: fmt.Sprintf("%d reps traded %s (%s to %s)", repCount, ticker, weekStart, weekEnd),
		Icon:  "🔗",
		Meta:  map[string]any{"ticker": ticker, "rep_count": repCount},
	})
	return tl
}

func buildRoundTripTimeline(d map[string]any) []TimelineEntry {
	var tl []TimelineEntry
	ticker, _ := d["ticker"].(string)
	name, _ := d["name"].(string)
	buyDate, _ := d["buy_date"].(string)
	sellDate, _ := d["sell_date"].(string)
	holdDays, _ := d["hold_days"].(float64)

	tl = append(tl, TimelineEntry{
		Date:  buyDate,
		Kind:  "trade",
		Label: fmt.Sprintf("%s bought %s", name, ticker),
		Icon:  "📈",
	})
	tl = append(tl, TimelineEntry{
		Date:  sellDate,
		Kind:  "trade",
		Label: fmt.Sprintf("%s sold %s (%dd hold)", name, ticker, int(holdDays)),
		Icon:  "📉",
	})
	return tl
}

func buildCommitteeTimeline(d map[string]any) []TimelineEntry {
	var tl []TimelineEntry
	ticker, _ := d["ticker"].(string)
	name, _ := d["name"].(string)
	committee, _ := d["committee"].(string)
	tradedAt, _ := d["traded_at"].(string)

	tl = append(tl, TimelineEntry{
		Date:  tradedAt,
		Kind:  "trade",
		Label: fmt.Sprintf("%s (on %s) traded %s", name, committee, ticker),
		Icon:  "🏛️",
		Meta:  map[string]any{"committee": committee},
	})
	return tl
}

func buildLoneWolfTimeline(d map[string]any) []TimelineEntry {
	var tl []TimelineEntry
	name, _ := d["name"].(string)
	ticker, _ := d["ticker"].(string)
	tradedAt, _ := d["traded_at"].(string)
	ratio, _ := d["ratio"].(float64)

	tl = append(tl, TimelineEntry{
		Date:  tradedAt,
		Kind:  "trade",
		Label: fmt.Sprintf("%s traded %s at %.0fx their median", name, ticker, ratio),
		Icon:  "🐺",
	})
	return tl
}

func fmtDollar(cents int64) string {
	if cents >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(cents)/1_000_000)
	}
	if cents >= 1_000 {
		return fmt.Sprintf("%dK", cents/1_000)
	}
	return fmt.Sprintf("%d", cents)
}

func eventIcon(eventType string) string {
	switch eventType {
	case "sec_litigation":
		return "⚖️"
	case "government_contract":
		return "📋"
	case "sanctions":
		return "🚫"
	case "regulatory_action":
		return "📜"
	case "tariff_action":
		return "🏷️"
	default:
		return "📰"
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/insights/ -run TestBuildTimeline -v`
Expected: PASS (all 3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/insights/timeline.go internal/insights/timeline_test.go
git commit -m "feat: add timeline proof builder for insight findings

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 2: Export Timeline-Enriched Insights

**Files:**
- Modify: `internal/export/export.go` (add enriched insights export)

- [ ] **Step 1: Read current export.go to find where insights.json is written**

Run: `grep -n "insights" internal/export/export.go`

- [ ] **Step 2: Add enriched insights export after existing insights export**

Find the block that writes `insights.json` and add an enriched version. After the `insights.json` write, add:

```go
// Enrich findings with timeline proofs
enriched := make([]insights.EnrichedFinding, len(output.Findings))
for i, f := range output.Findings {
	enriched[i] = insights.EnrichedFinding{
		Finding:  f,
		Timeline: insights.BuildTimeline(f),
	}
}
enrichedOutput := struct {
	GeneratedAt string                      `json:"generated_at"`
	Findings    []insights.EnrichedFinding  `json:"findings"`
}{
	GeneratedAt: output.GeneratedAt,
	Findings:    enriched,
}
if err := writeJSON(outDir, "insights.json", enrichedOutput); err != nil {
	return fmt.Errorf("writing insights.json: %w", err)
}
```

Replace the existing `insights.json` write with this enriched version so the same file now includes timeline data. No need for a separate file — the `timeline` field is additive.

- [ ] **Step 3: Build to verify**

Run: `go build ./cmd/mrdn`
Expected: clean build

- [ ] **Step 4: Commit**

```bash
git add internal/export/export.go
git commit -m "feat: enrich exported insights with timeline proof data

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 3: Timeline Cards in Frontend

**Files:**
- Modify: `web/static/index.html` (insights section, ~lines 373-428)

- [ ] **Step 1: Add timeline rendering to the insight hero card**

Replace the existing insight hero card's data rendering section. After the headline div, add a timeline strip. The key change: when a finding has a `timeline` array, render it as a horizontal sequence of dated proof points instead of the raw data dump.

Add this inside the hero card div (after the headline, replacing or augmenting the existing data display):

```html
<!-- Timeline proof strip -->
<div x-show="insightHero?.timeline?.length" class="flex items-center gap-0 mt-3 mb-2 overflow-x-auto">
  <template x-for="(step, i) in (insightHero?.timeline || [])" :key="i">
    <div class="flex items-center shrink-0">
      <!-- Connector line (not on first) -->
      <div x-show="i > 0" class="w-8 h-0.5 bg-accent/30"></div>
      <!-- Node -->
      <div class="flex flex-col items-center gap-1 px-2">
        <div class="w-8 h-8 rounded-full flex items-center justify-center text-sm"
          :class="step.kind === 'trade' ? 'bg-accent/20' : step.kind === 'event' ? 'bg-down/20' : 'bg-yellow-500/10'"
          x-text="step.icon"></div>
        <div class="text-[10px] text-neutral-400 whitespace-nowrap" x-text="step.date"></div>
        <div class="text-[10px] text-neutral-300 whitespace-nowrap max-w-[140px] truncate" x-text="step.label"></div>
      </div>
    </div>
  </template>
</div>
```

Keep the existing data fallback for findings without timelines.

- [ ] **Step 2: Test in browser**

Run: `source .env && go run ./cmd/mrdn export --out web/static/data && cd web/static && python -m http.server 8000`
Open: http://localhost:8000 — verify timeline dots appear on insight cards

- [ ] **Step 3: Commit**

```bash
git add web/static/index.html
git commit -m "feat: render timeline proof strips on insight cards

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 4: Accountability Score Calculator

**Files:**
- Create: `internal/score/accountability.go`
- Create: `internal/score/accountability_test.go`

- [ ] **Step 1: Write tests for the scoring formula**

```go
// internal/score/accountability_test.go
package score

import (
	"testing"
)

func TestAccountabilityScore_PerfectRecord(t *testing.T) {
	input := AccountabilityInput{
		MedianLatencyDays: 10,
		LatePct:           0.0,
		CommitteeTradeRatio: 0.0,
		RoundTripCount:    0,
		PreEventCount:     0,
		TradeCount:        50,
	}
	s := AccountabilityScore(input)
	if s > 15 {
		t.Errorf("perfect record should score low risk, got %d", s)
	}
}

func TestAccountabilityScore_WorstCase(t *testing.T) {
	input := AccountabilityInput{
		MedianLatencyDays: 120,
		LatePct:           0.8,
		CommitteeTradeRatio: 0.6,
		RoundTripCount:    15,
		PreEventCount:     10,
		TradeCount:        100,
	}
	s := AccountabilityScore(input)
	if s < 80 {
		t.Errorf("worst case should score high risk, got %d", s)
	}
}

func TestAccountabilityScore_Clamped(t *testing.T) {
	input := AccountabilityInput{
		MedianLatencyDays: 500,
		LatePct:           1.0,
		CommitteeTradeRatio: 1.0,
		RoundTripCount:    100,
		PreEventCount:     100,
		TradeCount:        1000,
	}
	s := AccountabilityScore(input)
	if s > 100 {
		t.Errorf("score should be clamped to 100, got %d", s)
	}
}

func TestAccountabilityScore_NoTrades(t *testing.T) {
	input := AccountabilityInput{TradeCount: 0}
	s := AccountabilityScore(input)
	if s != 0 {
		t.Errorf("no trades should score 0, got %d", s)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/score/ -run TestAccountabilityScore -v`
Expected: FAIL — `AccountabilityScore` not defined

- [ ] **Step 3: Implement the scoring function**

```go
// internal/score/accountability.go
package score

import "math"

// AccountabilityInput holds the raw metrics for one politician.
type AccountabilityInput struct {
	MedianLatencyDays   int     // STOCK Act filing delay (median days)
	LatePct             float64 // fraction of trades filed >45 days late
	CommitteeTradeRatio float64 // fraction of trades in own committee's sectors
	RoundTripCount      int     // number of short-hold round-trip trades
	PreEventCount       int     // trades within 14 days before corp events
	TradeCount          int     // total trades (used as denominator)
}

// AccountabilityScore returns 0-100 where higher = more concerning behavior.
//
// Weights:
//   30% filing latency (STOCK Act compliance)
//   25% committee-aligned trading (jurisdiction conflict)
//   20% round-trip frequency (suspicious speed)
//   15% pre-event timing (advance knowledge signal)
//   10% late filing percentage (chronic non-compliance)
func AccountabilityScore(in AccountabilityInput) int {
	if in.TradeCount == 0 {
		return 0
	}

	// Filing latency: 0-100 (45 days = threshold, 120+ = max)
	latency := clamp100(float64(in.MedianLatencyDays-20) / 100.0 * 100)

	// Late pct: direct 0-100
	late := clamp100(in.LatePct * 100)

	// Committee ratio: direct 0-100 (0.3+ is notable)
	committee := clamp100(in.CommitteeTradeRatio / 0.5 * 100)

	// Round trips: log-scaled, 1 = 30, 5 = 70, 10+ = 90+
	rt := 0.0
	if in.RoundTripCount > 0 {
		rt = clamp100(30 + 40*math.Log2(float64(in.RoundTripCount)))
	}

	// Pre-event: log-scaled similar to round trips
	pe := 0.0
	if in.PreEventCount > 0 {
		pe = clamp100(30 + 45*math.Log2(float64(in.PreEventCount)))
	}

	composite := 0.30*latency + 0.10*late + 0.25*committee + 0.20*rt + 0.15*pe
	return int(math.Round(clamp100(composite)))
}

func clamp100(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/score/ -run TestAccountabilityScore -v`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/score/accountability.go internal/score/accountability_test.go
git commit -m "feat: add per-politician accountability score (0-100)

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 5: Accountability Data Query

**Files:**
- Modify: `internal/db/compliance.go` (add AccountabilityInputs query)

- [ ] **Step 1: Read existing compliance.go to understand patterns**

Run: `head -60 internal/db/compliance.go`

- [ ] **Step 2: Add AccountabilityInputs query**

Add to `internal/db/compliance.go`:

```go
// AccountabilityRow holds the raw inputs for one person's accountability score.
type AccountabilityRow struct {
	PersonID            int     `json:"person_id"`
	Slug                string  `json:"slug"`
	Name                string  `json:"name"`
	Party               *string `json:"party,omitempty"`
	State               *string `json:"state,omitempty"`
	TradeCount          int     `json:"trade_count"`
	MedianLatencyDays   int     `json:"median_latency_days"`
	LatePct             float64 `json:"late_pct"`
	CommitteeTradeCount int     `json:"committee_trade_count"`
	RoundTripCount      int     `json:"round_trip_count"`
	PreEventCount       int     `json:"pre_event_count"`
}

// AccountabilityInputs returns raw accountability metrics for all persons
// with at least minTrades trades. Each row contains the inputs needed to
// compute an accountability score.
func (s *Store) AccountabilityInputs(ctx context.Context, minTrades int) ([]AccountabilityRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH trade_counts AS (
			SELECT person_id, COUNT(*) as cnt
			FROM congressional_trades
			GROUP BY person_id
			HAVING cnt >= ?
		),
		latency AS (
			SELECT person_id,
				CAST(julianday(filed_at) - julianday(traded_at) AS INTEGER) AS days
			FROM congressional_trades
			WHERE filed_at IS NOT NULL AND traded_at IS NOT NULL
		),
		latency_agg AS (
			SELECT l.person_id,
				COUNT(*) as scoreable,
				SUM(CASE WHEN l.days > 45 THEN 1 ELSE 0 END) as late_count
			FROM latency l
			JOIN trade_counts tc ON tc.person_id = l.person_id
			GROUP BY l.person_id
		),
		committee_trades AS (
			SELECT ct.person_id, COUNT(*) as cnt
			FROM congressional_trades ct
			JOIN person_committees pc ON pc.person_id = ct.person_id
			JOIN companies c ON c.ticker = ct.ticker
			WHERE (
				(pc.committee LIKE '%Armed%' AND c.sector IN ('Industrials', 'Defense'))
				OR (pc.committee LIKE '%Banking%' AND c.sector = 'Financials')
				OR (pc.committee LIKE '%Energy%' AND c.sector IN ('Energy', 'Utilities'))
				OR (pc.committee LIKE '%Health%' AND c.sector = 'Health Care')
				OR (pc.committee LIKE '%Commerce%' AND c.sector IN ('Technology', 'Communication Services'))
			)
			GROUP BY ct.person_id
		),
		round_trips AS (
			SELECT buy.person_id, COUNT(*) as cnt
			FROM congressional_trades buy
			JOIN congressional_trades sell
				ON buy.person_id = sell.person_id
				AND buy.ticker = sell.ticker
				AND buy.trade_type IN ('Purchase', 'purchase')
				AND sell.trade_type IN ('Sale (Full)', 'Sale (Partial)', 'sale_full', 'sale_partial', 'Sale')
				AND julianday(sell.traded_at) - julianday(buy.traded_at) BETWEEN 1 AND 60
			GROUP BY buy.person_id
		),
		pre_events AS (
			SELECT ct.person_id, COUNT(DISTINCT ct.id) as cnt
			FROM congressional_trades ct
			JOIN companies c ON c.ticker = ct.ticker
			JOIN events e ON e.company_id = c.id
			WHERE ct.traded_at IS NOT NULL
				AND e.event_type IN ('sec_litigation', 'government_contract', 'sanctions', 'regulatory_action', 'tariff_action')
				AND julianday(e.occurred_at) - julianday(ct.traded_at) BETWEEN 1 AND 14
			GROUP BY ct.person_id
		)
		SELECT p.id, p.slug, p.name, p.party, p.state,
			tc.cnt,
			COALESCE(la.scoreable, 0),
			COALESCE(la.late_count, 0),
			COALESCE(cmt.cnt, 0),
			COALESCE(rt.cnt, 0),
			COALESCE(pe.cnt, 0)
		FROM persons p
		JOIN trade_counts tc ON tc.person_id = p.id
		LEFT JOIN latency_agg la ON la.person_id = p.id
		LEFT JOIN committee_trades cmt ON cmt.person_id = p.id
		LEFT JOIN round_trips rt ON rt.person_id = p.id
		LEFT JOIN pre_events pe ON pe.person_id = p.id
		ORDER BY tc.cnt DESC
	`, minTrades)
	if err != nil {
		return nil, fmt.Errorf("accountability inputs: %w", err)
	}
	defer rows.Close()

	var result []AccountabilityRow
	for rows.Next() {
		var r AccountabilityRow
		var scoreable, lateCount int
		if err := rows.Scan(&r.PersonID, &r.Slug, &r.Name, &r.Party, &r.State,
			&r.TradeCount, &scoreable, &lateCount,
			&r.CommitteeTradeCount, &r.RoundTripCount, &r.PreEventCount,
		); err != nil {
			return nil, err
		}
		// Compute median latency in Go (reuse LatencyLeaderboard pattern if needed)
		// For now use late_count / scoreable as approximation
		if scoreable > 0 {
			r.LatePct = float64(lateCount) / float64(scoreable)
		}
		if r.TradeCount > 0 {
			r.CommitteeTradeCount = r.CommitteeTradeCount // raw count; ratio computed in scoring
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
```

- [ ] **Step 3: Build to verify**

Run: `go build ./cmd/mrdn`
Expected: clean build

- [ ] **Step 4: Commit**

```bash
git add internal/db/compliance.go
git commit -m "feat: add AccountabilityInputs query for scoreboard

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 6: Export Scoreboard JSON

**Files:**
- Modify: `internal/export/export.go` (add scoreboard export)

- [ ] **Step 1: Read export.go to find the signals section**

Run: `grep -n "signals\|latency\|swarm" internal/export/export.go`

- [ ] **Step 2: Add scoreboard export after the signals block**

```go
// Accountability scoreboard
accRows, err := store.AccountabilityInputs(ctx, 5)
if err != nil {
	log.Printf("export: accountability inputs: %v", err)
} else {
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
	entries := make([]ScoreboardEntry, 0, len(accRows))
	for _, r := range accRows {
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
			Score:               s,
			TradeCount:          r.TradeCount,
			MedianLatencyDays:   r.MedianLatencyDays,
			LatePct:             r.LatePct,
			CommitteeTradeCount: r.CommitteeTradeCount,
			RoundTripCount:      r.RoundTripCount,
			PreEventCount:       r.PreEventCount,
		})
	}
	// Sort by score descending
	sort.Slice(entries, func(i, j int) bool { return entries[i].Score > entries[j].Score })
	if err := writeJSON(outDir, "scoreboard.json", entries); err != nil {
		return fmt.Errorf("writing scoreboard.json: %w", err)
	}
}
```

Add the `score` import: `"github.com/arclighteng/mrdn/internal/score"`

- [ ] **Step 3: Build and test export**

Run: `go build ./cmd/mrdn && source .env && go run ./cmd/mrdn export --out /tmp/mrdn-test`
Run: `cat /tmp/mrdn-test/scoreboard.json | head -c 500`
Expected: JSON array of ScoreboardEntry objects sorted by score desc

- [ ] **Step 4: Commit**

```bash
git add internal/export/export.go
git commit -m "feat: export accountability scoreboard as scoreboard.json

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 7: Co-Trader Network Graph Export

**Files:**
- Modify: `internal/db/graph.go` (add CoTraderNetwork query)
- Modify: `internal/export/export.go` (export network JSON)

- [ ] **Step 1: Read graph.go to understand existing BFS patterns**

Run: `head -80 internal/db/graph.go`

- [ ] **Step 2: Add CoTraderNetwork query to graph.go**

This returns a precomputed network of persons who traded the same tickers within 14-day windows, with edge weights = shared ticker count.

```go
// CoTraderNetwork returns a graph of persons connected by shared trading activity.
// An edge exists between two persons if they traded the same ticker within windowDays
// of each other. Edge weight = number of shared ticker-week overlaps.
func (s *Store) CoTraderNetwork(ctx context.Context, minOverlaps int) (*GraphResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT a.person_id, b.person_id, COUNT(DISTINCT a.ticker) as shared_tickers
		FROM congressional_trades a
		JOIN congressional_trades b
			ON a.ticker = b.ticker
			AND a.person_id < b.person_id
			AND ABS(julianday(a.traded_at) - julianday(b.traded_at)) <= 14
		WHERE a.traded_at IS NOT NULL AND b.traded_at IS NOT NULL
		GROUP BY a.person_id, b.person_id
		HAVING shared_tickers >= ?
		ORDER BY shared_tickers DESC
		LIMIT 500
	`, minOverlaps)
	if err != nil {
		return nil, fmt.Errorf("co-trader network: %w", err)
	}
	defer rows.Close()

	nodeSet := map[int]bool{}
	var edges []GraphEdge
	for rows.Next() {
		var a, b, weight int
		if err := rows.Scan(&a, &b, &weight); err != nil {
			return nil, err
		}
		nodeSet[a] = true
		nodeSet[b] = true
		edges = append(edges, GraphEdge{
			From:         a,
			FromType:     "person",
			To:           b,
			ToType:       "person",
			Relationship: fmt.Sprintf("co-traded %d tickers", weight),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Hydrate nodes
	var nodes []GraphNode
	for id := range nodeSet {
		var name, slug string
		var party *string
		err := s.db.QueryRowContext(ctx,
			`SELECT name, slug, party FROM persons WHERE id = ?`, id,
		).Scan(&name, &slug, &party)
		if err != nil {
			continue
		}
		label := name
		if party != nil {
			label = name + " (" + *party + ")"
		}
		nodes = append(nodes, GraphNode{
			ID:    id,
			Type:  "person",
			Label: label,
			Slug:  slug,
		})
	}

	return &GraphResult{Nodes: nodes, Edges: edges}, nil
}
```

- [ ] **Step 3: Add network export to export.go**

After the scoreboard export, add:

```go
// Co-trader network
network, err := store.CoTraderNetwork(ctx, 2)
if err != nil {
	log.Printf("export: co-trader network: %v", err)
} else {
	if err := writeJSON(outDir, "network.json", network); err != nil {
		return fmt.Errorf("writing network.json: %w", err)
	}
}
```

- [ ] **Step 4: Build and verify**

Run: `go build ./cmd/mrdn`
Expected: clean build

- [ ] **Step 5: Commit**

```bash
git add internal/db/graph.go internal/export/export.go
git commit -m "feat: export co-trader network graph for visualization

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 8: Network Graph Visualization (Frontend)

**Files:**
- Modify: `web/static/index.html` (add network tab + D3 force graph)

- [ ] **Step 1: Add D3.js script tag to head**

In the `<head>` section, after the ECharts script tag, add:

```html
<script src="https://cdn.jsdelivr.net/npm/d3@7/dist/d3.min.js"></script>
```

- [ ] **Step 2: Add "network" to the navigation tabs**

Update the tabs array from:
```js
['dashboard','rankings','companies','persons','tickers','signals','status']
```
to:
```js
['dashboard','rankings','companies','persons','network','tickers','signals','status']
```

Do this in both the desktop tabs `template x-for` and the mobile `select` `template x-for`.

- [ ] **Step 3: Add network view data properties**

In the `app()` function's return object, add:

```js
networkData: null,
networkLoading: false,
```

- [ ] **Step 4: Add data fetching**

In the `navigate()` method or in the init logic, add a fetch for network data when the network tab is selected:

```js
async fetchNetwork() {
  if (this.networkData) return;
  this.networkLoading = true;
  const res = await this.api('/network');
  this.networkData = res;
  this.networkLoading = false;
  this.$nextTick(() => this.renderNetwork());
},
```

Add a call to `this.fetchNetwork()` in the navigate method when `view === 'network'`.

Also add to the `apiPathToFile` static map:
```js
'network': 'network.json',
```

- [ ] **Step 5: Add the network view HTML**

After the persons view section and before the tickers section, add:

```html
<!-- Network View -->
<div x-show="view === 'network'" x-cloak class="space-y-4">
  <div class="flex items-center justify-between">
    <div>
      <h2 class="text-xl font-bold text-white">Co-Trader Network</h2>
      <p class="text-sm text-neutral-400 mt-1">Representatives connected by shared trading activity within 14-day windows. Thicker lines = more tickers traded in common.</p>
    </div>
  </div>
  <div class="bg-surface-1 rounded-xl border border-white/15 p-4 relative" style="min-height:600px">
    <div x-show="networkLoading" class="absolute inset-0 flex items-center justify-center">
      <span class="text-neutral-400 text-sm">Loading network...</span>
    </div>
    <svg id="network-svg" width="100%" height="600" x-show="networkData && !networkLoading"></svg>
  </div>
</div>
```

- [ ] **Step 6: Add D3 force-graph renderer**

In the `app()` methods, add:

```js
renderNetwork() {
  const data = this.networkData;
  if (!data || !data.nodes?.length) return;

  const svg = d3.select('#network-svg');
  svg.selectAll('*').remove();

  const width = svg.node().getBoundingClientRect().width;
  const height = 600;

  // Build id→index map
  const nodeMap = new Map(data.nodes.map((n, i) => [n.id, i]));

  const links = data.edges
    .filter(e => nodeMap.has(e.from) && nodeMap.has(e.to))
    .map(e => ({
      source: nodeMap.get(e.from),
      target: nodeMap.get(e.to),
      label: e.relationship
    }));

  const nodes = data.nodes.map(n => ({...n}));

  const sim = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(links).distance(100))
    .force('charge', d3.forceManyBody().strength(-200))
    .force('center', d3.forceCenter(width / 2, height / 2))
    .force('collision', d3.forceCollide(30));

  const link = svg.append('g')
    .selectAll('line')
    .data(links)
    .enter().append('line')
    .attr('stroke', 'rgba(99,102,241,0.3)')
    .attr('stroke-width', 1.5);

  const node = svg.append('g')
    .selectAll('g')
    .data(nodes)
    .enter().append('g')
    .call(d3.drag()
      .on('start', (e, d) => { if (!e.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
      .on('drag', (e, d) => { d.fx = e.x; d.fy = e.y; })
      .on('end', (e, d) => { if (!e.active) sim.alphaTarget(0); d.fx = null; d.fy = null; })
    );

  node.append('circle')
    .attr('r', 6)
    .attr('fill', d => d.label?.includes('(R)') ? '#f87171' : d.label?.includes('(D)') ? '#60a5fa' : '#a78bfa');

  node.append('text')
    .text(d => d.label?.replace(/ \([RDI]\)$/, '') || '')
    .attr('dx', 10).attr('dy', 4)
    .attr('font-size', '10px')
    .attr('fill', '#9ca3af');

  sim.on('tick', () => {
    link
      .attr('x1', d => d.source.x).attr('y1', d => d.source.y)
      .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
    node.attr('transform', d => `translate(${d.x},${d.y})`);
  });
},
```

- [ ] **Step 7: Test in browser**

Run export, serve, open browser. Verify:
- Network tab appears in nav
- D3 graph renders with person nodes
- Nodes are draggable
- Party colors (red=R, blue=D, purple=other)
- Names visible next to nodes

- [ ] **Step 8: Commit**

```bash
git add web/static/index.html
git commit -m "feat: add interactive co-trader network graph visualization

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 9: Scoreboard View (Frontend)

**Files:**
- Modify: `web/static/index.html` (add scoreboard to persons view)

- [ ] **Step 1: Add scoreboard data properties**

In the `app()` return, add:

```js
scoreboard: [],
scoreboardSort: 'score',
scoreboardLoading: false,
```

- [ ] **Step 2: Add fetch and apiPathToFile mapping**

Add to `apiPathToFile` static map:
```js
'scoreboard': 'scoreboard.json',
```

Add fetch method:
```js
async fetchScoreboard() {
  if (this.scoreboard.length) return;
  this.scoreboardLoading = true;
  const res = await this.api('/scoreboard');
  this.scoreboard = res || [];
  this.scoreboardLoading = false;
},
```

Call `this.fetchScoreboard()` when navigating to the persons view.

- [ ] **Step 3: Add scoreboard toggle to persons view header**

At the top of the persons section, add a toggle between "Roster" and "Scoreboard" views:

```html
<div class="flex gap-2">
  <button @click="personsMode='roster'" :class="personsMode==='roster' ? 'bg-surface-3 text-white' : 'text-neutral-400 hover:text-white'" class="px-3 py-1 rounded-md text-sm">Roster</button>
  <button @click="personsMode='scoreboard'; fetchScoreboard()" :class="personsMode==='scoreboard' ? 'bg-surface-3 text-white' : 'text-neutral-400 hover:text-white'" class="px-3 py-1 rounded-md text-sm">Scoreboard</button>
</div>
```

Add `personsMode: 'roster'` to the app data.

- [ ] **Step 4: Add scoreboard table**

After the existing persons roster content, add (wrapped in `x-show="personsMode==='scoreboard'"`):

```html
<div x-show="personsMode==='scoreboard'" class="bg-surface-1 rounded-xl border border-white/15 overflow-hidden">
  <table class="w-full text-sm">
    <thead>
      <tr class="border-b border-white/10 text-neutral-400 text-xs uppercase tracking-wider">
        <th class="text-left py-3 px-4">#</th>
        <th class="text-left py-3 px-4">Name</th>
        <th class="text-center py-3 px-2">Party</th>
        <th class="text-right py-3 px-4 cursor-pointer hover:text-white" @click="scoreboardSort='score'; scoreboard.sort((a,b)=>b.score-a.score)">Score</th>
        <th class="text-right py-3 px-4 cursor-pointer hover:text-white" @click="scoreboardSort='trades'; scoreboard.sort((a,b)=>b.trade_count-a.trade_count)">Trades</th>
        <th class="text-right py-3 px-4 cursor-pointer hover:text-white" @click="scoreboardSort='latency'; scoreboard.sort((a,b)=>b.median_latency_days-a.median_latency_days)">Filing Lag</th>
        <th class="text-right py-3 px-4 cursor-pointer hover:text-white" @click="scoreboardSort='committee'; scoreboard.sort((a,b)=>b.committee_trades-a.committee_trades)">Committee</th>
        <th class="text-right py-3 px-4 cursor-pointer hover:text-white" @click="scoreboardSort='roundtrips'; scoreboard.sort((a,b)=>b.round_trips-a.round_trips)">Round-trips</th>
        <th class="text-right py-3 px-4 cursor-pointer hover:text-white" @click="scoreboardSort='preevent'; scoreboard.sort((a,b)=>b.pre_event_trades-a.pre_event_trades)">Pre-event</th>
      </tr>
    </thead>
    <tbody>
      <template x-for="(r, i) in scoreboard.slice(0, 50)" :key="r.slug">
        <tr class="border-b border-white/5 hover:bg-surface-2 cursor-pointer" @click="openPerson(r.slug)">
          <td class="py-2 px-4 text-neutral-500 tabular-nums" x-text="i + 1"></td>
          <td class="py-2 px-4">
            <span class="text-neutral-200 font-medium" x-text="r.name"></span>
            <span class="text-neutral-500 text-xs ml-1" x-text="r.state || ''"></span>
          </td>
          <td class="py-2 px-2 text-center">
            <span class="text-xs px-1.5 py-0.5 rounded"
              :class="r.party === 'R' ? 'bg-red-500/20 text-red-400' : r.party === 'D' ? 'bg-blue-500/20 text-blue-400' : 'bg-neutral-500/20 text-neutral-400'"
              x-text="r.party || '?'"></span>
          </td>
          <td class="py-2 px-4 text-right tabular-nums">
            <span class="font-bold" :class="r.score >= 70 ? 'text-red-400' : r.score >= 40 ? 'text-yellow-400' : 'text-green-400'" x-text="r.score"></span>
          </td>
          <td class="py-2 px-4 text-right tabular-nums text-neutral-300" x-text="r.trade_count"></td>
          <td class="py-2 px-4 text-right tabular-nums" :class="r.median_latency_days > 45 ? 'text-red-400' : 'text-neutral-300'" x-text="r.median_latency_days + 'd'"></td>
          <td class="py-2 px-4 text-right tabular-nums" :class="r.committee_trades > 5 ? 'text-yellow-400' : 'text-neutral-400'" x-text="r.committee_trades"></td>
          <td class="py-2 px-4 text-right tabular-nums" :class="r.round_trips > 3 ? 'text-red-300' : 'text-neutral-400'" x-text="r.round_trips"></td>
          <td class="py-2 px-4 text-right tabular-nums" :class="r.pre_event_trades > 2 ? 'text-red-300' : 'text-neutral-400'" x-text="r.pre_event_trades"></td>
        </tr>
      </template>
    </tbody>
  </table>
</div>
```

- [ ] **Step 5: Test in browser**

Run export + serve. Verify:
- Persons page has Roster / Scoreboard toggle
- Scoreboard shows ranked politicians with color-coded scores
- Columns are sortable
- Clicking a row navigates to person detail
- Score colors: green (0-39), yellow (40-69), red (70-100)

- [ ] **Step 6: Commit**

```bash
git add web/static/index.html
git commit -m "feat: add accountability scoreboard view to persons page

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 10: Integration Test — Full Export + Verify

- [ ] **Step 1: Run full export**

```bash
source .env && go run ./cmd/mrdn export --out web/static/data
```

- [ ] **Step 2: Verify all new files exist**

```bash
ls -la web/static/data/insights.json web/static/data/scoreboard.json web/static/data/network.json
```

- [ ] **Step 3: Verify insights have timeline field**

```bash
cat web/static/data/insights.json | python -c "import json,sys; d=json.load(sys.stdin); print(len(d['findings']), 'findings'); print('timeline' in d['findings'][0] if d['findings'] else 'no findings')"
```
Expected: `N findings` and `True`

- [ ] **Step 4: Verify scoreboard is sorted**

```bash
cat web/static/data/scoreboard.json | python -c "import json,sys; d=json.load(sys.stdin); print(len(d), 'entries'); print('top:', d[0]['name'], d[0]['score'] if d else 'empty')"
```

- [ ] **Step 5: Verify network has nodes and edges**

```bash
cat web/static/data/network.json | python -c "import json,sys; d=json.load(sys.stdin); print(len(d.get('nodes',[])), 'nodes', len(d.get('edges',[])), 'edges')"
```

- [ ] **Step 6: Serve and visual check**

```bash
cd web/static && python -m http.server 8000
```

Open http://localhost:8000 and verify:
- Dashboard: insight cards show timeline proof strips
- Persons > Scoreboard: ranked table with color-coded scores
- Network tab: interactive D3 force graph with draggable nodes

- [ ] **Step 7: Final commit**

```bash
git add -A
git commit -m "feat: add three differentiation features — timeline proofs, co-trader network, accountability scoreboard

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Review Fixes (Apply During Implementation)

The following issues were caught during plan review. Apply these corrections when implementing the corresponding tasks.

### M1 — `writeJSON` takes 1 path arg, not (dir, file) (Tasks 2, 6, 7)
The actual signature is `writeJSON(path string, data any)`. All export calls must use `filepath.Join(outDir, "filename.json")`:
```go
// WRONG:  writeJSON(outDir, "insights.json", enrichedOutput)
// RIGHT:  writeJSON(filepath.Join(outDir, "insights.json"), enrichedOutput)
```

### M2 — Don't redefine `prettyEventType` in timeline.go (Task 1)
`prettyEventType` is already defined in `internal/insights/preevent.go`. Both files share `package insights`. Remove the `prettyEventType` function from `timeline.go` — it's already accessible.

### M3 — Column is `committee_name`, not `committee` (Task 5)
The `person_committees` table uses `committee_name TEXT NOT NULL`. Replace all `pc.committee LIKE '...'` with `pc.committee_name LIKE '...'` in the AccountabilityInputs SQL.

### M4 — `MedianLatencyDays` is never populated (Task 5)
The `latency_agg` CTE only computes `scoreable` and `late_count` but never computes a median. Fix by collecting all latency days via `GROUP_CONCAT` in the CTE, parsing the CSV string in Go, and computing the median (reuse the pattern from `LatencyLeaderboard` at `internal/db/compliance.go:66-151`). Without this fix, the latency component (30% of the score) is always 0.

### M5 — D3 SVG width is 0 when hidden (Task 8)
`getBoundingClientRect().width` returns 0 on a just-shown SVG. Fix:
```js
// WRONG:  const width = svg.node().getBoundingClientRect().width;
// RIGHT:  const width = svg.node().closest('.bg-surface-1')?.clientWidth - 32 || 900;
```

### S1 — lone_wolf and committee_relevant use `"date"` not `"traded_at"` (Task 1)
In `buildLoneWolfTimeline`: read `d["date"]` not `d["traded_at"]`.
In `buildCommitteeTimeline`: read `d["date"]` not `d["traded_at"]`.

### S2 — CoTraderNetwork N+1 query (Task 7)
Replace per-node `QueryRowContext` loop with a single `WHERE id IN (?, ?, ...)` batch query.

### S3 — CoTraderNetwork needs date filter (Task 7)
Add `WHERE a.traded_at >= date('now', '-24 months')` to the self-join to bound scan size.
