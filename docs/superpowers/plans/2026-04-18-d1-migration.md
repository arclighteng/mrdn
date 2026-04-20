# Neon Postgres → Cloudflare D1 Migration

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Neon Postgres with Cloudflare D1 (SQLite) so the entire stack runs on Cloudflare's free tier with zero external database dependencies.

**Architecture:** The Go CI binary ingests data into a local SQLite file, exports static JSON, then uploads the SQLite data to D1 via `wrangler d1 execute`. The MQL Worker reads from D1 via native Worker bindings. All SQL is rewritten to SQLite dialect. Array columns become JSON text or normalized junction tables. LISTEN/NOTIFY is removed (unnecessary in static-first architecture).

**Tech Stack:** Cloudflare D1, SQLite, `modernc.org/sqlite` (pure Go, no CGO), `database/sql` stdlib interface

---

## File Map

### New files
- `internal/db/timeutil.go` — `scanTime` / `formatTime` / `formatTimePtr` helpers for `time.Time` ↔ TEXT roundtrip
- `internal/db/migrations/001_sqlite_initial.sql` — Full SQLite schema (replaces all Postgres migrations)
- `workers/query/src/d1.ts` — D1 native binding executor (replaces `db.ts`)

### Major rewrites (every query needs Postgres→SQLite conversion)
- `internal/db/db.go` — New DBTX interface using `database/sql` types
- `internal/db/companies.go` — `RETURNING` → `last_insert_rowid()`, `DISTINCT ON` → window function
- `internal/db/persons.go` — `RETURNING` → `last_insert_rowid()`, subquery-based ordering
- `internal/db/events.go` — Batch INSERT rewrite, `RETURNING` → `last_insert_rowid()`, `$N` → `?`
- `internal/db/scores.go` — `DISTINCT ON` → window function, `make_interval` → `datetime()`
- `internal/db/tickers.go` — `to_char` → `strftime`, `EXTRACT(EPOCH)` → `julianday`, `JOIN LATERAL` → correlated subquery
- `internal/db/compliance.go` — `PERCENTILE_CONT` → Go-side percentile, `DISTINCT ON` → window function
- `internal/db/signals.go` — `ARRAY_AGG` → `GROUP_CONCAT`, `date_trunc` → `strftime`
- `internal/db/profile.go` — 10+ queries with date functions, `PERCENTILE_CONT`, `date_trunc`
- `internal/db/stats.go` — `EXTRACT(DOW/MONTH)` → `strftime('%w'/'%m')`, `make_interval` → `datetime()`
- `internal/db/heatmap.go` — `DISTINCT ON` → window function
- `internal/db/migrate.go` — pgxpool → `database/sql`, simplified for SQLite
- `internal/db/typed_tables.go` — Array params (`[]string`) → JSON text
- `internal/db/source_meta.go` — `NOW()` → `datetime('now')`, `time.Time` params → `formatTime()`
- `internal/db/entity_links.go` — `RETURNING` → INSERT then SELECT, `pgx.ErrNoRows` → `sql.ErrNoRows`, `LOWER(alias)` conflict target
- `internal/db/timeline.go` — `s.db.Query/QueryRow` → `s.db.QueryContext/QueryRowContext`
- `internal/db/notify.go` — **DELETE** (LISTEN/NOTIFY is Postgres-only, unused in static-first arch)
- `workers/query/src/compiler.ts` — `$N` → `?`, `ANY()` → `IN()`, `DISTINCT ON` → window function, type casts removed
- `workers/query/src/types.ts` — `Env.DATABASE_URL` → `Env.DB: D1Database`

### Modified
- `go.mod` — Remove `pgx`, add `modernc.org/sqlite`
- `workers/query/wrangler.toml` — Add D1 binding, remove DATABASE_URL reference
- `workers/query/package.json` — Remove `@neondatabase/serverless`
- `.github/workflows/ingest-deploy.yml` — Use `wrangler d1 execute` for migrations, D1 API for writes

### Deleted
- `internal/db/notify.go` — Postgres LISTEN/NOTIFY, not applicable to SQLite
- `internal/db/notify_test.go` — Tests for deleted file
- `internal/db/migrations/001_initial.sql` through `005_mql_indexes.sql` — Replaced by single SQLite migration

---

## SQL Conversion Reference

This table covers every Postgres→SQLite pattern in the codebase. Refer to it when converting queries.

| Postgres | SQLite | Notes |
|----------|--------|-------|
| `SERIAL PRIMARY KEY` | `INTEGER PRIMARY KEY AUTOINCREMENT` | |
| `TIMESTAMPTZ` | `TEXT` | Store ISO 8601: `2026-04-18T12:00:00Z` |
| `JSONB` | `TEXT` | `json_valid()` for checks, `json_extract()` for queries |
| `TEXT[]` | JSON text or junction table | `court_filings.parties` → JSON, `tariffs.hs_codes` → junction table |
| `NUMERIC(5,2)` | `REAL` | |
| `$1, $2, $3` | `?, ?, ?` | Positional → positional (unnamed) |
| `NOW()` | `datetime('now')` | |
| `INTERVAL '24 hours'` | `datetime('now', '-1 day')` | Used in WHERE comparisons |
| `make_interval(days => $1)` | Use `julianday()` math | `traded_at >= julianday('now') - ?` or pre-compute in Go |
| `EXTRACT(EPOCH FROM (a - b)) / 86400` | `(julianday(a) - julianday(b))` | Returns days as float |
| `EXTRACT(DOW FROM x)` | `CAST(strftime('%w', x) AS INTEGER)` | 0=Sunday in both |
| `EXTRACT(MONTH FROM x)` | `CAST(strftime('%m', x) AS INTEGER)` | |
| `date_trunc('week', x)` | `date(x, '-' \|\| strftime('%w', x) \|\| ' days', '+1 day')` | Monday-start. Alt: `date(x, 'weekday 1', '-7 days')` — test edge cases! |
| `date_trunc('month', x)` | `date(x, 'start of month')` | |
| `to_char(x, 'YYYY-MM-DD')` | `strftime('%Y-%m-%d', x)` | |
| `to_char(date_trunc('month', x), 'YYYY-MM')` | `strftime('%Y-%m', x)` | |
| `DISTINCT ON (col) ... ORDER BY col, x DESC` | `ROW_NUMBER() OVER (PARTITION BY col ORDER BY x DESC)` + filter `rn=1` | |
| `= ANY($1)` (array param) | `IN (?,?,?)` | Expand array to individual params |
| `ARRAY_AGG(DISTINCT x)` | `GROUP_CONCAT(DISTINCT x)` | Returns comma-separated string |
| `arr && $1` (array overlap) | `EXISTS (SELECT ... FROM junction_table)` | Needs junction table |
| `ILIKE` | `LIKE` | SQLite LIKE is case-insensitive for ASCII |
| `::BIGINT`, `::INT`, `::text` | Remove cast | SQLite uses dynamic typing |
| `NULL::text`, `NULL::bigint` | `NULL` | Remove type annotations |
| `ON CONFLICT ... DO UPDATE ... RETURNING id` | `INSERT OR REPLACE` + `last_insert_rowid()` | Two-step: insert then select |
| `ON CONFLICT ... DO NOTHING` | `INSERT OR IGNORE` | |
| `RETURNING id` | `last_insert_rowid()` | Call after INSERT |
| `PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY x)` | Compute in Go | Fetch sorted values, pick median |
| `LISTEN` / `NOTIFY` / `pg_notify()` | Remove | Not needed in static-first architecture |
| `text_pattern_ops` | Remove | Not applicable to SQLite |
| `::date` | `date(x)` | |
| `COALESCE(x, 0)::BIGINT` | `COALESCE(x, 0)` | |

---

### Task 1: Create D1 Database and SQLite Schema

**Files:**
- Create: `internal/db/migrations/001_sqlite_initial.sql`
- Modify: `workers/query/wrangler.toml`

- [ ] **Step 1: Create the D1 database via wrangler**

```bash
npx wrangler d1 create mrdn-db
```

Note the database ID from the output. You'll need it for wrangler.toml and CI.

- [ ] **Step 2: Write the SQLite schema**

Create `internal/db/migrations/001_sqlite_initial.sql`:

