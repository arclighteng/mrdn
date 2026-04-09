# Unified Findings: dashboard-feature-pack-v1

Generated: 2026-04-08T22:13:55Z

## architect

## AGENT_FINDINGS: Architect
### Persona: Architect
### Task: dashboard-feature-pack-v1
### Date: 2026-04-08

#### Summary
Four UX gaps to scope: global search, score explainability, watchlists, time-range
control. After reading `internal/score/`, `internal/api/server.go`, and `internal/db/`,
the picture is much better than expected. Three of the four are pure additive work
on existing patterns; one (explainability) is *cheaper* than originally estimated
because sub-scores are already persisted on the `scores` table — they just aren't
surfaced. No structural refactoring needed for any feature in v1.

#### Findings
| Severity | Area | File/Component | Issue | Recommendation |
|----------|------|----------------|-------|----------------|
| Med | API surface | `internal/api/server.go` | No `/search` endpoint exists. Search will fan out across `companies`, `persons`, `tickers`, and possibly `sources`. | Add `GET /api/v1/search?q=&limit=` returning `{companies, persons, tickers}` groups with ≤10 each. Plain `ILIKE '%q%'` on `name`/`ticker`/`slug` columns is fine for v1; defer `tsvector` until row counts justify it. Single handler, parallel queries via `errgroup`. |
| Med | Data model | `internal/db/scores.go` | `Score` struct already persists `MarketScore`, `PolicyScore`, `InsiderScore`, `CompositeScore`, `WeightVersion`, `ComputedAt`. The breakdown exists; the UI just doesn't show it. | Feature #2 splits cleanly: **2a** surfaces existing sub-scores in the company drilldown (no API change). **2b** adds `GET /api/v1/companies/{ticker}/score-breakdown` returning top-N contributing rows from `insider_trades`, `sanctions`, `contracts`, `donations`, `market_data` for the score window. The `ScoreStore` interface methods (`GetInsiderTradesRange`, `GetSanctionsRange`, etc.) already exist — reuse them directly. |
| Low | API surface | `internal/db/events.go:35` | `EventFilter` has `Since *time.Time` but no `Until`. Other endpoints (`/scores/movers`, `/stats/activity`, heatmaps) have hardcoded windows. | Add `Until *time.Time` to `EventFilter`. Add `since`/`until` query params to `/scores/movers`, `/scores/rankings`, `/stats/activity`, and the activity heatmap. Use a shared `parseTimeRange(r)` helper in `internal/api/params.go` so each handler is two lines. |
| Low | Data model | (none) | Watchlists with no auth means client-side only. Risk: users assume cross-device sync. | Store in `localStorage` keyed `mrdn:watchlist:v1`. JSON array of tickers + person slugs. Add a tiny "this device only" caveat under the empty state. Server-side persistence is a v2 concern that requires Sign-In With Vercel or similar. **Don't build it now.** |
| Info | Coupling | `internal/api/companies.go` + `internal/api/persons.go` | Search will need to call into both stores. | Keep the search handler in a new `internal/api/search.go` file, import both stores. Don't create a "search service" abstraction — it's one handler doing two queries. |

#### Conflicts Anticipated
- **Watchlist persistence:** I'm recommending localStorage-only. QA will likely flag the lack of cross-device sync as a usability gap. Resolve in favor of localStorage for v1 — auth is the precondition and we don't have it. Document the gap.
- **Search ranking:** Reviewer may want to skip ranking entirely (dump matches in insertion order). I want a simple `ORDER BY length(name) ASC, name ASC` so exact-prefix wins. Cheap, high-value, not premature.
- **Score breakdown depth:** I want to ship 2a (surface existing sub-scores) and 2b (contributing events) as separate PRs. QA may want them shipped together so the user sees a "complete" feature. Resolve toward two PRs — 2a is 1–2h and gives 80% of the value.

#### Requires Human Decision
- **Search scope.** Should `/search` also match against `event_data` JSON (e.g. recipient names from USAspending, OFAC names)? Doable with `event_data->>'name' ILIKE` but risks slow scans and unstable results. **Recommendation: NO for v1**, reconsider after we add a `searchable_text` derived column.
- **Time-range default.** Currently most cards say "24h" implicitly. Switching to a global "Last 24h / 7d / 30d / 90d / All" selector means picking a default. Recommend **7d** as the new default — broad enough to feel populated, narrow enough to feel current. Confirm.

#### Recommended Next Steps
Phase 1 — ship in this order (cheapest first, each is a single PR):
1. Watchlists (localStorage) — 2–3h
2. Score explainability **2a** (surface existing sub-scores) — 1–2h
3. Time-range control — 3–5h
4. Global search — 4–6h
5. Score explainability **2b** (contributing events drawer) — 4–6h

