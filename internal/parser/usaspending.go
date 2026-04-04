package parser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	usaspendingSourceName = "usaspending"
	// usaspendingBaseURL is the USAspending spending-by-award search endpoint.
	// Hardcoded to prevent SSRF; no API key required (public data).
	usaspendingBaseURL = "https://api.usaspending.gov/api/v2/search/spending_by_award/"
)

// usaspendingRequestBody is the JSON body sent to the USAspending search API.
var usaspendingRequestBody = []byte(`{
	"subawards": false,
	"limit": 50,
	"page": 1,
	"filters": {
		"award_type_codes": ["A","B","C","D"]
	},
	"fields": [
		"internal_id",
		"Award ID",
		"Recipient Name",
		"Award Amount",
		"Award Type",
		"Start Date",
		"Awarding Agency"
	],
	"sort": "Award Amount",
	"order": "desc"
}`)

// USAspendingSource polls the USAspending API for recent government contract awards.
type USAspendingSource struct {
	client *http.Client
}

// NewUSAspendingSource returns a USAspendingSource using the provided HTTP client.
// If client is nil, http.DefaultClient is used.
func NewUSAspendingSource(client *http.Client) *USAspendingSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &USAspendingSource{client: client}
}

// Name implements ingestion.Source.
func (u *USAspendingSource) Name() string { return usaspendingSourceName }

// Poll fetches recent government contract awards from USAspending.gov.
// Implements ingestion.Source.
func (u *USAspendingSource) Poll(ctx context.Context) ([]db.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, usaspendingBaseURL,
		bytes.NewReader(usaspendingRequestBody))
	if err != nil {
		return nil, fmt.Errorf("usaspending: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usaspending: executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usaspending: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("usaspending: reading response body: %w", err)
	}

	events, err := ParseUSAspending(body)
	if err != nil {
		return nil, fmt.Errorf("usaspending: parsing response: %w", err)
	}
	return events, nil
}

// usaspendingResult is a single award record from the USAspending search response.
// Field names match the API's JSON keys exactly, including spaces.
type usaspendingResult struct {
	InternalID     int64   `json:"internal_id"`
	AwardID        string  `json:"Award ID"`
	RecipientName  string  `json:"Recipient Name"`
	AwardAmount    float64 `json:"Award Amount"`
	AwardType      string  `json:"Award Type"`
	StartDate      string  `json:"Start Date"`
	AwardingAgency string  `json:"Awarding Agency"`
}

// usaspendingResponse is the top-level envelope from the USAspending search endpoint.
type usaspendingResponse struct {
	Results []json.RawMessage `json:"results"`
}

// ParseUSAspending parses the raw USAspending JSON search response and returns one
// db.Event per award result with EventType "government_contract".
// This function is pure and safe to call independently of any HTTP transport.
func ParseUSAspending(data []byte) ([]db.Event, error) {
	var resp usaspendingResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("usaspending: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(resp.Results))
	for i, raw := range resp.Results {
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("usaspending: result[%d]: %w", i, err)
		}

		// Decode only the fields we need for event metadata.
		var result usaspendingResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("usaspending: decoding result[%d]: %w", i, err)
		}

		occurredAt := time.Now().UTC()
		if result.StartDate != "" {
			if t, err := time.Parse("2006-01-02", result.StartDate); err == nil {
				occurredAt = t.UTC()
			}
		}

		events = append(events, db.Event{
			Source:     usaspendingSourceName,
			SourceID:   sourceID(usaspendingSourceName, result.AwardID),
			EventType:  "government_contract",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
