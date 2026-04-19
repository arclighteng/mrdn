# Data Layer Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix data pipeline gaps (EFDS trades, SEC litigation, tariff routing), improve company match rates via aliases, and harden operations.

**Architecture:** Extend the existing resolver switch in `internal/resolver/resolver.go` with three new source handlers. Add alias-based company lookup as a 4th tier in the resolver chain. New SEC litigation parser follows the existing parser pattern (struct + Poll + Parse + test). CLI command for alias generation.

**Tech Stack:** Go 1.22, SQLite (modernc), Cloudflare D1, testify

**Spec:** `docs/superpowers/specs/2026-04-19-data-layer-phase2-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `internal/resolver/resolver.go` | isDuplicateError fix, interface expansion, alias lookup, 3 new resolvers |
| Modify | `internal/resolver/resolver_test.go` | Mock expansion, new resolver tests, isDuplicateError SQLite test |
| Create | `internal/resolver/fixtures_test.go` | Cross-package parser→resolver fixture tests |
| Create | `internal/parser/sec_litigation.go` | SEC EDGAR litigation releases parser |
| Create | `internal/parser/sec_litigation_test.go` | Parser tests |
| Modify | `internal/ingestion/supervisor.go` | Register sec_edgar_lit source |
| Modify | `internal/db/migrations/001_sqlite_initial.sql` | Seed source_meta for sec_edgar_lit |
| Create | `internal/cli/generate_aliases.go` | CLI command to seed entity_aliases |
| Modify | `internal/cli/ingest_once.go` | Silent-drop counters in summary |

---

### Task 1: Fix isDuplicateError for SQLite

**Files:**
- Modify: `internal/resolver/resolver.go:707-714`
- Modify: `internal/resolver/resolver_test.go:942-947`

- [ ] **Step 1: Write the failing test**

Add SQLite error pattern to `TestIsDuplicateError` in `resolver_test.go`:

```go
// In TestIsDuplicateError, add:
assert.True(t, isDuplicateError(errors.New("UNIQUE constraint failed: events.source, events.source_id")))
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/resolver/ -run TestIsDuplicateError -v`
Expected: FAIL — `isDuplicateError` returns false for the SQLite pattern.

- [ ] **Step 3: Fix isDuplicateError**

In `resolver.go`, replace lines 707-714:

```go
// isDuplicateError checks if the error is a unique constraint violation
// (works for both PostgreSQL and SQLite).
func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "23505") ||
		strings.Contains(msg, "UNIQUE constraint failed")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/resolver/ -run TestIsDuplicateError -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/resolver/resolver.go internal/resolver/resolver_test.go
git commit -m "fix: isDuplicateError now detects SQLite UNIQUE constraint errors"
```

---

### Task 2: Expand ResolverStore Interface + Mocks

**Files:**
- Modify: `internal/resolver/resolver.go:18-31`
- Modify: `internal/resolver/resolver_test.go:21-148` (mockStore) and `1007-1053` (cancellingStore)

- [ ] **Step 1: Add methods to ResolverStore interface**

In `resolver.go`, add these five methods to the `ResolverStore` interface (after line 30, before the closing `}`):

```go
	InsertCongressionalTrade(ctx context.Context, t db.CongressionalTrade) error
	InsertCourtFiling(ctx context.Context, cf db.CourtFiling) error
	InsertTariff(ctx context.Context, t db.Tariff) error
	GetCompanyByAlias(ctx context.Context, alias string) (db.CompanyLookup, error)
	GetPersonBySlug(ctx context.Context, slug string) (db.Person, error)
```

- [ ] **Step 2: Add fields and methods to mockStore**

In `resolver_test.go`, add fields to `mockStore` struct (after `insertedWarnFilings`):

```go
	insertedCongTrades  []db.CongressionalTrade
	insertedCourtFilings []db.CourtFiling
	insertedTariffs     []db.Tariff

	aliasByName map[string]db.CompanyLookup // for GetCompanyByAlias
	aliasErr    error

	personBySlug map[string]db.Person // for GetPersonBySlug
	personErr    error
```

Add the five methods to `mockStore`:

```go
func (m *mockStore) InsertCongressionalTrade(_ context.Context, t db.CongressionalTrade) error {
	m.mu.Lock()
	m.insertedCongTrades = append(m.insertedCongTrades, t)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) InsertCourtFiling(_ context.Context, cf db.CourtFiling) error {
	m.mu.Lock()
	m.insertedCourtFilings = append(m.insertedCourtFilings, cf)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) InsertTariff(_ context.Context, t db.Tariff) error {
	m.mu.Lock()
	m.insertedTariffs = append(m.insertedTariffs, t)
	m.mu.Unlock()
	return nil
}

func (m *mockStore) GetCompanyByAlias(_ context.Context, alias string) (db.CompanyLookup, error) {
	if m.aliasErr != nil {
		return db.CompanyLookup{}, m.aliasErr
	}
	if m.aliasByName != nil {
		if c, ok := m.aliasByName[strings.ToLower(alias)]; ok {
			return c, nil
		}
	}
	return db.CompanyLookup{}, fmt.Errorf("getting company by alias %q: %w", alias, sql.ErrNoRows)
}

