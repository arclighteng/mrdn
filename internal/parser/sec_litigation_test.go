package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSECLitigation(t *testing.T) {
	raw := []byte(`{
		"releases": [
			{
				"id": "LR-25832",
				"date": "2025-04-01",
				"title": "SEC Charges Acme Corp for Securities Fraud",
				"url": "https://www.sec.gov/litigation/litreleases/2025/lr25832.htm"
			},
			{
				"id": "LR-25833",
				"date": "2025-04-02",
				"title": "SEC Files Action Against John Doe and Widget Inc.",
				"url": "https://www.sec.gov/litigation/litreleases/2025/lr25833.htm"
			}
		]
	}`)

	events, err := ParseSECLitigation(raw)
	require.NoError(t, err)
	require.Len(t, events, 2)

	evt := events[0]
	assert.Equal(t, "sec_edgar_lit", evt.Source)
	assert.Equal(t, "sec_litigation", evt.EventType)
	assert.NotNil(t, evt.SourceID)
}

func TestParseSECLitigation_EmptyReleases(t *testing.T) {
	raw := []byte(`{"releases": []}`)
	events, err := ParseSECLitigation(raw)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseSECLitigation_InvalidJSON(t *testing.T) {
	_, err := ParseSECLitigation([]byte(`not json`))
	assert.Error(t, err)
}

func TestParseSECLitigation_FallbackSourceID(t *testing.T) {
	raw := []byte(`{"releases": [{"title": "Some Release", "date": "2025-01-01"}]}`)
	events, err := ParseSECLitigation(raw)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.NotNil(t, events[0].SourceID)
}
