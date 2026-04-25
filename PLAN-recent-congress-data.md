# Plan: Ingest Recent Congressional Trade Data (last 12 months)

**Goal:** Get April 2025 – April 2026 congressional trades into MRDN so the Persons page has live, meaningful data.

**Current state (2026-04-24):**
- 17,170 House trades loaded (2020–2023, archive stale since mid-2023)
- 7,104 Senate trades loaded (2012–2020, archive stale since Dec 2020)
- Senate EFDS returning 503
- **No data from the last 12 months.** GitHub archives are all stale.
- All parsers and CLI commands for Phases 1–3 are built and compiling.

---

## CRITICAL: Only live APIs have recent data

The GitHub archives (HSW, Senate Stock Watcher) stopped updating years ago. The only sources with April 2025–2026 trades are:

1. **FMP API** — code exists, just needs API key
2. **Lambda Finance API** — code exists, just needs API key
3. **Senate EFDS** — code exists, but site is returning 503

**You must sign up for at least one API key to get recent data.**

---

## Phase 1 — FMP API key (HIGHEST PRIORITY — code is ready)

FMP provides the most recent Senate + House trades. Parser and CLI already exist.

1. **Sign up** at https://financialmodelingprep.com (free tier, 250 calls/day)
2. **Add key to `.env`**
   ```bash
   export MRDN_FMP_API_KEY="your-key-here"
   ```
3. **Run**
   ```bash
   source .env && go run ./cmd/mrdn ingest-fmp-congress
   ```
4. **Add to GitHub secrets** for CI: `MRDN_FMP_API_KEY`

**Expected yield:** ~200–500 trades per poll covering the last few months. Run periodically to stay current.

---

## Phase 2 — Lambda Finance API (best ongoing coverage)

Lambda Finance aggregates both chambers with filtering by party/state/ticker/date. Hours from official filing.

1. **Sign up** at https://www.lambdafin.com (free tier: 100 req/month)
2. **Add key to `.env`**
   ```bash
   export MRDN_LAMBDA_API_KEY="your-key-here"
   ```
3. **Run**
   ```bash
   source .env && go run ./cmd/mrdn ingest-lambda-congress
   ```
4. **Verify API response schema** — the parser was built against an assumed schema.
   If the actual response has different field names, update `internal/parser/lambda_congress.go`.

**Expected yield:** Most comprehensive recent coverage, dual-chamber.

---

## Phase 3 — Wire into RegisterSources + scheduled cron

Once Phase 1–2 API keys are working:

1. **Add Lambda source to `RegisterSources()`** in `internal/ingestion/supervisor.go`:
   ```go
   if s.cfg.LambdaAPIKey != "" {
       sources = append(sources, parser.NewLambdaCongressSource(client, s.cfg.LambdaAPIKey))
   }
   ```
   (FMP is already wired in.)

2. **Update GitHub Actions cron** (`ingest-deploy.yml`):
   - Add `MRDN_LAMBDA_API_KEY: ${{ secrets.MRDN_LAMBDA_API_KEY }}`
   - Run `ingest-once` daily — picks up FMP + Lambda + EFDS (when it recovers)

3. **Score backfill** after each ingestion run:
   ```bash
   go run ./cmd/mrdn score-backfill
   ```

---

## What's already built

| Component | Status | File |
|-----------|--------|------|
| FMP congress parser | Done | `internal/parser/fmp_congress.go` |
| FMP congress CLI | Done | `internal/cli/ingest_fmp_congress.go` |
| Lambda congress parser | Done | `internal/parser/lambda_congress.go` |
| Lambda congress CLI | Done | `internal/cli/ingest_lambda_congress.go` |
| Lambda config field | Done | `internal/config/config.go` (LambdaAPIKey) |
| Senate Stock Watcher CLI | Done | `internal/cli/ingest_senate_trades.go` (historical backfill only) |
| House Stock Watcher CLI | Done | `internal/cli/ingest_house_trades.go` (historical backfill only) |

---

## Action items for you

| # | Action | Time |
|---|--------|------|
| 1 | Sign up at financialmodelingprep.com, get API key | 2 min |
| 2 | Add `MRDN_FMP_API_KEY` to `.env`, run `ingest-fmp-congress` | 1 min |
| 3 | Sign up at lambdafin.com, get API key | 2 min |
| 4 | Add `MRDN_LAMBDA_API_KEY` to `.env`, run `ingest-lambda-congress` | 1 min |
| 5 | Ask Claude to wire Lambda into RegisterSources + cron | 5 min |
