package db

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// TimelineEntry is a flattened view of events and score snapshots for a
// company, ordered by timestamp descending. Fields are selectively populated
// depending on the entry type.
type TimelineEntry struct {
	Timestamp      time.Time `json:"timestamp"`
	Type           string    `json:"type"` // "event" or "score"
	EventID        *int      `json:"event_id,omitempty"`
	Source         *string   `json:"source,omitempty"`
	EventType      *string   `json:"event_type,omitempty"`
	ScoreID        *int      `json:"score_id,omitempty"`
	CompositeScore *float64  `json:"composite_score,omitempty"`
}

// GetCompanyTimeline returns the most recent limit timeline entries for the
// given company, merging events and score snapshots and sorting them by
// timestamp descending. limit defaults to 50 when <= 0.
func (s *Store) GetCompanyTimeline(ctx context.Context, companyID int, limit int) ([]TimelineEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	// Fetch events ordered newest-first, up to limit rows.
	events, err := s.ListEvents(ctx, EventFilter{
		CompanyID: &companyID,
		Limit:     limit,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching events for timeline (company %d): %w", companyID, err)
	}

	// Fetch score history, newest-first, up to limit rows.
	scores, err := s.GetScoreHistory(ctx, companyID, limit)
	if err != nil {
		return nil, fmt.Errorf("fetching scores for timeline (company %d): %w", companyID, err)
	}

	// Merge into a single flattened slice.
	entries := make([]TimelineEntry, 0, len(events)+len(scores))
	for _, e := range events {
		entries = append(entries, TimelineEntry{
			Timestamp: e.OccurredAt,
			Type:      "event",
			EventID:   &e.ID,
			Source:    &e.Source,
			EventType: &e.EventType,
		})
	}
	for _, sc := range scores {
		entries = append(entries, TimelineEntry{
			Timestamp:      sc.ComputedAt,
			Type:           "score",
			ScoreID:        &sc.ID,
			CompositeScore: &sc.CompositeScore,
		})
	}

	// Sort descending by timestamp.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	// Trim to the requested limit.
	if len(entries) > limit {
		entries = entries[:limit]
	}

	return entries, nil
}
