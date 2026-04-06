package parser

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	efdsSourceName = "senate_efds"
	// efdsBaseURL is the Senate EFDS report data endpoint.
	// Hardcoded to prevent SSRF; no API key required (public data).
	efdsBaseURL = "https://efdsearch.senate.gov/search/report/data/"
)

// EFDSSource polls the Senate Electronic Financial Disclosure System for
// congressional financial disclosure filings.
type EFDSSource struct {
	client *http.Client
}

// NewEFDSSource returns an EFDSSource using the provided HTTP client.
// If client is nil, http.DefaultClient is used.
func NewEFDSSource(client *http.Client) *EFDSSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &EFDSSource{client: client}
}

// Name implements ingestion.Source.
func (e *EFDSSource) Name() string { return efdsSourceName }

// Poll fetches Senate EFDS filings and returns parsed congressional disclosure events.
// Implements ingestion.Source.
func (e *EFDSSource) Poll(ctx context.Context) ([]db.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, efdsBaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("efds: building request: %w", err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("efds: executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("efds: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("efds: reading response body: %w", err)
	}

	events, err := ParseEFDS(body)
	if err != nil {
		return nil, fmt.Errorf("efds: parsing response: %w", err)
	}
	return events, nil
}

// efdsFiling is a single EFDS filing record from the XML response.
type efdsFiling struct {
	FirstName   string `xml:"first_name" json:"first_name"`
	LastName    string `xml:"last_name" json:"last_name"`
	FilingType  string `xml:"filing_type" json:"filing_type"`
	FilingDate  string `xml:"filing_date" json:"filing_date"`
	ReportID    string `xml:"report_id" json:"report_id"`
}

// efdsFilings is the top-level XML envelope.
type efdsFilings struct {
	XMLName xml.Name     `xml:"filings"`
	Filings []efdsFiling `xml:"filing"`
}

// ParseEFDS parses raw EFDS XML data and returns one db.Event per filing with
// EventType "congressional_disclosure".
// This function is pure and safe to call independently of any HTTP transport.
func ParseEFDS(data []byte) ([]db.Event, error) {
	var envelope efdsFilings
	dec := xml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("efds: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(envelope.Filings))
	for _, filing := range envelope.Filings {
		raw, err := json.Marshal(filing)
		if err != nil {
			return nil, fmt.Errorf("efds: re-marshaling filing report_id=%s: %w", filing.ReportID, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("efds: filing report_id=%s: %w", filing.ReportID, err)
		}

		occurredAt := time.Now().UTC()
		if filing.FilingDate != "" {
			if t, err := time.Parse("01/02/2006", filing.FilingDate); err == nil {
				occurredAt = t.UTC()
			}
		}

		events = append(events, db.Event{
			Source:     efdsSourceName,
			SourceID:   sourceID(efdsSourceName, filing.ReportID),
			EventType:  "congressional_disclosure",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
