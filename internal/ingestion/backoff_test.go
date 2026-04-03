package ingestion_test

import (
	"context"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/ingestion"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// epoch is a fixed point in time used to initialise FakeClock in tests.
var epoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func TestBackoffSequence(t *testing.T) {
	fc := ingestion.NewFakeClock(epoch)
	b := ingestion.NewBackoff(fc)

	// attempt 0: base * 2^0 = 5s, +jitter [0,3s) → expect [5s, 8s)
	// attempt 1: base * 2^1 = 10s             → expect [10s, 13s)
	// attempt 2: base * 2^2 = 20s             → expect [20s, 23s)
	// attempt 3: base * 2^3 = 40s             → expect [40s, 43s)
	// attempt 4: base * 2^4 = 80s             → expect [80s, 83s)
	// ...up to Max = 15min = 900s
	cases := []struct {
		minD time.Duration
		maxD time.Duration
	}{
		{5 * time.Second, 8 * time.Second},
		{10 * time.Second, 13 * time.Second},
		{20 * time.Second, 23 * time.Second},
		{40 * time.Second, 43 * time.Second},
		{80 * time.Second, 83 * time.Second},
	}

	for i, tc := range cases {
		d := b.Next()
		assert.GreaterOrEqual(t, d, tc.minD, "attempt %d: duration %v below min %v", i, d, tc.minD)
		assert.Less(t, d, tc.maxD, "attempt %d: duration %v at or above max %v", i, d, tc.maxD)
	}

	// After enough doublings the cap kicks in.
	b2 := ingestion.NewBackoff(fc)
	for i := 0; i < 20; i++ {
		d := b2.Next()
		assert.LessOrEqual(t, d, 15*time.Minute, "duration exceeded Max on attempt %d", i)
	}
}

func TestBackoffReset(t *testing.T) {
	fc := ingestion.NewFakeClock(epoch)
	b := ingestion.NewBackoff(fc)

	// Advance a few attempts so the duration grows.
	for i := 0; i < 5; i++ {
		b.Next()
	}

	b.Reset()

	// After reset the first Next() should be back in the base range [5s, 8s).
	d := b.Next()
	assert.GreaterOrEqual(t, d, 5*time.Second, "post-reset duration %v below 5s", d)
	assert.Less(t, d, 8*time.Second, "post-reset duration %v at or above 8s", d)
}

func TestBackoffWaitCancelled(t *testing.T) {
	// Use RealClock with a very long backoff that will never fire naturally,
	// but cancel the context immediately so Wait returns ctx.Err() quickly.
	b := ingestion.NewBackoff(ingestion.RealClock())
	b.Base = 10 * time.Minute // ensure the timer won't fire during the test

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := b.Wait(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestBackoffWaitCompletes(t *testing.T) {
	fc := ingestion.NewFakeClock(epoch)
	b := ingestion.NewBackoff(fc)

	ctx := context.Background()
	err := b.Wait(ctx)
	require.NoError(t, err)
}
