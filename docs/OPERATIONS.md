# MRDN — Operations

How to deploy MRDN, connect to production, and run ingestion jobs.

---

## Architecture

Production runs **two Railway services** from the same Docker image:

| Service | Start command | Purpose |
|---------|---------------|---------|
| `mrdn-api` | `mrdn serve` (default CMD) | HTTP API server — public, serves the dashboard and `/v1/*` endpoints. |
| `mrdn-ingest` | `mrdn ingest` (custom start command) | Long-running ingestion supervisor — polls all sources, runs the score worker, Finnhub stream, and rebalancer. Private (no public networking, no health check). |

Both services share the same `DATABASE_URL` and API-key env vars. They are deployed in
lockstep: every push to `master` updates both to the same SHA-tagged image.

## Deployment (hands-off CI/CD)

Deploys are fully automated. Pushing to `master` or `main` ships to production.

### Pipeline

1. **Push to `master`** — triggers `.github/workflows/deploy.yml`.
2. **Build & push image** — GitHub Actions builds the Docker image and pushes it to
   Docker Hub with two tags: `adamreed/mrdn:latest` and `adamreed/mrdn:<git-sha>`.
3. **Update Railway image source** — a GraphQL `serviceInstanceUpdate` mutation points
   **both** `mrdn-api` and `mrdn-ingest` at `adamreed/mrdn:<git-sha>`. Using a unique tag
   per commit bypasses Railway's digest cache (which is why `railway redeploy` on
   `:latest` does not force a fresh pull).
4. **Trigger deploy** — a second GraphQL mutation (`serviceInstanceDeployV2`) kicks off
   the new deployment on each service. Railway pulls the SHA-tagged image and rolls it out.

Typical end-to-end time: ~2–3 minutes from push to live.

### Required GitHub secrets

| Secret | Where to get it |
|--------|-----------------|
| `DOCKERHUB_USERNAME` | Docker Hub account username |
| `DOCKERHUB_TOKEN` | Docker Hub → Account Settings → Personal access tokens |
| `RAILWAY_TOKEN` | Railway → `mrdn` project → Settings → Tokens (**project-scoped**, not account) |
| `RAILWAY_SERVICE_ID` | UUID of the `mrdn-api` service: `/service/<SERVICE_ID>` |
| `RAILWAY_INGEST_SERVICE_ID` | UUID of the `mrdn-ingest` service: `/service/<SERVICE_ID>` |
| `RAILWAY_ENVIRONMENT_ID` | UUID in the Railway dashboard URL: `?environmentId=<ENV_ID>` |

**Important:** `RAILWAY_TOKEN` must be a **project token**, not an account token.
Project tokens authenticate the GraphQL API via the `Project-Access-Token` header; account
tokens do not work with the CLI or this API in CI. Regenerate from
`Railway → mrdn project → Settings → Tokens` if you lose it.

### Verifying a deploy

1. **GitHub Actions** — check the most recent `Build & Deploy` workflow run is green.
2. **Railway dashboard** — `mrdn-api` should show a new deployment with an "ACTIVE" status
   and a timestamp matching the push.
3. **Hard-refresh the dashboard** at `https://mrdn.arclighteng.com` (Ctrl+Shift+R) to bypass
   browser cache.

### Rollback

Point the Railway service at a previous SHA tag. From the Railway dashboard:
`mrdn-api` → Settings → Source → Image → change tag to the desired
`adamreed/mrdn:<previous-sha>`. Railway will pull and deploy that image.

Alternatively, re-run the GitHub Action for the previous commit.

---

## Connecting to production

### Railway SSH

Open a shell inside the running `mrdn-api` container:

```
railway ssh
```

(Run from the project root with the Railway CLI linked to the `mrdn` project.)

Or from the Railway dashboard: `mrdn-api` service → top-right **⋯** menu → **SSH**.

Inside the SSH session you have access to the `mrdn` binary and all env vars
(including `DATABASE_URL`). Use this for running ingestion, score backfill,
migrations, or ad-hoc DB queries via `psql "$DATABASE_URL"`.

### Railway logs

```
railway logs --service mrdn-api
```

Or from the dashboard: service card → **Deployments** → click a deployment → **View Logs**.

---

## CLI commands (`mrdn <cmd>`)

All commands are run inside `railway ssh` against the production database.

### Database

| Command | Purpose |
|---------|---------|
| `mrdn migrate` | Run pending DB migrations. Runs automatically on container start, but can be invoked manually. |
| `mrdn seed` | Seed the database with initial company data (tickers, names, sectors). One-time bootstrap. |
| `mrdn sources` | Print data-source health and freshness (last-seen timestamps per source). Diagnostic only. |

### Ingestion

| Command | Purpose |
|---------|---------|
| `mrdn ingest` | Start long-running ingestion workers (used as the primary `serve`-adjacent process). Pulls from all configured sources on an interval. |
| `mrdn ingest-efds` | One-shot poll of the Senate EFDS (Electronic Financial Disclosures) source. No API keys required. Use for ad-hoc Senate trade top-ups. |
| `mrdn ingest-house-trades` | Ingest US House congressional stock trades from a HSW-format JSON source file. Expects `/data/hsw.json` (baked into the image) or a `--file` flag. Use when HSW publishes a new dump. |

### Backfill / enrichment

| Command | Purpose |
|---------|---------|
| `mrdn backfill [source]` | Resolve unlinked events to companies and populate typed record tables (`congressional_trades`, etc.). Run after a large ingest or when new resolver rules land. Accepts an optional `source` name to scope the backfill. |
| `mrdn backfill-sectors` | Populate `companies.sector` / `companies.subsector` from an HSW-format JSON file. Run when the sector mapping source is updated. |
| `mrdn score-backfill` | Compute composite scores for every company that has events. **Must be run at least twice** for "Top Movers" to populate — the UI requires both a current and a previous score snapshot to compute a delta. |

### Serving (used by Railway, not operators)

| Command | Purpose |
|---------|---------|
| `mrdn serve` | Start the HTTP API server. This is the container's default entrypoint on Railway — operators should not need to invoke it manually. |

---

## Common ops recipes

### "I pushed a change but I don't see it on production"

1. Check the GitHub Actions run is green.
2. Check Railway shows a new deployment (not the previous one).
3. Hard-refresh the browser (Ctrl+Shift+R). The static dashboard is embedded and
   aggressively cached.

### "Top Movers is empty"

Run `mrdn score-backfill` a second time inside `railway ssh`. Movers requires two
score snapshots to compute a delta.

### "New congressional trades aren't showing up"

```
railway ssh
mrdn ingest-efds              # Senate
mrdn ingest-house-trades      # House (if HSW has a new dump)
mrdn backfill                 # link events to companies
mrdn score-backfill           # recompute scores
```

### "A data source looks stale"

```
railway ssh
mrdn sources                  # check last-seen timestamps
```
