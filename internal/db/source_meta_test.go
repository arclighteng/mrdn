package db_test

import (
	"context"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListSourceMeta(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	sources, err := store.ListSourceMeta(ctx)
	require.NoError(t, err)
	// Seeded in migration
	assert.GreaterOrEqual(t, len(sources), 1)
}

func TestUpdateSourceStatus(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	err := store.RecordPoll(ctx, "polygon", true)
	require.NoError(t, err)

	src, err := store.GetSourceMeta(ctx, "polygon")
	require.NoError(t, err)
	assert.Equal(t, "healthy", src.Status)
	assert.NotNil(t, src.LastSuccessfulPoll)
}
