package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFinnhubCongress(t *testing.T) {
	data, err := os.ReadFile("testdata/finnhub_congress_sample.json")
	require.NoError(t, err)

	events, err := parser.ParseFinnhubCongress(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	// First event — Pelosi / AAPL purchase
	e0 := events[0]
	assert.Equal(t, "finnhub_congress", e0.Source)
	assert.NotNil(t, e0.SourceID)
	assert.NotEmpty(t, *e0.SourceID)
	assert.Equal(t, "congressional_trade", e0.EventType)
	assert.Equal(t, time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), e0.OccurredAt)
	require.NoError(t, parser.ValidateEventData(e0.EventData))
	assert.Contains(t, string(e0.EventData), `"Nancy Pelosi"`)
	assert.Contains(t, string(e0.EventData), `"AAPL"`)
	assert.Contains(t, string(e0.EventData), `"Purchase"`)

	// Second event — Tuberville / MSFT sale
	e1 := events[1]
	assert.Equal(t, "finnhub_congress", e1.Source)
	assert.NotNil(t, e1.SourceID)
	assert.Equal(t, "congressional_trade", e1.EventType)
	assert.Equal(t, time.Date(2025, 2, 28, 0, 0, 0, 0, time.UTC), e1.OccurredAt)
	assert.Contains(t, string(e1.EventData), `"MSFT"`)
	assert.Contains(t, string(e1.EventData), `"Sale"`)

	// Source IDs must be distinct
	assert.NotEqual(t, *e0.SourceID, *e1.SourceID)
}

func TestParseFinnhubCongressEmpty(t *testing.T) {
	data := []byte(`{"data":[],"symbol":"AAPL"}`)

	events, err := parser.ParseFinnhubCongress(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseFinnhubCongressSkipsMissingDate(t *testing.T) {
	// transactionDate is empty — should fall back to filingDate.
	data := []byte(`{
		"data": [
			{
				"amountFrom": 1001,
				"amountTo": 15000,
				"assetName": "Apple Inc",
				"filingDate": "2025-04-01",
				"name": "Jane Doe",
				"ownerType": "self",
				"position": "Representative",
				"symbol": "AAPL",
				"transactionDate": "",
				"transactionType": "Purchase"
			}
		],
		"symbol": "AAPL"
	}`)

	events, err := parser.ParseFinnhubCongress(data)
	require.NoError(t, err)
	require.Len(t, events, 1)

	// OccurredAt should be the filingDate, not zero/now.
	assert.Equal(t, time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC), events[0].OccurredAt)
}
