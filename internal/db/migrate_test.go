package db_test

import (
	"context"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/require"
)

func TestMigrate_CreatesTablesOnce(t *testing.T) {
	ctx := context.Background()
	d, err := db.Connect(ctx, ":memory:")
	require.NoError(t, err)
	defer d.Close()

	// Run migrations twice — second run should be a no-op
	err = db.Migrate(ctx, d)
	require.NoError(t, err)

	err = db.Migrate(ctx, d)
	require.NoError(t, err)

	// Verify companies table exists
	var count int
	err = d.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='companies'").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Verify source_meta was seeded
	err = d.QueryRowContext(ctx, "SELECT COUNT(*) FROM source_meta").Scan(&count)
	require.NoError(t, err)
	require.Greater(t, count, 0)

	// Verify persons were seeded
	err = d.QueryRowContext(ctx, "SELECT COUNT(*) FROM persons").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 20, count)
}
