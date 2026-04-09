package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

// Note: UNIQUE (source, source_id) does not trigger on NULL source_id.
// Events without a source_id are not deduped — the ingestion worker must
// handle dedup for those sources before calling InsertEvent.

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

// batchInsertChunkSize bounds the number of events per multi-row INSERT.
// Postgres' max bind parameter count is 65535; at 6 params/event this caps
// around 10_000. 500 keeps each round-trip fast and memory bounded.
const batchInsertChunkSize = 500

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

// InsertEventsBatch inserts many events in a small number of round trips
// using multi-row INSERTs with ON CONFLICT DO UPDATE ... RETURNING id.
// It returns ids in the same order as the input. Events that fail validation
// are skipped; the returned ids slice has 0 at their position.
//
// This is a drop-in fast path for bulk sources (polygon, ofac_sdn) where the
// sequential per-row InsertEvent loop otherwise takes tens of minutes per poll.
func (s *Store) InsertEventsBatch(ctx context.Context, events []Event) ([]int, error) {
	ids := make([]int, len(events))
	if len(events) == 0 {
		return ids, nil
	}

	// Filter valid events but remember original positions.
	type slot struct {
		origIdx int
		evt     Event
	}
	valid := make([]slot, 0, len(events))
	for i, e := range events {
		if err := validateEventData(e.EventData); err != nil {
			// Mirror per-row behavior: skip and continue.
			continue
		}
		valid = append(valid, slot{origIdx: i, evt: e})
	}

	for start := 0; start < len(valid); start += batchInsertChunkSize {
		end := start + batchInsertChunkSize
		if end > len(valid) {
			end = len(valid)
		}
		chunk := valid[start:end]

		var sb strings.Builder
		sb.WriteString("INSERT INTO events (source, source_id, company_id, event_type, event_data, occurred_at) VALUES ")
		args := make([]any, 0, len(chunk)*6)
		for i, s := range chunk {
			if i > 0 {
				sb.WriteByte(',')
			}
			base := i * 6
			sb.WriteByte('(')
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(base + 1))
			sb.WriteString(",$")
			sb.WriteString(strconv.Itoa(base + 2))
			sb.WriteString(",$")
			sb.WriteString(strconv.Itoa(base + 3))
			sb.WriteString(",$")
			sb.WriteString(strconv.Itoa(base + 4))
			sb.WriteString(",$")
			sb.WriteString(strconv.Itoa(base + 5))
			sb.WriteString(",$")
			sb.WriteString(strconv.Itoa(base + 6))
			sb.WriteByte(')')
			args = append(args,
				s.evt.Source, s.evt.SourceID, s.evt.CompanyID,
				s.evt.EventType, s.evt.EventData, s.evt.OccurredAt,
			)
		}
		// DO UPDATE (not DO NOTHING) so RETURNING yields a row for every
		// VALUES tuple, in input order — matching InsertEvent's semantics.
		sb.WriteString(" ON CONFLICT (source, source_id) DO UPDATE SET source = EXCLUDED.source RETURNING id")

		rows, err := s.db.Query(ctx, sb.String(), args...)
		if err != nil {
			return ids, fmt.Errorf("batch inserting events: %w", err)
		}
		i := 0
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return ids, fmt.Errorf("scanning batch insert id: %w", err)
			}
			if i < len(chunk) {
				ids[chunk[i].origIdx] = id
			}
			i++
		}
		if err := rows.Err(); err != nil {
			return ids, fmt.Errorf("iterating batch insert ids: %w", err)
		}
	}
	return ids, nil
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
	query := `SELECT e.id, e.source, e.source_id, e.company_id, c.ticker,
		e.event_type, e.event_data, e.occurred_at, e.ingested_at
		FROM events e LEFT JOIN companies c ON c.id = e.company_id WHERE 1=1`
	args := []any{}
	argN := 1

	if f.Source != "" {
		query += fmt.Sprintf(" AND e.source = $%d", argN)
		args = append(args, f.Source)
		argN++
	}
	if f.EventType != "" {
		query += fmt.Sprintf(" AND e.event_type = $%d", argN)
		args = append(args, f.EventType)
		argN++
	}
	if f.CompanyID != nil {
		query += fmt.Sprintf(" AND e.company_id = $%d", argN)
		args = append(args, *f.CompanyID)
		argN++
	}
	if f.Since != nil {
		query += fmt.Sprintf(" AND e.occurred_at >= $%d", argN)
		args = append(args, *f.Since)
		argN++
	}
	if f.Until != nil {
		query += fmt.Sprintf(" AND e.occurred_at < $%d", argN)
		args = append(args, *f.Until)
		argN++
	}

	query += " ORDER BY e.occurred_at DESC"

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
		if err := rows.Scan(&e.ID, &e.Source, &e.SourceID, &e.CompanyID, &e.Ticker,
			&e.EventType, &e.EventData, &e.OccurredAt, &e.IngestedAt); err != nil {
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
	if f.Until != nil {
		query += fmt.Sprintf(" AND occurred_at < $%d", argN)
		args = append(args, *f.Until)
		argN++
	}

	var count int
	if err := s.db.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting events: %w", err)
	}
	return count, nil
}

