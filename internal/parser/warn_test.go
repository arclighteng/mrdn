package parser_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWarnCA_ValidEntries(t *testing.T) {
	data, err := os.ReadFile("testdata/warn_ca_sample.html")
	require.NoError(t, err)

	events, err := parser.ParseWarnCA(data)
	require.NoError(t, err)
	require.Len(t, events, 3)

	// First entry: Acme Technologies Inc.
	e := events[0]
	assert.Equal(t, "warn", e.Source)
	assert.NotNil(t, e.SourceID)
	assert.NotEmpty(t, *e.SourceID)
	assert.Equal(t, "warn_filing", e.EventType)
	assert.Equal(t, time.Date(2024, 12, 15, 0, 0, 0, 0, time.UTC), e.OccurredAt)
	require.NoError(t, parser.ValidateEventData(e.EventData))
	assert.Contains(t, string(e.EventData), `"company"`)
	assert.Contains(t, string(e.EventData), `Acme Technologies Inc.`)
	assert.Contains(t, string(e.EventData), `"employees_affected":250`)
	assert.Contains(t, string(e.EventData), `"state":"CA"`)
	assert.Contains(t, string(e.EventData), `"city":"San Francisco"`)
	assert.Contains(t, string(e.EventData), `Layoff Permanent`)

	// Second entry: Pacific Manufacturing LLC
	e2 := events[1]
	assert.Equal(t, "warn", e2.Source)
	assert.Equal(t, time.Date(2024, 11, 20, 0, 0, 0, 0, time.UTC), e2.OccurredAt)
	assert.Contains(t, string(e2.EventData), `Pacific Manufacturing LLC`)
	assert.Contains(t, string(e2.EventData), `"employees_affected":150`)

	// Third entry: Golden State Retail Corp
	e3 := events[2]
	assert.Equal(t, time.Date(2024, 10, 5, 0, 0, 0, 0, time.UTC), e3.OccurredAt)
	assert.Contains(t, string(e3.EventData), `Golden State Retail Corp`)
	assert.Contains(t, string(e3.EventData), `"employees_affected":85`)
}

func TestParseWarnCA_EmptyTable(t *testing.T) {
	data := []byte(`<html><body>
		<table border="1">
		<tr><th>Notice Date</th><th>Effective Date</th><th>Received Date</th>
		<th>Company</th><th>City</th><th>County</th><th>No. Of Employees</th><th>Layoff/Closure</th></tr>
		</table></body></html>`)

	events, err := parser.ParseWarnCA(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseWarnCA_MalformedHTML(t *testing.T) {
	data := []byte(`<html><body>no table here</body></html>`)

	events, err := parser.ParseWarnCA(data)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseWarnCA_SourceIDIsDeterministic(t *testing.T) {
	data, err := os.ReadFile("testdata/warn_ca_sample.html")
	require.NoError(t, err)

	events1, err := parser.ParseWarnCA(data)
	require.NoError(t, err)

	events2, err := parser.ParseWarnCA(data)
	require.NoError(t, err)

	require.NotNil(t, events1[0].SourceID)
	require.NotNil(t, events2[0].SourceID)
	assert.Equal(t, *events1[0].SourceID, *events2[0].SourceID)
}

func TestWarnSource_Name(t *testing.T) {
	src := parser.NewWarnSource(nil)
	assert.Equal(t, "warn", src.Name())
}

func TestWarnSource_Poll(t *testing.T) {
	caData, err := os.ReadFile("testdata/warn_ca_sample.html")
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(caData)
	}))
	defer srv.Close()

	src := parser.NewWarnSourceWithURLs(srv.Client(), map[string]string{
		"CA": srv.URL,
	})

	events, err := src.Poll(context.Background())
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Equal(t, "warn", events[0].Source)
	assert.Equal(t, "warn_filing", events[0].EventType)
}
