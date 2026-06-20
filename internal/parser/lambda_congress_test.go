package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLambdaCongress(t *testing.T) {
	data, err := os.ReadFile("testdata/lambda_congress_sample.json")
	require.NoError(t, err)

	events, err := parser.ParseLambdaCongress(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	// First event — Pelosi / AAPL purchase
	e0 := events[0]
	assert.Equal(t, "lambda_congress", e0.Source)
	assert.NotNil(t, e0.SourceID)
	assert.NotEmpty(t, *e0.SourceID)
	assert.Equal(t, "congressional_trade", e0.EventType)
	assert.Equal(t, time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC), e0.OccurredAt)
	require.NoError(t, parser.ValidateEventData(e0.EventData))
	assert.Contains(t, string(e0.EventData), `"AAPL"`)
	assert.Contains(t, string(e0.EventData), `"house"`)
	assert.Contains(t, string(e0.EventData), `"purchase"`)
	assert.Contains(t, string(e0.EventData), `"amount_low":1001`)
	assert.Contains(t, string(e0.EventData), `"amount_high":15000`)
	assert.Contains(t, string(e0.EventData), `"Nancy Pelosi"`)

	// Second event — Tuberville / NVDA sale
	e1 := events[1]
	assert.Equal(t, "lambda_congress", e1.Source)
	assert.NotNil(t, e1.SourceID)
	assert.Equal(t, "congressional_trade", e1.EventType)
	assert.Equal(t, time.Date(2025, 6, 20, 0, 0, 0, 0, time.UTC), e1.OccurredAt)
	assert.Contains(t, string(e1.EventData), `"NVDA"`)
	assert.Contains(t, string(e1.EventData), `"senate"`)
	assert.Contains(t, string(e1.EventData), `"amount_low":100001`)
	assert.Contains(t, string(e1.EventData), `"amount_high":250000`)

	// Source IDs must be distinct.
	assert.NotEqual(t, *e0.SourceID, *e1.SourceID)
}

func TestParseLambdaCongressEmpty(t *testing.T) {
	events, err := parser.ParseLambdaCongress([]byte(`{"trades":[],"count":0,"days":30}`))
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseLambdaCongressFallbackToDisclosureDate(t *testing.T) {
	data := []byte(`{"trades":[{
		"symbol": "MSFT",
		"representative": "Jane Doe",
		"transactionDate": "",
		"disclosureDate": "2025-08-01",
		"type": "purchase",
		"amount": "$15,001 - $50,000",
		"chamber": "senate",
		"party": "Democrat",
		"state": "NY",
		"district": null,
		"assetDescription": "Microsoft Corp",
		"owner": "Self",
		"ptrLink": "",
		"capGainsOver200": false,
		"comment": null
	}],"count":1,"days":30}`)

	events, err := parser.ParseLambdaCongress(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC), events[0].OccurredAt)
	assert.Contains(t, string(events[0].EventData), `"senate"`)
}

func TestParseLambdaCongressAmountRange(t *testing.T) {
	data := []byte(`{"trades":[{
		"symbol": "TSLA",
		"representative": "Test User",
		"transactionDate": "2025-05-01",
		"disclosureDate": "2025-05-15",
		"type": "purchase",
		"amount": "$50,001 - $100,000",
		"chamber": "house",
		"party": "Republican",
		"state": "TX",
		"district": "03",
		"assetDescription": "Tesla Inc",
		"owner": "Joint",
		"ptrLink": "",
		"capGainsOver200": false,
		"comment": null
	}],"count":1,"days":30}`)

	events, err := parser.ParseLambdaCongress(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Contains(t, string(events[0].EventData), `"amount_low":50001`)
	assert.Contains(t, string(events[0].EventData), `"amount_high":100000`)
}

func TestParseLambdaCongressNullParty(t *testing.T) {
	// party can be null in the API response.
	data := []byte(`{"trades":[{
		"symbol": "GOOG",
		"representative": "Someone",
		"transactionDate": "2025-05-01",
		"disclosureDate": "2025-05-15",
		"type": "Purchase",
		"amount": "$1,001 - $15,000",
		"chamber": "house",
		"party": null,
		"state": "IN",
		"district": "06",
		"assetDescription": "Alphabet",
		"owner": "Self",
		"ptrLink": "",
		"capGainsOver200": false,
		"comment": null
	}],"count":1,"days":30}`)

	events, err := parser.ParseLambdaCongress(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Contains(t, string(events[0].EventData), `"party":""`)
}

func TestParseLambdaCongressNilFields(t *testing.T) {
	// Fields entirely absent from JSON — should not panic.
	data := []byte(`{"trades":[{"symbol": "GOOG"}],"count":1,"days":30}`)

	events, err := parser.ParseLambdaCongress(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "lambda_congress", events[0].Source)
	assert.Contains(t, string(events[0].EventData), `"GOOG"`)
}
