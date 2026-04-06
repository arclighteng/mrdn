package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// setupTestTx returns a Store backed by a transaction that is rolled back
// when the test completes. This provides complete isolation between tests:
// every insert, update, or delete is invisible to other connections and is
// discarded automatically via t.Cleanup — no manual teardown required.
//
// The function skips the test when DATABASE_URL is not set, matching the
// behaviour of setupTestDB in companies_test.go.
func setupTestTx(t *testing.T) *db.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	// Run migrations on the pool (idempotent — safe to call on every test run).
	require.NoError(t, db.Migrate(ctx, pool))

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	// Always roll back — even when the test itself calls t.Fatal or panics.
	t.Cleanup(func() { tx.Rollback(context.Background()) }) //nolint:errcheck

	// pgx.Tx satisfies db.DBTX (Exec / Query / QueryRow), so NewStore accepts it directly.
	return db.NewStore(tx)
}
