# Data Layer Phase 2 — Design Specification

## Overview

Phase 2 addresses gaps found during the Phase 1 data audit: missing data pipelines, low company match rates, and operational fragility. The work spans four areas: new data pipelines for underserved event types, improved company resolution via aliases, frontend verification, and operational hardening.

## 1. Company Alias System

### Problem
Company matching relies on exact ticker or name lookup. The resolver chain is: ticker cache → name cache (with `normalizeName` suffix stripping) → `SearchCompanyByName` (bidirectional prefix match). This handles casing and corporate suffixes, but fails on abbreviations ("JPM" for "JPMorgan Chase"), DBA names, and names that differ structurally ("Meta Platforms" vs "Facebook"). The `entity_aliases` table exists but is empty.

### Design
Add a CLI command `generate-aliases` that seeds `entity_aliases` from existing data:
- For each company, generate common abbreviations and known DBA names
- Mine unmatched events: extract company names from `event_data` where `company_id IS NULL`, group by frequency, and present the top unresolved names for manual alias mapping
- Source: `companies` table + unresolved event scan

Modify the resolver's `lookupName()` to add alias lookup as a 4th tier after `SearchCompanyByName` fails:
1. Ticker cache (exact match)
2. Name cache + `normalizeName` (suffix stripping, case normalization)
3. `SearchCompanyByName` (bidirectional prefix match in DB)
4. **`GetCompanyByAlias`** (already exists on `*db.Store` in `entity_links.go` — query `entity_aliases` table, case-insensitive via existing `COLLATE NOCASE` index; returns `(CompanyLookup, error)`, wrapping `sql.ErrNoRows` on miss)

Add `GetCompanyByAlias(ctx context.Context, alias string) (CompanyLookup, error)` to `ResolverStore` interface. The method already exists on `*db.Store`; only the interface declaration is needed.

Successful alias hits should be cached in the resolver's `byNameLower` map to avoid repeated DB lookups for the same name.

### Backfill
After seeding aliases, `ingest-once` already calls `res.Backfill()` which re-resolves events where `company_id IS NULL`. Once the EFDS and Federal Register resolver cases are implemented (sections 2 and 4), those events will resolve and exit the backfill pool. Until then, backfill will log and skip them (as it does today).

## 2. EFDS Senate → Congressional Trades

### Problem
The EFDS Senate parser (`internal/parser/efds.go`) ingests filing-level events with `source: "efds_senate"` and `event_type: "congressional_disclosure"`. The resolver currently returns 0 for `efds_senate` events (line 155–157 of `resolver.go`). Individual trade details (ticker, amount, direction) are not extracted into `congressional_trades`.

### Design
Replace the `efds_senate` early-return in the resolver's `Resolve()` switch with a call to `resolveEFDSTrades()`. Dispatch is on `evt.Source` (not `event_type`), matching the existing pattern.

`resolveEFDSTrades()`:
- Unmarshal `event_data` JSON to extract the trade array from the EFDS filing
- Resolve person via bioguide ID or name lookup in `persons` table
- For each trade: resolve company via ticker, insert into `congressional_trades`
- Use `isDuplicateError` (after the SQLite fix) to suppress re-insert errors during backfill
- Return first resolved `company_id` for the event-level link, or 0 if no trades resolved

Add to `ResolverStore` interface:
- `InsertCongressionalTrade` (exists on `*db.Store` in `typed_tables.go`, missing from interface)
- `GetPersonBySlug(ctx context.Context, slug string) (Person, error)` (exists on `*db.Store` in `persons.go`, missing from interface — needed for person resolution)

No `FindPersonByBioguide` method exists. Person lookup will use `GetPersonBySlug` since the EFDS parser generates slugs from senator names. If bioguide-based lookup is needed, add `GetPersonByBioguide` to `db.Store` first.

### Parser changes
The EFDS parser may need to include more detail in `event_data`. If the current filing JSON doesn't contain per-trade fields (ticker, amount, trade_type), the parser must be updated to include them. This will be determined during implementation by inspecting the actual EFDS API response shape.

## 3. SEC Litigation Releases → Court Filings

### Problem
The `court_filings` table exists but has no data source populating it.

### Design
New parser: `internal/parser/sec_litigation.go`
- Source: SEC EDGAR litigation releases RSS feed
- `source_id`: assumed to be the litigation release number (e.g., "LR-25832") — **validate against actual RSS feed during implementation**; if no stable ID exists in the feed, fall back to a hash of title + date
- Extract: case_number, court, filing_type, parties, filed_at
- Produce events with `source: "sec_edgar_lit"` and `event_type: "sec_litigation"`

New resolver case in `Resolve()` switch: `case "sec_edgar_lit"` → `resolveSecLitigation()`
- Unmarshal event_data, resolve company from party names via alias chain
- Insert into `court_filings` + `court_filing_parties` junction table

Add to `ResolverStore` interface:
- `InsertCourtFiling` (exists on `*db.Store`, missing from interface)

Add `sec_edgar_lit` to source registry in `ingestion/supervisor.go` and seed `source_meta`.

Register the new source in `ingestion/supervisor.go`.

## 4. Federal Register → Tariffs

### Problem
Federal Register events are already ingested (`source: "federal_register"`, `event_type: "regulatory_action"`) but tariff-relevant actions aren't routed to the `tariffs` table. The resolver currently returns 0 for all `federal_register` events.

