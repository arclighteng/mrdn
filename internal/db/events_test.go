package db_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertEvent_Dedup(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	srcID := "dedup-001"
	evt := db.Event{
		Source:     "test",
		SourceID:   &srcID,
		EventType:  "test_event",
		EventData:  json.RawMessage(`{"foo": "bar"}`),
		OccurredAt: time.Now().UTC(),
	}

	id1, err := store.InsertEvent(ctx, evt)
	require.NoError(t, err)
	assert.Greater(t, id1, 0)

	// Same source + source_id should be ignored (dedup)
	id2, err := store.InsertEvent(ctx, evt)
	require.NoError(t, err)
	assert.Equal(t, id1, id2)
}

func TestListEvents(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	listSrcID := "list-001"
	store.InsertEvent(ctx, db.Event{
		Source:     "test",
		SourceID:   &listSrcID,
		EventType:  "test_event",
		EventData:  json.RawMessage(`{}`),
		OccurredAt: time.Now().UTC(),
	})

	events, err := store.ListEvents(ctx, db.EventFilter{Source: "test", Limit: 10})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(events), 1)
}
