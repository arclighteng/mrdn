package parser

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	ofacSourceName = "ofac_sdn"
	// ofacBaseURL is the official US Treasury OFAC SDN XML feed.
	// Hardcoded to prevent SSRF; no API key required (public data).
	ofacBaseURL = "https://sanctionslistservice.ofac.treas.gov/api/PublicationPreview/exports/SDN.XML"
	// ofacMaxResponseBody is higher than the default because the full SDN list
	// is ~28 MB XML with ~18,000 entries.
	ofacMaxResponseBody = 50 * 1024 * 1024
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

// Poll fetches the OFAC SDN list from the official US Treasury XML feed and
// returns parsed sanction events. The endpoint may return a 302 redirect to an
// S3 pre-signed URL; the default http.Client follows redirects automatically.
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
		return nil, &HTTPStatusError{Source: "ofac", StatusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, ofacMaxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("ofac: reading response body: %w", err)
	}

	events, err := ParseOFACXML(body)
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

// ofacXMLSDNList is the top-level XML envelope from the Treasury SDN XML feed.
type ofacXMLSDNList struct {
	XMLName xml.Name        `xml:"sdnList"`
	Entries []ofacXMLEntry  `xml:"sdnEntry"`
}

// ofacXMLEntry is a single SDN entry in the Treasury XML format.
type ofacXMLEntry struct {
	UID       int              `xml:"uid"`
	FirstName string           `xml:"firstName"`
	LastName  string           `xml:"lastName"`
	SDNType   string           `xml:"sdnType"`
	Programs  ofacXMLPrograms  `xml:"programList"`
}

type ofacXMLPrograms struct {
	Programs []string `xml:"program"`
}

// ParseOFACXML parses the official US Treasury OFAC SDN XML feed and returns
// one db.Event per SDN entry with EventType "sanction_designation".
func ParseOFACXML(data []byte) ([]db.Event, error) {
	var list ofacXMLSDNList
	dec := xml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&list); err != nil {
		return nil, fmt.Errorf("ofac: unmarshal xml: %w", err)
	}

	events := make([]db.Event, 0, len(list.Entries))
	for _, entry := range list.Entries {
		// Convert to our JSON-friendly struct for storage.
		jsonEntry := ofacEntry{
			UID:       entry.UID,
			FirstName: entry.FirstName,
			LastName:  entry.LastName,
			SDNType:   entry.SDNType,
			Programs:  entry.Programs.Programs,
		}

		raw, err := json.Marshal(jsonEntry)
		if err != nil {
			return nil, fmt.Errorf("ofac: marshaling entry uid=%d: %w", entry.UID, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("ofac: entry uid=%d: %w", entry.UID, err)
		}

		events = append(events, db.Event{
			Source:     ofacSourceName,
			SourceID:   sourceID(ofacSourceName, strconv.Itoa(entry.UID)),
			EventType:  "sanction_designation",
			EventData:  raw,
			OccurredAt: time.Now().UTC(),
		})
	}
	return events, nil
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
