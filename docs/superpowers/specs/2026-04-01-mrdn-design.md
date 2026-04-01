# MRDN — Design Specification

## Overview

MRDN is a public, free, API-first platform that aggregates public government data — congressional stock trades, federal contracts, sanctions, tariffs, layoff notices, campaign donations, lobbying registrations, court filings, and market data — resolves entities across sources, computes composite activity scores per company, and serves it all via REST, SSE, and CLI.

No editorial voice. No causal claims. The data is organized and color-coded; users draw their own conclusions.

## Architecture

Single Go binary with three internal subsystems, backed by Postgres.

```
┌──────────────────────────────────────────────────┐
│                    MRDN (Go)                      │
│                                                    │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────┐│
│  │  Ingestion    │  │  Score       │  │  API     ││
│  │  Workers      │  │  Engine      │  │  Server  ││
│  │               │  │              │  │          ││
│  │  One goroutine│  │  Computes    │  │  REST +  ││
│  │  per source   │  │  composite   │  │  SSE +   ││
│  │               │  │  per company │  │  Swagger ││
│  └──────┬────────┘  └──────┬───────┘  └────┬─────┘│
│         └────────┬─────────┘               │      │
│                  ▼                         │      │
│           ┌────────────┐                   │      │
│           │  Postgres   │◄─────────────────┘      │
│           └────────────┘                          │
│                                                    │
│  ┌──────────────┐                                  │
│  │  CLI          │  mrdn ingest, mrdn query,       │
│  │               │  mrdn score, mrdn serve         │
│  └──────────────┘                                  │
└──────────────────────────────────────────────────┘
        ▲                           ▲
        │ API calls                 │ SSE stream
        │                           │
  ┌─────┴──────┐            ┌───────┴────────┐
  │  Jupyter    │            │  JS Frontend   │
  │  (explore)  │            │  (later)       │
  └────────────┘            └────────────────┘
```

### Components

- **Ingestion Workers** — one goroutine per data source, each on its own poll schedule, with independent rate limiting, backoff, and health checks.
- **Score Engine** — recalculates composite scores when new events arrive. Stores history.
- **API Server** — REST + SSE + Swagger UI. Serves all data with per-source freshness metadata.
- **CLI** — `mrdn serve`, `mrdn ingest`, `mrdn query`, `mrdn score`, `mrdn sources`, `mrdn link`.
- **Jupyter** — local-only exploration scratchpad. Calls the Go API via HTTP. Never deployed.

## Data Sources

| Source | Data | Method | Poll Frequency | Format | Freshness Grade |
|--------|------|--------|---------------|--------|-----------------|
| Senate EFDS | Congressional + spousal stock trades | Scrape XML e-filings | Hourly | XML | D (30-45 day filing delay) |
| House EFDS | Congressional + spousal stock trades | PDF parsing (deferred) | TBD | PDF | D (30-45 day filing delay) |
| USAspending.gov | Federal contract awards | REST API | Daily | JSON | B (1-2 day lag) |
| OFAC SDN | Sanctions designations | REST API + XML feed | Every 30 min | XML/JSON | A (~30 min) |
| Federal Register | Tariffs, executive orders, rules | REST API | Hourly | JSON | A (~1 hour) |
| WARN Act | Layoff notices (per state) | Scrape state DOL sites | Daily | HTML/CSV/PDF | B-C (1 day to weeks, varies by state) |
| FEC | Campaign donations | REST API | Daily | JSON/CSV | B (1-7 day lag) |
| LDA (Senate) | Lobbying registrations + spending | Bulk XML downloads | Daily | XML | D (weeks to quarterly) |
| PACER/RECAP | Federal court dockets | CourtListener RECAP API | Hourly | JSON | B+ (hours) |
| Finnhub | Real-time price + volume | WebSocket | Real-time | JSON | A+ (seconds) |
| Polygon.io | Daily OHLCV + historicals | REST API | Daily | JSON | B (end of day) |
| SEC EDGAR Form 4 | Corporate insider trades | REST API | Hourly | XML | A (same day) |
| FRED | Macro economic indicators | REST API | Daily/monthly | JSON | B (daily) |
| OGE 278e | Executive branch financial disclosures | FOIA / scrape ethics pages | Weekly | PDF/HTML | C (annual, delayed) |
| Fed disclosures | Federal Reserve officials | Fed websites | Weekly | PDF/HTML | C (annual) |
| Fix the Court | Judicial financial disclosures | Scrape / bulk download | Weekly | Various | C (annual, delayed) |

### Prioritization

