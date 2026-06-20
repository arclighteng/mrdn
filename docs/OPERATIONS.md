# MRDN — Operations

How to deploy MRDN, monitor data sources, and run ingestion jobs.

---

## Architecture

Production runs as a **static site on Cloudflare Pages** with supporting services:

| Component | Platform | Purpose |
|-----------|----------|---------|
| Dashboard + JSON | Cloudflare Pages (free) | Static HTML + pre-computed JSON files |
| MQL Worker | Cloudflare Workers (free) | Dynamic query language API |
| D1 Database | Cloudflare D1 (free) | Queryable data for the MQL Worker |
| KV Store | Cloudflare KV (free) | Signal and metadata cache |
| Postgres | Neon (free tier) | Persistent ingestion database |

There is no long-running server. The ingestion pipeline runs as a one-shot GitHub
Actions workflow that polls sources, computes scores, exports static JSON, and
deploys everything to Cloudflare.

## Data sources

| Source | Branch | API Key Required | Approx Records |
|--------|--------|-----------------|----------------|
| FMP Congress | Legislative | Yes (`MRDN_FMP_API_KEY`) | ~200/poll |
| Lambda Finance | Legislative | Yes (`MRDN_LAMBDA_API_KEY`, 100 req/month) | ~100/poll |
| Senate EFDS | Legislative | No | Variable |
| House HSW | Legislative | No (historical file) | Stale since 2023 |
| CourtListener | Judicial | Yes (`MRDN_COURTLISTENER_TOKEN`) | ~13,869 events |
| OGE (Office of Government Ethics) | Executive | No (public API) | ~16,715 records |
| EDGAR Form 4 | Market/Insider | No | Variable |
| Finnhub | Market | Yes (`MRDN_FINNHUB_API_KEY`) | Streaming |
| Polygon | Market | Yes (`MRDN_POLYGON_API_KEY`) | On-demand |

## Deployment

