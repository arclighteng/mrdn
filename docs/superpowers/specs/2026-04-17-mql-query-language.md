# MQL — MRDN Query Language

**Date:** 2026-04-17
**Status:** Draft
**Author:** Claude (architect review)

## Overview

MQL is a GitHub-style structured query language for searching MRDN's political risk intelligence data. It replaces the current sector text filter with a powerful query bar that supports structured filters, autocomplete, and shareable URLs.

### Architecture

```
[Query Bar] → POST /api/query → [Cloudflare Worker] → MQL parser → parameterized SQL → [Neon Postgres] → JSON → [Render]
```

The existing static JSON dashboard (tabs, heatmaps, movers, signals) is unchanged. The Worker exclusively handles MQL queries from the query bar.

### Design Decisions

1. **GitHub-style `key:value` syntax** — structured filters with bare text fallback
2. **Power tool + discovery engine** — structured syntax for power users, autocomplete for approachability
3. **Unified timeline by default, grouped on demand** — `group:` modifier switches layout
4. **Query bar sits on top of tabs** — doesn't replace navigation; results overlay the active tab
5. **Autocomplete: filter keys + entity values** — static `query-index.json` on page load, no server round trip
6. **Cloudflare Worker backend** — queries Neon Postgres directly; eliminates client-side bundle resolution

---

## 1. Query Grammar

### Syntax

```
query       ::= clause (WS clause)*
clause      ::= negation? (filter | bare_text)
negation    ::= "-"
filter      ::= key ":" value_expr
value_expr  ::= range_expr | list_expr | date_expr | quoted_str | bare_value
range_expr  ::= (">" | "<" | ">=" | "<=") number | number ".." number
date_expr   ::= YYYY | YYYY-MM | YYYY-MM-DD | relative_date
relative_date ::= number ("d"|"w"|"m"|"y") | "today" | "last-week" | "last-month" | "last-quarter" | "ytd"
list_expr   ::= value ("," value)*
quoted_str  ::= '"' [^"]* '"'
```

- Space between clauses = AND
- Comma within a value = OR (within same key)
- `-` before a filter = NOT
- Relative dates resolve at evaluation time (server clock)

### Filter Vocabulary

#### Entity Filters

| Key | What it filters | Values | Example |
|-----|----------------|--------|---------|
| `ticker:` | Company symbol | Ticker symbols, comma-separated | `ticker:MSFT,AMZN` |
| `sector:` | GICS sector | Sector name (case-insensitive) | `sector:defense` |
| `subsector:` | GICS subsector | Subsector name | `subsector:aerospace` |
| `market-cap:` | Company size bucket | `large`, `mid`, `small` | `market-cap:large` |
| `by:` | Person (slug or name) | Person slug or quoted name | `by:pelosi`, `by:"nancy pelosi"` |
| `party:` | Political party | `D`, `R`, `I` | `party:D` |
| `branch:` | Congressional chamber | `senate`, `house` | `branch:senate` |
| `committee:` | Committee membership | Committee code or slug | `committee:armed-services` |

#### Event Type Filter

| Key | Values | Maps to |
|-----|--------|---------|
| `type:` | `trade`, `contract`, `sanction`, `donation`, `lobbying`, `insider`, `court`, `warn`, `tariff` | Dispatches to the corresponding typed table |

#### Event-Specific Filters

| Key | Applicable types | Values | Example |
|-----|-----------------|--------|---------|
| `action:` | trade, insider, contract | Trade: `buy`, `sell`, `exchange`. Insider: `buy`, `sell`, `10b5-1`, `option`, `gift`. Contract: `award`, `modification`, `cancellation`. | `action:buy` |
| `owner:` | trade | `self`, `spouse`, `dependent` | `owner:spouse` |
| `agency:` | contract | Agency name (fuzzy) | `agency:DoD` |
| `country:` | sanction, tariff | ISO code or name | `country:RU` |

