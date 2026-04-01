# MRDN Phase 1: Foundation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Standing Go binary with Postgres schema, config, DB layer, seed data, basic API health endpoint, and CLI skeleton — the foundation everything else builds on.

**Architecture:** Single Go module (`github.com/arclighteng/mrdn`) using cobra for CLI, chi for HTTP routing, pgx for Postgres. All database access through a `Store` interface for testability. Schema managed via embedded SQL migrations.

**Tech Stack:** Go 1.22+, PostgreSQL 15+ (Supabase), cobra (CLI), chi (router), pgx (Postgres driver), testify (assertions)

**Spec:** `docs/superpowers/specs/2026-04-01-mrdn-design.md`

---

## File Structure

```
mrdn/
├── cmd/
│   └── mrdn/
│       └── main.go                 -- entrypoint, cobra root command
├── internal/
│   ├── config/
│   │   ├── config.go               -- env var loading, validation
│   │   └── config_test.go
│   ├── db/
│   │   ├── db.go                   -- connection pool setup
│   │   ├── migrate.go              -- migration runner (embedded SQL)
│   │   ├── migrate_test.go
│   │   ├── companies.go            -- company CRUD operations
│   │   ├── companies_test.go
│   │   ├── events.go               -- event insert + query
│   │   ├── events_test.go
│   │   ├── source_meta.go          -- source freshness CRUD
│   │   ├── source_meta_test.go
│   │   ├── scores.go               -- score read/write
│   │   ├── scores_test.go
│   │   └── migrations/
│   │       └── 001_initial.sql     -- full DDL from spec
│   ├── api/
│   │   ├── server.go               -- chi router setup, middleware
│   │   ├── server_test.go
│   │   ├── health.go               -- GET /health
│   │   └── health_test.go
│   ├── seeddata/
│   │   ├── seeddata.go             -- embedded seed JSON
│   │   └── tech_companies.json     -- top 100 tech tickers
│   └── cli/
│       ├── root.go                 -- cobra root command + global flags
│       ├── serve.go                -- `mrdn serve` command
│       ├── migrate.go              -- `mrdn migrate` command
│       ├── seed.go                 -- `mrdn seed` command
│       └── sources.go              -- `mrdn sources` command
├── go.mod
├── go.sum
├── .env.example
└── docs/
```

---

### Task 1: Go Module + Project Scaffolding

**Files:**
- Create: `go.mod`
- Create: `cmd/mrdn/main.go`
- Create: `internal/cli/root.go`
- Create: `.env.example`
- Create: `.gitignore`

- [ ] **Step 1: Initialize Go module**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go mod init github.com/arclighteng/mrdn
```

Expected: `go.mod` created with module path.

- [ ] **Step 2: Install core dependencies**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go get github.com/spf13/cobra@latest
go get github.com/go-chi/chi/v5@latest
go get github.com/jackc/pgx/v5@latest
go get github.com/stretchr/testify@latest
```

- [ ] **Step 3: Create .gitignore**

Create `.gitignore`:
```
.env
mrdn
mrdn.exe
*.test
coverage.out
```

- [ ] **Step 4: Create .env.example**

Create `.env.example`:
```bash
# Required
DATABASE_URL=postgresql://user:pass@host:5432/mrdn

# Optional
MRDN_PORT=8080
MRDN_LOG_LEVEL=info
```

- [ ] **Step 5: Create root CLI command**

Create `internal/cli/root.go`:
```go
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "mrdn",
	Short: "MRDN — public data aggregation platform",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 6: Create entrypoint**

Create `cmd/mrdn/main.go`:
```go
package main

import "github.com/arclighteng/mrdn/internal/cli"

func main() {
	cli.Execute()
}
```

- [ ] **Step 7: Verify it compiles and runs**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go run ./cmd/mrdn --help
```

Expected: Help text showing "MRDN — public data aggregation platform"

- [ ] **Step 8: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add go.mod go.sum cmd/ internal/cli/root.go .gitignore .env.example
git commit -m "feat: project scaffolding — Go module, cobra CLI, entrypoint"
```

---

### Task 2: Config Loading

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write failing test for config loading**

Create `internal/config/config_test.go`:
```go
package config_test

import (
	"os"
	"testing"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	os.Unsetenv("DATABASE_URL")
	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL")
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://localhost/mrdn")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "postgresql://localhost/mrdn", cfg.DatabaseURL)
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_OverridePort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://localhost/mrdn")
	t.Setenv("MRDN_PORT", "9090")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, 9090, cfg.Port)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go test ./internal/config/... -v
```

Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement config**

Create `internal/config/config.go`:
```go
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL string
	Port        int
	LogLevel    string
}

