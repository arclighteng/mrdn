# Insights Card — Design Spec

## Summary

A non-deterministic, non-partisan "Insights" card on the dashboard that surfaces statistically unusual data patterns — coordinated trades, suspicious timing, outlier behavior — without making opinionated claims. Replaces the Risk Scale legend.

## Layout

Horizontal strip at the top of the dashboard (where Risk Scale legend currently sits). Full-width, single card with:

- **Hero finding (left, ~60% width):** Accent left-border, B-style headline (curious/pattern-naming, e.g. "Unusual cluster: 4 Banking Committee members exited SIVB in 3 days"), mini data table showing the underlying records (names, amounts, dates), "+N more" truncation if needed.
- **Secondary findings (right, ~40% width):** Two compact cards stacked vertically. Each shows: pattern type label, one-line headline, rarity score, and age.

All three findings are clickable — they navigate to the existing view that generated the data, with filters pre-applied.

## Pattern Detectors (6 types)

Each detector runs during the export pipeline and produces scored findings.

### 1. Coordinated Trades
- **Signal:** N representatives trading the same ticker within X days
- **Data:** `congressional_trades` grouped by ticker + time window
- **Rarity scoring:** Higher with more reps, tighter time window, same trade direction
- **Clickthrough:** Signals → Swarms tab, filtered to that ticker

### 2. Committee-Relevant Trades
- **Signal:** Representative on a committee trades a stock in that committee's jurisdiction
- **Data:** `congressional_trades` + `persons.committees` (if available) or inferred from event co-occurrence
- **Rarity scoring:** Higher when trade is large, committee relevance is strong, timing is close to committee activity
- **Clickthrough:** Person detail view for that representative

### 3. Pre-Event Timing
- **Signal:** Congressional trade occurs within N days before a significant event on that ticker (sanctions, contracts, regulatory actions)
- **Data:** `congressional_trades` joined with `events` on ticker, filtered by time proximity
- **Rarity scoring:** Higher with shorter time gap, larger trade size, more significant event type
- **Clickthrough:** Company detail view showing both the trade and the event in timeline

### 4. Round-Trip Anomalies
- **Signal:** Unusually fast buy→sell or sell→buy cycles
- **Data:** Existing `round_trips` signal data
- **Rarity scoring:** Higher with shorter round-trip duration, larger amounts, deviation from rep's historical pattern
- **Clickthrough:** Signals → Round Trips tab, filtered to that person/ticker

### 5. Swarm Outliers
- **Signal:** Ticker with abnormally high coordinated-trading count
- **Data:** Existing `swarms` signal data
- **Rarity scoring:** Use the swarm's rep count and time concentration as the rarity input
- **Clickthrough:** Signals → Swarms tab, filtered to that ticker

### 6. Lone Wolf
- **Signal:** Single representative making a trade far larger than their usual pattern
- **Data:** `congressional_trades` — compare trade amount to rep's historical median
- **Rarity scoring:** Based on z-score deviation from rep's baseline (e.g., >3σ = rarity 80+)
- **Clickthrough:** Person detail view

## Selection Algorithm

### Pre-computation (backend, during export)
1. Run all 6 detectors
2. Score each finding with a `rarity_score` (0–100)
3. Record `timestamp` (when the underlying data occurred)
4. Keep top 20 by rarity score
5. Write to `data/insights.json`

### Display selection (frontend, each page load)
1. **Hero:** Always `argmax(findings, rarity_score)` — the strangest thing never gets hidden
2. **Secondaries:** Weighted random sample (2 without replacement) from remaining 19:
   - `selection_score = 0.55 × rarity_score + 0.40 × recency_score + 0.05 × Math.random()`
   - `recency_score = 100 × 0.5^(days_old / 7)` (7-day half-life)
   - Use Gumbel-max trick or simple weighted sampling

### Output format (`data/insights.json`)
```json
{
  "generated_at": "2026-04-19T12:00:00Z",
  "findings": [
    {
      "id": "coordinated-SIVB-20260212",
      "type": "coordinated_trades",
      "headline": "4 Banking Committee members exited SIVB in 3 days",
      "rarity_score": 96,
      "timestamp": "2026-02-14T00:00:00Z",
      "data": [
        {"name": "Pelosi, N.", "action": "Sold", "amount": "$500K–$1M", "date": "2026-02-12"},
        {"name": "Tuberville, T.", "action": "Sold", "amount": "$250K–$500K", "date": "2026-02-13"},
        {"name": "Warren, E.", "action": "Sold", "amount": "$100K–$250K", "date": "2026-02-13"},
        {"name": "Scott, T.", "action": "Sold", "amount": "$50K–$100K", "date": "2026-02-14"}
      ],
      "link": {"view": "signals", "tab": "swarms", "filter": "SIVB"}
    }
  ]
}
```

## Dashboard Changes

### Remove: Risk Scale legend
- Delete the dismissable Risk Scale banner from above the dashboard
- Move the scale explanation into a tooltip on the Rankings view header (where it's contextually relevant)

### Fix: Activity Mix badges clickable
- Each event type badge in the Activity Mix strip becomes clickable
- Clicking navigates to Signals → Latest Events filtered by that event type, or scrolls to Latest Events on the dashboard and applies a filter

### Fix: Top Movers shows sub-scores
- Add Market/Policy/Insider sub-score bars or numbers to each mover row
- Show the composite score more prominently with the delta

## Architecture

### Backend (`internal/export/`)
- New file: `internal/insights/detector.go` — pattern detection engine
  - One function per detector type
  - Each returns `[]Finding` with rarity scores
  - Shared `Finding` struct with: ID, Type, Headline, RarityScore, Timestamp, Data (json.RawMessage), Link
- Modify: `internal/export/export.go` — call insight detectors, write `data/insights.json`

### Frontend (`web/static/index.html`)
- New state: `insights: []`, fetched from `data/insights.json`
- New method: `selectInsights()` — implements the two-tier selection algorithm
- New method: `insightClick(finding)` — navigates to the linked view with filters
- New HTML: Insights card in the dashboard section, replacing Risk Scale

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
