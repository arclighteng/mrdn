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
	fecSourceName = "fec"
	// fecBaseURL is the FEC Schedule A contributions endpoint.
	// Hardcoded to prevent SSRF. API key and two_year_transaction_period are
	// appended at runtime.
	fecBaseURL = "https://api.open.fec.gov/v1/schedules/schedule_a/" +
		"?sort=-contribution_receipt_date&per_page=50"
)

// FECSource polls the FEC API for recent campaign finance contributions.
type FECSource struct {
	client *http.Client
	apiKey string
}

// NewFECSource returns a FECSource using the provided HTTP client and FEC API key.
// If client is nil, http.DefaultClient is used.
func NewFECSource(client *http.Client, apiKey string) *FECSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &FECSource{client: client, apiKey: apiKey}
}

// Name implements ingestion.Source.
func (f *FECSource) Name() string { return fecSourceName }

// Poll fetches recent campaign finance contributions from the FEC API.
// Implements ingestion.Source.
//
// Security note: the API key is never included in error messages.
// Any URL in errors has the key value replaced with "[REDACTED]".
func (f *FECSource) Poll(ctx context.Context) ([]db.Event, error) {
	// FEC requires two_year_transaction_period; use the current even-numbered
	// election cycle year (e.g. 2025→2026, 2026→2026).
	year := time.Now().Year()
	if year%2 != 0 {
		year++
	}
	rawURL := fmt.Sprintf("%s&two_year_transaction_period=%d&api_key=%s", fecBaseURL, year, f.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fec: building request for %s: %w",
			redactKey(rawURL, f.apiKey), err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fec: executing request for %s: %w",
			redactKey(rawURL, f.apiKey), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{Source: "fec", StatusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("fec: reading response body: %w", err)
	}

	events, err := ParseFEC(body)
	if err != nil {
		return nil, fmt.Errorf("fec: parsing response: %w", err)
	}
	return events, nil
}

// fecContribution holds the fields needed to build the event from an FEC
// Schedule A record. Sensitive display fields are sanitized before storage.
type fecContribution struct {
	SubID                    string  `json:"sub_id"`
	ContributorName          string  `json:"contributor_name"`
	ContributionReceiptAmount float64 `json:"contribution_receipt_amount"`
	ContributionReceiptDate  string  `json:"contribution_receipt_date"`
	CommitteeName            string  `json:"committee_name"`
	ContributorEmployer      string  `json:"contributor_employer"`
	ContributorOccupation    string  `json:"contributor_occupation"`
}

// fecResponse is the top-level envelope from the FEC Schedule A endpoint.
type fecResponse struct {
	Results []json.RawMessage `json:"results"`
}

// SanitizeCSVField strips leading formula-injection characters (=, +, -, @) from
// the string. This prevents CSV injection if event data is ever exported to CSV.
func SanitizeCSVField(s string) string {
	return strings.TrimLeft(s, "=+-@")
}

// ParseFEC parses the raw FEC Schedule A JSON response and returns one db.Event
// per contribution result with EventType "political_contribution".
// contributor_name and committee_name are sanitized for CSV injection before storage.
// This function is pure and safe to call independently of any HTTP transport.
func ParseFEC(data []byte) ([]db.Event, error) {
	var resp fecResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("fec: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(resp.Results))
	for i, raw := range resp.Results {
		var contrib fecContribution
		if err := json.Unmarshal(raw, &contrib); err != nil {
			return nil, fmt.Errorf("fec: decoding result[%d]: %w", i, err)
		}

		// Sanitize CSV-injectable fields before re-marshaling for storage.
		contrib.ContributorName = SanitizeCSVField(contrib.ContributorName)
		contrib.CommitteeName = SanitizeCSVField(contrib.CommitteeName)

		sanitized, err := json.Marshal(contrib)
		if err != nil {
			return nil, fmt.Errorf("fec: re-marshaling result[%d] sub_id=%s: %w", i, contrib.SubID, err)
		}
		if err := ValidateEventData(sanitized); err != nil {
			return nil, fmt.Errorf("fec: result[%d] sub_id=%s: %w", i, contrib.SubID, err)
		}

		occurredAt := time.Now().UTC()
		if contrib.ContributionReceiptDate != "" {
			if t, err := time.Parse("2006-01-02", contrib.ContributionReceiptDate); err == nil {
				occurredAt = t.UTC()
			}
		}

		events = append(events, db.Event{
			Source:     fecSourceName,
			SourceID:   sourceID(fecSourceName, contrib.SubID),
			EventType:  "political_contribution",
			EventData:  sanitized,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