func (m *mockStore) GetPersonBySlug(_ context.Context, slug string) (db.Person, error) {
	if m.personErr != nil {
		return db.Person{}, m.personErr
	}
	if m.personBySlug != nil {
		if p, ok := m.personBySlug[slug]; ok {
			return p, nil
		}
	}
	return db.Person{}, fmt.Errorf("getting person %s: %w", slug, sql.ErrNoRows)
}
```

- [ ] **Step 3: Add methods to cancellingStore**

Add the five methods to `cancellingStore` (delegate to inner):

```go
func (c *cancellingStore) InsertCongressionalTrade(ctx context.Context, t db.CongressionalTrade) error {
	return c.inner.InsertCongressionalTrade(ctx, t)
}
func (c *cancellingStore) InsertCourtFiling(ctx context.Context, cf db.CourtFiling) error {
	return c.inner.InsertCourtFiling(ctx, cf)
}
func (c *cancellingStore) InsertTariff(ctx context.Context, t db.Tariff) error {
	return c.inner.InsertTariff(ctx, t)
}
func (c *cancellingStore) GetCompanyByAlias(ctx context.Context, alias string) (db.CompanyLookup, error) {
	return c.inner.GetCompanyByAlias(ctx, alias)
}
func (c *cancellingStore) GetPersonBySlug(ctx context.Context, slug string) (db.Person, error) {
	return c.inner.GetPersonBySlug(ctx, slug)
}
```

- [ ] **Step 4: Add imports**

Add `"database/sql"` to the import block in `resolver_test.go` (needed for `sql.ErrNoRows`).

- [ ] **Step 5: Verify compilation**

Run: `go build ./internal/resolver/`
Expected: compiles with no errors. The compile-time guard `var _ ResolverStore = (*db.Store)(nil)` at line 953 validates that `*db.Store` still satisfies the interface.

- [ ] **Step 6: Run all resolver tests**

Run: `go test ./internal/resolver/ -v`
Expected: all existing tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/resolver/resolver.go internal/resolver/resolver_test.go
git commit -m "feat: expand ResolverStore with trade, filing, tariff, alias, and person methods"
```

---

### Task 3: Alias-Based Company Lookup in Resolver

**Files:**
- Modify: `internal/resolver/resolver.go` (add `lookupByAlias` method, integrate into resolve flows)
- Modify: `internal/resolver/resolver_test.go` (alias lookup tests)

- [ ] **Step 1: Write the failing test**

```go
func TestResolveEdgar_AliasHit(t *testing.T) {
	// Cache and SearchCompanyByName both miss, but alias table has a match.
	st := &mockStore{
		companies:    []db.CompanyLookup{}, // empty cache
		searchResult: nil,
		searchErr:    errors.New("not found"),
		aliasByName: map[string]db.CompanyLookup{
			"meta platforms": {ID: 50, Ticker: "META", Name: "Meta Platforms Inc"},
		},
	}
	r := newTestResolver(t, st)

	data := mustMarshal(edgarFiling{
		DisplayNames: []string{"Person", "Meta Platforms"},
	})
	cid, err := r.resolveEdgar(context.Background(), makeEvent("edgar_form4", 10, data))

	require.NoError(t, err)
	assert.Equal(t, 50, cid)
	// After alias hit, subsequent lookups should use cache.
	assert.Equal(t, 50, r.lookupName("meta platforms"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/resolver/ -run TestResolveEdgar_AliasHit -v`
Expected: FAIL — `cid` is 0 because alias lookup doesn't exist yet.

- [ ] **Step 3: Add lookupByAlias method**

In `resolver.go`, add after `lookupName`:

```go
// lookupByAlias queries the entity_aliases table for a case-insensitive match.
// On hit, caches the result in byNameLower. Returns 0 if no alias matches.
func (r *Resolver) lookupByAlias(ctx context.Context, name string) int {
	c, err := r.store.GetCompanyByAlias(ctx, name)
	if err != nil {
		return 0
	}
	// Cache for future lookups.
	r.mu.Lock()
	r.byNameLower[strings.ToLower(name)] = c.ID
	r.mu.Unlock()
	return c.ID
}
```

- [ ] **Step 4: Integrate alias lookup into resolveEdgar**

In `resolveEdgar`, after the `SearchCompanyByName` block (around line 283), add:

```go
		if companyID == 0 {
			companyID = r.lookupByAlias(ctx, companyName)
		}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/resolver/ -run TestResolveEdgar_AliasHit -v`
Expected: PASS

- [ ] **Step 6: Add alias fallback to other resolvers**

Apply the same pattern to `resolveFEC` (after SearchCompanyByName, before the `if companyID == 0 { return 0, nil }` check), `resolveOFAC` (Entity branch, after SearchCompanyByName), and `resolveUSASpending`:

```go
	if companyID == 0 {
		companyID = r.lookupByAlias(ctx, <nameVar>)
	}
```

Where `<nameVar>` is `employer` (FEC), `entityName` (OFAC), `recipient` (USASpending).

- [ ] **Step 7: Run all resolver tests**

