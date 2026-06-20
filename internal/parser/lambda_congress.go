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
	lambdaCongressSourceName = "lambda_congress"
	lambdaRecentURL          = "https://www.lambdafin.com/api/congressional/recent"
)

// LambdaCongressSource polls the Lambda Finance congressional trades endpoint.
type LambdaCongressSource struct {
	client *http.Client
	apiKey string
}

// NewLambdaCongressSource returns a LambdaCongressSource. If client is nil,
// http.DefaultClient is used.
func NewLambdaCongressSource(client *http.Client, apiKey string) *LambdaCongressSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &LambdaCongressSource{
		client: client,
		apiKey: apiKey,
	}
}

// Name implements ingestion.Source.
func (l *LambdaCongressSource) Name() string { return lambdaCongressSourceName }

// Poll fetches recent congressional trades from Lambda Finance and returns
// the parsed events.
func (l *LambdaCongressSource) Poll(ctx context.Context) ([]db.Event, error) {
	url := fmt.Sprintf("%s?apikey=%s&days=365", lambdaRecentURL, l.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("lambda_congress: building request for %s: %w", redactKey(url, l.apiKey), err)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lambda_congress: executing request for %s: %w", redactKey(url, l.apiKey), err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &HTTPStatusError{Source: lambdaCongressSourceName, StatusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("lambda_congress: reading response: %w", err)
	}

	return ParseLambdaCongress(body)
}

// lambdaCongressResponse is the top-level envelope from the Lambda Finance
// congressional/recent endpoint.
type lambdaCongressResponse struct {
	Trades []lambdaCongressRecord `json:"trades"`
	Count  int                    `json:"count"`
}

// lambdaCongressRecord is one trade record from the Lambda Finance
// congressional/recent endpoint. Field names match the actual API response
// verified 2026-06-20.
type lambdaCongressRecord struct {
	Symbol           string  `json:"symbol"`
	Representative   string  `json:"representative"`
	TransactionDate  string  `json:"transactionDate"`
	DisclosureDate   string  `json:"disclosureDate"`
	Type             string  `json:"type"`
	Amount           string  `json:"amount"`
	Chamber          string  `json:"chamber"`
	Party            *string `json:"party"`
	State            string  `json:"state"`
	District         string  `json:"district"`
	AssetDescription string  `json:"assetDescription"`
	Owner            string  `json:"owner"`
	PtrLink          string  `json:"ptrLink"`
}

// lambdaCongressEventData is the event_data payload stored per trade.
type lambdaCongressEventData struct {
	Symbol           string `json:"symbol"`
	Representative   string `json:"representative"`
	TransactionDate  string `json:"transaction_date"`
	DisclosureDate   string `json:"disclosure_date"`
	TradeType        string `json:"trade_type"`
	Amount           string `json:"amount"`
	AmountLow        int    `json:"amount_low"`
	AmountHigh       int    `json:"amount_high"`
	Chamber          string `json:"chamber"`
	Party            string `json:"party"`
	State            string `json:"state"`
	District         string `json:"district"`
	AssetDescription string `json:"asset_description"`
	Owner            string `json:"owner"`
	PtrLink          string `json:"ptr_link"`
}

// ParseLambdaCongress parses raw JSON from the Lambda Finance congressional
// trades endpoint and returns one db.Event per trade. This function is pure
// and safe to call independently of any HTTP transport.
func ParseLambdaCongress(data []byte) ([]db.Event, error) {
	var resp lambdaCongressResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("lambda_congress: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(resp.Trades))
	for _, rec := range resp.Trades {
		low, high := ParseFMPAmountRange(rec.Amount)

		party := ""
		if rec.Party != nil {
			party = *rec.Party
		}

		payload := lambdaCongressEventData{
			Symbol:           rec.Symbol,
			Representative:   rec.Representative,
			TransactionDate:  rec.TransactionDate,
			DisclosureDate:   rec.DisclosureDate,
			TradeType:        rec.Type,
			Amount:           rec.Amount,
			AmountLow:        low,
			AmountHigh:       high,
			Chamber:          rec.Chamber,
			Party:            party,
			State:            rec.State,
			District:         rec.District,
			AssetDescription: rec.AssetDescription,
			Owner:            rec.Owner,
			PtrLink:          rec.PtrLink,
		}

		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("lambda_congress: marshal trade %s %s: %w",
				rec.Representative, rec.Symbol, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("lambda_congress: trade %s %s: %w",
				rec.Representative, rec.Symbol, err)
		}

		// OccurredAt: prefer transactionDate, fall back to disclosureDate.
		dateStr := rec.TransactionDate
		if dateStr == "" {
			dateStr = rec.DisclosureDate
		}
		occurredAt := time.Now().UTC()
		if dateStr != "" {
			if t, err := time.Parse("2006-01-02", dateStr); err == nil {
				occurredAt = t.UTC()
			}
		}

		events = append(events, db.Event{
			Source: lambdaCongressSourceName,
			SourceID: sourceID(
				lambdaCongressSourceName,
				rec.Representative,
				rec.Symbol,
				rec.TransactionDate,
				rec.Type,
			),
			EventType:  "congressional_trade",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
