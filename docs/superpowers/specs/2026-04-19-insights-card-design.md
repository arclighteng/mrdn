# Insights Card — Design Spec

## Summary

A non-deterministic, non-partisan "Insights" card on the dashboard that surfaces statistically unusual data patterns — coordinated trades, suspicious timing, outlier behavior — without making opinionated claims. Replaces the Risk Scale legend.

## Layout

Horizontal strip at the top of the dashboard (where Risk Scale legend currently sits). Full-width, single card with:

- **Hero finding (left, ~60% width):** Accent left-border, B-style headline (curious/pattern-naming, e.g. "Unusual cluster: 4 Banking Committee members exited SIVB in 3 days"), mini data table showing the underlying records (names, amounts, dates), "+N more" truncation if needed.
- **Secondary findings (right, ~40% width):** Two compact cards stacked vertically. Each shows: pattern type label, one-line headline, rarity score, and age.

All three findings are clickable — they navigate to the existing view that generated the data, with filters pre-applied.

**Degenerate cases:** If 0 findings, hide the Insights card entirely and show nothing in that slot. If 1 finding, show hero only (no secondaries). If 2 findings, show hero + 1 secondary.

**Fallback on missing data:** If `data/insights.json` is absent (404) or unparseable, hide the Insights card. The Risk Scale legend is not preserved — it moves to a tooltip on Rankings regardless.

## Pattern Detectors (6 types)

Each detector runs during the export pipeline and produces scored findings.

### 1. Coordinated Trades
- **Signal:** N representatives trading the same ticker within the same calendar week (matches existing `SwarmDetector` bucket granularity)
- **Data:** `congressional_trades` grouped by ticker + ISO week (reuses `SwarmDetector` query logic from `internal/db/signals.go`)
- **Rarity scoring:** Higher with more reps, same trade direction. Minimum 3 reps to qualify. Score: `min(100, 40 + (rep_count - 3) × 15 + (same_direction_bonus × 10))`
- **Clickthrough:** `{"view": "signals", "tab": "swarms", "search": "SIVB"}`
- **Relationship to Detector 5:** Detector 1 looks for *specific notable instances* within swarm data. Detector 5 surfaces tickers with abnormally high overall swarm counts across multiple weeks. A ticker can appear in both if it has a single extreme week (Detector 1) AND high aggregate coordination (Detector 5).

### 2. Committee-Relevant Trades
- **Signal:** Representative on a committee trades a stock in a sector mapped to that committee's jurisdiction
- **Data:** `congressional_trades` JOIN `person_committees` ON person slug, filtered by a hard-coded `committeeToSectors` mapping (see below)
- **Committee→Sector mapping:** Hard-coded Go map, e.g.:
  - "Armed Services" → ["Aerospace & Defense", "Industrials"]
  - "Banking" / "Financial Services" → ["Financials", "Banks"]
  - "Energy and Commerce" / "Energy and Natural Resources" → ["Energy", "Utilities"]
  - "Agriculture" → ["Consumer Staples", "Materials"]
  - "Health" / "HELP" → ["Health Care"]
  - "Commerce, Science, and Transportation" → ["Technology", "Communication Services"]
  - Initial mapping covers ~10 major committees. Unmapped committees produce no findings.
- **Rarity scoring:** Higher when trade is large and timing overlaps with a committee hearing or related event. Score: `min(100, 50 + amount_factor × 20 + timing_factor × 30)` where `amount_factor` scales with trade size bucket and `timing_factor` rewards proximity to committee activity dates (if available from events, else omitted)
- **Clickthrough:** `{"view": "person", "slug": "nancy-pelosi"}`

### 3. Pre-Event Timing
- **Signal:** Congressional trade occurs within 14 days before a significant event on the same company
- **Data:** Three-table join: `congressional_trades.ticker` → `companies.ticker` → `companies.id` → `events.company_id`. Filter: `events.occurred_at` BETWEEN `trade.traded_at` AND `trade.traded_at + 14 days`. Event types considered significant: `sec_litigation`, `government_contract`, `sanctions`, `regulatory_action`, `tariff_action`.
- **Rarity scoring:** Shorter gap = higher score. Score: `min(100, 60 + (14 - days_gap) × 3 + amount_factor × 5)`. Minimum gap of 1 day (same-day trades are likely reactive, not predictive).
- **Clickthrough:** `{"view": "company", "ticker": "LMT"}`

