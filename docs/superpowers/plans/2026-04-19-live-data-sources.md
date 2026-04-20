# Live Government Financial Disclosure Sources — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace stale/broken congressional trade pipeline with live Finnhub data (House + Senate) and add judicial financial disclosures from CourtListener.

**Architecture:** Two new poll-based sources following the existing `Source` interface pattern. Each source produces `db.Event` records that the resolver extracts into `congressional_trades` rows. Finnhub congressional trades use the existing REST API with the already-configured API key. CourtListener uses token-based auth against their v4 REST API.

**Tech Stack:** Go, `net/http`, `encoding/json`, existing `internal/parser` + `internal/resolver` patterns.

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/parser/finnhub_congress.go` | HTTP source + pure parser for Finnhub congressional trading endpoint |
| `internal/parser/finnhub_congress_test.go` | Parser tests with fixture data |
| `internal/parser/courtlistener.go` | HTTP source + pure parser for CourtListener disclosures + investments |
| `internal/parser/courtlistener_test.go` | Parser tests with fixture data |
| `internal/parser/testdata/finnhub_congress_sample.json` | Test fixture |
| `internal/parser/testdata/courtlistener_disclosures_sample.json` | Test fixture |
| `internal/config/config.go` | Add `CourtListenerToken` field (lines 10-24, 62-72) |
| `internal/ingestion/supervisor.go` | Register both new sources (lines 66-87) |
| `internal/resolver/resolver.go` | Add `finnhub_congress` and `courtlistener` cases to source switch + resolver functions |

---

### Task 1: Finnhub Congressional Trading Parser

**Files:**
- Create: `internal/parser/finnhub_congress.go`
- Create: `internal/parser/finnhub_congress_test.go`
- Create: `internal/parser/testdata/finnhub_congress_sample.json`

**Context:** The Finnhub congressional trading endpoint is `GET /api/v1/stock/congressional-trading?symbol={SYM}&from={DATE}&to={DATE}&token={KEY}`. It returns trades for a specific symbol. Our source needs to iterate over tracked companies.

Current sources are pure HTTP fetchers with no DB access. This source needs ticker lists, so it takes a `TickerLister` interface (`ListTickers(ctx) ([]string, error)`). The supervisor provides the store as the implementation.

**Reference files:**
- `internal/parser/polygon.go` — pattern for source with API key
- `internal/parser/efds.go` — pattern for source + pure parser separation
- `internal/parser/parser.go:43-47` — `sourceID()` helper

- [ ] **Step 1: Create test fixture**

Create `internal/parser/testdata/finnhub_congress_sample.json`:
```json
{
  "data": [
    {
      "amountFrom": 1001,
      "amountTo": 15000,
      "assetName": "Apple Inc",
      "filingDate": "2025-03-15",
      "name": "Nancy Pelosi",
      "ownerType": "joint",
      "position": "Representative",
      "symbol": "AAPL",
      "transactionDate": "2025-03-01",
      "transactionType": "Purchase"
    },
    {
      "amountFrom": 15001,
      "amountTo": 50000,
      "assetName": "Microsoft Corp",
      "filingDate": "2025-03-15",
      "name": "Tommy Tuberville",
      "ownerType": "self",
      "position": "Senator",
      "symbol": "MSFT",
      "transactionDate": "2025-02-28",
      "transactionType": "Sale"
    }
  ],
  "symbol": "AAPL"
}
```

- [ ] **Step 2: Write failing parser test**

Create `internal/parser/finnhub_congress_test.go`:
```go
package parser

import (
	"os"
	"testing"
)