> **Note on tariffs:** The `tariffs` table has no `company_id`. It links to companies only via `hs_codes[]` → `company_hs_codes` join table. Filters like `ticker:` and `sector:` combined with `type:tariff` require this two-hop join. If the join yields no match, results are empty with no error. Entity filters (`by:`, `party:`, `branch:`) are inapplicable to tariffs.
| `program:` | sanction | OFAC program name | `program:RUSSIA-EO14024` |
| `state:` | warn | Two-letter postal code | `state:OH` |
| `registrant:` | lobbying | Lobbying firm name | `registrant:"Akin Gump"` |
| `filing:` | court | Filing type | `filing:complaint` |
| `signal:` | trade | `swarm`, `first-mover`, `round-trip`, `partisan` | `signal:swarm` |

#### Numeric Filters

| Key | Syntax | Notes |
|-----|--------|-------|
| `score:` | `>70`, `50..80`, `<=30` | Composite score (0–100) |
| `market-score:` | Same range syntax | Market sub-score |
| `policy-score:` | Same range syntax | Policy sub-score |
| `insider-score:` | Same range syntax | Insider sub-score |
| `amount:` | `>1m`, `100k..500k` | Dollar amount; `k`=1,000, `m`=1,000,000, `b`=1,000,000,000. User always writes dollars. Compiler converts to cents for `contracts.amount_cents` and `donations.amount_cents` (multiply by 100). Congressional trades `amount_range_low/high` are already in dollars. |
| `workers:` | `>1000`, `500..2000` | WARN filing headcount |

#### Date Filters

| Key | Syntax | Notes |
|-----|--------|-------|
| `since:` | `2025-01`, `30d`, `last-quarter`, `ytd` | Lower bound on event date |
| `before:` | Same | Upper bound on event date |

Each event type uses its canonical date column: `congressional_trades.traded_at`, `contracts.awarded_at`, `sanctions.added_at`, `donations.donated_at`, `lobbying.period_end`, `insider_trades.traded_at`, `court_filings.filed_at`, `warn_filings.layoff_date`, `tariffs.effective_at`.

#### Modifiers

| Key | Values | Default | Notes |
|-----|--------|---------|-------|
| `sort:` | `recent`, `score`, `amount`, `score-delta` | `recent` | Sort order for results |
| `group:` | `type`, `company`, `person`, `sector`, `week`, `month` | _(none)_ | Groups results; changes response shape. Max one per query. |
| `limit:` | 1–200 | 50 | Result cap |

### Interaction Rules

1. Different keys = AND
2. Same key, comma-separated values = OR (`sector:defense,tech`)
3. Same key, repeated = AND (usually zero results — parser warns and suggests comma)
4. `-key:value` negates. `-ticker:MSFT,AMZN` = NOT (MSFT OR AMZN)
5. `branch:`, `party:`, `owner:`, `action:` only apply to person-linked event types. Incompatible combos exclude those branches with a response warning.
6. One `group:` key per query max.

### Bare Text

Bare text (no key prefix) matches against `companies.ticker`, `companies.name`, and `persons.name` via ILIKE. Does NOT search event descriptions. Multiple bare words are ANDed.

For UNION ALL branches without a `companies` join (some tariffs), bare text on company fields is omitted for that branch. For branches without a `persons` join (contracts, sanctions, warn, court, tariffs), bare text on person fields is omitted. Each branch applies only the joins it has.

### Example Queries

```
type:trade by:pelosi sector:defense since:30d
type:contract agency:DoD amount:>10m sort:amount
ticker:MSFT,AMZN type:insider since:2025
score:>80 sector:pharma -branch:house
type:trade action:buy owner:spouse since:last-quarter
type:sanction country:RU,CN program:RUSSIA-EO14024
type:warn state:TX workers:>1000 since:2025
type:trade signal:swarm since:90d group:company
committee:armed-services type:trade action:buy
type:donation by:"ted cruz" amount:>5000
```

---

## 2. Worker Architecture

### Stack

- **Runtime:** Cloudflare Worker (TypeScript)
- **Database:** Neon Postgres via `@neondatabase/serverless` (WebSocket mode for transaction support — HTTP mode does not support `SET LOCAL statement_timeout` which requires a transaction block; use `neonConfig.webSocketConstructor` with `ws` polyfill, or set `statement_timeout` via the connection string parameter `?options=-c statement_timeout=8000`)
- **Caching:** Cloudflare KV for signal membership blobs and data metadata
- **Location:** `workers/query/` in the monorepo

### File Structure

