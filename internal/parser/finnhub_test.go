package parser_test

import (
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFinnhubTrade_ValidTrade(t *testing.T) {
	data := []byte(`{
		"type": "trade",
		"data": [
			{"s": "AAPL", "p": 150.25, "v": 100, "t": 1705276800000, "c": ["1","12"]}
		]
	}`)

	events, err := parser.ParseFinnhubTrade(data)
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, "finnhub", e.Source)
	assert.NotNil(t, e.SourceID)
	assert.NotEmpty(t, *e.SourceID)
	assert.Equal(t, "market_trade", e.EventType)
	assert.False(t, e.OccurredAt.IsZero())

	// Timestamp 1705276800000 ms = 2024-01-15 00:00:00 UTC
	assert.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), e.OccurredAt)

	require.NoError(t, parser.ValidateEventData(e.EventData))
	assert.Contains(t, string(e.EventData), `"AAPL"`)
	assert.Contains(t, string(e.EventData), `150.25`)
}

func TestParseFinnhubTrade_MultipleTrades(t *testing.T) {
	data := []byte(`{
		"type": "trade",
		"data": [
			{"s": "AAPL", "p": 150.25, "v": 100, "t": 1705276800000, "c": ["1"]},
			{"s": "MSFT", "p": 370.10, "v": 50,  "t": 1705276801000, "c": []},
			{"s": "GOOG", "p": 140.00, "v": 200, "t": 1705276802000, "c": null}
		]
	}`)

	events, err := parser.ParseFinnhubTrade(data)
	require.NoError(t, err)
	require.Len(t, events, 3)

	assert.Equal(t, "finnhub", events[0].Source)
	assert.Equal(t, "finnhub", events[1].Source)
	assert.Equal(t, "finnhub", events[2].Source)

	assert.Contains(t, string(events[0].EventData), `"AAPL"`)
	assert.Contains(t, string(events[1].EventData), `"MSFT"`)
	assert.Contains(t, string(events[2].EventData), `"GOOG"`)

	// Source IDs must be distinct across different symbols/timestamps.
	require.NotNil(t, events[0].SourceID)
	require.NotNil(t, events[1].SourceID)
	require.NotNil(t, events[2].SourceID)
	assert.NotEqual(t, *events[0].SourceID, *events[1].SourceID)
	assert.NotEqual(t, *events[1].SourceID, *events[2].SourceID)
}

func TestParseFinnhubTrade_PingMessage(t *testing.T) {
	data := []byte(`{"type":"ping"}`)

	events, err := parser.ParseFinnhubTrade(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseFinnhubTrade_MalformedJSON(t *testing.T) {
	data := []byte(`{not valid json`)

	_, err := parser.ParseFinnhubTrade(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParseFinnhubTrade_UnknownType(t *testing.T) {
	data := []byte(`{"type":"heartbeat","data":null}`)

	events, err := parser.ParseFinnhubTrade(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseFinnhubTrade_SourceIDIsDeterministic(t *testing.T) {
	data := []byte(`{
		"type": "trade",
		"data": [{"s": "AAPL", "p": 150.00, "v": 10, "t": 1705276800000, "c": []}]
	}`)

	events1, err := parser.ParseFinnhubTrade(data)
	require.NoError(t, err)

	events2, err := parser.ParseFinnhubTrade(data)
	require.NoError(t, err)

	require.NotNil(t, events1[0].SourceID)
	require.NotNil(t, events2[0].SourceID)
	assert.Equal(t, *events1[0].SourceID, *events2[0].SourceID)
}
