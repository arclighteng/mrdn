package parser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

const (
	warnSourceName = "warn"

	// Hardcoded URLs to prevent SSRF. These are the official state WARN notice pages.
	warnURLCA = "https://edd.ca.gov/en/jobs_and_training/layoff_services_warn"
	warnURLTX = "https://www.twc.texas.gov/businesses/worker-adjustment-and-retraining-notification-warn-notices"
	warnURLNY = "https://dol.ny.gov/warn-notices"
	warnURLIL = "https://www.illinoisworknet.com/LayoffRecovery/Pages/WARNReport.aspx"
	warnURLFL = "https://floridajobs.org/workforce-statistics/workforce-statistics-data-releases/latest-warn-notices"
)

// warnStateParseFn is the function signature for per-state WARN parsers.
type warnStateParseFn func(data []byte) ([]db.Event, error)

// warnStateConfig holds URL and parser for a single state.
type warnStateConfig struct {
	url   string
	parse warnStateParseFn
}

// WarnSource polls state WARN Act notice pages for layoff filing events.
type WarnSource struct {
	client *http.Client
	states map[string]warnStateConfig
}

// NewWarnSource returns a WarnSource using the provided HTTP client.
// If client is nil, http.DefaultClient is used.
func NewWarnSource(client *http.Client) *WarnSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &WarnSource{
		client: client,
		states: map[string]warnStateConfig{
			"CA": {url: warnURLCA, parse: ParseWarnCA},
			"TX": {url: warnURLTX, parse: parseWarnStub},
			"NY": {url: warnURLNY, parse: parseWarnStub},
			"IL": {url: warnURLIL, parse: parseWarnStub},
			"FL": {url: warnURLFL, parse: parseWarnStub},
		},
	}
}

// NewWarnSourceWithURLs returns a WarnSource with custom URLs for testing.
// Only states present in the urls map are polled.
func NewWarnSourceWithURLs(client *http.Client, urls map[string]string) *WarnSource {
	if client == nil {
		client = http.DefaultClient
	}

	parsers := map[string]warnStateParseFn{
		"CA": ParseWarnCA,
		"TX": parseWarnStub,
		"NY": parseWarnStub,
		"IL": parseWarnStub,
		"FL": parseWarnStub,
	}

	states := make(map[string]warnStateConfig, len(urls))
	for st, u := range urls {
		pf := parseWarnStub
		if fn, ok := parsers[st]; ok {
			pf = fn
		}
		states[st] = warnStateConfig{url: u, parse: pf}
	}

	return &WarnSource{client: client, states: states}
}

// Name implements ingestion.Source.
func (w *WarnSource) Name() string { return warnSourceName }

// Poll fetches WARN notice pages for all configured states and returns parsed events.
// Implements ingestion.Source.
func (w *WarnSource) Poll(ctx context.Context) ([]db.Event, error) {
	var allEvents []db.Event

	for state, cfg := range w.states {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.url, nil)
		if err != nil {
			return nil, fmt.Errorf("warn[%s]: building request: %w", state, err)
		}

		resp, err := w.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("warn[%s]: executing request: %w", state, err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("warn[%s]: reading response body: %w", state, err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("warn[%s]: unexpected status %d", state, resp.StatusCode)
		}

		events, err := cfg.parse(body)
		if err != nil {
			return nil, fmt.Errorf("warn[%s]: parsing response: %w", state, err)
		}
		allEvents = append(allEvents, events...)
	}

	return allEvents, nil
}

// warnCAFiling represents a single California WARN notice filing.
type warnCAFiling struct {
	NoticeDate    string `json:"notice_date"`
	EffectiveDate string `json:"effective_date"`
	ReceivedDate  string `json:"received_date"`
	Company       string `json:"company"`
	City          string `json:"city"`
	County        string `json:"county"`
	Employees     int    `json:"employees_affected"`
	LayoffClosure string `json:"layoff_closure"`
	State         string `json:"state"`
}

