package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/require"
)

// testDB returns an in-memory SQLite database with migrations applied.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Connect(context.Background(), ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(context.Background(), d))
	t.Cleanup(func() { d.Close() })
	return d
}

// setupTestTx returns a Store backed by an in-memory SQLite database.
func setupTestTx(t *testing.T) *db.Store {
	t.Helper()
	d := testDB(t)
	return db.NewStore(d)
}
