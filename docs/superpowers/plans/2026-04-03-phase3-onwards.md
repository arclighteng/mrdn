# MRDN Phases 3-8: Score Engine → Production — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete MRDN from current state (REST API + SSE + ingestion framework + Polygon parser) through score engine, real source parsers, entity resolution, typed table extraction, CLI commands, and production hardening.

**Architecture decisions (from orchestration reviews 2026-04-03):**
- **Two-process model:** `mrdn serve` (API + SSE) and `mrdn ingest` (workers + score engine) as separate processes. Postgres LISTEN/NOTIFY bridges events.
- **Score windows:** 30d market, 90d policy, 90d insider — configurable via `score_weights.weights` JSONB.
- **Sub-scores hidden:** Composite drives card display/sorting. Sub-scores stored internally, not in public API.
- **SSE auth:** Public access. Validate API keys — unrecognized key = anonymous (3/IP limit).
- **Entity resolver:** Single goroutine subscriber. Exact normalized match first, no fuzzy from day 1.
- **Person seed:** congress-legislators YAML from @unitedstates project.
- **Graph API:** Flat `{nodes, edges}` format with BFS, depth cap 3-4, node budget.
- **Donor names:** Exposed in API (individual donor names visible).
- **CLI:** Ops-only, not public distribution.
- **Dependent children:** Separate endpoint, not inlined in list responses.

**Existing signature notes (match these exactly):**
- `Store.GetCompanyByTicker(ctx, ticker) → (Company, error)` — returns value, not pointer
- `Store.GetEvent(ctx, id) → (Event, error)` — returns value, not pointer
- `Store.InsertScore(ctx, Score) → error` — takes value, not pointer
- `NewPolygonSource(client *http.Client, apiKey string)` — client first, then key
- `FakeClock.Advance(d)` already exists (simple clock advance, no mutex)
- Source name in seed data: `efds_senate` (not `senate_efds`)

**Tech Stack:** Go 1.25, chi v5, pgx v5, cobra, testify, `golang.org/x/time/rate`, `nhooyr.io/websocket` (Finnhub).

**Spec:** `docs/superpowers/specs/2026-04-01-mrdn-design.md`

---

## File Structure

```
internal/
├── score/
│   ├── engine.go              -- NEW: ScoreComputer interface, Engine struct, Compute(companyID)
│   ├── engine_test.go         -- NEW: unit tests with mock store
│   ├── market.go              -- NEW: MarketScore sub-calculator (price trend, volume anomaly, insider trades)
│   ├── market_test.go         -- NEW
│   ├── policy.go              -- NEW: PolicyScore sub-calculator (tariffs, sanctions, contracts, court)
│   ├── policy_test.go         -- NEW
│   ├── insider.go             -- NEW: InsiderScore sub-calculator (congressional trades, lobbying, donations)
│   ├── insider_test.go        -- NEW
│   └── worker.go              -- NEW: ScoreWorker — broker subscriber that triggers recomputation
│   └── worker_test.go         -- NEW
├── resolve/
│   ├── resolver.go            -- NEW: Resolver struct, Run(ctx) loop, entity resolution pipeline
│   ├── resolver_test.go       -- NEW
│   ├── match.go               -- NEW: ticker match, normalized name match, manual overrides
│   └── match_test.go          -- NEW
├── extract/
│   ├── dispatcher.go          -- NEW: broker subscriber, routes events to type-specific extractors
│   ├── dispatcher_test.go     -- NEW
│   ├── trades.go              -- NEW: congressional_trades extractor
│   ├── trades_test.go         -- NEW
│   ├── contracts.go           -- NEW: contracts extractor
│   ├── contracts_test.go      -- NEW
│   ├── sanctions.go           -- NEW: sanctions extractor
│   ├── sanctions_test.go      -- NEW
│   ├── insider_trades.go      -- NEW: insider_trades extractor (SEC Form 4)
│   ├── insider_trades_test.go -- NEW
│   ├── donations.go           -- NEW: donations extractor (FEC)
│   ├── donations_test.go      -- NEW
│   ├── market.go              -- NEW: market_data extractor (Polygon/Finnhub)
│   └── market_test.go         -- NEW
├── parser/
│   ├── edgar.go               -- NEW: SEC EDGAR Form 4 XML parser
│   ├── edgar_test.go          -- NEW
│   ├── efds.go                -- NEW: Senate EFDS XML parser
│   ├── efds_test.go           -- NEW
│   ├── usaspending.go         -- NEW: USAspending.gov JSON parser
│   ├── usaspending_test.go    -- NEW
│   ├── fedregister.go         -- NEW: Federal Register JSON parser
│   ├── fedregister_test.go    -- NEW
│   ├── fec.go                 -- NEW: FEC JSON parser
│   ├── fec_test.go            -- NEW
│   ├── ofac.go                -- MODIFY: complete implementation (currently test stubs only)
│   ├── finnhub.go             -- NEW: Finnhub WebSocket message parser
│   ├── finnhub_test.go        -- NEW
│   └── testdata/              -- NEW fixtures per source
├── db/
│   ├── persons.go             -- NEW: Person CRUD (upsert, get by slug, list)
│   ├── persons_test.go        -- NEW
│   ├── typed_tables.go        -- NEW: Insert/query for congressional_trades, contracts, sanctions, etc.
│   ├── typed_tables_test.go   -- NEW
│   ├── entity_links.go        -- NEW: entity_links and entity_aliases CRUD
│   ├── entity_links_test.go   -- NEW
│   ├── graph.go               -- NEW: BFS graph query (nodes + edges)
│   ├── graph_test.go          -- NEW
│   ├── store.go               -- MODIFY: add new methods to Store
│   └── migrations/
│       └── 002_persons_seed.sql -- NEW: person seed data from congress-legislators
├── cli/
│   ├── ingest.go              -- MODIFY: wire LISTEN/NOTIFY bridge, register all sources
│   ├── serve.go               -- MODIFY: wire LISTEN/NOTIFY → broker bridge
│   ├── query.go               -- NEW: `mrdn query` subcommands
│   ├── scores_cmd.go          -- NEW: `mrdn scores` command
│   ├── sources_cmd.go         -- NEW: `mrdn sources` command
│   └── link.go                -- NEW: `mrdn link` command (manual entity alias)
├── api/
│   ├── persons.go             -- NEW: /persons endpoints
│   ├── persons_test.go        -- NEW
│   ├── connections.go         -- NEW: /connections endpoints (graph)
│   ├── connections_test.go    -- NEW
│   ├── server.go              -- MODIFY: mount new routes
│   └── middleware.go          -- MODIFY: validate API keys on SSE
└── ingestion/
    └── clock.go               -- MODIFY: add manual-advance mode to FakeClock
```

---

## Phase 0: Foundation Fixes

These fix gaps identified during orchestration that block later phases.

### Task 1: Wire LISTEN/NOTIFY in `mrdn serve`

The `ListenNewEvents` and `NotifyNewEvent` functions exist in `internal/db/notify.go` but are never called from production code. `mrdn serve` needs to LISTEN for new events and publish them to the SSE broker so that the two-process model works.

**Files:**
- Modify: `internal/cli/serve.go`
- Test: manual (requires running Postgres; integration test deferred to Phase 6)

- [ ] **Step 1: Read current serve.go**

Read `internal/cli/serve.go` to understand the current setup flow.

- [ ] **Step 2: Add LISTEN/NOTIFY → broker bridge**

After creating the API server and before starting the HTTP server, add a goroutine that:
1. Calls `db.ListenNewEvents(ctx, pool)` to get a channel of event IDs
2. For each event ID, fetches the event from the store
3. Converts it to a `broker.Event` and publishes to the server's broker

```go
// Bridge: Postgres LISTEN/NOTIFY → SSE broker.
// When mrdn ingest writes an event and calls NotifyNewEvent,
// this goroutine picks it up and fans it out to SSE subscribers.
eventCh, err := db.ListenNewEvents(ctx, pool)
if err != nil {
    return fmt.Errorf("starting event listener: %w", err)
}
go func() {
    for eventID := range eventCh {
        evt, err := store.GetEvent(ctx, eventID)
        if err != nil {
            log.Printf("LISTEN bridge: failed to fetch event %d: %v", eventID, err)
            continue
        }
        bEvt := broker.Event{
            ID:         evt.ID,
            CompanyID:  evt.CompanyID,
            Source:     evt.Source,
            EventType:  evt.EventType,
            OccurredAt: evt.OccurredAt,
        }
        if evt.CompanyID != nil {
            // Look up ticker for the broker event
            company, err := store.GetCompanyByID(ctx, *evt.CompanyID)
            if err == nil {
                bEvt.Ticker = company.Ticker
            }
        }
        srv.Broker().Publish(bEvt)
    }
}()
```

- [ ] **Step 3: Add `GetCompanyByID` to store if missing**

Check if `internal/db/companies.go` has a `GetCompanyByID(ctx, id)` method. If not, add it:

```go
func (s *Store) GetCompanyByID(ctx context.Context, id int) (*Company, error) {
    row := s.db.QueryRow(ctx, `SELECT id, ticker, name, sector, subsector, naics_code, market_cap_bucket FROM companies WHERE id = $1`, id)
    var c Company
    err := row.Scan(&c.ID, &c.Ticker, &c.Name, &c.Sector, &c.Subsector, &c.NAICSCode, &c.MarketCapBucket)
    if err != nil {
        return nil, fmt.Errorf("get company by id %d: %w", id, err)
    }
    return &c, nil
}
```

- [ ] **Step 4: Wire NotifyNewEvent in the ingest worker**

In `internal/ingestion/worker.go`, after `InsertEvent` succeeds, call `db.NotifyNewEvent`. The PollWorker needs access to the pool (not just the store). Check if PollWorker already has the pool reference.

Look at the PollWorker struct. If it only has `store *db.Store`, the store's underlying `DBTX` might not support `Exec` with `pg_notify`. Since `NotifyNewEvent` takes `*pgxpool.Pool`, the supervisor needs to pass the pool to the worker.

