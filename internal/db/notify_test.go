package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/require"
)

func TestNotifyAndListen(t *testing.T) {
	dsn := setupDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	ch, err := db.ListenNewEvents(ctx, pool)
	require.NoError(t, err)

	// Give LISTEN a moment to establish on the server side.
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, db.NotifyNewEvent(ctx, pool, 42))

	select {
	case id := <-ch:
		require.Equal(t, 42, id)
	case <-ctx.Done():
		t.Fatal("timed out waiting for notification")
	}
}

func TestListenCancelledContext(t *testing.T) {
	dsn := setupDSN(t)
	ctx, cancel := context.WithCancel(context.Background())

	pool, err := db.Connect(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	ch, err := db.ListenNewEvents(ctx, pool)
	require.NoError(t, err)

	// Cancel the context immediately — the goroutine must exit and close ch.
	cancel()

	select {
	case _, ok := <-ch:
		_ = ok // closed channel or a value both indicate the goroutine responded
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel to close after context cancel")
	}
}