func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	port := 8080
	if p := os.Getenv("MRDN_PORT"); p != "" {
		var err error
		port, err = strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("MRDN_PORT must be a number: %w", err)
		}
	}

	logLevel := "info"
	if l := os.Getenv("MRDN_LOG_LEVEL"); l != "" {
		logLevel = l
	}

	return &Config{
		DatabaseURL: dbURL,
		Port:        port,
		LogLevel:    logLevel,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go test ./internal/config/... -v
```

Expected: 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/config/
git commit -m "feat: config loading from env vars with defaults"
```

---

### Task 3: Database Schema (DDL)

**Files:**
- Create: `internal/db/migrations/001_initial.sql`

- [ ] **Step 1: Write the full DDL migration**

Create `internal/db/migrations/001_initial.sql`:
```sql
-- MRDN initial schema
-- All tables from spec: 2026-04-01-mrdn-design.md
-- Note: schema_migrations is created by the Go migration runner, not here.

CREATE TABLE IF NOT EXISTS companies (
    id SERIAL PRIMARY KEY,
    ticker TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    sector TEXT,
    subsector TEXT,
    naics_code TEXT,
    market_cap_bucket TEXT
);

CREATE TABLE IF NOT EXISTS events (
    id SERIAL PRIMARY KEY,
    source TEXT NOT NULL,
    source_id TEXT,
    company_id INT REFERENCES companies,
    event_type TEXT NOT NULL,
    event_data JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, source_id)
);

CREATE TABLE IF NOT EXISTS persons (
    id SERIAL PRIMARY KEY,
    slug TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    role TEXT NOT NULL,
    tier INT NOT NULL,
    branch TEXT,
    linked_person_id INT REFERENCES persons,
    linked_relationship TEXT,
    disclosure_source TEXT
);

CREATE TABLE IF NOT EXISTS congressional_trades (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    person_id INT REFERENCES persons,
    company_id INT REFERENCES companies,
    owner_type TEXT,
    ticker TEXT,
    trade_type TEXT,
    amount_range_low INT,
    amount_range_high INT,
    filed_at TIMESTAMPTZ,
    traded_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS contracts (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    agency TEXT,
    amount_cents BIGINT,
    action_type TEXT,
    description TEXT,
    awarded_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS sanctions (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    entity_name TEXT,
    entity_type TEXT,
    program TEXT,
    country TEXT,
    added_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS tariffs (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    hs_codes TEXT[],
    affected_countries TEXT[],
    action_type TEXT,
    effective_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS warn_filings (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    state TEXT,
    city TEXT,
    workers_affected INT,
    layoff_date DATE,
    filed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS donations (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    donor_name TEXT,
    donor_type TEXT,
    donor_employer TEXT,
    recipient TEXT,
    recipient_person_id INT REFERENCES persons,
    recipient_type TEXT,
    amount_cents BIGINT,
    donated_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS lobbying (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    client_company_id INT REFERENCES companies,
    registrant TEXT,
    client TEXT,
    specific_issues TEXT,
    amount_cents BIGINT,
    period_start DATE,
    period_end DATE,
    filed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS court_filings (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    case_number TEXT,
    court TEXT,
    parties TEXT[],
    filing_type TEXT,
    filed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS market_data (
    id SERIAL PRIMARY KEY,
    company_id INT REFERENCES companies NOT NULL,
    source TEXT NOT NULL,
    data_type TEXT NOT NULL,
    price_cents BIGINT,
    volume BIGINT,
    change_pct NUMERIC(8,4),
    recorded_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS insider_trades (
    id SERIAL PRIMARY KEY,
    event_id INT REFERENCES events,
    company_id INT REFERENCES companies,
    filer_name TEXT,
    filer_title TEXT,
    trade_type TEXT,
    shares INT,
    price_cents BIGINT,
    filed_at TIMESTAMPTZ,
    traded_at TIMESTAMPTZ
);

-- Supporting tables

CREATE TABLE IF NOT EXISTS person_committees (
    id SERIAL PRIMARY KEY,
    person_id INT REFERENCES persons NOT NULL,
    committee_name TEXT NOT NULL,
    committee_code TEXT,
    start_date DATE,
    end_date DATE
);

CREATE TABLE IF NOT EXISTS company_hs_codes (
    id SERIAL PRIMARY KEY,
    company_id INT REFERENCES companies NOT NULL,
    hs_code TEXT NOT NULL,
    source TEXT,
    confidence NUMERIC(3,2)
);

CREATE TABLE IF NOT EXISTS score_weights (
    id SERIAL PRIMARY KEY,
    version INT UNIQUE NOT NULL,
    weights JSONB NOT NULL,
    active BOOLEAN DEFAULT false,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS bills (
    id SERIAL PRIMARY KEY,
    bill_number TEXT UNIQUE NOT NULL,
    title TEXT,
    status TEXT,
    congress INT,
    introduced_at DATE,
    last_action_at DATE,
    source TEXT
);

-- Entity resolution

CREATE TABLE IF NOT EXISTS entity_aliases (
    id SERIAL PRIMARY KEY,
    entity_id INT NOT NULL,
    entity_type TEXT NOT NULL,
    alias TEXT NOT NULL,
    source TEXT,
    confidence NUMERIC(3,2),
    auto_applied BOOLEAN DEFAULT false
);

CREATE TABLE IF NOT EXISTS entity_links (
    id SERIAL PRIMARY KEY,
    from_entity INT NOT NULL,
    from_type TEXT NOT NULL,
    to_entity INT NOT NULL,
    to_type TEXT NOT NULL,
    relationship TEXT NOT NULL,
    evidence_event_id INT REFERENCES events,
    discovered_at TIMESTAMPTZ DEFAULT NOW()
);

-- Freshness tracking

CREATE TABLE IF NOT EXISTS source_meta (
    id SERIAL PRIMARY KEY,
    source_name TEXT UNIQUE NOT NULL,
    expected_lag TEXT,
    last_successful_poll TIMESTAMPTZ,
    last_new_data_at TIMESTAMPTZ,
    poll_interval_seconds INT,
    status TEXT DEFAULT 'healthy' CHECK (status IN ('healthy', 'degraded', 'stale', 'down'))
);

-- Scores

CREATE TABLE IF NOT EXISTS scores (
    id SERIAL PRIMARY KEY,
    company_id INT REFERENCES companies NOT NULL,
    market_score NUMERIC(5,2),
    policy_score NUMERIC(5,2),
    insider_score NUMERIC(5,2),
    composite_score NUMERIC(5,2),
    weight_version INT REFERENCES score_weights(version),
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- API keys

CREATE TABLE IF NOT EXISTS api_keys (
    id SERIAL PRIMARY KEY,
    key_hash TEXT UNIQUE NOT NULL,
    label TEXT,
    rate_limit INT DEFAULT 600,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Critical indexes

CREATE INDEX IF NOT EXISTS idx_events_company_occurred ON events(company_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_market_data_company_recorded ON market_data(company_id, recorded_at);
CREATE INDEX IF NOT EXISTS idx_entity_links_from ON entity_links(from_entity, from_type);
CREATE INDEX IF NOT EXISTS idx_entity_links_to ON entity_links(to_entity, to_type);
CREATE INDEX IF NOT EXISTS idx_scores_company_computed ON scores(company_id, computed_at);
CREATE INDEX IF NOT EXISTS idx_congressional_trades_company ON congressional_trades(company_id);
CREATE INDEX IF NOT EXISTS idx_person_committees_person ON person_committees(person_id);

-- Seed default score weights (v1)

INSERT INTO score_weights (version, weights, active)
VALUES (1, '{"market": 0.35, "policy": 0.40, "insider": 0.25, "market_price_trend": 0.30, "market_volume_anomaly": 0.30, "market_insider_activity": 0.40, "policy_tariff": 0.25, "policy_sanctions": 0.25, "policy_contracts": 0.25, "policy_court": 0.25, "insider_congressional": 0.40, "insider_lobbying": 0.30, "insider_donations": 0.30}', true)
ON CONFLICT (version) DO NOTHING;

-- Seed source_meta for launch sources

INSERT INTO source_meta (source_name, expected_lag, poll_interval_seconds, status) VALUES
    ('polygon', '1 day', 86400, 'healthy'),
    ('finnhub', 'seconds', 0, 'healthy'),
    ('edgar_form4', 'same day', 3600, 'healthy'),
    ('ofac_sdn', 'minutes', 1800, 'healthy'),
    ('usaspending', '1-2 days', 86400, 'healthy'),
    ('federal_register', '1 hour', 3600, 'healthy'),
    ('fec', '1-7 days', 86400, 'healthy'),
    ('efds_senate', '30-45 days', 3600, 'healthy')
ON CONFLICT (source_name) DO NOTHING;
```

- [ ] **Step 2: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/db/migrations/
git commit -m "feat: initial database schema — all tables, indexes, seed data"
```

---

### Task 4: Database Connection + Migration Runner

**Files:**
- Create: `internal/db/db.go`
- Create: `internal/db/migrate.go`
- Create: `internal/db/migrate_test.go`
- Modify: `internal/cli/root.go`
- Create: `internal/cli/migrate.go`

- [ ] **Step 1: Write failing test for migration runner**

Create `internal/db/migrate_test.go`:
```go
package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/require"
)

