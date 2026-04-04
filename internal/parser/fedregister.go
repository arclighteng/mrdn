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
	fedRegisterSourceName = "federal_register"
	// fedRegisterBaseURL is the Federal Register documents API endpoint.
	// Hardcoded to prevent SSRF; no API key required (public data).
	fedRegisterBaseURL = "https://www.federalregister.gov/api/v1/documents.json" +
		"?conditions[type][]=RULE&conditions[type][]=PRORULE&per_page=50"
)

// FedRegisterSource polls the Federal Register API for new regulatory actions.
type FedRegisterSource struct {
	client *http.Client
}

// NewFedRegisterSource returns a FedRegisterSource using the provided HTTP client.
// If client is nil, http.DefaultClient is used.
func NewFedRegisterSource(client *http.Client) *FedRegisterSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &FedRegisterSource{client: client}
}

// Name implements ingestion.Source.
func (f *FedRegisterSource) Name() string { return fedRegisterSourceName }

// Poll fetches recent rules and proposed rules from the Federal Register.
// Implements ingestion.Source.
func (f *FedRegisterSource) Poll(ctx context.Context) ([]db.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fedRegisterBaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fedregister: building request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fedregister: executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fedregister: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("fedregister: reading response body: %w", err)
	}

	events, err := ParseFedRegister(body)
	if err != nil {
		return nil, fmt.Errorf("fedregister: parsing response: %w", err)
	}
	return events, nil
}

// fedRegisterResponse is the top-level envelope from the Federal Register API.
type fedRegisterResponse struct {
	Results []json.RawMessage `json:"results"`
}

// fedRegisterDocument holds only the metadata fields needed to build the event.
type fedRegisterDocument struct {
	DocumentNumber  string `json:"document_number"`
	PublicationDate string `json:"publication_date"`
}

// ParseFedRegister parses the raw Federal Register JSON response and returns one
// db.Event per document with EventType "regulatory_action".
// This function is pure and safe to call independently of any HTTP transport.
func ParseFedRegister(data []byte) ([]db.Event, error) {
	var resp fedRegisterResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("fedregister: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(resp.Results))
	for i, raw := range resp.Results {
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("fedregister: result[%d]: %w", i, err)
		}

		var doc fedRegisterDocument
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("fedregister: decoding result[%d]: %w", i, err)
		}

		occurredAt := time.Now().UTC()
		if doc.PublicationDate != "" {
			if t, err := time.Parse("2006-01-02", doc.PublicationDate); err == nil {
				occurredAt = t.UTC()
			}
		}

		events = append(events, db.Event{
			Source:     fedRegisterSourceName,
			SourceID:   sourceID(fedRegisterSourceName, doc.DocumentNumber),
			EventType:  "regulatory_action",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