```
workers/query/
  src/
    index.ts         — entrypoint, routing, CORS, error handling
    parser.ts        — MQL tokenizer → ParsedQuery struct
    compiler.ts      — ParsedQuery → { sql: string, params: any[] }
    db.ts            — Neon serverless driver setup
    rate-limit.ts    — KV-backed concurrent request limiting
  wrangler.toml      — Worker config, KV bindings, secrets
  tsconfig.json
  package.json
```

### Request / Response

**Request:**
```
POST /api/query
Content-Type: application/json

{
  "q": "type:trade by:pelosi since:30d",
  "cursor": null
}
```

**Success response (200):**
```json
{
  "results": [...],
  "meta": {
    "query": "type:trade by:pelosi since:30d",
    "query_ms": 42,
    "result_count": 47,
    "has_more": true,
    "next_cursor": "eyJ0cyI6IjIwMjUtMDMtMTRUMDA6MDAiLCJpZCI6ODQ3Mjl9",
    "data_as_of": "2026-04-17T06:00:00Z",
    "grouped": false
  },
  "warnings": []
}
```

**Error responses:**

| Status | Code | When |
|--------|------|------|
| 400 | `PARSE_ERROR` | Unknown key, bad syntax, invalid value |
| 400 | `COMPLEXITY_LIMIT` | Query exceeds complexity threshold |
| 413 | `REQUEST_TOO_LARGE` | Request body > 2KB |
| 429 | `RATE_LIMITED` | Rate limit exceeded |
| 409 | `CURSOR_STALE` | Data re-ingested since cursor was created |
| 503 | `QUERY_TIMEOUT` | Postgres statement timeout (8s) |

All errors: `{ "error": string, "message": string, "code": string }`.

### Rate Limiting

- **Edge-level:** 30 requests/min/IP via Cloudflare rate limiting rules
- **Concurrent:** 5 in-flight queries per IP via KV counter (approximate, prevents slow query abuse)

### CORS

```
Access-Control-Allow-Origin: *
Access-Control-Allow-Methods: POST, OPTIONS
Access-Control-Allow-Headers: Content-Type
```

Public data — wildcard origin is appropriate.

### Authentication

None. All data is already publicly available via static JSON exports. Rate limiting is the only access control.

### Input Limits

- Request body max: 2KB
- Query max tokens: 20 filter clauses
- Comma-separated values per key: 20

---

## 3. MQL-to-SQL Compilation

### Pipeline

```
Input string → Tokenizer → Token classifier → ParsedQuery → Validator → SQLBuilder → SQL + params
```

The parser and SQL builder are completely separate. The parser produces a `ParsedQuery` struct; the compiler consumes it. This separation allows swapping the backend (e.g., replacing Neon with D1) without changing the parser.

### Type Dispatch

The `type:` filter determines which tables to query:

**Single type** → direct query against the typed table with joins to `events`, `persons`, `companies`, `scores` as needed.

**Multiple types** → UNION ALL across specified typed tables with the canonical projection (see below).

**No type** → UNION ALL across all ten typed tables (most expensive path, subject to complexity limits).

### Canonical UNION ALL Projection

Every branch of the UNION ALL must SELECT the same column list. Columns not applicable to a given type are NULL:

```sql
SELECT
  'trade'              AS event_type,
  e.id                 AS event_id,        -- globally unique, used for cursor pagination
  ct.traded_at         AS occurred_at,      -- canonical date for this type
  p.slug               AS person_slug,      -- NULL for non-person types
  p.name               AS person_name,      -- NULL for non-person types
  p.party              AS person_party,     -- NULL for non-person types
  p.branch             AS person_branch,    -- NULL for non-person types
  c.ticker             AS ticker,           -- NULL if no company link
  c.name               AS company_name,     -- NULL if no company link
  c.sector             AS sector,           -- NULL if no company link
  ct.trade_type        AS action,           -- type-specific action column
  ct.owner_type        AS owner,            -- NULL for non-trade types
  (ct.amount_range_low + ct.amount_range_high) / 2 AS amount_mid,  -- normalized to dollars
  NULL::text           AS agency,           -- NULL for non-contract types
  NULL::text           AS program,          -- NULL for non-sanction types
  NULL::text           AS country,          -- NULL for non-sanction/tariff types
  NULL::text           AS state,            -- NULL for non-warn types
  NULL::int            AS workers_affected, -- NULL for non-warn types
  NULL::text           AS entity_name,      -- NULL for non-sanction types
  NULL::text           AS description,      -- NULL for non-contract types
  NULL::text           AS registrant,       -- NULL for non-lobbying types
  NULL::text           AS filing_type,      -- NULL for non-court types
  NULL::text           AS filer_name,       -- NULL for non-insider types
  NULL::text           AS filer_title,      -- NULL for non-insider types
  NULL::int            AS shares,           -- NULL for non-insider types
  ct.filed_at          AS filed_at,         -- filing date (separate from occurred_at)
  ls.composite_score   AS score             -- NULL if no score filter/sort
FROM congressional_trades ct
JOIN events e ON e.id = ct.event_id
JOIN persons p ON p.id = ct.person_id
JOIN companies c ON c.id = ct.company_id
LEFT JOIN latest_scores ls ON ls.company_id = c.id
WHERE ...
```

