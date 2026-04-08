package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEdgarForm4_SingleTransaction(t *testing.T) {
	data := []byte(`{
		"hits": {
			"hits": [{
				"_source": {
					"file_num": "0001234567-24-000001",
					"display_names": ["DOE JOHN"],
					"form_type": "4",
					"file_date": "2024-01-15",
					"period_of_report": "2024-01-12",
					"entity_name": "Apple Inc"
				}
			}]
		}
	}`)

	events, err := parser.ParseEdgarForm4(data)
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, "edgar_form4", e.Source)
	assert.NotNil(t, e.SourceID)
	assert.NotEmpty(t, *e.SourceID)
	assert.Equal(t, "insider_trade", e.EventType)
	assert.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), e.OccurredAt)

	require.NoError(t, parser.ValidateEventData(e.EventData))
	assert.Contains(t, string(e.EventData), `"file_num"`)
	assert.Contains(t, string(e.EventData), `"Apple Inc"`)
}

func TestParseEdgarForm4_MultipleTransactions(t *testing.T) {
	data, err := os.ReadFile("testdata/edgar_form4_sample.json")
	require.NoError(t, err)

	events, err := parser.ParseEdgarForm4(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	// All events must have distinct source_ids.
	require.NotNil(t, events[0].SourceID)
	require.NotNil(t, events[1].SourceID)
	assert.NotEqual(t, *events[0].SourceID, *events[1].SourceID)

	for _, e := range events {
		assert.Equal(t, "edgar_form4", e.Source)
		assert.Equal(t, "insider_trade", e.EventType)
		assert.False(t, e.OccurredAt.IsZero())
		require.NoError(t, parser.ValidateEventData(e.EventData))
	}
}

func TestParseEdgarForm4_EmptyResults(t *testing.T) {
	data := []byte(`{"hits": {"hits": []}}`)

	events, err := parser.ParseEdgarForm4(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseEdgarForm4_MalformedJSON(t *testing.T) {
	data := []byte(`{not valid json`)

	_, err := parser.ParseEdgarForm4(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParseEdgarForm4_SourceIDIsDeterministic(t *testing.T) {
	data := []byte(`{
		"hits": {
			"hits": [{
				"_source": {
					"file_num": "0001111111-24-000099",
					"display_names": ["SMITH JANE"],
					"form_type": "4",
					"file_date": "2024-05-01",
					"period_of_report": "2024-04-30",
					"entity_name": "Tesla Inc"
				}
			}]
		}
	}`)

	events1, err := parser.ParseEdgarForm4(data)
	require.NoError(t, err)

	events2, err := parser.ParseEdgarForm4(data)
	require.NoError(t, err)

	require.NotNil(t, events1[0].SourceID)
	require.NotNil(t, events2[0].SourceID)
	assert.Equal(t, *events1[0].SourceID, *events2[0].SourceID)
}

func TestParseEdgarForm4_MissingFileDateFallsBackToNow(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	data := []byte(`{
		"hits": {
			"hits": [{
				"_source": {
					"file_num": "0001111111-24-000001",
					"display_names": ["X Y"],
					"form_type": "4",
					"file_date": "",
					"period_of_report": "",
					"entity_name": "Some Corp"
				}
			}]
		}
	}`)

	events, err := parser.ParseEdgarForm4(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.True(t, events[0].OccurredAt.After(before),
		"expected OccurredAt to fall back to ~now when file_date is empty")
}