func TestParseFinnhubCongress(t *testing.T) {
	raw, err := os.ReadFile("testdata/finnhub_congress_sample.json")
	if err != nil {
		t.Fatal(err)
	}

	events, err := ParseFinnhubCongress(raw)
	if err != nil {
		t.Fatal(err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// First event: Pelosi purchase
	e0 := events[0]
	if e0.Source != "finnhub_congress" {
		t.Errorf("source = %q, want finnhub_congress", e0.Source)
	}
	if e0.EventType != "congressional_trade" {
		t.Errorf("event_type = %q, want congressional_trade", e0.EventType)
	}
	if e0.SourceID == nil {
		t.Fatal("source_id is nil")
	}
	if e0.OccurredAt.Format("2006-01-02") != "2025-03-01" {
		t.Errorf("occurred_at = %v, want 2025-03-01", e0.OccurredAt)
	}

	// Second event: Tuberville sale
	e1 := events[1]
	if e1.OccurredAt.Format("2006-01-02") != "2025-02-28" {
		t.Errorf("occurred_at = %v, want 2025-02-28", e1.OccurredAt)
	}
}

func TestParseFinnhubCongressEmpty(t *testing.T) {
	events, err := ParseFinnhubCongress([]byte(`{"data":[],"symbol":"AAPL"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestParseFinnhubCongressSkipsMissingDate(t *testing.T) {
	raw := []byte(`{"data":[{"name":"Test","symbol":"X","transactionDate":"","transactionType":"Purchase","amountFrom":100,"amountTo":500,"position":"Representative","filingDate":"2025-01-01","ownerType":"self","assetName":"Test Co"}],"symbol":"X"}`)
	events, err := ParseFinnhubCongress(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Should still produce an event, using filingDate as fallback
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd C:/Users/AR/Projects/mrdn && go test ./internal/parser/ -run TestParseFinnhubCongress -v`
Expected: FAIL — `ParseFinnhubCongress` undefined

- [ ] **Step 4: Implement parser and source**

Create `internal/parser/finnhub_congress.go`:
```go
package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	finnhubCongressSourceName = "finnhub_congress"
	finnhubCongressBaseURL    = "https://finnhub.io/api/v1/stock/congressional-trading"
)

// TickerLister provides the list of tickers to poll. Implemented by db.Store.
type TickerLister interface {
	ListTickers(ctx context.Context) ([]string, error)
}

// FinnhubCongressSource polls the Finnhub congressional trading REST endpoint
// for both House and Senate stock trades.
type FinnhubCongressSource struct {
	client  *http.Client
	apiKey  string
	tickers TickerLister
}

// NewFinnhubCongressSource returns a FinnhubCongressSource.
func NewFinnhubCongressSource(client *http.Client, apiKey string, tickers TickerLister) *FinnhubCongressSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &FinnhubCongressSource{client: client, apiKey: apiKey, tickers: tickers}
}

// Name implements ingestion.Source.
func (f *FinnhubCongressSource) Name() string { return finnhubCongressSourceName }

// Poll fetches congressional trades for all tracked tickers from Finnhub.
// Implements ingestion.Source.
func (f *FinnhubCongressSource) Poll(ctx context.Context) ([]db.Event, error) {
	tickers, err := f.tickers.ListTickers(ctx)
	if err != nil {
		return nil, fmt.Errorf("finnhub_congress: listing tickers: %w", err)
	}

	// Look back 90 days from today.
	to := time.Now().UTC().Format("2006-01-02")
	from := time.Now().UTC().AddDate(0, 0, -90).Format("2006-01-02")

	var all []db.Event
	for _, ticker := range tickers {
		if ctx.Err() != nil {
			break
		}
		url := fmt.Sprintf("%s?symbol=%s&from=%s&to=%s&token=%s",
			finnhubCongressBaseURL, ticker, from, to, f.apiKey)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}

		resp, err := f.client.Do(req)
		if err != nil {
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			continue
		}
		if err != nil {
			continue
		}

		events, err := ParseFinnhubCongress(body)
		if err != nil {
			continue
		}
		all = append(all, events...)

		// Respect Finnhub rate limit (30 req/sec) — be conservative.
		time.Sleep(50 * time.Millisecond)
	}
	return all, nil
}

// finnhubCongressResponse is the top-level API response.
type finnhubCongressResponse struct {
	Data   []finnhubCongressTrade `json:"data"`
	Symbol string                `json:"symbol"`
}

// finnhubCongressTrade is a single trade record from the Finnhub response.
type finnhubCongressTrade struct {
	AmountFrom      float64 `json:"amountFrom"`
	AmountTo        float64 `json:"amountTo"`
	AssetName       string  `json:"assetName"`
	FilingDate      string  `json:"filingDate"`
	Name            string  `json:"name"`
	OwnerType       string  `json:"ownerType"`
	Position        string  `json:"position"` // "Representative" or "Senator"
	Symbol          string  `json:"symbol"`
	TransactionDate string  `json:"transactionDate"`
	TransactionType string  `json:"transactionType"` // "Purchase" or "Sale"
}

// ParseFinnhubCongress parses a Finnhub congressional trading JSON response
// and returns one db.Event per trade.
func ParseFinnhubCongress(data []byte) ([]db.Event, error) {
	var resp finnhubCongressResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("finnhub_congress: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(resp.Data))
	for _, trade := range resp.Data {
		raw, err := json.Marshal(trade)
		if err != nil {
			continue
		}
		if err := ValidateEventData(raw); err != nil {
			continue
		}

		occurredAt := time.Now().UTC()
		if trade.TransactionDate != "" {
			if t, err := time.Parse("2006-01-02", trade.TransactionDate); err == nil {
				occurredAt = t.UTC()
			}
		} else if trade.FilingDate != "" {
			if t, err := time.Parse("2006-01-02", trade.FilingDate); err == nil {
				occurredAt = t.UTC()
			}
		}

		sid := sourceID(finnhubCongressSourceName,
			strings.TrimSpace(trade.Name),
			strings.ToUpper(strings.TrimSpace(trade.Symbol)),
			trade.TransactionDate,
			trade.TransactionType,
		)

		events = append(events, db.Event{
			Source:     finnhubCongressSourceName,
			SourceID:   sid,
			EventType:  "congressional_trade",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
```

- [ ] **Step 5: Add ListTickers method to db.Store**

Check if `ListTickers` already exists on Store. If not, add to `internal/db/store.go` (or the file where company queries live):
```go
// ListTickers returns all distinct tickers from the companies table.
func (s *Store) ListTickers(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ticker FROM companies ORDER BY ticker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tickers []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tickers = append(tickers, t)
	}
	return tickers, rows.Err()
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd C:/Users/AR/Projects/mrdn && go test ./internal/parser/ -run TestParseFinnhubCongress -v`
Expected: PASS (all 3 tests)

- [ ] **Step 7: Commit**

```bash
git add internal/parser/finnhub_congress.go internal/parser/finnhub_congress_test.go internal/parser/testdata/finnhub_congress_sample.json internal/db/
git commit -m "feat: add Finnhub congressional trading parser and source"
```

---

### Task 2: Finnhub Congressional Resolver

**Files:**
- Modify: `internal/resolver/resolver.go` — add `finnhub_congress` case (~line 173) and resolver function

**Context:** The resolver needs to handle `finnhub_congress` events by:
1. Extracting the trade from event_data (same shape as `finnhubCongressTrade`)
2. Resolving or creating the person by name slug (same pattern as `resolveEFDSTrades`)
3. Ensuring the company exists
4. Inserting a `CongressionalTrade` record

**Reference:** `resolver.go:711-787` (`resolveEFDSTrades`) — nearly identical logic.

- [ ] **Step 1: Add resolver case to source switch**

In `internal/resolver/resolver.go`, after the `case "efds_senate":` block (~line 171-172), add:
```go
	case "finnhub_congress":
		companyID, err = r.resolveFinnhubCongress(ctx, evt)
```

- [ ] **Step 2: Implement resolveFinnhubCongress**

Add to `internal/resolver/resolver.go`:
```go
type finnhubCongressEvent struct {
	AmountFrom      float64 `json:"amountFrom"`
	AmountTo        float64 `json:"amountTo"`
	AssetName       string  `json:"assetName"`
	FilingDate      string  `json:"filingDate"`
	Name            string  `json:"name"`
	OwnerType       string  `json:"ownerType"`
	Position        string  `json:"position"`
	Symbol          string  `json:"symbol"`
	TransactionDate string  `json:"transactionDate"`
	TransactionType string  `json:"transactionType"`
}

func (r *Resolver) resolveFinnhubCongress(ctx context.Context, evt db.Event) (int, error) {
	var trade finnhubCongressEvent
	if err := json.Unmarshal(evt.EventData, &trade); err != nil {
		return 0, fmt.Errorf("unmarshal finnhub congress trade: %w", err)
	}

	ticker := strings.ToUpper(strings.TrimSpace(trade.Symbol))
	if ticker == "" || ticker == "--" || ticker == "N/A" {
		return 0, nil
	}

	// Resolve person — create if not found.
	slug := slugifyName(trade.Name)
	var personID *int
	if p, err := r.store.GetPersonBySlug(ctx, slug); err == nil {
		personID = &p.ID
	}
	// Person creation is deferred to a separate pass or handled at
	// ingest time; resolver only links to existing persons.

	companyID, err := r.ensureCompany(ctx, ticker, trade.AssetName)
	if err != nil {
		return 0, fmt.Errorf("finnhub congress ensure company %s: %w", ticker, err)
	}

	var companyIDPtr *int
	if companyID > 0 {
		companyIDPtr = &companyID
	}

	var tradedAt *time.Time
	if trade.TransactionDate != "" {
		if t, err := time.Parse("2006-01-02", trade.TransactionDate); err == nil {
			tt := t.UTC()
			tradedAt = &tt
		}
	}

	var filedAt *time.Time
	if trade.FilingDate != "" {
		if t, err := time.Parse("2006-01-02", trade.FilingDate); err == nil {
			ft := t.UTC()
			filedAt = &ft
		}
	}

	// Map Finnhub transaction types to our schema.
	tradeType := trade.TransactionType // "Purchase" or "Sale"

	amtLow := int(trade.AmountFrom)
	amtHigh := int(trade.AmountTo)

	eventID := evt.ID
	ct := db.CongressionalTrade{
		EventID:         &eventID,
		PersonID:        personID,
		CompanyID:       companyIDPtr,
		OwnerType:       strPtr(trade.OwnerType),
		Ticker:          strPtr(ticker),
		TradeType:       strPtr(tradeType),
		AmountRangeLow:  intPtr(amtLow),
		AmountRangeHigh: intPtr(amtHigh),
		FiledAt:         filedAt,
		TradedAt:        tradedAt,
	}

	if err := r.store.InsertCongressionalTrade(ctx, ct); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] finnhub congress trade insert: %v", err)
		}
	}

	return companyID, nil
}
```

- [ ] **Step 3: Add slugifyName helper** (if not already present)

Check if a slug helper exists in resolver.go. If not, add:
```go
func slugifyName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	// Replace non-alphanumeric sequences with hyphens.
	var b strings.Builder
	prev := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prev = false
		} else if !prev {
			b.WriteByte('-')
			prev = true
		}
	}
	return strings.Trim(b.String(), "-")
}
```

- [ ] **Step 4: Run full test suite**

Run: `cd C:/Users/AR/Projects/mrdn && go test ./internal/resolver/ -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/resolver/resolver.go
git commit -m "feat: add Finnhub congressional trade resolver"
```

---

### Task 3: CourtListener Judicial Disclosures Parser

**Files:**
- Create: `internal/parser/courtlistener.go`
- Create: `internal/parser/courtlistener_test.go`
- Create: `internal/parser/testdata/courtlistener_disclosures_sample.json`

**Context:** CourtListener API v4 returns paginated disclosures at `/api/rest/v4/financial-disclosures/?ordering=-date_created`. Each disclosure has an `investments` array with transaction details. Auth is `Authorization: Token {key}`.

Investment value codes map to dollar ranges (J=$15K-$50K, K=$50K-$100K, etc.). We decode these to `amount_range_low`/`amount_range_high`.

**Reference:** `internal/parser/efds.go` — similar structure.

- [ ] **Step 1: Create test fixture**

Create `internal/parser/testdata/courtlistener_disclosures_sample.json`:
```json
{
  "count": 1,
  "next": null,
  "previous": null,
  "results": [
    {
      "id": 34211,
      "person": "https://www.courtlistener.com/api/rest/v4/people/1234/",
      "year": 2024,
      "date_created": "2025-03-15T08:00:00-07:00",
      "has_been_extracted": true,
      "investments": [
        {
          "id": 99001,
          "description": "Apple Inc (AAPL) - Stock",
          "redacted": false,
          "gross_value_code": "K",
          "transaction_during_reporting_period": "Purchase",
          "transaction_date": "2024-06-15",
          "transaction_value_code": "J",
          "financial_disclosure": "https://www.courtlistener.com/api/rest/v4/financial-disclosures/34211/"
        },
        {
          "id": 99002,
          "description": "Vanguard S&P 500 ETF (VOO)",
          "redacted": false,
          "gross_value_code": "N",
          "transaction_during_reporting_period": "",
          "transaction_date": null,
          "transaction_value_code": "",
          "financial_disclosure": "https://www.courtlistener.com/api/rest/v4/financial-disclosures/34211/"
        }
      ]
    }
  ]
}
```

- [ ] **Step 2: Write failing parser test**

Create `internal/parser/courtlistener_test.go`:
```go
package parser

import (
	"os"
	"testing"
)

func TestParseCourtListener(t *testing.T) {
	raw, err := os.ReadFile("testdata/courtlistener_disclosures_sample.json")
	if err != nil {
		t.Fatal(err)
	}

	events, _, err := ParseCourtListener(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Only investments with transactions produce events.
	// The VOO holding has no transaction, so only 1 event.
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.Source != "courtlistener" {
		t.Errorf("source = %q, want courtlistener", e.Source)
	}
	if e.EventType != "judicial_disclosure" {
		t.Errorf("event_type = %q, want judicial_disclosure", e.EventType)
	}
	if e.OccurredAt.Format("2006-01-02") != "2024-06-15" {
		t.Errorf("occurred_at = %v, want 2024-06-15", e.OccurredAt)
	}
}

func TestParseCourtListenerEmpty(t *testing.T) {
	events, _, err := ParseCourtListener([]byte(`{"count":0,"results":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestCourtListenerValueCode(t *testing.T) {
	tests := []struct {
		code    string
		wantLow int
		wantHi  int
	}{
		{"J", 15001, 50000},
		{"K", 50001, 100000},
		{"N", 500001, 1000000},
		{"P1", 5000001, 25000000},
	}
	for _, tt := range tests {
		lo, hi := courtListenerValueRange(tt.code)
		if lo != tt.wantLow || hi != tt.wantHi {
			t.Errorf("code %s: got (%d, %d), want (%d, %d)", tt.code, lo, hi, tt.wantLow, tt.wantHi)
		}
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd C:/Users/AR/Projects/mrdn && go test ./internal/parser/ -run TestParseCourtListener -v`
Expected: FAIL — `ParseCourtListener` undefined

- [ ] **Step 4: Implement parser and source**

Create `internal/parser/courtlistener.go`:
```go
package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	courtlistenerSourceName = "courtlistener"
	courtlistenerBaseURL    = "https://www.courtlistener.com/api/rest/v4/financial-disclosures/"
)

// CourtListenerSource polls the CourtListener API for federal judge
// financial disclosures.
type CourtListenerSource struct {
	client *http.Client
	token  string
}

// NewCourtListenerSource returns a CourtListenerSource.
func NewCourtListenerSource(client *http.Client, token string) *CourtListenerSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &CourtListenerSource{client: client, token: token}
}

// Name implements ingestion.Source.
func (c *CourtListenerSource) Name() string { return courtlistenerSourceName }

// Poll fetches recent judicial financial disclosures from CourtListener.
func (c *CourtListenerSource) Poll(ctx context.Context) ([]db.Event, error) {
	url := courtlistenerBaseURL + "?ordering=-date_created&has_been_extracted=true&limit=20"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("courtlistener: building request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("courtlistener: executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{Source: "courtlistener", StatusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("courtlistener: reading response: %w", err)
	}

	events, _, err := ParseCourtListener(body)
	if err != nil {
		return nil, fmt.Errorf("courtlistener: parsing: %w", err)
	}
	return events, nil
}

// CourtListener API response types.

type clResponse struct {
	Count   int            `json:"count"`
	Next    *string        `json:"next"`
	Results []clDisclosure `json:"results"`
}

type clDisclosure struct {
	ID          int            `json:"id"`
	Person      string         `json:"person"` // URL like ".../people/1234/"
	Year        int            `json:"year"`
	DateCreated string         `json:"date_created"`
	Investments []clInvestment `json:"investments"`
}

type clInvestment struct {
	ID              int     `json:"id"`
	Description     string  `json:"description"`
	Redacted        bool    `json:"redacted"`
	GrossValueCode  string  `json:"gross_value_code"`
	TransactionType string  `json:"transaction_during_reporting_period"`
	TransactionDate *string `json:"transaction_date"`
	TxValueCode     string  `json:"transaction_value_code"`
}

// clEventData is what we store in event_data JSON.
type clEventData struct {
	DisclosureID int    `json:"disclosure_id"`
	InvestmentID int    `json:"investment_id"`
	PersonURL    string `json:"person_url"`
	Year         int    `json:"year"`
	Description  string `json:"description"`
	TxType       string `json:"transaction_type"`
	TxDate       string `json:"transaction_date"`
	TxValueCode  string `json:"transaction_value_code"`
	GrossCode    string `json:"gross_value_code"`
}

var tickerInParens = regexp.MustCompile(`\(([A-Z]{1,5})\)`)

// ParseCourtListener parses a CourtListener disclosures response.
// Returns events for investments that have transactions, plus the next page URL.
func ParseCourtListener(data []byte) ([]db.Event, *string, error) {
	var resp clResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, fmt.Errorf("courtlistener: unmarshal: %w", err)
	}

	var events []db.Event
	for _, disc := range resp.Results {
		for _, inv := range disc.Investments {
			if inv.Redacted {
				continue
			}
			// Only produce events for investments with actual transactions.
			if inv.TransactionType == "" {
				continue
			}

			txDate := ""
			if inv.TransactionDate != nil {
				txDate = *inv.TransactionDate
			}

			ed := clEventData{
				DisclosureID: disc.ID,
				InvestmentID: inv.ID,
				PersonURL:    disc.Person,
				Year:         disc.Year,
				Description:  inv.Description,
				TxType:       inv.TransactionType,
				TxDate:       txDate,
				TxValueCode:  inv.TxValueCode,
				GrossCode:    inv.GrossValueCode,
			}

			raw, err := json.Marshal(ed)
			if err != nil {
				continue
			}
			if err := ValidateEventData(raw); err != nil {
				continue
			}

			occurredAt := time.Now().UTC()
			if txDate != "" {
				if t, err := time.Parse("2006-01-02", txDate); err == nil {
					occurredAt = t.UTC()
				}
			}

			sid := sourceID(courtlistenerSourceName,
				fmt.Sprintf("%d", disc.ID),
				fmt.Sprintf("%d", inv.ID),
			)

			events = append(events, db.Event{
				Source:     courtlistenerSourceName,
				SourceID:   sid,
				EventType:  "judicial_disclosure",
				EventData:  raw,
				OccurredAt: occurredAt,
			})
		}
	}
	return events, resp.Next, nil
}

// courtListenerValueRange converts a CourtListener value code to a dollar range.
func courtListenerValueRange(code string) (low, high int) {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "A":
		return 0, 0 // None or less than $201
	case "B":
		return 1001, 2500
	case "C":
		return 2501, 5000
	case "D":
		return 5001, 15000
	case "E":
		return 0, 200 // $1-$200
	case "F":
		return 201, 1000
	case "G":
		return 1001, 2500 // alias
	case "H":
		return 2501, 5000 // alias
	case "I":
		return 5001, 15000 // alias
	case "J":
		return 15001, 50000
	case "K":
		return 50001, 100000
	case "L":
		return 100001, 250000
	case "M":
		return 250001, 500000
	case "N":
		return 500001, 1000000
	case "O":
		return 1000001, 5000000
	case "P1":
		return 5000001, 25000000
	case "P2":
		return 25000001, 50000000
	case "P3":
		return 50000001, 0 // over $50M, no upper bound
	case "P4":
		return 100000001, 0 // over $100M
	}
	return 0, 0
}

// extractTickerFromDescription tries to pull a ticker from descriptions
// like "Apple Inc (AAPL) - Stock".
func extractTickerFromDescription(desc string) string {
	m := tickerInParens.FindStringSubmatch(desc)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd C:/Users/AR/Projects/mrdn && go test ./internal/parser/ -run "TestParseCourtListener|TestCourtListenerValueCode" -v`
Expected: PASS (all 3 tests)

- [ ] **Step 6: Commit**

```bash
git add internal/parser/courtlistener.go internal/parser/courtlistener_test.go internal/parser/testdata/courtlistener_disclosures_sample.json
git commit -m "feat: add CourtListener judicial disclosures parser and source"
```

---

### Task 4: CourtListener Resolver

**Files:**
- Modify: `internal/resolver/resolver.go` — add `courtlistener` case and resolver function
- Modify: `internal/resolver/resolver.go` — add `UpsertPerson` to `ResolverStore` interface

**Context:** The resolver extracts judicial investments into `congressional_trades` rows. It needs to:
1. Extract ticker from investment description (regex for `(AAPL)` pattern)
2. Decode value codes to dollar ranges
3. Create/resolve judge as a person (`branch: "judicial"`, `role: "judge"`)
4. The person URL format is `.../people/1234/` — extract the numeric ID for dedup

**Reference:** Task 2 resolver pattern. Also needs `UpsertPerson` on the store interface since judges won't exist yet.

- [ ] **Step 1: Add UpsertPerson to ResolverStore interface**

In `internal/resolver/resolver.go`, add to the `ResolverStore` interface (~line 18-36):
```go
	UpsertPerson(ctx context.Context, p db.Person) (db.Person, error)
```

- [ ] **Step 2: Add resolver case to source switch**

After the `finnhub_congress` case, add:
```go
	case "courtlistener":
		companyID, err = r.resolveCourtListener(ctx, evt)
```

- [ ] **Step 3: Implement resolveCourtListener**

Add to `internal/resolver/resolver.go`:
```go
type courtlistenerEvent struct {
	DisclosureID int    `json:"disclosure_id"`
	InvestmentID int    `json:"investment_id"`
	PersonURL    string `json:"person_url"`
	Year         int    `json:"year"`
	Description  string `json:"description"`
	TxType       string `json:"transaction_type"`
	TxDate       string `json:"transaction_date"`
	TxValueCode  string `json:"transaction_value_code"`
	GrossCode    string `json:"gross_value_code"`
}

func (r *Resolver) resolveCourtListener(ctx context.Context, evt db.Event) (int, error) {
	var inv courtlistenerEvent
	if err := json.Unmarshal(evt.EventData, &inv); err != nil {
		return 0, fmt.Errorf("unmarshal courtlistener investment: %w", err)
	}

	// Extract ticker from description like "Apple Inc (AAPL) - Stock".
	ticker := extractTickerFromCLDesc(inv.Description)
	if ticker == "" {
		return 0, nil // Can't resolve without a ticker
	}

	companyID, err := r.ensureCompany(ctx, ticker, "")
	if err != nil {
		return 0, fmt.Errorf("courtlistener ensure company %s: %w", ticker, err)
	}

	var companyIDPtr *int
	if companyID > 0 {
		companyIDPtr = &companyID
	}

	// Resolve judge — use person URL as a stable identifier.
	// URL format: "https://www.courtlistener.com/api/rest/v4/people/1234/"
	slug := "judge-cl-" + extractCLPersonID(inv.PersonURL)
	var personID *int
	if p, err := r.store.GetPersonBySlug(ctx, slug); err == nil {
		personID = &p.ID
	}

	var tradedAt *time.Time
	if inv.TxDate != "" {
		if t, err := time.Parse("2006-01-02", inv.TxDate); err == nil {
			tt := t.UTC()
			tradedAt = &tt
		}
	}

	low, high := clValueRange(inv.TxValueCode)
	if low == 0 && high == 0 {
		// Fallback to gross value code.
		low, high = clValueRange(inv.GrossCode)
	}

	eventID := evt.ID
	ct := db.CongressionalTrade{
		EventID:         &eventID,
		PersonID:        personID,
		CompanyID:       companyIDPtr,
		Ticker:          strPtr(ticker),
		TradeType:       strPtr(inv.TxType),
		AmountRangeLow:  intPtrOrNil(low),
		AmountRangeHigh: intPtrOrNil(high),
		TradedAt:        tradedAt,
	}

	if err := r.store.InsertCongressionalTrade(ctx, ct); err != nil {
		if !isDuplicateError(err) {
			log.Printf("[resolver] courtlistener trade insert: %v", err)
		}
	}

	return companyID, nil
}

var clTickerRe = regexp.MustCompile(`\(([A-Z]{1,5})\)`)

func extractTickerFromCLDesc(desc string) string {
	m := clTickerRe.FindStringSubmatch(desc)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func extractCLPersonID(url string) string {
	// ".../people/1234/" → "1234"
	parts := strings.Split(strings.Trim(url, "/"), "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "unknown"
}

func clValueRange(code string) (int, int) {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "J":
		return 15001, 50000
	case "K":
		return 50001, 100000
	case "L":
		return 100001, 250000
	case "M":
		return 250001, 500000
	case "N":
		return 500001, 1000000
	case "O":
		return 1000001, 5000000
	case "P1":
		return 5000001, 25000000
	case "P2":
		return 25000001, 50000000
	case "P3":
		return 50000001, 0
	case "P4":
		return 100000001, 0
	}
	return 0, 0
}

func intPtrOrNil(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
```

- [ ] **Step 4: Add regexp import if not present**

Check if `regexp` is already imported in resolver.go. If not, add it.

- [ ] **Step 5: Run full test suite**

Run: `cd C:/Users/AR/Projects/mrdn && go test ./internal/resolver/ -v -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/resolver/resolver.go
git commit -m "feat: add CourtListener judicial disclosure resolver"
```

---

### Task 5: Config + Supervisor Wiring

**Files:**
- Modify: `internal/config/config.go:10-24` — add `CourtListenerToken`
- Modify: `internal/config/config.go:62-72` — load from env
- Modify: `internal/ingestion/supervisor.go:66-87` — register both sources

**Context:** Wire the two new sources into the supervisor's `RegisterSources()` method. Follow the pattern of Polygon/FEC — only register if the API key is present.

- [ ] **Step 1: Add CourtListenerToken to Config struct**

In `internal/config/config.go`, add to the Config struct after `FECAPIKey` (line 18):
```go
	CourtListenerToken string
```

- [ ] **Step 2: Load from environment**

In `internal/config/config.go`, in the `Load()` function return block (~line 62-72), add:
```go
	CourtListenerToken: os.Getenv("MRDN_COURTLISTENER_TOKEN"),
```

- [ ] **Step 3: Register sources in supervisor**

In `internal/ingestion/supervisor.go`, after the FEC block (~line 82-84), add:
```go
	if s.cfg.FinnhubAPIKey != "" {
		sources = append(sources, parser.NewFinnhubCongressSource(client, s.cfg.FinnhubAPIKey, s.store))
	}
	if s.cfg.CourtListenerToken != "" {
		sources = append(sources, parser.NewCourtListenerSource(client, s.cfg.CourtListenerToken))
	}
```

Note: `s.store` implements `TickerLister` because we added `ListTickers` in Task 1. Verify that `db.Store` satisfies the interface.

- [ ] **Step 4: Run full project build**

Run: `cd C:/Users/AR/Projects/mrdn && go build ./...`
Expected: SUCCESS

- [ ] **Step 5: Run full test suite**

Run: `cd C:/Users/AR/Projects/mrdn && go test ./... -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/ingestion/supervisor.go
git commit -m "feat: wire Finnhub congress + CourtListener sources into supervisor"
```

---

### Task 6: Add secrets to CI and deploy

**Files:**
- Modify: `.github/workflows/ingest-deploy.yml` — add `MRDN_COURTLISTENER_TOKEN` env var

**Context:** The Finnhub key is already in CI as `MRDN_FINNHUB_API_KEY`. CourtListener token needs to be added as a GitHub secret and referenced in the workflow.

- [ ] **Step 1: Check current workflow env vars**

Read `.github/workflows/ingest-deploy.yml` to find where env vars are set.

- [ ] **Step 2: Add CourtListener token to workflow**

Add `MRDN_COURTLISTENER_TOKEN: ${{ secrets.MRDN_COURTLISTENER_TOKEN }}` alongside existing env vars.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ingest-deploy.yml
git commit -m "ci: add CourtListener token to ingest workflow"
```

- [ ] **Step 4: Remind user to add GitHub secret**

The user needs to manually add `MRDN_COURTLISTENER_TOKEN` as a repository secret in GitHub Settings > Secrets. Also needs to sign up for a free CourtListener API token at https://www.courtlistener.com/sign-in/.
