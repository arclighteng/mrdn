package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/001_sqlite_initial.sql
var sqliteSchema string

func Migrate(ctx context.Context, d *sql.DB) error {
	var exists int
	err := d.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'",
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking schema_migrations: %w", err)
	}

	if exists > 0 {
		var applied int
		d.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&applied)
		if applied > 0 {
			return nil
		}
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning migration tx: %w", err)
	}

	if _, err := tx.ExecContext(ctx, sqliteSchema); err != nil {
		tx.Rollback()
		return fmt.Errorf("running schema migration: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_migrations (version) VALUES (1)",
	); err != nil {
		tx.Rollback()
		return fmt.Errorf("recording migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration: %w", err)
	}

	return nil
}