func TestMigrate_CreatesTablesOnce(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Run migrations twice — second run should be a no-op
	err = db.Migrate(ctx, pool)
	require.NoError(t, err)

	err = db.Migrate(ctx, pool)
	require.NoError(t, err)

	// Verify companies table exists
	var exists bool
	err = pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'companies')").Scan(&exists)
	require.NoError(t, err)
	require.True(t, exists)

	// Verify source_meta was seeded
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM source_meta").Scan(&count)
	require.NoError(t, err)
	require.Greater(t, count, 0)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go test ./internal/db/... -v -run TestMigrate
```

Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement database connection**

Create `internal/db/db.go`:
```go
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}
```

- [ ] **Step 4: Implement migration runner**

Create `internal/db/migrate.go`:
```go
package db

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_initial.sql
var migration001 string

var migrations = []struct {
	version int
	sql     string
}{
	{1, migration001},
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// Ensure schema_migrations table exists
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}

	for _, m := range migrations {
		var applied bool
		err := pool.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", m.version,
		).Scan(&applied)
		if err != nil {
			return fmt.Errorf("checking migration %d: %w", m.version, err)
		}
		if applied {
			continue
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("beginning transaction for migration %d: %w", m.version, err)
		}

		if _, err := tx.Exec(ctx, m.sql); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("running migration %d: %w", m.version, err)
		}

		if _, err := tx.Exec(ctx,
			"INSERT INTO schema_migrations (version) VALUES ($1)", m.version,
		); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("recording migration %d: %w", m.version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("committing migration %d: %w", m.version, err)
		}
	}

	return nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
DATABASE_URL=postgresql://... go test ./internal/db/... -v -run TestMigrate
```

Expected: PASS (or SKIP if no DATABASE_URL).

- [ ] **Step 6: Add `mrdn migrate` CLI command**

Create `internal/cli/migrate.go`:
```go
package cli

import (
	"context"
	"fmt"
	"log"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx := context.Background()
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		if err := db.Migrate(ctx, pool); err != nil {
			return fmt.Errorf("running migrations: %w", err)
		}

		log.Println("migrations complete")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
```

- [ ] **Step 7: Verify CLI compiles**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go run ./cmd/mrdn migrate --help
```

Expected: Help text for migrate command.

- [ ] **Step 8: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/db/db.go internal/db/migrate.go internal/db/migrate_test.go internal/cli/migrate.go
git commit -m "feat: database connection pool + migration runner with embedded SQL"
```

---

### Task 5: Company DB Layer

**Files:**
- Create: `internal/db/companies.go`
- Create: `internal/db/companies_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/db/companies_test.go`:
```go
package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *db.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, pool))
	t.Cleanup(func() { pool.Close() })
	return db.NewStore(pool)
}

func TestUpsertCompany(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	c, err := store.UpsertCompany(ctx, db.Company{
		Ticker:    "TEST",
		Name:      "Test Corp",
		Sector:    db.StrPtr("Technology"),
		Subsector: db.StrPtr("Software"),
	})
	require.NoError(t, err)
	assert.Equal(t, "TEST", c.Ticker)
	assert.Greater(t, c.ID, 0)

	// Upsert again — should update, not duplicate
	c2, err := store.UpsertCompany(ctx, db.Company{
		Ticker: "TEST",
		Name:   "Test Corp Updated",
		Sector: db.StrPtr("Technology"),
	})
	require.NoError(t, err)
	assert.Equal(t, c.ID, c2.ID)
	assert.Equal(t, "Test Corp Updated", c2.Name)
	assert.Nil(t, c2.NAICSCode) // NULL columns scan correctly

	// Clean up
	store.DeleteCompany(ctx, c.ID)
}

func TestGetCompanyByTicker(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.UpsertCompany(ctx, db.Company{
		Ticker: "LOOK",
		Name:   "Lookup Corp",
		Sector: db.StrPtr("Technology"),
	})
	require.NoError(t, err)

	found, err := store.GetCompanyByTicker(ctx, "LOOK")
	require.NoError(t, err)
	assert.Equal(t, "Lookup Corp", found.Name)

	_, err = store.GetCompanyByTicker(ctx, "NOPE")
	assert.Error(t, err)

	store.DeleteCompany(ctx, found.ID)
}

func TestListCompanies(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	testSector := "TestSector_List"
	_, err := store.UpsertCompany(ctx, db.Company{Ticker: "LST1", Name: "List One", Sector: db.StrPtr(testSector)})
	require.NoError(t, err)
	_, err = store.UpsertCompany(ctx, db.Company{Ticker: "LST2", Name: "List Two", Sector: db.StrPtr(testSector)})
	require.NoError(t, err)

	companies, err := store.ListCompanies(ctx, db.CompanyFilter{Sector: testSector, Limit: 10})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(companies), 2)

	// Clean up
	for _, c := range companies {
		if c.Ticker == "LST1" || c.Ticker == "LST2" {
			store.DeleteCompany(ctx, c.ID)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go test ./internal/db/... -v -run TestUpsert
```

Expected: FAIL — `db.Store` not defined.

- [ ] **Step 3: Implement Store and company operations**

Create `internal/db/companies.go`:
```go
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

type Company struct {
	ID              int     `json:"id"`
	Ticker          string  `json:"ticker"`
	Name            string  `json:"name"`
	Sector          *string `json:"sector,omitempty"`
	Subsector       *string `json:"subsector,omitempty"`
	NAICSCode       *string `json:"naics_code,omitempty"`
	MarketCapBucket *string `json:"market_cap_bucket,omitempty"`
}

type CompanyFilter struct {
	Sector string
	Limit  int
	Offset int
}

// StrPtr is a helper for creating *string values in test code and seed data.
func StrPtr(s string) *string { return &s }

func (s *Store) UpsertCompany(ctx context.Context, c Company) (Company, error) {
	var result Company
	err := s.pool.QueryRow(ctx, `
		INSERT INTO companies (ticker, name, sector, subsector, naics_code, market_cap_bucket)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (ticker) DO UPDATE SET
			name = EXCLUDED.name,
			sector = EXCLUDED.sector,
			subsector = EXCLUDED.subsector,
			naics_code = EXCLUDED.naics_code,
			market_cap_bucket = EXCLUDED.market_cap_bucket
		RETURNING id, ticker, name, sector, subsector, naics_code, market_cap_bucket
	`, c.Ticker, c.Name, c.Sector, c.Subsector, c.NAICSCode, c.MarketCapBucket,
	).Scan(&result.ID, &result.Ticker, &result.Name, &result.Sector,
		&result.Subsector, &result.NAICSCode, &result.MarketCapBucket)
	if err != nil {
		return Company{}, fmt.Errorf("upserting company %s: %w", c.Ticker, err)
	}
	return result, nil
}

func (s *Store) GetCompanyByTicker(ctx context.Context, ticker string) (Company, error) {
	var c Company
	err := s.pool.QueryRow(ctx, `
		SELECT id, ticker, name, sector, subsector, naics_code, market_cap_bucket
		FROM companies WHERE ticker = $1
	`, ticker).Scan(&c.ID, &c.Ticker, &c.Name, &c.Sector,
		&c.Subsector, &c.NAICSCode, &c.MarketCapBucket)
	if err != nil {
		return Company{}, fmt.Errorf("getting company %s: %w", ticker, err)
	}
	return c, nil
}

func (s *Store) ListCompanies(ctx context.Context, f CompanyFilter) ([]Company, error) {
	query := "SELECT id, ticker, name, sector, subsector, naics_code, market_cap_bucket FROM companies WHERE 1=1"
	args := []any{}
	argN := 1

	if f.Sector != "" {
		query += fmt.Sprintf(" AND sector = $%d", argN)
		args = append(args, f.Sector)
		argN++
	}

	query += " ORDER BY ticker"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, f.Limit)
		argN++
	}
	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing companies: %w", err)
	}
	defer rows.Close()

	var companies []Company
	for rows.Next() {
		var c Company
		if err := rows.Scan(&c.ID, &c.Ticker, &c.Name, &c.Sector,
			&c.Subsector, &c.NAICSCode, &c.MarketCapBucket); err != nil {
			return nil, fmt.Errorf("scanning company: %w", err)
		}
		companies = append(companies, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating companies: %w", err)
	}
	return companies, nil
}

