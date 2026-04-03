package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	ofacSourceName = "ofac_sdn"
	// ofacBaseURL is the OFAC SDN JSON API endpoint. Hardcoded to prevent SSRF.
	ofacBaseURL = "https://api.ofac-api.com/v4/sdn"
)

// OFACSource polls the OFAC SDN list for sanction designation events.
type OFACSource struct {
	client *http.Client
}

// NewOFACSource returns an OFACSource using the provided HTTP client.
// If client is nil, http.DefaultClient is used.
func NewOFACSource(client *http.Client) *OFACSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &OFACSource{client: client}
}

// Name implements ingestion.Source.
func (o *OFACSource) Name() string { return ofacSourceName }

// Poll fetches the OFAC SDN list and returns parsed sanction events.
// Implements ingestion.Source.
func (o *OFACSource) Poll(ctx context.Context) ([]db.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ofacBaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ofac: building request: %w", err)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ofac: executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ofac: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("ofac: reading response body: %w", err)
	}

	events, err := ParseOFAC(body)
	if err != nil {
		return nil, fmt.Errorf("ofac: parsing response: %w", err)
	}
	return events, nil
}

// ofacEntry is a single SDN list entry as returned by the OFAC API.
type ofacEntry struct {
	UID       int             `json:"uid"`
	FirstName string          `json:"firstName"`
	LastName  string          `json:"lastName"`
	SDNType   string          `json:"sdnType"`
	Programs  []string        `json:"programs"`
	DateAdded string          `json:"dateAdded"` // "YYYY-MM-DD"
	Aliases   json.RawMessage `json:"aliases"`
}

// ofacResponse is the top-level envelope returned by the OFAC API.
type ofacResponse struct {
	SDNList []ofacEntry `json:"sdnList"`
}

// ParseOFAC parses the raw OFAC SDN JSON response and returns one db.Event per
// SDN entry with EventType "sanction_designation".
// This function is pure and safe to call independently of any HTTP transport.
func ParseOFAC(data []byte) ([]db.Event, error) {
	var resp ofacResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("ofac: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(resp.SDNList))
	for _, entry := range resp.SDNList {
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("ofac: re-marshaling entry uid=%d: %w", entry.UID, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("ofac: entry uid=%d: %w", entry.UID, err)
		}

		occurredAt := time.Now().UTC()
		if entry.DateAdded != "" {
			if t, err := time.Parse("2006-01-02", entry.DateAdded); err == nil {
				occurredAt = t.UTC()
			}
		}

		events = append(events, db.Event{
			Source:     ofacSourceName,
			SourceID:   sourceID(ofacSourceName, strconv.Itoa(entry.UID)),
			EventType:  "sanction_designation",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
