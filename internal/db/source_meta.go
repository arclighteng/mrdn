package db

import (
	"context"
	"fmt"
	"time"
)

type SourceMeta struct {
	ID                  int        `json:"id"`
	SourceName          string     `json:"source_name"`
	ExpectedLag         string     `json:"expected_lag"`
	LastSuccessfulPoll  *time.Time `json:"last_successful_poll"`
	LastNewDataAt       *time.Time `json:"last_new_data_at"`
	PollIntervalSeconds int        `json:"poll_interval_seconds"`
	Status              string     `json:"status"`
}

func (s *Store) ListSourceMeta(ctx context.Context) ([]SourceMeta, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, source_name, expected_lag, last_successful_poll,
			   last_new_data_at, poll_interval_seconds, status
		FROM source_meta ORDER BY source_name
	`)
	if err != nil {
		return nil, fmt.Errorf("listing source meta: %w", err)
	}
	defer rows.Close()

	sources := make([]SourceMeta, 0)
	for rows.Next() {
		var sm SourceMeta
		if err := rows.Scan(&sm.ID, &sm.SourceName, &sm.ExpectedLag,
			&sm.LastSuccessfulPoll, &sm.LastNewDataAt,
			&sm.PollIntervalSeconds, &sm.Status); err != nil {
			return nil, fmt.Errorf("scanning source meta: %w", err)
		}
		sources = append(sources, sm)
	}
	return sources, nil
}

func (s *Store) GetSourceMeta(ctx context.Context, name string) (SourceMeta, error) {
	var sm SourceMeta
	err := s.pool.QueryRow(ctx, `
		SELECT id, source_name, expected_lag, last_successful_poll,
			   last_new_data_at, poll_interval_seconds, status
		FROM source_meta WHERE source_name = $1
	`, name).Scan(&sm.ID, &sm.SourceName, &sm.ExpectedLag,
		&sm.LastSuccessfulPoll, &sm.LastNewDataAt,
		&sm.PollIntervalSeconds, &sm.Status)
	if err != nil {
		return SourceMeta{}, fmt.Errorf("getting source %s: %w", name, err)
	}
	return sm, nil
}

func (s *Store) RecordPoll(ctx context.Context, sourceName string, hasNewData bool) error {
	now := time.Now().UTC()
	var err error
	if hasNewData {
		_, err = s.pool.Exec(ctx, `
			UPDATE source_meta SET last_successful_poll = $2, last_new_data_at = $2, status = 'healthy'
			WHERE source_name = $1
		`, sourceName, now)
	} else {
		_, err = s.pool.Exec(ctx, `
			UPDATE source_meta SET last_successful_poll = $2, status = 'healthy'
			WHERE source_name = $1
		`, sourceName, now)
	}
	if err != nil {
		return fmt.Errorf("recording poll for %s: %w", sourceName, err)
	}
	return nil
}

func (s *Store) SetSourceStatus(ctx context.Context, sourceName, status string) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE source_meta SET status = $2 WHERE source_name = $1",
		sourceName, status)
	return err
}
