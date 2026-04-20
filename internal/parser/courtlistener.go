package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	courtListenerSourceName = "courtlistener"
	courtListenerBaseURL    = "https://www.courtlistener.com/api/rest/v4/financial-disclosures/"
	courtListenerPollQuery  = "?ordering=-date_created&has_been_extracted=true&limit=20"
)

// clTickerRe extracts a ticker symbol from parentheses in an investment description,
// e.g. "Apple Inc (AAPL) - Stock" → "AAPL".
var clTickerRe = regexp.MustCompile(`\(([A-Z]{1,5})\)`)

// CourtListenerSource polls the CourtListener API for federal judge financial
// disclosures.
type CourtListenerSource struct {
	client *http.Client
	token  string
}

// NewCourtListenerSource returns a CourtListenerSource using the provided HTTP
// client and API token. If client is nil, http.DefaultClient is used.
func NewCourtListenerSource(client *http.Client, token string) *CourtListenerSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &CourtListenerSource{client: client, token: token}
}

// Name implements ingestion.Source.
func (c *CourtListenerSource) Name() string { return courtListenerSourceName }

// Poll fetches the first page of recently-extracted financial disclosures and
// returns parsed judicial disclosure events. Implements ingestion.Source.
func (c *CourtListenerSource) Poll(ctx context.Context) ([]db.Event, error) {
	url := courtListenerBaseURL + courtListenerPollQuery

	var all []db.Event
	for url != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("courtlistener: building request: %w", err)
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Token "+c.token)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("courtlistener: executing request: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, &HTTPStatusError{Source: courtListenerSourceName, StatusCode: resp.StatusCode}
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("courtlistener: reading response body: %w", err)
		}

		events, next, err := ParseCourtListener(body)
		if err != nil {
			return nil, fmt.Errorf("courtlistener: parsing response: %w", err)
		}
		all = append(all, events...)

		if next != nil {
			url = *next
		} else {
			url = ""
		}
	}

	return all, nil
}

// clResponse is the top-level paginated envelope from the CourtListener
// financial-disclosures endpoint.
type clResponse struct {
	Count   int          `json:"count"`
	Next    *string      `json:"next"`
	Results []clDisclosure `json:"results"`
}

// clDisclosure is a single financial disclosure record.
type clDisclosure struct {
	ID          int           `json:"id"`
	Person      string        `json:"person"`
	Year        int           `json:"year"`
	Investments []clInvestment `json:"investments"`
}

// clInvestment is a single investment line item within a disclosure.
type clInvestment struct {
	ID              int    `json:"id"`
	Description     string `json:"description"`
	Redacted        bool   `json:"redacted"`
	GrossValueCode  string `json:"gross_value_code"`
	TxType          string `json:"transaction_during_reporting_period"`
	TxDate          *string `json:"transaction_date"`
	TxValueCode     string `json:"transaction_value_code"`
}

// clEventData is the shape stored in event_data JSON for each judicial
// disclosure investment event.
type clEventData struct {
	DisclosureID int    `json:"disclosure_id"`
	InvestmentID int    `json:"investment_id"`
	PersonURL    string `json:"person_url"`
	Year         int    `json:"year"`
	Description  string `json:"description"`
	TxType       string `json:"transaction_type"`
	TxDate       string `json:"transaction_date"`
	TxValueCode  string `json:"transaction_value_code"`
	GrossCode    string `json:"gross_value_code"`
}

// ParseCourtListener parses raw JSON from the CourtListener financial-disclosures
// endpoint. It returns one db.Event per investment that has a non-empty
// transaction_during_reporting_period and is not redacted, plus the next page
// URL (nil if there is none). This function is pure and safe to call
// independently of any HTTP transport.
func ParseCourtListener(data []byte) ([]db.Event, *string, error) {
	var page clResponse
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, nil, fmt.Errorf("courtlistener: unmarshal: %w", err)
	}

	var events []db.Event
	for _, disc := range page.Results {
		discIDStr := strconv.Itoa(disc.ID)
		for _, inv := range disc.Investments {
			// Only create events for actual transactions; skip holdings-only rows.
			if inv.TxType == "" {
				continue
			}
			// Skip redacted entries — they contain no usable data.
			if inv.Redacted {
				continue
			}

			txDateStr := ""
			if inv.TxDate != nil {
				txDateStr = *inv.TxDate
			}

			payload := clEventData{
				DisclosureID: disc.ID,
				InvestmentID: inv.ID,
				PersonURL:    disc.Person,
				Year:         disc.Year,
				Description:  inv.Description,
				TxType:       inv.TxType,
				TxDate:       txDateStr,
				TxValueCode:  inv.TxValueCode,
				GrossCode:    inv.GrossValueCode,
			}

			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, nil, fmt.Errorf("courtlistener: re-marshal disclosure_id=%d investment_id=%d: %w",
					disc.ID, inv.ID, err)
			}
			if err := ValidateEventData(raw); err != nil {
				return nil, nil, fmt.Errorf("courtlistener: disclosure_id=%d investment_id=%d: %w",
					disc.ID, inv.ID, err)
			}

			occurredAt := time.Now().UTC()
			if txDateStr != "" {
				if t, err := time.Parse("2006-01-02", txDateStr); err == nil {
					occurredAt = t.UTC()
				}
			}

			invIDStr := strconv.Itoa(inv.ID)
			events = append(events, db.Event{
				Source:     courtListenerSourceName,
				SourceID:   sourceID("courtlistener", discIDStr, invIDStr),
				EventType:  "judicial_disclosure",
				EventData:  raw,
				OccurredAt: occurredAt,
			})
		}
	}

	return events, page.Next, nil
}

// CourtListenerValueRange decodes a CourtListener investment value code into an
// inclusive dollar range [low, high]. P3 has no upper bound so high is set to
// 0. An unknown code returns (0, 0).
func CourtListenerValueRange(code string) (low, high int) {
	switch code {
	case "J":
		return 15_000, 50_000
	case "K":
		return 50_000, 100_000
	case "L":
		return 100_000, 250_000
	case "M":
		return 250_000, 500_000
	case "N":
		return 500_000, 1_000_000
	case "O":
		return 1_000_000, 5_000_000
	case "P1":
		return 5_000_000, 25_000_000
	case "P2":
		return 25_000_000, 50_000_000
	case "P3":
		return 50_000_000, 0 // no upper bound
	default:
		return 0, 0
	}
}

// courtListenerExtractTicker extracts a ticker symbol from an investment
// description such as "Apple Inc (AAPL) - Stock". Returns an empty string if
// no ticker is found.
func courtListenerExtractTicker(description string) string {
	m := clTickerRe.FindStringSubmatch(description)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
