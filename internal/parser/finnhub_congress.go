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
	finnhubCongressSourceName = "finnhub_congress"
	finnhubCongressBaseURL    = "https://finnhub.io/api/v1/stock/congressional-trading"

	// finnhubCongressLookback is how far back to request trades on each poll.
	finnhubCongressLookback = 90 * 24 * time.Hour

	// finnhubCongressSleep is the inter-request delay to respect Finnhub's
	// 30 req/sec rate limit.
	finnhubCongressSleep = 50 * time.Millisecond
)

// TickerLister can return the list of tickers that should be polled.
type TickerLister interface {
	ListTickers(ctx context.Context) ([]string, error)
}

// FinnhubCongressSource polls the Finnhub congressional-trading REST endpoint
// for each ticker returned by a TickerLister.
type FinnhubCongressSource struct {
	client  *http.Client
	apiKey  string
	tickers TickerLister
}

// NewFinnhubCongressSource returns a FinnhubCongressSource. If client is nil,
// http.DefaultClient is used.
func NewFinnhubCongressSource(client *http.Client, apiKey string, tickers TickerLister) *FinnhubCongressSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &FinnhubCongressSource{
		client:  client,
		apiKey:  apiKey,
		tickers: tickers,
	}
}

// Name implements ingestion.Source.
func (f *FinnhubCongressSource) Name() string { return finnhubCongressSourceName }

// Poll iterates over all tickers from the TickerLister, fetches congressional
// trading data for each, and returns deduplicated events. A 50 ms sleep is
// inserted between requests to respect Finnhub's rate limit.
func (f *FinnhubCongressSource) Poll(ctx context.Context) ([]db.Event, error) {
	syms, err := f.tickers.ListTickers(ctx)
	if err != nil {
		return nil, fmt.Errorf("finnhub_congress: listing tickers: %w", err)
	}

	now := time.Now().UTC()
	from := now.Add(-finnhubCongressLookback).Format("2006-01-02")
	to := now.Format("2006-01-02")

	var all []db.Event
	for i, sym := range syms {
		if i > 0 {
			select {
			case <-ctx.Done():
				return all, ctx.Err()
			case <-time.After(finnhubCongressSleep):
			}
		}

		url := fmt.Sprintf("%s?symbol=%s&from=%s&to=%s&token=%s",
			finnhubCongressBaseURL, sym, from, to, f.apiKey)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("finnhub_congress: building request for %s: %w", sym, err)
		}

		resp, err := f.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("finnhub_congress: executing request for %s: %w", sym, err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, &HTTPStatusError{Source: finnhubCongressSourceName, StatusCode: resp.StatusCode}
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("finnhub_congress: reading response for %s: %w", sym, err)
		}

		events, err := ParseFinnhubCongress(body)
		if err != nil {
			return nil, fmt.Errorf("finnhub_congress: parsing response for %s: %w", sym, err)
		}
		all = append(all, events...)
	}

	return all, nil
}

// finnhubCongressResponse is the top-level JSON envelope from the
// /stock/congressional-trading endpoint.
type finnhubCongressResponse struct {
	Data   []finnhubCongressTrade `json:"data"`
	Symbol string                 `json:"symbol"`
}

// finnhubCongressTrade is one trade record within the response.
type finnhubCongressTrade struct {
	AmountFrom      float64 `json:"amountFrom"`
	AmountTo        float64 `json:"amountTo"`
	AssetName       string  `json:"assetName"`
	FilingDate      string  `json:"filingDate"`
	Name            string  `json:"name"`
	OwnerType       string  `json:"ownerType"`
	Position        string  `json:"position"`
	Symbol          string  `json:"symbol"`
	TransactionDate string  `json:"transactionDate"`
	TransactionType string  `json:"transactionType"`
}

// ParseFinnhubCongress parses raw JSON from the Finnhub congressional-trading
// endpoint and returns one db.Event per trade. This function is pure and safe
// to call independently of any HTTP transport.
func ParseFinnhubCongress(data []byte) ([]db.Event, error) {
	var resp finnhubCongressResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("finnhub_congress: unmarshal: %w", err)
	}

	events := make([]db.Event, 0, len(resp.Data))
	for _, trade := range resp.Data {
		raw, err := json.Marshal(trade)
		if err != nil {
			return nil, fmt.Errorf("finnhub_congress: re-marshal trade name=%s symbol=%s: %w",
				trade.Name, trade.Symbol, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("finnhub_congress: trade name=%s symbol=%s: %w",
				trade.Name, trade.Symbol, err)
		}

		// OccurredAt: prefer transactionDate, fall back to filingDate.
		dateStr := trade.TransactionDate
		if dateStr == "" {
			dateStr = trade.FilingDate
		}
		occurredAt := time.Now().UTC()
		if dateStr != "" {
			if t, err := time.Parse("2006-01-02", dateStr); err == nil {
				occurredAt = t.UTC()
			}
		}

		events = append(events, db.Event{
			Source: finnhubCongressSourceName,
			SourceID: sourceID(
				finnhubCongressSourceName,
				trade.Name,
				trade.Symbol,
				trade.TransactionDate,
				trade.TransactionType,
			),
			EventType:  "congressional_trade",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