```sql
-- MRDN SQLite schema (D1)
-- Converted from Postgres migrations 001-005

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS companies (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    sector TEXT,
    subsector TEXT,
    naics_code TEXT,
    market_cap_bucket TEXT
);

CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    source_id TEXT,
    company_id INTEGER REFERENCES companies(id),
    event_type TEXT NOT NULL,
    event_data TEXT NOT NULL,  -- JSON stored as text
    occurred_at TEXT NOT NULL, -- ISO 8601
    ingested_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (source, source_id)
);

CREATE TABLE IF NOT EXISTS persons (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    role TEXT NOT NULL,
    tier INTEGER NOT NULL,
    branch TEXT,
    state TEXT,
    party TEXT,
    bioguide_id TEXT,
    linked_person_id INTEGER REFERENCES persons(id),
    linked_relationship TEXT,
    disclosure_source TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_persons_bioguide ON persons(bioguide_id) WHERE bioguide_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS congressional_trades (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    person_id INTEGER REFERENCES persons(id),
    company_id INTEGER REFERENCES companies(id),
    owner_type TEXT,
    ticker TEXT,
    trade_type TEXT,
    amount_range_low INTEGER,
    amount_range_high INTEGER,
    filed_at TEXT,  -- ISO 8601
    traded_at TEXT   -- ISO 8601
);

CREATE TABLE IF NOT EXISTS contracts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    agency TEXT,
    amount_cents INTEGER,
    action_type TEXT,
    description TEXT,
    awarded_at TEXT
);

CREATE TABLE IF NOT EXISTS sanctions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    entity_name TEXT,
    entity_type TEXT,
    program TEXT,
    country TEXT,
    added_at TEXT
);

-- Tariffs: array columns become junction tables
CREATE TABLE IF NOT EXISTS tariffs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    action_type TEXT,
    effective_at TEXT
);

CREATE TABLE IF NOT EXISTS tariff_hs_codes (
    tariff_id INTEGER NOT NULL REFERENCES tariffs(id) ON DELETE CASCADE,
    hs_code TEXT NOT NULL,
    PRIMARY KEY (tariff_id, hs_code)
);

CREATE TABLE IF NOT EXISTS tariff_countries (
    tariff_id INTEGER NOT NULL REFERENCES tariffs(id) ON DELETE CASCADE,
    country TEXT NOT NULL,
    PRIMARY KEY (tariff_id, country)
);

CREATE TABLE IF NOT EXISTS warn_filings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    state TEXT,
    city TEXT,
    workers_affected INTEGER,
    layoff_date TEXT,
    filed_at TEXT
);

CREATE TABLE IF NOT EXISTS donations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    donor_name TEXT,
    donor_type TEXT,
    donor_employer TEXT,
    recipient TEXT,
    recipient_person_id INTEGER REFERENCES persons(id),
    recipient_type TEXT,
    amount_cents INTEGER,
    donated_at TEXT
);

CREATE TABLE IF NOT EXISTS lobbying (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    client_company_id INTEGER REFERENCES companies(id),
    registrant TEXT,
    client TEXT,
    specific_issues TEXT,
    amount_cents INTEGER,
    period_start TEXT,
    period_end TEXT,
    filed_at TEXT
);

-- Court filings: parties array → junction table
CREATE TABLE IF NOT EXISTS court_filings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    case_number TEXT,
    court TEXT,
    filing_type TEXT,
    filed_at TEXT
);

CREATE TABLE IF NOT EXISTS court_filing_parties (
    filing_id INTEGER NOT NULL REFERENCES court_filings(id) ON DELETE CASCADE,
    party_name TEXT NOT NULL,
    PRIMARY KEY (filing_id, party_name)
);

CREATE TABLE IF NOT EXISTS market_data (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id INTEGER NOT NULL REFERENCES companies(id),
    source TEXT NOT NULL,
    data_type TEXT NOT NULL,
    price_cents INTEGER,
    volume INTEGER,
    change_pct REAL,
    recorded_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS insider_trades (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER REFERENCES events(id),
    company_id INTEGER REFERENCES companies(id),
    filer_name TEXT,
    filer_title TEXT,
    trade_type TEXT,
    shares INTEGER,
    price_cents INTEGER,
    filed_at TEXT,
    traded_at TEXT
);

CREATE TABLE IF NOT EXISTS person_committees (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id INTEGER NOT NULL REFERENCES persons(id),
    committee_name TEXT NOT NULL,
    committee_code TEXT,
    start_date TEXT,
    end_date TEXT
);

CREATE TABLE IF NOT EXISTS company_hs_codes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id INTEGER NOT NULL REFERENCES companies(id),
    hs_code TEXT NOT NULL,
    source TEXT,
    confidence REAL
);

CREATE TABLE IF NOT EXISTS score_weights (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    version INTEGER UNIQUE NOT NULL,
    weights TEXT NOT NULL,  -- JSON
    active INTEGER DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS bills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    bill_number TEXT UNIQUE NOT NULL,
    title TEXT,
    status TEXT,
    congress INTEGER,
    introduced_at TEXT,
    last_action_at TEXT,
    source TEXT
);

CREATE TABLE IF NOT EXISTS entity_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_id INTEGER NOT NULL,
    entity_type TEXT NOT NULL,
    alias TEXT NOT NULL,
    source TEXT,
    confidence REAL,
    auto_applied INTEGER DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_entity_aliases_unique ON entity_aliases(entity_type, alias COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_entity_aliases_lookup ON entity_aliases(entity_type, alias COLLATE NOCASE);

CREATE TABLE IF NOT EXISTS entity_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_entity INTEGER NOT NULL,
    from_type TEXT NOT NULL,
    to_entity INTEGER NOT NULL,
    to_type TEXT NOT NULL,
    relationship TEXT NOT NULL,
    evidence_event_id INTEGER REFERENCES events(id),
    discovered_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS source_meta (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_name TEXT UNIQUE NOT NULL,
    expected_lag TEXT,
    last_successful_poll TEXT,
    last_new_data_at TEXT,
    poll_interval_seconds INTEGER,
    status TEXT DEFAULT 'healthy' CHECK (status IN ('healthy', 'degraded', 'stale', 'down')),
    last_attempt_at TEXT,
    last_http_code INTEGER,
    last_error TEXT,
    last_records INTEGER,
    last_duration_ms INTEGER
);

CREATE TABLE IF NOT EXISTS scores (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id INTEGER NOT NULL REFERENCES companies(id),
    market_score REAL,
    policy_score REAL,
    insider_score REAL,
    composite_score REAL,
    weight_version INTEGER REFERENCES score_weights(version),
    computed_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS api_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key_hash TEXT UNIQUE NOT NULL,
    label TEXT,
    rate_limit INTEGER DEFAULT 600,
    created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS party_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id INTEGER NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    party TEXT NOT NULL,
    started_at TEXT,
    ended_at TEXT,
    note TEXT,
    UNIQUE (person_id, party, started_at)
);

-- Indexes (MQL performance)
CREATE INDEX IF NOT EXISTS idx_events_company_occurred ON events(company_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_events_source ON events(source);
CREATE INDEX IF NOT EXISTS idx_events_type ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_market_data_company_recorded ON market_data(company_id, recorded_at);
CREATE INDEX IF NOT EXISTS idx_entity_links_from ON entity_links(from_entity, from_type);
CREATE INDEX IF NOT EXISTS idx_entity_links_to ON entity_links(to_entity, to_type);
CREATE INDEX IF NOT EXISTS idx_scores_company_computed ON scores(company_id, computed_at);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_company ON congressional_trades(company_id);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_traded_at ON congressional_trades(traded_at);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_person ON congressional_trades(person_id);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_ticker ON congressional_trades(ticker);
CREATE INDEX IF NOT EXISTS idx_person_committees_person ON person_committees(person_id);
CREATE INDEX IF NOT EXISTS idx_party_history_person ON party_history(person_id);
CREATE INDEX IF NOT EXISTS idx_contracts_awarded_at ON contracts(awarded_at);
CREATE INDEX IF NOT EXISTS idx_contracts_agency ON contracts(agency);
CREATE INDEX IF NOT EXISTS idx_donations_donated_at ON donations(donated_at);
CREATE INDEX IF NOT EXISTS idx_sanctions_country_program ON sanctions(country, program);
CREATE INDEX IF NOT EXISTS idx_warn_filings_state ON warn_filings(state, filed_at);
CREATE INDEX IF NOT EXISTS idx_lobbying_registrant ON lobbying(registrant);
CREATE INDEX IF NOT EXISTS idx_court_filings_filing_type ON court_filings(filing_type, filed_at);
CREATE INDEX IF NOT EXISTS idx_companies_sector ON companies(sector);
CREATE INDEX IF NOT EXISTS idx_tariff_countries_country ON tariff_countries(country);
CREATE INDEX IF NOT EXISTS idx_tariff_hs_codes_hs ON tariff_hs_codes(hs_code);

-- Seed default score weights
INSERT OR IGNORE INTO score_weights (version, weights, active)
VALUES (1, '{"market": 0.35, "policy": 0.40, "insider": 0.25, "market_price_trend": 0.30, "market_volume_anomaly": 0.30, "market_insider_activity": 0.40, "policy_tariff": 0.25, "policy_sanctions": 0.25, "policy_contracts": 0.25, "policy_court": 0.25, "insider_congressional": 0.40, "insider_lobbying": 0.30, "insider_donations": 0.30}', 1);

-- Seed source_meta
INSERT OR IGNORE INTO source_meta (source_name, expected_lag, poll_interval_seconds, status) VALUES
    ('polygon', '1 day', 86400, 'healthy'),
    ('finnhub', 'seconds', 0, 'healthy'),
    ('edgar_form4', 'same day', 3600, 'healthy'),
    ('ofac_sdn', 'minutes', 1800, 'healthy'),
    ('usaspending', '1-2 days', 86400, 'healthy'),
    ('federal_register', '1 hour', 3600, 'healthy'),
    ('fec', '1-7 days', 86400, 'healthy'),
    ('efds_senate', '30-45 days', 3600, 'healthy'),
    ('house_clerk_ptr', '1-30 days', 86400, 'healthy'),
    ('score_engine', 'on-demand', 86400, 'healthy');

-- Seed 20 initial congress members
INSERT OR IGNORE INTO persons (slug, name, role, tier, branch, state, party) VALUES
    ('nancy-pelosi', 'Nancy Pelosi', 'representative', 1, 'legislative', 'CA', 'D'),
    ('mitch-mcconnell', 'Mitch McConnell', 'senator', 1, 'legislative', 'KY', 'R'),
    ('chuck-schumer', 'Chuck Schumer', 'senator', 1, 'legislative', 'NY', 'D'),
    ('kevin-mccarthy', 'Kevin McCarthy', 'representative', 1, 'legislative', 'CA', 'R'),
    ('elizabeth-warren', 'Elizabeth Warren', 'senator', 1, 'legislative', 'MA', 'D'),
    ('ted-cruz', 'Ted Cruz', 'senator', 1, 'legislative', 'TX', 'R'),
    ('bernie-sanders', 'Bernie Sanders', 'senator', 1, 'legislative', 'VT', 'I'),
    ('aoc', 'Alexandria Ocasio-Cortez', 'representative', 1, 'legislative', 'NY', 'D'),
    ('mitt-romney', 'Mitt Romney', 'senator', 2, 'legislative', 'UT', 'R'),
    ('joe-manchin', 'Joe Manchin', 'senator', 2, 'legislative', 'WV', 'D'),
    ('dan-crenshaw', 'Dan Crenshaw', 'representative', 2, 'legislative', 'TX', 'R'),
    ('katie-porter', 'Katie Porter', 'representative', 2, 'legislative', 'CA', 'D'),
    ('josh-hawley', 'Josh Hawley', 'senator', 2, 'legislative', 'MO', 'R'),
    ('kyrsten-sinema', 'Kyrsten Sinema', 'senator', 2, 'legislative', 'AZ', 'I'),
    ('marco-rubio', 'Marco Rubio', 'senator', 1, 'legislative', 'FL', 'R'),
    ('ron-wyden', 'Ron Wyden', 'senator', 2, 'legislative', 'OR', 'D'),
    ('tommy-tuberville', 'Tommy Tuberville', 'senator', 1, 'legislative', 'AL', 'R'),
    ('mark-kelly', 'Mark Kelly', 'senator', 2, 'legislative', 'AZ', 'D'),
    ('marjorie-taylor-greene', 'Marjorie Taylor Greene', 'representative', 1, 'legislative', 'GA', 'R'),
    ('hakeem-jeffries', 'Hakeem Jeffries', 'representative', 1, 'legislative', 'NY', 'D');
```