- **Launch:** Senate EFDS, USAspending, OFAC, Federal Register, Finnhub, Polygon, SEC EDGAR Form 4, FEC
- **Fast follow:** WARN Act (top 5-10 states: CA, TX, NY, IL, FL), PACER/RECAP, LDA
- **Later:** House EFDS (PDF), OGE 278e, Fed disclosures, judiciary disclosures, remaining WARN states

### Hard Sources

- **EFDS:** Start with Senate XML e-filings (structured). Defer House PDF parsing.
- **WARN Act:** 50 different state websites, no standard format. Start with biggest states, expand over time.

## Data Model

### Core Tables

```sql
companies (
  id SERIAL PRIMARY KEY,
  ticker TEXT UNIQUE NOT NULL,
  name TEXT NOT NULL,
  sector TEXT,
  subsector TEXT,
  naics_code TEXT,              -- NAICS industry code for sector grouping
  market_cap_bucket TEXT
)

events (
  id SERIAL PRIMARY KEY,
  source TEXT NOT NULL,
  source_id TEXT,
  company_id INT REFERENCES companies,  -- nullable, resolved async
  event_type TEXT NOT NULL,
  event_data JSONB NOT NULL,             -- full raw payload
  occurred_at TIMESTAMPTZ NOT NULL,
  ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (source, source_id)            -- dedup: same source event ingested once
)

persons (
  id SERIAL PRIMARY KEY,
  slug TEXT UNIQUE NOT NULL,   -- URL-safe identifier, e.g. "tommy-tuberville" or "janet-yellen"
  name TEXT NOT NULL,
  role TEXT NOT NULL,  -- lawmaker/cabinet/agency_head/fed_governor/judge/senior_official/military/spouse/child
  tier INT NOT NULL,   -- 1-6
  branch TEXT,         -- legislative/executive/judicial/military
  linked_person_id INT REFERENCES persons,
  linked_relationship TEXT,  -- spouse/dependent_child
  disclosure_source TEXT     -- efds/oge278/fed/judiciary
)

congressional_trades (
  id SERIAL PRIMARY KEY,
  event_id INT REFERENCES events,
  person_id INT REFERENCES persons,
  company_id INT REFERENCES companies,  -- nullable, resolved async via ticker
  owner_type TEXT,  -- self/SP/DC/JT
  ticker TEXT,
  trade_type TEXT,  -- buy/sell
  amount_range_low INT,
  amount_range_high INT,
  filed_at TIMESTAMPTZ,
  traded_at TIMESTAMPTZ
)

contracts (
  id SERIAL PRIMARY KEY,
  event_id INT REFERENCES events,
  company_id INT REFERENCES companies,
  agency TEXT,
  amount_cents BIGINT,
  action_type TEXT,  -- award/modification/cancellation
  description TEXT,
  awarded_at TIMESTAMPTZ
)

sanctions (
  id SERIAL PRIMARY KEY,
  event_id INT REFERENCES events,
  company_id INT REFERENCES companies,  -- nullable, resolved async
  entity_name TEXT,
  entity_type TEXT,  -- person/company/vessel
  program TEXT,
  country TEXT,
  added_at TIMESTAMPTZ
)

tariffs (
  id SERIAL PRIMARY KEY,
  event_id INT REFERENCES events,
  hs_codes TEXT[],
  affected_countries TEXT[],
  action_type TEXT,  -- new/modified/removed
  effective_at TIMESTAMPTZ
)

warn_filings (
  id SERIAL PRIMARY KEY,
  event_id INT REFERENCES events,
  company_id INT REFERENCES companies,
  state TEXT,
  city TEXT,
  workers_affected INT,
  layoff_date DATE,
  filed_at TIMESTAMPTZ
)

donations (
  id SERIAL PRIMARY KEY,
  event_id INT REFERENCES events,
  company_id INT REFERENCES companies,  -- nullable, resolved from donor employer
  donor_name TEXT,
  donor_type TEXT,  -- individual/pac/corp
  donor_employer TEXT,
  recipient TEXT,
  recipient_person_id INT REFERENCES persons,  -- nullable, resolved async
  recipient_type TEXT,  -- candidate/committee
  amount_cents BIGINT,
  donated_at TIMESTAMPTZ
)

lobbying (
  id SERIAL PRIMARY KEY,
  event_id INT REFERENCES events,
  client_company_id INT REFERENCES companies,  -- nullable, resolved from client name
  registrant TEXT,
  client TEXT,
  specific_issues TEXT,
  amount_cents BIGINT,
  period_start DATE,           -- LDA reporting period start
  period_end DATE,             -- LDA reporting period end
  filed_at TIMESTAMPTZ
)

court_filings (
  id SERIAL PRIMARY KEY,
  event_id INT REFERENCES events,
  company_id INT REFERENCES companies,  -- nullable, resolved from parties
  case_number TEXT,
  court TEXT,
  parties TEXT[],
  filing_type TEXT,
  filed_at TIMESTAMPTZ
)

market_data (
  id SERIAL PRIMARY KEY,
  company_id INT REFERENCES companies NOT NULL,
  source TEXT NOT NULL,        -- finnhub/polygon
  data_type TEXT NOT NULL,     -- realtime/daily_ohlcv
  price_cents BIGINT,
  volume BIGINT,
  change_pct NUMERIC(8,4),
  recorded_at TIMESTAMPTZ NOT NULL
)

insider_trades (
  id SERIAL PRIMARY KEY,
  event_id INT REFERENCES events,
  company_id INT REFERENCES companies,
  filer_name TEXT,
  filer_title TEXT,
  trade_type TEXT,  -- purchase/sale/option_exercise
  shares INT,
  price_cents BIGINT,
  filed_at TIMESTAMPTZ,
  traded_at TIMESTAMPTZ
)
```