Run: `go test ./internal/resolver/ -v`
Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/resolver/resolver.go internal/resolver/resolver_test.go
git commit -m "feat: add alias-based company lookup as 4th tier in resolver chain"
```

---

### Task 4: EFDS Senate Trade Resolver

**Files:**
- Modify: `internal/resolver/resolver.go` (add `resolveEFDSTrades`, wire into switch)
- Modify: `internal/resolver/resolver_test.go` (tests)

- [ ] **Step 1: Write the test**

```go
func TestResolveEFDSTrades(t *testing.T) {
	t.Run("valid filing with trades — inserts congressional_trades", func(t *testing.T) {
		pelosiSlug := "nancy-pelosi"
		st := &mockStore{
			companies: []db.CompanyLookup{
				{ID: 7, Ticker: "AAPL", Name: "Apple Inc"},
				{ID: 8, Ticker: "MSFT", Name: "Microsoft Corp"},
			},
			personBySlug: map[string]db.Person{
				pelosiSlug: {ID: 1, Slug: pelosiSlug, Name: "Nancy Pelosi"},
			},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"first_name":  "Nancy",
			"last_name":   "Pelosi",
			"filing_type": "Periodic Transaction Report",
			"filing_date": "01/15/2025",
			"report_id":   "abc123",
			"transactions": []map[string]any{
				{
					"ticker":     "AAPL",
					"trade_type": "Purchase",
					"amount_low": 1001,
					"amount_high": 15000,
					"owner":      "SP",
					"traded_at":  "2025-01-10",
				},
				{
					"ticker":     "MSFT",
					"trade_type": "Sale (Full)",
					"amount_low": 15001,
					"amount_high": 50000,
					"owner":      "JT",
					"traded_at":  "2025-01-12",
				},
			},
		})
		cid, err := r.resolveEFDSTrades(context.Background(), makeEvent("efds_senate", 100, data))

		require.NoError(t, err)
		assert.Equal(t, 7, cid) // first resolved company
		require.Len(t, st.insertedCongTrades, 2)

		trade1 := st.insertedCongTrades[0]
		assert.Equal(t, 100, *trade1.EventID)
		assert.Equal(t, 1, *trade1.PersonID)
		assert.Equal(t, 7, *trade1.CompanyID)
		assert.Equal(t, "AAPL", *trade1.Ticker)
		assert.Equal(t, "Purchase", *trade1.TradeType)
		assert.Equal(t, 1001, *trade1.AmountRangeLow)
		assert.Equal(t, 15000, *trade1.AmountRangeHigh)
	})

	t.Run("no transactions field — returns 0", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"first_name":  "Unknown",
			"last_name":   "Senator",
			"filing_type": "Annual Report",
			"report_id":   "xyz",
		})
		cid, err := r.resolveEFDSTrades(context.Background(), makeEvent("efds_senate", 101, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		assert.Empty(t, st.insertedCongTrades)
	})

	t.Run("person not found — still inserts trades without person_id", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{
				{ID: 7, Ticker: "AAPL", Name: "Apple Inc"},
			},
			personBySlug: map[string]db.Person{}, // empty — no match
		}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"first_name": "Unknown",
			"last_name":  "NewSenator",
			"transactions": []map[string]any{
				{"ticker": "AAPL", "trade_type": "Purchase", "amount_low": 1001, "amount_high": 15000},
			},
		})
		cid, err := r.resolveEFDSTrades(context.Background(), makeEvent("efds_senate", 102, data))

		require.NoError(t, err)
		assert.Equal(t, 7, cid)
		require.Len(t, st.insertedCongTrades, 1)
		assert.Nil(t, st.insertedCongTrades[0].PersonID) // no person link
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/resolver/ -run TestResolveEFDSTrades -v`
Expected: FAIL — `resolveEFDSTrades` does not exist.

- [ ] **Step 3: Implement resolveEFDSTrades**

In `resolver.go`, add after `resolveWarn`:

```go
// efdsDisclosure mirrors the JSON stored by the EFDS parser plus the
// transactions array that the updated parser will include.
type efdsDisclosure struct {
	FirstName    string            `json:"first_name"`
	LastName     string            `json:"last_name"`
	FilingType   string            `json:"filing_type"`
	FilingDate   string            `json:"filing_date"`
	ReportID     string            `json:"report_id"`
	Transactions []efdsTransaction `json:"transactions"`
}

type efdsTransaction struct {
	Ticker    string `json:"ticker"`
	TradeType string `json:"trade_type"`
	AmountLow  int   `json:"amount_low"`
	AmountHigh int   `json:"amount_high"`
	Owner     string `json:"owner"`
	TradedAt  string `json:"traded_at"`
}

