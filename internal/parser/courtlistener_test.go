package parser_test

import (
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCourtListener(t *testing.T) {
	data, err := os.ReadFile("testdata/courtlistener_disclosures_sample.json")
	require.NoError(t, err)

	events, next, err := parser.ParseCourtListener(data)
	require.NoError(t, err)

	// The fixture has 2 investments but only AAPL (id=99001) has a transaction;
	// VOO (id=99002) has an empty transaction_during_reporting_period and is skipped.
	require.Len(t, events, 1)
	assert.Nil(t, next, "next page URL should be nil")

	e := events[0]
	assert.Equal(t, "courtlistener", e.Source)
	assert.NotNil(t, e.SourceID)
	assert.NotEmpty(t, *e.SourceID)
	assert.Equal(t, "judicial_disclosure", e.EventType)

	// OccurredAt should be the transaction_date from the fixture: 2024-06-15
	assert.Equal(t, time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC), e.OccurredAt)

	require.NoError(t, parser.ValidateEventData(e.EventData))
	assert.Contains(t, string(e.EventData), `"disclosure_id":34211`)
	assert.Contains(t, string(e.EventData), `"investment_id":99001`)
	assert.Contains(t, string(e.EventData), `"transaction_type":"Purchase"`)
	assert.Contains(t, string(e.EventData), `"transaction_date":"2024-06-15"`)
}

func TestParseCourtListenerEmpty(t *testing.T) {
	data := []byte(`{"count":0,"next":null,"previous":null,"results":[]}`)

	events, next, err := parser.ParseCourtListener(data)
	require.NoError(t, err)
	assert.Empty(t, events)
	assert.Nil(t, next)
}

func TestCourtListenerValueCode(t *testing.T) {
	cases := []struct {
		code string
		low  int
		high int
	}{
		{"J", 15_000, 50_000},
		{"K", 50_000, 100_000},
		{"N", 500_000, 1_000_000},
		{"P1", 5_000_000, 25_000_000},
		{"P3", 50_000_000, 0},   // no upper bound
		{"ZZ", 0, 0},            // unknown code
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.code, func(t *testing.T) {
			low, high := parser.CourtListenerValueRange(tc.code)
			assert.Equal(t, tc.low, low, "low mismatch for code %s", tc.code)
			assert.Equal(t, tc.high, high, "high mismatch for code %s", tc.code)
		})
	}
}