- [ ] **Step 3: Update wrangler.toml with D1 binding**

Add to `workers/query/wrangler.toml`:

```toml
[[d1_databases]]
binding = "DB"
database_name = "mrdn-db"
database_id = "<ID_FROM_STEP_1>"
```

- [ ] **Step 4: Apply the schema to D1**

```bash
cd workers/query
npx wrangler d1 execute mrdn-db --file=../../internal/db/migrations/001_sqlite_initial.sql --remote
```

- [ ] **Step 5: Commit**

```bash
git add internal/db/migrations/001_sqlite_initial.sql workers/query/wrangler.toml
git commit -m "feat(db): add SQLite schema for D1 migration"
```

---

### Task 2: Swap Go Dependencies (pgx → modernc/sqlite + database/sql)

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Remove pgx, add sqlite driver**

```bash
cd /c/Users/AR/Projects/mrdn
go get -u modernc.org/sqlite
go get github.com/jackc/pgx/v5@none
```

Note: This will break compilation. That's expected — the remaining tasks fix every file.

- [ ] **Step 2: Run `go mod tidy`**

```bash
go mod tidy
```

This will fail because code still imports pgx. That's OK — we'll fix file by file.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(deps): swap pgx for modernc.org/sqlite"
```

---

### Task 3: Rewrite DBTX Interface and Connection Layer

**Files:**
- Rewrite: `internal/db/db.go`
- Rewrite: `internal/db/companies.go` (DBTX interface is defined here currently)

- [ ] **Step 1: Write the test — verify Connect opens a SQLite database**

Create `internal/db/connect_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/require"
)

func TestConnect_SQLite(t *testing.T) {
	d, err := db.Connect(context.Background(), ":memory:")
	require.NoError(t, err)
	defer d.Close()

	// Verify we can execute a simple query
	var result int
	err = d.QueryRowContext(context.Background(), "SELECT 1").Scan(&result)
	require.NoError(t, err)
	require.Equal(t, 1, result)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /c/Users/AR/Projects/mrdn && go test ./internal/db/ -run TestConnect_SQLite -v
```

Expected: compilation error (Connect signature changed)

- [ ] **Step 3: Rewrite `internal/db/db.go`**

```go
package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Connect opens a SQLite database at dsn (file path or ":memory:").
// For D1 in production, use ConnectD1 instead.
func Connect(ctx context.Context, dsn string) (*sql.DB, error) {
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// SQLite pragmas for performance
	if _, err := d.ExecContext(ctx, `
		PRAGMA journal_mode=WAL;
		PRAGMA foreign_keys=ON;
		PRAGMA busy_timeout=5000;
	`); err != nil {
		d.Close()
		return nil, fmt.Errorf("setting pragmas: %w", err)
	}

	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return d, nil
}
```

- [ ] **Step 4: Rewrite the DBTX interface in `internal/db/companies.go`**

Replace the DBTX interface and Store definition at the top of `companies.go`:

```go
package db

import (
	"context"
	"database/sql"
	"fmt"
)

// DBTX is implemented by *sql.DB and *sql.Tx.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db DBTX
}

func NewStore(db DBTX) *Store {
	return &Store{db: db}
}
```

**IMPORTANT: Create `internal/db/timeutil.go` with time helpers used by ALL subsequent tasks:**

```go
package db

import "time"

