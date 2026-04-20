# Live Government Financial Disclosure Sources

## Goal

Replace stale/broken data pipeline with live sources covering House, Senate, and federal judiciary financial disclosures. Currently all congressional trades stop at April 2023 (dead House Stock Watcher dataset) and Senate EFDS produces zero trade records (parser extracts filing metadata but not transactions).

## Sources

### 1. Finnhub Congressional Trading (House + Senate)

- **Endpoint:** `GET /api/v1/stock/congressional-trading?symbol={SYM}&from={DATE}&to={DATE}&token={KEY}`
- **Auth:** Existing `MRDN_FINNHUB_API_KEY`
- **Response fields:** `name`, `position` (Representative/Senator), `symbol`, `transactionDate`, `filingDate`, `transactionType` (Sale/Purchase), `amountFrom`, `amountTo`, `ownerType`, `assetName`
- **Poll strategy:** Iterate over tracked companies (from `companies` table), fetch trades per symbol. Rate limit: 30 req/sec. Use 60-second poll interval with batching.
- **Dedup:** Source ID = `finnhub_congress|{name}|{symbol}|{transactionDate}|{transactionType}`

### 2. CourtListener Judicial Disclosures (Federal Judges)

- **Endpoint:** `GET /api/rest/v4/financial-disclosures/?ordering=-date_created` and nested `investments` array
- **Auth:** `Authorization: Token {MRDN_COURTLISTENER_TOKEN}` (new config field)
- **Response fields:** `person` (judge URL/ID), `year`, `investments[].description`, `investments[].transaction_date`, `investments[].transaction_value_code`, `investments[].gross_value_code`, `investments[].income_during_reporting_period_code`
- **Poll strategy:** Fetch recent disclosures ordered by `date_created`, paginate through new ones since last poll. 60-second interval.
- **Dedup:** Source ID = `courtlistener|{disclosure_id}|{investment_id}`

## Architecture

Both sources follow the existing `Source` interface (`Name() string`, `Poll(ctx) ([]db.Event, error)`).

### Data flow

```
Finnhub REST / CourtListener REST
  → parser (JSON → db.Event with structured event_data)
    → store.InsertEvent (dedup by source_id)
      → resolver (event → congressional_trades + person upsert)
```

### Person resolution

- Finnhub: `position` field → `role: "representative"` or `"senator"`, `branch: "legislative"`
- CourtListener: `person` endpoint → judge name, court → `role: "judge"`, `branch: "judicial"`
- Both use existing `UpsertPerson` with no-clobber (existing tier preserved)

### Reusing congressional_trades table

The `congressional_trades` table already has the right shape for judicial investments:
- `person_id`, `company_id`, `ticker`, `trade_type`, `amount_range_low/high`, `traded_at`, `filed_at`
- Judicial holdings use coded value ranges (A-P) which map to dollar ranges — decode in parser

### CourtListener value codes → dollar amounts

| Code | Min | Max |
|------|-----|-----|
| J | $15,001 | $50,000 |
| K | $50,001 | $100,000 |
| L | $100,001 | $250,000 |
| M | $250,001 | $500,000 |
| N | $500,001 | $1,000,000 |
| O | $1,000,001 | $5,000,000 |
| P1 | $5,000,001 | $25,000,000 |
| P2 | $25,000,001 | $50,000,000 |
| P3 | Over $50,000,000 | - |

## New files

- `internal/ingestion/sources/finnhub_congress.go` — poll source
- `internal/ingestion/sources/courtlistener.go` — poll source
- `internal/parser/finnhub_congress.go` — parse Finnhub congressional response
- `internal/parser/finnhub_congress_test.go`
- `internal/parser/courtlistener.go` — parse CourtListener disclosures + investments
- `internal/parser/courtlistener_test.go`
- `internal/resolver/resolver.go` — add two cases to source switch

## Modified files

- `internal/config/config.go` — add `CourtListenerToken` field
- `internal/ingestion/supervisor.go` — register both sources in `RegisterSources()`
- `internal/resolver/resolver.go` — add `finnhub_congress` and `courtlistener` resolver cases

## What this replaces

- Dead House Stock Watcher dataset (frozen April 2023)
- Broken EFDS Senate parser (ingests metadata but no transactions)
- Both old sources can remain for historical replay but are no longer the live pipeline

## What stays untouched

- Existing Finnhub WebSocket market data source (different endpoint, different purpose)
- `ingest-house-trades` CLI command (historical replay)
- All insight detectors (swarms, lone wolf, coordinated) — automatically pick up new trades
- Frontend — no changes needed
