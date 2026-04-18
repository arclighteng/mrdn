package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Connect opens a SQLite database at dsn (file path or ":memory:").
func Connect(ctx context.Context, dsn string) (*sql.DB, error) {
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if _, err := d.ExecContext(ctx, `
		PRAGMA journal_mode=WAL;
		PRAGMA foreign_keys=ON;
		PRAGMA busy_timeout=5000;
	`); err != nil {
		d.Close()
		return nil, fmt.Errorf("setting pragmas: %w", err)
	}

	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return d, nil
}