// formatTime converts a time.Time to an ISO 8601 string for SQLite storage.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// formatTimePtr converts a *time.Time to *string for SQLite storage.
func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// scanTime parses an ISO 8601 string from SQLite into a time.Time.
func scanTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// scanTimePtr parses an optional time string.
func scanTimePtr(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil
	}
	return &t
}
```

All `time.Time` values MUST be formatted to RFC3339 strings before passing as bind params to SQLite. All `time.Time` fields MUST be scanned as strings, then parsed with `scanTime()`. This applies to every file in every subsequent task — not just the ones that explicitly mention it.

Then convert every method in `companies.go` from `s.db.Query(ctx, q, args...)` to `s.db.QueryContext(ctx, q, args...)`, from `s.db.QueryRow(ctx, ...)` to `s.db.QueryRowContext(ctx, ...)`, and from `s.db.Exec(ctx, ...)` to `s.db.ExecContext(ctx, ...)`.

Also convert all `$N` params to `?`, remove `pgx.ErrNoRows` → `sql.ErrNoRows`, remove `RETURNING` → use `result.LastInsertId()`, and remove `DISTINCT ON` → use window function subquery.

Key conversions for companies.go:

**EnsureCompany** — `ON CONFLICT DO NOTHING ... RETURNING` becomes two steps:
```go
func (s *Store) EnsureCompany(ctx context.Context, c Company) (Company, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO companies (ticker, name, sector, subsector, naics_code, market_cap_bucket)
		VALUES (?, ?, ?, ?, ?, ?)
	`, c.Ticker, c.Name, c.Sector, c.Subsector, c.NAICSCode, c.MarketCapBucket)
	if err != nil {
		return Company{}, fmt.Errorf("ensuring company %s: %w", c.Ticker, err)
	}
	return s.GetCompanyByTicker(ctx, c.Ticker)
}
```

**UpsertCompany** — `ON CONFLICT DO UPDATE ... RETURNING` becomes:
```go
func (s *Store) UpsertCompany(ctx context.Context, c Company) (Company, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO companies (ticker, name, sector, subsector, naics_code, market_cap_bucket)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (ticker) DO UPDATE SET
			name = excluded.name,
			sector = excluded.sector,
			subsector = excluded.subsector,
			naics_code = excluded.naics_code,
			market_cap_bucket = excluded.market_cap_bucket
	`, c.Ticker, c.Name, c.Sector, c.Subsector, c.NAICSCode, c.MarketCapBucket)
	if err != nil {
		return Company{}, fmt.Errorf("upserting company %s: %w", c.Ticker, err)
	}
	return s.GetCompanyByTicker(ctx, c.Ticker)
}
```

**ListCompanies with DISTINCT ON** — `DISTINCT ON (company_id)` becomes:
```go
// In buildCompanyWhere and ListCompanies, replace $N with ? and
// replace DISTINCT ON CTE with:
query = `WITH latest_scores AS (
    SELECT company_id, composite_score FROM (
        SELECT company_id, composite_score,
               ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY computed_at DESC) AS rn
        FROM scores
    ) WHERE rn = 1
)
SELECT c.id, c.ticker, c.name, c.sector, c.subsector, c.naics_code, c.market_cap_bucket
FROM companies c
JOIN latest_scores ls ON ls.company_id = c.id
` + conditions
```

**buildCompanyWhere** — Replace `$%d` with `?`:
```go
func buildCompanyWhere(f CompanyFilter) (conditions string, args []any) {
	conditions = "WHERE 1=1"
	if f.Sector != "" {
		conditions += " AND c.sector = ?"
		args = append(args, f.Sector)
	}
	if f.Ticker != "" {
		conditions += " AND c.ticker LIKE ?"
		args = append(args, "%"+f.Ticker+"%")
	}
	if f.MinComposite != nil {
		conditions += " AND ls.composite_score >= ?"
		args = append(args, *f.MinComposite)
	}
	if f.MaxComposite != nil {
		conditions += " AND ls.composite_score <= ?"
		args = append(args, *f.MaxComposite)
	}
	return conditions, args
}
```

Note: `buildCompanyWhere` no longer returns `argN` — with `?` placeholders, argument position is implicit.

- [ ] **Step 5: Run the test**

```bash
go test ./internal/db/ -run TestConnect_SQLite -v
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/db/db.go internal/db/companies.go internal/db/connect_test.go
git commit -m "feat(db): rewrite DBTX interface for database/sql + SQLite"
```

---

### Task 4: Rewrite Migration System

**Files:**
- Rewrite: `internal/db/migrate.go`
- Delete: `internal/db/migrations/001_initial.sql` through `005_mql_indexes.sql` (Postgres-specific)

- [ ] **Step 1: Write the test**

```go
// In internal/db/migrate_test.go, update to use sql.DB:
func TestMigrate(t *testing.T) {
	d, err := db.Connect(context.Background(), ":memory:")
	require.NoError(t, err)
	defer d.Close()

	err = db.Migrate(context.Background(), d)
	require.NoError(t, err)

	// Verify tables exist
	var count int
	err = d.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM companies").Scan(&count)
	require.NoError(t, err)

	// Verify seed data
	err = d.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM persons").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 20, count)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/db/ -run TestMigrate -v
```

- [ ] **Step 3: Rewrite `internal/db/migrate.go`**

```go
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/001_sqlite_initial.sql
var sqliteSchema string

func Migrate(ctx context.Context, d *sql.DB) error {
	// Check if already migrated
	var exists int
	err := d.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'",
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking schema_migrations: %w", err)
	}

	if exists > 0 {
		var applied int
		d.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&applied)
		if applied > 0 {
			return nil // Already migrated
		}
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning migration tx: %w", err)
	}

	if _, err := tx.ExecContext(ctx, sqliteSchema); err != nil {
		tx.Rollback()
		return fmt.Errorf("running schema migration: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_migrations (version) VALUES (1)",
	); err != nil {
		tx.Rollback()
		return fmt.Errorf("recording migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Delete old Postgres migration files**

```bash
rm internal/db/migrations/001_initial.sql
rm internal/db/migrations/002_persons_graph.sql
rm internal/db/migrations/003_party_history.sql
rm internal/db/migrations/004_source_status.sql
rm internal/db/migrations/005_mql_indexes.sql
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/db/ -run TestMigrate -v
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrate.go internal/db/migrate_test.go internal/db/migrations/
git commit -m "feat(db): rewrite migration system for SQLite"
```

---

### Task 5: Rewrite Persons and Events (CRUD Layer)

**Files:**
- Rewrite: `internal/db/persons.go`
- Rewrite: `internal/db/events.go`
- Modify: `internal/db/persons_test.go`, `internal/db/events_test.go`

- [ ] **Step 1: Convert `persons.go`**

Key changes:
- `$N` → `?` everywhere
- `RETURNING ...` in UpsertPerson → `INSERT ... ON CONFLICT DO UPDATE` then `SELECT` by slug
- `ILIKE` → `LIKE`
- `buildPersonWhere` returns `(conditions, args)` without `argN`
- `::BIGINT` casts removed from midpoint expressions in ListPersons
- Replace `fmt.Sprintf(" AND p.tier = $%d", argN)` with `" AND p.tier = ?"`
- All `rows.Scan` stays the same (database/sql uses same scan pattern)

```go
func (s *Store) UpsertPerson(ctx context.Context, p Person) (Person, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO persons (slug, name, role, tier, branch, state, party, bioguide_id, linked_person_id, linked_relationship, disclosure_source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (slug) DO UPDATE SET
			name = excluded.name, role = excluded.role, tier = excluded.tier,
			branch = excluded.branch, state = excluded.state, party = excluded.party,
			bioguide_id = excluded.bioguide_id, linked_person_id = excluded.linked_person_id,
			linked_relationship = excluded.linked_relationship, disclosure_source = excluded.disclosure_source
	`, p.Slug, p.Name, p.Role, p.Tier, p.Branch, p.State, p.Party, p.BioguideID,
		p.LinkedPersonID, p.LinkedRelationship, p.DisclosureSource)
	if err != nil {
		return Person{}, fmt.Errorf("upserting person %s: %w", p.Slug, err)
	}
	return s.GetPersonBySlug(ctx, p.Slug)
}
```

For `ListPersons`, the correlated subqueries for trade stats use the same `midpointExpr` but without the `::BIGINT` cast:

```go
const midpointExpr = `
COALESCE(
  CASE
    WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
      THEN (ct.amount_range_low + ct.amount_range_high) / 2
    WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
    WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
    ELSE 0
  END,
  0
)`
```

Note: This `midpointExpr` is defined in `profile.go`. Remove the `::BIGINT` cast from it.

- [ ] **Step 2: Convert `events.go`**

Key changes:
- `InsertEvent`: `RETURNING id` → use `result.LastInsertId()`
- `InsertEventsBatch`: Rewrite to use `?` params and `LastInsertId()`. D1/SQLite doesn't support multi-row RETURNING; use a loop or batch inserts.
- `ListEvents` and `CountEvents`: `$N` → `?`, remove `argN` counter

```go
func (s *Store) InsertEvent(ctx context.Context, e Event) (int, error) {
	// ... validation unchanged ...

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO events (source, source_id, company_id, event_type, event_data, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (source, source_id) DO UPDATE SET source = excluded.source
	`, e.Source, e.SourceID, e.CompanyID, e.EventType, string(e.EventData), e.OccurredAt.Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("inserting event: %w", err)
	}

	// For ON CONFLICT, LastInsertId may not return the existing row's ID.
	// Instead, query by source+source_id to get the actual ID.
	var id int
	err = s.db.QueryRowContext(ctx,
		"SELECT id FROM events WHERE source = ? AND source_id = ?",
		e.Source, e.SourceID,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("getting event id: %w", err)
	}
	return id, nil
}
```

For `InsertEventsBatch`, convert to individual inserts in a transaction (SQLite is fast for this with WAL mode):

```go
func (s *Store) InsertEventsBatch(ctx context.Context, events []Event) ([]int, error) {
	ids := make([]int, len(events))
	if len(events) == 0 {
		return ids, nil
	}

	for i, e := range events {
		if err := validateEventData(e.EventData); err != nil {
			continue
		}
		id, err := s.InsertEvent(ctx, e)
		if err != nil {
			return ids, fmt.Errorf("batch insert event %d: %w", i, err)
		}
		ids[i] = id
	}
	return ids, nil
}
```

For `ListEvents` dynamic query building, replace `$%d` with `?`:
```go
if f.Source != "" {
    query += " AND e.source = ?"
    args = append(args, f.Source)
}
```

- [ ] **Step 3: Update test files to use `database/sql` and `:memory:` SQLite**

Replace the test helpers in `testutil_test.go` to create in-memory SQLite databases:
```go
func testDB(t *testing.T) *sql.DB {
    t.Helper()
    d, err := db.Connect(context.Background(), ":memory:")
    require.NoError(t, err)
    require.NoError(t, db.Migrate(context.Background(), d))
    t.Cleanup(func() { d.Close() })
    return d
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/db/ -run "TestUpsertPerson|TestInsertEvent|TestListEvents" -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/db/persons.go internal/db/events.go internal/db/persons_test.go internal/db/events_test.go internal/db/testutil_test.go
git commit -m "feat(db): convert persons and events to SQLite dialect"
```

---

### Task 6: Rewrite Typed Tables and Scores

**Files:**
- Rewrite: `internal/db/typed_tables.go`
- Rewrite: `internal/db/scores.go`
- Modify: `internal/db/profile.go` (midpointExpr only — remove `::BIGINT`)

- [ ] **Step 1: Convert `typed_tables.go`**

All insert functions: `$N` → `?`. Most are straightforward.

Special cases:
- `InsertCourtFiling`: `cf.Parties` was `[]string` passed to pgx which handled arrays. Now store as JSON text:
```go
func (s *Store) InsertCourtFiling(ctx context.Context, cf CourtFiling) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO court_filings (event_id, company_id, case_number, court, filing_type, filed_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, cf.EventID, cf.CompanyID, cf.CaseNumber, cf.Court, cf.FilingType,
		formatTimePtr(cf.FiledAt))
	if err != nil {
		return fmt.Errorf("inserting court filing: %w", err)
	}

	if len(cf.Parties) > 0 {
		filingID, _ := result.LastInsertId()
		for _, p := range cf.Parties {
			if _, err := s.db.ExecContext(ctx,
				"INSERT OR IGNORE INTO court_filing_parties (filing_id, party_name) VALUES (?, ?)",
				filingID, p,
			); err != nil {
				return fmt.Errorf("inserting court filing party: %w", err)
			}
		}
	}
	return nil
}
```

- Time values: Convert `*time.Time` to ISO 8601 string before INSERT. Add helper:
```go
func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}
```

- [ ] **Step 2: Convert `scores.go`**

Key changes:
- `$N` → `?`
- `DISTINCT ON` in `GetScoreRankings` → window function:
```go
rows, err := s.db.QueryContext(ctx, `
    WITH latest AS (
        SELECT company_id, market_score, policy_score, insider_score,
               composite_score, weight_version, computed_at
        FROM (
            SELECT company_id, market_score, policy_score, insider_score,
                   composite_score, weight_version, computed_at,
                   ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY computed_at DESC) AS rn
            FROM scores
        ) WHERE rn = 1
    )
    SELECT c.ticker, c.name, c.sector, l.market_score, l.policy_score, l.insider_score,
        l.composite_score, l.weight_version, l.computed_at
    FROM latest l
    JOIN companies c ON c.id = l.company_id
    ORDER BY l.composite_score DESC
    LIMIT ?
`, limit)
```

- `GetScoreMovers`: `DISTINCT ON` → window function, `make_interval(hours => $1)` → `datetime('now', '-' || ? || ' hours')`:
```go
// ... BUT SQLite doesn't support dynamic interval strings easily.
// Pre-compute the cutoff in Go:
cutoff := time.Now().UTC().Add(-time.Duration(hours) * time.Hour).Format(time.RFC3339)
rows, err := s.db.QueryContext(ctx, `
    WITH recent AS (
        SELECT company_id, composite_score, computed_at FROM (
            SELECT company_id, composite_score, computed_at,
                   ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY computed_at DESC) AS rn
            FROM scores
            WHERE computed_at >= ?
        ) WHERE rn = 1
    ),
    previous AS (
        SELECT s.company_id, s.composite_score FROM (
            SELECT s.company_id, s.composite_score,
                   ROW_NUMBER() OVER (PARTITION BY s.company_id ORDER BY s.computed_at DESC) AS rn
            FROM scores s
            JOIN recent r ON r.company_id = s.company_id
            WHERE s.computed_at < r.computed_at
        ) WHERE rn = 1
    )
    SELECT c.ticker, c.name,
        p.composite_score, r.composite_score,
        r.composite_score - p.composite_score,
        ABS(r.composite_score - p.composite_score)
    FROM recent r
    JOIN previous p ON p.company_id = r.company_id
    JOIN companies c ON c.id = r.company_id
    ORDER BY ABS(r.composite_score - p.composite_score) DESC
    LIMIT ?
`, cutoff, limit)
```

- Time scanning: `ComputedAt time.Time` must be scanned as string then parsed:
```go
var computedAtStr string
rows.Scan(..., &computedAtStr)
sc.ComputedAt, _ = time.Parse(time.RFC3339, computedAtStr)
```

- [ ] **Step 3: Remove `::BIGINT` from midpointExpr in `profile.go`**

```go
const midpointExpr = `
COALESCE(
  CASE
    WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
      THEN (ct.amount_range_low + ct.amount_range_high) / 2
    WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
    WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
    ELSE 0
  END,
  0
)`
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/db/ -run "TestInsertScore|TestGetScoreRankings|TestInsertContract" -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/db/typed_tables.go internal/db/scores.go internal/db/profile.go
git commit -m "feat(db): convert typed_tables and scores to SQLite"
```

---

### Task 7: Rewrite Stats (Date Functions and Heatmaps)

**Files:**
- Rewrite: `internal/db/stats.go`
- Rewrite: `internal/db/heatmap.go`

- [ ] **Step 1: Convert `stats.go`**

Key conversions:
- `NOW() - INTERVAL '24 hours'` → pre-compute cutoff in Go
- `EXTRACT(DOW FROM traded_at)::int` → `CAST(strftime('%w', traded_at) AS INTEGER)`
- `EXTRACT(MONTH FROM traded_at)::int` → `CAST(strftime('%m', traded_at) AS INTEGER)`
- `make_interval(days => $1)` → pre-compute cutoff in Go
- `date_trunc('month', NOW()) - interval '11 months'` → pre-compute in Go
- `to_char(date_trunc('month', ct.traded_at), 'YYYY-MM')` → `strftime('%Y-%m', ct.traded_at)`
- `to_char(x, 'YYYY-MM-DD')` → `strftime('%Y-%m-%d', x)`
- `::bigint` → remove

Example — `GetActivityStats`:
```go
func (s *Store) GetActivityStats(ctx context.Context) (*ActivityStats, error) {
	stats := &ActivityStats{}
	cutoff24h := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	cutoff7d := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339)

	s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM events WHERE occurred_at >= ?", cutoff24h).
		Scan(&stats.EventsLast24h)
	s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM events WHERE occurred_at >= ?", cutoff7d).
		Scan(&stats.EventsLast7d)
	// ... rest unchanged except $ → ? ...
```

Example — `GetActivityHeatmap`:
```go
func (s *Store) GetActivityHeatmap(ctx context.Context, days int) ([]ActivityHeatCell, error) {
	if days <= 0 || days > 3650 {
		days = 365
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `
		SELECT CAST(strftime('%w', traded_at) AS INTEGER) AS dow,
		       CAST(strftime('%m', traded_at) AS INTEGER) AS month,
		       COUNT(*)
		FROM congressional_trades
		WHERE traded_at >= ?
		  AND traded_at >= '2000-01-01'
		  AND traded_at < '2100-01-01'
		GROUP BY dow, month
		ORDER BY dow, month
	`, cutoff)
	// ...
```

Apply the same patterns to ALL remaining stats.go functions (8 total):
- `TradesByDowMonth` — `EXTRACT(DOW/MONTH)` → `strftime('%w'/'%m')`, `make_interval(days => $3)` → pre-compute cutoff, `to_char()` → `strftime()`
- `TradesByPersonTicker` — `to_char()` → `strftime()`, `$N` → `?`
- `GetPartySectorHeatmap` — `::bigint` → remove, `$N` → `?`
- `TradesByPartySector` — `to_char()` → `strftime()`, `$N` → `?`
- `TradesByPersonMonth` — `to_char(date_trunc('month', ...))` → `strftime('%Y-%m', ...)`, `$N` → `?`
- `GetRepTickerHeatmap` — `$1` → `?`

Example — `GetRepMonthHeatmap`:
```go
windowStart := time.Now().UTC().AddDate(0, -11, 0)
windowStart = time.Date(windowStart.Year(), windowStart.Month(), 1, 0, 0, 0, 0, time.UTC)
windowEnd := time.Date(time.Now().Year(), time.Now().Month()+1, 1, 0, 0, 0, 0, time.UTC)

rows, err := s.db.QueryContext(ctx, `
    WITH window_trades AS (
        SELECT ct.*
        FROM congressional_trades ct
        WHERE ct.traded_at >= ?
          AND ct.traded_at < ?
    ),
    top_reps AS (
        SELECT person_id
        FROM window_trades
        WHERE person_id IS NOT NULL
        GROUP BY person_id
        ORDER BY COUNT(*) DESC
        LIMIT ?
    )
    SELECT p.slug, p.name, COALESCE(NULLIF(p.party,''), '?') AS party,
           strftime('%Y-%m', ct.traded_at) AS month,
           COUNT(*) AS trade_count,
           COALESCE(SUM(`+midpointExpr+`), 0) AS volume_mid
    FROM window_trades ct
    JOIN top_reps tr ON tr.person_id = ct.person_id
    JOIN persons p ON p.id = ct.person_id
    GROUP BY p.slug, p.name, p.party, month
    ORDER BY p.name, month
`, windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339), topN)
```

- [ ] **Step 2: Convert `heatmap.go`**

Replace `DISTINCT ON` with window function:
```go
rows, err := s.db.QueryContext(ctx, `
    WITH latest_scores AS (
        SELECT company_id, market_score, policy_score, insider_score, composite_score
        FROM (
            SELECT company_id, market_score, policy_score, insider_score, composite_score,
                   ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY computed_at DESC) AS rn
            FROM scores
        ) WHERE rn = 1
    )
    SELECT c.sector,
        AVG(ls.market_score), AVG(ls.policy_score),
        AVG(ls.insider_score), AVG(ls.composite_score),
        COUNT(DISTINCT c.id)
    FROM companies c
    JOIN latest_scores ls ON ls.company_id = c.id
    WHERE c.sector IS NOT NULL
    GROUP BY c.sector
    ORDER BY AVG(ls.composite_score) DESC
