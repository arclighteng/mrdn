package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/arclighteng/mrdn/internal/jsonutil"
)

const (
	maxEventDataSize  = 65536 // 64 KB
	maxEventDataDepth = 10
)

type Event struct {
	ID         int             `json:"id"`
	Source     string          `json:"source"`
	SourceID   *string         `json:"source_id,omitempty"`
	CompanyID  *int            `json:"company_id,omitempty"`
	Ticker     *string         `json:"ticker,omitempty"`
	EventType  string          `json:"event_type"`
	EventData  json.RawMessage `json:"event_data"`
	OccurredAt time.Time       `json:"occurred_at"`
	IngestedAt time.Time       `json:"ingested_at"`
}

type EventFilter struct {
	Source    string
	EventType string
	CompanyID *int
	Since     *time.Time
	Until     *time.Time
	Limit     int
	Offset    int
}

func (s *Store) InsertEvent(ctx context.Context, e Event) (int, error) {
	if len(e.EventData) > maxEventDataSize {
		return 0, fmt.Errorf("event_data exceeds %d bytes", maxEventDataSize)
	}
	if !json.Valid(e.EventData) {
		return 0, fmt.Errorf("event_data is not valid JSON")
	}
	if depth, err := jsonutil.Depth(e.EventData); err != nil {
		return 0, fmt.Errorf("checking event_data depth: %w", err)
	} else if depth > maxEventDataDepth {
		return 0, fmt.Errorf("event_data nesting exceeds %d levels", maxEventDataDepth)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO events (source, source_id, company_id, event_type, event_data, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (source, source_id) DO UPDATE SET source = excluded.source
	`, e.Source, e.SourceID, e.CompanyID, e.EventType, string(e.EventData), formatTime(e.OccurredAt))
	if err != nil {
		return 0, fmt.Errorf("inserting event: %w", err)
	}

	var id int
	err = s.db.QueryRowContext(ctx,
		"SELECT id FROM events WHERE source = ? AND source_id = ?",
		e.Source, e.SourceID,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("getting event id: %w", err)
	}
	return id, nil
}

// validateEventData applies the same size/shape checks as InsertEvent.
func validateEventData(data json.RawMessage) error {
	if len(data) > maxEventDataSize {
		return fmt.Errorf("event_data exceeds %d bytes", maxEventDataSize)
	}
	if !json.Valid(data) {
		return fmt.Errorf("event_data is not valid JSON")
	}
	depth, err := jsonutil.Depth(data)
	if err != nil {
		return fmt.Errorf("checking event_data depth: %w", err)
	}
	if depth > maxEventDataDepth {
		return fmt.Errorf("event_data nesting exceeds %d levels", maxEventDataDepth)
	}
	return nil
}

// InsertEventsBatch inserts many events using individual inserts.
func (s *Store) InsertEventsBatch(ctx context.Context, events []Event) ([]int, error) {
	ids := make([]int, len(events))
	if len(events) == 0 {
		return ids, nil
	}

	for i, e := range events {
		if err := validateEventData(e.EventData); err != nil {
			continue
		}
		id, err := s.InsertEvent(ctx, e)
		if err != nil {
			return ids, fmt.Errorf("batch insert event %d: %w", i, err)
		}
		ids[i] = id
	}
	return ids, nil
}

func (s *Store) GetEvent(ctx context.Context, id int) (Event, error) {
	var e Event
	var eventDataStr string
	var occurredAtStr, ingestedAtStr string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, source, source_id, company_id, event_type, event_data, occurred_at, ingested_at
		FROM events WHERE id = ?
	`, id).Scan(&e.ID, &e.Source, &e.SourceID, &e.CompanyID, &e.EventType,
		&eventDataStr, &occurredAtStr, &ingestedAtStr)
	if err != nil {
		return Event{}, fmt.Errorf("getting event %d: %w", id, err)
	}
	e.EventData = json.RawMessage(eventDataStr)
	e.OccurredAt, _ = scanTime(occurredAtStr)
	e.IngestedAt, _ = scanTime(ingestedAtStr)
	return e, nil
}

func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]Event, error) {
	query := `SELECT e.id, e.source, e.source_id, e.company_id, c.ticker,
		e.event_type, e.event_data, e.occurred_at, e.ingested_at
		FROM events e LEFT JOIN companies c ON c.id = e.company_id WHERE 1=1`
	args := []any{}

	if f.Source != "" {
		query += " AND e.source = ?"
		args = append(args, f.Source)
	}
	if f.EventType != "" {
		query += " AND e.event_type = ?"
		args = append(args, f.EventType)
	}
	if f.CompanyID != nil {
		query += " AND e.company_id = ?"
		args = append(args, *f.CompanyID)
	}
	if f.Since != nil {
		query += " AND e.occurred_at >= ?"
		args = append(args, formatTime(*f.Since))
	}
	if f.Until != nil {
		query += " AND e.occurred_at < ?"
		args = append(args, formatTime(*f.Until))
	}

	query += " ORDER BY e.occurred_at DESC"

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query += " LIMIT ?"
	args = append(args, limit)

	if f.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, f.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		var e Event
		var eventDataStr string
		var occurredAtStr, ingestedAtStr string
		if err := rows.Scan(&e.ID, &e.Source, &e.SourceID, &e.CompanyID, &e.Ticker,
			&e.EventType, &eventDataStr, &occurredAtStr, &ingestedAtStr); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		e.EventData = json.RawMessage(eventDataStr)
		e.OccurredAt, _ = scanTime(occurredAtStr)
		e.IngestedAt, _ = scanTime(ingestedAtStr)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events: %w", err)
	}
	return events, nil
}

// CountEvents returns the total number of events matching the filter.
func (s *Store) CountEvents(ctx context.Context, f EventFilter) (int, error) {
	query := "SELECT COUNT(*) FROM events WHERE 1=1"
	args := []any{}

	if f.Source != "" {
		query += " AND source = ?"
		args = append(args, f.Source)
	}
	if f.EventType != "" {
		query += " AND event_type = ?"
		args = append(args, f.EventType)
	}
	if f.CompanyID != nil {
		query += " AND company_id = ?"
		args = append(args, *f.CompanyID)
	}
	if f.Since != nil {
		query += " AND occurred_at >= ?"
		args = append(args, formatTime(*f.Since))
	}
	if f.Until != nil {
		query += " AND occurred_at < ?"
		args = append(args, formatTime(*f.Until))
	}

	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting events: %w", err)
	}
	return count, nil
}
