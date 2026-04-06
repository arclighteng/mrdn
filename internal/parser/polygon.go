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

// Poll fetches the most recent trading day's grouped daily bars from Polygon.io.
// It walks backward up to 5 days to skip weekends and holidays.
// Implements ingestion.Source.
//
// Security note (S07): the API key is never included in error messages.
// Any URL logged or returned in errors has the key value replaced with "[REDACTED]".
func (p *PolygonSource) Poll(ctx context.Context) ([]db.Event, error) {
	// Try the most recent weekday, then walk back up to 4 more to handle
	// holidays (e.g. Good Friday, MLK Day). Stop at the first date with data.
	now := time.Now().UTC()
	for attempt := 0; attempt < 5; attempt++ {
		date := nthWeekdayBefore(now, attempt)
		events, err := p.fetchDate(ctx, date)
		if err != nil {
			return nil, err
		}
		if len(events) > 0 {
			return events, nil
		}
		// 0 results — likely a market holiday, try the previous weekday.
	}
	return nil, nil // no data found in the last 5 weekdays
}

// fetchDate fetches grouped daily bars for a single date.
func (p *PolygonSource) fetchDate(ctx context.Context, date string) ([]db.Event, error) {
	rawURL := fmt.Sprintf("%s/%s?adjusted=true&apiKey=%s", polygonBaseURL, date, p.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
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

	return ParsePolygonDaily(body)
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

// nthWeekdayBefore returns the nth weekday before now (0 = most recent weekday
// before today, 1 = the one before that, etc.), formatted as "2006-01-02".
func nthWeekdayBefore(now time.Time, n int) string {
	d := now.AddDate(0, 0, -1) // start with yesterday
	found := 0
	for i := 0; i < 14; i++ { // 14 days covers any n up to ~10
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			if found == n {
				return d.Format("2006-01-02")
			}
			found++
		}
		d = d.AddDate(0, 0, -1)
	}
	return d.Format("2006-01-02")
}
