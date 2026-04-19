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
	// during implementation. The JSON envelope shape ({releases: [...]}) is a placeholder.
	secLitBaseURL = "https://efts.sec.gov/LATEST/search-index?q=%22litigation+release%22&dateRange=custom&startdt=2024-01-01&enddt=2099-12-31&forms=LR"
)

// SECLitigationSource polls the SEC EDGAR full-text search index for litigation releases.
type SECLitigationSource struct {
	client *http.Client
}

// NewSECLitigationSource returns a SECLitigationSource using the provided HTTP client.
// If client is nil, http.DefaultClient is used.
func NewSECLitigationSource(client *http.Client) *SECLitigationSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &SECLitigationSource{client: client}
}

// Name implements ingestion.Source.
func (s *SECLitigationSource) Name() string { return secLitSourceName }

// Poll fetches recent SEC litigation releases from the EDGAR search index.
// Implements ingestion.Source.
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

// secLitResponse is the top-level envelope from the SEC EDGAR litigation releases endpoint.
// NOTE: This shape is a placeholder; validate against the real API before relying on it.
type secLitResponse struct {
	Releases []json.RawMessage `json:"releases"`
}

// secLitRelease holds the metadata fields needed to build the event.
type secLitRelease struct {
	ID    string `json:"id"`
	Date  string `json:"date"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// ParseSECLitigation parses the raw SEC litigation releases JSON response and returns
// one db.Event per release with EventType "sec_litigation".
// This function is pure and safe to call independently of any HTTP transport.
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
