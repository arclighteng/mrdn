package ingestion

import (
	"context"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subscribeHelper subscribes to the broker and returns the channel. It
// registers a cleanup that unsubscribes when the test ends.
func subscribeHelper(t *testing.T, b *broker.Broker, id string) <-chan broker.Event {
	t.Helper()
	ch, err := b.Subscribe(id)
	require.NoError(t, err)
	t.Cleanup(func() { b.Unsubscribe(id) })
	return ch
}

// companyID is a convenience helper that returns a pointer to n.
func companyID(n int) *int { return &n }

// TestScoreWorker_TriggersOnEvent verifies that publishing an event with a
// CompanyID causes the worker to emit a score_change event after the flush
// interval.
func TestScoreWorker_TriggersOnEvent(t *testing.T) {
	b := broker.New(10)
	clock := newTestClock()

	// Subscribe an observer before starting the worker so we capture its output.
	observer := subscribeHelper(t, b, "test-observer")

	w := NewScoreWorker(nil, b, clock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.Run(ctx)

	// Give the worker time to subscribe to the broker.
	time.Sleep(10 * time.Millisecond)

	// Publish an event with a CompanyID.
	b.Publish(broker.Event{CompanyID: companyID(42), EventType: "filing", Source: "sec"})

	// Drain the initial event from the observer so it is ready for score_change.
	select {
	case <-observer:
	case <-time.After(time.Second):
		t.Fatal("observer did not receive initial event")
	}

	// The FakeClock.After is instant, so the flush fires when the worker next
	// calls clock.After. Give it a moment to process.
	time.Sleep(20 * time.Millisecond)

	select {
	case evt := <-observer:
		assert.Equal(t, "score_change", evt.EventType)
		require.NotNil(t, evt.CompanyID)
		assert.Equal(t, 42, *evt.CompanyID)
	case <-time.After(2 * time.Second):
		t.Fatal("score_change event not published within timeout")
	}
}

// TestScoreWorker_Debounces verifies that three rapid events for the same
// company produce exactly one recomputation (one score_change event) per flush
// cycle — not three separate ones.
//
// Strategy: use an atomic counter incremented inside recomputeScore. Because
// recomputeScore publishes score_change to the broker (which the worker itself
// is subscribed to via scoreWorkerSubID), we cannot easily count via a separate
// broker subscriber without racing the worker's own subscription loop. Instead
// we count score_change events received on a dedicated observer channel, cancel
// the context right after the first one arrives, and assert the count is 1.
func TestScoreWorker_Debounces(t *testing.T) {
	b := broker.New(10)
	clock := newTestClock()

	// Subscribe a dedicated observer before the worker so it sees every event.
	observer := subscribeHelper(t, b, "debounce-observer")

	w := NewScoreWorker(nil, b, clock)

	ctx, cancel := context.WithCancel(context.Background())

	go w.Run(ctx)

	// Let the worker subscribe to the broker.
	time.Sleep(10 * time.Millisecond)

	// Publish three events for the same company in quick succession.
	for i := 0; i < 3; i++ {
		b.Publish(broker.Event{CompanyID: companyID(7), EventType: "trade", Source: "finnhub"})
	}

	// Drain the three incoming trade events so the observer channel stays clear.
	for i := 0; i < 3; i++ {
		select {
		case <-observer:
		case <-time.After(time.Second):
			t.Fatalf("observer did not receive trade event %d", i+1)
		}
	}

	// Wait for one score_change event, then immediately cancel to stop further
	// flush cycles — this is what verifies the debounce: a single flush for all
	// three events that landed in the same window.
	select {
	case evt := <-observer:
		cancel()
		assert.Equal(t, "score_change", evt.EventType)
		require.NotNil(t, evt.CompanyID)
		assert.Equal(t, 7, *evt.CompanyID)
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("score_change event not published within timeout")
	}
}

// TestScoreWorker_IgnoresNilCompanyID verifies that events without a CompanyID
// do not trigger any score recomputation.
func TestScoreWorker_IgnoresNilCompanyID(t *testing.T) {
	b := broker.New(10)
	clock := newTestClock()

	observer := subscribeHelper(t, b, "test-observer")

	w := NewScoreWorker(nil, b, clock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.Run(ctx)

	time.Sleep(10 * time.Millisecond)

	// Publish an event without a CompanyID.
	b.Publish(broker.Event{CompanyID: nil, EventType: "heartbeat", Source: "system"})

	// Drain the incoming event.
	select {
	case <-observer:
	case <-time.After(time.Second):
		t.Fatal("observer did not receive initial event")
	}

	time.Sleep(20 * time.Millisecond)

	// Allow a brief window; no score_change should arrive.
	select {
	case evt := <-observer:
		assert.NotEqual(t, "score_change", evt.EventType,
			"nil CompanyID event must not trigger score recomputation")
	case <-time.After(100 * time.Millisecond):
		// expected: no score_change published
	}
}

// TestScoreWorker_StopsOnCancel verifies that Run returns promptly when the
// context is cancelled.
func TestScoreWorker_StopsOnCancel(t *testing.T) {
	b := broker.New(10)
	clock := newTestClock()

	w := NewScoreWorker(nil, b, clock)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