// ParseWarnCA parses the raw California EDD WARN HTML page and returns one
// db.Event per notice with EventType "warn_filing".
// This function is pure and safe to call independently of any HTTP transport.
func ParseWarnCA(data []byte) ([]db.Event, error) {
	rows := extractTableRows(data)
	if len(rows) == 0 {
		return nil, nil
	}

	var events []db.Event
	for _, cells := range rows {
		if len(cells) < 8 {
			continue
		}

		employees, _ := strconv.Atoi(strings.TrimSpace(cells[6]))

		filing := warnCAFiling{
			NoticeDate:    strings.TrimSpace(cells[0]),
			EffectiveDate: strings.TrimSpace(cells[1]),
			ReceivedDate:  strings.TrimSpace(cells[2]),
			Company:       strings.TrimSpace(cells[3]),
			City:          strings.TrimSpace(cells[4]),
			County:        strings.TrimSpace(cells[5]),
			Employees:     employees,
			LayoffClosure: strings.TrimSpace(cells[7]),
			State:         "CA",
		}

		raw, err := json.Marshal(filing)
		if err != nil {
			return nil, fmt.Errorf("warn_ca: marshaling filing company=%s: %w", filing.Company, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("warn_ca: filing company=%s: %w", filing.Company, err)
		}

		occurredAt := time.Now().UTC()
		if filing.NoticeDate != "" {
			if t, err := time.Parse("01/02/2006", filing.NoticeDate); err == nil {
				occurredAt = t.UTC()
			}
		}

		sid := sourceID(warnSourceName, "CA", filing.Company, filing.NoticeDate)

		events = append(events, db.Event{
			Source:     warnSourceName,
			SourceID:   sid,
			EventType:  "warn_filing",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}

	return events, nil
}

// parseWarnStub is a placeholder parser for states not yet implemented.
// Returns nil, nil to indicate no events parsed without error.
func parseWarnStub(_ []byte) ([]db.Event, error) {
	return nil, nil
}

// extractTableRows parses an HTML table and returns the cell text for each
// data row (skipping the header row). This is a lightweight parser that handles
// the simple table structures used by state WARN pages without requiring an
// external HTML parsing dependency.
func extractTableRows(data []byte) [][]string {
	tableStart := bytes.Index(data, []byte("<table"))
	if tableStart < 0 {
		return nil
	}
	tableEnd := bytes.Index(data[tableStart:], []byte("</table>"))
	if tableEnd < 0 {
		return nil
	}
	tableHTML := data[tableStart : tableStart+tableEnd+len("</table>")]

	// Split into rows.
	rawRows := splitTag(tableHTML, "tr")
	if len(rawRows) == 0 {
		return nil
	}

	var result [][]string
	for i, row := range rawRows {
		// Skip header row (contains <th> elements).
		if i == 0 && bytes.Contains(row, []byte("<th")) {
			continue
		}

		cells := splitTag(row, "td")
		if len(cells) == 0 {
			continue
		}

		textCells := make([]string, len(cells))
		for j, cell := range cells {
			textCells[j] = stripTags(cell)
		}
		result = append(result, textCells)
	}

	return result
}

// splitTag extracts the inner content of each occurrence of <tag>...</tag>.
func splitTag(data []byte, tag string) [][]byte {
	openTag := "<" + tag
	closeTag := "</" + tag + ">"
	var parts [][]byte

	remaining := data
	for {
		start := bytes.Index(remaining, []byte(openTag))
		if start < 0 {
			break
		}
		// Find the end of the opening tag (handle attributes).
		tagClose := bytes.IndexByte(remaining[start:], '>')
		if tagClose < 0 {
			break
		}
		contentStart := start + tagClose + 1

		end := bytes.Index(remaining[contentStart:], []byte(closeTag))
		if end < 0 {
			break
		}

		parts = append(parts, remaining[contentStart:contentStart+end])
		remaining = remaining[contentStart+end+len(closeTag):]
	}

	return parts
}

// stripTags removes HTML tags from a fragment and decodes common HTML entities.
func stripTags(data []byte) string {
	var buf bytes.Buffer
	inTag := false
	for _, b := range data {
		switch {
		case b == '<':
			inTag = true
		case b == '>':
			inTag = false
		case !inTag:
			buf.WriteByte(b)
		}
	}
	s := buf.String()
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return strings.TrimSpace(s)
}
