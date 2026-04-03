package ingestion

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRebalanceable records every Rebalance call for assertion.
type fakeRebalanceable struct {
	mu      sync.Mutex
	calls   [][]string
	callErr error
}

func (f *fakeRebalanceable) Rebalance(symbols []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]string, len(symbols))
	copy(cp, symbols)
	f.calls = append(f.calls, cp)
	return f.callErr
}

func (f *fakeRebalanceable) lastCall() ([]string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil, false
	}
	return f.calls[len(f.calls)-1], true
}

func (f *fakeRebalanceable) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// makeRankings builds a slice of n ScoreRankings with synthetic ticker names.
func makeRankings(n int) []db.ScoreRanking {
	out := make([]db.ScoreRanking, n)
	for i := range out {
		out[i] = db.ScoreRanking{
			Ticker:         string(rune('A'+i%26)) + "AA",
			CompanyName:    "Company",
			CompositeScore: float64(n - i),
		}
	}
	return out
}

// TestRebalancer_SelectsTop30 confirms that when rankFn returns 50 rankings the
// rebalancer requests exactly 30 (topN+buffer) and passes all 30 tickers.
func TestRebalancer_SelectsTop30(t *testing.T) {
	target := &fakeRebalanceable{}
	clock := newTestClock()

	var capturedLimit int
	rankFn := func(_ context.Context, limit int) ([]db.ScoreRanking, error) {
		capturedLimit = limit
		// Simulate the store returning exactly what was requested.
		return makeRankings(limit), nil
	}

	r := NewRebalancer(rankFn, target, clock, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run one tick synchronously via the private helper.
	r.tick(ctx)

	assert.Equal(t, rebalancerTopN+rebalancerBuffer, capturedLimit, "should request topN+buffer")

	got, ok := target.lastCall()
	require.True(t, ok, "Rebalance should have been called")
	assert.Len(t, got, rebalancerTopN+rebalancerBuffer, "should pass all 30 tickers")
}

// TestRebalancer_HandlesEmptyRankings confirms Rebalance is called with an
// empty slice when rankFn returns no results.
func TestRebalancer_HandlesEmptyRankings(t *testing.T) {
	target := &fakeRebalanceable{}
	clock := newTestClock()

	rankFn := func(_ context.Context, _ int) ([]db.ScoreRanking, error) {
		return []db.ScoreRanking{}, nil
	}

	r := NewRebalancer(rankFn, target, clock, time.Minute)
	r.tick(context.Background())

	got, ok := target.lastCall()
	require.True(t, ok, "Rebalance should have been called even for empty rankings")
	assert.Empty(t, got)
}

// TestRebalancer_CallsRebalance verifies the ticker symbols forwarded to
// target.Rebalance match those returned by rankFn.
func TestRebalancer_CallsRebalance(t *testing.T) {
	want := []string{"AAPL", "MSFT", "GOOGL"}
	target := &fakeRebalanceable{}
	clock := newTestClock()

	rankFn := func(_ context.Context, _ int) ([]db.ScoreRanking, error) {
		rankings := make([]db.ScoreRanking, len(want))
		for i, tk := range want {
			rankings[i] = db.ScoreRanking{Ticker: tk}
		}
		return rankings, nil
	}

	r := NewRebalancer(rankFn, target, clock, time.Minute)
	r.tick(context.Background())

	got, ok := target.lastCall()
	require.True(t, ok)
	assert.Equal(t, want, got)
}

// TestRebalancer_StopsOnCancel verifies that Run returns promptly when the
// context is cancelled.
func TestRebalancer_StopsOnCancel(t *testing.T) {
	target := &fakeRebalanceable{}
	clock := newTestClock() // After() is instant, so the loop drives fast

	rankFn := func(_ context.Context, limit int) ([]db.ScoreRanking, error) {
		return makeRankings(limit), nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run is even called

	r := NewRebalancer(rankFn, target, clock, time.Minute)

	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestRebalancer_ContinuesOnError verifies that a rankFn error is logged and
// the rebalancer continues — Rebalance is not called on error cycles.
func TestRebalancer_ContinuesOnError(t *testing.T) {
	target := &fakeRebalanceable{}
	clock := newTestClock()

	callCount := 0
	rankFn := func(_ context.Context, _ int) ([]db.ScoreRanking, error) {
		callCount++
		if callCount == 1 {
			return nil, errors.New("transient store error")
		}
		return makeRankings(3), nil
	}

	r := NewRebalancer(rankFn, target, clock, time.Minute)

	// First tick: error — Rebalance must not be called.
	r.tick(context.Background())
	assert.Equal(t, 0, target.callCount(), "Rebalance should not be called when rankFn errors")

	// Second tick: success — Rebalance must be called.
	r.tick(context.Background())
	assert.Equal(t, 1, target.callCount(), "Rebalance should be called on success")
}
