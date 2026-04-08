# MRDN Phase 2b: SSE Streaming + Ingestion Workers — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add ingestion worker framework with 8 source parsers, SSE streaming endpoints, score recomputation pipeline, and supporting infrastructure (broker, graceful shutdown, config extensions).

**Architecture decisions (from orchestration review 2026-04-03):**
- **Separate processes:** `mrdn serve` (API + SSE) and `mrdn ingest` (workers) are independent binaries. Postgres LISTEN/NOTIFY bridges events between them.
- **event_data is untrusted:** 64KB size cap, 10-level depth limit at storage. Documented in API contract. Frontend must escape.
- **SSE limits:** 3/IP anonymous, 10/key, 500 global concurrent connections. 30-minute max duration with `Last-Event-ID` reconnection.
- **Finnhub rotation:** Top 25 companies by heat score + 5 buffer slots. Periodic rebalance via `Rebalance(symbols)`.
- **Test isolation:** Transaction rollback per test (Store refactored to accept `pgx.Tx`).
- **Source URLs hardcoded:** No env-var base URLs (SSRF prevention). Only API keys from env.
- **XML parsing:** stdlib `encoding/xml` only. `io.LimitReader(10MB)` on all XML input.
- **Clock injection:** 2-method `Clock` interface in `internal/ingestion/` for deterministic backoff tests.
- **Broker:** In-memory pub/sub for intra-process fan-out. LISTEN/NOTIFY for cross-process (serve ↔ ingest).

**Tech Stack:** Go 1.25, chi v5, pgx v5 (+ LISTEN/NOTIFY), cobra, testify, `nhooyr.io/websocket` (Finnhub only), `go.uber.org/goleak` (test-only).

**Spec:** `docs/superpowers/specs/2026-04-01-mrdn-design.md`
**Orchestration report:** Inline in session 2026-04-03 (Architect + Security + QA)

---

## File Structure

```
internal/
├── broker/
│   ├── broker.go              -- NEW: in-memory pub/sub (publish, subscribe, unsubscribe)
│   └── broker_test.go         -- NEW: subscribe/publish/drop/cap tests
├── ingestion/
│   ├── source.go              -- NEW: Source interface, StreamSource interface
│   ├── worker.go              -- NEW: PollWorker loop (backoff, panic recovery, context)
│   ├── worker_test.go         -- NEW: lifecycle tests with fake source + fake clock
│   ├── stream_worker.go       -- NEW: StreamWorker for persistent connections (Finnhub)
│   ├── stream_worker_test.go  -- NEW: connect/recv/reconnect/shutdown tests
│   ├── supervisor.go          -- NEW: manages all workers, signal handling
│   ├── supervisor_test.go     -- NEW: start/stop/restart tests
│   ├── backoff.go             -- NEW: exponential backoff with jitter
│   ├── backoff_test.go        -- NEW: deterministic sequence tests
│   ├── clock.go               -- NEW: Clock interface + realClock + fakeClock
│   ├── rebalancer.go          -- NEW: Finnhub symbol rotation (top 25 + 5 buffer)
│   ├── rebalancer_test.go     -- NEW: ranking + rotation tests
│   └── testdata/              -- NEW: shared test fixtures
├── parser/
│   ├── parser.go              -- NEW: common types, validation helpers, event_data limits
│   ├── ofac.go                -- NEW: OFAC SDN JSON/XML parser
│   ├── ofac_test.go           -- NEW: golden file tests
│   ├── polygon.go             -- NEW: Polygon.io daily OHLCV parser
│   ├── polygon_test.go        -- NEW
│   ├── edgar.go               -- NEW: SEC EDGAR Form 4 XML parser
│   ├── edgar_test.go          -- NEW
│   ├── efds.go                -- NEW: Senate EFDS XML parser
│   ├── efds_test.go           -- NEW
│   ├── usaspending.go         -- NEW: USAspending.gov JSON parser
│   ├── usaspending_test.go    -- NEW
│   ├── fedregister.go         -- NEW: Federal Register JSON parser
│   ├── fedregister_test.go    -- NEW
│   ├── fec.go                 -- NEW: FEC JSON/CSV parser
│   ├── fec_test.go            -- NEW
│   ├── finnhub.go             -- NEW: Finnhub WebSocket message parser
│   ├── finnhub_test.go        -- NEW
│   └── testdata/              -- NEW: per-source fixture files (XML, JSON, CSV)
│       ├── ofac_sample.json
│       ├── ofac_sample.xml
│       ├── edgar_form4_sample.xml
│       ├── efds_sample.xml
│       ├── polygon_daily_sample.json
│       ├── usaspending_sample.json
│       ├── fedregister_sample.json
│       ├── fec_sample.json
│       ├── fec_sample.csv
│       └── finnhub_trade_sample.json
├── api/
│   ├── server.go              -- MODIFY: mount SSE routes outside JSON middleware, add SSE manager
│   ├── stream.go              -- NEW: SSE handlers (/stream, /stream/:ticker, /stream/scores)
│   ├── stream_test.go         -- NEW: SSE tests using httptest.NewServer
│   ├── sse.go                 -- NEW: SSE connection manager (per-IP limits, heartbeat, duration cap)
│   ├── sse_test.go            -- NEW: connection limit + heartbeat tests
│   └── middleware.go          -- NEW: CORS + security headers middleware
├── cli/
│   ├── serve.go               -- MODIFY: http.Server with timeouts, signal handling, LISTEN/NOTIFY
│   └── ingest.go              -- NEW: `mrdn ingest` command — starts supervisor
├── config/
│   └── config.go              -- MODIFY: add source API keys, poll intervals, SSE limits
├── db/
│   ├── db.go                  -- MODIFY: configure pgxpool limits, add LISTEN/NOTIFY helpers
│   ├── events.go              -- MODIFY: add event_data size/depth validation in InsertEvent
│   ├── notify.go              -- NEW: LISTEN/NOTIFY wrapper (publish event IDs, subscribe)
│   ├── notify_test.go         -- NEW: integration test for NOTIFY
│   └── store.go               -- NEW: refactor Store to accept pgx.Tx for test isolation
```

