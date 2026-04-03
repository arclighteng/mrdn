package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEFDS_ValidFiling(t *testing.T) {
	data, err := os.ReadFile("testdata/efds_sample.xml")
	require.NoError(t, err)

	events, err := parser.ParseEFDS(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	e := events[0]
	assert.Equal(t, "senate_efds", e.Source)
	assert.NotNil(t, e.SourceID)
	assert.NotEmpty(t, *e.SourceID)
	assert.Equal(t, "congressional_disclosure", e.EventType)
	assert.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), e.OccurredAt)

	require.NoError(t, parser.ValidateEventData(e.EventData))
	assert.Contains(t, string(e.EventData), `"report_id"`)
	assert.Contains(t, string(e.EventData), `"abc-123"`)
}

func TestParseEFDS_PartialFiling(t *testing.T) {
	// Filing with only required fields — no last_name, no filing_type.
	data := []byte(`<filings>
		<filing>
			<first_name>Alice</first_name>
			<filing_date>06/01/2024</filing_date>
			<report_id>partial-001</report_id>
		</filing>
	</filings>`)

	events, err := parser.ParseEFDS(data)
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, "senate_efds", e.Source)
	assert.Equal(t, "congressional_disclosure", e.EventType)
	assert.Equal(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), e.OccurredAt)
	require.NoError(t, parser.ValidateEventData(e.EventData))
}

func TestParseEFDS_EmptyFilings(t *testing.T) {
	data := []byte(`<filings></filings>`)

	events, err := parser.ParseEFDS(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseEFDS_MalformedXML(t *testing.T) {
	data := []byte(`<filings><filing><first_name>Broken`)

	_, err := parser.ParseEFDS(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParseEFDS_MissingDateFallsBackToNow(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	data := []byte(`<filings>
		<filing>
			<first_name>Bob</first_name>
			<last_name>JONES</last_name>
			<filing_type>Annual</filing_type>
			<filing_date></filing_date>
			<report_id>no-date-001</report_id>
		</filing>
	</filings>`)

	events, err := parser.ParseEFDS(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.True(t, events[0].OccurredAt.After(before),
		"expected OccurredAt to fall back to ~now when filing_date is empty")
}

func TestParseEFDS_SourceIDIsDeterministic(t *testing.T) {
	data := []byte(`<filings>
		<filing>
			<first_name>Jane</first_name>
			<last_name>DOE</last_name>
			<filing_type>Annual</filing_type>
			<filing_date>01/15/2024</filing_date>
			<report_id>dedup-999</report_id>
		</filing>
	</filings>`)

	events1, err := parser.ParseEFDS(data)
	require.NoError(t, err)

	events2, err := parser.ParseEFDS(data)
	require.NoError(t, err)

	require.NotNil(t, events1[0].SourceID)
	require.NotNil(t, events2[0].SourceID)
	assert.Equal(t, *events1[0].SourceID, *events2[0].SourceID)
}
