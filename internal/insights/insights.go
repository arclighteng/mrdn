package insights

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

// Finding represents a single insight detected by one of the pattern detectors.
type Finding struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Headline    string          `json:"headline"`
	RarityScore int             `json:"rarity_score"`
	Timestamp   time.Time       `json:"timestamp"`
	Data        json.RawMessage `json:"data"`
	Link        json.RawMessage `json:"link"`
}

// InsightsOutput is the top-level JSON structure written to insights.json.
type InsightsOutput struct {
	GeneratedAt string    `json:"generated_at"`
	Findings    []Finding `json:"findings"`
}

// committeeToSectors maps congressional committee name substrings to GICS sectors.
var committeeToSectors = map[string][]string{
	"Armed Services":                        {"Aerospace & Defense", "Industrials"},
	"Banking":                               {"Financials", "Banks"},
	"Financial Services":                    {"Financials", "Banks"},
	"Energy and Commerce":                   {"Energy", "Utilities"},
	"Energy and Natural Resources":          {"Energy", "Utilities"},
	"Agriculture":                           {"Consumer Staples", "Materials"},
	"Health":                                {"Health Care"},
	"Commerce, Science, and Transportation": {"Technology", "Communication Services"},
	"Science, Space, and Technology":        {"Technology"},
	"Veterans' Affairs":                     {"Health Care"},
}

// Detect runs all pattern detectors and returns the top 20 findings by rarity.
func Detect(ctx context.Context, store *db.Store) ([]Finding, error) {
	type detector struct {
		name string
		fn   func(context.Context, *db.Store) ([]Finding, error)
	}

	detectors := []detector{
		{"coordinated", detectCoordinated},
		{"committee", detectCommittee},
		{"pre-event", detectPreEvent},
		{"round-trips", detectRoundTrips},
		{"swarm-outliers", detectSwarmOutliers},
		{"lone-wolf", detectLoneWolf},
	}

	var all []Finding
	for _, d := range detectors {
		dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		findings, err := d.fn(dctx, store)
		cancel()
		if err != nil {
			log.Printf("[insights] %s detector error: %v (skipping)", d.name, err)
			continue
		}
		all = append(all, findings...)
	}

	// Filter to last 12 months — stale findings are not actionable.
	cutoff := time.Now().AddDate(0, -12, 0)
	recent := make([]Finding, 0, len(all))
	for _, f := range all {
		if f.Timestamp.After(cutoff) {
			recent = append(recent, f)
		}
	}

	// Sort by rarity descending, keep top 20.
	sort.Slice(recent, func(i, j int) bool {
		if recent[i].RarityScore != recent[j].RarityScore {
			return recent[i].RarityScore > recent[j].RarityScore
		}
		return recent[i].Timestamp.After(recent[j].Timestamp)
	})
	if len(recent) > 20 {
		recent = recent[:20]
	}
	return recent, nil
}

// clampScore clamps a rarity score to 0-100.
func clampScore(s int) int {
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
}

// mustJSON marshals v to json.RawMessage, panicking on error (for known-good structs).
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// parseTime attempts to parse a time string using several common layouts.
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time: %s", s)
}
