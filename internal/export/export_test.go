package export

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
)

func TestWriteJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "test.json")

	data := map[string]any{"data": []string{"a", "b"}}
	if err := writeJSON(path, data); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if len(got) == 0 {
		t.Fatal("empty file")
	}
}

func TestBuildScoreboardEntries_ZeroTrades(t *testing.T) {
	rows := []db.AccountabilityRow{
		{
			PersonID:   1,
			Slug:       "jane-doe",
			Name:       "Jane Doe",
			TradeCount: 0,
		},
	}
	entries := buildScoreboardEntries(rows)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Score != 0 {
		t.Errorf("expected score 0 for zero trades, got %d", entries[0].Score)
	}
	if entries[0].Slug != "jane-doe" {
		t.Errorf("unexpected slug: %q", entries[0].Slug)
	}
}

func TestBuildScoreboardEntries_PerfectRecord(t *testing.T) {
	// Fast filing, zero late pct, no committee overlap, no round trips, no pre-event trades.
	rows := []db.AccountabilityRow{
		{
			PersonID:            2,
			Slug:                "clean-rep",
			Name:                "Clean Rep",
			TradeCount:          20,
			MedianLatencyDays:   5, // well under the 20-day penalty threshold
			LatePct:             0.0,
			CommitteeTradeCount: 0,
			RoundTripCount:      0,
			PreEventCount:       0,
		},
	}
	entries := buildScoreboardEntries(rows)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Score != 0 {
		t.Errorf("expected score 0 for perfect record, got %d", entries[0].Score)
	}
}

func TestBuildScoreboardEntries_WorstCase(t *testing.T) {
	// Every metric is saturated to drive the composite score to 100.
	rows := []db.AccountabilityRow{
		{
			PersonID:            3,
			Slug:                "bad-actor",
			Name:                "Bad Actor",
			TradeCount:          100,
			MedianLatencyDays:   200, // far beyond 120-day ceiling
			LatePct:             1.0, // 100 % of trades filed late
			CommitteeTradeCount: 100, // all trades in own committee sectors → ratio 1.0
			RoundTripCount:      50,
			PreEventCount:       30,
		},
	}
	entries := buildScoreboardEntries(rows)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Score != 100 {
		t.Errorf("expected score 100 for worst case, got %d", entries[0].Score)
	}
}

func TestBuildScoreboardEntries_SortedDescending(t *testing.T) {
	party := "D"
	rows := []db.AccountabilityRow{
		// Low: fast filer, no issues.
		{PersonID: 10, Slug: "low", Name: "Low", TradeCount: 10, MedianLatencyDays: 5},
		// High: extreme latency + all flags saturated.
		{PersonID: 11, Slug: "high", Name: "High", TradeCount: 50, MedianLatencyDays: 200,
			LatePct: 1.0, CommitteeTradeCount: 50, RoundTripCount: 20, PreEventCount: 15, Party: &party},
		// Mid: moderate latency only.
		{PersonID: 12, Slug: "mid", Name: "Mid", TradeCount: 30, MedianLatencyDays: 70},
	}
	entries := buildScoreboardEntries(rows)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].Score > entries[i-1].Score {
			t.Errorf("entries not sorted descending: entries[%d].Score=%d > entries[%d].Score=%d",
				i, entries[i].Score, i-1, entries[i-1].Score)
		}
	}
	if entries[0].Slug != "high" {
		t.Errorf("expected 'high' as first entry, got %q", entries[0].Slug)
	}
}

func TestBuildScoreboardEntries_EmptyInput(t *testing.T) {
	entries := buildScoreboardEntries(nil)
	if len(entries) != 0 {
		t.Errorf("expected empty slice for nil input, got %d entries", len(entries))
	}
}
