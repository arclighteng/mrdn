package db

import (
	"context"
	"fmt"
	"time"
)

type SourceMeta struct {
	ID                  int        `json:"id"`
	SourceName          string     `json:"source_name"`
	ExpectedLag         *string    `json:"expected_lag,omitempty"`
	LastSuccessfulPoll  *time.Time `json:"last_successful_poll"`
	LastNewDataAt       *time.Time `json:"last_new_data_at"`
	PollIntervalSeconds int        `json:"poll_interval_seconds"`
	Status              string     `json:"status"`
	LastAttemptAt       *time.Time `json:"last_attempt_at"`
	LastHTTPCode        *int       `json:"last_http_code"`
	LastError           *string    `json:"last_error"`
	LastRecords         *int       `json:"last_records"`
	LastDurationMs      *int       `json:"last_duration_ms"`
}

const sourceSelect = `
	SELECT id, source_name, expected_lag, last_successful_poll,
	       last_new_data_at, poll_interval_seconds, status,
	       last_attempt_at, last_http_code, last_error,
	       last_records, last_duration_ms
	FROM source_meta`

func scanSource(row interface {
	Scan(dest ...any) error
}) (SourceMeta, error) {
	var sm SourceMeta
	var lastSuccessfulPoll, lastNewDataAt, lastAttemptAt *string
	err := row.Scan(
		&sm.ID, &sm.SourceName, &sm.ExpectedLag,
		&lastSuccessfulPoll, &lastNewDataAt,
		&sm.PollIntervalSeconds, &sm.Status,
		&lastAttemptAt, &sm.LastHTTPCode, &sm.LastError,
		&sm.LastRecords, &sm.LastDurationMs,
	)
	if err != nil {
		return SourceMeta{}, err
	}
	sm.LastSuccessfulPoll = scanTimePtr(lastSuccessfulPoll)
	sm.LastNewDataAt = scanTimePtr(lastNewDataAt)
	sm.LastAttemptAt = scanTimePtr(lastAttemptAt)
	return sm, nil
}

func (s *Store) ListSourceMeta(ctx context.Context) ([]SourceMeta, error) {
	rows, err := s.db.QueryContext(ctx, sourceSelect+" ORDER BY source_name")
	if err != nil {
		return nil, fmt.Errorf("listing source meta: %w", err)
	}
	defer rows.Close()

	sources := make([]SourceMeta, 0)
	for rows.Next() {
		sm, err := scanSource(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning source meta: %w", err)
		}
		sources = append(sources, sm)
	}
	return sources, nil
}

func (s *Store) GetSourceMeta(ctx context.Context, name string) (SourceMeta, error) {
	row := s.db.QueryRowContext(ctx, sourceSelect+" WHERE source_name = ?", name)
	sm, err := scanSource(row)
	if err != nil {
		return SourceMeta{}, fmt.Errorf("getting source %s: %w", name, err)
	}
	return sm, nil
}

func (s *Store) RecordPoll(ctx context.Context, sourceName string, hasNewData bool) error {
	now := formatTime(time.Now().UTC())
	var err error
	if hasNewData {
		_, err = s.db.ExecContext(ctx, `
			UPDATE source_meta SET last_successful_poll = ?, last_new_data_at = ?, status = 'healthy'
			WHERE source_name = ?
		`, now, now, sourceName)
	} else {
		_, err = s.db.ExecContext(ctx, `
			UPDATE source_meta SET last_successful_poll = ?, status = 'healthy'
			WHERE source_name = ?
		`, now, sourceName)
	}
	if err != nil {
		return fmt.Errorf("recording poll for %s: %w", sourceName, err)
	}
	return nil
}

func (s *Store) SetSourceStatus(ctx context.Context, sourceName, status string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE source_meta SET status = ? WHERE source_name = ?",
		status, sourceName)
	return err
}

// IngestAttempt captures one invocation of an ingest command so the /status
// endpoint can display recency, latency, HTTP error codes, and last error.
type IngestAttempt struct {
	Source     string
	Success    bool
	HTTPCode   int    // 0 when not an HTTP-bounded failure
	Error      string // empty on success
	Records    int    // rows processed
	DurationMs int
	HasNewData bool
}

// RecordIngestAttempt upserts the per-attempt fields on source_meta. On
// success it also advances last_successful_poll (and last_new_data_at when
// HasNewData is true). Status is derived: ok → healthy, http_code >= 500 →
// down, other errors → degraded.
func (s *Store) RecordIngestAttempt(ctx context.Context, a IngestAttempt) error {
	now := formatTime(time.Now().UTC())

	status := "degraded"
	var httpPtr *int
	var errPtr *string
	var recPtr *int
	var durPtr *int
	if a.HTTPCode != 0 {
		c := a.HTTPCode
		httpPtr = &c
	}
	if a.Error != "" {
		e := a.Error
		errPtr = &e
	}
	if a.Records != 0 || a.Success {
		r := a.Records
		recPtr = &r
	}
	if a.DurationMs != 0 {
		d := a.DurationMs
		durPtr = &d
	}

	if a.Success {
		status = "healthy"
	} else if a.HTTPCode >= 500 {
		status = "down"
	}

	// Ensure the row exists so this helper works for sources that weren't
	// seeded by migration 001.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO source_meta (source_name, status)
		VALUES (?, ?)
		ON CONFLICT (source_name) DO NOTHING
	`, a.Source, status); err != nil {
		return fmt.Errorf("upserting source_meta row for %s: %w", a.Source, err)
	}

	if a.Success {
		// SQLite does not support boolean parameters directly; convert HasNewData
		// to an integer so CASE WHEN evaluates correctly.
		var hasNewDataInt int
		if a.HasNewData {
			hasNewDataInt = 1
		}
		_, err := s.db.ExecContext(ctx, `
			UPDATE source_meta
			SET status = ?,
			    last_attempt_at = ?,
			    last_successful_poll = ?,
			    last_new_data_at = CASE WHEN ? = 1 THEN ? ELSE last_new_data_at END,
			    last_http_code = ?,
			    last_error = NULL,
			    last_records = ?,
			    last_duration_ms = ?
			WHERE source_name = ?
		`, status, now, now, hasNewDataInt, now, httpPtr, recPtr, durPtr, a.Source)
		if err != nil {
			return fmt.Errorf("recording success for %s: %w", a.Source, err)
		}
		return nil
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE source_meta
		SET status = ?,
		    last_attempt_at = ?,
		    last_http_code = ?,
		    last_error = ?,
		    last_records = ?,
		    last_duration_ms = ?
		WHERE source_name = ?
	`, status, now, httpPtr, errPtr, recPtr, durPtr, a.Source)
	if err != nil {
		return fmt.Errorf("recording failure for %s: %w", a.Source, err)
	}
	return nil
}
