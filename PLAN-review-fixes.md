# Plan: Review Fixes for Differentiation Features

> **For agentic workers:** Execute these tasks in order. Tasks within the same priority tier can be parallelized if they touch different files. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Address all findings from the 5-agent review (security, code quality, usability, accessibility, architecture) of commits 75d619d..c42eb7f.

**Architecture:** All fixes are surgical — no new features, no refactors beyond what's needed. The codebase is Go + SQLite backend, Alpine.js + Tailwind + D3.js frontend in a single `index.html`.

---

## Priority 1: Fix Now (before next deploy)

---

### Task 1: Add `redactKey` to Lambda and FMP parser error paths

**Why:** API keys are embedded in URLs via `fmt.Sprintf("%s?apikey=%s", ...)` but unlike `polygon.go` and `fec.go`, neither parser applies `redactKey()` before logging or returning errors containing the URL. Keys will leak into logs.

**Files:**
- Modify: `internal/parser/lambda_congress.go`
- Modify: `internal/cli/ingest_fmp_congress.go` (the FMP source is in `internal/parser/fmp_congress.go` — check both)

**Context:**
- `redactKey` is defined in `internal/parser/polygon.go` — it replaces the key substring with `***` in a string. It's in the same `parser` package so lambda can use it directly.
- For the FMP parser, check if `redactKey` is accessible (same package) or needs to be called from the CLI file.
- The pattern to follow is in `internal/parser/polygon.go` lines 59/65 and `internal/parser/fec.go` lines 59/65.

- [ ] **Step 1: Read `internal/parser/lambda_congress.go` and find all error format strings that could contain the URL**

Look for `fmt.Errorf` or `log.Printf` calls where the `url` variable (which contains the API key) could appear in the output.

- [ ] **Step 2: Apply `redactKey(url, l.apiKey)` in all error paths in lambda_congress.go**

Every place the URL appears in an error string, replace `url` with `redactKey(url, l.apiKey)`.

- [ ] **Step 3: Read `internal/parser/fmp_congress.go` and apply the same treatment**

The FMP parser has the same pattern: `url := fmt.Sprintf("%s?apikey=%s", ch.url, f.apiKey)`. Apply `redactKey` to all error paths.

- [ ] **Step 4: Verify build**

Run: `go vet ./internal/parser/... ./internal/cli/...`

- [ ] **Step 5: Commit**

```bash
git add internal/parser/lambda_congress.go internal/parser/fmp_congress.go
git commit -m "security: redact API keys in lambda/fmp parser error paths"
```

---

### Task 2: Add duplicate-skip guard to Lambda congress ingestion

**Why:** `ingest_lambda_congress.go` is missing the `if id == 0 { continue }` guard after `InsertEvent`. When a duplicate event returns id=0 with no error, the code proceeds to call `res.Resolve` on a zero-ID event, which could panic or create bad foreign keys. The FMP ingester at `internal/cli/ingest_fmp_congress.go:88-90` has this guard; Lambda does not.

**Files:**
- Modify: `internal/cli/ingest_lambda_congress.go`

**Context:**
- The FMP pattern (correct) at `ingest_fmp_congress.go:83-90`:
  ```go
  id, ierr := store.InsertEvent(ctx, evt)
  if ierr != nil {
      failed++
      continue
  }
  if id == 0 {
      continue // duplicate
  }
  inserted++
  ```
- The Lambda code is missing the `if id == 0` block.

- [ ] **Step 1: Read `internal/cli/ingest_lambda_congress.go` and find the InsertEvent call**

- [ ] **Step 2: Add `if id == 0 { continue }` after the error check, before `inserted++`**

- [ ] **Step 3: Verify build**

Run: `go vet ./internal/cli/...`

- [ ] **Step 4: Commit**

```bash
git add internal/cli/ingest_lambda_congress.go
git commit -m "fix: add duplicate-skip guard to lambda congress ingestion"
```

---

### Task 3: Add context timeout to `AccountabilityInputs`

**Why:** Every other heavy query in the codebase has a context timeout (`CoTraderNetwork` = 10s, `BFSGraph` = 5s). `AccountabilityInputs` has a 6-CTE query with two self-joins and no timeout. At scale this could block the entire export indefinitely.

**Files:**
- Modify: `internal/db/compliance.go`

