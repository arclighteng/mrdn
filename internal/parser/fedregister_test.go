package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFedRegister_TariffRule(t *testing.T) {
	data, err := os.ReadFile("testdata/fedregister_sample.json")
	require.NoError(t, err)

	events, err := parser.ParseFedRegister(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	e := events[0]
	assert.Equal(t, "federal_register", e.Source)
	assert.NotNil(t, e.SourceID)
	assert.NotEmpty(t, *e.SourceID)
	assert.Equal(t, "regulatory_action", e.EventType)
	assert.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), e.OccurredAt)

	require.NoError(t, parser.ValidateEventData(e.EventData))
	assert.Contains(t, string(e.EventData), `"document_number"`)
	assert.Contains(t, string(e.EventData), `"2024-00123"`)
	assert.Contains(t, string(e.EventData), `"Tariff Adjustment on Steel Imports"`)
}

func TestParseFedRegister_ExecutiveOrder(t *testing.T) {
	// Simulate an executive order type document.
	data := []byte(`{"results": [{
		"document_number": "2024-EO-001",
		"title": "Strengthening National Cybersecurity",
		"type": "Rule",
		"abstract": "Executive directive on federal cybersecurity posture.",
		"publication_date": "2024-05-10",
		"agencies": [{"name": "Executive Office of the President"}]
	}]}`)

	events, err := parser.ParseFedRegister(data)
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, "federal_register", e.Source)
	assert.Equal(t, "regulatory_action", e.EventType)
	assert.Equal(t, time.Date(2024, 5, 10, 0, 0, 0, 0, time.UTC), e.OccurredAt)
	require.NoError(t, parser.ValidateEventData(e.EventData))
}

func TestParseFedRegister_EmptyResults(t *testing.T) {
	data := []byte(`{"results": []}`)

	events, err := parser.ParseFedRegister(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseFedRegister_MalformedJSON(t *testing.T) {
	data := []byte(`{not valid json`)

	_, err := parser.ParseFedRegister(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParseFedRegister_MissingDateFallsBackToNow(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	data := []byte(`{"results": [{
		"document_number": "2024-NO-DATE",
		"title": "Undated Rule",
		"type": "Rule",
		"abstract": "Missing publication date.",
		"publication_date": "",
		"agencies": [{"name": "Some Agency"}]
	}]}`)

	events, err := parser.ParseFedRegister(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.True(t, events[0].OccurredAt.After(before),
		"expected OccurredAt to fall back to ~now when publication_date is empty")
}

func TestParseFedRegister_SourceIDIsDeterministic(t *testing.T) {
	data := []byte(`{"results": [{
		"document_number": "2024-00123",
		"title": "Tariff Adjustment on Steel Imports",
		"type": "Rule",
		"abstract": "Some abstract.",
		"publication_date": "2024-01-15",
		"agencies": [{"name": "Department of Commerce"}]
	}]}`)

	events1, err := parser.ParseFedRegister(data)
	require.NoError(t, err)

	events2, err := parser.ParseFedRegister(data)
	require.NoError(t, err)

	require.NotNil(t, events1[0].SourceID)
	require.NotNil(t, events2[0].SourceID)
	assert.Equal(t, *events1[0].SourceID, *events2[0].SourceID)
}