### Design
Replace the `federal_register` early-return in the resolver's `Resolve()` switch with a call to `resolveFedRegTariff()`.

`resolveFedRegTariff()`:
- Unmarshal `event_data` JSON (raw Federal Register API document)
- Check `type` field for "Rule" or "Proposed Rule"
- Check `subtype` or `cfr_references` for tariff-relevant CFR parts (Title 19 — Customs Duties, specifically parts 12, 134, 159, 163 which cover trade remedies and duty rates)
- If not tariff-relevant, return 0 (most Federal Register events are not tariffs)
- Extract: action_type from document title, effective_at from `effective_on` field
- HS codes and countries: extract from document body text if present (best-effort regex), otherwise leave junction tables empty
- Insert into `tariffs` + junction tables `tariff_hs_codes` and `tariff_countries`

Add `InsertTariff` to `ResolverStore` interface (exists on `*db.Store`, missing from interface).

## 5. Frontend Verification

### Problem
New data flowing into previously-empty tables (court_filings, tariffs with detail rows) needs to render correctly in existing MQL widgets.

### Design
No new frontend components. Verify:
- MQL queries with `type:court_filing`, `type:tariff` return results after data flows
- Timeline and grouped views render the new event types
- No JS errors from unexpected data shapes

This is a manual verification step after data pipelines are live, not a code change.

## 6. Operational Hardening

### 6a. Cross-Package Fixture Tests
Add test fixtures that serialize events through the parser and deserialize them in the resolver, catching JSON tag mismatches at test time instead of production.

Location: `internal/resolver/fixtures_test.go`
- One fixture per event type (including the new EFDS trade and SEC litigation types)
- Parser produces JSON → resolver unmarshals → assert all fields populated
- Specifically: assert `EventType` and `Source` values match what the resolver switch expects
- These are unit tests using `mockStore` — no real DB required

### 6b. Silent-Drop Counters
Add metrics to the resolver for events that fail to resolve:
- Log counter of events where `company_id` remains NULL after resolution
- Log counter per source for resolver errors
- Emit in `ingest-once` summary line

### 6c. D1 Migration Versioning
The single-file `001_sqlite_initial.sql` will diverge as we add schema. Add a version check:
- The existing `Migrate()` function in `db/migrate.go` already tracks version 1 in `schema_migrations`. Increment to version 2 when adding any schema changes in Phase 2.
- `Migrate()` will insert the new version row after applying the migration, using `INSERT OR IGNORE` to handle re-runs.
- CI D1 upload step: after local ingest, embed the schema version in the uploaded SQL as a final `INSERT OR IGNORE INTO schema_migrations (version) VALUES (N)` statement. No remote query needed — the version is self-documenting in the data.
- To detect drift: add a CI step that runs `SELECT MAX(version) FROM schema_migrations` on the local SQLite after ingest and compares against the `SchemaVersion` constant. Mismatch = build failure.

### 6d. Fix `isDuplicateError` for SQLite
Pre-existing bug: `isDuplicateError()` in `resolver.go` checks for PostgreSQL error strings (`"duplicate key"`, `"23505"`). SQLite returns `"UNIQUE constraint failed"`. Fix to check all three patterns. This affects all existing resolver insert paths, not just new ones.

All new resolver functions (`resolveEFDSTrades`, `resolveSecLitigation`, `resolveFedRegTariff`) must wrap their typed-table inserts with `isDuplicateError` checks to suppress duplicate errors during backfill re-processing. The typed-table insert methods use plain `INSERT` (not `INSERT OR IGNORE`), so backfill will re-encounter already-inserted records.

## Interface Changes Summary

Methods to add to `ResolverStore` interface in `resolver.go`:
- `InsertCongressionalTrade(ctx context.Context, t db.CongressionalTrade) error`
- `InsertCourtFiling(ctx context.Context, f db.CourtFiling) error`
- `InsertTariff(ctx context.Context, t db.Tariff) error`
- `GetCompanyByAlias(ctx context.Context, alias string) (db.CompanyLookup, error)`
- `GetPersonBySlug(ctx context.Context, slug string) (db.Person, error)`

All five methods must also be added to test mocks in `resolver_test.go`:
- `mockStore`: add slice fields (`insertedCongressionalTrades`, `insertedCourtFilings`, `insertedTariffs`) to capture inserted records for test assertions. `GetCompanyByAlias` and `GetPersonBySlug` return from configurable maps.
- `cancellingStore`: add no-op implementations that return `ctx.Err()`.

## Implementation Order

1. `isDuplicateError` SQLite fix + interface expansion (unblocks everything)
2. Company alias system (higher match rates improve all downstream data)
3. EFDS Senate trades (highest-value data gap)
4. SEC Litigation parser + resolver (new parser, moderate complexity)
5. Federal Register tariff routing (smallest scope, no new parser)
6. Operational hardening (can be done in parallel with 3-5)
7. Frontend verification (after data flows)

## Non-Goals

- No new UI components or dashboard pages
- No PACER integration (too complex, requires account)
- No fuzzy/ML-based company matching (aliases cover the 80% case)
- No new score engine changes (existing weights handle new event types)
- No transactional wrapping of multi-table inserts (pre-existing design; out of scope for this phase)