**Context:**
- The pattern to follow is in `internal/db/graph.go` line 274:
  ```go
  ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
  defer cancel()
  ```
- `AccountabilityInputs` is more complex, so use 30 seconds.
- The `time` package may need to be added to imports.

- [ ] **Step 1: Read `internal/db/compliance.go` and find `func (s *Store) AccountabilityInputs`**

- [ ] **Step 2: Add timeout at the top of the function body**

```go
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
```

- [ ] **Step 3: Ensure `"time"` is in the imports**

- [ ] **Step 4: Verify build**

Run: `go vet ./internal/db/...`

- [ ] **Step 5: Commit**

```bash
git add internal/db/compliance.go
git commit -m "fix: add 30s timeout to AccountabilityInputs query"
```

---

### Task 4: Fix weak deduplication key in Lambda parser

**Why:** The `sourceID` in `lambda_congress.go` concatenates fields with no separator: `FirstName + LastName + Symbol + TransactionDate + Type`. This means `"JohnSmith" + "AAPL"` collides with `"John" + "SmithAAPL"`.

**Files:**
- Modify: `internal/parser/lambda_congress.go`

**Context:**
- The Senate trades parser uses `"|"` as separator in `senateSourceID` at `internal/cli/ingest_senate_trades.go:103-108`:
  ```go
  return strings.TrimSpace(r.PtrLink) + "|" + strings.TrimSpace(r.Ticker) + "|" + ...
  ```
- Apply the same pattern to the Lambda parser's source ID construction.

- [ ] **Step 1: Read `internal/parser/lambda_congress.go` and find the sourceID construction (around line 162-165)**

- [ ] **Step 2: Add `"|"` separators between each field**

- [ ] **Step 3: Verify build**

Run: `go vet ./internal/parser/...`

- [ ] **Step 4: Commit**

```bash
git add internal/parser/lambda_congress.go
git commit -m "fix: add separator to lambda congress dedup key to prevent collisions"
```

---

### Task 5: Wrap scan error in compliance.go

**Why:** `rows.Scan` error in `AccountabilityInputs` is returned unwrapped — no context about which step failed. Every other scan error in the codebase is wrapped with `fmt.Errorf`.

**Files:**
- Modify: `internal/db/compliance.go`

- [ ] **Step 1: Find the `rows.Scan` error return in `AccountabilityInputs`**

It looks like: `return nil, err`

- [ ] **Step 2: Wrap it**

Change to: `return nil, fmt.Errorf("scanning accountability row: %w", err)`

- [ ] **Step 3: Verify build**

Run: `go vet ./internal/db/...`

- [ ] **Step 4: Commit**

```bash
git add internal/db/compliance.go
git commit -m "fix: wrap scan error with context in AccountabilityInputs"
```

---

### Task 6: Frontend quick fixes (scoreboard overflow, network empty state, fetch error handling, basic a11y)

**Why:** Multiple reviewers flagged these as fix-now items. They're all in `web/static/index.html` and can be done in one pass.

**Files:**
- Modify: `web/static/index.html`

**Context:**
- The scoreboard table wrapper has `overflow-hidden` — needs `overflow-x-auto` for mobile
- The network view has no empty-state message when nodes array is empty
- `fetchScoreboard()` has no try/finally — spinner gets stuck on error
- Scoreboard `<th>` elements need `scope="col"`
- Toggle buttons need `aria-pressed`

- [ ] **Step 1: Read the scoreboard section (search for `personsMode==='scoreboard'`)**

- [ ] **Step 2: Change `overflow-hidden` to `overflow-x-auto` on the scoreboard wrapper div**

The div looks like:
```html
<div x-show="personsMode==='scoreboard'" class="bg-surface-1 rounded-xl border border-white/15 overflow-hidden">
```
Change `overflow-hidden` to `overflow-x-auto`.

- [ ] **Step 3: Add `scope="col"` to all `<th>` elements in the scoreboard thead**

Each `<th>` should get `scope="col"` added.

- [ ] **Step 4: Add `aria-pressed` to the Roster/Scoreboard toggle buttons**

Find the two toggle buttons (search `personsMode='roster'`). Add:
- Roster button: `:aria-pressed="personsMode==='roster'"`
- Scoreboard button: `:aria-pressed="personsMode==='scoreboard'"`

- [ ] **Step 5: Add network empty state message**