func (s *Store) DeleteCompany(ctx context.Context, id int) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM companies WHERE id = $1", id)
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
DATABASE_URL=postgresql://... go test ./internal/db/... -v -run "TestUpsert|TestGetCompany|TestListCompanies"
```

Expected: All 3 tests PASS (or SKIP if no DATABASE_URL).

- [ ] **Step 5: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/db/companies.go internal/db/companies_test.go
git commit -m "feat: company CRUD — upsert, get by ticker, list with filters"
```

---

### Task 6: Events DB Layer

**Files:**
- Create: `internal/db/events.go`
- Create: `internal/db/events_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/db/events_test.go`:
```go
package db_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertEvent_Dedup(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	srcID := "dedup-001"
	evt := db.Event{
		Source:     "test",
		SourceID:   &srcID,
		EventType:  "test_event",
		EventData:  json.RawMessage(`{"foo": "bar"}`),
		OccurredAt: time.Now().UTC(),
	}

	id1, err := store.InsertEvent(ctx, evt)
	require.NoError(t, err)
	assert.Greater(t, id1, 0)

	// Same source + source_id should be ignored (dedup)
	id2, err := store.InsertEvent(ctx, evt)
	require.NoError(t, err)
	assert.Equal(t, id1, id2)
}

func TestListEvents(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	listSrcID := "list-001"
	store.InsertEvent(ctx, db.Event{
		Source:     "test",
		SourceID:   &listSrcID,
		EventType:  "test_event",
		EventData:  json.RawMessage(`{}`),
		OccurredAt: time.Now().UTC(),
	})

	events, err := store.ListEvents(ctx, db.EventFilter{Source: "test", Limit: 10})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(events), 1)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go test ./internal/db/... -v -run TestInsertEvent
```

Expected: FAIL — `db.Event` not defined.

- [ ] **Step 3: Implement event operations**

Create `internal/db/events.go`:
```go
package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type Event struct {
	ID         int             `json:"id"`
	Source     string          `json:"source"`
	SourceID   *string         `json:"source_id,omitempty"`
	CompanyID  *int            `json:"company_id,omitempty"`
	EventType  string          `json:"event_type"`
	EventData  json.RawMessage `json:"event_data"`
	OccurredAt time.Time       `json:"occurred_at"`
	IngestedAt time.Time       `json:"ingested_at"`
}

// Note: UNIQUE (source, source_id) does not trigger on NULL source_id.
// Events without a source_id are not deduped — the ingestion worker must
// handle dedup for those sources before calling InsertEvent.

type EventFilter struct {
	Source    string
	EventType string
	CompanyID *int
	Since     *time.Time
	Limit     int
	Offset    int
}

func (s *Store) InsertEvent(ctx context.Context, e Event) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO events (source, source_id, company_id, event_type, event_data, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (source, source_id) DO UPDATE SET source = EXCLUDED.source
		RETURNING id
	`, e.Source, e.SourceID, e.CompanyID, e.EventType, e.EventData, e.OccurredAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("inserting event: %w", err)
	}
	return id, nil
}

func (s *Store) GetEvent(ctx context.Context, id int) (Event, error) {
	var e Event
	err := s.pool.QueryRow(ctx, `
		SELECT id, source, source_id, company_id, event_type, event_data, occurred_at, ingested_at
		FROM events WHERE id = $1
	`, id).Scan(&e.ID, &e.Source, &e.SourceID, &e.CompanyID, &e.EventType,
		&e.EventData, &e.OccurredAt, &e.IngestedAt)
	if err != nil {
		return Event{}, fmt.Errorf("getting event %d: %w", id, err)
	}
	return e, nil
}

func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]Event, error) {
	query := "SELECT id, source, source_id, company_id, event_type, event_data, occurred_at, ingested_at FROM events WHERE 1=1"
	args := []any{}
	argN := 1

	if f.Source != "" {
		query += fmt.Sprintf(" AND source = $%d", argN)
		args = append(args, f.Source)
		argN++
	}
	if f.EventType != "" {
		query += fmt.Sprintf(" AND event_type = $%d", argN)
		args = append(args, f.EventType)
		argN++
	}
	if f.CompanyID != nil {
		query += fmt.Sprintf(" AND company_id = $%d", argN)
		args = append(args, *f.CompanyID)
		argN++
	}
	if f.Since != nil {
		query += fmt.Sprintf(" AND occurred_at >= $%d", argN)
		args = append(args, *f.Since)
		argN++
	}

	query += " ORDER BY occurred_at DESC"

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit)
	argN++

	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Source, &e.SourceID, &e.CompanyID, &e.EventType,
			&e.EventData, &e.OccurredAt, &e.IngestedAt); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events: %w", err)
	}
	return events, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
DATABASE_URL=postgresql://... go test ./internal/db/... -v -run "TestInsertEvent|TestListEvents"
```

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/db/events.go internal/db/events_test.go
git commit -m "feat: event insert with dedup + list with filters"
```

---

### Task 7: Source Meta DB Layer

**Files:**
- Create: `internal/db/source_meta.go`
- Create: `internal/db/source_meta_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/db/source_meta_test.go`:
```go
package db_test

import (
	"context"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListSourceMeta(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	sources, err := store.ListSourceMeta(ctx)
	require.NoError(t, err)
	// Seeded in migration
	assert.GreaterOrEqual(t, len(sources), 1)
}

func TestUpdateSourceStatus(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	err := store.RecordPoll(ctx, "polygon", true)
	require.NoError(t, err)

	src, err := store.GetSourceMeta(ctx, "polygon")
	require.NoError(t, err)
	assert.Equal(t, "healthy", src.Status)
	assert.NotNil(t, src.LastSuccessfulPoll)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go test ./internal/db/... -v -run TestListSourceMeta
```