### 4. Round-Trip Anomalies
- **Signal:** Unusually fast buy→sell or sell→buy cycles
- **Data:** Existing `RoundTrips` query from `internal/db/tickers.go`
- **Rarity scoring:** Based on hold duration relative to a per-person median hold period (new sub-query: `SELECT person_slug, median(hold_days) FROM round_trips GROUP BY person_slug`). Score: `min(100, 50 + deviation_factor × 25 + amount_factor × 25)` where `deviation_factor = max(0, (median_hold - actual_hold) / median_hold)`. Minimum 5 historical round-trips for a person to qualify (prevents noisy scores on sparse data).
- **Clickthrough:** `{"view": "signals", "tab": "round-trips", "search": "pelosi"}`
- **`timestamp`:** The sell date (trade completion).

### 5. Swarm Outliers
- **Signal:** Ticker with abnormally high aggregate coordinated-trading count across multiple weeks
- **Data:** Existing `SwarmDetector` query, aggregated across all weeks
- **Rarity scoring:** Use total rep count across all swarm weeks and number of distinct weeks with swarm activity. Score: `min(100, 30 + total_reps × 5 + distinct_weeks × 10)`. Differs from Detector 1 which scores individual week-level clusters.
- **Clickthrough:** `{"view": "signals", "tab": "swarms", "search": "TSLA"}`
- **`timestamp`:** The most recent swarm week's end date.

### 6. Lone Wolf
- **Signal:** Single representative making a trade far larger than their usual pattern
- **Data:** New query: for each person with ≥5 trades, compute median trade amount (using `est_amount` midpoint). Flag trades where `trade_amount / median_amount ≥ 4` (i.e., 4× or more their typical size).
- **Rarity scoring:** Score: `min(100, 50 + min(50, (ratio - 4) × 10))` where `ratio = trade_amount / person_median`. A trade 4× baseline scores 50; 9× scores 100.
- **Clickthrough:** `{"view": "person", "slug": "tommy-tuberville"}`
- **`timestamp`:** The trade date.

## Selection Algorithm

### Pre-computation (backend, during export)
1. Run all 6 detectors (each with a 30-second timeout; if a detector times out, log a warning and skip it)
2. Score each finding with a `rarity_score` (0–100)
3. Record `timestamp` (see per-detector definition above for what this means)
4. Keep top 20 by rarity score
5. Write to `{outDir}/insights.json`

### Display selection (frontend, each page load)
1. If 0 findings: hide card. If 1: hero only. If 2: hero + 1 secondary.
2. **Hero:** Always `argmax(findings, rarity_score)` — the strangest thing never gets hidden
3. **Secondaries (if ≥2 remaining):** Weighted random sample (up to 2 without replacement) from remaining findings:
   - `selection_score = 0.55 × rarity_score + 0.40 × recency_score + 0.05 × Math.random()`
   - `recency_score = 100 × 0.5^(days_old / 7)` (7-day half-life)
   - Simple weighted sampling: compute scores, normalize to probabilities, pick without replacement

### Finding ID format
IDs follow the pattern `{type}-{ticker_or_slug}-{YYYYMMDD}` where the date is from the finding's `timestamp`. Examples: `coordinated-SIVB-20260212`, `lone_wolf-pelosi-20260301`. IDs are **display-only** — not used for deduplication across exports. Each export recomputes all findings from scratch.

### Output format (`data/insights.json`)
```json
{
  "generated_at": "2026-04-19T12:00:00Z",
  "findings": [
    {
      "id": "coordinated-SIVB-20260212",
      "type": "coordinated_trades",
      "headline": "Unusual cluster: 4 Banking Committee members exited SIVB in 3 days",
      "rarity_score": 96,
      "timestamp": "2026-02-14T00:00:00Z",
      "data": [
        {"name": "Pelosi, N.", "action": "Sold", "amount": "$500K–$1M", "date": "2026-02-12"},
        {"name": "Tuberville, T.", "action": "Sold", "amount": "$250K–$500K", "date": "2026-02-13"},
        {"name": "Warren, E.", "action": "Sold", "amount": "$100K–$250K", "date": "2026-02-13"},
        {"name": "Scott, T.", "action": "Sold", "amount": "$50K–$100K", "date": "2026-02-14"}
      ],
      "link": {"view": "signals", "tab": "swarms", "search": "SIVB"}
    }
  ]
}
```

