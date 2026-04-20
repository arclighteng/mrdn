package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFMPCongress(t *testing.T) {
	data, err := os.ReadFile("testdata/fmp_congress_sample.json")
	require.NoError(t, err)

	events, err := parser.ParseFMPCongress(data, "senate")
	require.NoError(t, err)
	require.Len(t, events, 2)

	// First event — Boozman / MSFT purchase
	e0 := events[0]
	assert.Equal(t, "fmp_congress", e0.Source)
	assert.NotNil(t, e0.SourceID)
	assert.NotEmpty(t, *e0.SourceID)
	assert.Equal(t, "congressional_trade", e0.EventType)
	assert.Equal(t, time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), e0.OccurredAt)
	require.NoError(t, parser.ValidateEventData(e0.EventData))
	assert.Contains(t, string(e0.EventData), `"MSFT"`)
	assert.Contains(t, string(e0.EventData), `"senate"`)
	assert.Contains(t, string(e0.EventData), `"Purchase"`)
	assert.Contains(t, string(e0.EventData), `"amount_low":1001`)
	assert.Contains(t, string(e0.EventData), `"amount_high":15000`)

	// Second event — Fetterman / GOOGL purchase
	e1 := events[1]
	assert.Equal(t, "fmp_congress", e1.Source)
	assert.NotNil(t, e1.SourceID)
	assert.Equal(t, "congressional_trade", e1.EventType)
	assert.Equal(t, time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC), e1.OccurredAt)
	assert.Contains(t, string(e1.EventData), `"GOOGL"`)
	assert.Contains(t, string(e1.EventData), `"senate"`)

	// Source IDs must be distinct.
	assert.NotEqual(t, *e0.SourceID, *e1.SourceID)
}

func TestParseFMPCongressEmpty(t *testing.T) {
	events, err := parser.ParseFMPCongress([]byte(`[]`), "senate")
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseFMPCongressFallbackToDisclosureDate(t *testing.T) {
	// transactionDate is empty — OccurredAt should fall back to disclosureDate.
	data := []byte(`[{
		"symbol": "AAPL",
		"disclosureDate": "2026-04-14",
		"transactionDate": "",
		"firstName": "Jane",
		"lastName": "Doe",
		"office": "Jane Doe",
		"district": "CA",
		"owner": "Self",
		"assetDescription": "Apple Inc",
		"assetType": "Stock",
		"type": "Purchase",
		"amount": "$1,001 - $15,000",
		"comment": "",
		"link": ""
	}]`)

	events, err := parser.ParseFMPCongress(data, "house")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC), events[0].OccurredAt)
	assert.Contains(t, string(events[0].EventData), `"house"`)
}

func TestParseFMPAmountRange(t *testing.T) {
	tests := []struct {
		input       string
		wantLow     int
		wantHigh    int
	}{
		{"$1,001 - $15,000", 1001, 15000},
		{"$100,001 - $250,000", 100001, 250000},
		{"$15,001 - $50,000", 15001, 50000},
		{"$50,001 - $100,000", 50001, 100000},
		{"", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			low, high := parser.ParseFMPAmountRange(tt.input)
			assert.Equal(t, tt.wantLow, low)
			assert.Equal(t, tt.wantHigh, high)
		})
	}
}