Each typed table branch fills in its specific columns and NULLs the rest. The client uses `event_type` to decide which fields to render.

### Filter-to-SQL Mapping

| MQL Key | SQL Predicate | Notes |
|---------|--------------|-------|
| `ticker:` | `c.ticker = ANY($N)` | On `companies` join |
| `sector:` | `c.sector ILIKE $N` | Case-insensitive match |
| `subsector:` | `c.subsector ILIKE $N` | |
| `market-cap:` | `c.market_cap_bucket = $N` | |
| `by:` (slug) | `p.slug = ANY($N)` | On `persons` join |
| `by:` (name) | `p.name ILIKE $N` | Detected by presence of spaces or non-slug chars |
| `party:` | `p.party = ANY($N)` | |
| `branch:` | `p.branch = ANY($N)` | |
| `committee:` | `EXISTS (SELECT 1 FROM person_committees pc WHERE pc.person_id = p.id AND pc.committee_code = ANY($N))` | Subquery |
| `action:` | `ct.trade_type = ANY($N)` | Column name varies by table |
| `owner:` | `ct.owner_type = ANY($N)` | |
| `agency:` | `co.agency ILIKE $N` | |
| `country:` | `s.country = ANY($N)` | For tariffs: `t.affected_countries && ARRAY[$N]`. Tariff→company join goes through `company_hs_codes`: `JOIN company_hs_codes chc ON chc.hs_code = ANY(t.hs_codes) JOIN companies c ON c.id = chc.company_id` |
| `program:` | `s.program = ANY($N)` | |
| `state:` | `w.state = ANY($N)` | |
| `registrant:` | `l.registrant ILIKE $N` | |
| `filing:` | `cf.filing_type = ANY($N)` | |
| `amount:` | Range predicate on amount column | Midpoint for trade amounts; cents/100 for contracts/donations |
| `workers:` | Range predicate on `w.workers_affected` | |
| `score:` | Join to `latest_scores` CTE, `ls.composite_score > $N` | Uses DISTINCT ON CTE |
| `since:` | `<date_col> >= $N` | Column per table (see grammar section) |
| `before:` | `<date_col> < $N` | |
| `signal:` | `c.ticker = ANY($signal_tickers)` | Tickers from KV pre-computed membership |
| Bare text | `(c.ticker ILIKE $N OR c.name ILIKE $N)` | ANDed across multiple bare words |

Filters that don't apply to a table branch produce `WHERE 1=0` for that branch + a response warning.

### Score Filter

Uses a CTE to avoid per-row correlated subqueries:

```sql
WITH latest_scores AS (
  SELECT DISTINCT ON (company_id)
    company_id, composite_score, market_score, policy_score, insider_score
  FROM scores
  ORDER BY company_id, computed_at DESC
)
```

Each UNION ALL branch joins `latest_scores` when any score filter is present.

### Signal Filter

Signals are pre-computed at export time and stored in Cloudflare KV as ticker lists. The compiler loads the relevant KV key and injects `c.ticker = ANY($signal_tickers)` into the WHERE clause. No expensive live computation.

**KV fallback:** if the KV key is missing or returns `undefined`, return a warning `"Signal data is temporarily unavailable. Results may be incomplete."` and omit the signal filter (return unfiltered results rather than empty).

### Sort Mapping