---

## Dependency Graph

```
Wave 1 (no dependencies — parallel):
  Task 1: Config extensions
  Task 2: Broker
  Task 3: Clock + Backoff
  Task 4: Store refactor (Tx support)
  Task 5: Security hardening (serve.go timeouts, headers, CORS, pgxpool)

Wave 2 (depends on Wave 1):
  Task 6: Source interface + PollWorker        → depends on 2, 3
  Task 7: event_data validation               → depends on 4
  Task 8: LISTEN/NOTIFY bridge                → depends on 2, 5

Wave 3 (depends on Wave 2):
  Task 9: SSE connection manager + handlers   → depends on 6, 8
  Task 10: Supervisor + CLI                   → depends on 6

Wave 4 (depends on Wave 2, parallel):
  Task 11: OFAC SDN parser (first parser, validates interface)  → depends on 6
  Task 12: Polygon.io parser                  → depends on 6
  Task 13: SEC EDGAR Form 4 parser            → depends on 6
  Task 14: Senate EFDS parser                 → depends on 6
  Task 15: USAspending parser                 → depends on 6
  Task 16: Federal Register parser            → depends on 6
  Task 17: FEC parser                         → depends on 6

Wave 5 (depends on Wave 3):
  Task 18: Finnhub StreamWorker + parser      → depends on 6, 9, 10
  Task 19: Rebalancer (symbol rotation)       → depends on 18
  Task 20: Score recomputation worker         → depends on 2, 6
```

---

## Task 1: Config Extensions

Extend `internal/config/config.go` with source API keys and operational settings.

**File:** `internal/config/config.go` — MODIFY

- [ ] Add `FinnhubAPIKey string` loaded from `MRDN_FINNHUB_API_KEY`
- [ ] Add `PolygonAPIKey string` loaded from `MRDN_POLYGON_API_KEY`
- [ ] Add `FECAPIKey string` loaded from `MRDN_FEC_API_KEY` (FEC requires key)
- [ ] Add `SSEMaxPerIP int` (default 3), `SSEMaxPerKey int` (default 10), `SSEMaxGlobal int` (default 500)
- [ ] Add `SSEMaxDuration time.Duration` (default 30m)
- [ ] Add `SourceEnabled map[string]bool` for toggling individual sources
- [ ] Validate: if ingestion mode and a required API key is empty, return error at startup (fail loud)
- [ ] Update `.env.example` with all new vars