func (r *Resolver) resolveEFDSTrades(ctx context.Context, evt db.Event) (int, error) {
	var disc efdsDisclosure
	if err := json.Unmarshal(evt.EventData, &disc); err != nil {
		return 0, fmt.Errorf("unmarshal efds disclosure: %w", err)
	}

	if len(disc.Transactions) == 0 {
		return 0, nil
	}

	// Resolve person by slug (first-last, lowered, hyphenated).
	slug := strings.ToLower(strings.TrimSpace(disc.FirstName) + "-" + strings.TrimSpace(disc.LastName))
	slug = strings.ReplaceAll(slug, " ", "-")
	var personID *int
	if p, err := r.store.GetPersonBySlug(ctx, slug); err == nil {
		personID = &p.ID
	}

	// Parse filing date for filed_at.
	var filedAt *time.Time
	if disc.FilingDate != "" {
		if t, err := time.Parse("01/02/2006", disc.FilingDate); err == nil {
			ft := t.UTC()
			filedAt = &ft
		}
	}

	var firstCompanyID int
	eventID := evt.ID
	for _, tx := range disc.Transactions {
		ticker := strings.ToUpper(strings.TrimSpace(tx.Ticker))
		if ticker == "" || ticker == "--" || ticker == "N/A" {
			continue
		}

		companyID, err := r.ensureCompany(ctx, ticker, "")
		if err != nil {
			log.Printf("[resolver] efds trade ticker %s: %v", ticker, err)
			continue
		}
		if firstCompanyID == 0 && companyID > 0 {
			firstCompanyID = companyID
		}

		var companyIDPtr *int
		if companyID > 0 {
			companyIDPtr = &companyID
		}

		var tradedAt *time.Time
		if tx.TradedAt != "" {
			if t, err := time.Parse("2006-01-02", tx.TradedAt); err == nil {
				tt := t.UTC()
				tradedAt = &tt
			}
		}

		trade := db.CongressionalTrade{
			EventID:         &eventID,
			PersonID:        personID,
			CompanyID:       companyIDPtr,
			OwnerType:       strPtr(tx.Owner),
			Ticker:          strPtr(ticker),
			TradeType:       strPtr(tx.TradeType),
			AmountRangeLow:  intPtr(tx.AmountLow),
			AmountRangeHigh: intPtr(tx.AmountHigh),
			FiledAt:         filedAt,
			TradedAt:        tradedAt,
		}

		if err := r.store.InsertCongressionalTrade(ctx, trade); err != nil {
			if !isDuplicateError(err) {
				log.Printf("[resolver] efds congressional_trade insert: %v", err)
			}
		}
	}

	return firstCompanyID, nil
}
```

Also add the `intPtr` helper (note: unlike `strPtr`, zero is a valid integer value so we never return nil for it):

```go
func intPtr(n int) *int {
	return &n
}
```

- [ ] **Step 4: Wire into the switch**

Replace the `efds_senate` case in `Resolve()`:

```go
	case "efds_senate":
		companyID, err = r.resolveEFDSTrades(ctx, evt)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/resolver/ -run TestResolveEFDSTrades -v`
Expected: PASS

- [ ] **Step 6: Update dispatch test**

In `TestResolve_Dispatch`, update the `efds_senate` test case — it should no longer return 0 for events with transactions. Add a separate test for efds_senate with no transactions confirming 0 return.

- [ ] **Step 7: Run all resolver tests**

Run: `go test ./internal/resolver/ -v`
Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/resolver/resolver.go internal/resolver/resolver_test.go
git commit -m "feat: resolve EFDS Senate filings into congressional_trades"
```

---

### Task 5: SEC Litigation Parser

**Files:**
- Create: `internal/parser/sec_litigation.go`
- Create: `internal/parser/sec_litigation_test.go`

- [ ] **Step 1: Write the test**

Create `internal/parser/sec_litigation_test.go`:

```go
package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSECLitigation(t *testing.T) {
	// Minimal RSS-like JSON response with two items.
	raw := []byte(`{
		"releases": [
			{
				"id": "LR-25832",
				"date": "2025-04-01",
				"title": "SEC Charges Acme Corp for Securities Fraud",
				"url": "https://www.sec.gov/litigation/litreleases/2025/lr25832.htm"
			},
			{
				"id": "LR-25833",
				"date": "2025-04-02",
				"title": "SEC Files Action Against John Doe and Widget Inc.",
				"url": "https://www.sec.gov/litigation/litreleases/2025/lr25833.htm"
			}
		]
	}`)

	events, err := ParseSECLitigation(raw)
	require.NoError(t, err)
	require.Len(t, events, 2)

	evt := events[0]
	assert.Equal(t, "sec_edgar_lit", evt.Source)
	assert.Equal(t, "sec_litigation", evt.EventType)
	assert.NotNil(t, evt.SourceID)
}

func TestParseSECLitigation_EmptyReleases(t *testing.T) {
	raw := []byte(`{"releases": []}`)
	events, err := ParseSECLitigation(raw)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseSECLitigation_InvalidJSON(t *testing.T) {
	_, err := ParseSECLitigation([]byte(`not json`))
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/parser/ -run TestParseSECLitigation -v`
Expected: FAIL — `ParseSECLitigation` does not exist.

- [ ] **Step 3: Implement the parser**

Create `internal/parser/sec_litigation.go`:

```go
package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	secLitSourceName = "sec_edgar_lit"
	// secLitBaseURL is the SEC litigation releases endpoint.
	// TODO(PHASE2): Validate this URL against the actual SEC EDGAR API response shape
	// during implementation. The JSON envelope shape ({releases: [...]}) is a placeholder —
	// adjust secLitResponse struct to match the real response.
	// Hardcoded to prevent SSRF; no API key required (public data).
	secLitBaseURL = "https://efts.sec.gov/LATEST/search-index?q=%22litigation+release%22&dateRange=custom&startdt=2024-01-01&enddt=2099-12-31&forms=LR"
)

// SECLitigationSource polls SEC EDGAR for litigation releases.
type SECLitigationSource struct {
	client *http.Client
}

// NewSECLitigationSource returns a SECLitigationSource using the provided HTTP client.
func NewSECLitigationSource(client *http.Client) *SECLitigationSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &SECLitigationSource{client: client}
}

// Name implements ingestion.Source.
func (s *SECLitigationSource) Name() string { return secLitSourceName }

// Poll fetches recent SEC litigation releases.
func (s *SECLitigationSource) Poll(ctx context.Context) ([]db.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secLitBaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("sec_lit: building request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sec_lit: executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{Source: "sec_lit", StatusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("sec_lit: reading response body: %w", err)
	}

	return ParseSECLitigation(body)
}

// secLitResponse is the parsed JSON envelope.
type secLitResponse struct {
	Releases []json.RawMessage `json:"releases"`
}

// secLitRelease extracts metadata fields needed for the event.
type secLitRelease struct {
	ID    string `json:"id"`
	Date  string `json:"date"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// ParseSECLitigation parses raw SEC litigation JSON and returns one db.Event per release.
