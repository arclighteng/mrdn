package db

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
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
	EventType  string          `json:"event_type"`
	EventData  json.RawMessage `json:"event_data"`
	OccurredAt time.Time       `json:"occurred_at"`
	IngestedAt time.Time       `json:"ingested_at"`
}

// Note: UNIQUE (source, source_id) does not trigger on NULL source_id.
// Events without a source_id are not deduped — the ingestion worker must
// handle dedup for those sources before calling InsertEvent.

type EventFilter struct {
	Source    string
	EventType string
	CompanyID *int
	Since     *time.Time
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
	if depth, err := jsonDepth(e.EventData); err != nil {
		return 0, fmt.Errorf("checking event_data depth: %w", err)
	} else if depth > maxEventDataDepth {
		return 0, fmt.Errorf("event_data nesting exceeds %d levels", maxEventDataDepth)
	}

	var id int
	err := s.db.QueryRow(ctx, `
		INSERT INTO events (source, source_id, company_id, event_type, event_data, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (source, source_id) DO UPDATE SET source = EXCLUDED.source
		RETURNING id
	`, e.Source, e.SourceID, e.CompanyID, e.EventType, e.EventData, e.OccurredAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("inserting event: %w", err)
	}
	return id, nil
}

func (s *Store) GetEvent(ctx context.Context, id int) (Event, error) {
	var e Event
	err := s.db.QueryRow(ctx, `
		SELECT id, source, source_id, company_id, event_type, event_data, occurred_at, ingested_at
		FROM events WHERE id = $1
	`, id).Scan(&e.ID, &e.Source, &e.SourceID, &e.CompanyID, &e.EventType,
		&e.EventData, &e.OccurredAt, &e.IngestedAt)
	if err != nil {
		return Event{}, fmt.Errorf("getting event %d: %w", id, err)
	}
	return e, nil
}

func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]Event, error) {
	query := "SELECT id, source, source_id, company_id, event_type, event_data, occurred_at, ingested_at FROM events WHERE 1=1"
	args := []any{}
	argN := 1

	if f.Source != "" {
		query += fmt.Sprintf(" AND source = $%d", argN)
		args = append(args, f.Source)
		argN++
	}
	if f.EventType != "" {
		query += fmt.Sprintf(" AND event_type = $%d", argN)
		args = append(args, f.EventType)
		argN++
	}
	if f.CompanyID != nil {
		query += fmt.Sprintf(" AND company_id = $%d", argN)
		args = append(args, *f.CompanyID)
		argN++
	}
	if f.Since != nil {
		query += fmt.Sprintf(" AND occurred_at >= $%d", argN)
		args = append(args, *f.Since)
		argN++
	}

	query += " ORDER BY occurred_at DESC"

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit)
	argN++

	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Source, &e.SourceID, &e.CompanyID, &e.EventType,
			&e.EventData, &e.OccurredAt, &e.IngestedAt); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating events: %w", err)
	}
	return events, nil
}

// CountEvents returns the total number of events matching the filter, applying
// the same WHERE logic as ListEvents (without LIMIT/OFFSET).
func (s *Store) CountEvents(ctx context.Context, f EventFilter) (int, error) {
	query := "SELECT COUNT(*) FROM events WHERE 1=1"
	args := []any{}
	argN := 1

	if f.Source != "" {
		query += fmt.Sprintf(" AND source = $%d", argN)
		args = append(args, f.Source)
		argN++
	}
	if f.EventType != "" {
		query += fmt.Sprintf(" AND event_type = $%d", argN)
		args = append(args, f.EventType)
		argN++
	}
	if f.CompanyID != nil {
		query += fmt.Sprintf(" AND company_id = $%d", argN)
		args = append(args, *f.CompanyID)
		argN++
	}
	if f.Since != nil {
		query += fmt.Sprintf(" AND occurred_at >= $%d", argN)
		args = append(args, *f.Since)
		argN++
	}

	var count int
	if err := s.db.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting events: %w", err)
	}
	return count, nil
}

// jsonDepth returns the maximum nesting depth of the JSON value in raw.
// An empty object or array at the top level has depth 1; a scalar has depth 0.
func jsonDepth(raw json.RawMessage) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	maxDepth := 0
	currentDepth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, err
		}
		switch t {
		case json.Delim('{'), json.Delim('['):
			currentDepth++
			if currentDepth > maxDepth {
				maxDepth = currentDepth
			}
		case json.Delim('}'), json.Delim(']'):
			currentDepth--
		}
	}
	return maxDepth, nil
}