| `sort:` value | ORDER BY |
|--------------|----------|
| `recent` | `occurred_at DESC` |
| `score` | `ls.composite_score DESC NULLS LAST` (join to latest_scores CTE) |
| `amount` | `amount_mid DESC NULLS LAST` |
| `score-delta` | **Deferred to v2.** Requires a `score_movers` materialized view. If requested, return 400 with `"sort:score-delta is not yet supported. Use sort:score instead."` |

### Group Mapping

`group:` changes the response shape. The compiled SQL wraps the base query in an aggregation:

```sql
SELECT <group_key>, COUNT(*) AS count,
       MIN(occurred_at) AS first_seen, MAX(occurred_at) AS last_seen,
       SUM(amount_mid) AS total_amount
FROM (...base query without LIMIT...) AS base
GROUP BY <group_key>
ORDER BY count DESC
LIMIT $N
```

- `group:week` → `date_trunc('week', occurred_at)`
- `group:month` → `date_trunc('month', occurred_at)`
- `group:company` → `ticker`
- `group:person` → `person_slug`
- `group:sector` → `sector`
- `group:type` → `event_type`

Group queries require at least one narrowing filter (`type:`, `ticker:`, `by:`, or `since:`).

### Parameterization

All user-supplied values are bind parameters exclusively. Table names, column names, and operators are compile-time constants. The parameter array is built positionally (`$1`, `$2`, ...) during compilation.

Multi-value OR lists use `= ANY($N)` with array parameters (pgx handles array binding natively).

### Complexity Pre-Check

Before issuing the query, the compiler assigns a complexity score:

| Factor | Cost |
|--------|------|
| No `type:` (cross-type UNION) | +3 |
| `group:` present | +2 |
| `group:` present AND no date bounds | +2 (additional) |
| No date bounds | +2 |
| `signal:` without pre-computed KV | +3 |
| `sort:score-delta` | +1 |
| No `type:` AND no `ticker:` AND no `by:` AND no date bounds | +2 (additional) |

If total > 6, return 400 `COMPLEXITY_LIMIT` before touching the database.

### Query Timeout

```sql
SET LOCAL statement_timeout = '8000';
```

Issued before the compiled query within the same transaction. Postgres returns error code `57014` on timeout; Worker returns 503 `QUERY_TIMEOUT`.

---

## 4. Pagination

### Strategy: Cursor-Based

Cursor encodes the last row's sort key, the parent `events.id` (globally unique across all typed tables), and the `data_as_of` timestamp for staleness detection. Base64-encoded:

```json
{
  "occurred_at": "2025-03-15T14:22:00Z",
  "event_id": 92041,
  "data_as_of": "2026-04-17T06:00:00Z"
}
```

Next-page WHERE clause uses `events.id` (globally unique) for tie-breaking, not the typed table's local `id`:

```sql
WHERE (occurred_at < $cursor_ts)
   OR (occurred_at = $cursor_ts AND e.id < $cursor_event_id)
```

All UNION ALL branches join to `events` via `event_id`, so `e.id` is available in every branch and is globally unique across types. This resolves the cross-type pagination problem.

For non-default sorts, the cursor encodes the relevant sort column instead of `occurred_at`.

### Staleness Detection

The cursor embeds `data_as_of` (from KV metadata). On decode, the Worker compares cursor's `data_as_of` against the current KV value. If they differ, the data has been re-ingested since the cursor was created. Return 409 `CURSOR_STALE`.

| Status | Code | When |
|--------|------|------|
| 409 | `CURSOR_STALE` | Cursor's `data_as_of` differs from current metadata |

---

## 5. Result Shapes

### Event Result (unified timeline)

Each result includes `event_type` for client-side rendering dispatch.

**Trade:**
```json
{
  "event_type": "trade",
  "id": 84729,
  "occurred_at": "2025-03-14T00:00:00Z",
  "person": { "slug": "nancy-pelosi", "name": "Nancy Pelosi", "party": "D", "branch": "house" },
  "company": { "ticker": "MSFT", "name": "Microsoft Corp", "sector": "Technology" },
  "action": "purchase",
  "owner": "spouse",
  "amount_range_low": 1000001,
  "amount_range_high": 5000000,
  "amount_mid": 3000000,
  "filed_at": "2025-03-29T00:00:00Z",
  "score": 82.4
}
```

