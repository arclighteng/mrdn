package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	ogeSourceName = "oge"
	// ogeBaseURL is the OGE (Office of Government Ethics) executive branch
	// financial disclosure REST API. DataTables server-side endpoint.
	// Hardcoded to prevent SSRF; no API key required (public data).
	ogeBaseURL = "https://extapps2.oge.gov/201/Presiden.nsf/API.xsp/v2/rest"
	// ogePageSize is the number of records to fetch per request.
	ogePageSize = 100
	// ogeMaxPages caps the number of pages fetched per poll cycle.
	// At 100 records/page, 200 pages covers all ~16,715 records.
	ogeMaxPages = 200
	// ogePageDelay is the delay between paginated requests to respect rate limits.
	ogePageDelay = 500 * time.Millisecond
)

// ogeLinkRe extracts the href URL from the HTML link field returned by the OGE API.
// Example: <a href="https://...pdf">View</a>
var ogeLinkRe = regexp.MustCompile(`href="([^"]+)"`)

// OGESource polls the OGE executive branch financial disclosure API.
type OGESource struct {
	client *http.Client
}

// NewOGESource returns an OGESource using the provided HTTP client.
// If client is nil, http.DefaultClient is used.
func NewOGESource(client *http.Client) *OGESource {
	if client == nil {
		client = http.DefaultClient
	}
	return &OGESource{client: client}
}

// Name implements ingestion.Source.
func (o *OGESource) Name() string { return ogeSourceName }

// Poll fetches executive branch financial disclosures from the OGE API with
// pagination and returns parsed events. Implements ingestion.Source.
func (o *OGESource) Poll(ctx context.Context) ([]db.Event, error) {
	var all []db.Event
	draw := 1

	for page := 0; page < ogeMaxPages; page++ {
		start := page * ogePageSize
		url := fmt.Sprintf("%s?draw=%d&start=%d&length=%d", ogeBaseURL, draw, start, ogePageSize)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("oge: building request: %w", err)
		}

		resp, err := o.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("oge: executing request (page %d): %w", page, err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, &HTTPStatusError{Source: ogeSourceName, StatusCode: resp.StatusCode}
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("oge: reading response body (page %d): %w", page, err)
		}

		events, totalRecords, err := ParseOGE(body)
		if err != nil {
			return nil, fmt.Errorf("oge: parsing response (page %d): %w", page, err)
		}
		all = append(all, events...)

		// Stop if we've fetched all records.
		if start+ogePageSize >= totalRecords {
			break
		}

		draw++

		// Rate-limit delay between pages.
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		case <-time.After(ogePageDelay):
		}
	}

	return all, nil
}

// ogeResponse is the top-level DataTables server-side response from the OGE API.
type ogeResponse struct {
	Draw            int         `json:"draw"`
	RecordsTotal    int         `json:"recordsTotal"`
	RecordsFiltered int         `json:"recordsFiltered"`
	Data            []ogeRecord `json:"data"`
}

// ogeRecord is a single disclosure record from the OGE API.
type ogeRecord struct {
	Name    string `json:"name"`
	Agency  string `json:"agency"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	DocDate string `json:"docDate"`
	Link    string `json:"link"` // HTML anchor tag containing the PDF URL
}

// ogeEventData is the shape stored in event_data JSON for each executive
// disclosure event.
type ogeEventData struct {
	Name           string `json:"name"`
	Agency         string `json:"agency"`
	Title          string `json:"title"`
	DisclosureType string `json:"disclosure_type"`
	DocDate        string `json:"doc_date"`
	PDFURL         string `json:"pdf_url"`
}

// ParseOGE parses raw JSON from the OGE DataTables API. It returns one db.Event
// per disclosure record plus the total number of records for pagination control.
func ParseOGE(data []byte) ([]db.Event, int, error) {
	var resp ogeResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, 0, fmt.Errorf("oge: unmarshal: %w", err)
	}

	var events []db.Event
	for _, rec := range resp.Data {
		pdfURL := extractOGELink(rec.Link)

		payload := ogeEventData{
			Name:           rec.Name,
			Agency:         rec.Agency,
			Title:          rec.Title,
			DisclosureType: rec.Type,
			DocDate:        rec.DocDate,
			PDFURL:         pdfURL,
		}

		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("oge: re-marshal record %q: %w", rec.Name, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, 0, fmt.Errorf("oge: record %q: %w", rec.Name, err)
		}

		occurredAt := time.Now().UTC()
		if rec.DocDate != "" {
			// Try common date formats the OGE API might use.
			for _, layout := range []string{
				"01/02/2006",
				"1/2/2006",
				"2006-01-02",
			} {
				if t, err := time.Parse(layout, rec.DocDate); err == nil {
					occurredAt = t.UTC()
					break
				}
			}
		}

		// Deduplicate by name + docDate + type composite key.
		sid := sourceID("oge", rec.Name, rec.DocDate, rec.Type)

		events = append(events, db.Event{
			Source:     ogeSourceName,
			SourceID:   sid,
			EventType:  "executive_disclosure",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}

	return events, resp.RecordsTotal, nil
}

// extractOGELink extracts the href URL from an HTML anchor tag.
// Returns an empty string if no URL is found.
func extractOGELink(html string) string {
	m := ogeLinkRe.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