Deploys are triggered manually. There is no automatic cron (disabled per #23).

### Trigger options

1. **GitHub Actions** — go to Actions → "Ingest & Deploy to Cloudflare" → Run workflow
2. **Local script** — `./ingest_and_deploy.sh` (requires env vars set)
3. **CLI** — `gh workflow run "Ingest & Deploy to Cloudflare" --ref master`

### Pipeline steps

The `.github/workflows/ingest-deploy.yml` workflow runs:

1. Checkout + build Go binary
2. Run SQLite migrations
3. `ingest-once` — one-shot poll all data sources
4. Ingest House trades (historical)
5. Enrich company names from Polygon
6. Generate company aliases (for EDGAR resolver)
7. Backfill sectors from HSW data
8. `score-backfill` — compute composite risk scores
9. `export --out dist/data` — write all static JSON files
10. Copy frontend assets to `dist/`
11. Deploy `dist/` to Cloudflare Pages
12. Migrate D1 schema + upload data
13. Deploy MQL Worker
14. Upload signals/metadata to KV
15. `prune --keep-days 90` — keep Neon under budget

Typical end-to-end time: ~15 minutes.

### Required GitHub secrets

| Secret | Purpose |
|--------|---------|
| `DATABASE_URL` | Neon Postgres connection string |
| `CF_API_TOKEN` | Cloudflare API token (Pages + D1 + Workers + KV access) |
| `MRDN_FMP_API_KEY` | FMP Congress API |
| `MRDN_LAMBDA_API_KEY` | Lambda Finance API |
| `MRDN_POLYGON_API_KEY` | Polygon market data |
| `MRDN_COURTLISTENER_TOKEN` | CourtListener API |
| `MRDN_FINNHUB_API_KEY` | Finnhub market data (optional) |

### Verifying a deploy

1. **GitHub Actions** — check the workflow run is green
2. **Hard-refresh** `https://mrdn.arclighteng.com` (Ctrl+Shift+R)
3. Check the `sources.json` file for updated timestamps
4. Check the "What's new" banner appears (first visit after deploy)

### Rollback

Re-run the GitHub Actions workflow for a previous commit, or deploy a previous
`dist/` directory to Cloudflare Pages via `wrangler pages deploy`.

---

## CLI commands (`mrdn <cmd>`)

### Database

| Command | Purpose |
|---------|---------|
| `mrdn migrate` | Run pending DB migrations |
| `mrdn seed` | Seed initial company data (tickers, names, sectors) |
| `mrdn sources` | Print data-source health and freshness |

### Ingestion

| Command | Purpose |
|---------|---------|
| `mrdn ingest` | Start long-running ingestion workers (development use) |
| `mrdn ingest-once` | One-shot poll all sources, resolve, exit (CI use) |
| `mrdn ingest-efds` | One-shot Senate EFDS poll |
| `mrdn ingest-house-trades` | Ingest House trades from HSW JSON file |

### Backfill / enrichment

| Command | Purpose |
|---------|---------|
| `mrdn backfill [source]` | Resolve unlinked events to typed records |
| `mrdn backfill-sectors` | Populate company sector metadata |
| `mrdn score-backfill` | Compute composite scores for all companies |

### Export & deploy

| Command | Purpose |
|---------|---------|
| `mrdn export --out dist/data` | Export all dashboard data as static JSON |
| `mrdn prune --keep-days 90` | Delete old data to keep Neon under 0.5 GB |

---

## Scoring

Three sub-scorers produce 0–100 values (50 = neutral), weighted into a composite:

| Sub-scorer | Weight | Inputs |
|------------|--------|--------|
| Market | 0.35 | Price trend, volume anomaly, SEC Form 4 count |
| Policy | 0.40 | Sanctions count, contract value, donation count |
| Insider | 0.25 | Congressional trades (0.50), SEC Form 4 (0.30), donations (0.20) |

The insider scorer uses **graceful degradation**: if a data source has no data,
its weight is redistributed to sources that do.

## Insights

14 pattern detectors produce findings ranked by blended score (40% rarity + 60% recency):

| Detector | Type | Signal |
|----------|------|--------|
| Coordinated | `coordinated_trades` | 3+ reps trade same ticker same week |
| Committee | `committee_relevant` | Trade matches member's committee sector |
| Pre-event | `pre_event` | Trade 1–14 days before company event |
| Round-trip | `round_trip` | Buy then sell within 30 days |
| Swarm | `swarm_outlier` | Cluster of 4+ reps on a ticker |
| Lone wolf | `lone_wolf` | Trade 4x+ a member's typical size |
| Copy-trader | `copy_trader` | Member's buys outperform at 30 days |
| Court-trade | `court_trade` | Trade 1–14 days before court filing |
| Sector rotation | `sector_rotation` | Congress piling into a sector vs average |
| Late profitable | `late_profitable` | Late-reported buy where stock went up |
| Accumulation | `quiet_accumulation` | 3+ small buys of same ticker in 60 days |
| Committee insider | `committee_insider` | Committee member trades before sector event |
| Bipartisan | `bipartisan_convergence` | Both parties trade same ticker same week |
| Insider echo | `insider_echo` | SEC insider + politician trade same ticker within 14 days |

Top 20 findings are exported to `insights.json`. The frontend shows a hero card +
2 secondaries with an expandable grid for the rest.

---

## Common ops recipes

### "I pushed a change but I don't see it on production"

1. Trigger the deploy workflow (it's `workflow_dispatch`, not automatic on push)
2. Check the GitHub Actions run is green
3. Hard-refresh the browser (Ctrl+Shift+R)

### "Top Movers is empty"

Run the deploy pipeline twice. Movers requires two score snapshots to compute a delta.

### "A data source looks stale"

Check `sources.json` on the live site or run `mrdn sources` locally with `DATABASE_URL` set.

### "Lambda Finance returns errors"

Lambda has a 100 req/month limit. If exceeded, it returns 429s until the next month.
The source logs warnings but continues with other sources.