Expected: FAIL.

- [ ] **Step 3: Implement source meta operations**

Create `internal/db/source_meta.go`:
```go
package db

import (
	"context"
	"fmt"
	"time"
)

type SourceMeta struct {
	ID                  int        `json:"id"`
	SourceName          string     `json:"source_name"`
	ExpectedLag         string     `json:"expected_lag"`
	LastSuccessfulPoll  *time.Time `json:"last_successful_poll"`
	LastNewDataAt       *time.Time `json:"last_new_data_at"`
	PollIntervalSeconds int        `json:"poll_interval_seconds"`
	Status              string     `json:"status"`
}

func (s *Store) ListSourceMeta(ctx context.Context) ([]SourceMeta, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, source_name, expected_lag, last_successful_poll,
			   last_new_data_at, poll_interval_seconds, status
		FROM source_meta ORDER BY source_name
	`)
	if err != nil {
		return nil, fmt.Errorf("listing source meta: %w", err)
	}
	defer rows.Close()

	var sources []SourceMeta
	for rows.Next() {
		var sm SourceMeta
		if err := rows.Scan(&sm.ID, &sm.SourceName, &sm.ExpectedLag,
			&sm.LastSuccessfulPoll, &sm.LastNewDataAt,
			&sm.PollIntervalSeconds, &sm.Status); err != nil {
			return nil, fmt.Errorf("scanning source meta: %w", err)
		}
		sources = append(sources, sm)
	}
	return sources, nil
}

func (s *Store) GetSourceMeta(ctx context.Context, name string) (SourceMeta, error) {
	var sm SourceMeta
	err := s.pool.QueryRow(ctx, `
		SELECT id, source_name, expected_lag, last_successful_poll,
			   last_new_data_at, poll_interval_seconds, status
		FROM source_meta WHERE source_name = $1
	`, name).Scan(&sm.ID, &sm.SourceName, &sm.ExpectedLag,
		&sm.LastSuccessfulPoll, &sm.LastNewDataAt,
		&sm.PollIntervalSeconds, &sm.Status)
	if err != nil {
		return SourceMeta{}, fmt.Errorf("getting source %s: %w", name, err)
	}
	return sm, nil
}

func (s *Store) RecordPoll(ctx context.Context, sourceName string, hasNewData bool) error {
	now := time.Now().UTC()
	var err error
	if hasNewData {
		_, err = s.pool.Exec(ctx, `
			UPDATE source_meta SET last_successful_poll = $2, last_new_data_at = $2, status = 'healthy'
			WHERE source_name = $1
		`, sourceName, now)
	} else {
		_, err = s.pool.Exec(ctx, `
			UPDATE source_meta SET last_successful_poll = $2, status = 'healthy'
			WHERE source_name = $1
		`, sourceName, now)
	}
	if err != nil {
		return fmt.Errorf("recording poll for %s: %w", sourceName, err)
	}
	return nil
}

func (s *Store) SetSourceStatus(ctx context.Context, sourceName, status string) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE source_meta SET status = $2 WHERE source_name = $1",
		sourceName, status)
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
DATABASE_URL=postgresql://... go test ./internal/db/... -v -run "TestListSourceMeta|TestUpdateSourceStatus"
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/db/source_meta.go internal/db/source_meta_test.go
git commit -m "feat: source meta CRUD — list, get, record poll, set status"
```

---

### Task 8: API Server Shell + Health Endpoint

**Files:**
- Create: `internal/api/server.go`
- Create: `internal/api/health.go`
- Create: `internal/api/health_test.go`
- Create: `internal/api/server_test.go`
- Create: `internal/cli/serve.go`

- [ ] **Step 1: Write failing test for health endpoint**

Create `internal/api/health_test.go`:
```go
package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arclighteng/mrdn/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestHealthEndpoint(t *testing.T) {
	srv := api.NewServer(nil) // nil store — health doesn't need DB
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "ok")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go test ./internal/api/... -v -run TestHealth
```

Expected: FAIL.

- [ ] **Step 3: Implement API server + health endpoint**

Create `internal/api/server.go`:
```go
package api

import (
	"net/http"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	store  *db.Store
	router chi.Router
}

func NewServer(store *db.Store) *Server {
	s := &Server{store: store}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.SetHeader("Content-Type", "application/json"))

	r.Get("/health", s.handleHealth)

	s.router = r
}

func (s *Server) Handler() http.Handler {
	return s.router
}
```

Create `internal/api/health.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go test ./internal/api/... -v -run TestHealth
```

Expected: PASS.

- [ ] **Step 5: Add `mrdn serve` CLI command**

Create `internal/cli/serve.go`:
```go
package cli

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/arclighteng/mrdn/internal/api"
	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the API server",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx := context.Background()
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		// Verify DB connectivity but don't auto-migrate.
		// Run `mrdn migrate` separately before starting the server.

		store := db.NewStore(pool)
		srv := api.NewServer(store)

		addr := fmt.Sprintf(":%d", cfg.Port)
		log.Printf("MRDN API server listening on %s", addr)
		return http.ListenAndServe(addr, srv.Handler())
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
```

- [ ] **Step 6: Verify it compiles**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go build ./cmd/mrdn
```

Expected: Binary compiles without errors.