**Test:** `internal/config/config_test.go` — MODIFY
- [ ] Test loading new env vars
- [ ] Test validation fails when required keys missing

**Lines:** ~40 new

---

## Task 2: In-Memory Broker

Channel-based pub/sub for intra-process event distribution.

**File:** `internal/broker/broker.go` — NEW

- [ ] `type Event struct { ID int; CompanyID *int; Ticker string; Source string; EventType string; OccurredAt time.Time }`
- [ ] `type Broker struct` with `sync.RWMutex`-protected subscriber map
- [ ] `func New() *Broker`
- [ ] `func (b *Broker) Subscribe(id string) (<-chan Event, error)` — returns buffered channel (64 cap). Error if at global cap.
- [ ] `func (b *Broker) Unsubscribe(id string)` — removes and closes channel
- [ ] `func (b *Broker) Publish(evt Event)` — fan-out to all subscribers. On full buffer, drop oldest (non-blocking send after drain).
- [ ] `func (b *Broker) Close()` — close all channels, clear map
- [ ] `func (b *Broker) Count() int` — current subscriber count
- [ ] Hard cap: reject `Subscribe` when `Count() >= maxGlobal` (configurable)

**Test:** `internal/broker/broker_test.go` — NEW
- [ ] TestPublishReceive — subscribe, publish, assert received
- [ ] TestMultipleSubscribers — 3 subscribers all get same event
- [ ] TestUnsubscribe — channel closed after unsubscribe
- [ ] TestFullBufferDropsOldest — fill buffer, publish more, verify no block and newest survives
- [ ] TestSubscribeAtCap — returns error when at max subscribers
- [ ] TestClose — all channels closed
- [ ] Use `goleak.VerifyNone(t)` in TestMain

**Lines:** ~120 broker + ~100 tests

---

## Task 3: Clock + Backoff

Deterministic time control for testable backoff.

**File:** `internal/ingestion/clock.go` — NEW
- [ ] `type Clock interface { Now() time.Time; After(d time.Duration) <-chan time.Time }`
- [ ] `type realClock struct{}` implementing with `time.Now()` and `time.After()`
- [ ] `type fakeClock struct` with manually controllable `Now()` and immediately-ready `After()`

**File:** `internal/ingestion/backoff.go` — NEW
- [ ] `type Backoff struct { Base, Max time.Duration; attempt int; clock Clock }`
- [ ] `func (b *Backoff) Next() time.Duration` — `min(base * 2^attempt + jitter, max)`, increment attempt
- [ ] `func (b *Backoff) Reset()` — set attempt to 0
- [ ] `func (b *Backoff) Wait(ctx context.Context) error` — sleep via `clock.After()`, return ctx.Err() if cancelled
- [ ] Jitter: random 0–3s via `math/rand/v2`
- [ ] Default: base=5s, max=15min

**Test:** `internal/ingestion/backoff_test.go` — NEW
- [ ] TestBackoffSequence — verify 5s, 10s, 20s, 40s, ... caps at 15min
- [ ] TestBackoffReset — reset returns to 5s
- [ ] TestBackoffWaitCancelled — context cancel returns immediately
- [ ] TestBackoffJitter — duration differs between calls (non-deterministic but bounded)

**Lines:** ~60 clock + ~50 backoff + ~60 tests

---

## Task 4: Store Refactor for Test Isolation

Refactor `db.Store` to accept either `*pgxpool.Pool` or `pgx.Tx` for transaction-per-test.

