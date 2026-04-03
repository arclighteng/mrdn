package ingestion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
)

// fakeStreamSource is a test double for StreamSource.
type fakeStreamSource struct {
	name      string
	connectFn func(ctx context.Context) error
	recvFn    func(ctx context.Context) ([]db.Event, error)
	closeFn   func() error
}

func (f *fakeStreamSource) Name() string { return f.name }

func (f *fakeStreamSource) Connect(ctx context.Context) error {
	if f.connectFn != nil {
		return f.connectFn(ctx)
	}
	return nil
}

func (f *fakeStreamSource) Recv(ctx context.Context) ([]db.Event, error) {
	if f.recvFn != nil {
		return f.recvFn(ctx)
	}
	// Default: block until context is cancelled.
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeStreamSource) Close() error {
	if f.closeFn != nil {
		return f.closeFn()
	}
	return nil
}

// TestStreamWorker_ConnectsAndRecvs verifies that the worker connects to the
// source, receives events, and publishes them to the broker.
func TestStreamWorker_ConnectsAndRecvs(t *testing.T) {
	published := make(chan broker.Event, 4)
	b := broker.New(10)

	subCh, err := b.Subscribe("test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Unsubscribe("test") })

	// Mirror broker publishes into our channel for inspection.
	go func() {
		for evt := range subCh {
			published <- evt
		}
	}()

	recvCount := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := &fakeStreamSource{
		name: "test-stream",
		recvFn: func(ctx context.Context) ([]db.Event, error) {
			recvCount++
			if recvCount == 1 {
				return []db.Event{
					{Source: "test-stream", EventType: "market_trade", OccurredAt: time.Now()},
				}, nil
			}
			// After delivering one batch, block until context done.
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	clock := newTestClock()
	w := NewStreamWorker(src, nil, b, clock)

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// With nil store no insert happens, so broker.Publish is called with ID=0.
	// The source delivers one event then blocks, so worker publishes exactly one.
	select {
	case evt := <-published:
		assert.Equal(t, "test-stream", evt.Source)
		assert.Equal(t, "market_trade", evt.EventType)
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("no event published to broker within timeout")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestStreamWorker_ReconnectsOnError verifies that when Recv returns an error
// the worker closes the source, backs off, and reconnects.
func TestStreamWorker_ReconnectsOnError(t *testing.T) {
	connects := make(chan struct{}, 10)
	recvCallCount := 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := &fakeStreamSource{
		name: "error-stream",
		connectFn: func(ctx context.Context) error {
			select {
			case connects <- struct{}{}:
			default:
			}
			return nil
		},
		recvFn: func(ctx context.Context) ([]db.Event, error) {
			recvCallCount++
			if recvCallCount <= 2 {
				return nil, errors.New("injected recv error")
			}
			// Block after two errors so the test can cancel cleanly.
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	clock := newTestClock() // instantaneous backoff
	b := broker.New(10)
	w := NewStreamWorker(src, nil, b, clock)

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Expect at least two Connect calls (initial + one reconnect after error).
	connectCount := 0
	deadline := time.After(2 * time.Second)
	for connectCount < 2 {
		select {
		case <-connects:
			connectCount++
		case <-deadline:
			t.Fatalf("only saw %d connects before timeout; expected at least 2", connectCount)
		}
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestStreamWorker_StopsOnCancel verifies that Run returns promptly when the
// context is cancelled before the first connect.
func TestStreamWorker_StopsOnCancel(t *testing.T) {
	src := &fakeStreamSource{name: "cancel-stream"}
	b := broker.New(10)
	clock := newTestClock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	w := NewStreamWorker(src, nil, b, clock)

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

// TestStreamWorker_PanicRecovery verifies that a panicking Recv does not crash
// Run; the worker should log the error and reconnect.
func TestStreamWorker_PanicRecovery(t *testing.T) {
	connects := make(chan struct{}, 10)
	recvCallCount := 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := &fakeStreamSource{
		name: "panic-stream",
		connectFn: func(ctx context.Context) error {
			select {
			case connects <- struct{}{}:
			default:
			}
			return nil
		},
		recvFn: func(ctx context.Context) ([]db.Event, error) {
			recvCallCount++
			if recvCallCount == 1 {
				panic("test panic in recv")
			}
			// After panic recovery and reconnect, block until done.
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	clock := newTestClock()
	b := broker.New(10)
	w := NewStreamWorker(src, nil, b, clock)

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Wait for second Connect, which proves the worker survived the panic.
	connectCount := 0
	deadline := time.After(2 * time.Second)
	for connectCount < 2 {
		select {
		case <-connects:
			connectCount++
		case <-deadline:
			t.Fatalf("only saw %d connects before timeout; panic may have crashed Run", connectCount)
		}
	}
	cancel()

	select {
	case <-done:
		// expected — Run survived the panic
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
