package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFEC_ValidContribution(t *testing.T) {
	data, err := os.ReadFile("testdata/fec_sample.json")
	require.NoError(t, err)

	events, err := parser.ParseFEC(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	e := events[0]
	assert.Equal(t, "fec", e.Source)
	assert.NotNil(t, e.SourceID)
	assert.NotEmpty(t, *e.SourceID)
	assert.Equal(t, "political_contribution", e.EventType)
	assert.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), e.OccurredAt)

	require.NoError(t, parser.ValidateEventData(e.EventData))
	assert.Contains(t, string(e.EventData), `"sub_id"`)
	assert.Contains(t, string(e.EventData), `"4012345678"`)
}

func TestParseFEC_EmptyResults(t *testing.T) {
	data := []byte(`{"results": []}`)

	events, err := parser.ParseFEC(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseFEC_MalformedJSON(t *testing.T) {
	data := []byte(`{not valid json`)

	_, err := parser.ParseFEC(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParseFEC_MissingDateFallsBackToNow(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	data := []byte(`{"results": [{
		"sub_id": "9999999999",
		"contributor_name": "TEST, USER",
		"contribution_receipt_amount": 500.00,
		"contribution_receipt_date": "",
		"committee_name": "SOME COMMITTEE",
		"contributor_employer": "Self",
		"contributor_occupation": "Engineer"
	}]}`)

	events, err := parser.ParseFEC(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.True(t, events[0].OccurredAt.After(before),
		"expected OccurredAt to fall back to ~now when contribution_receipt_date is empty")
}

func TestParseFEC_SourceIDIsDeterministic(t *testing.T) {
	data := []byte(`{"results": [{
		"sub_id": "4012345678",
		"contributor_name": "DOE, JOHN",
		"contribution_receipt_amount": 2800.00,
		"contribution_receipt_date": "2024-01-15",
		"committee_name": "FRIENDS OF SENATOR X",
		"contributor_employer": "ACME Corp",
		"contributor_occupation": "CEO"
	}]}`)

	events1, err := parser.ParseFEC(data)
	require.NoError(t, err)

	events2, err := parser.ParseFEC(data)
	require.NoError(t, err)

	require.NotNil(t, events1[0].SourceID)
	require.NotNil(t, events2[0].SourceID)
	assert.Equal(t, *events1[0].SourceID, *events2[0].SourceID)
}

func TestParseFEC_CSVSanitizationStripsLeadingFormulaChars(t *testing.T) {
	data := []byte(`{"results": [{
		"sub_id": "1111111111",
		"contributor_name": "=CMD|'/C calc'!A0",
		"contribution_receipt_amount": 100.00,
		"contribution_receipt_date": "2024-04-01",
		"committee_name": "+DANGEROUS COMMITTEE",
		"contributor_employer": "Evil Corp",
		"contributor_occupation": "Hacker"
	}]}`)

	events, err := parser.ParseFEC(data)
	require.NoError(t, err)
	require.Len(t, events, 1)

	// Verify injected fields were stripped in the stored EventData.
	eventStr := string(events[0].EventData)
	assert.NotContains(t, eventStr, `"=CMD`)
	assert.NotContains(t, eventStr, `"+DANGEROUS`)
	// Legitimate content after the prefix char must be retained.
	assert.Contains(t, eventStr, `CMD`)
	assert.Contains(t, eventStr, `DANGEROUS COMMITTEE`)
}

func TestSanitizeCSVField(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"=FORMULA", "FORMULA"},
		{"+FORMULA", "FORMULA"},
		{"-FORMULA", "FORMULA"},
		{"@FORMULA", "FORMULA"},
		{"Normal Name", "Normal Name"},
		{"", ""},
		{"===triple", "triple"},
		{"ALLCAPS", "ALLCAPS"},
	}

	for _, tc := range tests {
		got := parser.SanitizeCSVField(tc.input)
		assert.Equal(t, tc.want, got, "SanitizeCSVField(%q)", tc.input)
	}
}