func ParseSECLitigation(data []byte) ([]db.Event, error) {
	var resp secLitResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("sec_lit: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(resp.Releases))
	for i, raw := range resp.Releases {
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("sec_lit: release[%d]: %w", i, err)
		}

		var rel secLitRelease
		if err := json.Unmarshal(raw, &rel); err != nil {
			return nil, fmt.Errorf("sec_lit: decoding release[%d]: %w", i, err)
		}

		// Use the release ID as source_id if available, else hash title+date.
		sid := rel.ID
		if sid == "" {
			sid = rel.Title + "|" + rel.Date
		}

		occurredAt := time.Now().UTC()
		if rel.Date != "" {
			if t, err := time.Parse("2006-01-02", rel.Date); err == nil {
				occurredAt = t.UTC()
			}
		}

		events = append(events, db.Event{
			Source:     secLitSourceName,
			SourceID:   sourceID(secLitSourceName, sid),
			EventType:  "sec_litigation",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/parser/ -run TestParseSECLitigation -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/parser/sec_litigation.go internal/parser/sec_litigation_test.go
git commit -m "feat: add SEC EDGAR litigation releases parser"
```

---

### Task 6: SEC Litigation Resolver

**Files:**
- Modify: `internal/resolver/resolver.go` (add `resolveSecLitigation`, wire into switch)
- Modify: `internal/resolver/resolver_test.go`

- [ ] **Step 1: Write the test**

```go
func TestResolveSecLitigation(t *testing.T) {
	t.Run("release with company match — inserts court_filing", func(t *testing.T) {
		st := &mockStore{
			companies: []db.CompanyLookup{
				{ID: 55, Ticker: "ACME", Name: "Acme Corp"},
			},
		}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"id":    "LR-25832",
			"date":  "2025-04-01",
			"title": "SEC Charges Acme Corp for Securities Fraud",
			"url":   "https://www.sec.gov/litigation/litreleases/2025/lr25832.htm",
		})
		cid, err := r.resolveSecLitigation(context.Background(), makeEvent("sec_edgar_lit", 200, data))

		require.NoError(t, err)
		assert.Equal(t, 55, cid)
		require.Len(t, st.insertedCourtFilings, 1)
		cf := st.insertedCourtFilings[0]
		assert.Equal(t, 200, *cf.EventID)
		assert.Equal(t, 55, *cf.CompanyID)
		assert.Equal(t, "LR-25832", *cf.CaseNumber)
		assert.Equal(t, "sec_litigation", *cf.FilingType)
		assert.Contains(t, cf.Parties, "Acme Corp")
	})

	t.Run("no company match — inserts filing without company_id", func(t *testing.T) {
		st := &mockStore{
			companies:    []db.CompanyLookup{},
			searchResult: nil,
			searchErr:    errors.New("not found"),
		}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"id":    "LR-99999",
			"title": "SEC Files Action Against Unknown Person",
		})
		cid, err := r.resolveSecLitigation(context.Background(), makeEvent("sec_edgar_lit", 201, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		require.Len(t, st.insertedCourtFilings, 1)
		assert.Nil(t, st.insertedCourtFilings[0].CompanyID)
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/resolver/ -run TestResolveSecLitigation -v`
Expected: FAIL

- [ ] **Step 3: Implement resolveSecLitigation**

```go
// secLitEvent mirrors the JSON stored by the SEC litigation parser.
type secLitEvent struct {
	ID    string `json:"id"`
	Date  string `json:"date"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func (r *Resolver) resolveSecLitigation(ctx context.Context, evt db.Event) (int, error) {
	var rel secLitEvent
	if err := json.Unmarshal(evt.EventData, &rel); err != nil {
		return 0, fmt.Errorf("unmarshal sec litigation: %w", err)
	}

	// Extract potential company names from the title.
	// SEC titles follow patterns like "SEC Charges [Company] for ..."
	// or "SEC Files Action Against [Name] and [Company]".
	parties := extractParties(rel.Title)

	var companyID int
	for _, party := range parties {
		cid := r.lookupName(party)
		if cid == 0 {
			c, err := r.store.SearchCompanyByName(ctx, party)
			if err == nil && c != nil {
				cid = c.ID
				r.mu.Lock()
				r.byNameLower[strings.ToLower(party)] = cid
				r.mu.Unlock()
			}
		}
		if cid == 0 {
			cid = r.lookupByAlias(ctx, party)
		}
		if cid > 0 {
			companyID = cid
			break
		}
	}

	var companyIDPtr *int
	if companyID > 0 {
		companyIDPtr = &companyID
	}

	var filedAt *time.Time
	if rel.Date != "" {
		if t, err := time.Parse("2006-01-02", rel.Date); err == nil {
			ft := t.UTC()
			filedAt = &ft
		}
	}

	eventID := evt.ID
	if err := r.store.InsertCourtFiling(ctx, db.CourtFiling{
		EventID:    &eventID,
		CompanyID:  companyIDPtr,
		CaseNumber: strPtr(rel.ID),
		Court:      strPtr("SEC"),
		FilingType: strPtr("sec_litigation"),
		Parties:    parties,
		FiledAt:    filedAt,
	}); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] sec litigation court_filing insert: %v", err)
		}
	}

	return companyID, nil
}

// extractParties pulls potential entity names from an SEC title.
// Looks for patterns like "SEC Charges X" or "SEC Files Action Against X and Y".
func extractParties(title string) []string {
	// Remove the "SEC " prefix and common verbs.
	title = strings.TrimPrefix(title, "SEC ")
	for _, verb := range []string{
		"Charges ", "Files Action Against ", "Obtains ", "Announces ",
		"Settles With ", "Orders ", "Sues ",
	} {
		if strings.HasPrefix(title, verb) {
			title = strings.TrimPrefix(title, verb)
			break
		}
	}

	// Split on " for ", " with ", " in " to get the entity portion.
	for _, sep := range []string{" for ", " with ", " in ", " Related to "} {
		if idx := strings.Index(title, sep); idx > 0 {
			title = title[:idx]
			break
		}
	}

	// Split on " and " to separate multiple parties.
	parts := strings.Split(title, " and ")
	parties := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			parties = append(parties, p)
		}
	}
	return parties
}
```

- [ ] **Step 4: Wire into the switch**

In `Resolve()`, add before the `default` case:

```go
	case "sec_edgar_lit":
		companyID, err = r.resolveSecLitigation(ctx, evt)
```

- [ ] **Step 5: Run test**

Run: `go test ./internal/resolver/ -run TestResolveSecLitigation -v`
Expected: PASS

- [ ] **Step 6: Run all resolver tests**

Run: `go test ./internal/resolver/ -v`
Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/resolver/resolver.go internal/resolver/resolver_test.go
git commit -m "feat: resolve SEC litigation releases into court_filings"
```

---

### Task 7: Federal Register Tariff Resolver

**Files:**
- Modify: `internal/resolver/resolver.go` (add `resolveFedRegTariff`, wire into switch)
- Modify: `internal/resolver/resolver_test.go`

- [ ] **Step 1: Write the test**

```go
func TestResolveFedRegTariff(t *testing.T) {
	t.Run("tariff-relevant rule — inserts tariff", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"document_number":  "2025-00123",
			"type":             "Rule",
			"title":            "Increase in Duties on Steel Products From China",
			"publication_date": "2025-03-15",
			"effective_on":     "2025-04-01",
			"cfr_references": []map[string]any{
				{"title": 19, "part": 134},
			},
		})
		cid, err := r.resolveFedRegTariff(context.Background(), makeEvent("federal_register", 300, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid) // tariffs don't link to companies
		require.Len(t, st.insertedTariffs, 1)
		tf := st.insertedTariffs[0]
		assert.Equal(t, 300, *tf.EventID)
		assert.Contains(t, *tf.ActionType, "Increase in Duties")
	})

	t.Run("non-tariff rule — returns 0, no insert", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"document_number":  "2025-00456",
			"type":             "Rule",
			"title":            "Air Quality Standards Update",
			"publication_date": "2025-03-15",
			"cfr_references": []map[string]any{
				{"title": 40, "part": 50}, // EPA, not customs
			},
		})
		cid, err := r.resolveFedRegTariff(context.Background(), makeEvent("federal_register", 301, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		assert.Empty(t, st.insertedTariffs)
	})

	t.Run("proposed rule with title 19 CFR — inserts tariff", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"document_number": "2025-00789",
			"type":            "Proposed Rule",
			"title":           "Proposed Modification of Tariff Rate Quota",
			"effective_on":    "2025-06-01",
			"cfr_references": []map[string]any{
				{"title": 19, "part": 12},
			},
		})
		cid, err := r.resolveFedRegTariff(context.Background(), makeEvent("federal_register", 302, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		require.Len(t, st.insertedTariffs, 1)
	})

	t.Run("no type field — returns 0", func(t *testing.T) {
		st := &mockStore{}
		r := newTestResolver(t, st)

		data := mustMarshal(map[string]any{
			"document_number": "2025-00999",
			"title":           "Some Document",
		})
		cid, err := r.resolveFedRegTariff(context.Background(), makeEvent("federal_register", 303, data))

		require.NoError(t, err)
		assert.Equal(t, 0, cid)
		assert.Empty(t, st.insertedTariffs)
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/resolver/ -run TestResolveFedRegTariff -v`
Expected: FAIL

- [ ] **Step 3: Implement resolveFedRegTariff**

```go
// fedRegDoc mirrors the JSON stored by the Federal Register parser.
// The parser stores the full raw API document — we decode only the fields
// needed for tariff classification.
type fedRegDoc struct {
	Type            string          `json:"type"`
	Title           string          `json:"title"`
	EffectiveOn     string          `json:"effective_on"`
	CFRReferences   []cfrReference  `json:"cfr_references"`
}

type cfrReference struct {
	Title int `json:"title"`
	Part  int `json:"part"`
}

// tariffCFRParts are Title 19 (Customs Duties) parts that indicate trade/tariff actions.
var tariffCFRParts = map[int]bool{
	12: true, 134: true, 159: true, 163: true,
}

func (r *Resolver) resolveFedRegTariff(ctx context.Context, evt db.Event) (int, error) {
	var doc fedRegDoc
	if err := json.Unmarshal(evt.EventData, &doc); err != nil {
		return 0, fmt.Errorf("unmarshal federal register doc: %w", err)
	}

	// Only process Rules and Proposed Rules.
	if doc.Type != "Rule" && doc.Type != "Proposed Rule" {
		return 0, nil
	}

	// Check if any CFR reference is Title 19 + a tariff-relevant part.
	isTariff := false
	for _, ref := range doc.CFRReferences {
		if ref.Title == 19 && tariffCFRParts[ref.Part] {
			isTariff = true
			break
		}
	}
	if !isTariff {
		return 0, nil
	}

	var effectiveAt *time.Time
	if doc.EffectiveOn != "" {
		if t, err := time.Parse("2006-01-02", doc.EffectiveOn); err == nil {
			et := t.UTC()
			effectiveAt = &et
		}
	}

	eventID := evt.ID
	if err := r.store.InsertTariff(ctx, db.Tariff{
		EventID:     &eventID,
		ActionType:  strPtr(doc.Title),
		EffectiveAt: effectiveAt,
	}); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] federal_register tariff insert: %v", err)
		}
	}

	// Tariffs are not linked to individual companies.
	return 0, nil
}
```

- [ ] **Step 4: Wire into the switch**

Replace the `federal_register` case:

```go
	case "federal_register":
		companyID, err = r.resolveFedRegTariff(ctx, evt)
```

- [ ] **Step 5: Run test**

Run: `go test ./internal/resolver/ -run TestResolveFedRegTariff -v`
Expected: PASS

- [ ] **Step 6: Run all resolver tests**

Run: `go test ./internal/resolver/ -v`
Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/resolver/resolver.go internal/resolver/resolver_test.go
git commit -m "feat: route tariff-relevant Federal Register docs to tariffs table"
```

---

### Task 8: Register SEC Litigation Source + Migration Version 2

**Files:**
- Modify: `internal/ingestion/supervisor.go:69-76`
- Modify: `internal/db/migrate.go` (add version-2 migration block)
- Modify: `internal/db/migrations/001_sqlite_initial.sql` (add source_meta seed for new DBs)

- [ ] **Step 1: Add source to supervisor**

In `supervisor.go`, add to the `sources` slice in `RegisterSources()`:

```go
		parser.NewSECLitigationSource(client),
```

- [ ] **Step 2: Seed source_meta in schema for new databases**

In `001_sqlite_initial.sql`, add to the `INSERT OR IGNORE INTO source_meta` block:

```sql
    ('sec_edgar_lit', '1 day', 86400, 'healthy'),
```

- [ ] **Step 3: Add version-2 migration for existing databases**

In `migrate.go`, after the version-1 block (after `return nil` at line 28), add a version-2 migration:

```go
	// Version 2: seed sec_edgar_lit source_meta for existing databases.
	var v2Applied int
	d.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 2").Scan(&v2Applied)
	if v2Applied == 0 {
		if _, err := d.ExecContext(ctx, `
			INSERT OR IGNORE INTO source_meta (source_name, expected_lag, poll_interval_seconds, status)
			VALUES ('sec_edgar_lit', '1 day', 86400, 'healthy')
		`); err != nil {
			return fmt.Errorf("running v2 migration: %w", err)
		}
		if _, err := d.ExecContext(ctx,
			"INSERT OR IGNORE INTO schema_migrations (version) VALUES (2)",
		); err != nil {
			return fmt.Errorf("recording v2 migration: %w", err)
		}
	}
```

- [ ] **Step 4: Build and verify**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 5: Commit**

```bash
git add internal/ingestion/supervisor.go internal/db/migrate.go internal/db/migrations/001_sqlite_initial.sql
git commit -m "feat: register SEC litigation source, add v2 migration for source_meta"
```

---

### Task 9: Generate-Aliases CLI Command

**Files:**
- Create: `internal/cli/generate_aliases.go`

- [ ] **Step 1: Implement the command**

Create `internal/cli/generate_aliases.go`:

```go
package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/spf13/cobra"
)

var generateAliasesCmd = &cobra.Command{
	Use:   "generate-aliases",
	Short: "Seed entity_aliases from existing company data",
	Long: `Generates company aliases from the companies table:
- Ticker as alias for the company name
- Common abbreviations and DBA names
- Logs top unresolved event company names for manual review.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		d, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connecting to database: %w", err)
		}
		defer d.Close()

		store := db.NewStore(d)

		companies, err := store.ListAllCompanyLookups(ctx)
		if err != nil {
			return fmt.Errorf("listing companies: %w", err)
		}

		var inserted, skipped int
		for _, c := range companies {
			aliases := generateCompanyAliases(c)
			for _, alias := range aliases {
				result, err := store.InsertEntityAlias(ctx, db.EntityAlias{
					EntityID:   c.ID,
					EntityType: "company",
					Alias:      alias,
					Source:     strRef("generate-aliases"),
				})
				if err != nil {
					log.Printf("[generate-aliases] error inserting alias %q for %s: %v", alias, c.Ticker, err)
					continue
				}
				if result.ID == 0 {
					skipped++ // duplicate
				} else {
					inserted++
				}
			}
		}

		log.Printf("[generate-aliases] done — %d aliases inserted, %d duplicates skipped", inserted, skipped)
		return nil
	},
}