- [ ] **Step 7: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/api/ internal/cli/serve.go
git commit -m "feat: API server shell with health endpoint + serve CLI command"
```

---

### Task 9: Seed Data — Top 100 Tech Companies

**Files:**
- Create: `seed/tech_companies.json`
- Create: `internal/cli/seed.go`

- [ ] **Step 1: Create seed data package and file**

Create `internal/seeddata/tech_companies.json` with the top 100 tech companies:
```json
[
  {"ticker": "AAPL", "name": "Apple Inc", "sector": "Technology", "subsector": "Consumer Electronics"},
  {"ticker": "MSFT", "name": "Microsoft Corp", "sector": "Technology", "subsector": "Software"},
  {"ticker": "NVDA", "name": "NVIDIA Corp", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "GOOG", "name": "Alphabet Inc", "sector": "Technology", "subsector": "Internet Services"},
  {"ticker": "GOOGL", "name": "Alphabet Inc Class A", "sector": "Technology", "subsector": "Internet Services"},
  {"ticker": "META", "name": "Meta Platforms Inc", "sector": "Technology", "subsector": "Social Media"},
  {"ticker": "AMZN", "name": "Amazon.com Inc", "sector": "Technology", "subsector": "E-Commerce"},
  {"ticker": "TSLA", "name": "Tesla Inc", "sector": "Technology", "subsector": "Electric Vehicles"},
  {"ticker": "AVGO", "name": "Broadcom Inc", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "TSM", "name": "Taiwan Semiconductor", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "ORCL", "name": "Oracle Corp", "sector": "Technology", "subsector": "Software"},
  {"ticker": "CRM", "name": "Salesforce Inc", "sector": "Technology", "subsector": "Software"},
  {"ticker": "AMD", "name": "Advanced Micro Devices", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "ADBE", "name": "Adobe Inc", "sector": "Technology", "subsector": "Software"},
  {"ticker": "CSCO", "name": "Cisco Systems", "sector": "Technology", "subsector": "Networking"},
  {"ticker": "ACN", "name": "Accenture plc", "sector": "Technology", "subsector": "IT Services"},
  {"ticker": "INTC", "name": "Intel Corp", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "IBM", "name": "IBM Corp", "sector": "Technology", "subsector": "IT Services"},
  {"ticker": "QCOM", "name": "Qualcomm Inc", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "TXN", "name": "Texas Instruments", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "NOW", "name": "ServiceNow Inc", "sector": "Technology", "subsector": "Software"},
  {"ticker": "INTU", "name": "Intuit Inc", "sector": "Technology", "subsector": "Software"},
  {"ticker": "AMAT", "name": "Applied Materials", "sector": "Technology", "subsector": "Semiconductor Equipment"},
  {"ticker": "UBER", "name": "Uber Technologies", "sector": "Technology", "subsector": "Ride Sharing"},
  {"ticker": "MU", "name": "Micron Technology", "sector": "Technology", "subsector": "Memory"},
  {"ticker": "LRCX", "name": "Lam Research", "sector": "Technology", "subsector": "Semiconductor Equipment"},
  {"ticker": "PANW", "name": "Palo Alto Networks", "sector": "Technology", "subsector": "Cybersecurity"},
  {"ticker": "KLAC", "name": "KLA Corp", "sector": "Technology", "subsector": "Semiconductor Equipment"},
  {"ticker": "SNPS", "name": "Synopsys Inc", "sector": "Technology", "subsector": "EDA"},
  {"ticker": "CDNS", "name": "Cadence Design Systems", "sector": "Technology", "subsector": "EDA"},
  {"ticker": "CRWD", "name": "CrowdStrike Holdings", "sector": "Technology", "subsector": "Cybersecurity"},
  {"ticker": "MRVL", "name": "Marvell Technology", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "ADSK", "name": "Autodesk Inc", "sector": "Technology", "subsector": "Software"},
  {"ticker": "FTNT", "name": "Fortinet Inc", "sector": "Technology", "subsector": "Cybersecurity"},
  {"ticker": "WDAY", "name": "Workday Inc", "sector": "Technology", "subsector": "Software"},
  {"ticker": "DASH", "name": "DoorDash Inc", "sector": "Technology", "subsector": "Delivery"},
  {"ticker": "TEAM", "name": "Atlassian Corp", "sector": "Technology", "subsector": "Software"},
  {"ticker": "TTD", "name": "The Trade Desk", "sector": "Technology", "subsector": "Ad Tech"},
  {"ticker": "MCHP", "name": "Microchip Technology", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "ON", "name": "ON Semiconductor", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "NXPI", "name": "NXP Semiconductors", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "SNOW", "name": "Snowflake Inc", "sector": "Technology", "subsector": "Data"},
  {"ticker": "PLTR", "name": "Palantir Technologies", "sector": "Technology", "subsector": "Data Analytics"},
  {"ticker": "NET", "name": "Cloudflare Inc", "sector": "Technology", "subsector": "Cloud Infrastructure"},
  {"ticker": "DDOG", "name": "Datadog Inc", "sector": "Technology", "subsector": "Observability"},
  {"ticker": "HUBS", "name": "HubSpot Inc", "sector": "Technology", "subsector": "Software"},
  {"ticker": "ZS", "name": "Zscaler Inc", "sector": "Technology", "subsector": "Cybersecurity"},
  {"ticker": "MDB", "name": "MongoDB Inc", "sector": "Technology", "subsector": "Database"},
  {"ticker": "SHOP", "name": "Shopify Inc", "sector": "Technology", "subsector": "E-Commerce"},
  {"ticker": "SQ", "name": "Block Inc", "sector": "Technology", "subsector": "Fintech"},
  {"ticker": "COIN", "name": "Coinbase Global", "sector": "Technology", "subsector": "Crypto"},
  {"ticker": "ARM", "name": "Arm Holdings", "sector": "Technology", "subsector": "Semiconductors"},
  {"ticker": "SPOT", "name": "Spotify Technology", "sector": "Technology", "subsector": "Streaming"},
  {"ticker": "RBLX", "name": "Roblox Corp", "sector": "Technology", "subsector": "Gaming"},
  {"ticker": "U", "name": "Unity Software", "sector": "Technology", "subsector": "Gaming"},
  {"ticker": "PATH", "name": "UiPath Inc", "sector": "Technology", "subsector": "Automation"},
  {"ticker": "DOCU", "name": "DocuSign Inc", "sector": "Technology", "subsector": "Software"},
  {"ticker": "ZM", "name": "Zoom Video Communications", "sector": "Technology", "subsector": "Communications"},
  {"ticker": "OKTA", "name": "Okta Inc", "sector": "Technology", "subsector": "Identity"},
  {"ticker": "TWLO", "name": "Twilio Inc", "sector": "Technology", "subsector": "Communications"},
  {"ticker": "PINS", "name": "Pinterest Inc", "sector": "Technology", "subsector": "Social Media"},
  {"ticker": "SNAP", "name": "Snap Inc", "sector": "Technology", "subsector": "Social Media"},
  {"ticker": "ROKU", "name": "Roku Inc", "sector": "Technology", "subsector": "Streaming"},
  {"ticker": "ABNB", "name": "Airbnb Inc", "sector": "Technology", "subsector": "Travel"},
  {"ticker": "LYFT", "name": "Lyft Inc", "sector": "Technology", "subsector": "Ride Sharing"},
  {"ticker": "HOOD", "name": "Robinhood Markets", "sector": "Technology", "subsector": "Fintech"},
  {"ticker": "SOFI", "name": "SoFi Technologies", "sector": "Technology", "subsector": "Fintech"},
  {"ticker": "AFRM", "name": "Affirm Holdings", "sector": "Technology", "subsector": "Fintech"},
  {"ticker": "AI", "name": "C3.ai Inc", "sector": "Technology", "subsector": "AI"},
  {"ticker": "SMCI", "name": "Super Micro Computer", "sector": "Technology", "subsector": "Servers"},
  {"ticker": "DELL", "name": "Dell Technologies", "sector": "Technology", "subsector": "Hardware"},
  {"ticker": "HPQ", "name": "HP Inc", "sector": "Technology", "subsector": "Hardware"},
  {"ticker": "HPE", "name": "Hewlett Packard Enterprise", "sector": "Technology", "subsector": "Infrastructure"},
  {"ticker": "SPLK", "name": "Splunk Inc", "sector": "Technology", "subsector": "Observability"},
  {"ticker": "ESTC", "name": "Elastic NV", "sector": "Technology", "subsector": "Search"},
  {"ticker": "CFLT", "name": "Confluent Inc", "sector": "Technology", "subsector": "Data Streaming"},
  {"ticker": "BILL", "name": "BILL Holdings", "sector": "Technology", "subsector": "Fintech"},
  {"ticker": "GDDY", "name": "GoDaddy Inc", "sector": "Technology", "subsector": "Internet Services"},
  {"ticker": "VEEV", "name": "Veeva Systems", "sector": "Technology", "subsector": "Health Tech"},
  {"ticker": "MNDY", "name": "monday.com", "sector": "Technology", "subsector": "Software"},
  {"ticker": "IOT", "name": "Samsara Inc", "sector": "Technology", "subsector": "IoT"},
  {"ticker": "GLBE", "name": "Global-e Online", "sector": "Technology", "subsector": "E-Commerce"},
  {"ticker": "S", "name": "SentinelOne Inc", "sector": "Technology", "subsector": "Cybersecurity"},
  {"ticker": "FOUR", "name": "Shift4 Payments", "sector": "Technology", "subsector": "Payments"},
  {"ticker": "DT", "name": "Dynatrace Inc", "sector": "Technology", "subsector": "Observability"},
  {"ticker": "TOST", "name": "Toast Inc", "sector": "Technology", "subsector": "Restaurant Tech"},
  {"ticker": "APP", "name": "AppLovin Corp", "sector": "Technology", "subsector": "Ad Tech"},
  {"ticker": "RDDT", "name": "Reddit Inc", "sector": "Technology", "subsector": "Social Media"},
  {"ticker": "IONQ", "name": "IonQ Inc", "sector": "Technology", "subsector": "Quantum Computing"},
  {"ticker": "RGTI", "name": "Rigetti Computing", "sector": "Technology", "subsector": "Quantum Computing"},
  {"ticker": "QBTS", "name": "D-Wave Quantum", "sector": "Technology", "subsector": "Quantum Computing"},
  {"ticker": "SOUN", "name": "SoundHound AI", "sector": "Technology", "subsector": "AI"},
  {"ticker": "BBAI", "name": "BigBear.ai", "sector": "Technology", "subsector": "AI"},
  {"ticker": "ASTS", "name": "AST SpaceMobile", "sector": "Technology", "subsector": "Space Tech"},
  {"ticker": "RKLB", "name": "Rocket Lab USA", "sector": "Technology", "subsector": "Space Tech"},
  {"ticker": "MNTS", "name": "Momentus Inc", "sector": "Technology", "subsector": "Space Tech"},
  {"ticker": "VRT", "name": "Vertiv Holdings", "sector": "Technology", "subsector": "Data Center Infrastructure"},
  {"ticker": "ANET", "name": "Arista Networks", "sector": "Technology", "subsector": "Networking"},
  {"ticker": "FFIV", "name": "F5 Inc", "sector": "Technology", "subsector": "Networking"},
  {"ticker": "AKAM", "name": "Akamai Technologies", "sector": "Technology", "subsector": "CDN"}
]
```

- [ ] **Step 1b: Create seed data embed package**

Create `internal/seeddata/seeddata.go`:
```go
package seeddata