`)
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/db/ -run "TestActivityStats|TestHeatmap" -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/db/stats.go internal/db/heatmap.go
git commit -m "feat(db): convert stats and heatmap queries to SQLite"
```

---

### Task 8: Rewrite Tickers (LATERAL Join + Date Math)

**Files:**
- Rewrite: `internal/db/tickers.go`

- [ ] **Step 1: Convert `TopTickers`**

Replace `to_char(x, 'YYYY-MM-DD')` → `strftime('%Y-%m-%d', x)`, `::timestamptz` → remove, `$1` → `?`:

```go
q := `
SELECT
  ct.ticker,
  MAX(c.sector),
  COUNT(*),
  COUNT(DISTINCT ct.person_id),
  COUNT(DISTINCT CASE WHEN ct.trade_type = 'purchase' THEN ct.person_id END),
  COUNT(DISTINCT CASE WHEN ct.trade_type LIKE 'sale%' THEN ct.person_id END),
  COALESCE(SUM(` + midpointExpr + `), 0),
  COALESCE(SUM(CASE WHEN ct.trade_type = 'purchase' THEN ` + midpointExpr + ` ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN ct.trade_type LIKE 'sale%' THEN ` + midpointExpr + ` ELSE 0 END), 0),
  COUNT(DISTINCT CASE WHEN ct.trade_type = 'purchase' AND p.party = 'R' THEN ct.person_id END),
  COUNT(DISTINCT CASE WHEN ct.trade_type = 'purchase' AND p.party = 'D' THEN ct.person_id END),
  strftime('%Y-%m-%d', MIN(ct.traded_at)),
  strftime('%Y-%m-%d', MAX(ct.traded_at))
FROM congressional_trades ct
LEFT JOIN persons p ON p.id = ct.person_id
LEFT JOIN companies c ON c.id = ct.company_id
WHERE ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
  AND ct.traded_at >= '2000-01-01'
  AND ct.traded_at < '2100-01-01'
GROUP BY ct.ticker
ORDER BY COUNT(DISTINCT ct.person_id) DESC, COUNT(*) DESC
LIMIT ?
`
```

- [ ] **Step 2: Convert `CoTraders`**

Replace `EXTRACT(EPOCH FROM (a - b)) / 86400` → `ABS(julianday(o.traded_at) - julianday(t.traded_at))`:

```go
q := `
WITH target AS (
  SELECT ct.ticker, ct.traded_at, ct.person_id
  FROM congressional_trades ct
  JOIN persons p ON p.id = ct.person_id
  WHERE p.slug = ?
    AND ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
    AND ct.traded_at >= '2000-01-01'
    AND ct.traded_at < '2100-01-01'
),
pairs AS (
  SELECT o.person_id AS other_id, t.ticker,
         MIN(ABS(julianday(o.traded_at) - julianday(t.traded_at))) AS day_gap
  FROM target t
  JOIN congressional_trades o
    ON o.ticker = t.ticker
   AND o.person_id <> t.person_id
   AND ABS(julianday(o.traded_at) - julianday(t.traded_at)) <= ?
  WHERE o.traded_at >= '2000-01-01'
    AND o.traded_at < '2100-01-01'
  GROUP BY o.person_id, t.ticker
)
SELECT p.id, p.slug, p.name, p.party, p.state,
       COUNT(DISTINCT pairs.ticker) AS shared,
       COUNT(*) AS overlap_n,
       MIN(pairs.ticker) AS sample
FROM pairs
JOIN persons p ON p.id = pairs.other_id
GROUP BY p.id, p.slug, p.name, p.party, p.state
ORDER BY shared DESC, overlap_n DESC
LIMIT ?
`
```

- [ ] **Step 3: Convert `RoundTrips` — Replace JOIN LATERAL with correlated subquery**

The `JOIN LATERAL` finds the first sale after each buy. In SQLite, use a correlated subquery:

```go
q := `
WITH buys AS (
  SELECT ct.id, ct.person_id, ct.ticker, ct.traded_at AS buy_at,
         ` + midpointExpr + ` AS buy_amt
  FROM congressional_trades ct
  WHERE ct.trade_type = 'purchase'
    AND ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
    AND ct.traded_at >= '2000-01-01'
    AND ct.traded_at < '2100-01-01'
),
matched AS (
  SELECT b.person_id, b.ticker, b.buy_at, b.buy_amt,
         (SELECT ct2.traded_at FROM congressional_trades ct2
          WHERE ct2.person_id = b.person_id AND ct2.ticker = b.ticker
            AND ct2.trade_type LIKE 'sale%' AND ct2.traded_at > b.buy_at
            AND ct2.traded_at < '2100-01-01'
          ORDER BY ct2.traded_at ASC LIMIT 1) AS sell_at,
         (SELECT COALESCE(
            CASE
              WHEN ct2.amount_range_low IS NOT NULL AND ct2.amount_range_high IS NOT NULL
                THEN (ct2.amount_range_low + ct2.amount_range_high) / 2
              WHEN ct2.amount_range_low IS NOT NULL THEN ct2.amount_range_low
              WHEN ct2.amount_range_high IS NOT NULL THEN ct2.amount_range_high
              ELSE 0
            END, 0) FROM congressional_trades ct2
          WHERE ct2.person_id = b.person_id AND ct2.ticker = b.ticker
            AND ct2.trade_type LIKE 'sale%' AND ct2.traded_at > b.buy_at
            AND ct2.traded_at < '2100-01-01'
          ORDER BY ct2.traded_at ASC LIMIT 1) AS sell_amt
  FROM buys b
)
SELECT m.person_id, p.slug, p.name, p.party,
       m.ticker,
       strftime('%Y-%m-%d', m.buy_at),
       strftime('%Y-%m-%d', m.sell_at),
       CAST(julianday(m.sell_at) - julianday(m.buy_at) AS INTEGER),
       m.buy_amt, m.sell_amt
