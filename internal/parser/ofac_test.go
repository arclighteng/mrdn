package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOFAC_ValidEntry(t *testing.T) {
	data, err := os.ReadFile("testdata/ofac_sample.json")
	require.NoError(t, err)

	events, err := parser.ParseOFAC(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	e := events[0]
	assert.Equal(t, "ofac_sdn", e.Source)
	assert.NotNil(t, e.SourceID)
	assert.NotEmpty(t, *e.SourceID)
	assert.Equal(t, "sanction_designation", e.EventType)
	assert.False(t, e.OccurredAt.IsZero())
	assert.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), e.OccurredAt)

	// EventData must be valid JSON and contain the expected fields.
	require.NoError(t, parser.ValidateEventData(e.EventData))
	assert.Contains(t, string(e.EventData), `"uid"`)
	assert.Contains(t, string(e.EventData), `"sdnType"`)
}

func TestParseOFAC_WithAliases(t *testing.T) {
	data := []byte(`{
		"sdnList": [{
			"uid": 99,
			"firstName": "ALIAS",
			"lastName": "TEST",
			"sdnType": "Individual",
			"programs": ["SDGT"],
			"dateAdded": "2024-06-01",
			"aliases": [
				{"firstName": "A", "lastName": "TEST"},
				{"firstName": "AL", "lastName": "TEST"}
			]
		}]
	}`)

	events, err := parser.ParseOFAC(data)
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, "sanction_designation", e.EventType)
	// aliases array must be present in EventData
	assert.Contains(t, string(e.EventData), `"aliases"`)
	assert.Equal(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), e.OccurredAt)
}

func TestParseOFAC_EmptyList(t *testing.T) {
	data := []byte(`{"sdnList": []}`)

	events, err := parser.ParseOFAC(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseOFAC_MalformedJSON(t *testing.T) {
	data := []byte(`{not valid json`)

	_, err := parser.ParseOFAC(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParseOFAC_MissingDateFallsBackToNow(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	data := []byte(`{
		"sdnList": [{
			"uid": 1,
			"firstName": "X",
			"lastName": "Y",
			"sdnType": "Individual",
			"programs": ["SDGT"],
			"dateAdded": "",
			"aliases": []
		}]
	}`)

	events, err := parser.ParseOFAC(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	// OccurredAt should be approximately now (within a generous window).
	assert.True(t, events[0].OccurredAt.After(before),
		"expected OccurredAt to fall back to ~now when dateAdded is empty")
}

func TestParseOFAC_SourceIDIsDeterministic(t *testing.T) {
	data := []byte(`{
		"sdnList": [{
			"uid": 42,
			"firstName": "Foo",
			"lastName": "Bar",
			"sdnType": "Individual",
			"programs": ["SDGT"],
			"dateAdded": "2024-01-01",
			"aliases": []
		}]
	}`)

	events1, err := parser.ParseOFAC(data)
	require.NoError(t, err)

	events2, err := parser.ParseOFAC(data)
	require.NoError(t, err)

	require.NotNil(t, events1[0].SourceID)
	require.NotNil(t, events2[0].SourceID)
	assert.Equal(t, *events1[0].SourceID, *events2[0].SourceID)
}
