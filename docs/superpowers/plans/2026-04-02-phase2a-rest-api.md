# MRDN Phase 2a: REST API Endpoints + Rate Limiting — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete read-only REST API with 11 endpoints, response envelope with freshness metadata, pagination, and rate limiting. No SSE streaming, no ingestion workers (those are Phase 2b).

**Architecture:** Extends the existing `api.Server` with new handler methods. All handlers follow the pattern in `health.go`: methods on `*Server` with `(w http.ResponseWriter, r *http.Request)` signature. Routes registered in `setupRoutes()` using chi. All DB access through the existing `*db.Store`.

**Tech Stack:** Same as Phase 1 — Go 1.25, chi v5, pgx v5, testify, httptest. New dependency: `golang.org/x/time/rate` for token bucket rate limiter.

**Spec:** `docs/superpowers/specs/2026-04-01-mrdn-design.md` (API section, freshness metadata, rate limiting, score response shapes)

---

## File Structure

```
internal/
├── api/
│   ├── server.go              -- MODIFY: add routes to setupRoutes()
│   ├── health.go              -- EXISTS: no changes
│   ├── health_test.go         -- EXISTS: no changes
│   ├── response.go            -- NEW: JSON envelope, pagination, freshness, error helpers
│   ├── response_test.go       -- NEW: unit tests for response helpers
│   ├── params.go              -- NEW: query param parsing + validation helpers
│   ├── params_test.go         -- NEW: unit tests for param parsing
│   ├── ratelimit.go           -- NEW: chi middleware, token bucket, API key lookup
│   ├── ratelimit_test.go      -- NEW: middleware tests
│   ├── companies.go           -- NEW: company endpoints (list, detail, scores, events)
│   ├── companies_test.go      -- NEW: handler tests
│   ├── events.go              -- NEW: event endpoints (list, detail, latest)
│   ├── events_test.go         -- NEW: handler tests
│   ├── scores.go              -- NEW: score endpoints (rankings, movers)
│   ├── scores_test.go         -- NEW: handler tests
│   ├── sources.go             -- NEW: source endpoints (list, detail)
│   └── sources_test.go        -- NEW: handler tests
├── db/
│   ├── companies.go           -- MODIFY: add Ticker filter, score range filter; add CountCompanies
│   ├── events.go              -- MODIFY: add CountEvents
│   ├── scores.go              -- MODIFY: add GetScoreMovers
│   ├── api_keys.go            -- NEW: GetAPIKey method
│   └── api_keys_test.go       -- NEW: test for GetAPIKey
```

---

## Task 1: Response Helpers (JSON Envelope, Pagination, Freshness, Errors)

Foundation for all endpoints. Every response wraps data in a standard envelope.

**Files:** `internal/api/response.go`, `internal/api/response_test.go`

### Types and Signatures

```go
package api

type Freshness struct {
    Source      string     `json:"source"`
    SourceLag   string    `json:"source_lag"`
    LastUpdated *time.Time `json:"last_updated"`
    AgeSeconds  int        `json:"age_seconds"`
    Grade       string     `json:"grade"`
}

type Pagination struct {
    Limit  int `json:"limit"`
    Offset int `json:"offset"`
    Total  int `json:"total"`
}

type ListResponse struct {
    Data       any         `json:"data"`
    Pagination *Pagination `json:"pagination,omitempty"`
    Freshness  any         `json:"freshness"`
}

type DetailResponse struct {
    Data      any `json:"data"`
    Freshness any `json:"freshness"`
}

type ErrorBody struct {
    Error string `json:"error"`
    Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, v any)
func writeError(w http.ResponseWriter, status int, code, message string)
func freshnessFromSource(sm db.SourceMeta) Freshness
```

**Grade logic:** A = age < 2x poll_interval, B = < 5x, C = < 10x, D = older or degraded/stale/down.

### Steps

- [ ] **1.1** Write `internal/api/response_test.go` — test `writeJSON` writes correct status + body, `writeError` returns `{"error":..., "code":...}`, `freshnessFromSource` produces correct grades for healthy/degraded/stale/down sources.
- [ ] **1.2** Write `internal/api/response.go` — implement. Run tests.

