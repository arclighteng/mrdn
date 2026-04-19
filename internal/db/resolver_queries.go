package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// UpdateEventCompanyID sets the company_id on an existing event row.
func (s *Store) UpdateEventCompanyID(ctx context.Context, eventID int, companyID int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE events SET company_id = ? WHERE id = ?`,
		companyID, eventID)
	if err != nil {
		return fmt.Errorf("updating event %d company_id: %w", eventID, err)
	}
	return nil
}

// CompanyLookup is a minimal company record for resolver cache.
type CompanyLookup struct {
	ID     int
	Ticker string
	Name   string
}

// ListAllCompanyLookups returns all companies with just id, ticker, name
// for populating the in-memory resolver cache.
func (s *Store) ListAllCompanyLookups(ctx context.Context) ([]CompanyLookup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ticker, name FROM companies ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing company lookups: %w", err)
	}
	defer rows.Close()

	var result []CompanyLookup
	for rows.Next() {
		var c CompanyLookup
		if err := rows.Scan(&c.ID, &c.Ticker, &c.Name); err != nil {
			return nil, fmt.Errorf("scanning company lookup: %w", err)
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// ListUnresolvedEventsAfter returns events with NULL company_id and id > afterID,
// for the given source, limited to batchSize rows. Paginated by ID to avoid
// reprocessing events that remain unresolved after resolution.
func (s *Store) ListUnresolvedEventsAfter(ctx context.Context, source string, afterID, batchSize int) ([]Event, error) {
	query := `SELECT id, source, source_id, company_id, event_type, event_data, occurred_at, ingested_at
		FROM events WHERE company_id IS NULL`
	args := []any{}

	query += " AND id > ?"
	args = append(args, afterID)

	if source != "" {
		query += " AND source = ?"
		args = append(args, source)
	}

	query += " ORDER BY id LIMIT ?"
	args = append(args, batchSize)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing unresolved events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		var eventDataStr string
		var occurredAtStr, ingestedAtStr string
		if err := rows.Scan(&e.ID, &e.Source, &e.SourceID, &e.CompanyID, &e.EventType,
			&eventDataStr, &occurredAtStr, &ingestedAtStr); err != nil {
			return nil, fmt.Errorf("scanning unresolved event: %w", err)
		}
		e.EventData = json.RawMessage(eventDataStr)
		e.OccurredAt, _ = scanTime(occurredAtStr)
		e.IngestedAt, _ = scanTime(ingestedAtStr)
		events = append(events, e)
	}
	return events, rows.Err()
}

// SearchCompanyByName tries an exact case-insensitive match first, then falls
// back to a prefix match (for names with/without suffixes like "Inc", "Corp").
// Returns up to 1 result. Used as a fallback when ticker match fails.
func (s *Store) SearchCompanyByName(ctx context.Context, name string) (*CompanyLookup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}

	var c CompanyLookup

	// Try exact match first.
	err := s.db.QueryRowContext(ctx,
		`SELECT id, ticker, name FROM companies WHERE name = ? COLLATE NOCASE LIMIT 1`,
		name).Scan(&c.ID, &c.Ticker, &c.Name)
	if err == nil {
		return &c, nil
	}

	// Try prefix match: "Micron Technology" matches "MICRON TECHNOLOGY INC".
	// Use the shorter of the two as the prefix — query both directions.
	// Escape LIKE metacharacters so user input cannot alter the match pattern.
	escapedName := escapeLike(name)
	err = s.db.QueryRowContext(ctx,
		`SELECT id, ticker, name FROM companies
		WHERE LOWER(name) LIKE LOWER(?) || '%' ESCAPE '\'
		   OR LOWER(?) LIKE LOWER(name) || '%' ESCAPE '\'
		ORDER BY LENGTH(name) LIMIT 1`,
		escapedName, escapedName).Scan(&c.ID, &c.Ticker, &c.Name)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// escapeLike escapes LIKE metacharacters in a string.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