Find the Network View section (search `view === 'network'`). After the loading div and before the SVG, add:

```html
<div x-show="networkData && (!networkData.nodes || !networkData.nodes.length) && !networkLoading"
  class="absolute inset-0 flex items-center justify-center">
  <span class="text-neutral-400 text-sm">No co-trading relationships found. Ingest trade data to populate the network.</span>
</div>
```

- [ ] **Step 6: Add try/finally to `fetchScoreboard()`**

Find `async fetchScoreboard()` and wrap the body:

```js
async fetchScoreboard() {
  if (this.scoreboard.length) return;
  this.scoreboardLoading = true;
  try {
    const res = await this.api('/scoreboard');
    this.scoreboard = res || [];
  } finally {
    this.scoreboardLoading = false;
  }
},
```

- [ ] **Step 7: Verify the page loads**

Run: `source .env && go run ./cmd/mrdn export --out web/static/data`
Then serve and check in a browser.

- [ ] **Step 8: Commit**

```bash
git add web/static/index.html
git commit -m "fix: scoreboard overflow, network empty state, fetch error handling, basic a11y"
```

---

## Priority 2: Fix Soon (before public launch)

---

### Task 7: Add composite index migration for self-join performance

**Why:** Both `CoTraderNetwork` and the `round_trips` CTE in `AccountabilityInputs` self-join `congressional_trades` on `(ticker, person_id, traded_at)`. The existing single-column indexes force full scans. At 100K+ rows these queries will time out.

**Files:**
- Create: `internal/db/migrations/002_composite_indexes.sql`
- Modify: `internal/db/migrate.go` (or wherever migrations are registered — read the existing migration runner first)

- [ ] **Step 1: Read the migration runner to understand how migrations are loaded**

Check `internal/db/migrate.go` or grep for `001_sqlite_initial.sql` to find the migration pattern.

- [ ] **Step 2: Create the migration file**

```sql
-- 002: Composite indexes for self-join queries (CoTraderNetwork, AccountabilityInputs)
CREATE INDEX IF NOT EXISTS idx_ct_ticker_traded_person
    ON congressional_trades(ticker, traded_at, person_id);

CREATE INDEX IF NOT EXISTS idx_ct_person_ticker_traded
    ON congressional_trades(person_id, ticker, traded_at);
```

- [ ] **Step 3: Register the migration in the runner**

- [ ] **Step 4: Test migration runs cleanly**

Run: `source .env && go run ./cmd/mrdn migrate`

- [ ] **Step 5: Commit**

```bash
git add internal/db/migrations/002_composite_indexes.sql internal/db/migrate.go
git commit -m "perf: add composite indexes for congressional_trades self-joins"
```

---

### Task 8: Scoreboard keyboard accessibility and sort indicators

**Why:** Scoreboard columns are sortable by click only — no keyboard access, no sort direction indicator, no `aria-sort`. Keyboard users and screen reader users cannot use sorting at all.

**Files:**
- Modify: `web/static/index.html`

- [ ] **Step 1: Add `scoreboardSortCol` state to app data**

Add `scoreboardSortCol: 'score'` near the existing scoreboard properties.

- [ ] **Step 2: Update each sortable `<th>` to be keyboard-accessible with sort indicator**

Each sortable `<th>` needs:
- `tabindex="0"`
- `@keydown.enter="..."` and `@keydown.space.prevent="..."` with the same sort action as `@click`
- `:aria-sort="scoreboardSortCol === 'colname' ? 'descending' : 'none'"`
- `focus-visible:ring-2 focus-visible:ring-accent` in the class
- A sort arrow indicator: append `<span x-show="scoreboardSortCol === 'colname'" class="ml-1">&#9660;</span>` after the header text

- [ ] **Step 3: Update the sort `@click` handlers to also set `scoreboardSortCol`**

Example for Score column:
```html
@click="scoreboardSortCol='score'; scoreboard.sort((a,b)=>b.score-a.score)"
```

- [ ] **Step 4: Make scoreboard `<tr>` rows keyboard-navigable**

Add to each `<tr>`:
- `tabindex="0"`
- `@keydown.enter="openPerson(r.slug)"`

- [ ] **Step 5: Add `<caption>` to the table**

```html
<caption class="sr-only">Congressional Trading Accountability Scoreboard - click column headers to sort</caption>
```

- [ ] **Step 6: Commit**

