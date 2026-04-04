package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUSAspending_ValidAward(t *testing.T) {
	data, err := os.ReadFile("testdata/usaspending_sample.json")
	require.NoError(t, err)

	events, err := parser.ParseUSAspending(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	e := events[0]
	assert.Equal(t, "usaspending", e.Source)
	assert.NotNil(t, e.SourceID)
	assert.NotEmpty(t, *e.SourceID)
	assert.Equal(t, "government_contract", e.EventType)
	assert.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), e.OccurredAt)

	require.NoError(t, parser.ValidateEventData(e.EventData))
	assert.Contains(t, string(e.EventData), `"Award ID"`)
	assert.Contains(t, string(e.EventData), `"W911NF2410001"`)
}

func TestParseUSAspending_EmptyResults(t *testing.T) {
	data := []byte(`{"results": []}`)

	events, err := parser.ParseUSAspending(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseUSAspending_MalformedJSON(t *testing.T) {
	data := []byte(`{not valid json`)

	_, err := parser.ParseUSAspending(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParseUSAspending_MissingDateFallsBackToNow(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	data := []byte(`{"results": [{
		"internal_id": 99,
		"Award ID": "TEST0001",
		"Recipient Name": "No Date Corp",
		"Award Amount": 100000.00,
		"Award Type": "Contract",
		"Start Date": "",
		"Awarding Agency": "Test Agency"
	}]}`)

	events, err := parser.ParseUSAspending(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.True(t, events[0].OccurredAt.After(before),
		"expected OccurredAt to fall back to ~now when Start Date is empty")
}

func TestParseUSAspending_SourceIDIsDeterministic(t *testing.T) {
	data := []byte(`{"results": [{
		"internal_id": 12345,
		"Award ID": "W911NF2410001",
		"Recipient Name": "ACME Corp",
		"Award Amount": 5000000.00,
		"Award Type": "Contract",
		"Start Date": "2024-01-15",
		"Awarding Agency": "Department of Defense"
	}]}`)

	events1, err := parser.ParseUSAspending(data)
	require.NoError(t, err)

	events2, err := parser.ParseUSAspending(data)
	require.NoError(t, err)

	require.NotNil(t, events1[0].SourceID)
	require.NotNil(t, events2[0].SourceID)
	assert.Equal(t, *events1[0].SourceID, *events2[0].SourceID)
}

func TestParseUSAspending_MultipleAwardsHaveDistinctSourceIDs(t *testing.T) {
	data, err := os.ReadFile("testdata/usaspending_sample.json")
	require.NoError(t, err)

	events, err := parser.ParseUSAspending(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	require.NotNil(t, events[0].SourceID)
	require.NotNil(t, events[1].SourceID)
	assert.NotEqual(t, *events[0].SourceID, *events[1].SourceID)
}