FROM matched m
JOIN persons p ON p.id = m.person_id
WHERE m.sell_at IS NOT NULL
  AND CAST(julianday(m.sell_at) - julianday(m.buy_at) AS INTEGER) BETWEEN 0 AND ?
  AND m.buy_amt >= ?
ORDER BY CAST(julianday(m.sell_at) - julianday(m.buy_at) AS INTEGER) ASC,
         m.buy_amt DESC
LIMIT ?
`
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/db/ -run "TestTopTickers|TestCoTraders|TestRoundTrips" -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/db/tickers.go
git commit -m "feat(db): convert tickers queries to SQLite (LATERAL → correlated subquery)"
```

---

### Task 9: Rewrite Compliance (PERCENTILE_CONT → Go-side)

**Files:**
- Rewrite: `internal/db/compliance.go`

- [ ] **Step 1: Convert `LatencyLeaderboard`**

`PERCENTILE_CONT` doesn't exist in SQLite. Fetch the raw latency values per person and compute percentiles in Go:

```go
func (s *Store) LatencyLeaderboard(ctx context.Context, minTrades, limit int) ([]LatencyRow, error) {
	if minTrades < 1 { minTrades = 1 }
	if limit <= 0 { limit = 100 }

	// Step 1: Get per-person aggregate stats (everything except percentiles)
	rows, err := s.db.QueryContext(ctx, `
		WITH scored AS (
			SELECT ct.person_id, ct.ticker,
			       julianday(ct.filed_at) - julianday(ct.traded_at) AS days
			FROM congressional_trades ct
			WHERE ct.person_id IS NOT NULL
			  AND ct.filed_at IS NOT NULL AND ct.traded_at IS NOT NULL
			  AND ct.filed_at >= ct.traded_at
			  AND ct.traded_at >= '2000-01-01'
			  AND ct.filed_at < '2100-01-01'
		)
		SELECT person_id,
		       COUNT(*) AS trades,
		       MAX(days) AS worst_days,
		       SUM(CASE WHEN days > 45 THEN 1 ELSE 0 END) AS late_count,
		       -- Collect all days as JSON array for Go-side percentile
		       '[' || GROUP_CONCAT(CAST(ROUND(days) AS INTEGER)) || ']' AS days_arr,
		       -- Worst ticker
		       (SELECT ticker FROM (
		           SELECT person_id AS pid, ticker, julianday(filed_at) - julianday(traded_at) AS d
		           FROM congressional_trades
		           WHERE person_id IS NOT NULL AND filed_at IS NOT NULL AND traded_at IS NOT NULL
		             AND filed_at >= traded_at
		       ) WHERE pid = scored.person_id ORDER BY d DESC LIMIT 1) AS worst_ticker
		FROM scored
		GROUP BY person_id
		HAVING COUNT(*) >= ?
		ORDER BY MAX(days) DESC
	`, minTrades)
	if err != nil {
		return nil, fmt.Errorf("latency leaderboard: %w", err)
	}
	defer rows.Close()

	type rawRow struct {
		personID    int
		trades      int
		worstDays   float64
		lateCount   int
		daysJSON    string
		worstTicker *string
	}
	var raw []rawRow
	for rows.Next() {
		var r rawRow
		if err := rows.Scan(&r.personID, &r.trades, &r.worstDays,
			&r.lateCount, &r.daysJSON, &r.worstTicker); err != nil {
			return nil, fmt.Errorf("scanning latency raw: %w", err)
		}
		raw = append(raw, r)
	}

	// Step 2: Compute percentiles in Go and join with person data
	out := make([]LatencyRow, 0, len(raw))
	for _, r := range raw {
		var days []float64
		json.Unmarshal([]byte(r.daysJSON), &days)
		sort.Float64s(days)

		var p Person
		s.db.QueryRowContext(ctx,
			"SELECT id, slug, name, party, state FROM persons WHERE id = ?",
			r.personID,
		).Scan(&p.ID, &p.Slug, &p.Name, &p.Party, &p.State)

		lr := LatencyRow{
			PersonID:   r.personID,
			Slug:       p.Slug,
			Name:       p.Name,
			Party:      p.Party,
			State:      p.State,
			Trades:     r.trades,
			MedianDays: int(percentile(days, 0.5)),
			P90Days:    int(percentile(days, 0.9)),
			WorstDays:  int(r.worstDays + 0.5),
			WorstTicker: r.worstTicker,
			LateCount:  r.lateCount,
		}
		if r.trades > 0 {
			lr.LatePct = float64(r.lateCount) / float64(r.trades)
		}
		out = append(out, lr)
	}

	// Sort by median descending
	sort.Slice(out, func(i, j int) bool {
		if out[i].MedianDays != out[j].MedianDays {
			return out[i].MedianDays > out[j].MedianDays
		}
		return out[i].LatePct > out[j].LatePct
	})
	if len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

// percentile computes a linear-interpolation percentile from sorted data.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 { return 0 }
	if len(sorted) == 1 { return sorted[0] }
	idx := p * float64(len(sorted)-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(sorted) { return sorted[len(sorted)-1] }
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
```

- [ ] **Step 2: Convert `LatencySummaryAll` similarly**

Pre-fetch all latency values, compute percentiles in Go.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/db/ -run "TestLatency" -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/db/compliance.go
git commit -m "feat(db): convert compliance to SQLite (PERCENTILE_CONT → Go-side)"
```

---

### Task 10: Rewrite Signals (ARRAY_AGG + date_trunc)

**Files:**
- Rewrite: `internal/db/signals.go`

- [ ] **Step 1: Convert `SwarmDetector`**

Key changes:
- `ARRAY_AGG(DISTINCT name)` → `GROUP_CONCAT(DISTINCT name)` (returns comma-separated string)
- `date_trunc('week', ct.traded_at)::date` → `date(ct.traded_at, 'weekday 0', '-6 days')`
- Scan `RepNames` as string, split on comma
- `::timestamptz` → remove

```go
// In SwarmRow, change RepNames from []string to still be []string,
// but scan as string and split:
var repNamesStr string
rows.Scan(..., &repNamesStr)
r.RepNames = strings.Split(repNamesStr, ",")
```

The date_trunc('week') → SQLite equivalent for Monday-start weeks:
```sql
date(ct.traded_at, 'weekday 0', '-6 days') AS week_start
```

- [ ] **Step 2: Convert `PartisanTickers`**

Straightforward `$N` → `?` conversion. No special Postgres features used.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/db/ -run "TestSwarm|TestPartisan" -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/db/signals.go
git commit -m "feat(db): convert signals to SQLite (ARRAY_AGG → GROUP_CONCAT)"
```

---

### Task 11: Rewrite Profile (Largest Single File)

**Files:**
- Rewrite: `internal/db/profile.go`

- [ ] **Step 1: Convert all queries in `GetPersonProfile`**

