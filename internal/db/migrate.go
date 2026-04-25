package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/001_sqlite_initial.sql
var sqliteSchema string

//go:embed migrations/002_composite_indexes.sql
var compositeIndexesSQL string

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
			// Version 2: seed sec_edgar_lit source_meta for existing databases.
			var v2Applied int
			d.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 2").Scan(&v2Applied)
			if v2Applied == 0 {
				if _, err := d.ExecContext(ctx, `
					INSERT OR IGNORE INTO source_meta (source_name, expected_lag, poll_interval_seconds, status)
					VALUES ('sec_edgar_lit', '1 day', 86400, 'healthy')
				`); err != nil {
					return fmt.Errorf("running v2 migration: %w", err)
				}
				if _, err := d.ExecContext(ctx,
					"INSERT OR IGNORE INTO schema_migrations (version) VALUES (2)",
				); err != nil {
					return fmt.Errorf("recording v2 migration: %w", err)
				}
			}

			// Version 3: composite indexes for congressional_trades self-join queries.
			var v3Applied int
			d.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 3").Scan(&v3Applied)
			if v3Applied == 0 {
				if _, err := d.ExecContext(ctx, compositeIndexesSQL); err != nil {
					return fmt.Errorf("running v3 migration: %w", err)
				}
				if _, err := d.ExecContext(ctx,
					"INSERT OR IGNORE INTO schema_migrations (version) VALUES (3)",
				); err != nil {
					return fmt.Errorf("recording v3 migration: %w", err)
				}
			}
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