```bash
git add web/static/index.html
git commit -m "a11y: keyboard-accessible scoreboard with sort indicators and ARIA"
```

---

### Task 9: D3 network accessibility and motion safety

**Why:** The network SVG is invisible to screen readers, D3 nodes aren't keyboard-accessible, and the force simulation doesn't respect `prefers-reduced-motion`.

**Files:**
- Modify: `web/static/index.html`

- [ ] **Step 1: Add `role="img"` and `aria-label` to the SVG element**

Find `<svg id="network-svg"` and add:
```html
role="img" aria-label="Co-Trader Network graph showing relationships between congressional traders"
```

- [ ] **Step 2: In `renderNetwork()`, add a `<title>` element to the SVG**

After `svg.selectAll('*').remove()`:
```js
svg.append('title').text('Co-Trader Network: ' + data.nodes.length + ' representatives, ' + data.edges.length + ' connections');
```

- [ ] **Step 3: Add `prefers-reduced-motion` check**

Before `sim.on('tick', ...)`:
```js
if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
  for (let i = 0; i < 300; i++) sim.tick();
  sim.stop();
  // Apply final positions
  link.attr('x1', d => d.source.x).attr('y1', d => d.source.y)
      .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
  node.attr('transform', d => `translate(${d.x},${d.y})`);
  return;
}
```

- [ ] **Step 4: Add `alphaDecay` so the simulation settles naturally**

Change the simulation setup to include:
```js
.alphaDecay(0.05)
```

- [ ] **Step 5: Add `tabindex` and `aria-label` to D3 nodes**

In the node creation block, after `node.append('text')`:
```js
node.attr('tabindex', 0)
    .attr('role', 'button')
    .attr('aria-label', d => d.label || 'Unknown')
    .on('keydown', (e, d) => { if (e.key === 'Enter') { /* navigate to person if slug available */ } });
```

- [ ] **Step 6: Commit**

```bash
git add web/static/index.html
git commit -m "a11y: network graph screen reader support, motion safety, keyboard access"
```

---

### Task 10: Timeline accessibility and tooltip

**Why:** Timeline emoji icons have no `role="img"` or `aria-label`, and truncated labels have no tooltip.

**Files:**
- Modify: `web/static/index.html`

- [ ] **Step 1: Find the timeline proof strip (search "Timeline proof strip")**

- [ ] **Step 2: Add `role="img"` and `:aria-label` to the icon div**

The icon div currently:
```html
<div class="w-8 h-8 rounded-full flex items-center justify-center text-sm"
  :class="..." x-text="step.icon"></div>
```

Change to:
```html
<div class="w-8 h-8 rounded-full flex items-center justify-center text-sm"
  :class="..." x-text="step.icon" role="img" :aria-label="step.kind + ' indicator'"></div>
```

- [ ] **Step 3: Add `aria-hidden="true"` to the connector line div**

The connector: `<div x-show="i > 0" class="w-8 h-0.5 bg-accent/30"></div>`
Add `aria-hidden="true"`.

- [ ] **Step 4: Add `:title` tooltip to truncated label**

The label div: `<div class="text-[10px] text-neutral-300 whitespace-nowrap max-w-[140px] truncate" x-text="step.label"></div>`
Add `:title="step.label"` so the full text shows on hover.

- [ ] **Step 5: Commit**

```bash
git add web/static/index.html
git commit -m "a11y: timeline strip aria labels, tooltips, and connector hiding"
```

---

### Task 11: Score color-only fix — add supplementary text

**Why:** Score severity (high/medium/low) is communicated by color alone (red/yellow/green), violating WCAG 1.4.1. Colorblind users cannot distinguish severity.

**Files:**
- Modify: `web/static/index.html`

- [ ] **Step 1: Find the score `<td>` in the scoreboard (search `r.score >= 70`)**

- [ ] **Step 2: Add a visually-hidden severity label**

After the score `<span>`, add:
```html
<span class="sr-only" x-text="r.score >= 70 ? 'high risk' : r.score >= 40 ? 'moderate risk' : 'low risk'"></span>
```

- [ ] **Step 3: Similarly for Filing Lag, Round-trips, Pre-event threshold cells**

Add `<span class="sr-only">above threshold</span>` inside the conditional-colored cells when the threshold is exceeded.

- [ ] **Step 4: Ensure `.sr-only` class exists**

