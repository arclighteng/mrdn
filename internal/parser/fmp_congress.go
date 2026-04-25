package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	fmpCongressSourceName  = "fmp_congress"
	fmpSenateLatestURL     = "https://financialmodelingprep.com/stable/senate-latest"
	fmpHouseLatestURL      = "https://financialmodelingprep.com/stable/house-latest"
)

// fmpAmountNumRe matches digit sequences (with optional commas) for amount parsing.
var fmpAmountNumRe = regexp.MustCompile(`[\d,]+`)

// FMPCongressSource polls the Financial Modeling Prep senate-latest and
// house-latest endpoints for congressional trading data.
type FMPCongressSource struct {
	client *http.Client
	apiKey string
}

// NewFMPCongressSource returns an FMPCongressSource. If client is nil,
// http.DefaultClient is used.
func NewFMPCongressSource(client *http.Client, apiKey string) *FMPCongressSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &FMPCongressSource{
		client: client,
		apiKey: apiKey,
	}
}

// Name implements ingestion.Source.
func (f *FMPCongressSource) Name() string { return fmpCongressSourceName }

// Poll fetches both Senate and House congressional trading endpoints and returns
// the combined set of events.
func (f *FMPCongressSource) Poll(ctx context.Context) ([]db.Event, error) {
	chambers := []struct {
		url     string
		chamber string
	}{
		{fmpSenateLatestURL, "senate"},
		{fmpHouseLatestURL, "house"},
	}

	var all []db.Event
	for _, ch := range chambers {
		url := fmt.Sprintf("%s?apikey=%s", ch.url, f.apiKey)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("fmp_congress: building request for %s (%s): %w", ch.chamber, redactKey(url, f.apiKey), err)
		}

		resp, err := f.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fmp_congress: executing request for %s (%s): %w", ch.chamber, redactKey(url, f.apiKey), err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, &HTTPStatusError{Source: fmpCongressSourceName, StatusCode: resp.StatusCode}
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("fmp_congress: reading response for %s: %w", ch.chamber, err)
		}

		events, err := ParseFMPCongress(body, ch.chamber)
		if err != nil {
			return nil, fmt.Errorf("fmp_congress: parsing response for %s: %w", ch.chamber, err)
		}
		all = append(all, events...)
	}

	return all, nil
}

// fmpCongressRecord is one trade record from the FMP senate-latest or
// house-latest endpoint.
type fmpCongressRecord struct {
	Symbol          string `json:"symbol"`
	DisclosureDate  string `json:"disclosureDate"`
	TransactionDate string `json:"transactionDate"`
	FirstName       string `json:"firstName"`
	LastName        string `json:"lastName"`
	Office          string `json:"office"`
	District        string `json:"district"`
	Owner           string `json:"owner"`
	AssetDescription string `json:"assetDescription"`
	AssetType       string `json:"assetType"`
	Type            string `json:"type"`
	Amount          string `json:"amount"`
	Comment         string `json:"comment"`
	Link            string `json:"link"`
}

// fmpCongressEventData is the event_data payload stored per trade.
type fmpCongressEventData struct {
	Symbol           string `json:"symbol"`
	DisclosureDate   string `json:"disclosure_date"`
	TransactionDate  string `json:"transaction_date"`
	FirstName        string `json:"first_name"`
	LastName         string `json:"last_name"`
	Office           string `json:"office"`
	District         string `json:"district"`
	Owner            string `json:"owner"`
	AssetDescription string `json:"asset_description"`
	AssetType        string `json:"asset_type"`
	TradeType        string `json:"trade_type"`
	Amount           string `json:"amount"`
	AmountLow        int    `json:"amount_low"`
	AmountHigh       int    `json:"amount_high"`
	Chamber          string `json:"chamber"` // "senate" or "house"
	Link             string `json:"link"`
}

// ParseFMPCongress parses raw JSON from the FMP senate-latest or house-latest
// endpoint and returns one db.Event per trade. chamber must be "senate" or
// "house". This function is pure and safe to call independently of any HTTP
// transport.
func ParseFMPCongress(data []byte, chamber string) ([]db.Event, error) {
	var records []fmpCongressRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("fmp_congress: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(records))
	for _, rec := range records {
		low, high := ParseFMPAmountRange(rec.Amount)

		payload := fmpCongressEventData{
			Symbol:           rec.Symbol,
			DisclosureDate:   rec.DisclosureDate,
			TransactionDate:  rec.TransactionDate,
			FirstName:        rec.FirstName,
			LastName:         rec.LastName,
			Office:           rec.Office,
			District:         rec.District,
			Owner:            rec.Owner,
			AssetDescription: rec.AssetDescription,
			AssetType:        rec.AssetType,
			TradeType:        rec.Type,
			Amount:           rec.Amount,
			AmountLow:        low,
			AmountHigh:       high,
			Chamber:          chamber,
			Link:             rec.Link,
		}

		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("fmp_congress: marshal trade %s %s %s: %w",
				rec.FirstName, rec.LastName, rec.Symbol, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("fmp_congress: trade %s %s %s: %w",
				rec.FirstName, rec.LastName, rec.Symbol, err)
		}

		// OccurredAt: prefer transactionDate, fall back to disclosureDate.
		dateStr := rec.TransactionDate
		if dateStr == "" {
			dateStr = rec.DisclosureDate
		}
		occurredAt := time.Now().UTC()
		if dateStr != "" {
			if t, err := time.Parse("2006-01-02", dateStr); err == nil {
				occurredAt = t.UTC()
			}
		}

		events = append(events, db.Event{
			Source: fmpCongressSourceName,
			SourceID: sourceID(
				fmpCongressSourceName,
				rec.FirstName+"|"+rec.LastName,
				rec.Symbol,
				rec.TransactionDate,
				rec.Type,
			),
			EventType:  "congressional_trade",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}

// ParseFMPAmountRange parses strings like "$1,001 - $15,000" into (low, high)
// integer values. Returns (0, 0) if the string cannot be parsed.
// Exported for use in tests.
func ParseFMPAmountRange(s string) (int, int) {
	matches := fmpAmountNumRe.FindAllString(s, -1)
	if len(matches) == 0 {
		return 0, 0
	}
	parse := func(x string) int {
		v, err := strconv.Atoi(strings.ReplaceAll(x, ",", ""))
		if err != nil {
			return 0
		}
		return v
	}
	if len(matches) == 1 {
		v := parse(matches[0])
		return v, v
	}
	return parse(matches[0]), parse(matches[1])
}
