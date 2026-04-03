package ingestion

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSource is a test double for Source.
type fakeSource struct {
	name        string
	events      []db.Event
	err         error
	polls       chan struct{} // receives a token on each Poll call
	panicOnPoll bool
	// blockAfter, if > 0, causes Poll to block on ctx.Done() once pollCount
	// exceeds blockAfter. Set to 1 to block from the second call onward, 2 to
	// block from the third, etc. This prevents runaway goroutines from hitting
	// nil-store paths during test teardown.
	blockAfter int
	pollCount  int
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Poll(ctx context.Context) ([]db.Event, error) {
	f.pollCount++
	if f.panicOnPoll && f.pollCount == 1 {
		panic("test panic")
	}
	if f.polls != nil {
		select {
		case f.polls <- struct{}{}:
		default:
		}
	}
	if f.blockAfter > 0 && f.pollCount > f.blockAfter {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.events, f.err
}

// newFakeClock returns a FakeClock anchored at a fixed moment.
func newTestClock() *FakeClock {
	return NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
}

// TestPollWorker_StopsOnCancel verifies that Run returns promptly when the
// context is cancelled before the first poll.
func TestPollWorker_StopsOnCancel(t *testing.T) {
	src := &fakeSource{name: "test-source"}
	b := broker.New(10)
	clock := newTestClock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	w := NewPollWorker(src, nil, b, time.Second, clock)

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestPollWorker_CallsSource verifies that Run invokes Poll on the source.
// The source returns an error so the worker takes the backoff path (no store
// calls), allowing a nil store to be passed safely.
func TestPollWorker_CallsSource(t *testing.T) {
	polls := make(chan struct{}, 1)
	src := &fakeSource{
		name:       "test-source",
		err:        errors.New("injected error"),
		polls:      polls,
		blockAfter: 1, // block on ctx.Done() starting from the second call
	}
	b := broker.New(10)
	clock := newTestClock() // makes backoff.Wait instantaneous

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// nil store is safe: error path only calls SetSourceStatus after ≥3
	// consecutive failures. blockAfter=1 ensures the second poll blocks on
	// ctx.Done(), so the failure count never reaches 3.
	w := NewPollWorker(src, nil, b, time.Second, clock)

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Wait for at least one Poll call.
	select {
	case <-polls:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("Poll was not called within timeout")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestPollWorker_PanicRecovery verifies that a panicking source does not crash
// Run; the worker should log the error and continue (until context is done).
func TestPollWorker_PanicRecovery(t *testing.T) {
	src := &fakeSource{
		name:        "panic-source",
		panicOnPoll: true,
		err:         errors.New("post-panic error"), // returned for polls 2+
		blockAfter:  1,                              // block on ctx.Done() from poll 2 onward
	}
	b := broker.New(10)
	clock := newTestClock() // FakeClock makes backoff.Wait instant

	ctx, cancel := context.WithCancel(context.Background())

	w := NewPollWorker(src, nil, b, time.Second, clock)

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Give the worker a moment to execute the first (panicking) poll and
	// reach the blocking second poll.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// expected — Run survived the panic
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestPollWorker_BacksOffOnError verifies that when the source returns an error
// the worker calls backoff.Wait (via FakeClock.After which is instantaneous)
// and retries.  We count polls via the channel and cancel after seeing two.
func TestPollWorker_BacksOffOnError(t *testing.T) {
	polls := make(chan struct{}, 10)
	src := &fakeSource{
		name:       "error-source",
		err:        errors.New("temporary failure"),
		polls:      polls,
		blockAfter: 2, // allow 2 polls freely, block thereafter to avoid nil-store at ≥3 failures
	}
	b := broker.New(10)
	clock := newTestClock() // After() is instant, so backoff never blocks

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := NewPollWorker(src, nil, b, time.Second, clock)

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Wait for at least two polls to confirm the retry loop is working.
	count := 0
	deadline := time.After(2 * time.Second)
	for count < 2 {
		select {
		case <-polls:
			count++
		case <-deadline:
			t.Fatalf("only saw %d polls before timeout; expected at least 2", count)
		}
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestPollWorker_PublishesToBroker verifies end-to-end event flow: source
// returns an event, worker inserts it via the store, and publishes to the
// broker.  Requires a real database; skips if DATABASE_URL is not set.
func TestPollWorker_PublishesToBroker(t *testing.T) {
	dsn := testDSN(t) // skips if DATABASE_URL unset

	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	store := db.NewStore(pool)
	b := broker.New(10)

	ch, err := b.Subscribe("test-sub")
	require.NoError(t, err)
	t.Cleanup(func() { b.Unsubscribe("test-sub") })

	occurred := time.Now().UTC().Truncate(time.Second)
	src := &fakeSource{
		name: "sec",
		events: []db.Event{
			{
				Source:     "sec",
				EventType:  "filing",
				OccurredAt: occurred,
			},
		},
	}

	clock := newTestClock()
	w := NewPollWorker(src, store, b, time.Hour, clock) // long interval — we cancel after one poll

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()

	go w.Run(ctx2)

	select {
	case evt := <-ch:
		assert.Equal(t, "sec", evt.Source)
		assert.Equal(t, "filing", evt.EventType)
		assert.True(t, evt.ID > 0, "event ID should be set after insert")
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("no event published to broker within timeout")
	}
}

// testDSN returns the DATABASE_URL environment variable or calls t.Skip.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return dsn
}