### Link shapes
- Signals tab: `{"view": "signals", "tab": "<tab-id>", "search": "<filter_text>"}`
  - Valid tab IDs: `swarms`, `round-trips`, `compliance`, `first-movers`, `consensus`, `contrarian`
- Company detail: `{"view": "company", "ticker": "<TICKER>"}`
- Person detail: `{"view": "person", "slug": "<person-slug>"}`

### `insightClick(finding)` implementation
```javascript
insightClick(f) {
  const link = f.link;
  if (link.view === 'signals') {
    // Set tab and search state BEFORE navigating
    this.signalsTab = link.tab;
    // Set the appropriate search field for the tab
    const searchFields = {
      'swarms': 'swarmSearch',
      'round-trips': 'roundTripSearch',
      'compliance': 'complianceSearch',
      'first-movers': null,  // no search field
      'consensus': 'partisanSearch',
      'contrarian': 'partisanSearch',
    };
    const field = searchFields[link.tab];
    if (field && link.search) this[field] = link.search;
    this.navigate('signals');
  } else if (link.view === 'company') {
    this.openCompany(link.ticker);
  } else if (link.view === 'person') {
    this.openPerson(link.slug);
  }
}
```

## Dashboard Changes

### Remove: Risk Scale legend
- Delete the dismissable Risk Scale banner from above the dashboard
- Move the scale explanation into a tooltip on the Rankings view header (where it's contextually relevant)

### Fix: Activity Mix badges clickable
- Each event type badge in the Activity Mix strip becomes clickable
- Clicking scrolls to Latest Events on the dashboard and sets a temporary filter on `event_type`

### Fix: Top Movers shows sub-scores
- Add Market/Policy/Insider sub-score numbers to each mover row
- Show the composite score more prominently with the delta

## Architecture

### Backend
- New file: `internal/insights/detector.go` — pattern detection engine
  - `type Finding struct` with: ID, Type, Headline, RarityScore, Timestamp, Data (json.RawMessage), Link (json.RawMessage)
  - `func Detect(ctx context.Context, d *db.DB) ([]Finding, error)` — runs all 6 detectors with 30s timeout each, merges results, sorts by rarity, keeps top 20
  - One unexported function per detector type: `detectCoordinated`, `detectCommitteeRelevant`, `detectPreEvent`, `detectRoundTrips`, `detectSwarmOutliers`, `detectLoneWolf`
- New file: `internal/insights/detector_test.go` — table-driven tests per detector with in-memory SQLite fixtures
- Modify: `internal/export/export.go` — call `insights.Detect()`, marshal to JSON, write to `{outDir}/insights.json`

### Frontend (`web/static/index.html`)
- New state: `insights: []`, `insightHero: null`, `insightSecondaries: []`
- New method: `fetchInsights()` — fetches `data/insights.json`, runs `selectInsights()`
- New method: `selectInsights()` — implements the two-tier selection algorithm, sets `insightHero` and `insightSecondaries`
- New method: `insightClick(finding)` — navigates to the linked view with filters pre-applied (see implementation above)
- New HTML: Insights card in the dashboard section, replacing Risk Scale
- Call `fetchInsights()` during `init()` alongside other dashboard data fetches

## Headline Style Guide

Headlines should:
- Name the pattern type briefly ("Unusual cluster:", "Lone wolf:", "Pre-event timing:")
- State the factual observation without judgment
- Include key numbers (count of reps, time window, amount)
- Never use words like "suspicious," "corrupt," "insider trading," or any language implying wrongdoing

Examples:
- "Unusual cluster: 4 Banking Committee members exited SIVB in 3 days"
- "Lone wolf: Rep. X traded $2.1M in NVDA — 8× their typical size"
- "Pre-event timing: 3 trades in LMT within 48h before $2B DoD contract announced"
- "Fast round-trip: Sen. Y bought then sold AAPL in 12 days for $500K"
- "Swarm: 7 representatives traded TSLA in a single week"
- "Committee overlap: 2 Armed Services members bought RTX before hearing"
