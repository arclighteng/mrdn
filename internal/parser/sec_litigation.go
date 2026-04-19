package parser

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	secLitSourceName = "sec_edgar_lit"
	// secLitBaseURL is the SEC litigation releases RSS feed.
	// Hardcoded to prevent SSRF; no API key required (public data).
	secLitBaseURL = "https://www.sec.gov/enforcement-litigation/litigation-releases/rss"
)

// SECLitigationSource polls the SEC litigation releases RSS feed.
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

// Poll fetches recent SEC litigation releases from the RSS feed.
// Implements ingestion.Source.
func (s *SECLitigationSource) Poll(ctx context.Context) ([]db.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secLitBaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("sec_lit: building request: %w", err)
	}
	req.Header.Set("User-Agent", "mrdn/1.0 (data-pipeline)")

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

// secLitRSS mirrors the RSS 2.0 feed structure from sec.gov.
type secLitRSS struct {
	XMLName xml.Name       `xml:"rss"`
	Channel secLitChannel  `xml:"channel"`
}

type secLitChannel struct {
	Items []secLitItem `xml:"item"`
}

type secLitItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
	Creator string `xml:"http://purl.org/dc/elements/1.1/ creator"`
	GUID    string `xml:"guid"`
}

// secLitEventData is the JSON shape stored in event_data.
// Must match secLitEvent in the resolver.
type secLitEventData struct {
	ID    string `json:"id"`
	Date  string `json:"date"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// ParseSECLitigation parses the SEC litigation releases RSS feed and returns
// one db.Event per item with EventType "sec_litigation".
func ParseSECLitigation(data []byte) ([]db.Event, error) {
	var rss secLitRSS
	if err := xml.Unmarshal(data, &rss); err != nil {
		return nil, fmt.Errorf("sec_lit: unmarshal rss: %w", err)
	}

	events := make([]db.Event, 0, len(rss.Channel.Items))
	for _, item := range rss.Channel.Items {
		// dc:creator holds the LR number (e.g., "LR-26531").
		lrID := strings.TrimSpace(item.Creator)
		if lrID == "" {
			lrID = strings.TrimSpace(item.Title) + "|" + strings.TrimSpace(item.PubDate)
		}

		occurredAt := time.Now().UTC()
		if item.PubDate != "" {
			if t, err := time.Parse(time.RFC1123Z, strings.TrimSpace(item.PubDate)); err == nil {
				occurredAt = t.UTC()
			} else if t, err := time.Parse(time.RFC1123, strings.TrimSpace(item.PubDate)); err == nil {
				occurredAt = t.UTC()
			}
		}

		eventData := secLitEventData{
			ID:    lrID,
			Date:  occurredAt.Format("2006-01-02"),
			Title: strings.TrimSpace(item.Title),
			URL:   strings.TrimSpace(item.Link),
		}

		raw, err := json.Marshal(eventData)
		if err != nil {
			return nil, fmt.Errorf("sec_lit: marshaling event data: %w", err)
		}

		events = append(events, db.Event{
			Source:     secLitSourceName,
			SourceID:   sourceID(secLitSourceName, lrID),
			EventType:  "sec_litigation",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