Phase 2 — defer:
- `tsvector` search index (only if `ILIKE` gets slow)
- Server-side watchlist persistence (requires auth)
- Cross-source entity resolution for search

---

## qa

## AGENT_FINDINGS: QA
### Persona: QA
### Task: dashboard-feature-pack-v1
### Date: 2026-04-08

#### Coverage Summary
Current: existing handlers in `internal/api/` have companion `_test.go` files
(events, scores, companies, persons, etc.) — coverage discipline is in place.
Threshold for new features: every new handler ships with at least the standard
trio (happy path, empty result, bad input). The four features have very different
test profiles — some are trivial, one has real edge cases.

#### Test Pyramid Assessment
Unit: healthy (parser tests, db tests, score engine tests, params tests)
Integration: present (`*_test.go` next to handlers, hitting `*Server`)
E2E: none — no Playwright/browser tests for `web/static/index.html`
Assessment: **Healthy backend, thin frontend.** The Alpine UI has no automated
tests. New frontend code (watchlists, sub-score chart, search modal) will be
manually verified. This is fine for v1; flag for v2.

#### Findings
| Severity | Type | File/Component | Issue | Recommendation |
|----------|------|----------------|-------|----------------|
| Med | Edge case | feature #1 search | Multiple risk-prone inputs: empty `q`, single character, SQL meta-chars (`%`, `_`), unicode, very long strings, leading/trailing whitespace, mixed case. | Test cases: `q=""` → 400 or empty groups (decide & document); `q="a"` → minimum 2 chars or return all (decide); `q="50%"` → escape `%`/`_` in `ILIKE` patterns or document the quirk; `q="MICROSOFT"` matches `Microsoft Corp`; trailing whitespace stripped. **Critical:** `ILIKE` `%` and `_` are wildcards — escape them in user input or you'll return surprising matches. |
| Med | Edge case | feature #5 time-range | Time zones, DST, `until < since`, `since > now`, very wide ranges (10 years). | Backend always operates in UTC (verify). Reject `until < since` with 400. Reject ranges > some sane max (1 year?) to prevent accidental table scans. Frontend chips should clamp to UTC midnight boundaries to avoid "off by 4h" confusion for users on the East Coast. |
| Med | Edge case | feature #2b explainability | "Top contributing events" is a ranking question — by what? | Decide & document the ranking: insider trades by `transaction_value DESC`, sanctions by `effective_date DESC`, contracts by `award_amount DESC`, donations by `contribution_amount DESC`. Test: a company with zero events in the window returns an empty breakdown (not an error). Test: `weight_version` mismatch between current score and historical contributing events — document expected behavior (either re-score or flag the staleness). |
| Med | State | feature #4 watchlists | localStorage gotchas: schema migration, quota exceeded, private browsing, cross-tab sync. | Version the key (`mrdn:watchlist:v1`) so future schema changes are migration-safe. Wrap reads/writes in try/catch — Safari private mode throws on `setItem`. Fail open: a broken watchlist should not crash the dashboard. **Test manually:** add 100 tickers, refresh, verify all present; clear localStorage, verify empty state renders. |
| Low | Coverage | feature #1 search backend | New handler needs the standard test trio. | `internal/api/search_test.go`: happy (q matches one company → returns it); empty (q matches nothing → empty groups, 200); bad (`q` missing → 400); large (`q` 1000 chars → handled, not a panic). |
| Low | Coverage | feature #5 backend plumbing | New `parseTimeRange` helper needs a table-driven test. | `internal/api/params_test.go`: cases for happy, missing, malformed, `until < since`, both nil, only one set. ~10 cases. |
| Info | Manual verification | features #1, #2a, #4 frontend | No browser test infra; rely on manual smoke-test post-deploy. | Document a 5-minute smoke checklist: open dashboard → search "micro" → click result → verify drilldown loads → star a ticker → reload → verify watchlist persists → toggle time-range to 7d → verify chart updates. |

#### Missing Test Scenarios
- **Search:** `ILIKE` injection of `%` / `_` wildcards.
- **Time-range:** DST boundary days (Mar 8 / Nov 1 in US/Eastern users).
- **Score breakdown:** company exists but has zero events in window.
- **Watchlist:** corrupted localStorage value (not JSON, wrong shape).
- **Watchlist:** quota exceeded (write fails silently).
- **All four:** behavior when the API returns a 5xx — empty state vs. error toast?

#### Conflicts Anticipated
- **Architect wants `ILIKE` for v1 search, no `tsvector`.** I agree on the `tsvector` deferral but want explicit `%`/`_` escaping in v1 — that's a correctness bug, not a perf optimization.
- **Reviewer wants minimum abstraction.** Agreed. The only test-related abstraction I want is `parseTimeRange` (justified — used by 4+ handlers).
- **Watchlist server persistence:** I'll note the cross-device gap but not push for a server backend in v1. The right answer is "auth first, then sync" and that's a separate roadmap item.