### Supporting Tables

```sql
person_committees (
  id SERIAL PRIMARY KEY,
  person_id INT REFERENCES persons NOT NULL,
  committee_name TEXT NOT NULL,
  committee_code TEXT,
  start_date DATE,
  end_date DATE
)

company_hs_codes (
  id SERIAL PRIMARY KEY,
  company_id INT REFERENCES companies NOT NULL,
  hs_code TEXT NOT NULL,
  source TEXT,
  confidence NUMERIC(3,2)
)

score_weights (
  id SERIAL PRIMARY KEY,
  version INT UNIQUE NOT NULL,
  weights JSONB NOT NULL,       -- {"market": 0.35, "policy": 0.40, "insider": 0.25, ...}
  active BOOLEAN DEFAULT false,
  created_at TIMESTAMPTZ DEFAULT NOW()
)

bills (
  id SERIAL PRIMARY KEY,
  bill_number TEXT UNIQUE NOT NULL,  -- e.g. "H.R.4521"
  title TEXT,
  status TEXT,
  congress INT,
  introduced_at DATE,
  last_action_at DATE,
  source TEXT                   -- congress.gov
)
```

### Entity Resolution

```sql
entity_aliases (
  id SERIAL PRIMARY KEY,
  entity_id INT NOT NULL,
  entity_type TEXT NOT NULL,  -- company/person/agency
  alias TEXT NOT NULL,
  source TEXT,
  confidence NUMERIC(3,2),
  auto_applied BOOLEAN DEFAULT false  -- false = flagged for review if below threshold
)

entity_links (
  id SERIAL PRIMARY KEY,
  from_entity INT NOT NULL,
  from_type TEXT NOT NULL,
  to_entity INT NOT NULL,
  to_type TEXT NOT NULL,
  relationship TEXT NOT NULL,  -- trades_stock_in/lobbies_for/contracts_with/donates_to/employs/sits_on_committee/supplies_to/affected_by_tariff/spouse_of
  evidence_event_id INT REFERENCES events,
  discovered_at TIMESTAMPTZ DEFAULT NOW()
)
```

