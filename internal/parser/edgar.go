package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	edgarSourceName = "edgar_form4"
	// edgarBaseURL is the SEC EDGAR full-text search index endpoint.
	// Hardcoded to prevent SSRF; no API key required (public data).
	edgarBaseURL = "https://efts.sec.gov/LATEST/search-index"
	// edgarUserAgent is required by SEC fair-access policy.
	edgarUserAgent = "MRDN/1.0 (contact@arclighteng.com)"
)

// EdgarSource polls the SEC EDGAR full-text search API for Form 4
// (insider trading) filings.
type EdgarSource struct {
	client *http.Client
}

// NewEdgarSource returns an EdgarSource using the provided HTTP client.
// If client is nil, http.DefaultClient is used.
func NewEdgarSource(client *http.Client) *EdgarSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &EdgarSource{client: client}
}

// Name implements ingestion.Source.
func (e *EdgarSource) Name() string { return edgarSourceName }

// Poll fetches recent Form 4 filings from EDGAR for the past 3 days.
// The 3-day window ensures weekend/holiday coverage.
// Implements ingestion.Source.
func (e *EdgarSource) Poll(ctx context.Context) ([]db.Event, error) {
	now := time.Now().UTC()
	endDate := now.Format("2006-01-02")
	startDate := now.AddDate(0, 0, -3).Format("2006-01-02")

	params := url.Values{}
	params.Set("q", "4")
	params.Set("forms", "4")
	params.Set("dateRange", "custom")
	params.Set("startdt", startDate)
	params.Set("enddt", endDate)

	reqURL := edgarBaseURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("edgar: building request: %w", err)
	}
	req.Header.Set("User-Agent", edgarUserAgent)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("edgar: executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{Source: "edgar", StatusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("edgar: reading response body: %w", err)
	}

	events, err := ParseEdgarForm4(body)
	if err != nil {
		return nil, fmt.Errorf("edgar: parsing response: %w", err)
	}
	return events, nil
}

// edgarFilingSource is the _source object inside an EFTS search hit.
// The real EDGAR API returns file_num as an array; test fixtures may use a
// plain string. We store the raw JSON and extract a scalar with fileNumScalar().
type edgarFilingSource struct {
	FileNum        json.RawMessage `json:"file_num"`
	DisplayNames   []string        `json:"display_names"`
	FormType       string          `json:"form_type"`
	Form           string          `json:"form"`
	FileDate       string          `json:"file_date"`        // "YYYY-MM-DD"
	PeriodOfReport string          `json:"period_of_report"` // "YYYY-MM-DD"
	PeriodEnding   string          `json:"period_ending"`    // "YYYY-MM-DD"
	EntityName     string          `json:"entity_name"`
	ADSH           string          `json:"adsh"` // accession number
}

// fileNumScalar returns a single file_num string regardless of whether the
// API returned a string or an array of strings.
func (s *edgarFilingSource) fileNumScalar() string {
	if s.ADSH != "" {
		return s.ADSH // prefer accession number as unique ID
	}
	if len(s.FileNum) == 0 {
		return ""
	}
	// Try string first.
	var str string
	if json.Unmarshal(s.FileNum, &str) == nil {
		return str
	}
	// Try array.
	var arr []string
	if json.Unmarshal(s.FileNum, &arr) == nil && len(arr) > 0 {
		return arr[0]
	}
	return string(s.FileNum)
}

// effectiveDate returns the best available date for the filing.
func (s *edgarFilingSource) effectiveDate() string {
	if s.FileDate != "" {
		return s.FileDate
	}
	if s.PeriodOfReport != "" {
		return s.PeriodOfReport
	}
	return s.PeriodEnding
}

// edgarHit is a single hit in the EFTS search response.
type edgarHit struct {
	Source edgarFilingSource `json:"_source"`
}

// edgarHits wraps the nested hits structure.
type edgarHits struct {
	Hits []edgarHit `json:"hits"`
}

// edgarResponse is the top-level EFTS search response envelope.
type edgarResponse struct {
	Hits edgarHits `json:"hits"`
}

// ParseEdgarForm4 parses the raw EDGAR EFTS JSON search response and returns
// one db.Event per Form 4 filing hit with EventType "insider_trade".
// This function is pure and safe to call independently of any HTTP transport.
func ParseEdgarForm4(data []byte) ([]db.Event, error) {
	var resp edgarResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("edgar: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(resp.Hits.Hits))
	for _, hit := range resp.Hits.Hits {
		src := hit.Source
		fnum := src.fileNumScalar()

		raw, err := json.Marshal(src)
		if err != nil {
			return nil, fmt.Errorf("edgar: re-marshaling filing file_num=%s: %w", fnum, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("edgar: filing file_num=%s: %w", fnum, err)
		}

		occurredAt := time.Now().UTC()
		if d := src.effectiveDate(); d != "" {
			if t, err := time.Parse("2006-01-02", d); err == nil {
				occurredAt = t.UTC()
			}
		}

		events = append(events, db.Event{
			Source:     edgarSourceName,
			SourceID:   sourceID(edgarSourceName, fnum),
			EventType:  "insider_trade",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
