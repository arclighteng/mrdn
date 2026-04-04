package ingestion

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/arclighteng/mrdn/internal/config"
	"github.com/arclighteng/mrdn/internal/db"
)

const (
	workerRestartDelay  = 5 * time.Second
	workerPollInterval  = 60 * time.Second
	supervisorStopTimeout = 10 * time.Second
)

// Supervisor manages the lifecycle of all ingestion workers. It launches one
// PollWorker per registered source, recovers from panics, and restarts workers
// that exit unexpectedly.
type Supervisor struct {
	cfg    *config.Config
	store  *db.Store
	broker *broker.Broker
	clock  Clock

	// sources is the set of sources to supervise. Populated by registerSources
	// but can be overridden in tests via WithSources.
	sources []Source

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewSupervisor constructs a Supervisor. Call Start to begin worker goroutines.
func NewSupervisor(cfg *config.Config, store *db.Store, b *broker.Broker, clock Clock) *Supervisor {
	return &Supervisor{
		cfg:    cfg,
		store:  store,
		broker: b,
		clock:  clock,
	}
}

// WithSources overrides the source list used by Start. Intended for tests.
func (s *Supervisor) WithSources(sources []Source) {
	s.sources = sources
}

// registerSources returns the set of Sources to supervise. Wave 4 tasks will
// populate this list with real API sources; for now it returns nil so Start is
// a clean no-op in production.
func (s *Supervisor) registerSources() []Source {
	return nil
}

// Start creates a child context and launches one supervised goroutine per
// source. If WithSources was not called, registerSources is used.
func (s *Supervisor) Start() {
	s.ctx, s.cancel = context.WithCancel(context.Background())

	srcs := s.sources
	if srcs == nil {
		srcs = s.registerSources()
	}

	for _, src := range srcs {
		s.wg.Add(1)
		go s.runWorkerLoop(src)
	}

	log.Printf("[supervisor] started with %d source(s)", len(srcs))
}

// Stop cancels the supervisor context and waits up to 10 seconds for all
// workers to exit. If workers are still running after the timeout it logs a
// warning and returns.
func (s *Supervisor) Stop() {
	if s.cancel != nil {
		s.cancel()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("[supervisor] all workers stopped")
	case <-time.After(supervisorStopTimeout):
		log.Println("[supervisor] stop timed out — some workers may still be running")
	}
}

// runWorkerLoop supervises a single source. It creates a PollWorker, runs it,
// and restarts it after a delay if it exits unexpectedly. A panic in the worker
// is caught here and treated as a crash requiring a restart.
func (s *Supervisor) runWorkerLoop(src Source) {
	defer s.wg.Done()

	for {
		log.Printf("[supervisor] starting worker for source %q", src.Name())
		crashed := s.runWorkerOnce(src)

		// Check whether we should exit before restarting.
		if s.ctx.Err() != nil {
			log.Printf("[supervisor] stopping worker for source %q (context cancelled)", src.Name())
			return
		}

		if crashed {
			log.Printf("[supervisor] worker for source %q panicked; restarting in %s", src.Name(), workerRestartDelay)
		} else {
			log.Printf("[supervisor] worker for source %q exited; restarting in %s", src.Name(), workerRestartDelay)
		}

		select {
		case <-s.ctx.Done():
			log.Printf("[supervisor] stopping worker for source %q (context cancelled during restart delay)", src.Name())
			return
		case <-s.clock.After(workerRestartDelay):
		}
	}
}

// runWorkerOnce runs the PollWorker for src until it returns (or panics).
// It returns true if the worker panicked, false if it returned normally.
func (s *Supervisor) runWorkerOnce(src Source) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[supervisor] panic in worker for source %q: %v", src.Name(), r)
			panicked = true
		}
	}()

	w := NewPollWorker(src, s.store, s.broker, workerPollInterval, s.clock)
	w.Run(s.ctx)
	return false
}