**File:** `internal/db/store.go` — NEW
- [ ] Define `type DBTX interface` with `Exec`, `Query`, `QueryRow` methods (pgx's common interface)
- [ ] Refactor `Store` to hold `DBTX` instead of `*pgxpool.Pool`
- [ ] `func NewStore(db DBTX) *Store`
- [ ] `func NewStoreFromPool(pool *pgxpool.Pool) *Store` — convenience wrapper
- [ ] Update all existing Store methods to use `s.db` (the interface) instead of `s.pool`

**Modify existing files:**
- [ ] `internal/db/companies.go` — change `s.pool.Query/QueryRow` → `s.db.Query/QueryRow`
- [ ] `internal/db/events.go` — same
- [ ] `internal/db/scores.go` — same
- [ ] `internal/db/source_meta.go` — same
- [ ] `internal/db/api_keys.go` — same

**Test helper update:**
- [ ] `internal/api/companies_test.go` `setupTestServer` — begin TX, create Store from TX, t.Cleanup rolls back
- [ ] Verify all 49 existing tests still pass

**Lines:** ~40 new store.go + ~30 modifications across 5 files + ~15 test helper changes

---

## Task 5: Security Hardening

Fix all blocking security findings from orchestration review.

**File:** `internal/cli/serve.go` — MODIFY
- [ ] Replace `http.ListenAndServe` with `http.Server{ ReadTimeout: 10s, ReadHeaderTimeout: 5s, WriteTimeout: 30s, IdleTimeout: 120s, MaxHeaderBytes: 1<<20 }`
- [ ] Add signal trapping: `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)`
- [ ] Call `httpServer.Shutdown(shutdownCtx)` with 10s grace period
- [ ] Call `srv.Shutdown()` for API server cleanup

**File:** `internal/api/middleware.go` — NEW
- [ ] Security headers middleware: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`
- [ ] CORS middleware using chi/cors: allow `localhost:*` dev, `mrdn.arclighteng.com` prod. Never `*` with credentials.

**File:** `internal/api/server.go` — MODIFY
- [ ] Add security headers middleware to router
- [ ] Add CORS middleware to router

**File:** `internal/db/db.go` — MODIFY
- [ ] Configure pgxpool: `MaxConns` (match Supabase limit, default 20), `MinConns` (2), `MaxConnLifetime` (30m), `MaxConnIdleTime` (5m)

**Test:**
- [ ] TestSecurityHeaders — verify headers present on response
- [ ] TestCORS — verify allowed/denied origins

**Lines:** ~60 middleware + ~30 serve.go changes + ~20 db.go changes + ~40 tests

---

## Task 6: Source Interface + PollWorker

The core worker loop that all poll-based sources use.

**File:** `internal/ingestion/source.go` — NEW
- [ ] `type Source interface { Name() string; Poll(ctx context.Context) ([]db.Event, error) }`
- [ ] `type StreamSource interface { Name() string; Connect(ctx context.Context) error; Recv(ctx context.Context) ([]db.Event, error); Close() error }`

**File:** `internal/ingestion/worker.go` — NEW
- [ ] `type PollWorker struct { source Source; store *db.Store; broker *broker.Broker; backoff *Backoff; interval time.Duration; clock Clock }`
- [ ] `func (w *PollWorker) Run(ctx context.Context)` — loop: wait interval → Poll → InsertEvent for each → RecordPoll → Publish to broker → on error: backoff + SetSourceStatus degraded/down
- [ ] Panic recovery: `defer func() { if r := recover(); r != nil { log source down, backoff, restart } }()`
- [ ] On successful poll with new data: `backoff.Reset()`, `SetSourceStatus("healthy")`
- [ ] After 3 consecutive failures: status = "degraded". After 10: status = "down".

**File:** `internal/ingestion/worker_test.go` — NEW
- [ ] `type fakeSource struct` — returns canned events or errors, counts Poll calls via channel
- [ ] TestPollWorker_HappyPath — polls, inserts, publishes, records poll
- [ ] TestPollWorker_BacksOff — source returns error, verify increasing delays
- [ ] TestPollWorker_ResetsOnSuccess — error then success, verify backoff reset
- [ ] TestPollWorker_ShutdownMidSleep — cancel context during backoff wait
- [ ] TestPollWorker_PanicRecovery — source panics, worker restarts
- [ ] TestPollWorker_DegradedAfter3Failures — verify status transitions
- [ ] TestPollWorker_Dedup — same source_id twice, second is no-op (ON CONFLICT)
- [ ] `goleak.VerifyNone(t)` in TestMain

**Lines:** ~30 source.go + ~120 worker.go + ~150 tests

---

## Task 7: event_data Validation

Add size and depth limits to event insertion.

**File:** `internal/db/events.go` — MODIFY
- [ ] Before INSERT: check `len(event.EventData) <= 65536` (64KB). Return error if exceeded.
- [ ] Before INSERT: validate JSON depth ≤ 10 levels (simple recursive check or `json.Decoder` with depth counter)
- [ ] Validate JSON is well-formed via `json.Valid()`

**File:** `internal/parser/parser.go` — NEW
- [ ] `func ValidateEventData(raw json.RawMessage) error` — shared validation (size, depth, well-formed)
- [ ] `func StripHTMLFromStrings(raw json.RawMessage) json.RawMessage` — walk JSON, strip `<` `>` from string values
- [ ] `const MaxEventDataSize = 65536`
- [ ] `const MaxEventDataDepth = 10`

**Test:** `internal/db/events_test.go` — MODIFY + `internal/parser/parser_test.go` — NEW
- [ ] TestInsertEvent_OversizedPayload — returns error
- [ ] TestInsertEvent_DeepNesting — returns error
- [ ] TestValidateEventData_Valid/Oversize/TooDeep/Malformed
- [ ] TestStripHTMLFromStrings — removes tags from string values, preserves structure

**Lines:** ~60 parser.go + ~20 events.go changes + ~80 tests

---

## Task 8: LISTEN/NOTIFY Bridge

Cross-process event notification for SSE.

**File:** `internal/db/notify.go` — NEW
- [ ] `func NotifyNewEvent(ctx context.Context, pool *pgxpool.Pool, eventID int)` — `SELECT pg_notify('new_event', $1)`
- [ ] `func ListenNewEvents(ctx context.Context, pool *pgxpool.Pool) (<-chan int, error)` — acquire conn, `LISTEN new_event`, read notifications, parse event ID, send to channel. Close on ctx cancel.
- [ ] Channel is buffered (256). Drop on full.

**File:** `internal/cli/serve.go` — MODIFY
- [ ] On startup: `ListenNewEvents(ctx, pool)` → goroutine reads event IDs → fetches full event → publishes to broker
- [ ] This feeds the SSE handlers without requiring ingestion to run in-process

**Test:** `internal/db/notify_test.go` — NEW (integration, requires Postgres)
- [ ] TestNotifyAndListen — notify in one goroutine, listen in another, verify event ID received
- [ ] TestListenCancelledContext — verify clean shutdown

**Lines:** ~60 notify.go + ~20 serve.go changes + ~40 tests

---

## Task 9: SSE Connection Manager + Handlers

SSE streaming endpoints with connection limiting.

**File:** `internal/api/sse.go` — NEW
- [ ] `type SSEManager struct` — tracks connections per IP, per key, global count
- [ ] `func (m *SSEManager) Acquire(ip string, apiKey string) (releaseFunc, error)` — check limits, increment counters, return release function. Error if at cap.
- [ ] `func (m *SSEManager) Release(ip string, apiKey string)` — decrement counters
- [ ] Per-IP: 3 anon / 10 keyed. Global: 500. Configurable via `config.Config`.

**File:** `internal/api/stream.go` — NEW
- [ ] `func (s *Server) handleStream(w http.ResponseWriter, r *http.Request)` — all events
- [ ] `func (s *Server) handleStreamTicker(w http.ResponseWriter, r *http.Request)` — filter by ticker
- [ ] `func (s *Server) handleStreamScores(w http.ResponseWriter, r *http.Request)` — score changes only
- [ ] SSE format: `event: <type>\ndata: <json>\nid: <event_id>\n\n`
- [ ] Heartbeat: send `: keepalive\n\n` every 15 seconds
- [ ] Max duration: 30 minutes, then send `event: reconnect\n\n` and close
- [ ] On connect: `SSEManager.Acquire()`. On disconnect: release. Subscribe to broker.
- [ ] Set `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`
- [ ] Read `Last-Event-ID` header for future reconnection support (defer replay to Beta, just log it)

**File:** `internal/api/server.go` — MODIFY
- [ ] Mount SSE routes in separate group (no JSON content-type middleware, no request rate limiter)
- [ ] SSE group: `/api/v1/stream`, `/api/v1/stream/{ticker}`, `/api/v1/stream/scores`

**Test:** `internal/api/sse_test.go` — NEW + `internal/api/stream_test.go` — NEW
- [ ] TestSSEManager_AcquireRelease — basic flow
- [ ] TestSSEManager_PerIPLimit — 4th connection rejected
- [ ] TestSSEManager_GlobalLimit — at cap, all rejected
- [ ] TestStreamEndpoint_ReceivesEvent — `httptest.NewServer`, publish to broker, read SSE line
- [ ] TestStreamTicker_FiltersCorrectly — only matching ticker events received
- [ ] TestStreamScores_OnlyScoreEvents — filters to score change events
- [ ] TestStream_Heartbeat — verify keepalive within timeout
- [ ] TestStream_ClientDisconnect — no goroutine leak (goleak)

**Lines:** ~80 sse.go + ~120 stream.go + ~30 server.go changes + ~150 tests

---

## Task 10: Supervisor + `mrdn ingest` CLI

Worker lifecycle management and CLI command.

**File:** `internal/ingestion/supervisor.go` — NEW
- [ ] `type Supervisor struct { workers []workerHandle; ctx context.Context; cancel context.CancelFunc }`
- [ ] `func NewSupervisor(cfg config.Config, store *db.Store, broker *broker.Broker) *Supervisor`
- [ ] `func (s *Supervisor) Start()` — launch one PollWorker per enabled source, one StreamWorker for Finnhub
- [ ] `func (s *Supervisor) Stop()` — cancel context, wait for all workers (with timeout)
- [ ] Each worker runs in its own goroutine with panic recovery + restart
- [ ] Log worker start/stop/restart events

**File:** `internal/cli/ingest.go` — NEW
- [ ] Cobra command: `mrdn ingest`
- [ ] Loads config, connects to DB, creates broker, creates supervisor
- [ ] Signal handling: SIGINT/SIGTERM → supervisor.Stop() → clean exit
- [ ] Publishes to NOTIFY channel on each event insert (so `mrdn serve` SSE picks it up)

**Test:** `internal/ingestion/supervisor_test.go` — NEW
- [ ] TestSupervisor_StartsAllWorkers — verify all enabled sources polled
- [ ] TestSupervisor_StopGraceful — cancel, verify all goroutines exit
- [ ] TestSupervisor_WorkerRestart — one worker panics, verify it restarts
- [ ] goleak.VerifyNone(t)

**Lines:** ~100 supervisor.go + ~60 ingest.go + ~80 tests

---

## Tasks 11–17: Source Parsers (Wave 4, parallel)

All parsers follow the same pattern: implement `Source` interface, pure `Parse()` function tested with golden files.

### Task 11: OFAC SDN Parser (first — validates the interface)

**Files:** `internal/parser/ofac.go`, `internal/parser/ofac_test.go`, `internal/parser/testdata/ofac_sample.json`
- [ ] `type OFACSource struct { client *http.Client; baseURL string }`
- [ ] `func (o *OFACSource) Name() string` → `"ofac_sdn"`
- [ ] `func (o *OFACSource) Poll(ctx context.Context) ([]db.Event, error)` — fetch SDN list, parse, normalize to events
- [ ] `func ParseOFAC(data []byte) ([]db.Event, error)` — pure function, testable
- [ ] Generate deterministic `source_id`: `sha256(source + entity_name + program + added_date)`
- [ ] Base URL hardcoded: `https://api.ofac-api.com/v4/` (or treasury.gov direct)
- [ ] `io.LimitReader(10MB)` on response body
- [ ] Tests: valid SDN entry, entity with aliases, empty response, malformed JSON

### Task 12: Polygon.io Parser

**Files:** `internal/parser/polygon.go`, `internal/parser/polygon_test.go`, `internal/parser/testdata/polygon_daily_sample.json`
- [ ] Daily OHLCV aggregates endpoint
- [ ] API key from config (query param `apiKey`)
- [ ] Base URL hardcoded: `https://api.polygon.io/`
- [ ] `func ParsePolygonDaily(data []byte) ([]db.Event, error)`
- [ ] **Redact API key from error messages** (Security S07)
- [ ] Tests: valid daily bar, empty results, rate limit 429 response

### Task 13: SEC EDGAR Form 4 Parser

**Files:** `internal/parser/edgar.go`, `internal/parser/edgar_test.go`, `internal/parser/testdata/edgar_form4_sample.xml`
- [ ] EDGAR XBRL/XML filings
- [ ] No API key (public, 10 req/s rate limit — respect via poll interval)
- [ ] `encoding/xml` only (Security S04). `io.LimitReader(10MB)`.
- [ ] Base URL hardcoded: `https://efts.sec.gov/LATEST/`
- [ ] `func ParseEdgarForm4(data []byte) ([]db.Event, error)`
- [ ] Tests: single transaction, multiple transactions, amendment filing

### Task 14: Senate EFDS Parser

**Files:** `internal/parser/efds.go`, `internal/parser/efds_test.go`, `internal/parser/testdata/efds_sample.xml`
- [ ] Scrape Senate EFDS XML e-filings
- [ ] `encoding/xml` only. `io.LimitReader(10MB)`.
- [ ] Base URL hardcoded: `https://efds.senate.gov/`
- [ ] `func ParseEFDS(data []byte) ([]db.Event, error)`
- [ ] Tests: valid filing, partial filing, malformed XML

### Task 15: USAspending Parser

**Files:** `internal/parser/usaspending.go`, `internal/parser/usaspending_test.go`, `internal/parser/testdata/usaspending_sample.json`
- [ ] USAspending.gov REST API (no key required)
- [ ] Base URL hardcoded: `https://api.usaspending.gov/api/v2/`
- [ ] `func ParseUSAspending(data []byte) ([]db.Event, error)`
- [ ] Tests: valid award, empty results, schema variation

### Task 16: Federal Register Parser

**Files:** `internal/parser/fedregister.go`, `internal/parser/fedregister_test.go`, `internal/parser/testdata/fedregister_sample.json`
- [ ] Federal Register API (no key required)
- [ ] Base URL hardcoded: `https://www.federalregister.gov/api/v1/`
- [ ] `func ParseFedRegister(data []byte) ([]db.Event, error)`
- [ ] Tests: executive order, tariff rule, empty search results

### Task 17: FEC Parser

**Files:** `internal/parser/fec.go`, `internal/parser/fec_test.go`, `internal/parser/testdata/fec_sample.json`, `internal/parser/testdata/fec_sample.csv`
- [ ] FEC API (key required from config)
- [ ] Base URL hardcoded: `https://api.open.fec.gov/v1/`
- [ ] **CSV sanitization:** strip cells starting with `=`, `+`, `-`, `@` (Security S11)
- [ ] **Redact API key from error messages**
- [ ] `func ParseFEC(data []byte) ([]db.Event, error)`
- [ ] Tests: individual donation, PAC contribution, bulk CSV

**Lines per parser:** ~80 source + ~60 tests = ~140 each. Total: ~980 for 7 parsers.

---

## Task 18: Finnhub StreamWorker + Parser

Persistent WebSocket connection for real-time market data.

**File:** `internal/ingestion/stream_worker.go` — NEW
- [ ] `type StreamWorker struct { source StreamSource; store *db.Store; broker *broker.Broker; backoff *Backoff }`
- [ ] `func (w *StreamWorker) Run(ctx context.Context)` — connect, loop recv, on error: backoff + reconnect
- [ ] Panic recovery per iteration

**File:** `internal/parser/finnhub.go` — NEW
- [ ] `type FinnhubSource struct { apiKey string; conn *websocket.Conn; symbols []string }`
- [ ] `func (f *FinnhubSource) Connect(ctx context.Context) error` — `wss://ws.finnhub.io?token=...`
- [ ] **`wss://` only — reject `ws://`** (Security S05)
- [ ] **Never log full URL** (contains API key)
- [ ] `func (f *FinnhubSource) Recv(ctx context.Context) ([]db.Event, error)` — read frame, validate size (<1MB), parse
- [ ] `func (f *FinnhubSource) Close() error`
- [ ] `func (f *FinnhubSource) Rebalance(symbols []string) error` — unsubscribe old, subscribe new via WS messages
- [ ] `func ParseFinnhubTrade(data []byte) ([]db.Event, error)` — pure parser function

**Test:** `internal/parser/finnhub_test.go` — NEW
- [ ] TestParseFinnhubTrade — valid trade message
- [ ] TestParseFinnhubTrade_PingPong — non-trade messages ignored
- [ ] TestParseFinnhubTrade_Malformed — error returned
- [ ] StreamWorker lifecycle tests with mock WebSocket server (httptest + websocket upgrade)

**Dependency:** `nhooyr.io/websocket`

**Lines:** ~80 stream_worker + ~120 finnhub + ~100 tests

---

## Task 19: Rebalancer (Symbol Rotation)

Periodic ranking of companies to select top 25+5 for Finnhub.

**File:** `internal/ingestion/rebalancer.go` — NEW
- [ ] `type Rebalancer struct { store *db.Store; finnhub *FinnhubSource; clock Clock; interval time.Duration }`
- [ ] `func (r *Rebalancer) Run(ctx context.Context)` — every N minutes, rank companies, call `finnhub.Rebalance(top30)`
- [ ] Ranking signal: composite_score DESC (from `GetScoreRankings`), or recent event count, or configurable
- [ ] Top 25 subscribed, 5 buffer slots for companies trending up (score increased in last hour)

**Test:** `internal/ingestion/rebalancer_test.go` — NEW
- [ ] TestRebalancer_SelectsTop30 — seeds 50 companies with scores, verifies top 30 selected
- [ ] TestRebalancer_BufferSlots — company trending up gets buffer slot
- [ ] TestRebalancer_CallsRebalance — verify Finnhub source receives new symbol list

**Lines:** ~70 rebalancer + ~60 tests

---

## Task 20: Score Recomputation Worker

Broker subscriber that triggers score recalculation when new events arrive.

**File:** `internal/ingestion/score_worker.go` — NEW
- [ ] `type ScoreWorker struct { store *db.Store; broker *broker.Broker; clock Clock }`
- [ ] Subscribes to broker. On event with non-nil CompanyID: add to debounce map.
- [ ] Every 5 seconds: flush debounce map, recompute scores for each company.
- [ ] Recomputation: placeholder that calls `store.InsertScore()` with updated composite.
  (Actual score formula is Phase 3 — this task just wires the trigger pipeline.)
- [ ] Publish score-change events to broker (for SSE `/stream/scores`).

**Test:** `internal/ingestion/score_worker_test.go` — NEW
- [ ] TestScoreWorker_TriggersOnEvent — publish event, verify InsertScore called
- [ ] TestScoreWorker_Debounces — 3 events for same company in 2s, only 1 recomputation
- [ ] TestScoreWorker_IgnoresUnresolved — event with nil CompanyID skipped
- [ ] TestScoreWorker_PublishesScoreChange — verify broker receives score event

**Lines:** ~80 score_worker + ~80 tests

---

## Error Codes (new for Phase 2B)

| Code | HTTP | When |
|------|------|------|
| `SSE_LIMIT_REACHED` | 429 | Per-IP or global SSE connection cap hit |
| `EVENT_DATA_TOO_LARGE` | 400 | event_data exceeds 64KB |
| `EVENT_DATA_INVALID` | 400 | Malformed JSON or depth > 10 |
| `SOURCE_UNAVAILABLE` | 503 | Source marked "down", poll failed |

---

## New Dependencies

| Package | Purpose | Why |
|---------|---------|-----|
| `nhooyr.io/websocket` | Finnhub WebSocket client | Modern, context-aware, maintained. Only WS dependency. |
| `go.uber.org/goleak` | Goroutine leak detection in tests | Test-only. Catches leaked goroutines from workers/SSE. |

---

## Estimated Totals

| Metric | Count |
|--------|-------|
| New files | ~35 |
| Modified files | ~10 |
| New lines of code | ~1,800 (source) |
| New lines of test | ~1,200 |
| New tests | ~80-100 |
| Waves | 5 (max parallelism: 7 agents in Wave 4) |