Resolution layers (in order):
1. Ticker match (EFDS, market data use tickers directly)
2. Name fuzzy match (Levenshtein + normalized names against SEC EDGAR CIK database)
3. Person-to-company (FEC employer field, lawmaker committee jurisdiction)
4. HS code to company (tariff codes mapped to importers)
5. Manual overrides (`mrdn link --alias "NVDA Corp" --entity NVDA`)
6. Unresolved queue (events that can't be matched, reviewed via CLI or API)

### Freshness Tracking

```sql
source_meta (
  id SERIAL PRIMARY KEY,
  source_name TEXT UNIQUE NOT NULL,
  expected_lag TEXT,
  last_successful_poll TIMESTAMPTZ,
  last_new_data_at TIMESTAMPTZ,
  poll_interval_seconds INT,
  status TEXT DEFAULT 'healthy' CHECK (status IN ('healthy', 'degraded', 'stale', 'down'))
)
```

Every API response includes:
```json
{
  "data": {},
  "freshness": {
    "source": "ofac_sdn",
    "source_lag": "minutes",
    "last_updated": "2026-04-01T14:32:00Z",
    "age_seconds": 847,
    "grade": "A"
  }
}
```

### Score Storage

```sql
scores (
  id SERIAL PRIMARY KEY,
  company_id INT REFERENCES companies NOT NULL,
  market_score NUMERIC(5,2),
  policy_score NUMERIC(5,2),
  insider_score NUMERIC(5,2),
  composite_score NUMERIC(5,2),
  weight_version INT REFERENCES score_weights(version),
  computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
```

## Composite Score Engine

Three sub-scores per company, each 0-100.

### Market Score (0-100)

| Signal | Weight | Calculation |
|--------|--------|-------------|
| Price trend (5-day) | 30% | Normalized percentage change |
| Volume anomaly | 30% | Current volume vs 30-day average |
| SEC Form 4 insider activity | 40% | Corporate insider buys/sells, recency-weighted |

### Policy Exposure Score (0-100)

| Signal | Weight | Calculation |
|--------|--------|-------------|
| Tariff hits | 25% | Count + magnitude of tariff actions on company's import codes |
| Sanctions proximity | 25% | Degrees of separation from sanctioned entities |
| Contract changes | 25% | Awards/cancellations in last 30 days, normalized by revenue |
| Court filings | 25% | Active federal cases involving the company or sector |

### Insider Signal Score (0-100)

| Signal | Weight | Calculation |
|--------|--------|-------------|
| Congressional/official trades | 40% | Trades by officials on relevant committees, recency-weighted. Includes spouse/dependent trades. |
| Lobbying spend changes | 30% | Period-over-period change in spend targeting relevant issues (LDA reports semi-annually) |
| FEC donation spikes | 30% | Unusual donation volume to lawmakers on relevant committees |

### Composite

Weighted average: Market 35%, Policy 40%, Insider 25%.

Policy weighted highest — that is MRDN's unique value. Weights are configurable, not hardcoded. Old scores retained for historical trending.

Recalculation triggered by any new event involving the company.

## Person Tracking

### Tiers

| Tier | Who | Source |
|------|-----|--------|
| 1 | Congress (535 lawmakers) + spouses + dependents | EFDS |
| 2 | President, VP, Cabinet + spouses | OGE 278e |
| 3 | Deputy/undersecretaries, senior advisors + spouses | OGE 278e |
| 4 | Fed governors, regional bank presidents + spouses | Fed disclosures |
| 5 | Federal judges, SCOTUS + spouses | Fix the Court + judiciary.gov |
| 6 | Senior DOD/intel officials + spouses | OGE 278e via FOIA |

Launch with Tier 1. Fast follow Tiers 2-4. Tiers 5-6 later.

## API

Base: `/api/v1`

### Companies
```
GET /companies                     -- list, filter by sector/ticker/score range
GET /companies/:ticker             -- detail + current scores
GET /companies/:ticker/scores      -- score history over time
GET /companies/:ticker/events      -- all events for this company
GET /companies/:ticker/timeline    -- events + scores on a single timeline
```

### Events
```
GET /events                        -- firehose, filter by source/type/date
GET /events/:id                    -- single event detail
GET /events/latest                 -- most recent across all sources
```

### Scores
```
GET /scores/rankings               -- all companies sorted by composite
GET /scores/movers                 -- biggest score changes in last N hours
GET /scores/heatmap                -- sector-level aggregation
```

### Sources
```
GET /sources                       -- all sources + freshness status
GET /sources/:name                 -- single source detail + health
```

### Persons
```
GET /persons                       -- list, filter by tier/branch/role
GET /persons/:slug                 -- detail: trades, committees, donations
```

### Connections
```
GET /connections/:ticker           -- chain: trades, lobbying, contracts, tariffs
GET /connections/:slug             -- person view: trades, committees, donations
GET /connections/graph             -- network graph: companies, persons, agencies, bills + edges
                                   -- supports ?depth=N (default 2, max 4) and ?limit=N (default 200 nodes)
```

### Stream (SSE)
```
GET /stream                        -- all new events
GET /stream/:ticker                -- single company
GET /stream/scores                 -- score changes
```

### CLI Equivalents
```
mrdn serve                         -- starts API server
mrdn query companies --sector tech --min-score 60
mrdn query events --source ofac --since 24h
mrdn query connections NVDA
mrdn scores --movers --hours 6
mrdn ingest --source all
mrdn ingest --source warn --state CA
mrdn sources
mrdn link --alias "NVDA Corp" --entity NVDA
```

All list endpoints support pagination, date range filtering, and JSON response. All responses include freshness metadata.

### Score Response Shape

All score endpoints return the full breakdown:
```json
{
  "ticker": "NVDA",
  "scores": {
    "market": 72.5,
    "policy": 91.0,
    "insider": 45.3,
    "composite": 73.8
  },
  "weight_version": 1,
  "computed_at": "2026-04-01T15:00:00Z"
}
```

### API Access

Public, no auth required for read access. Optional API key for higher rate limits. Anonymous: 60 req/min. Keyed: 600 req/min.

```sql
api_keys (
  id SERIAL PRIMARY KEY,
  key_hash TEXT UNIQUE NOT NULL,
  label TEXT,
  rate_limit INT DEFAULT 600,
  created_at TIMESTAMPTZ DEFAULT NOW()
)
```

## Frontend Visualization

Built later as a separate JS app consuming the API.

### Primary View: The Board

Ranked vertical bar chart of companies. Each bar segmented into three score components. Re-sorts in real-time via SSE. Tallest bars = most activity.

### Secondary Views

| View | Purpose | API Source |
|------|---------|-----------|
| Company Deep Dive | Timeline of events + score history | `/companies/:ticker/timeline` + `/scores` |
| Connection Graph | Interactive network of entities and relationships | `/connections/graph` |
| Event Feed | Filterable firehose of new events | `/events/latest` + `/stream` |
| Movers | Biggest score changes recently | `/scores/movers` |
| Sector Heatmap | Sector-level aggregate exposure | `/scores/heatmap` |
| Person View | Official's trades, committees, donations | `/connections/:person` |
| Freshness Dashboard | Source health and update times | `/sources` |

### Color System

Semantic color tokens, never hardcoded values. Frontend resolves tokens via a theme JSON object at runtime.

Default semantic mapping:
- **Money** — follows dollars wherever they appear (donations, contracts, lobbying spend, trade amounts). Intensity scales with magnitude.
- **Policy** — government actions (tariffs, sanctions, executive orders, court rulings).
- **Consequence** — outcomes (WARN filings, stock drops, layoffs).

Multiple palettes shipped. User can switch. Power users can paste custom palettes. Theme swap = one JSON object, no code changes.

The API returns semantic labels (`"type": "money"`), never colors. Color is purely a frontend concern.

### Tone

- No tagline
- No editorial voice, no causal claims, no accusatory framing
- Data points shown on timelines; users see proximity and draw their own conclusions
- Dry, factual. Like a well-designed government filing.
- Footer: "Source: public records. Not investment advice."

## Critical Indexes

```sql
CREATE INDEX idx_events_company_occurred ON events(company_id, occurred_at);
CREATE INDEX idx_market_data_company_recorded ON market_data(company_id, recorded_at);
CREATE INDEX idx_entity_links_from ON entity_links(from_entity, from_type);
CREATE INDEX idx_entity_links_to ON entity_links(to_entity, to_type);
CREATE INDEX idx_scores_company_computed ON scores(company_id, computed_at);
CREATE INDEX idx_congressional_trades_company ON congressional_trades(company_id);
CREATE INDEX idx_person_committees_person ON person_committees(person_id);
```

## Storage Budget

Supabase free tier: 500MB. Constraints at launch:

- **Company list:** cap at ~100 tech companies initially
- **Market data:** at 100 companies, ~1 tick/min during market hours (6.5h), ~39K rows/day, ~1.5MB/day. 90-day retention before downsampling = ~135MB.
- **Events:** low volume relative to market data. ~1-5K events/day across all sources.
- **Headroom:** ~300MB for all other tables, indexes, and growth.

If approaching 500MB: aggressive downsampling, drop to daily-only market data, or upgrade Supabase.

## Deployment

| Component | Where | Cost |
|-----------|-------|------|
| Go binary | Fly.io or Railway free tier | $0 |
| Postgres | Supabase free tier (500MB) | $0 |
| Frontend | Vercel or Cloudflare Pages free tier | $0 |
| DNS | Cloudflare — `mrdn.arclighteng.com` | $0 |
| Market data | Finnhub (WebSocket, free) + Polygon.io (free tier) | $0 |
| All other APIs | Free / public government data | $0 |

Total: $0/mo at launch.

## Operations

| Concern | Approach |
|---------|----------|
| Source goes down | Worker health check, freshness grade degrades, API serves stale data with honest grade |
| Source changes format | Parser fails, worker marks degraded. Fix parser, redeploy. |
| Postgres fills up | Downsample market_data after 90 days (keep daily OHLCV, drop intraday). Events table append-only, compresses well. |
| Score accuracy | Transparent — every input visible via API |
| API abuse | Rate limiting per API key |

## Legal

- All data sourced from public government records
- No TOS restrictions on scraping federal government sites
- Market data used within provider terms (Polygon attribution, Finnhub free tier terms)
- No editorial voice, no causal claims = low defamation risk
- Visualization design must not imply wrongdoing through layout or adjacency
- Disclaimer: "Source: public records. Not investment advice."
