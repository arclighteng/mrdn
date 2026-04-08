# MRDN

**Trades that move with policy, people that move with both.**

MRDN aggregates public data — congressional stock trades, legislative activity,
lobbying disclosures, and corporate filings — and surfaces the connections between
money, policy, and the people moving them. Live at
[mrdn.arclighteng.com](https://mrdn.arclighteng.com).

---

## What it does

- **Ingests** congressional trading disclosures (Senate EFDS, US House HSW),
  legislative events, and corporate signals from public sources.
- **Resolves** free-text entities (company names, tickers, people) into typed records
  with stable IDs.
- **Scores** every company on a composite signal built from trading activity, timing,
  and cross-party/cross-chamber patterns.
- **Exposes** the data through a REST API and a single-page dashboard with heatmaps,
  rankings, top movers, swarms, partisan signals, and first-mover detection.
- **Streams** live events over SSE for real-time clients.

## Stack

- **Backend** — Go 1.25, [chi](https://github.com/go-chi/chi) router,
  [pgx](https://github.com/jackc/pgx) for Postgres access.
- **Database** — Neon Postgres (serverless).
- **Frontend** — Single-file dashboard: Alpine.js + Tailwind (CDN) + ECharts.
  Served from Go via an embedded `fs.FS` (no separate build step).
- **Deployment** — Docker image pushed to Docker Hub, deployed to Railway behind
  a custom domain. Hands-off CI/CD via GitHub Actions.

## Repository layout

```
cmd/mrdn            Entry point — dispatches to the cobra CLI
internal/cli        Cobra commands (serve, ingest, backfill, score-backfill, …)
internal/api        HTTP handlers, routing, rate limiter, SSE manager
internal/db         Postgres queries + migrations (internal/db/migrations/)
internal/broker     Event broker for SSE fan-out
internal/resolver   Entity resolution (free text → typed records)
internal/score      Composite scoring engine + sub-scorers
web/static          Embedded dashboard (index.html, assets)
docs                Operations docs and plans
```

## Running locally

Requires Go 1.25 and a Postgres database (Neon works; any modern Postgres does).

```bash
# 1. set the DB URL
export DATABASE_URL="postgres://user:pass@host:5432/mrdn?sslmode=require"

# 2. apply migrations
go run ./cmd/mrdn migrate

# 3. start the API + dashboard
go run ./cmd/mrdn serve
```

The dashboard is then at `http://localhost:8080`.

For live reload during frontend work, edit `web/static/index.html` — it's embedded
at build time, so a rebuild is needed (`go run ./cmd/mrdn serve`) but no bundler.

## CLI reference

All commands are invoked as `mrdn <subcommand>` (or `go run ./cmd/mrdn <subcommand>`
in development). Full descriptions in [`docs/OPERATIONS.md`](docs/OPERATIONS.md).

| Command | Purpose |
|---------|---------|
| `serve` | Start the HTTP API + dashboard |
| `migrate` | Apply pending DB migrations |
| `seed` | Seed initial company data |
| `ingest` | Long-running ingestion workers |
| `ingest-efds` | One-shot Senate EFDS poll |
| `ingest-house-trades` | Ingest House trades from HSW JSON |
| `backfill [source]` | Resolve unlinked events to typed records |
| `backfill-sectors` | Populate company sector metadata |
| `score-backfill` | Compute composite scores for all companies |
| `sources` | Report data-source freshness |

## API

Base: `https://mrdn.arclighteng.com/api/v1`

Selected endpoints:

```
GET /companies                        List companies
GET /companies/{ticker}               Company detail
GET /companies/{ticker}/scores        Score history
GET /companies/{ticker}/timeline      Event timeline
GET /companies/{ticker}/events        Events for a company

GET /scores/rankings                  Top-ranked companies by composite score
GET /scores/movers?hours=24           Companies with largest score deltas
GET /scores/heatmap                   Score heatmap data

GET /persons                          Reps / senators
GET /persons/{slug}                   Person detail
GET /persons/{slug}/profile           Full profile with trades
GET /persons/{slug}/co-traders        Reps who trade alongside this one

GET /stats/party-sector-heatmap       Party × sector trade counts
GET /stats/rep-month-heatmap          Rep × month activity heatmap
GET /stats/activity/heatmap           Day-of-week × hour activity

GET /signals/swarms                   Coordinated trading clusters
GET /signals/partisan                 Partisan-leaning tickers
GET /signals/first-movers             First-mover detection
GET /signals/round-trips              Round-trip trade detection

GET /compliance/latency               STOCK Act filing latency distribution

GET /stream                           Live SSE feed (all events)
GET /stream/scores                    Live score updates
GET /stream/{ticker}                  Per-ticker live feed
```

All REST responses are JSON with shape `{ "data": ..., "meta": ... }`.
Streaming endpoints are `text/event-stream` (SSE).

## Deployment

Push to `master` → GitHub Actions builds and pushes the Docker image to Docker Hub
(tagged with both `:latest` and `:<git-sha>`), then updates the Railway service to
the SHA tag and triggers a deploy. Typical end-to-end time: ~2–3 minutes.

See [`docs/OPERATIONS.md`](docs/OPERATIONS.md) for:

- Required GitHub secrets
- Why SHA-tagged images are required (Railway digest cache)
- Rollback procedure
- Common ops recipes

## Environment variables

| Variable | Required | Purpose |
|----------|----------|---------|
| `DATABASE_URL` | ✅ | Postgres connection string |
| `PORT` | optional | HTTP listen port (default `8080`) |
| `DEPLOY_SHA` | optional | Set by CI, surfaced for debugging |

## Contributing

- Code style: standard Go (`gofmt`, `go vet`, `go test ./...`).
- Migrations live in `internal/db/migrations/` and are numbered sequentially.
- Frontend changes go in `web/static/index.html` — no build step, but the file is
  embedded, so a server restart is required to see changes.
- Tests: `go test ./...`. Resolver and API handlers have unit tests; DB integration
  tests use a `testTx` helper that rolls back after each test.

## License

Private / internal — not yet open-sourced.