**Contract:**
```json
{
  "event_type": "contract",
  "id": 12034,
  "occurred_at": "2025-03-10T00:00:00Z",
  "company": { "ticker": "LMT", "name": "Lockheed Martin", "sector": "Industrials" },
  "agency": "DoD",
  "action": "award",
  "amount": 450000000,
  "description": "F-35 sustainment contract modification",
  "score": 91.2
}
```

**Sanction:**
```json
{
  "event_type": "sanction",
  "id": 5522,
  "occurred_at": "2025-03-01T00:00:00Z",
  "entity_name": "Rosneft Oil Company",
  "program": "RUSSIA-EO14024",
  "country": "RU",
  "company": { "ticker": "ROSN", "name": "Rosneft" }
}
```

**WARN:**
```json
{
  "event_type": "warn",
  "id": 8801,
  "occurred_at": "2025-03-05T00:00:00Z",
  "company": { "ticker": "META", "name": "Meta Platforms" },
  "state": "CA",
  "city": "Menlo Park",
  "workers_affected": 3400,
  "layoff_date": "2025-04-30"
}
```

**Donation, Lobbying, Insider, Court, Tariff:** follow the same pattern — `event_type` discriminator + type-specific fields.

### Grouped Result

When `group:` is present, `meta.grouped = true` and results contain aggregation rows:

```json
{
  "group_key": "Technology",
  "group_by": "sector",
  "count": 124,
  "total_amount": 8400000000,
  "first_seen": "2024-01-03T00:00:00Z",
  "last_seen": "2026-04-15T00:00:00Z"
}
```

---

## 6. Autocomplete Index

### File: `query-index.json`

Static file generated by Go export, fetched eagerly on page load. Target: <150KB gzipped.

```json
{
  "version": "2026-04-17T06:00:00Z",
  "keys": [
    { "key": "type:", "values": ["trade","contract","sanction","donation","lobbying","insider","court","warn","tariff"], "description": "Event type" },
    { "key": "action:", "values": ["buy","sell","exchange","10b5-1","option"], "description": "Trade direction" },
    { "key": "party:", "values": ["D","R","I"], "description": "Political party" },
    { "key": "branch:", "values": ["senate","house"], "description": "Chamber" },
    { "key": "owner:", "values": ["self","spouse","dependent"], "description": "Trade ownership" },
    { "key": "sort:", "values": ["recent","score","amount","score-delta"], "description": "Sort order" },
    { "key": "group:", "values": ["type","company","person","sector","week","month"], "description": "Group by" },
    { "key": "market-cap:", "values": ["large","mid","small"], "description": "Company size" },
    { "key": "signal:", "values": ["swarm","first-mover","round-trip","partisan"], "description": "Signal membership" }
  ],
  "persons": [
    { "slug": "nancy-pelosi", "name": "Nancy Pelosi", "party": "D", "branch": "house" }
  ],
  "tickers": [
    { "ticker": "MSFT", "name": "Microsoft Corp", "sector": "Technology" }
  ],
  "agencies": ["DoD", "HHS", "DHS", "DOE", "NASA"],
  "sectors": ["Technology", "Industrials", "Health Care", "Financials"],
  "programs": ["RUSSIA-EO14024", "IRAN-EO13846", "SDGT"],
  "committees": [
    { "code": "SARM", "name": "Armed Services (Senate)" }
  ]
}
```

- `persons`: active traders only (trade_count > 0), ~300–500 entries
- `tickers`: top 500 by congressional trade count
- `count: null` fields can be added later for faceted preview (extension seam)

### Autocomplete Behavior

**Empty bar focused:** show recent queries (localStorage, last 5) + suggested queries (curated) + filter key list.

**After key prefix typed (`by:`):** show matching values from index, filtered by typed partial. `by:pel` → `Nancy Pelosi (House, D)`.

**Bare text typed:** show matching tickers and persons from index. Selecting converts to structured filter (`pelosi` → `by:nancy-pelosi`). Enter without selection keeps as bare text.

**Keyboard:** Arrow up/down to navigate, Tab to complete without executing, Enter to execute, Escape to close dropdown, Backspace at token boundary deletes whole `key:value` token.

---