```go
// In PollWorker.run(), after successful insert:
if err := db.NotifyNewEvent(ctx, w.pool, event.ID); err != nil {
    log.Printf("worker %s: notify failed for event %d: %v", w.source.Name(), event.ID, err)
    // Non-fatal: the event is stored, just not broadcast cross-process
}
```

- [ ] **Step 5: Verify compilation**

Run: `cd /c/Users/AR/Projects/mrdn && go build ./...`
Expected: clean build

- [ ] **Step 6: Commit**

```bash
git add internal/cli/serve.go internal/ingestion/worker.go internal/db/companies.go
git commit -m "feat: wire LISTEN/NOTIFY bridge for two-process SSE"
```

---

### Task 2: Consolidate jsonDepth into single location

`jsonDepth` is duplicated in `internal/db/events.go` and `internal/parser/parser.go`. Extract to one canonical location.

**Files:**
- Modify: `internal/parser/parser.go` (keep here — parser is the validation layer)
- Modify: `internal/db/events.go` (import from parser, or inline the constant)
- Test: `internal/parser/parser_test.go` (existing tests should still pass)

- [ ] **Step 1: Check current duplication**

Read both files to confirm the duplication and decide which to keep.

- [ ] **Step 2: Remove jsonDepth from db/events.go, import from parser**

Since `db` should not import `parser` (dependency direction: parser → db), and `db/events.go` uses `jsonDepth` in `ValidateEventData`, the cleanest fix is:

Option A: Move `jsonDepth` to a small `internal/jsonutil/` package imported by both.
Option B: Keep it in both places but document the duplication.

Go with Option A — small utility package:

Create `internal/jsonutil/depth.go`:
```go
package jsonutil

import "encoding/json"

const MaxDepth = 10

// Depth returns the maximum nesting depth of a JSON value.
func Depth(raw json.RawMessage) int {
    var maxD int
    var walk func([]byte, int)
    walk = func(data []byte, d int) {
        // ... same implementation as current jsonDepth
    }
    walk(raw, 0)
    return maxD
}
```

- [ ] **Step 3: Update imports in both files**