#### Recommended Next Steps
1. **Before any handler ships:** add the `parseTimeRange` helper + table-driven test.
2. **Before search ships:** add `escapeILikePattern(s)` helper that escapes `%`, `_`, `\` and unit-test it. Use it in the search query builder.
3. **Before score 2b ships:** decide and document the per-table ranking, add it to the handler doc-comment, test the empty-window case.
4. **Watchlist localStorage wrapper:** wrap in try/catch, version the key, test manually in Safari private mode if available.
5. **Document a manual smoke-test checklist** in `docs/OPERATIONS.md` (which already exists per `git status`).

---

## reviewer

## AGENT_FINDINGS: Reviewer
### Persona: Reviewer
### Task: dashboard-feature-pack-v1
### Date: 2026-04-08

#### Summary
Reviewing the four features for code-shape risk: minimal diff, no premature
abstractions, no dead code paths. The existing API is consistently structured
(`internal/api/<noun>.go` + handler funcs on `*Server`) — all four features should
follow that pattern exactly. 0 blockers, 4 suggestions, 1 hard NO on a tempting
abstraction.

Blocking: 0 | Suggestions: 4 | Hard nos: 1

#### Findings
| Type | File/Component | Issue | Recommendation |
|------|----------------|-------|----------------|
| [SUGGESTION] | new `internal/api/search.go` | Match the existing pattern: one file per noun, methods on `*Server`, route registration in `server.go`. | Single handler `handleSearch`. Use `errgroup.Group` for the 3 parallel queries (companies, persons, tickers). Don't introduce a `SearchService` interface — it's one function calling three store methods. |
| [SUGGESTION] | `web/static/index.html` (watchlists) | Resist the temptation to introduce a "store" abstraction in the Alpine app. The existing code keeps state on the root `app()` data object. | Add `watchlist: JSON.parse(localStorage.getItem('mrdn:watchlist:v1') \|\| '[]')`, a `toggleWatchlist(ticker)` method that re-saves, and a `watchlistOnly` boolean filter. ~30 lines total. No new files. |
| [SUGGESTION] | `internal/api/params.go` | A `parseTimeRange(r)` helper would be reused by 4+ handlers when feature #5 lands. | Add it. Returns `(since, until *time.Time, err error)`. Validate `until > since`. Reuse `parseTime` (already exists for `since`). ~15 lines + a unit test. |
| [SUGGESTION] | feature #2a (surface existing sub-scores) | Don't add a new endpoint for 2a. The sub-scores are already in the response from `/companies/{ticker}/scores`. | Pure frontend change. Add a 3-bar mini-chart (Market / Policy / Insider) under the composite score in the company drilldown. Color each bar by its own value. ~20 lines of HTML + ECharts or just CSS divs. |
| [HARD NO] | feature #1 (search) | Do **not** introduce a `tsvector` column, GIN index, search migration, or any "search infrastructure" in v1. | The `companies` table is small (low thousands of rows) and `ILIKE '%foo%'` will return in <50ms on any reasonable host. Adding a search index is speculative generality. Revisit only when we have profiling data showing it's slow. |

#### Conflicts Anticipated
- **Architect wants 2a + 2b as separate PRs.** I agree — 2a is a 20-line frontend diff and shouldn't be blocked by 2b's backend work. No conflict.
- **QA may want test coverage for the new `parseTimeRange` helper before any handler uses it.** Agreed, but the helper is small enough that one table-driven test covers it. No friction.
- **Watchlist persistence:** Architect says localStorage only, no server. Agree. Anyone arguing for server-side persistence in v1 is signing up to build an auth system first.

#### Positive Observations
- The existing API structure (one file per noun, handlers on `*Server`, params parsed via `internal/api/params.go`) is clean and obvious. New features will fit naturally without restructuring.
- `internal/score/engine.go` is well-factored: each sub-scorer is isolated and the data-access methods are already range-windowed (`GetInsiderTradesRange`, etc.). Feature 2b will reuse them as-is — zero refactoring.
- `EventFilter` already has `Source`, `EventType`, `CompanyID`, `Since`, `Limit`, `Offset`. Adding `Until` is one field and one `WHERE` clause.

#### Recommended Next Steps
Blocking: none.

Order of work (matches Architect's, with the hard-no on `tsvector`):
1. Watchlists — pure frontend, no review risk.
2. Score 2a — pure frontend, no review risk.
3. `parseTimeRange` helper + plumb through 4 handlers — small backend, table-driven test.
4. Search — single handler, single new file, no abstraction.
5. Score 2b — single handler, reuse existing `ScoreStore` methods.

---