## 7. Client-Side Integration

### Query Bar

Sits above existing tab navigation in the sticky nav area. Does not replace tabs.

### State Machine

```
IDLE → (user types + Enter) → LOADING → (response) → RESULTS
RESULTS → (user clears bar) → IDLE (return to previous tab)
RESULTS → (user modifies query + Enter) → LOADING → RESULTS
```

### URL Serialization

Query state in `?q=` parameter: `?q=type%3Atrade+by%3Apelosi+since%3A30d`

- On page load, if `?q=` present: populate bar, auto-execute
- Updated via `history.pushState` (no reload)
- Clearing removes `?q=`

### Results View

**Unified timeline (default):** chronological feed of typed cards. Each card renders based on `event_type`. Company name/ticker links to existing company detail view. Person name links to existing person detail view.

**Grouped view (when `group:` active):** horizontal bar chart (ECharts) + sorted table with group key, count, total amount.

### States

- **Loading:** spinner overlay, tab content dimmed (opacity 0.3)
- **Empty:** "No results for [query]. Try broadening your date range or removing filters."
- **Error:** red inline message below query bar with `message` from error response
- **Warnings:** yellow callout below results listing incompatible filter warnings

---

## 8. Database Changes

### New Indexes

```sql
CREATE INDEX idx_congressional_trades_traded_at ON congressional_trades(traded_at DESC);
CREATE INDEX idx_contracts_awarded_at ON contracts(awarded_at DESC);
CREATE INDEX idx_contracts_agency ON contracts(agency);
CREATE INDEX idx_donations_donated_at ON donations(donated_at DESC);
CREATE INDEX idx_sanctions_country_program ON sanctions(country, program);
CREATE INDEX idx_warn_filings_state ON warn_filings(state, filed_at DESC);
CREATE INDEX idx_lobbying_registrant ON lobbying(registrant text_pattern_ops);
CREATE INDEX idx_court_filings_filing_type ON court_filings(filing_type, filed_at DESC);
CREATE INDEX idx_companies_name_pattern ON companies(name text_pattern_ops);
CREATE INDEX idx_congressional_trades_company_traded ON congressional_trades(company_id, traded_at DESC);
```

### No New Tables

Signal membership handled via Cloudflare KV, not database tables.

---

## 9. Go Export Changes

Two new functions in `internal/export/export.go`:

1. **`exportQueryIndex()`** — queries persons (active traders), tickers (top 500), agencies, sectors, programs, committees. Writes `dist/data/query-index.json`.

2. **`exportDataMeta()`** — writes `dist/data/meta.json` with `exported_at` timestamp.

Signal KV blobs exported as part of existing `exportSignals()` and uploaded to Cloudflare KV in the GitHub Actions workflow.

---

## 10. Security

- **SQL injection:** all user input via bind parameters exclusively. Table/column names are compile-time constants.
- **Query complexity:** pre-check prevents pathological queries from reaching database.
- **Rate limiting:** edge-level (30/min/IP) + concurrent query limit (5/IP via KV).
- **Input size:** 2KB body limit, 20 filter tokens, 20 values per key.
- **Auth:** none required (public data). Revisit if non-public data is added.
- **Response size:** 500KB soft limit. Truncated with `meta.truncated = true` if exceeded.

---

## 11. Trade-offs

| Decision | Alternative | Why this way |
|----------|------------|-------------|
| Worker + Neon SQL | Client-side bundle resolution | Eliminates fan-out, client joins, query horizon limits. Every query pattern works. |
| UNION ALL in single query | Separate requests per type | Single round trip cheaper; cross-type sort only possible in single query |
| Cursor pagination | Offset pagination | Offset on UNION ALL re-scans all rows per page |
| KV for signal membership | Live SQL signal computation | Signal queries (swarms, round-trips) are batch-designed, too expensive per-request |
| ILIKE for bare text | pg_trgm full-text | Companies table < 10k rows; seq scan is fine. Switch at 50k+ rows. |
| TypeScript Worker | Go via WASM | First-class CF Workers support; no existing Go parser to port |
| No auth | API keys | All data already public via static JSON. Rate limiting sufficient. |
| `type:` instead of `is:` | `is:` (GitHub convention) | `type:trade` is more intuitive for analysts; `is:` implies state not category |