---

## Task 2: Query Parameter Parsing + Validation

Centralized param parsing so handlers stay lean.

**Files:** `internal/api/params.go`, `internal/api/params_test.go`

### Signatures

```go
func parseInt(r *http.Request, key string, defaultVal int) (int, error)
func parseFloat(r *http.Request, key string, defaultVal float64) (float64, error)
func parseTime(r *http.Request, key string) (*time.Time, error)
func parseString(r *http.Request, key, defaultVal string) string
func parsePagination(r *http.Request) (limit, offset int, err error)
```

`parsePagination` clamps limit to [1, 100] (default 50), offset to [0, maxInt] (default 0).

### Steps

- [ ] **2.1** Write `internal/api/params_test.go` — test valid/invalid/absent for each parser, pagination clamps to bounds.
- [ ] **2.2** Write `internal/api/params.go` — implement. Run tests.

---

## Task 3: Rate Limiting Middleware

In-memory token bucket per IP (anonymous) or per API key. chi middleware.

**Files:** `internal/api/ratelimit.go`, `internal/api/ratelimit_test.go`, `internal/db/api_keys.go`, `internal/db/api_keys_test.go`

**New dependency:** `golang.org/x/time/rate`

### DB Method

```go
type APIKey struct {
    ID        int       `json:"id"`
    KeyHash   string    `json:"key_hash"`
    Label     *string   `json:"label,omitempty"`
    RateLimit int       `json:"rate_limit"`
    CreatedAt time.Time `json:"created_at"`
}

func (s *Store) GetAPIKey(ctx context.Context, keyHash string) (APIKey, error)
```

### Middleware Logic

1. Read `X-API-Key` header
2. If present: SHA-256 hash it, look up in `api_keys` table
   - Found: use `key.RateLimit` req/min. Limiter key = `"key:" + hash`
   - Not found: return 401 `{"error": "invalid API key", "code": "INVALID_KEY"}`
3. If no header: use IP as limiter key with 60 req/min. Key = `"ip:" + clientIP`
4. Rate exceeded: return 429 with `Retry-After` header
5. Set `X-RateLimit-Limit` and `X-RateLimit-Remaining` headers

Cleanup goroutine every 5 min, evict entries older than 10 min.

### Steps

- [ ] **3.1** Write `internal/db/api_keys_test.go` — test `GetAPIKey` with valid/missing hash.
- [ ] **3.2** Write `internal/db/api_keys.go` — implement. Run DB tests.
- [ ] **3.3** Write `internal/api/ratelimit_test.go` — test anonymous allowed, anonymous exceeds 60 returns 429, valid API key gets higher limit, invalid key returns 401, `hashAPIKey` correctness.
- [ ] **3.4** Write `internal/api/ratelimit.go` — run `go get golang.org/x/time` first. Implement. Run tests.
- [ ] **3.5** Modify `internal/api/server.go` — add rate limiter and `/api/v1` route group (see Task 9 for full route table).

---

## Task 4: New DB Methods (CountCompanies, CountEvents, GetScoreMovers, Extended CompanyFilter)

**Files:** Modify `internal/db/companies.go`, `internal/db/events.go`, `internal/db/scores.go`

### Extended CompanyFilter

```go
type CompanyFilter struct {
    Sector       string
    Ticker       string    // partial match (ILIKE)
    MinComposite *float64  // latest composite score >= this
    MaxComposite *float64  // latest composite score <= this
    Limit        int
    Offset       int
}
```

### New Methods

```go
func (s *Store) CountCompanies(ctx context.Context, f CompanyFilter) (int, error)
func (s *Store) CountEvents(ctx context.Context, f EventFilter) (int, error)
```

