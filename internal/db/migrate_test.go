package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/require"
)

func TestMigrate_CreatesTablesOnce(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Run migrations twice — second run should be a no-op
	err = db.Migrate(ctx, pool)
	require.NoError(t, err)

	err = db.Migrate(ctx, pool)
	require.NoError(t, err)

	// Verify companies table exists
	var exists bool
	err = pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'companies')").Scan(&exists)
	require.NoError(t, err)
	require.True(t, exists)

	// Verify source_meta was seeded
	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM source_meta").Scan(&count)
	require.NoError(t, err)
	require.Greater(t, count, 0)
}
