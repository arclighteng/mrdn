package resolver

import (
	"encoding/json"
	"testing"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFixture_EFDS verifies the parser→resolver JSON contract.
func TestFixture_EFDS(t *testing.T) {
	xml := []byte(`<filings><filing>
		<first_name>Nancy</first_name>
		<last_name>Pelosi</last_name>
		<filing_type>Periodic Transaction Report</filing_type>
		<filing_date>01/15/2025</filing_date>
		<report_id>abc123</report_id>
	</filing></filings>`)

	events, err := parser.ParseEFDS(xml)
	require.NoError(t, err)
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal(t, "efds_senate", evt.Source)
	assert.Equal(t, "congressional_disclosure", evt.EventType)

	// Verify the resolver can unmarshal the parser's output.
	var disc efdsDisclosure
	err = json.Unmarshal(evt.EventData, &disc)
	require.NoError(t, err)
	assert.Equal(t, "Nancy", disc.FirstName)
	assert.Equal(t, "Pelosi", disc.LastName)
}

// TestFixture_FedRegister verifies the parser→resolver JSON contract.
func TestFixture_FedRegister(t *testing.T) {
	raw := []byte(`{"results": [
		{
			"document_number": "2025-00123",
			"publication_date": "2025-03-15",
			"type": "Rule",
			"title": "Test Rule",
			"cfr_references": [{"title": 19, "part": 134}]
		}
	]}`)

	events, err := parser.ParseFedRegister(raw)
	require.NoError(t, err)
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal(t, "federal_register", evt.Source)
	assert.Equal(t, "regulatory_action", evt.EventType)

	// Verify the resolver can unmarshal the parser's output.
	var doc fedRegDoc
	err = json.Unmarshal(evt.EventData, &doc)
	require.NoError(t, err)
	assert.Equal(t, "Rule", doc.Type)
	assert.Equal(t, "Test Rule", doc.Title)
}

// TestFixture_SECLitigation verifies the parser→resolver JSON contract.
func TestFixture_SECLitigation(t *testing.T) {
	raw := []byte(`{"releases": [
		{
			"id": "LR-25832",
			"date": "2025-04-01",
			"title": "SEC Charges Acme Corp for Securities Fraud",
			"url": "https://www.sec.gov/lit/lr25832.htm"
		}
	]}`)

	events, err := parser.ParseSECLitigation(raw)
	require.NoError(t, err)
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal(t, "sec_edgar_lit", evt.Source)
	assert.Equal(t, "sec_litigation", evt.EventType)

	// Verify the resolver can unmarshal the parser's output.
	var rel secLitEvent
	err = json.Unmarshal(evt.EventData, &rel)
	require.NoError(t, err)
	assert.Equal(t, "LR-25832", rel.ID)
}
