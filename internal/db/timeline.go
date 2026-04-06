package db

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// TimelineEntry is a unified view of events and score snapshots for a company,
// ordered by timestamp descending. The Data field holds the underlying Event or
// Score value depending on EntryType.
type TimelineEntry struct {
	Timestamp time.Time `json:"timestamp"`
	EntryType string    `json:"entry_type"` // "event" or "score"
	Data      any       `json:"data"`
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

	// Merge into a single slice.
	entries := make([]TimelineEntry, 0, len(events)+len(scores))
	for _, e := range events {
		entries = append(entries, TimelineEntry{
			Timestamp: e.OccurredAt,
			EntryType: "event",
			Data:      e,
		})
	}
	for _, sc := range scores {
		entries = append(entries, TimelineEntry{
			Timestamp: sc.ComputedAt,
			EntryType: "score",
			Data:      sc,
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