import _ "embed"

//go:embed tech_companies.json
var TechCompanies []byte
```

- [ ] **Step 2: Create seed CLI command**

Create `internal/cli/seed.go`:
```go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/arclighteng/mrdn/internal/seeddata"
	"github.com/spf13/cobra"
)

type seedCompany struct {
	Ticker    string `json:"ticker"`
	Name      string `json:"name"`
	Sector    string `json:"sector"`
	Subsector string `json:"subsector"`
}

var seedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Seed the database with initial company data",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx := context.Background()
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		store := db.NewStore(pool)

		var companies []seedCompany
		if err := json.Unmarshal(seeddata.TechCompanies, &companies); err != nil {
			return fmt.Errorf("parsing seed data: %w", err)
		}

		for _, sc := range companies {
			_, err := store.UpsertCompany(ctx, db.Company{
				Ticker:    sc.Ticker,
				Name:      sc.Name,
				Sector:    db.StrPtr(sc.Sector),
				Subsector: db.StrPtr(sc.Subsector),
			})
			if err != nil {
				return fmt.Errorf("seeding %s: %w", sc.Ticker, err)
			}
		}

		log.Printf("seeded %d companies", len(companies))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(seedCmd)
}
```

- [ ] **Step 3: Verify it compiles**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go build ./cmd/mrdn
```

Expected: Compiles without errors.