If not already defined, add to the `<style>` block:
```css
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0,0,0,0); border: 0; }
```

- [ ] **Step 5: Commit**

```bash
git add web/static/index.html
git commit -m "a11y: add screen-reader severity labels to score and threshold cells"
```

---

## Priority 3: Track as Tech Debt

---

### Task 12: Extract `buildScoreboardEntries` from export.go

**Why:** `exportScoreboard` mixes scoring computation (ratio calc, `AccountabilityInput` construction) with I/O. Extracting the assembly into a pure function makes it testable and keeps the export layer as pure I/O.

**Files:**
- Modify: `internal/export/export.go` — extract the loop into a function
- Create: `internal/export/export_test.go` — test the extracted function

- [ ] Extract `buildScoreboardEntries(rows []db.AccountabilityRow) []ScoreboardEntry` as a pure function
- [ ] `exportScoreboard` calls it and writes the result
- [ ] Add unit test for the extracted function with at least 3 cases (zero trades, perfect record, worst case)
- [ ] Commit

---

### Task 13: Name magic number constants

**Why:** 7 legislative/behavioral thresholds appear as inline literals across 3 packages with no named constants and no documentation of their legislative basis.

**Files:**
- Modify: `internal/db/compliance.go` — extract constants for `45` (STOCK Act deadline), `60` (round-trip window), `14` (pre-event window)
- Modify: `internal/db/graph.go` — extract constant for `24` (co-trader month window)
- Modify: `internal/score/accountability.go` — extract constants for `20`/`0.5` (latency floor, committee saturation)

- [ ] Define named constants at the top of each file (e.g., `const StockActDeadlineDays = 45`)
- [ ] Replace inline literals with the constants
- [ ] Add brief comments explaining the legislative basis where applicable
- [ ] Verify tests still pass: `go test ./internal/db/... ./internal/score/...`
- [ ] Commit

---

### Task 14: Add `AccountabilityInputs` integration test

**Why:** The 6-CTE query is the most complex in the codebase and has zero test coverage. The DB package already has test patterns to follow.

**Files:**
- Create or modify: `internal/db/compliance_test.go`

- [ ] Use the existing test-DB helper (`testutil_test.go` or the in-memory DB pattern from `graph_test.go`)
- [ ] Insert test data: persons, congressional_trades with known dates, a person_committee, a company, an event
- [ ] Test cases:
  - Person with 0 late trades scores `late_pct = 0`
  - Person with a buy+sell within 60 days gets `round_trip_count = 1`
  - Person with a trade 10 days before an event gets `pre_event_count = 1`
  - Person below `minTrades` threshold is excluded
- [ ] Commit

---

### Task 15: D3 zoom/pan controls

**Why:** With 50+ nodes, the force graph becomes an unreadable cluster. No viewport controls exist.

**Files:**
- Modify: `web/static/index.html`

- [ ] In `renderNetwork()`, add `d3.zoom()` behavior to the SVG:
  ```js
  const zoom = d3.zoom().scaleExtent([0.3, 3]).on('zoom', (e) => {
    svg.selectAll('g').attr('transform', e.transform);
  });
  svg.call(zoom);
  ```
  Note: the link and node groups need to be wrapped in a single parent `<g>` for zoom to work on both.
- [ ] Add a hint below the SVG: `<p class="text-xs text-neutral-500 mt-2">Drag nodes to reposition. Scroll to zoom.</p>`
- [ ] Commit

---

### Task 16: Evaluate frontend component extraction

**Why:** `index.html` is at 3,548 lines and growing. Not blocking, but approaching maintainability limits.

**No code changes.** Decision needed:
- Option A: Accept monolith, add `<!-- SECTION: ... -->` comment structure
- Option B: Minimal build step (esbuild) to split into components
- Option C: Use Alpine.js `x-component` or template includes

Document the decision as an ADR if proceeding with B or C.

---

## Summary

| Priority | Tasks | Est. effort |
|----------|-------|-------------|
| Fix Now (P1) | Tasks 1-6 | ~30 min |
| Fix Soon (P2) | Tasks 7-11 | ~60 min |
| Tech Debt (P3) | Tasks 12-16 | ~90 min |

All P1 tasks are one-line to ten-line fixes. P2 tasks are more involved but well-scoped. P3 tasks are refactoring/architecture work that can be deferred.