```go
type ScoreMover struct {
    Ticker        string  `json:"ticker"`
    CompanyName   string  `json:"company_name"`
    PreviousScore float64 `json:"previous_score"`
    CurrentScore  float64 `json:"current_score"`
    Change        float64 `json:"change"`
    AbsChange     float64 `json:"abs_change"`
}

func (s *Store) GetScoreMovers(ctx context.Context, hours int, limit int) ([]ScoreMover, error)
```

**GetScoreMovers SQL:** Uses CTEs — `latest` (most recent score per company in window) and `previous` (the score before that), then computes change and orders by absolute change DESC.

### Steps

- [ ] **4.1** Add tests to `internal/db/companies_test.go` for `CountCompanies` and extended `ListCompanies` (ticker ILIKE, score range).
- [ ] **4.2** Modify `internal/db/companies.go` — extend filter, update query builder, add `CountCompanies`. Run tests.
- [ ] **4.3** Add tests to `internal/db/events_test.go` for `CountEvents`.
- [ ] **4.4** Modify `internal/db/events.go` — add `CountEvents`. Run tests.
- [ ] **4.5** Add tests to `internal/db/scores_test.go` for `GetScoreMovers`.
- [ ] **4.6** Modify `internal/db/scores.go` — add `ScoreMover` and `GetScoreMovers`. Run tests.

---

## Task 5: Company Endpoints

**Files:** `internal/api/companies.go`, `internal/api/companies_test.go`

### Handlers

```go
// GET /api/v1/companies?sector=X&ticker=X&min_score=N&max_score=N&limit=N&offset=N
func (s *Server) handleListCompanies(w http.ResponseWriter, r *http.Request)

// GET /api/v1/companies/{ticker}
func (s *Server) handleGetCompany(w http.ResponseWriter, r *http.Request)

// GET /api/v1/companies/{ticker}/scores?limit=N
func (s *Server) handleCompanyScores(w http.ResponseWriter, r *http.Request)

// GET /api/v1/companies/{ticker}/events?source=X&type=X&since=T&limit=N&offset=N
func (s *Server) handleCompanyEvents(w http.ResponseWriter, r *http.Request)
```

**handleGetCompany response shape** (per spec):
```json
{
  "data": {
    "id": 1, "ticker": "NVDA", "name": "NVIDIA Corp", "sector": "Technology",
    "scores": { "market": 72.5, "policy": 91.0, "insider": 45.3, "composite": 73.8 },
    "weight_version": 1, "computed_at": "2026-04-01T15:00:00Z"
  },
  "freshness": { ... }
}
```

### Test Helper

```go
func setupTestServer(t *testing.T) (*api.Server, *db.Store) {
    t.Helper()
    dsn := os.Getenv("DATABASE_URL")
    if dsn == "" { t.Skip("DATABASE_URL not set") }
    ctx := context.Background()
    pool, err := db.Connect(ctx, dsn)
    require.NoError(t, err)
    require.NoError(t, db.Migrate(ctx, pool))
    t.Cleanup(func() { pool.Close() })
    store := db.NewStore(pool)
    return api.NewServer(store), store
}
```

### Steps

- [ ] **5.1** Write `internal/api/companies_test.go` — test list 200 with pagination, get 404 for unknown ticker, get returns company+scores, company scores returns history, company events returns events.
- [ ] **5.2** Write `internal/api/companies.go` — implement. Run tests.

---

## Task 6: Event Endpoints

**Files:** `internal/api/events.go`, `internal/api/events_test.go`

### Handlers

```go
// GET /api/v1/events?source=X&type=X&since=T&limit=N&offset=N
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request)

// GET /api/v1/events/{id}
func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request)

// GET /api/v1/events/latest?limit=N
func (s *Server) handleLatestEvents(w http.ResponseWriter, r *http.Request)
```

**Note:** `/events/latest` registered before `/events/{id}` in chi to avoid "latest" being parsed as an ID.

### Steps

- [ ] **6.1** Write `internal/api/events_test.go` — test list with filters 200, get by ID, invalid ID returns 400, missing ID returns 404, latest returns limited set.
- [ ] **6.2** Write `internal/api/events.go` — implement. Run tests.

---

## Task 7: Score Endpoints

