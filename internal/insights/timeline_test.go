package insights

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTimeline_PreEvent(t *testing.T) {
	f := Finding{
		Type:      "pre_event",
		Headline:  "Pre-event: Alice traded LMT 5d before a government contract",
		Timestamp: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		Data: mustJSON(map[string]any{
			"name":       "Alice",
			"ticker":     "LMT",
			"trade_type": "purchase",
			"amount":     "175K",
			"trade_date": "2024-03-01",
			"event_type": "government_contract",
			"event_date": "2024-03-06",
			"days_gap":   5,
		}),
	}

	entries := BuildTimeline(f)
	require.Len(t, entries, 2, "pre_event must produce exactly 2 entries")

	assert.Equal(t, "trade", entries[0].Kind)
	assert.Equal(t, "2024-03-01", entries[0].Date)

	assert.Equal(t, "event", entries[1].Kind)
	assert.Equal(t, "2024-03-06", entries[1].Date)
	assert.Equal(t, "📋", entries[1].Icon)
}

func TestBuildTimeline_Coordinated(t *testing.T) {
	type repRow struct {
		Name string `json:"name"`
		Date string `json:"date"`
	}
	rows := []repRow{
		{Name: "Alice"},
		{Name: "Bob"},
		{Name: "Carol"},
	}
	data, err := json.Marshal(rows)
	require.NoError(t, err)

	f := Finding{
		Type:      "coordinated_trades",
		Headline:  "Coordinated: 3 reps bought NVDA in the same week",
		Timestamp: time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
		Data:      data,
	}

	entries := BuildTimeline(f)
	require.Len(t, entries, 1, "coordinated must produce exactly 1 entry")
	assert.Equal(t, "cluster", entries[0].Kind)
}

func TestBuildTimeline_RoundTrip(t *testing.T) {
	f := Finding{
		Type:      "round_trip",
		Headline:  "Fast round-trip: Bob bought then sold AAPL in 5 days",
		Timestamp: time.Date(2024, 5, 10, 0, 0, 0, 0, time.UTC),
		Data: mustJSON(map[string]any{
			"name":      "Bob",
			"ticker":    "AAPL",
			"hold_days": 5,
			"buy_date":  "2024-05-05",
			"sell_date": "2024-05-10",
			"amount":    "175K",
		}),
	}

	entries := BuildTimeline(f)
	require.Len(t, entries, 2, "round_trip must produce exactly 2 entries")
	assert.Equal(t, "trade", entries[0].Kind)
	assert.Equal(t, "trade", entries[1].Kind)
	assert.Equal(t, "2024-05-05", entries[0].Date)
	assert.Equal(t, "2024-05-10", entries[1].Date)
}

func TestBuildTimeline_CommitteeRelevant(t *testing.T) {
	f := Finding{
		Type:      "committee_relevant",
		Headline:  "Committee overlap: Carol (Armed Services) traded RTX (Aerospace & Defense)",
		Timestamp: time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
		Data: mustJSON(map[string]any{
			"name":      "Carol",
			"committee": "Armed Services",
			"ticker":    "RTX",
			"sector":    "Aerospace & Defense",
			"amount":    "250K",
			"date":      "2024-06-15",
		}),
	}

	entries := BuildTimeline(f)
	require.Len(t, entries, 1, "committee_relevant must produce exactly 1 entry")
	assert.Equal(t, "trade", entries[0].Kind)
	assert.NotEmpty(t, entries[0].Date, "date must be non-empty (uses 'date' key)")
	assert.Equal(t, "2024-06-15", entries[0].Date)
}

func TestBuildTimeline_LoneWolf(t *testing.T) {
	f := Finding{
		Type:      "lone_wolf",
		Headline:  "Lone wolf: Dave traded $2.0M in NVDA — 26x their typical size",
		Timestamp: time.Date(2024, 7, 20, 0, 0, 0, 0, time.UTC),
		Data: mustJSON(map[string]any{
			"name":       "Dave",
			"ticker":     "NVDA",
			"trade_type": "buy",
			"amount":     "2.0M",
			"ratio":      "26.0×",
			"date":       "2024-07-20",
		}),
	}

	entries := BuildTimeline(f)
	require.Len(t, entries, 1, "lone_wolf must produce exactly 1 entry")
	assert.Equal(t, "trade", entries[0].Kind)
	assert.NotEmpty(t, entries[0].Date, "date must be non-empty (uses 'date' key)")
	assert.Equal(t, "2024-07-20", entries[0].Date)
}

func TestBuildTimeline_UnknownType(t *testing.T) {
	f := Finding{
		Type:      "mystery_signal",
		Headline:  "Something unusual happened",
		Timestamp: time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC),
		Data:      mustJSON(map[string]any{"foo": "bar"}),
	}

	// Must not panic and must return exactly 1 fallback entry.
	require.NotPanics(t, func() {
		entries := BuildTimeline(f)
		require.Len(t, entries, 1, "unknown type must produce exactly 1 fallback entry")
		assert.Equal(t, "event", entries[0].Kind)
		assert.Equal(t, f.Headline, entries[0].Label)
		assert.Equal(t, "2024-08-01", entries[0].Date)
	})
}