Replace `jsonDepth(raw)` calls with `jsonutil.Depth(raw)` in both `db/events.go` and `parser/parser.go`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/db/... ./internal/parser/...`
Expected: all pass

- [ ] **Step 5: Commit**

```bash
git add internal/jsonutil/ internal/db/events.go internal/parser/parser.go
git commit -m "refactor: consolidate jsonDepth into internal/jsonutil"
```

---

### Task 3: Extend FakeClock with manual-advance mode

The current `FakeClock.After()` fires immediately. For debounce testing in the score engine, we need a mode where `After` blocks until `Advance()` is called.

**IMPORTANT:** `FakeClock` already has an `Advance(d)` method that simply moves the clock forward. This task replaces the entire FakeClock struct to add mutex protection and waiter tracking. The existing `Advance` method signature is preserved but its implementation changes (it now also fires pending waiters). Verify all existing tests still pass after the change.

**Files:**
- Modify: `internal/ingestion/clock.go`
- Test: `internal/ingestion/clock_test.go`

- [ ] **Step 1: Write failing test for blocking After**

```go
func TestFakeClock_ManualAdvance(t *testing.T) {
    fc := NewFakeClock(time.Now())
    fc.SetManualAdvance(true)

    ch := fc.After(5 * time.Second)

    // Should not fire immediately
    select {
    case <-ch:
        t.Fatal("After should not fire before Tick in manual mode")
    case <-time.After(50 * time.Millisecond):
        // good
    }

    // Advance past the duration
    fc.Advance(5 * time.Second)

    select {
    case <-ch:
        // good
    case <-time.After(100 * time.Millisecond):
        t.Fatal("After should fire after Advance")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingestion/ -run TestFakeClock_ManualAdvance -v`
Expected: FAIL (SetManualAdvance and Advance don't exist)

- [ ] **Step 3: Implement manual-advance mode**

Add to `FakeClock`:

```go
type FakeClock struct {
    mu            sync.Mutex
    now           time.Time
    manualAdvance bool
    waiters       []waiter
}

type waiter struct {
    deadline time.Time
    ch       chan time.Time
}

func (fc *FakeClock) SetManualAdvance(on bool) {
    fc.mu.Lock()
    defer fc.mu.Unlock()
    fc.manualAdvance = on
}

func (fc *FakeClock) Advance(d time.Duration) {
    fc.mu.Lock()
    fc.now = fc.now.Add(d)
    now := fc.now
    var remaining []waiter
    for _, w := range fc.waiters {
        if !now.Before(w.deadline) {
            w.ch <- now
            close(w.ch)
        } else {
            remaining = append(remaining, w)
        }
    }
    fc.waiters = remaining
    fc.mu.Unlock()
}

func (fc *FakeClock) After(d time.Duration) <-chan time.Time {
    fc.mu.Lock()
    defer fc.mu.Unlock()
    if !fc.manualAdvance {
        // Existing behavior: fire immediately, advance clock
        fc.now = fc.now.Add(d)
        ch := make(chan time.Time, 1)
        ch <- fc.now
        return ch
    }
    // Manual mode: register waiter
    ch := make(chan time.Time, 1)
    fc.waiters = append(fc.waiters, waiter{
        deadline: fc.now.Add(d),
        ch:       ch,
    })
    return ch
}
```

- [ ] **Step 4: Run all clock and ingestion tests**

Run: `go test ./internal/ingestion/ -v`
Expected: all pass (existing tests use default non-manual mode, new test passes)

- [ ] **Step 5: Commit**

```bash
git add internal/ingestion/clock.go internal/ingestion/clock_test.go
git commit -m "feat: add manual-advance mode to FakeClock for debounce testing"
```

---

### Task 4: SSE API key validation

SSE endpoints currently skip authentication. Add API key validation: unrecognized key → anonymous (3/IP limit), valid key → higher limits.

**Files:**
- Modify: `internal/api/stream.go` (or `internal/api/middleware.go`)
- Test: `internal/api/stream_test.go`

- [ ] **Step 1: Read current SSE handler and middleware**

Read `internal/api/stream.go` and `internal/api/middleware.go` to understand current auth flow.

- [ ] **Step 2: Write failing test**

```go
func TestSSE_InvalidAPIKey_TreatedAsAnonymous(t *testing.T) {
    // Create server with mock store that returns no key for hash
    // Send request with X-API-Key: "invalid-key"
    // Verify SSE connection succeeds but uses anonymous limits (3/IP)
}
```

- [ ] **Step 3: Implement key validation in serveSSE**

In `serveSSE`, before `Acquire`, validate the API key. If key is present but not found in DB, treat as anonymous (clear the key so SSEManager uses IP limits):

```go
apiKey := r.Header.Get("X-API-Key")
if apiKey != "" {
    hash := sha256Hex(apiKey)
    _, err := s.store.GetAPIKey(r.Context(), hash)
    if err != nil {
        // Unrecognized key — treat as anonymous
        apiKey = ""
    }
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api/ -v`
Expected: all pass

- [ ] **Step 5: Commit**

```bash
git add internal/api/stream.go internal/api/stream_test.go
git commit -m "feat: validate API keys on SSE endpoints, unrecognized = anonymous"
```

---

### Task 5: Source whitelist for score recomputation

Before building the score engine, define the set of valid sources. The score worker should only recompute when events come from known sources.

**Files:**
- Create: `internal/score/sources.go`
- Test: `internal/score/sources_test.go`

- [ ] **Step 1: Write failing test**

```go
package score

func TestIsScorableSource(t *testing.T) {
    assert.True(t, IsScorableSource("polygon"))
    assert.True(t, IsScorableSource("edgar_form4"))
    assert.True(t, IsScorableSource("efds_senate"))
    assert.False(t, IsScorableSource("unknown_source"))
    assert.False(t, IsScorableSource(""))
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/score/ -run TestIsScorableSource -v`
Expected: FAIL (package doesn't exist)

- [ ] **Step 3: Implement**

```go
package score

// scorableSources is the set of sources whose events trigger score recomputation.
var scorableSources = map[string]bool{
    "polygon":          true,
    "finnhub":          true,
    "edgar_form4":      true,
    "efds_senate":      true,
    "usaspending":      true,
    "ofac_sdn":         true,
    "federal_register": true,
    "fec":              true,
    "warn":             true,
    "pacer":            true,
    "lda":              true,
}

// IsScorableSource returns true if events from this source should trigger score recomputation.
func IsScorableSource(source string) bool {
    return scorableSources[source]
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/score/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/score/
git commit -m "feat: define source whitelist for score recomputation"
```

---

## Phase 1: Score Engine

### Task 6: Score engine interface and composite calculator

Define the `ScoreComputer` interface and the `Engine` struct that computes composite scores.

**Files:**
- Create: `internal/score/engine.go`
- Test: `internal/score/engine_test.go`

- [ ] **Step 1: Write failing test for composite calculation**

```go
package score

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestComposite(t *testing.T) {
    w := DefaultWeights()
    // Market 35%, Policy 40%, Insider 25%
    result := composite(70.0, 80.0, 60.0, w)
    // 70*0.35 + 80*0.40 + 60*0.25 = 24.5 + 32.0 + 15.0 = 71.5
    assert.InDelta(t, 71.5, result, 0.01)
}

func TestComposite_AllZero(t *testing.T) {
    assert.InDelta(t, 0.0, composite(0, 0, 0, DefaultWeights()), 0.01)
}

func TestComposite_AllMax(t *testing.T) {
    assert.InDelta(t, 100.0, composite(100, 100, 100, DefaultWeights()), 0.01)
}

func TestComposite_CustomWeights(t *testing.T) {
    w := Weights{Market: 0.50, Policy: 0.30, Insider: 0.20}
    result := composite(100.0, 0.0, 0.0, w)
    assert.InDelta(t, 50.0, result, 0.01)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/score/ -run TestComposite -v`
Expected: FAIL

- [ ] **Step 3: Implement engine.go**

```go
package score

import (
    "context"
    "fmt"
    "time"

    "github.com/arclighteng/mrdn/internal/db"
)

// Default weights. Configurable via score_weights table.
const (
    DefaultMarketWeight  = 0.35
    DefaultPolicyWeight  = 0.40
    DefaultInsiderWeight = 0.25
)

// ScoreComputer computes a composite score for a company.
type ScoreComputer interface {
    Compute(ctx context.Context, companyID int) (*db.Score, error)
}

// Engine implements ScoreComputer using sub-score calculators.
type Engine struct {
    store   *db.Store
    market  SubScorer
    policy  SubScorer
    insider SubScorer
    weights Weights
}

// Weights holds the configurable weight distribution.
type Weights struct {
    Market  float64
    Policy  float64
    Insider float64
}

// DefaultWeights returns the standard 35/40/25 split.
func DefaultWeights() Weights {
    return Weights{
        Market:  DefaultMarketWeight,
        Policy:  DefaultPolicyWeight,
        Insider: DefaultInsiderWeight,
    }
}

// SubScorer computes a single sub-score (0-100) for a company.
type SubScorer interface {
    Score(ctx context.Context, companyID int, now time.Time) (float64, error)
}

// NewEngine creates a score engine with the given sub-scorers and weights.
func NewEngine(store *db.Store, market, policy, insider SubScorer, weights Weights) *Engine {
    return &Engine{
        store:   store,
        market:  market,
        policy:  policy,
        insider: insider,
        weights: weights,
    }
}

// Compute calculates all sub-scores and the composite for a company.
func (e *Engine) Compute(ctx context.Context, companyID int) (*db.Score, error) {
    now := time.Now()

    m, err := e.market.Score(ctx, companyID, now)
    if err != nil {
        return nil, fmt.Errorf("market score: %w", err)
    }
    p, err := e.policy.Score(ctx, companyID, now)
    if err != nil {
        return nil, fmt.Errorf("policy score: %w", err)
    }
    i, err := e.insider.Score(ctx, companyID, now)
    if err != nil {
        return nil, fmt.Errorf("insider score: %w", err)
    }

    c := e.weights.Market*m + e.weights.Policy*p + e.weights.Insider*i

    return &db.Score{
        CompanyID:      companyID,
        MarketScore:    m,
        PolicyScore:    p,
        InsiderScore:   i,
        CompositeScore: c,
        WeightVersion:  1, // TODO: load from score_weights table
        ComputedAt:     now,
    }, nil
}

// composite calculates the weighted average using the given weights.
func composite(market, policy, insider float64, w Weights) float64 {
    return market*w.Market + policy*w.Policy + insider*w.Insider
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/score/ -run TestComposite -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/score/engine.go internal/score/engine_test.go
git commit -m "feat: score engine interface with composite calculator"
```

---

### Task 7: Market sub-score calculator

Computes market score (0-100) from price trend (30%), volume anomaly (30%), SEC Form 4 insider activity (40%). Window: 30 days.

**Files:**
- Create: `internal/score/market.go`
- Test: `internal/score/market_test.go`

- [ ] **Step 1: Define the DB queries needed**

The market scorer needs:
1. Price data for 5-day trend → `market_data` table (last 5 trading days)
2. Volume data for 30-day average → `market_data` table
3. SEC Form 4 insider trades → `insider_trades` table (last 30 days)

Add query methods to `internal/db/store.go` if they don't exist. We'll need:
- `GetMarketDataRange(ctx, companyID, since, until) ([]MarketData, error)`
- `GetInsiderTradesRange(ctx, companyID, since, until) ([]InsiderTrade, error)`

- [ ] **Step 2: Write failing test for market scorer**

```go
package score

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestMarketScorer_NoData(t *testing.T) {
    // With no market data and no insider trades, score should be 0
    ms := &MarketScorer{store: newMockScoreStore()}
    score, err := ms.Score(context.Background(), 1, time.Now())
    require.NoError(t, err)
    assert.InDelta(t, 0.0, score, 0.01)
}

func TestMarketScorer_StrongUptrend(t *testing.T) {
    // 5-day price increase of 10%, average volume, no insider trades
    // Price trend component: high → high sub-score
    mock := newMockScoreStore()
    mock.marketData = generateUptrend(1, 10.0, 30)
    ms := &MarketScorer{store: mock}
    score, err := ms.Score(context.Background(), 1, time.Now())
    require.NoError(t, err)
    assert.Greater(t, score, 50.0, "strong uptrend should score above 50")
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/score/ -run TestMarketScorer -v`
Expected: FAIL

- [ ] **Step 4: Implement MarketScorer**

```go
package score

import (
    "context"
    "math"
    "time"
)

const marketWindow = 30 * 24 * time.Hour

// MarketScorer computes the market sub-score (0-100).
// Components: price trend 30%, volume anomaly 30%, insider activity 40%.
type MarketScorer struct {
    store ScoreStore
}

func NewMarketScorer(store ScoreStore) *MarketScorer {
    return &MarketScorer{store: store}
}

func (ms *MarketScorer) Score(ctx context.Context, companyID int, now time.Time) (float64, error) {
    since := now.Add(-marketWindow)

    data, err := ms.store.GetMarketDataRange(ctx, companyID, since, now)
    if err != nil {
        return 0, err
    }
    if len(data) == 0 {
        return 0, nil
    }

    priceTrend := ms.priceTrend(data)
    volumeAnomaly := ms.volumeAnomaly(data)

    trades, err := ms.store.GetInsiderTradesRange(ctx, companyID, since, now)
    if err != nil {
        return 0, err
    }
    insiderActivity := ms.insiderActivity(trades, now)

    return clamp(priceTrend*0.30 + volumeAnomaly*0.30 + insiderActivity*0.40), nil
}

// priceTrend: last 5 data points percentage change, normalized to 0-100.
func (ms *MarketScorer) priceTrend(data []MarketDataRow) float64 {
    if len(data) < 2 {
        return 50 // neutral
    }
    // Use last 5 points or all if fewer
    n := min(5, len(data))
    recent := data[len(data)-n:]
    first := recent[0].PriceCents
    last := recent[len(recent)-1].PriceCents
    if first == 0 {
        return 50
    }
    pctChange := float64(last-first) / float64(first) * 100
    // Map -20%..+20% to 0..100
    return clamp((pctChange + 20) / 40 * 100)
}

// volumeAnomaly: current volume vs 30-day average, normalized to 0-100.
func (ms *MarketScorer) volumeAnomaly(data []MarketDataRow) float64 {
    if len(data) < 2 {
        return 50
    }
    var totalVol int64
    for _, d := range data {
        totalVol += d.Volume
    }
    avg := float64(totalVol) / float64(len(data))
    if avg == 0 {
        return 50
    }
    latest := float64(data[len(data)-1].Volume)
    ratio := latest / avg
    // Map 0.5x..2x to 0..100
    return clamp((ratio - 0.5) / 1.5 * 100)
}

// insiderActivity: SEC Form 4 activity, recency-weighted, normalized to 0-100.
func (ms *MarketScorer) insiderActivity(trades []InsiderTradeRow, now time.Time) float64 {
    if len(trades) == 0 {
        return 0
    }
    var weightedSum float64
    for _, t := range trades {
        age := now.Sub(t.TradedAt).Hours() / 24
        recencyWeight := math.Max(0, 1-age/30) // linear decay over 30 days
        magnitude := float64(t.Shares) * float64(t.PriceCents) / 100
        if t.TradeType == "sale" {
            magnitude = -magnitude
        }
        weightedSum += magnitude * recencyWeight
    }
    // Normalize: arbitrary scale, cap at ±$10M of activity
    normalized := weightedSum / 10_000_000 * 50
    return clamp(50 + normalized)
}

func clamp(v float64) float64 {
    if v < 0 {
        return 0
    }
    if v > 100 {
        return 100
    }
    return v
}
```

- [ ] **Step 5: Define ScoreStore interface**

Create a `ScoreStore` interface in `internal/score/engine.go` (or a separate `store.go`) that defines only the query methods the score package needs:

```go
// ScoreStore defines the data access methods needed by the score engine.
// This keeps the score package decoupled from the full db.Store.
type ScoreStore interface {
    GetMarketDataRange(ctx context.Context, companyID int, since, until time.Time) ([]MarketDataRow, error)
    GetInsiderTradesRange(ctx context.Context, companyID int, since, until time.Time) ([]InsiderTradeRow, error)
    GetCongressionalTradesRange(ctx context.Context, companyID int, since, until time.Time) ([]CongressionalTradeRow, error)
    GetContractsRange(ctx context.Context, companyID int, since, until time.Time) ([]ContractRow, error)
    GetSanctionsRange(ctx context.Context, companyID int, since, until time.Time) ([]SanctionRow, error)
    GetCourtFilingsRange(ctx context.Context, companyID int, since, until time.Time) ([]CourtFilingRow, error)
    GetTariffsForCompany(ctx context.Context, companyID int, since, until time.Time) ([]TariffRow, error)
    GetLobbyingRange(ctx context.Context, companyID int, since, until time.Time) ([]LobbyingRow, error)
    GetDonationsRange(ctx context.Context, companyID int, since, until time.Time) ([]DonationRow, error)
    InsertScore(ctx context.Context, score db.Score) error
}

// Row types for score calculations (lightweight projections, not full DB models)
type MarketDataRow struct {
    PriceCents int64
    Volume     int64
    ChangePct  float64
    RecordedAt time.Time
}

type InsiderTradeRow struct {
    TradeType  string // purchase/sale
    Shares     int
    PriceCents int64
    TradedAt   time.Time
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/score/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/score/market.go internal/score/market_test.go internal/score/engine.go
git commit -m "feat: market sub-score calculator (price trend, volume, insider trades)"
```

---

### Task 8: Policy sub-score calculator

Computes policy exposure score (0-100) from tariff hits (25%), sanctions proximity (25%), contract changes (25%), court filings (25%). Window: 90 days.

**Files:**
- Create: `internal/score/policy.go`
- Test: `internal/score/policy_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestPolicyScorer_NoData(t *testing.T) {
    ps := NewPolicyScorer(newMockScoreStore())
    score, err := ps.Score(context.Background(), 1, time.Now())
    require.NoError(t, err)
    assert.InDelta(t, 0.0, score, 0.01)
}

func TestPolicyScorer_HighTariffExposure(t *testing.T) {
    mock := newMockScoreStore()
    mock.tariffs = []TariffRow{{HSCodes: []string{"8471"}, ActionType: "new"}}
    ps := NewPolicyScorer(mock)
    score, err := ps.Score(context.Background(), 1, time.Now())
    require.NoError(t, err)
    assert.Greater(t, score, 0.0)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/score/ -run TestPolicyScorer -v`

- [ ] **Step 3: Implement PolicyScorer**

```go
package score

import (
    "context"
    "time"
)

const policyWindow = 90 * 24 * time.Hour

type PolicyScorer struct {
    store ScoreStore
}

func NewPolicyScorer(store ScoreStore) *PolicyScorer {
    return &PolicyScorer{store: store}
}

func (ps *PolicyScorer) Score(ctx context.Context, companyID int, now time.Time) (float64, error) {
    since := now.Add(-policyWindow)

    tariffs, err := ps.store.GetTariffsForCompany(ctx, companyID, since, now)
    if err != nil {
        return 0, err
    }
    tariffScore := ps.tariffScore(tariffs)

    sanctions, err := ps.store.GetSanctionsRange(ctx, companyID, since, now)
    if err != nil {
        return 0, err
    }
    sanctionScore := ps.sanctionScore(sanctions)

    contracts, err := ps.store.GetContractsRange(ctx, companyID, since, now)
    if err != nil {
        return 0, err
    }
    contractScore := ps.contractScore(contracts)

    filings, err := ps.store.GetCourtFilingsRange(ctx, companyID, since, now)
    if err != nil {
        return 0, err
    }
    courtScore := ps.courtScore(filings)

    return clamp(tariffScore*0.25 + sanctionScore*0.25 + contractScore*0.25 + courtScore*0.25), nil
}

func (ps *PolicyScorer) tariffScore(tariffs []TariffRow) float64 {
    if len(tariffs) == 0 {
        return 0
    }
    // Each tariff action contributes. New tariffs score higher.
    var total float64
    for _, t := range tariffs {
        switch t.ActionType {
        case "new":
            total += 30
        case "modified":
            total += 15
        case "removed":
            total += 5
        }
    }
    return clamp(total)
}

func (ps *PolicyScorer) sanctionScore(sanctions []SanctionRow) float64 {
    if len(sanctions) == 0 {
        return 0
    }
    // Any direct sanction is high exposure
    return clamp(float64(len(sanctions)) * 50)
}

func (ps *PolicyScorer) contractScore(contracts []ContractRow) float64 {
    if len(contracts) == 0 {
        return 0
    }
    var score float64
    for _, c := range contracts {
        switch c.ActionType {
        case "award":
            score += 20
        case "cancellation":
            score += 40
        case "modification":
            score += 10
        }
    }
    return clamp(score)
}

func (ps *PolicyScorer) courtScore(filings []CourtFilingRow) float64 {
    if len(filings) == 0 {
        return 0
    }
    return clamp(float64(len(filings)) * 25)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/score/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/score/policy.go internal/score/policy_test.go
git commit -m "feat: policy sub-score calculator (tariffs, sanctions, contracts, court)"
```

---

### Task 9: Insider sub-score calculator

Computes insider signal score (0-100) from congressional trades (40%), lobbying spend changes (30%), FEC donation spikes (30%). Window: 90 days.

**Files:**
- Create: `internal/score/insider.go`
- Test: `internal/score/insider_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestInsiderScorer_NoData(t *testing.T) {
    is := NewInsiderScorer(newMockScoreStore())
    score, err := is.Score(context.Background(), 1, time.Now())
    require.NoError(t, err)
    assert.InDelta(t, 0.0, score, 0.01)
}

func TestInsiderScorer_CongressionalTrades(t *testing.T) {
    mock := newMockScoreStore()
    mock.congressionalTrades = []CongressionalTradeRow{
        {TradeType: "buy", AmountRangeLow: 100000, TradedAt: time.Now().Add(-24 * time.Hour)},
    }
    is := NewInsiderScorer(mock)
    score, err := is.Score(context.Background(), 1, time.Now())
    require.NoError(t, err)
    assert.Greater(t, score, 0.0)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/score/ -run TestInsiderScorer -v`

- [ ] **Step 3: Implement InsiderScorer**

```go
package score

import (
    "context"
    "math"
    "time"
)

const insiderWindow = 90 * 24 * time.Hour

type InsiderScorer struct {
    store ScoreStore
}

func NewInsiderScorer(store ScoreStore) *InsiderScorer {
    return &InsiderScorer{store: store}
}

func (is *InsiderScorer) Score(ctx context.Context, companyID int, now time.Time) (float64, error) {
    since := now.Add(-insiderWindow)

    trades, err := is.store.GetCongressionalTradesRange(ctx, companyID, since, now)
    if err != nil {
        return 0, err
    }
    tradeScore := is.tradeScore(trades, now)

    lobbying, err := is.store.GetLobbyingRange(ctx, companyID, since, now)
    if err != nil {
        return 0, err
    }
    lobbyScore := is.lobbyScore(lobbying)

    donations, err := is.store.GetDonationsRange(ctx, companyID, since, now)
    if err != nil {
        return 0, err
    }
    donationScore := is.donationScore(donations, now)

    return clamp(tradeScore*0.40 + lobbyScore*0.30 + donationScore*0.30), nil
}

func (is *InsiderScorer) tradeScore(trades []CongressionalTradeRow, now time.Time) float64 {
    if len(trades) == 0 {
        return 0
    }
    var weighted float64
    for _, t := range trades {
        age := now.Sub(t.TradedAt).Hours() / 24
        recency := math.Max(0, 1-age/90)
        // Use midpoint of amount range as magnitude
        magnitude := float64(t.AmountRangeLow+t.AmountRangeHigh) / 2
        weighted += magnitude * recency / 100_000 // normalize per $100K
    }
    return clamp(weighted * 10)
}

func (is *InsiderScorer) lobbyScore(lobbying []LobbyingRow) float64 {
    if len(lobbying) == 0 {
        return 0
    }
    var totalCents int64
    for _, l := range lobbying {
        totalCents += l.AmountCents
    }
    // Normalize: $1M lobbying spend → score of 50
    return clamp(float64(totalCents) / 100 / 1_000_000 * 50)
}

func (is *InsiderScorer) donationScore(donations []DonationRow, now time.Time) float64 {
    if len(donations) == 0 {
        return 0
    }
    var weighted float64
    for _, d := range donations {
        age := now.Sub(d.DonatedAt).Hours() / 24
        recency := math.Max(0, 1-age/90)
        weighted += float64(d.AmountCents) / 100 * recency
    }
    // Normalize: $100K total weighted donations → score of 50
    return clamp(weighted / 100_000 * 50)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/score/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/score/insider.go internal/score/insider_test.go
git commit -m "feat: insider sub-score calculator (congressional trades, lobbying, donations)"
```

---

### Task 10: Score query methods for sub-scorers

The sub-scorers need range-query methods to fetch data from typed tables. These methods must exist on `db.Store` before the score engine can be wired into production (Task 12).

**Files:**
- Create: `internal/db/score_queries.go`
- Test: `internal/db/score_queries_test.go`

- [ ] **Step 1: Write failing tests for each range query**

```go
func TestGetMarketDataRange(t *testing.T) { ... }
func TestGetInsiderTradesRange(t *testing.T) { ... }
func TestGetCongressionalTradesRange(t *testing.T) { ... }
func TestGetContractsRange(t *testing.T) { ... }
func TestGetSanctionsRange(t *testing.T) { ... }
func TestGetCourtFilingsRange(t *testing.T) { ... }
func TestGetTariffsForCompany(t *testing.T) { ... }
func TestGetLobbyingRange(t *testing.T) { ... }
func TestGetDonationsRange(t *testing.T) { ... }
```

- [ ] **Step 2: Implement all range query methods**

Each query uses parameterized statements with company_id + time range:

```go
func (s *Store) GetMarketDataRange(ctx context.Context, companyID int, since, until time.Time) ([]MarketDataRow, error) {
    rows, err := s.db.Query(ctx, `
        SELECT price_cents, volume, change_pct, recorded_at
        FROM market_data
        WHERE company_id = $1 AND recorded_at BETWEEN $2 AND $3
        ORDER BY recorded_at`, companyID, since, until)
    // ...
}
```

For `GetTariffsForCompany`: join through `company_hs_codes` to find tariffs affecting the company's HS codes:

```go
func (s *Store) GetTariffsForCompany(ctx context.Context, companyID int, since, until time.Time) ([]TariffRow, error) {
    rows, err := s.db.Query(ctx, `
        SELECT t.hs_codes, t.affected_countries, t.action_type, t.effective_at
        FROM tariffs t
        JOIN company_hs_codes chc ON chc.hs_code = ANY(t.hs_codes)
        WHERE chc.company_id = $1 AND t.effective_at BETWEEN $2 AND $3
        ORDER BY t.effective_at`, companyID, since, until)
    // ...
}
```

- [ ] **Step 3: Run tests, commit**

```bash
git add internal/db/score_queries.go internal/db/score_queries_test.go
git commit -m "feat: score engine range query methods for all typed tables"
```

---

### Task 11: Score worker — broker subscriber that triggers recomputation

The `ScoreWorker` subscribes to the broker, listens for new events, and triggers score recomputation for the affected company. Includes debounce (waits 2s after last event before recomputing, batching rapid bursts).

**Files:**
- Create: `internal/score/worker.go`
- Test: `internal/score/worker_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestScoreWorker_TriggersRecompute(t *testing.T) {
    var computed atomic.Int32
    mockEngine := &mockScoreComputer{
        computeFn: func(ctx context.Context, companyID int) (*db.Score, error) {
            computed.Add(1)
            return &db.Score{CompanyID: companyID, CompositeScore: 50}, nil
        },
    }

    b := broker.New(100)
    defer b.Close()

    w := NewScoreWorker(b, mockEngine, nil) // nil store for insert
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go w.Run(ctx)

    // Publish an event with a company ID
    companyID := 42
    b.Publish(broker.Event{
        ID:        1,
        CompanyID: &companyID,
        Source:    "polygon",
        EventType: "daily_ohlcv",
    })

    require.Eventually(t, func() bool {
        return computed.Load() >= 1
    }, 5*time.Second, 50*time.Millisecond)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/score/ -run TestScoreWorker -v`

- [ ] **Step 3: Implement ScoreWorker**

```go
package score

import (
    "context"
    "log"
    "sync"
    "time"

    "github.com/arclighteng/mrdn/internal/broker"
    "github.com/arclighteng/mrdn/internal/db"
)

const debounceDuration = 2 * time.Second

// ScoreWorker subscribes to the broker and triggers score recomputation
// when events arrive for companies from scorable sources.
type ScoreWorker struct {
    broker  *broker.Broker
    engine  ScoreComputer
    store   *db.Store
}

func NewScoreWorker(b *broker.Broker, engine ScoreComputer, store *db.Store) *ScoreWorker {
    return &ScoreWorker{broker: b, engine: engine, store: store}
}

// Run starts the score worker loop. Blocks until ctx is cancelled.
func (sw *ScoreWorker) Run(ctx context.Context) {
    ch, err := sw.broker.Subscribe("score-worker")
    if err != nil {
        log.Printf("score worker: subscribe failed: %v", err)
        return
    }
    defer sw.broker.Unsubscribe("score-worker")

    // Debounce: collect company IDs, recompute after quiet period
    pending := make(map[int]struct{})
    var mu sync.Mutex
    timer := time.NewTimer(debounceDuration)
    timer.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case evt, ok := <-ch:
            if !ok {
                return
            }
            if evt.CompanyID == nil {
                continue
            }
            if !IsScorableSource(evt.Source) {
                continue
            }
            mu.Lock()
            pending[*evt.CompanyID] = struct{}{}
            mu.Unlock()
            timer.Reset(debounceDuration)

        case <-timer.C:
            mu.Lock()
            batch := pending
            pending = make(map[int]struct{})
            mu.Unlock()

            for companyID := range batch {
                s, err := sw.engine.Compute(ctx, companyID)
                if err != nil {
                    log.Printf("score worker: compute for company %d: %v", companyID, err)
                    continue
                }
                if sw.store != nil {
                    if err := sw.store.InsertScore(ctx, *s); err != nil {
                        log.Printf("score worker: insert score for company %d: %v", companyID, err)
                    }
                }
            }
        }
    }
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/score/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/score/worker.go internal/score/worker_test.go
git commit -m "feat: score worker with debounce — recomputes on new events"
```

---

### Task 12: Wire score engine into `mrdn ingest`

Connect the score worker to the ingest command so scores are recomputed as events arrive.

**Files:**
- Modify: `internal/cli/ingest.go`

- [ ] **Step 1: Read current ingest.go**

Read `internal/cli/ingest.go` to understand the current setup.

- [ ] **Step 2: Add score engine initialization**

After creating the supervisor and broker, initialize the score engine and start the score worker:

```go
// Score engine
scoreStore := db.NewStore(pool)
marketScorer := score.NewMarketScorer(scoreStore)
policyScorer := score.NewPolicyScorer(scoreStore)
insiderScorer := score.NewInsiderScorer(scoreStore)
engine := score.NewEngine(scoreStore, marketScorer, policyScorer, insiderScorer, score.DefaultWeights())

scoreWorker := score.NewScoreWorker(brk, engine, scoreStore)
go scoreWorker.Run(ctx)
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./...`
Expected: clean

- [ ] **Step 4: Commit**

```bash
git add internal/cli/ingest.go
git commit -m "feat: wire score engine into mrdn ingest command"
```

---

## Phase 2: Wire Real Sources (Parsers)

Each parser follows the same pattern: implement `Source` interface with `Name()` and `Poll()`, parse API response into `[]db.Event`, tested against golden fixture files.

### Task 13: SEC EDGAR Form 4 parser

**Files:**
- Create: `internal/parser/edgar.go`
- Create: `internal/parser/edgar_test.go`
- Create: `internal/parser/testdata/edgar_form4_sample.xml`

- [ ] **Step 1: Create test fixture**

Create a realistic SEC EDGAR Form 4 XML fixture at `internal/parser/testdata/edgar_form4_sample.xml`. Base this on the actual SEC EDGAR RSS feed format (XML with `ownershipDocument` elements).

- [ ] **Step 2: Write failing test**

```go
func TestEdgarSource_ParseForm4(t *testing.T) {
    data, err := os.ReadFile("testdata/edgar_form4_sample.xml")
    require.NoError(t, err)

    events, err := parseEdgarForm4(data)
    require.NoError(t, err)
    require.NotEmpty(t, events)

    evt := events[0]
    assert.Equal(t, "edgar_form4", evt.Source)
    assert.NotEmpty(t, evt.SourceID)
    assert.Equal(t, "insider_trade", evt.EventType)
    assert.NotEmpty(t, evt.EventData)
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/parser/ -run TestEdgarSource -v`

- [ ] **Step 4: Implement edgar.go**

Implement `EdgarSource` struct with `Name() string` returning `"edgar_form4"` and `Poll(ctx)` that:
1. Fetches the SEC EDGAR company filings RSS feed
2. Parses XML into Form 4 ownership documents
3. Extracts filer name, title, issuer ticker, trade type, shares, price, dates
4. Returns `[]db.Event` with `source_id` = accession number
5. Uses `io.LimitReader(10MB)` on response body
6. Uses hardcoded URL (no env var base URL — SSRF prevention)

- [ ] **Step 5: Run tests**

Run: `go test ./internal/parser/ -run TestEdgar -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/parser/edgar.go internal/parser/edgar_test.go internal/parser/testdata/edgar_form4_sample.xml
git commit -m "feat: SEC EDGAR Form 4 parser — insider trade ingestion"
```

---

### Task 14: Senate EFDS parser

**Files:**
- Create: `internal/parser/efds.go`
- Create: `internal/parser/efds_test.go`
- Create: `internal/parser/testdata/efds_sample.xml`

- [ ] **Step 1: Create fixture and write test**

Same pattern as EDGAR. Senate EFDS publishes XML e-filings with stock trade disclosures. Parse into `congressional_trade` event type.

- [ ] **Step 2: Implement efds.go**

`EfdsSource` with `Name() = "efds_senate"`. Polls the Senate EFDS XML feed. Extracts:
- Filer name, ticker, trade type (purchase/sale), amount range, dates
- Uses `encoding/xml` only, `io.LimitReader(10MB)`

- [ ] **Step 3: Run tests, commit**

```bash
git commit -m "feat: Senate EFDS parser — congressional stock trade ingestion"
```

---

### Task 15: USAspending parser

**Files:**
- Create: `internal/parser/usaspending.go`
- Create: `internal/parser/usaspending_test.go`
- Create: `internal/parser/testdata/usaspending_sample.json`

- [ ] **Step 1: Create fixture and write test**

USAspending.gov returns JSON with contract award data. Parse into `contract_award` event type.

- [ ] **Step 2: Implement usaspending.go**

`UsaspendingSource` with `Name() = "usaspending"`. Polls the USAspending REST API. Extracts:
- Recipient name, agency, amount, action type, description, award date
- Uses `json.Decoder` with size limit

- [ ] **Step 3: Run tests, commit**

```bash
git commit -m "feat: USAspending parser — federal contract ingestion"
```

---

### Task 16: Federal Register parser

**Files:**
- Create: `internal/parser/fedregister.go`
- Create: `internal/parser/fedregister_test.go`
- Create: `internal/parser/testdata/fedregister_sample.json`

- [ ] **Step 1: Create fixture, write test, implement**

Federal Register API returns JSON with executive orders, rules, tariff notices. Parse into appropriate event types.

- [ ] **Step 2: Run tests, commit**

```bash
git commit -m "feat: Federal Register parser — tariff and rule ingestion"
```

---

### Task 17: FEC parser

**Files:**
- Create: `internal/parser/fec.go`
- Create: `internal/parser/fec_test.go`
- Create: `internal/parser/testdata/fec_sample.json`

- [ ] **Step 1: Create fixture, write test, implement**

FEC API returns JSON with campaign donation data. Parse into `donation` event type. Individual donor names are exposed (per HD11 decision).

- [ ] **Step 2: Run tests, commit**

```bash
git commit -m "feat: FEC parser — campaign donation ingestion"
```

---

### Task 18: WARN Act parser (top 5 states)

**Files:**
- Create: `internal/parser/warn.go`
- Create: `internal/parser/warn_test.go`
- Create: `internal/parser/testdata/warn_ca_sample.html`

- [ ] **Step 1: Create fixture and write test**

WARN Act filings are per-state with no standard format. Start with California (EDD publishes a structured table). Parse into `warn_filing` event type.

- [ ] **Step 2: Implement warn.go**

`WarnSource` with `Name() = "warn"`. Starts with CA, TX, NY, IL, FL. Each state has its own parse function. Uses `io.LimitReader(10MB)`.

- [ ] **Step 3: Run tests, commit**

```bash
git commit -m "feat: WARN Act parser — top 5 states layoff filing ingestion"
```

---

### Task 19: OFAC SDN parser completion

The OFAC parser has test stubs but the implementation may be incomplete.

**Files:**
- Modify: `internal/parser/ofac.go`
- Test: `internal/parser/ofac_test.go`

- [ ] **Step 1: Review current state**

Read `internal/parser/ofac.go` and its tests. Complete any missing implementation.

- [ ] **Step 2: Run tests, commit**

```bash
git commit -m "feat: complete OFAC SDN parser implementation"
```

---

### Task 20: Finnhub WebSocket parser

**Files:**
- Create: `internal/parser/finnhub.go`
- Create: `internal/parser/finnhub_test.go`
- Create: `internal/parser/testdata/finnhub_trade_sample.json`

- [ ] **Step 1: Create fixture, write test, implement**

Finnhub sends real-time trade data over WebSocket. Implement `FinnhubSource` as a `StreamSource` (not `Source`) — it implements `Connect`, `Recv`, and `Close`.

The rebalancer already exists in `internal/ingestion/rebalancer.go`. Wire it to the Finnhub source.

- [ ] **Step 2: Run tests, commit**

```bash
git commit -m "feat: Finnhub WebSocket parser — real-time market data"
```

---

### Task 21: Register all sources in ingest command

Wire all parsers into the supervisor as registered sources.

**Files:**
- Modify: `internal/cli/ingest.go`

- [ ] **Step 1: Read current ingest.go**

- [ ] **Step 2: Register sources**

```go
sources := []ingestion.Source{
    parser.NewPolygonSource(httpClient, cfg.PolygonAPIKey),
    parser.NewEdgarSource(httpClient),
    parser.NewEfdsSource(httpClient),
    parser.NewUsaspendingSource(httpClient),
    parser.NewFedregisterSource(httpClient),
    parser.NewFecSource(httpClient, cfg.FECAPIKey),
    parser.NewOfacSource(httpClient),
}
sup.WithSources(sources)
// Finnhub is a StreamSource, handled separately
```

- [ ] **Step 3: Create hardened HTTP client**

Add a shared HTTP client with 30s timeout, redirect policy (strip API keys, reject non-HTTPS redirects), TLS handshake timeout:

```go
httpClient := &http.Client{
    Timeout: 30 * time.Second,
    CheckRedirect: func(req *http.Request, via []*http.Request) error {
        if req.URL.Scheme != "https" {
            return fmt.Errorf("refusing non-HTTPS redirect to %s", req.URL)
        }
        // Strip sensitive headers on redirect
        req.Header.Del("Authorization")
        req.Header.Del("X-API-Key")
        if len(via) >= 5 {
            return fmt.Errorf("too many redirects")
        }
        return nil
    },
    Transport: &http.Transport{
        TLSHandshakeTimeout: 10 * time.Second,
    },
}
```

- [ ] **Step 4: Verify compilation**

Run: `go build ./...`

- [ ] **Step 5: Commit**

```bash
git add internal/cli/ingest.go
git commit -m "feat: register all sources in ingest command with hardened HTTP client"
```

---

## Phase 3: Entity Resolution

### Task 22: Entity resolver — core pipeline

Single goroutine that subscribes to broker, resolves `company_id` on events where it's NULL.

**Files:**
- Create: `internal/resolve/resolver.go`
- Test: `internal/resolve/resolver_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestResolver_TickerMatch(t *testing.T) {
    mock := newMockResolveStore()
    mock.companies["NVDA"] = &db.Company{ID: 1, Ticker: "NVDA"}

    r := NewResolver(mock)
    companyID, err := r.Resolve(context.Background(), db.Event{
        EventType: "insider_trade",
        EventData: json.RawMessage(`{"ticker": "NVDA"}`),
    })
    require.NoError(t, err)
    require.NotNil(t, companyID)
    assert.Equal(t, 1, *companyID)
}

func TestResolver_NormalizedNameMatch(t *testing.T) {
    mock := newMockResolveStore()
    mock.aliases["nvidia corporation"] = &db.Company{ID: 1, Ticker: "NVDA"}

    r := NewResolver(mock)
    companyID, err := r.Resolve(context.Background(), db.Event{
        EventType: "contract_award",
        EventData: json.RawMessage(`{"recipient": "NVIDIA Corporation"}`),
    })
    require.NoError(t, err)
    require.NotNil(t, companyID)
    assert.Equal(t, 1, *companyID)
}
```

- [ ] **Step 2: Run to verify failure**

- [ ] **Step 3: Implement resolver.go**

Resolution pipeline (in order):
1. Extract ticker from event_data → look up in companies table
2. Extract entity name → normalize (lowercase, trim, remove Inc/Corp/LLC suffixes) → look up in entity_aliases
3. Check manual overrides in entity_aliases where `auto_applied = false`
4. If no match, leave company_id NULL (unresolved queue)

```go
package resolve

import (
    "context"
    "encoding/json"
    "log"
    "strings"

    "github.com/arclighteng/mrdn/internal/broker"
    "github.com/arclighteng/mrdn/internal/db"
)

type ResolveStore interface {
    GetCompanyByTicker(ctx context.Context, ticker string) (db.Company, error)
    GetCompanyByAlias(ctx context.Context, normalizedName string) (db.Company, error)
    GetEvent(ctx context.Context, id int) (db.Event, error)
    UpdateEventCompanyID(ctx context.Context, eventID, companyID int) error
}

type Resolver struct {
    store ResolveStore
}

func NewResolver(store ResolveStore) *Resolver {
    return &Resolver{store: store}
}

// Resolve attempts to find the company_id for an event.
// Returns nil if no match found.
func (r *Resolver) Resolve(ctx context.Context, evt db.Event) (*int, error) {
    // Layer 1: Ticker match
    ticker := extractTicker(evt.EventData)
    if ticker != "" {
        company, err := r.store.GetCompanyByTicker(ctx, ticker)
        if err == nil {
            return &company.ID, nil
        }
        // err means not found or DB error — fall through to next layer
    }

    // Layer 2: Normalized name match
    name := extractEntityName(evt.EventData, evt.EventType)
    if name != "" {
        normalized := normalizeName(name)
        company, err := r.store.GetCompanyByAlias(ctx, normalized)
        if err == nil {
            return &company.ID, nil
        }
    }

    return nil, nil // unresolved
}

// Run starts the resolver as a broker subscriber. Blocks until ctx is cancelled.
func (r *Resolver) Run(ctx context.Context, brk *broker.Broker) {
    ch, err := brk.Subscribe("entity-resolver")
    if err != nil {
        log.Printf("entity resolver: subscribe failed: %v", err)
        return
    }
    defer brk.Unsubscribe("entity-resolver")

    for {
        select {
        case <-ctx.Done():
            return
        case evt, ok := <-ch:
            if !ok {
                return
            }
            if evt.CompanyID != nil {
                continue // already resolved
            }
            r.resolveEvent(ctx, evt)
        }
    }
}

func (r *Resolver) resolveEvent(ctx context.Context, bEvt broker.Event) {
    // Fetch the full event from DB to get event_data
    // For now, we'll need a method to get events by ID
    // The broker event has the ID but not the full data
    // This is resolved by having the resolver also subscribe to the store
    log.Printf("entity resolver: resolving event %d from source %s", bEvt.ID, bEvt.Source)
}

func extractTicker(data json.RawMessage) string {
    var obj map[string]interface{}
    if err := json.Unmarshal(data, &obj); err != nil {
        return ""
    }
    if t, ok := obj["ticker"].(string); ok {
        return strings.ToUpper(strings.TrimSpace(t))
    }
    return ""
}

func extractEntityName(data json.RawMessage, eventType string) string {
    var obj map[string]interface{}
    if err := json.Unmarshal(data, &obj); err != nil {
        return ""
    }
    // Different event types store entity names in different fields
    for _, key := range []string{"recipient", "entity_name", "client", "company_name", "issuer", "filer_name"} {
        if name, ok := obj[key].(string); ok && name != "" {
            return name
        }
    }
    return ""
}

func normalizeName(name string) string {
    name = strings.ToLower(strings.TrimSpace(name))
    // Remove common suffixes
    for _, suffix := range []string{" inc", " inc.", " corp", " corp.", " llc", " ltd", " co.", " company", " corporation"} {
        name = strings.TrimSuffix(name, suffix)
    }
    return strings.TrimSpace(name)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/resolve/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/resolve/
git commit -m "feat: entity resolver — ticker match + normalized name match pipeline"
```

---

### Task 23: Add entity resolution DB methods

Add `GetCompanyByAlias`, `UpdateEventCompanyID`, and entity_aliases queries to the store.

**Files:**
- Create: `internal/db/entity_links.go`
- Test: `internal/db/entity_links_test.go`

- [ ] **Step 1: Write failing tests**

- [ ] **Step 2: Implement DB methods**

```go
// GetCompanyByAlias looks up a company by a normalized alias in entity_aliases.
func (s *Store) GetCompanyByAlias(ctx context.Context, normalizedName string) (*Company, error) {
    row := s.db.QueryRow(ctx, `
        SELECT c.id, c.ticker, c.name, c.sector, c.subsector, c.naics_code, c.market_cap_bucket
        FROM entity_aliases ea
        JOIN companies c ON ea.entity_id = c.id AND ea.entity_type = 'company'
        WHERE LOWER(ea.alias) = $1
        LIMIT 1`, normalizedName)
    var c Company
    err := row.Scan(&c.ID, &c.Ticker, &c.Name, &c.Sector, &c.Subsector, &c.NAICSCode, &c.MarketCapBucket)
    if err != nil {
        return nil, err
    }
    return &c, nil
}

// UpdateEventCompanyID sets the company_id on an event after entity resolution.
func (s *Store) UpdateEventCompanyID(ctx context.Context, eventID, companyID int) error {
    _, err := s.db.Exec(ctx, `UPDATE events SET company_id = $1 WHERE id = $2`, companyID, eventID)
    return err
}

// InsertEntityAlias adds an alias for entity resolution.
func (s *Store) InsertEntityAlias(ctx context.Context, entityID int, entityType, alias, source string, confidence float64, autoApplied bool) error {
    _, err := s.db.Exec(ctx, `
        INSERT INTO entity_aliases (entity_id, entity_type, alias, source, confidence, auto_applied)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT DO NOTHING`, entityID, entityType, alias, source, confidence, autoApplied)
    return err
}
```

- [ ] **Step 3: Run tests, commit**

```bash
git commit -m "feat: entity resolution DB methods — alias lookup, event update"
```

---

### Task 24: Wire resolver into ingest command

**Files:**
- Modify: `internal/cli/ingest.go`

- [ ] **Step 1: Add resolver startup**

After supervisor start, before signal wait:

```go
resolver := resolve.NewResolver(store)
go resolver.Run(ctx, brk)
```

- [ ] **Step 2: Commit**

```bash
git commit -m "feat: wire entity resolver into mrdn ingest"
```

---

## Phase 4: Typed Table Extraction

The `extract` package subscribes to the broker and dispatches events to type-specific extractors that parse `event_data` JSONB and insert into typed tables (congressional_trades, contracts, sanctions, etc.).

### Task 25: Extraction dispatcher

**Files:**
- Create: `internal/extract/dispatcher.go`
- Test: `internal/extract/dispatcher_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestDispatcher_RoutesToCorrectExtractor(t *testing.T) {
    var extracted atomic.Int32
    d := NewDispatcher(nil)
    d.Register("insider_trade", ExtractorFunc(func(ctx context.Context, evt db.Event) error {
        extracted.Add(1)
        return nil
    }))

    err := d.Dispatch(context.Background(), db.Event{EventType: "insider_trade"})
    require.NoError(t, err)
    assert.Equal(t, int32(1), extracted.Load())
}

func TestDispatcher_UnknownType_NoError(t *testing.T) {
    d := NewDispatcher(nil)
    err := d.Dispatch(context.Background(), db.Event{EventType: "unknown"})
    assert.NoError(t, err) // silently skip
}
```

- [ ] **Step 2: Implement dispatcher.go**

```go
package extract

import (
    "context"
    "log"

    "github.com/arclighteng/mrdn/internal/broker"
    "github.com/arclighteng/mrdn/internal/db"
)

// Extractor parses event_data and inserts into a typed table.
type Extractor interface {
    Extract(ctx context.Context, evt db.Event) error
}

// ExtractorFunc adapts a function to the Extractor interface.
type ExtractorFunc func(ctx context.Context, evt db.Event) error

func (f ExtractorFunc) Extract(ctx context.Context, evt db.Event) error {
    return f(ctx, evt)
}

// Dispatcher routes events to type-specific extractors.
type Dispatcher struct {
    store      *db.Store
    extractors map[string]Extractor
}

func NewDispatcher(store *db.Store) *Dispatcher {
    return &Dispatcher{
        store:      store,
        extractors: make(map[string]Extractor),
    }
}

func (d *Dispatcher) Register(eventType string, ext Extractor) {
    d.extractors[eventType] = ext
}

func (d *Dispatcher) Dispatch(ctx context.Context, evt db.Event) error {
    ext, ok := d.extractors[evt.EventType]
    if !ok {
        return nil // no extractor registered for this type
    }
    return ext.Extract(ctx, evt)
}

// Run starts the dispatcher as a broker subscriber. Blocks until ctx cancelled.
func (d *Dispatcher) Run(ctx context.Context, brk *broker.Broker) {
    ch, err := brk.Subscribe("extractor")
    if err != nil {
        log.Printf("extractor: subscribe failed: %v", err)
        return
    }
    defer brk.Unsubscribe("extractor")

    for {
        select {
        case <-ctx.Done():
            return
        case bEvt, ok := <-ch:
            if !ok {
                return
            }
            // Fetch full event from store
            evt, err := d.store.GetEvent(ctx, bEvt.ID)
            if err != nil {
                log.Printf("extractor: fetch event %d: %v", bEvt.ID, err)
                continue
            }
            if err := d.Dispatch(ctx, evt); err != nil {
                log.Printf("extractor: dispatch event %d (%s): %v", evt.ID, evt.EventType, err)
            }
        }
    }
}
```

- [ ] **Step 3: Run tests, commit**

```bash
git commit -m "feat: extraction dispatcher — routes events to typed table extractors"
```

---

### Task 26: Typed table DB methods

Add insert methods for congressional_trades, contracts, sanctions, insider_trades, donations, market_data.

**Files:**
- Create: `internal/db/typed_tables.go`
- Test: `internal/db/typed_tables_test.go`

- [ ] **Step 1: Write failing tests for each insert method**

- [ ] **Step 2: Implement all insert methods**

Each method takes a typed struct and inserts into the corresponding table with parameterized queries.

```go
func (s *Store) InsertCongressionalTrade(ctx context.Context, t *CongressionalTrade) error { ... }
func (s *Store) InsertContract(ctx context.Context, c *Contract) error { ... }
func (s *Store) InsertSanction(ctx context.Context, sn *Sanction) error { ... }
func (s *Store) InsertInsiderTrade(ctx context.Context, t *InsiderTrade) error { ... }
func (s *Store) InsertDonation(ctx context.Context, d *Donation) error { ... }
func (s *Store) InsertMarketData(ctx context.Context, m *MarketData) error { ... }
func (s *Store) InsertWarnFiling(ctx context.Context, w *WarnFiling) error { ... }
func (s *Store) InsertLobbyingRecord(ctx context.Context, l *Lobbying) error { ... }
func (s *Store) InsertCourtFiling(ctx context.Context, f *CourtFiling) error { ... }
```

- [ ] **Step 3: Run tests, commit**

```bash
git commit -m "feat: typed table insert methods for all event types"
```

---

### Task 27: Individual extractors

One extractor per event type. Each parses `event_data` JSONB and calls the corresponding typed table insert.

**Files:**
- Create: `internal/extract/trades.go` + test
- Create: `internal/extract/contracts.go` + test
- Create: `internal/extract/sanctions.go` + test
- Create: `internal/extract/insider_trades.go` + test
- Create: `internal/extract/donations.go` + test
- Create: `internal/extract/market.go` + test

- [ ] **Step 1: Write failing test for trades extractor**

```go
func TestTradesExtractor(t *testing.T) {
    mock := newMockExtractStore()
    ext := NewTradesExtractor(mock)
    evt := db.Event{
        ID: 1,
        EventType: "congressional_trade",
        EventData: json.RawMessage(`{"filer":"Tommy Tuberville","ticker":"NVDA","trade_type":"buy","amount_low":100000,"amount_high":250000}`),
    }
    err := ext.Extract(context.Background(), evt)
    require.NoError(t, err)
    assert.Len(t, mock.trades, 1)
}
```

- [ ] **Step 2: Implement each extractor** (one file per type, same pattern)

- [ ] **Step 3: Register all extractors in dispatcher**

- [ ] **Step 4: Wire dispatcher into ingest command**

- [ ] **Step 5: Run all tests, commit**

```bash
git commit -m "feat: typed table extractors for all event types"
```

---

## Phase 5: Persons & Connections

### Task 28: Person DB methods and seed migration

**Files:**
- Create: `internal/db/persons.go`
- Test: `internal/db/persons_test.go`
- Create: `internal/db/migrations/002_persons_seed.sql`

- [ ] **Step 1: Write failing tests for person CRUD**

```go
func TestUpsertPerson(t *testing.T) { ... }
func TestGetPersonBySlug(t *testing.T) { ... }
func TestListPersons(t *testing.T) { ... }
```

- [ ] **Step 2: Implement person DB methods**

```go
type Person struct {
    ID                 int
    Slug               string
    Name               string
    Role               string
    Tier               int
    Branch             *string
    LinkedPersonID     *int
    LinkedRelationship *string
    DisclosureSource   *string
}

type PersonFilter struct {
    Tier   *int
    Branch string
    Role   string
    Limit  int
    Offset int
}

func (s *Store) UpsertPerson(ctx context.Context, p *Person) error { ... }
func (s *Store) GetPersonBySlug(ctx context.Context, slug string) (*Person, error) { ... }
func (s *Store) ListPersons(ctx context.Context, f PersonFilter) ([]Person, error) { ... }
func (s *Store) CountPersons(ctx context.Context, f PersonFilter) (int, error) { ... }
```

- [ ] **Step 3: Create seed migration**

Write `002_persons_seed.sql` that inserts all 535 members of Congress (plus key spouses if available) from the @unitedstates/congress-legislators YAML data. This is a data-only migration.

- [ ] **Step 4: Run tests, commit**

```bash
git commit -m "feat: person CRUD + congressional seed data migration"
```

---

### Task 29: Person API endpoints

**Files:**
- Create: `internal/api/persons.go`
- Test: `internal/api/persons_test.go`
- Modify: `internal/api/server.go` (mount routes)

- [ ] **Step 1: Write failing tests**

```go
func TestListPersons(t *testing.T) { ... }
func TestGetPerson(t *testing.T) { ... }
```

- [ ] **Step 2: Implement handlers**

```
GET /api/v1/persons?tier=1&branch=legislative&limit=50&offset=0
GET /api/v1/persons/{slug}
```

- [ ] **Step 3: Mount routes in server.go**

- [ ] **Step 4: Run tests, commit**

```bash
git commit -m "feat: person API endpoints — list and get by slug"
```

---

### Task 30: Connection graph — BFS query

**Files:**
- Create: `internal/db/graph.go`
- Test: `internal/db/graph_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestBFSGraph_SingleHop(t *testing.T) {
    // Setup: company 1 → entity_link → person 2
    // Query BFS from company 1, depth 1
    // Expect 2 nodes, 1 edge
}
```

- [ ] **Step 2: Implement Go-side BFS**

```go
type GraphNode struct {
    ID     int    `json:"id"`
    Type   string `json:"type"` // company/person/agency
    Label  string `json:"label"`
    Ticker string `json:"ticker,omitempty"`
}

type GraphEdge struct {
    From         int    `json:"from"`
    FromType     string `json:"from_type"`
    To           int    `json:"to"`
    ToType       string `json:"to_type"`
    Relationship string `json:"relationship"`
}

type GraphResult struct {
    Nodes []GraphNode `json:"nodes"`
    Edges []GraphEdge `json:"edges"`
}

// BFSGraph performs breadth-first traversal of entity_links starting from a seed entity.
// depth: max hops (capped at 4), budget: max nodes returned.
// Uses statement_timeout per query to prevent runaway.
func (s *Store) BFSGraph(ctx context.Context, seedID int, seedType string, depth, budget int) (*GraphResult, error) {
    if depth > 4 {
        depth = 4
    }
    if budget > 500 {
        budget = 500
    }
    // ... BFS implementation using iterative SQL queries per level
}
```

Key implementation notes:
- Go-side BFS (not recursive CTE) — one SQL query per BFS level
- Each query has `statement_timeout` set via `SET LOCAL statement_timeout = '5s'`
- Hard depth cap at 4
- Node budget (default 200, max 500) — stop adding nodes once budget reached

- [ ] **Step 3: Run tests, commit**

```bash
git commit -m "feat: BFS graph traversal for connection explorer"
```

---

### Task 31: Connection API endpoints

**Files:**
- Create: `internal/api/connections.go`
- Test: `internal/api/connections_test.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Write failing tests, implement handlers**

```
GET /api/v1/connections/{ticker}?depth=2&limit=200
GET /api/v1/connections/{slug}?depth=2&limit=200    -- person view
GET /api/v1/connections/graph?depth=2&limit=200
```

- [ ] **Step 2: Mount routes, run tests, commit**

```bash
git commit -m "feat: connection graph API endpoints"
```

---

### Task 32: Missing spec API endpoints

Add endpoints required by spec but not yet implemented.

**Files:**
- Modify: `internal/api/server.go` (mount routes)
- Create: `internal/api/heatmap.go` + test
- Modify: `internal/api/companies.go` (add timeline handler)

- [ ] **Step 1: Implement `GET /scores/heatmap`**

Sector-level score aggregation. Query: group scores by company sector, return avg composite per sector.

```go
func (s *Server) handleScoresHeatmap(w http.ResponseWriter, r *http.Request) {
    // Query: SELECT c.sector, AVG(sc.composite_score), COUNT(*)
    // FROM scores sc JOIN companies c ON ...
    // GROUP BY c.sector
}
```

- [ ] **Step 2: Implement `GET /companies/{ticker}/timeline`**

Events + scores interleaved on a single timeline, ordered by time:

```go
func (s *Server) handleCompanyTimeline(w http.ResponseWriter, r *http.Request) {
    // Fetch events for company + score history
    // Merge and sort by timestamp
    // Return unified timeline array
}
```

- [ ] **Step 3: Mount routes, run tests, commit**

```bash
git commit -m "feat: scores heatmap + company timeline endpoints"
```

---

### Task 33: Freshness metadata on all API responses

The spec requires every response to include a `freshness` block. The existing handlers already use `ListResponse` and `DetailResponse` structs with a `Freshness` field, but not all handlers populate it correctly.

**Files:**
- Modify: `internal/api/handlers.go` (or each handler file)
- Modify: `internal/api/helpers.go` (freshness computation)

- [ ] **Step 1: Audit all handlers for freshness population**

Check every handler that returns `ListResponse` or `DetailResponse`. Ensure `Freshness` is populated by looking up the relevant source_meta.

- [ ] **Step 2: Add freshness to new endpoints (persons, connections, heatmap, timeline)**

- [ ] **Step 3: Run tests, commit**

```bash
git commit -m "fix: populate freshness metadata on all API responses"
```

---

## Phase 6: CLI Commands

### Task 34: `mrdn query` command

**Files:**
- Create: `internal/cli/query.go`
- Test: `internal/cli/query_test.go`

- [ ] **Step 1: Implement query subcommands**

```
mrdn query companies --sector tech --min-score 60
mrdn query events --source ofac --since 24h
mrdn query connections NVDA
```

Each subcommand calls the store directly and formats output as a table to stdout.

- [ ] **Step 2: Write tests**

Test each subcommand's flag parsing and output formatting using `bytes.Buffer` capture:

```go
func TestQueryCompanies_FlagParsing(t *testing.T) {
    cmd := newQueryCompaniesCmd(mockStore)
    cmd.SetArgs([]string{"--sector", "tech", "--min-score", "60"})
    var buf bytes.Buffer
    cmd.SetOut(&buf)
    require.NoError(t, cmd.Execute())
    assert.Contains(t, buf.String(), "NVDA")
}
```

- [ ] **Step 3: Run tests, commit**

```bash
git commit -m "feat: mrdn query command — companies, events, connections"
```

---

### Task 35: `mrdn scores` command

**Files:**
- Create: `internal/cli/scores_cmd.go`
- Test: `internal/cli/scores_cmd_test.go`

- [ ] **Step 1: Implement**

```
mrdn scores --movers --hours 6
mrdn scores --rankings --limit 20
mrdn scores --company NVDA
```

- [ ] **Step 2: Write tests** (same pattern as query — flag parsing + output formatting)

- [ ] **Step 3: Commit**

```bash
git commit -m "feat: mrdn scores command — movers, rankings, company detail"
```

---

### Task 36: `mrdn sources` command

**Files:**
- Create: `internal/cli/sources_cmd.go`
- Test: `internal/cli/sources_cmd_test.go`

- [ ] **Step 1: Implement**

```
mrdn sources          -- list all with status
mrdn sources polygon  -- detail for one source
```

- [ ] **Step 2: Write tests, commit**

```bash
git commit -m "feat: mrdn sources command — source health dashboard"
```

---

### Task 37: `mrdn link` command

Manual entity alias creation for the ops team.

**Files:**
- Create: `internal/cli/link.go`
- Test: `internal/cli/link_test.go`

- [ ] **Step 1: Implement**

```
mrdn link --alias "NVDA Corp" --entity NVDA
mrdn link --alias "Nvidia Corporation" --entity NVDA --confidence 0.95
```

Inserts into `entity_aliases` with `auto_applied = false`.

- [ ] **Step 2: Write tests, commit**

```bash
git commit -m "feat: mrdn link command — manual entity alias management"
```

---

## Phase 7: Production Hardening

### Task 38: Structured logging

Replace `log.Printf` calls with structured JSON logging using `log/slog` (stdlib in Go 1.21+).

**Files:**
- Modify: all files that use `log.Printf`

- [ ] **Step 1: Create logger setup**

In `internal/config/` or `cmd/mrdn/main.go`, initialize `slog` with JSON handler:

```go
logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
    Level: parseLogLevel(cfg.LogLevel),
}))
slog.SetDefault(logger)
```

- [ ] **Step 2: Replace log.Printf across codebase**

Replace `log.Printf("worker %s: ...")` with `slog.Error("worker poll failed", "source", name, "err", err)`.

- [ ] **Step 3: Run tests, commit**

```bash
git commit -m "refactor: structured JSON logging via slog"
```

---

### Task 39: Graceful shutdown coordination

Ensure all goroutines (supervisor, score worker, resolver, extractor, LISTEN bridge) shut down cleanly on SIGTERM.

**Files:**
- Modify: `internal/cli/ingest.go`
- Modify: `internal/cli/serve.go`

- [ ] **Step 1: Review current shutdown paths**

Both commands already trap SIGTERM. Verify that:
1. Context cancellation propagates to all goroutines
2. Supervisor.Stop() waits for workers
3. Broker.Close() is called after all subscribers stop
4. Pool.Close() is called last

- [ ] **Step 2: Add ordered shutdown in ingest.go**

```go
<-ctx.Done()
log.Println("shutting down...")
sup.Stop()         // workers first
// Score worker, resolver, extractor will exit via ctx cancellation
brk.Close()        // then broker
pool.Close()       // then DB
```

- [ ] **Step 3: Commit**

```bash
git commit -m "fix: ordered graceful shutdown for all goroutines"
```

---

### Task 40: Health check enrichment

Enrich `/health` to report source freshness and component status.

**Files:**
- Modify: `internal/api/handlers.go` (or wherever health handler lives)

- [ ] **Step 1: Implement enriched health**

```json
{
  "status": "ok",
  "components": {
    "database": "ok",
    "broker": {"subscribers": 12},
    "sources": {
      "polygon": "healthy",
      "ofac_sdn": "degraded"
    }
  }
}
```

- [ ] **Step 2: Run tests, commit**

```bash
git commit -m "feat: enriched health endpoint with source freshness"
```

---

### Task 41: DB connection pool monitoring

Add pool stats to health check and periodic logging.

**Files:**
- Modify: `internal/api/handlers.go`

- [ ] **Step 1: Add pool stats**

```go
stats := pool.Stat()
// Log: TotalConns, IdleConns, AcquiredConns, MaxConns
```

- [ ] **Step 2: Commit**

```bash
git commit -m "feat: DB pool stats in health check"
```

---

### Task 42: Rate limit configuration from DB

Currently rate limits are hardcoded. Load from `api_keys` table for per-key limits.

**Files:**
- Modify: `internal/api/ratelimit.go`

- [ ] **Step 1: Review current implementation**

The rate limiter already checks `api_keys` for per-key limits. Verify this works correctly and add a test.

- [ ] **Step 2: Commit if changes needed**

```bash
git commit -m "fix: verify per-key rate limits load from api_keys table"
```

---

### Task 43: Integration smoke test

A single test that wires everything together: insert an event, verify it triggers score recomputation, entity resolution, and typed table extraction.

**Files:**
- Create: `internal/integration_test.go` (build tag: `//go:build integration`)

- [ ] **Step 1: Write integration test**

This test requires a running Postgres instance. Guard with build tag.

```go
//go:build integration

func TestFullPipeline(t *testing.T) {
    // 1. Connect to test DB
    // 2. Run migrations
    // 3. Insert a company
    // 4. Create broker, score engine, resolver, extractor
    // 5. Insert an event via store
    // 6. Publish to broker
    // 7. Wait for: score computed, entity resolved, typed table populated
    // 8. Verify all three happened
}
```

- [ ] **Step 2: Commit**

```bash
git commit -m "test: integration smoke test for full event pipeline"
```
