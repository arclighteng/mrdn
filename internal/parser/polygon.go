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
	polygonSourceName = "polygon"
	// polygonBaseURL is the Polygon.io grouped daily bars endpoint.
	// The date segment is filled in at runtime. Hardcoded host prevents SSRF.
	polygonBaseURL = "https://api.polygon.io/v2/aggs/grouped/locale/us/market/stocks"
)

// PolygonSource polls Polygon.io for daily OHLCV market data.
type PolygonSource struct {
	client *http.Client
	apiKey string
}

// NewPolygonSource returns a PolygonSource using the provided HTTP client and
// Polygon.io API key. If client is nil, http.DefaultClient is used.
func NewPolygonSource(client *http.Client, apiKey string) *PolygonSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &PolygonSource{client: client, apiKey: apiKey}
}

// Name implements ingestion.Source.
func (p *PolygonSource) Name() string { return polygonSourceName }

// Poll fetches the previous trading day's grouped daily bars from Polygon.io.
// Implements ingestion.Source.
//
// Security note (S07): the API key is never included in error messages.
// Any URL logged or returned in errors has the key value replaced with "[REDACTED]".
func (p *PolygonSource) Poll(ctx context.Context) ([]db.Event, error) {
	// Use yesterday as the trading date.
	date := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")

	rawURL := fmt.Sprintf("%s/%s?adjusted=true&apiKey=%s", polygonBaseURL, date, p.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		// Redact the key before surfacing the error.
		return nil, fmt.Errorf("polygon: building request for %s: %w",
			redactKey(rawURL, p.apiKey), err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("polygon: executing request for %s: %w",
			redactKey(rawURL, p.apiKey), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("polygon: unexpected status %d for date %s", resp.StatusCode, date)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("polygon: reading response body: %w", err)
	}

	events, err := ParsePolygonDaily(body)
	if err != nil {
		return nil, fmt.Errorf("polygon: parsing response: %w", err)
	}
	return events, nil
}

// redactKey replaces apiKey value in s with "[REDACTED]" so keys never appear
// in logs or error strings.
func redactKey(s, apiKey string) string {
	if apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, apiKey, "[REDACTED]")
}

// polygonBar is a single grouped daily bar result from Polygon.io.
type polygonBar struct {
	Ticker    string  `json:"T"`
	Open      float64 `json:"o"`
	High      float64 `json:"h"`
	Low       float64 `json:"l"`
	Close     float64 `json:"c"`
	Volume    float64 `json:"v"`
	Timestamp int64   `json:"t"` // Unix milliseconds
}

// polygonResponse is the top-level envelope from the grouped daily bars endpoint.
type polygonResponse struct {
	ResultsCount int          `json:"resultsCount"`
	Results      []polygonBar `json:"results"`
}

// ParsePolygonDaily parses the raw Polygon.io grouped daily bars JSON response
// and returns one db.Event per bar with EventType "market_daily".
// This function is pure and safe to call independently of any HTTP transport.
func ParsePolygonDaily(data []byte) ([]db.Event, error) {
	var resp polygonResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("polygon: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(resp.Results))
	for _, bar := range resp.Results {
		raw, err := json.Marshal(bar)
		if err != nil {
			return nil, fmt.Errorf("polygon: re-marshaling bar ticker=%s: %w", bar.Ticker, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("polygon: bar ticker=%s: %w", bar.Ticker, err)
		}

		occurredAt := time.UnixMilli(bar.Timestamp).UTC()
		// Derive the date string for the source_id from the timestamp so the ID
		// is stable across re-runs on the same trading day.
		date := occurredAt.Format("2006-01-02")

		events = append(events, db.Event{
			Source:     polygonSourceName,
			SourceID:   sourceID(polygonSourceName, bar.Ticker, date),
			EventType:  "market_daily",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
