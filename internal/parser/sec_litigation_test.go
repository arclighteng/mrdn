package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSECLitigation(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="utf-8"?>
<rss xmlns:dc="http://purl.org/dc/elements/1.1/" version="2.0">
  <channel>
    <title>Litigation Releases</title>
    <item>
      <title>SEC Charges Acme Corp for Securities Fraud</title>
      <link>https://www.sec.gov/enforcement-litigation/litigation-releases/lr-25832</link>
      <pubDate>Tue, 01 Apr 2025 12:00:00 -0400</pubDate>
      <dc:creator>LR-25832</dc:creator>
      <guid isPermaLink="false">abc-123</guid>
    </item>
    <item>
      <title>SEC Files Action Against John Doe and Widget Inc.</title>
      <link>https://www.sec.gov/enforcement-litigation/litigation-releases/lr-25833</link>
      <pubDate>Wed, 02 Apr 2025 10:00:00 -0400</pubDate>
      <dc:creator>LR-25833</dc:creator>
      <guid isPermaLink="false">def-456</guid>
    </item>
  </channel>
</rss>`)

	events, err := ParseSECLitigation(raw)
	require.NoError(t, err)
	require.Len(t, events, 2)

	evt := events[0]
	assert.Equal(t, "sec_edgar_lit", evt.Source)
	assert.Equal(t, "sec_litigation", evt.EventType)
	assert.NotEmpty(t, evt.SourceID)
	assert.Contains(t, string(evt.EventData), "LR-25832")
	assert.Contains(t, string(evt.EventData), "Acme Corp")
}

func TestParseSECLitigation_EmptyChannel(t *testing.T) {
	raw := []byte(`<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`)
	events, err := ParseSECLitigation(raw)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestParseSECLitigation_InvalidXML(t *testing.T) {
	_, err := ParseSECLitigation([]byte(`not xml`))
	assert.Error(t, err)
}

func TestParseSECLitigation_FallbackSourceID(t *testing.T) {
	raw := []byte(`<?xml version="1.0"?>
<rss xmlns:dc="http://purl.org/dc/elements/1.1/" version="2.0">
  <channel>
    <item>
      <title>Some Release</title>
      <pubDate>Wed, 01 Jan 2025 12:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`)
	events, err := ParseSECLitigation(raw)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.NotEmpty(t, events[0].SourceID)
}
