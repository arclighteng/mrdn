package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePolygonDaily_ValidBar(t *testing.T) {
	data, err := os.ReadFile("testdata/polygon_daily_sample.json")
	require.NoError(t, err)

	events, err := parser.ParsePolygonDaily(data)
	require.NoError(t, err)
	require.Len(t, events, 3)

	e := events[0]
	assert.Equal(t, "polygon", e.Source)
	assert.NotNil(t, e.SourceID)
	assert.NotEmpty(t, *e.SourceID)
	assert.Equal(t, "market_daily", e.EventType)
	assert.False(t, e.OccurredAt.IsZero())

	// Timestamp 1705276800000 ms = 2024-01-15 00:00:00 UTC
	assert.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), e.OccurredAt)

	require.NoError(t, parser.ValidateEventData(e.EventData))
	assert.Contains(t, string(e.EventData), `"T"`)
	assert.Contains(t, string(e.EventData), `"AAPL"`)
}

func TestParsePolygonDaily_EmptyResults(t *testing.T) {
	data := []byte(`{"resultsCount": 0, "results": []}`)

	events, err := parser.ParsePolygonDaily(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParsePolygonDaily_NullResults(t *testing.T) {
	// Polygon returns null results on non-trading days.
	data := []byte(`{"resultsCount": 0, "results": null}`)

	events, err := parser.ParsePolygonDaily(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParsePolygonDaily_MalformedJSON(t *testing.T) {
	data := []byte(`{not valid json`)

	_, err := parser.ParsePolygonDaily(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParsePolygonDaily_SourceIDIsDeterministic(t *testing.T) {
	data := []byte(`{
		"resultsCount": 1,
		"results": [{"T": "AAPL", "o": 100, "h": 110, "l": 99, "c": 105, "v": 5000000, "t": 1705276800000}]
	}`)

	events1, err := parser.ParsePolygonDaily(data)
	require.NoError(t, err)

	events2, err := parser.ParsePolygonDaily(data)
	require.NoError(t, err)

	require.NotNil(t, events1[0].SourceID)
	require.NotNil(t, events2[0].SourceID)
	assert.Equal(t, *events1[0].SourceID, *events2[0].SourceID)
}

func TestParsePolygonDaily_SourceIDDiffersAcrossTickers(t *testing.T) {
	data := []byte(`{
		"resultsCount": 2,
		"results": [
			{"T": "AAPL", "o": 100, "h": 110, "l": 99, "c": 105, "v": 1000, "t": 1705276800000},
			{"T": "MSFT", "o": 200, "h": 210, "l": 199, "c": 205, "v": 2000, "t": 1705276800000}
		]
	}`)

	events, err := parser.ParsePolygonDaily(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	require.NotNil(t, events[0].SourceID)
	require.NotNil(t, events[1].SourceID)
	assert.NotEqual(t, *events[0].SourceID, *events[1].SourceID,
		"source_id must differ across tickers on the same date")
}