This function has 10+ queries. Apply these patterns to each:
- `$1` → `?`
- `to_char(date_trunc('month', x), 'YYYY-MM')` → `strftime('%Y-%m', x)`
- `PERCENTILE_CONT` (latency stats, query #2) → Go-side percentile (same as compliance.go)
- `date_trunc('week', x)` → `date(x, 'weekday 0', '-6 days')`
- `::BIGINT` → remove (already done in midpointExpr)

Each of the 10 queries is a standalone scan — convert them one at a time.

- [ ] **Step 2: Convert `FirstMovers`**

Replace `DISTINCT ON (ct.ticker, ct.person_id)` with window function:
```sql
WITH first_buy AS (
  SELECT ticker, person_id, name, party, traded_at FROM (
    SELECT ct.ticker, ct.person_id, p.name, p.party, ct.traded_at,
           ROW_NUMBER() OVER (PARTITION BY ct.ticker, ct.person_id ORDER BY ct.traded_at ASC) AS rn
    FROM congressional_trades ct
    JOIN persons p ON p.id = ct.person_id
    WHERE ct.trade_type = 'purchase'
      AND ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
      AND ct.traded_at IS NOT NULL
      AND ct.traded_at >= '2000-01-01'
      AND ct.traded_at < '2100-01-01'
  ) WHERE rn = 1
),
...
```

Replace `EXTRACT(EPOCH FROM (traded_at - first_date)) / 86400.0` with `julianday(traded_at) - julianday(first_date)`.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/db/ -run "TestProfile|TestFirstMovers" -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/db/profile.go
git commit -m "feat(db): convert profile queries to SQLite"
```

---

### Task 12: Rewrite Remaining Go Files

**Files:**
- Rewrite: `internal/db/graph.go`, `internal/db/entity_links.go`, `internal/db/source_meta.go`, `internal/db/api_keys.go`, `internal/db/query_index.go`, `internal/db/resolver_queries.go`, `internal/db/score_queries.go`
- Delete: `internal/db/notify.go`, `internal/db/notify_test.go`

- [ ] **Step 1: Delete notify.go and notify_test.go**

LISTEN/NOTIFY is Postgres-only and not used in the static-first architecture:
```bash
rm internal/db/notify.go internal/db/notify_test.go
```

- [ ] **Step 2: Convert `entity_links.go`**

This file has the most complex conversions beyond `$N` → `?`:

**InsertEntityLink** — uses `RETURNING`:
```go
func (s *Store) InsertEntityLink(ctx context.Context, l EntityLink) (EntityLink, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO entity_links (from_entity, from_type, to_entity, to_type, relationship, evidence_event_id)
		VALUES (?, ?, ?, ?, ?, ?)
	`, l.FromEntity, l.FromType, l.ToEntity, l.ToType, l.Relationship, l.EvidenceEventID)
	if err != nil {
		return EntityLink{}, fmt.Errorf("inserting entity link: %w", err)
	}

	// Fetch the inserted row (RETURNING not available in SQLite for all cases)
	var result EntityLink
	var discoveredStr string
	err = s.db.QueryRowContext(ctx, `
		SELECT id, from_entity, from_type, to_entity, to_type, relationship, evidence_event_id, discovered_at
		FROM entity_links
		WHERE from_entity = ? AND from_type = ? AND to_entity = ? AND to_type = ? AND relationship = ?
		ORDER BY id DESC LIMIT 1
	`, l.FromEntity, l.FromType, l.ToEntity, l.ToType, l.Relationship,
	).Scan(&result.ID, &result.FromEntity, &result.FromType,
		&result.ToEntity, &result.ToType, &result.Relationship,
		&result.EvidenceEventID, &discoveredStr)
	if err != nil {
		return EntityLink{}, fmt.Errorf("fetching inserted entity link: %w", err)
	}
	result.DiscoveredAt, _ = scanTime(discoveredStr)
	return result, nil
}
```

**InsertEntityAlias** — uses `ON CONFLICT (entity_type, (LOWER(alias))) DO NOTHING ... RETURNING` + catches `pgx.ErrNoRows`:
```go
func (s *Store) InsertEntityAlias(ctx context.Context, a EntityAlias) (EntityAlias, error) {
	// SQLite unique index uses COLLATE NOCASE, so INSERT OR IGNORE handles case-insensitive conflicts
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO entity_aliases (entity_id, entity_type, alias, source, confidence)
		VALUES (?, ?, ?, ?, ?)
	`, a.EntityID, a.EntityType, a.Alias, a.Source, a.Confidence)
	if err != nil {
		return EntityAlias{}, fmt.Errorf("inserting entity alias: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		// Conflict — alias already exists. Return zero value (matches pgx behavior).
		return EntityAlias{}, nil
	}

	// Fetch the inserted row
	var out EntityAlias
	err = s.db.QueryRowContext(ctx, `
		SELECT id, entity_id, entity_type, alias, source, confidence
		FROM entity_aliases WHERE entity_type = ? AND alias = ? COLLATE NOCASE
	`, a.EntityType, a.Alias).Scan(
		&out.ID, &out.EntityID, &out.EntityType, &out.Alias, &out.Source, &out.Confidence)
	if err != nil {
		return EntityAlias{}, fmt.Errorf("fetching inserted alias: %w", err)
	}
	return out, nil
}
```

**GetCompanyByAlias** — replace `pgx.ErrNoRows` with `sql.ErrNoRows`:
```go
// Replace LOWER($1) with ? COLLATE NOCASE:
WHERE ea.entity_type = 'company' AND ea.alias = ? COLLATE NOCASE
```

**GetEntityLinks** — scan `DiscoveredAt` as string, parse with `scanTime()`.

- [ ] **Step 3: Convert `timeline.go`**

Replace `s.db.Query/QueryRow` calls to `s.db.QueryContext/QueryRowContext`. Timeline calls `s.ListEvents` and `s.GetScoreHistory` internally — those are already converted in Tasks 5 and 6. Just need to update the method signatures to match the new DBTX interface.

- [ ] **Step 4: Convert `source_meta.go`**

Key conversions:
- `$N` → `?`
- `time.Time` params (e.g., `now` in RecordPoll) → `formatTime(now)`
- `*time.Time` scan fields → scan as `*string`, then `scanTimePtr()`
- `CASE WHEN $4 THEN $3 ELSE last_new_data_at END` — SQLite handles Go booleans differently; pass 1/0:
```go
hasNew := 0
if a.HasNewData { hasNew = 1 }
// ... WHERE ... last_new_data_at = CASE WHEN ? THEN ? ELSE last_new_data_at END
```

- [ ] **Step 5: Convert remaining files**

For each remaining file (`graph.go`, `api_keys.go`, `query_index.go`, `resolver_queries.go`, `score_queries.go`), apply the standard conversions:
- `$N` → `?`
- `s.db.Query(ctx, ...)` → `s.db.QueryContext(ctx, ...)`
- `s.db.QueryRow(ctx, ...)` → `s.db.QueryRowContext(ctx, ...)`
- `s.db.Exec(ctx, ...)` → `s.db.ExecContext(ctx, ...)`
- `RETURNING` → `LastInsertId()` or follow-up SELECT
- `DISTINCT ON` → window function
- `NOW()` → `datetime('now')` or pre-compute in Go
- `time.Time` params → `formatTime()`, `time.Time` scan → `scanTime()`
- Remove all `pgx` imports
- Remove all `pgconn` imports

- [ ] **Step 3: Fix all remaining compilation errors**

```bash
go build ./...
```

Fix any remaining pgx references, type mismatches, or import errors.

- [ ] **Step 4: Run full test suite**

```bash
go test ./internal/db/... -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/db/
git commit -m "feat(db): convert all remaining Go store files to SQLite"
```

---

### Task 13: Rewrite MQL Worker (Neon → D1)

**Files:**
- Create: `workers/query/src/d1.ts`
- Rewrite: `workers/query/src/compiler.ts`
- Rewrite: `workers/query/src/types.ts`
- Rewrite: `workers/query/src/index.ts`
- Delete: `workers/query/src/db.ts`
- Modify: `workers/query/package.json`

- [ ] **Step 1: Write the D1 executor (`d1.ts`)**

```typescript
export interface QueryResult {
  rows: Record<string, unknown>[];
  duration_ms: number;
}

export async function executeQuery(
  db: D1Database,
  sql: string,
  params: unknown[],
): Promise<QueryResult> {
  const start = Date.now();
  const stmt = db.prepare(sql).bind(...params);
  const result = await stmt.all();
  return {
    rows: (result.results ?? []) as Record<string, unknown>[],
    duration_ms: Date.now() - start,
  };
}
```

- [ ] **Step 2: Update `types.ts` — Replace DATABASE_URL with D1 binding**

```typescript
export interface Env {
  DB: D1Database;
  MQL_KV: KVNamespace;
}
```

- [ ] **Step 3: Update `index.ts` — Use D1 instead of DATABASE_URL**

Replace:
```typescript
result = await executeQuery(env.DATABASE_URL, compiled.sql, compiled.params);
```
With:
```typescript
import { executeQuery } from "./d1";
// ...
result = await executeQuery(env.DB, compiled.sql, compiled.params);
```

- [ ] **Step 4: Rewrite `compiler.ts` for SQLite dialect**

This is the largest change. Key patterns:

**a) Replace `$N` params with `?`:**
Every `$${params.length}` becomes just `?`. Remove all parameter index tracking.

**b) Replace `= ANY($N)` with `IN (?,?,?)`:**
```typescript
// Before:
params.push(filter.values);
return `c.ticker = ANY($${params.length})`;

// After:
const placeholders = filter.values.map(() => '?').join(',');
params.push(...filter.values);
return `${neg}c.ticker IN (${placeholders})`;
```

Apply this pattern everywhere `ANY()` is used: ticker, slug, party, role, committee.

**c) Replace `DISTINCT ON` in score CTE:**
```typescript
sql = `WITH latest_scores AS (
  SELECT company_id, composite_score, market_score, policy_score, insider_score
  FROM (
    SELECT company_id, composite_score, market_score, policy_score, insider_score,
           ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY computed_at DESC) AS rn
    FROM scores
  ) WHERE rn = 1
)\n${sql}`;
```

**d) Replace type casts:**
- `NULL::text` → `NULL`
- `NULL::bigint` → `NULL`
- `NULL::int` → `NULL`
- `NULL::numeric` → `NULL`
- `NULL::timestamptz` → `NULL`

In `buildProjection`, remove all `::text`, `::bigint`, etc. suffixes.

**e) Replace `date_trunc` in `getGroupKey`:**
```typescript
case "week": return "date(occurred_at, 'weekday 1', '-7 days')";
case "month": return "date(occurred_at, 'start of month')";
```

**f) Replace tariff array overlap:**
```typescript
// Before:
return `${neg}tf.affected_countries && $${params.length}`;

// After (using junction table):
const placeholders = filter.values.map(() => '?').join(',');
params.push(...filter.values);
return `${neg}EXISTS (SELECT 1 FROM tariff_countries tc WHERE tc.tariff_id = tf.id AND tc.country IN (${placeholders}))`;
```

**g) Update tariff custom joins:**
```typescript
customJoins: "LEFT JOIN tariff_hs_codes thc ON thc.tariff_id = tf.id LEFT JOIN company_hs_codes chc ON chc.hs_code = thc.hs_code LEFT JOIN companies c ON c.id = chc.company_id",
```

**h) Replace `ILIKE` with `LIKE`:**
SQLite's LIKE is case-insensitive for ASCII by default. Just change the keyword.

- [ ] **Step 5: Remove Neon dependency**

```bash
cd workers/query
npm uninstall @neondatabase/serverless
rm src/db.ts
```

- [ ] **Step 6: Run Worker tests**

```bash
cd workers/query && npx vitest run
```

Update test expectations if needed (e.g., `$1` → `?` in SQL assertions).

- [ ] **Step 7: Commit**

```bash
git add workers/query/
git commit -m "feat(worker): migrate MQL Worker from Neon to D1"
```

---

### Task 14: Update MQL Tests for SQLite Dialect

**Files:**
- Rewrite: `workers/query/src/compiler.test.ts`

- [ ] **Step 1: Update all test assertions**

Tests currently check for `$1`, `$2`, etc. Update to check for `?`:

```typescript
it("compiles a single-type query", () => {
  const q = parse("type:trade by:pelosi since:30d");
  const result = compile(q, null, []);
  expect(result.sql).toContain("congressional_trades");
  expect(result.sql).toContain("?");
  expect(result.sql).not.toContain("UNION ALL");
});
```

Tests checking for `ANY` should now check for `IN`:
```typescript
it("compiles signal filter using provided tickers", () => {
  const q = parse("type:trade signal:swarm since:30d");
  const result = compile(q, null, ["MSFT", "AAPL"]);
  expect(result.sql).toContain("c.ticker IN");
});
```

Tests checking for `DISTINCT ON` should check for `ROW_NUMBER`:
```typescript
it("compiles score filter with CTE", () => {
  const q = parse("type:trade score:>70 since:30d");
  const result = compile(q, null, []);
  expect(result.sql).toContain("latest_scores");
  expect(result.sql).toContain("ROW_NUMBER");
});
```

- [ ] **Step 2: Run tests**

```bash
cd workers/query && npx vitest run
```

Expected: All 18 tests PASS

- [ ] **Step 3: Commit**

```bash
git add workers/query/src/compiler.test.ts
git commit -m "test(worker): update compiler tests for SQLite dialect"
```

---

### Task 15: Update CI Workflow

**Files:**
- Rewrite: `.github/workflows/ingest-deploy.yml`
- Delete: `.github/workflows/deploy.yml` (old Railway deploy)

- [ ] **Step 1: Update ingest-deploy.yml**

The CI workflow now:
1. Builds the Go binary
2. Runs ingestion against a **local SQLite file** (not remote DB)
3. Exports static JSON from the local SQLite file
4. Uploads the SQLite data to D1 using `wrangler d1 execute`
5. Deploys Pages and Worker as before

```yaml
name: Ingest & Deploy to Cloudflare

on:
  schedule:
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

      - name: Run migrations (local SQLite)
        run: ./mrdn migrate
        env:
          DATABASE_URL: file:mrdn.db

      - name: Ingest (one-shot poll all sources)
        run: ./mrdn ingest-once
        env:
          DATABASE_URL: file:mrdn.db
          MRDN_POLYGON_API_KEY: ${{ secrets.MRDN_POLYGON_API_KEY }}
          MRDN_FEC_API_KEY: ${{ secrets.MRDN_FEC_API_KEY }}

      - name: Compute scores
        run: ./mrdn score-backfill --workers 4
        env:
          DATABASE_URL: file:mrdn.db

      - name: Export static JSON
        run: ./mrdn export --out dist/data
        env:
          DATABASE_URL: file:mrdn.db

      - name: Copy frontend assets
        run: cp web/static/* dist/

      - name: Deploy to Cloudflare Pages
        uses: cloudflare/wrangler-action@v3
        with:
          apiToken: ${{ secrets.CF_API_TOKEN }}
          command: pages deploy dist --project-name=mrdn

      - name: Setup Node.js
        uses: actions/setup-node@v4
        with:
          node-version: '22'

      - name: Migrate D1 schema
        run: |
          cd workers/query
          npx wrangler d1 execute mrdn-db \
            --file=../../internal/db/migrations/001_sqlite_initial.sql \
            --remote
        env:
          CLOUDFLARE_API_TOKEN: ${{ secrets.CF_API_TOKEN }}

      - name: Upload data to D1
        run: |
          # Export each table as safe INSERT statements using .mode insert
          # This handles multi-line TEXT values (JSON) correctly
          TABLES="companies persons events congressional_trades contracts sanctions tariffs tariff_hs_codes tariff_countries warn_filings donations lobbying court_filings court_filing_parties market_data insider_trades person_committees company_hs_codes score_weights bills entity_aliases entity_links source_meta scores api_keys party_history"
          > d1-data.sql
          for TABLE in $TABLES; do
            echo "-- $TABLE" >> d1-data.sql
            sqlite3 mrdn.db ".mode insert $TABLE" "SELECT * FROM $TABLE;" >> d1-data.sql 2>/dev/null || true
          done

          # Split into chunks < 1MB for D1 API limits
          split -l 500 -d d1-data.sql d1-chunk-
          cd workers/query
          for CHUNK in ../../d1-chunk-*; do
            npx wrangler d1 execute mrdn-db --file="$CHUNK" --remote || true
          done
        env:
          CLOUDFLARE_API_TOKEN: ${{ secrets.CF_API_TOKEN }}

      - name: Install Worker dependencies
        run: cd workers/query && npm ci

      - name: Run Worker tests
        run: cd workers/query && npx vitest run

      - name: Deploy MQL Worker
        run: cd workers/query && npx wrangler deploy
        env:
          CLOUDFLARE_API_TOKEN: ${{ secrets.CF_API_TOKEN }}

      - name: Upload signal and metadata to KV
        run: |
          EXPORTED_AT=$(python3 -c "import json; print(json.load(open('dist/data/meta.json'))['exported_at'])")
          npx wrangler kv key put --namespace-id=${{ vars.MQL_KV_NAMESPACE_ID }} "meta:data_as_of" "$EXPORTED_AT"

          for SIGNAL_FILE in dist/data/signals/swarms.json dist/data/signals/first-movers.json dist/data/signals/round-trips.json dist/data/signals/partisan-consensus.json dist/data/signals/partisan-contrarian.json; do
            SIGNAL_NAME=$(basename "$SIGNAL_FILE" .json)
            python3 -c "
          import json, sys
          data = json.load(open('$SIGNAL_FILE'))
          items = data.get('data', [])
          tickers = list(set(item.get('ticker', '') for item in items if item.get('ticker')))
          print(json.dumps(tickers))
          " | npx wrangler kv key put --namespace-id=${{ vars.MQL_KV_NAMESPACE_ID }} "signal:$SIGNAL_NAME" --pipe
          done
        env:
          CLOUDFLARE_API_TOKEN: ${{ secrets.CF_API_TOKEN }}

      - name: Prune old data (local)
        run: ./mrdn prune --keep-days 90
        env:
          DATABASE_URL: file:mrdn.db
```

- [ ] **Step 2: Delete old Railway deploy workflow**

```bash
rm .github/workflows/deploy.yml
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/
git commit -m "ci: update workflow for D1 (local SQLite ingest → D1 upload)"
```

---

### Task 16: Update CMD Entrypoints

**Files:**
- Modify: All files in `cmd/mrdn/` that call `db.Connect()` or `db.Migrate()`

- [ ] **Step 1: Find all db.Connect and db.Migrate call sites**

```bash
grep -rn "db.Connect\|db.Migrate\|pgxpool" cmd/
```

- [ ] **Step 2: Update signatures**

`db.Connect` now returns `*sql.DB` instead of `*pgxpool.Pool`.
`db.Migrate` now takes `*sql.DB` instead of `*pgxpool.Pool`.

Replace:
```go
pool, err := db.Connect(ctx, dsn)
defer pool.Close()
db.Migrate(ctx, pool)
store := db.NewStore(pool)
```
With:
```go
d, err := db.Connect(ctx, dsn)
defer d.Close()
db.Migrate(ctx, d)
store := db.NewStore(d)
```

Remove all `pgxpool` imports. Replace `pool` variable names with `d` or `database`.

- [ ] **Step 3: Remove any `db.NotifyNewEvent` or `db.ListenNewEvents` calls**

```bash
grep -rn "NotifyNewEvent\|ListenNewEvents" cmd/ internal/
```

Remove or comment out these calls — they're Postgres-only.

- [ ] **Step 4: Build**

```bash
go build ./cmd/mrdn/
```

- [ ] **Step 5: Commit**

```bash
git add cmd/
git commit -m "feat(cmd): update entrypoints for SQLite/D1"
```

---

### Task 17: Full Test Suite and Smoke Test

**Files:** None new — validation only.

- [ ] **Step 1: Run all Go tests**

```bash
go test ./... -v -count=1
```

Fix any remaining failures.

- [ ] **Step 2: Run Worker tests**

```bash
cd workers/query && npx vitest run
```

- [ ] **Step 3: Local smoke test**

```bash
./mrdn migrate
./mrdn ingest-once  # with API keys set
./mrdn export --out dist/data
ls dist/data/
```

Verify JSON files are generated.

- [ ] **Step 4: Test MQL Worker locally with D1**

```bash
cd workers/query
npx wrangler d1 execute mrdn-db --local --file=../../internal/db/migrations/001_sqlite_initial.sql
npx wrangler dev
# In another terminal:
curl -X POST http://localhost:8787/api/query -d '{"q":"type:trade since:30d"}'
```

- [ ] **Step 5: Commit any remaining fixes**

```bash
git add -A
git commit -m "fix: resolve remaining SQLite migration issues"
```

---

### Task 18: Deploy and Verify

- [ ] **Step 1: Push to master**

```bash
git push origin master
```

- [ ] **Step 2: Trigger the workflow**

```bash
gh workflow run "Ingest & Deploy to Cloudflare"
```

- [ ] **Step 3: Monitor the run**

```bash
gh run list --workflow=ingest-deploy.yml --limit 1
gh run watch
```

- [ ] **Step 4: Verify MQL Worker is live**

```bash
curl -X POST https://mrdn-query.<your-subdomain>.workers.dev/api/query \
  -H 'Content-Type: application/json' \
  -d '{"q":"type:trade since:30d"}'
```

- [ ] **Step 5: Clean up Neon references**

Remove any remaining Neon-related code, env vars, or documentation:
```bash
grep -rn "neon\|NEON\|neondatabase" --include="*.go" --include="*.ts" --include="*.toml" --include="*.yml" .
```

- [ ] **Step 6: Final commit**

```bash
git add -A
git commit -m "chore: remove all Neon/Postgres references"
```

---

## Risk Register

| Risk | Impact | Mitigation |
|------|--------|------------|
| D1 5GB free tier exceeded | Data loss / billing | Prune aggressively, monitor with `wrangler d1 info` |
| SQLite performance on complex analytics | Slow queries | Pre-compute via export (static-first), add indexes |
| D1 REST API rate limits in CI | Ingest fails | Batch SQL statements, use `.dump` + single execute |
| `PERCENTILE_CONT` accuracy in Go | Slightly different results | Use standard linear interpolation, results will be close enough |
| Array columns → junction tables | Schema divergence | Single migration file, test thoroughly |
| `time.Time` ↔ TEXT roundtrip | Timezone bugs | Always store/parse as UTC RFC3339, never local time |
| D1 row size limits (1MB per row) | Large event_data rejected | Already capped at 64KB in Go validation |
