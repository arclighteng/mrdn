package ingestion

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test helpers -----------------------------------------------------------

// funcSource is a controllable Source whose Poll delegates to a provided function.
// Named distinctly to avoid collision with fakeSource in worker_test.go.
type funcSource struct {
	name   string
	pollFn func(ctx context.Context) ([]db.Event, error)
}

func (f *funcSource) Name() string { return f.name }
func (f *funcSource) Poll(ctx context.Context) ([]db.Event, error) {
	return f.pollFn(ctx)
}

// newFuncSource returns a Source whose Poll calls the provided function.
func newFuncSource(name string, fn func(ctx context.Context) ([]db.Event, error)) Source {
	return &funcSource{name: name, pollFn: fn}
}

// newSupervisorForTest builds a Supervisor with nil store/broker (safe for
// unit tests that don't reach the DB/broker code paths).
func newSupervisorForTest(sources []Source) *Supervisor {
	clock := NewFakeClock(time.Now())
	s := NewSupervisor(nil, nil, nil, clock)
	s.WithSources(sources)
	return s
}

// --- tests ------------------------------------------------------------------

// TestSupervisor_StartStop verifies that Start followed immediately by Stop
// exits cleanly with no goroutine leak.
func TestSupervisor_StartStop(t *testing.T) {
	s := newSupervisorForTest(nil) // no sources
	s.Start()
	s.Stop()
	// If we reach here without deadlock the test passes.
}

// TestSupervisor_RunsRegisteredWorkers verifies that each registered source is
// polled at least once before the supervisor is stopped.
func TestSupervisor_RunsRegisteredWorkers(t *testing.T) {
	var pollCount atomic.Int32

	src := newFuncSource("test-source", func(ctx context.Context) ([]db.Event, error) {
		pollCount.Add(1)
		// Block until context is cancelled so the worker stays in its loop.
		<-ctx.Done()
		return nil, ctx.Err()
	})

	s := newSupervisorForTest([]Source{src})
	s.Start()

	// Give the worker a moment to call Poll at least once.
	require.Eventually(t, func() bool {
		return pollCount.Load() >= 1
	}, 2*time.Second, 10*time.Millisecond, "expected source to be polled")

	s.Stop()
	assert.GreaterOrEqual(t, pollCount.Load(), int32(1))
}

// TestSupervisor_WorkerPanicRestart verifies that a worker that panics on its
// first invocation is restarted and eventually polls successfully.
func TestSupervisor_WorkerPanicRestart(t *testing.T) {
	var callCount atomic.Int32
	var successCount atomic.Int32

	src := newFuncSource("panic-source", func(ctx context.Context) ([]db.Event, error) {
		n := callCount.Add(1)
		if n == 1 {
			panic("simulated worker panic")
		}
		successCount.Add(1)
		// Block so the worker stays alive after the successful poll.
		<-ctx.Done()
		return nil, ctx.Err()
	})

	// Use a real clock with a very short restart delay by replacing the
	// constant via a custom supervisor clock that returns immediately.
	fc := NewFakeClock(time.Now())
	sup := NewSupervisor(nil, nil, nil, fc)
	sup.WithSources([]Source{src})
	sup.Start()

	// Wait for at least one successful poll (call #2).
	require.Eventually(t, func() bool {
		return successCount.Load() >= 1
	}, 3*time.Second, 10*time.Millisecond, "expected worker to restart and poll successfully")

	sup.Stop()
	assert.GreaterOrEqual(t, callCount.Load(), int32(2), "source should have been called at least twice")
}

// TestSupervisor_StopTimeout verifies that Stop returns even when a worker is
// completely stuck (does not honour context cancellation).
func TestSupervisor_StopTimeout(t *testing.T) {
	// A source whose Poll blocks forever, ignoring ctx.
	started := make(chan struct{})
	src := newFuncSource("stuck-source", func(ctx context.Context) ([]db.Event, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		// Deliberately ignore ctx.Done() to simulate a truly stuck worker.
		select {}
	})

	s := newSupervisorForTest([]Source{src})
	s.Start()

	// Wait until the worker has started polling before stopping.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never started")
	}

	// Override supervisorStopTimeout by calling Stop and verifying it returns
	// within a generous bound (the actual timeout is 10 s; we just verify Stop
	// does not block the test runner forever).
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Stop returned — pass.
	case <-time.After(15 * time.Second):
		t.Fatal("Stop did not return within 15 seconds")
	}
}

// TestSupervisor_NoSources verifies that a supervisor with an empty source
// list starts and stops without error.
func TestSupervisor_NoSources(t *testing.T) {
	s := newSupervisorForTest([]Source{})
	s.Start()
	s.Stop()
}

// TestSupervisor_MultipleWorkers verifies that all registered sources are
// polled concurrently.
func TestSupervisor_MultipleWorkers(t *testing.T) {
	const numSources = 3
	var counts [numSources]atomic.Int32

	sources := make([]Source, numSources)
	for i := range sources {
		idx := i
		sources[idx] = newFuncSource("source-"+string(rune('A'+idx)), func(ctx context.Context) ([]db.Event, error) {
			counts[idx].Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		})
	}

	s := newSupervisorForTest(sources)
	s.Start()

	require.Eventually(t, func() bool {
		for i := range counts {
			if counts[i].Load() < 1 {
				return false
			}
		}
		return true
	}, 2*time.Second, 10*time.Millisecond, "all sources should be polled")

	s.Stop()
}

// TestSupervisor_ErrorDoesNotStopWorker verifies that a source returning an
// error (not a panic) keeps retrying via the backoff path in PollWorker.
func TestSupervisor_ErrorDoesNotStopWorker(t *testing.T) {
	var callCount atomic.Int32

	src := newFuncSource("error-source", func(ctx context.Context) ([]db.Event, error) {
		n := callCount.Add(1)
		if n < 3 {
			return nil, errors.New("transient error")
		}
		// After two errors, block until cancelled.
		<-ctx.Done()
		return nil, ctx.Err()
	})

	fc := NewFakeClock(time.Now())
	sup := NewSupervisor(nil, nil, nil, fc)
	sup.WithSources([]Source{src})
	sup.Start()

	require.Eventually(t, func() bool {
		return callCount.Load() >= 3
	}, 3*time.Second, 10*time.Millisecond, "worker should retry after errors")

	sup.Stop()
}
