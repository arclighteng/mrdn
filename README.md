# MRDN

**Trades that move with policy, people that move with both.**

MRDN aggregates public data — congressional stock trades, judicial financial
disclosures, executive branch ethics filings, lobbying records, and corporate
signals — and surfaces the connections between money, policy, and the people
moving them. Live at [mrdn.arclighteng.com](https://mrdn.arclighteng.com).

---

## What it does

- **Ingests** data from all three branches of government: congressional trading
  disclosures (FMP Congress, Lambda Finance, Senate EFDS, House HSW), judicial
  financial disclosures (CourtListener), and executive branch ethics filings
  (OGE 278e via the Office of Government Ethics API).
- **Resolves** free-text entities (company names, tickers, people) into typed records
  with stable IDs.
- **Scores** every company on a composite signal built from market activity (price
  trends, volume anomalies), policy exposure (sanctions, contracts, donations),
  and insider activity (SEC Form 4 filings, congressional trades).
- **Detects patterns** with 14 insight detectors: coordinated trades, committee
  overlap, pre-event trades, round-trips, swarm clusters, lone wolves,
  copy-trader returns, court-trade correlations, sector rotation, late-but-profitable
  disclosures, quiet accumulation, committee insiders, bipartisan convergence,
  and insider echo.
- **Exposes** the data through a static JSON dashboard with heatmaps, rankings,
  top movers, swarms, partisan signals, first-mover detection, and an expandable
  insights grid.

## Stack

- **Backend** — Go 1.25, SQLite for ingestion/scoring (local DB built fresh each deploy).
- **Database** — Neon Postgres (serverless, free tier) for persistent storage.
  Cloudflare D1 for the MQL query worker.
- **Frontend** — Single-file dashboard: Alpine.js + Tailwind (CDN) + ECharts.
  Fetches pre-computed JSON from Cloudflare Pages.
- **Hosting** — Cloudflare Pages (static JSON + HTML), Cloudflare Workers (MQL API),
  Cloudflare KV (signal/metadata cache). All free tier.
- **Ingestion** — GitHub Actions `workflow_dispatch`. One-shot: poll → score →
  export → deploy. Triggered manually or via `ingest_and_deploy.sh`.

## Data sources

| Branch | Source | Type | Status |
|--------|--------|------|--------|
| Legislative | FMP Congress API | Congressional trades | Live |
| Legislative | Lambda Finance API | Congressional trades | Live (100 req/month) |
| Legislative | Senate EFDS | Senate disclosures | Live |
| Legislative | House HSW | House disclosures | Historical |
| Judicial | CourtListener API | Judicial financial disclosures | Live |
| Executive | OGE REST API | Executive branch 278e filings | Live (~16,715 records) |
| Market | EDGAR Form 4 | SEC insider trades | Live |
| Market | Finnhub | Real-time market data | Live |
| Market | Polygon | Market data / enrichment | Live |

## Repository layout

```
cmd/mrdn            Entry point — dispatches to the cobra CLI
internal/cli        Cobra commands (serve, ingest, backfill, score-backfill, …)
internal/api        HTTP handlers, routing, rate limiter
internal/db         SQLite queries + migrations (internal/db/migrations/)
internal/broker     Event broker
internal/ingestion  Source supervisor, polling orchestration
internal/parser     Data source parsers (fmp, lambda, courtlistener, oge, edgar, …)
internal/resolver   Entity resolution (free text → typed records)
internal/score      Composite scoring engine + sub-scorers (market, policy, insider)
internal/insights   14 pattern detectors (coordinated, committee, pre-event, …)
internal/export     Static JSON export for Cloudflare Pages
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
| `ingest-once` | One-shot poll all sources, resolve, exit |
| `ingest-efds` | One-shot Senate EFDS poll |
| `ingest-house-trades` | Ingest House trades from HSW JSON |
| `backfill [source]` | Resolve unlinked events to typed records |
| `backfill-sectors` | Populate company sector metadata |
| `score-backfill` | Compute composite scores for all companies |
| `export --out dist/data` | Export all dashboard data as static JSON |
| `prune --keep-days 90` | Delete old data to keep Neon under budget |
| `sources` | Report data-source freshness |

## API

Base: `https://mrdn.arclighteng.com/data/` (static JSON files)

The frontend reads pre-computed JSON files. API paths are mapped to static files:

```
/scores/movers       → scores-movers.json
/scores/rankings     → scores-rankings.json
/events/latest       → events-latest.json
/sources             → sources.json
/insights            → insights.json
/signals/swarms      → signals/swarms.json
/signals/partisan    → signals/partisan-consensus.json
/signals/first-movers → signals/first-movers.json
/signals/round-trips → signals/round-trips.json
/compliance/latency  → signals/latency.json
/companies/{TICKER}  → companies/{TICKER}.json
/persons/{slug}      → persons/{slug}.json
/lobbying            → lobbying.json
```

All responses are JSON. The MQL Worker on Cloudflare handles dynamic queries.

## Deployment

Deployment is triggered manually via GitHub Actions `workflow_dispatch` or the
local `ingest_and_deploy.sh` script. The pipeline:

1. Build the Go binary
2. Run migrations (local SQLite)
3. Poll all data sources (`ingest-once`)
4. Compute scores (`score-backfill`)
5. Export static JSON (`export --out dist/data`)
6. Deploy to Cloudflare Pages
7. Migrate D1 schema + upload data to D1
8. Deploy MQL Worker
9. Upload signals/metadata to KV
10. Prune old data

See [`docs/OPERATIONS.md`](docs/OPERATIONS.md) for details.

### Required GitHub secrets

| Secret | Purpose |
|--------|---------|
| `DATABASE_URL` | Neon Postgres connection string |
| `CF_API_TOKEN` | Cloudflare API token (Pages + D1 + Workers + KV) |
| `MRDN_FMP_API_KEY` | FMP Congress API |
| `MRDN_LAMBDA_API_KEY` | Lambda Finance API |
| `MRDN_POLYGON_API_KEY` | Polygon market data |
| `MRDN_COURTLISTENER_TOKEN` | CourtListener API |
| `MRDN_FINNHUB_API_KEY` | Finnhub market data |

## Environment variables

| Variable | Required | Purpose |
|----------|----------|---------|
| `DATABASE_URL` | yes | Postgres connection string |
| `MRDN_FMP_API_KEY` | yes | FMP Congress API key |
| `MRDN_LAMBDA_API_KEY` | yes | Lambda Finance API key |
| `MRDN_POLYGON_API_KEY` | yes | Polygon API key |
| `MRDN_COURTLISTENER_TOKEN` | yes | CourtListener auth token |
| `MRDN_FINNHUB_API_KEY` | optional | Finnhub WebSocket key |
| `PORT` | optional | HTTP listen port (default `8080`) |

## Contributing

- Code style: standard Go (`gofmt`, `go vet`, `go test ./...`).
- Migrations live in `internal/db/migrations/` and are numbered sequentially.
- Frontend changes go in `web/static/index.html` — no build step, but the file is
  embedded, so a server restart is required to see changes.
- Tests: `go test ./...`. Resolver and API handlers have unit tests; DB integration
  tests use a `testTx` helper that rolls back after each test.

## License

Private / internal — not yet open-sourced.