- [ ] **Step 4: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/seeddata/ internal/cli/seed.go
git commit -m "feat: seed command — top 100 tech companies"
```

---

### Task 10: Sources CLI Command

**Files:**
- Create: `internal/cli/sources.go`

- [ ] **Step 1: Create sources command**

Create `internal/cli/sources.go`:
```go
package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var sourcesCmd = &cobra.Command{
	Use:   "sources",
	Short: "Show data source health and freshness",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx := context.Background()
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer pool.Close()

		store := db.NewStore(pool)
		sources, err := store.ListSourceMeta(ctx)
		if err != nil {
			return fmt.Errorf("listing sources: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "SOURCE\tSTATUS\tEXPECTED LAG\tLAST POLL\tLAST DATA")
		for _, s := range sources {
			lastPoll := "never"
			if s.LastSuccessfulPoll != nil {
				lastPoll = time.Since(*s.LastSuccessfulPoll).Truncate(time.Second).String() + " ago"
			}
			lastData := "never"
			if s.LastNewDataAt != nil {
				lastData = time.Since(*s.LastNewDataAt).Truncate(time.Second).String() + " ago"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				s.SourceName, s.Status, s.ExpectedLag, lastPoll, lastData)
		}
		w.Flush()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(sourcesCmd)
}
```

- [ ] **Step 2: Verify it compiles**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go build ./cmd/mrdn
```

Expected: Compiles.

- [ ] **Step 3: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/cli/sources.go
git commit -m "feat: sources CLI command — tabular source health display"
```

---

### Task 11: Scores DB Layer

**Files:**
- Create: `internal/db/scores.go`
- Create: `internal/db/scores_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/db/scores_test.go`:
```go
package db_test

import (
	"context"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertAndGetLatestScore(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	c, err := store.UpsertCompany(ctx, db.Company{Ticker: "SCR1", Name: "Score Test", Sector: "Technology"})
	require.NoError(t, err)
	defer store.DeleteCompany(ctx, c.ID)

	err = store.InsertScore(ctx, db.Score{
		CompanyID:      c.ID,
		MarketScore:    50.0,
		PolicyScore:    75.0,
		InsiderScore:   30.0,
		CompositeScore: 55.0,
		WeightVersion:  1,
	})
	require.NoError(t, err)

	score, err := store.GetLatestScore(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, 50.0, score.MarketScore)
	assert.Equal(t, 75.0, score.PolicyScore)
	assert.Equal(t, 55.0, score.CompositeScore)
}

func TestGetScoreRankings(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	c1, _ := store.UpsertCompany(ctx, db.Company{Ticker: "RNK1", Name: "Rank One", Sector: db.StrPtr("TestSector_Rank")})
	c2, _ := store.UpsertCompany(ctx, db.Company{Ticker: "RNK2", Name: "Rank Two", Sector: db.StrPtr("TestSector_Rank")})
	defer store.DeleteCompany(ctx, c1.ID)
	defer store.DeleteCompany(ctx, c2.ID)

	store.InsertScore(ctx, db.Score{CompanyID: c1.ID, CompositeScore: 80.0, WeightVersion: 1})
	store.InsertScore(ctx, db.Score{CompanyID: c2.ID, CompositeScore: 60.0, WeightVersion: 1})

	rankings, err := store.GetScoreRankings(ctx, 100)
	require.NoError(t, err)

	// Find our test companies by ticker and verify ordering
	var rnk1Idx, rnk2Idx int
	rnk1Idx, rnk2Idx = -1, -1
	for i, r := range rankings {
		if r.Ticker == "RNK1" { rnk1Idx = i }
		if r.Ticker == "RNK2" { rnk2Idx = i }
	}
	require.NotEqual(t, -1, rnk1Idx, "RNK1 not found in rankings")
	require.NotEqual(t, -1, rnk2Idx, "RNK2 not found in rankings")
	assert.Less(t, rnk1Idx, rnk2Idx, "RNK1 (80) should rank higher than RNK2 (60)")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
go test ./internal/db/... -v -run TestInsertAndGetLatestScore
```

Expected: FAIL.

- [ ] **Step 3: Implement score operations**

Create `internal/db/scores.go`:
```go
package db

import (
	"context"
	"fmt"
	"time"
)

type Score struct {
	ID             int       `json:"id"`
	CompanyID      int       `json:"company_id"`
	MarketScore    float64   `json:"market_score"`
	PolicyScore    float64   `json:"policy_score"`
	InsiderScore   float64   `json:"insider_score"`
	CompositeScore float64   `json:"composite_score"`
	WeightVersion  int       `json:"weight_version"`
	ComputedAt     time.Time `json:"computed_at"`
}

type ScoreRanking struct {
	Ticker         string  `json:"ticker"`
	CompanyName    string  `json:"company_name"`
	MarketScore    float64 `json:"market_score"`
	PolicyScore    float64 `json:"policy_score"`
	InsiderScore   float64 `json:"insider_score"`
	CompositeScore float64 `json:"composite_score"`
	WeightVersion  int     `json:"weight_version"`
	ComputedAt     time.Time `json:"computed_at"`
}

func (s *Store) InsertScore(ctx context.Context, sc Score) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO scores (company_id, market_score, policy_score, insider_score, composite_score, weight_version)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, sc.CompanyID, sc.MarketScore, sc.PolicyScore, sc.InsiderScore,
		sc.CompositeScore, sc.WeightVersion)
	if err != nil {
		return fmt.Errorf("inserting score for company %d: %w", sc.CompanyID, err)
	}
	return nil
}

func (s *Store) GetLatestScore(ctx context.Context, companyID int) (Score, error) {
	var sc Score
	// Cast NUMERIC to float8 so pgx v5 can scan into float64
	err := s.pool.QueryRow(ctx, `
		SELECT id, company_id,
			   market_score::float8, policy_score::float8, insider_score::float8,
			   composite_score::float8, weight_version, computed_at
		FROM scores WHERE company_id = $1
		ORDER BY computed_at DESC LIMIT 1
	`, companyID).Scan(&sc.ID, &sc.CompanyID, &sc.MarketScore, &sc.PolicyScore,
		&sc.InsiderScore, &sc.CompositeScore, &sc.WeightVersion, &sc.ComputedAt)
	if err != nil {
		return Score{}, fmt.Errorf("getting latest score for company %d: %w", companyID, err)
	}
	return sc, nil
}

func (s *Store) GetScoreRankings(ctx context.Context, limit int) ([]ScoreRanking, error) {
	if limit <= 0 {
		limit = 50
	}
	// CTE gets latest score per company, then sorts by composite and limits in SQL
	rows, err := s.pool.Query(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (s.company_id)
				s.company_id, s.market_score::float8, s.policy_score::float8,
				s.insider_score::float8, s.composite_score::float8,
				s.weight_version, s.computed_at
			FROM scores s
			ORDER BY s.company_id, s.computed_at DESC
		)
		SELECT c.ticker, c.name, l.market_score, l.policy_score,
			   l.insider_score, l.composite_score, l.weight_version, l.computed_at
		FROM latest l
		JOIN companies c ON c.id = l.company_id
		ORDER BY l.composite_score DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("getting score rankings: %w", err)
	}
	defer rows.Close()

	var rankings []ScoreRanking
	for rows.Next() {
		var r ScoreRanking
		if err := rows.Scan(&r.Ticker, &r.CompanyName, &r.MarketScore, &r.PolicyScore,
			&r.InsiderScore, &r.CompositeScore, &r.WeightVersion, &r.ComputedAt); err != nil {
			return nil, fmt.Errorf("scanning ranking: %w", err)
		}
		rankings = append(rankings, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rankings: %w", err)
	}
	return rankings, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
DATABASE_URL=postgresql://... go test ./internal/db/... -v -run "TestInsertAndGetLatestScore|TestGetScoreRankings"
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/db/scores.go internal/db/scores_test.go
git commit -m "feat: score CRUD — insert, get latest, rankings"
```

---

### Task 12: End-to-End Smoke Test

**Files:**
- Create: `internal/api/server_test.go`

- [ ] **Step 1: Write integration test**

Create `internal/api/server_test.go`:
```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/arclighteng/mrdn/internal/api"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupIntegrationServer(t *testing.T) *api.Server {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, pool))
	t.Cleanup(func() { pool.Close() })
	store := db.NewStore(pool)
	return api.NewServer(store)
}

func TestSmoke_HealthReturnsOK(t *testing.T) {
	srv := setupIntegrationServer(t)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	assert.Equal(t, "ok", body["status"])
}
```

- [ ] **Step 2: Run the smoke test**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
DATABASE_URL=postgresql://... go test ./internal/api/... -v -run TestSmoke
```

Expected: PASS.

- [ ] **Step 3: Run all tests**

Run:
```bash
cd C:/Users/AR/Projects/mrdn
DATABASE_URL=postgresql://... go test ./... -v
```

Expected: All tests PASS.

- [ ] **Step 4: Commit**

```bash
cd C:/Users/AR/Projects/mrdn
git add internal/api/server_test.go
git commit -m "test: end-to-end smoke test — health endpoint with real DB"
```

---

## Phase 1 Complete

At this point you have:
- Go binary that compiles and runs
- `mrdn migrate` — runs schema migrations
- `mrdn seed` — loads 100 tech companies
- `mrdn serve` — starts API server with `/health` endpoint
- `mrdn sources` — shows data source health
- Full DDL with all tables, indexes, constraints from the spec
- DB layer for companies, events, source_meta, scores — all tested
- Config loading from environment variables

**Next phase:** Phase 2 — Ingestion worker framework + first 3 data sources (Polygon, EDGAR Form 4, OFAC).