func strRef(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// generateCompanyAliases produces alias variants for a company.
func generateCompanyAliases(c db.CompanyLookup) []string {
	aliases := make([]string, 0, 4)

	name := c.Name
	ticker := c.Ticker

	// Ticker itself as an alias (e.g., "AAPL" → Apple Inc).
	aliases = append(aliases, ticker)

	// Name without common suffixes.
	suffixes := []string{
		" Inc.", " Inc", " Corporation", " Corp.", " Corp",
		" Limited", " Ltd.", " Ltd", " Company", " Co.",
		" Holdings", " Holding", " Group", " PLC", " SE", " SA", " NV",
		", Inc.", ", Inc", ", LLC", ", Ltd.",
	}
	stripped := name
	lowerName := strings.ToLower(name)
	for _, suf := range suffixes {
		lowerSuf := strings.ToLower(suf)
		if strings.HasSuffix(lowerName, lowerSuf) {
			stripped = name[:len(name)-len(suf)]
			break
		}
	}
	if stripped != name && stripped != ticker {
		aliases = append(aliases, stripped)
	}

	// Uppercase version of full name (for OFAC-style names).
	upper := strings.ToUpper(name)
	if upper != name {
		aliases = append(aliases, upper)
	}

	return aliases
}

func init() {
	rootCmd.AddCommand(generateAliasesCmd)
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/generate_aliases.go
git commit -m "feat: add generate-aliases CLI command to seed entity_aliases"
```

---

### Task 10: Cross-Package Fixture Tests

**Files:**
- Create: `internal/resolver/fixtures_test.go`

- [ ] **Step 1: Write fixture tests**

Create `internal/resolver/fixtures_test.go`:

```go
package resolver

import (
	"encoding/json"
	"testing"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFixture_EFDS verifies the parser→resolver JSON contract.
func TestFixture_EFDS(t *testing.T) {
	xml := []byte(`<filings><filing>
		<first_name>Nancy</first_name>
		<last_name>Pelosi</last_name>
		<filing_type>Periodic Transaction Report</filing_type>
		<filing_date>01/15/2025</filing_date>
		<report_id>abc123</report_id>
	</filing></filings>`)

	events, err := parser.ParseEFDS(xml)
	require.NoError(t, err)
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal(t, "efds_senate", evt.Source)
	assert.Equal(t, "congressional_disclosure", evt.EventType)

	// Verify the resolver can unmarshal the parser's output.
	var disc efdsDisclosure
	err = json.Unmarshal(evt.EventData, &disc)
	require.NoError(t, err)
	assert.Equal(t, "Nancy", disc.FirstName)
	assert.Equal(t, "Pelosi", disc.LastName)
}

// TestFixture_FedRegister verifies the parser→resolver JSON contract.
func TestFixture_FedRegister(t *testing.T) {
	raw := []byte(`{"results": [
		{
			"document_number": "2025-00123",
			"publication_date": "2025-03-15",
			"type": "Rule",
			"title": "Test Rule",
			"cfr_references": [{"title": 19, "part": 134}]
		}
	]}`)

	events, err := parser.ParseFedRegister(raw)
	require.NoError(t, err)
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal(t, "federal_register", evt.Source)
	assert.Equal(t, "regulatory_action", evt.EventType)

	// Verify the resolver can unmarshal the parser's output.
	var doc fedRegDoc
	err = json.Unmarshal(evt.EventData, &doc)
	require.NoError(t, err)
	assert.Equal(t, "Rule", doc.Type)
	assert.Equal(t, "Test Rule", doc.Title)
}

// TestFixture_SECLitigation verifies the parser→resolver JSON contract.
func TestFixture_SECLitigation(t *testing.T) {
	raw := []byte(`{"releases": [
		{
			"id": "LR-25832",
			"date": "2025-04-01",
			"title": "SEC Charges Acme Corp for Securities Fraud",
			"url": "https://www.sec.gov/lit/lr25832.htm"
		}
	]}`)

	events, err := parser.ParseSECLitigation(raw)
	require.NoError(t, err)
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal(t, "sec_edgar_lit", evt.Source)
	assert.Equal(t, "sec_litigation", evt.EventType)

	// Verify the resolver can unmarshal the parser's output.
	var rel secLitEvent
	err = json.Unmarshal(evt.EventData, &rel)
	require.NoError(t, err)
	assert.Equal(t, "LR-25832", rel.ID)
}
```

- [ ] **Step 2: Run fixture tests**

Run: `go test ./internal/resolver/ -run TestFixture -v`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/resolver/fixtures_test.go
git commit -m "test: add cross-package parser→resolver fixture tests"
```

---

### Task 11: Silent-Drop Counters

**Files:**
- Modify: `internal/cli/ingest_once.go` (log drop counters)

- [ ] **Step 1: Add resolution counters to ingest_once**

In `ingest_once.go`, add counters alongside `totalNew`. Before the source loop (around line 50), add:

```go
		var totalResolved, totalUnresolved int
```

Inside the per-event loop (line 82-86), capture the Resolve return value:

```go
				// Replace the existing res.Resolve(ctx, evt) call:
				cid := res.Resolve(ctx, evt)
				if cid > 0 {
					totalResolved++
				} else {
					totalUnresolved++
				}
```

After the source loop (line 99), add a log line:

```go
		log.Printf("[ingest-once] resolution: %d resolved, %d unresolved (of %d new)", totalResolved, totalUnresolved, totalNew)
```

- [ ] **Step 2: Build and verify**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/ingest_once.go
git commit -m "feat: add silent-drop counters to ingest-once summary"
```

---

### Task 12: Full Test Suite Verification

- [ ] **Step 1: Run all Go tests**

Run: `go test ./... -count=1`
Expected: all packages pass.

- [ ] **Step 2: Run MQL Worker tests**

Run: `cd workers/query && npm test`
Expected: all 50 tests pass.

- [ ] **Step 3: Final commit if any fixups needed**

```bash
git add -A
git commit -m "fix: test suite fixups for data layer phase 2"
```