**Files:** `internal/api/scores.go`, `internal/api/scores_test.go`

### Handlers

```go
// GET /api/v1/scores/rankings?limit=N
func (s *Server) handleScoreRankings(w http.ResponseWriter, r *http.Request)

// GET /api/v1/scores/movers?hours=N&limit=N
func (s *Server) handleScoreMovers(w http.ResponseWriter, r *http.Request)
```

Rankings: default limit 100, max 500. Movers: default hours 24 (max 168), default limit 20 (max 100).

### Steps

- [ ] **7.1** Write `internal/api/scores_test.go` — test rankings sorted, movers with score changes, movers empty returns `[]`.
- [ ] **7.2** Write `internal/api/scores.go` — implement. Run tests.

---

## Task 8: Source Endpoints

**Files:** `internal/api/sources.go`, `internal/api/sources_test.go`

### Handlers

```go
// GET /api/v1/sources
func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request)

// GET /api/v1/sources/{name}
func (s *Server) handleGetSource(w http.ResponseWriter, r *http.Request)
```

### Steps

- [ ] **8.1** Write `internal/api/sources_test.go` — test list returns seeded sources, get returns single source, unknown name returns 404.
- [ ] **8.2** Write `internal/api/sources.go` — implement. Run tests.

---

## Task 9: Route Registration + Integration Smoke Test

### Updated setupRoutes

```go
func (s *Server) setupRoutes() {
    r := chi.NewRouter()
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)
    r.Use(middleware.SetHeader("Content-Type", "application/json"))

    rl := NewRateLimiter(s.store)
    r.Use(rl.Middleware())

    r.Get("/health", s.handleHealth)

    r.Route("/api/v1", func(r chi.Router) {
        r.Get("/companies", s.handleListCompanies)
        r.Get("/companies/{ticker}", s.handleGetCompany)
        r.Get("/companies/{ticker}/scores", s.handleCompanyScores)
        r.Get("/companies/{ticker}/events", s.handleCompanyEvents)

        r.Get("/events", s.handleListEvents)
        r.Get("/events/latest", s.handleLatestEvents)
        r.Get("/events/{id}", s.handleGetEvent)

        r.Get("/scores/rankings", s.handleScoreRankings)
        r.Get("/scores/movers", s.handleScoreMovers)

        r.Get("/sources", s.handleListSources)
        r.Get("/sources/{name}", s.handleGetSource)
    })

    s.router = r
}
```

### Steps

- [ ] **9.1** Verify all routes registered. Write `internal/api/routes_test.go` — confirm all 11 endpoints return non-404 with real DB.
- [ ] **9.2** Run full test suite: `go test ./internal/... -v -count=1`. Fix any failures.

---

## Dependency Graph

| Task | Depends On | Can Parallel With |
|------|-----------|-------------------|
| 1 (Response) | — | 2, 4, 10 |
| 2 (Params) | — | 1, 4, 10 |
| 3 (Rate limit) | 1 | — |
| 4 (DB methods) | — | 1, 2, 10 |
| 5 (Companies) | 1, 2, 4 | 6, 7, 8 |
| 6 (Events) | 1, 2, 4 | 5, 7, 8 |
| 7 (Scores) | 1, 2, 4 | 5, 6, 8 |
| 8 (Sources) | 1, 2 | 5, 6, 7 |
| 9 (Routes) | 3, 5, 6, 7, 8 | — |

**Wave 1 (parallel):** Tasks 1 + 2 + 4
**Wave 2 (parallel):** Tasks 3 + 5 + 6 + 7 + 8
**Wave 3:** Task 9

---

## Error Code Reference

| HTTP | Code | When |
|------|------|------|
| 400 | `BAD_REQUEST` | Malformed query param, invalid ID |
| 401 | `INVALID_KEY` | X-API-Key present but not in DB |
| 404 | `NOT_FOUND` | Ticker/ID/source not found |
| 429 | `RATE_LIMITED` | Token bucket exhausted |
| 500 | `INTERNAL_ERROR` | Unexpected DB error |
