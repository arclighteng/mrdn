package ingestion

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/arclighteng/mrdn/internal/db"
)

// EventResolver is the interface for post-insert entity resolution.
// Implemented by resolver.Resolver.
type EventResolver interface {
	Resolve(ctx context.Context, evt db.Event) int
}

// PollWorker drives a Source on a fixed interval, inserting events into the
// store, publishing them to the broker, and updating source health metadata.
// It applies exponential backoff on consecutive poll failures.
type PollWorker struct {
	source   Source
	store    *db.Store
	broker   *broker.Broker
	resolver EventResolver
	backoff  *Backoff
	interval time.Duration
	clock    Clock
}

// NewPollWorker constructs a PollWorker for the given source. interval controls
// the sleep between successful polls; clock is injectable for testing.
func NewPollWorker(source Source, store *db.Store, b *broker.Broker, interval time.Duration, clock Clock) *PollWorker {
	return &PollWorker{
		source:   source,
		store:    store,
		broker:   b,
		backoff:  NewBackoff(clock),
		interval: interval,
		clock:    clock,
	}
}

// SetResolver sets the entity resolver for post-insert processing.
func (w *PollWorker) SetResolver(r EventResolver) {
	w.resolver = r
}

// Run starts the polling loop. It returns when ctx is cancelled.
func (w *PollWorker) Run(ctx context.Context) {
	consecutiveFailures := 0

	for {
		if ctx.Err() != nil {
			return
		}

		events, err := w.pollWithRecovery(ctx)
		if err != nil {
			consecutiveFailures++
			log.Printf("[%s] poll error (%d consecutive): %v", w.source.Name(), consecutiveFailures, err)

			if w.store != nil {
				if consecutiveFailures >= 10 {
					if serr := w.store.SetSourceStatus(ctx, w.source.Name(), "down"); serr != nil {
						log.Printf("[%s] set status down: %v", w.source.Name(), serr)
					}
				} else if consecutiveFailures >= 3 {
					if serr := w.store.SetSourceStatus(ctx, w.source.Name(), "degraded"); serr != nil {
						log.Printf("[%s] set status degraded: %v", w.source.Name(), serr)
					}
				}
			}

			if werr := w.backoff.Wait(ctx); werr != nil {
				return // context cancelled
			}
			continue
		}

		// Success path.
		consecutiveFailures = 0
		w.backoff.Reset()
		hasNewData := len(events) > 0

		if w.store != nil {
			ids, berr := w.store.InsertEventsBatch(ctx, events)
			if berr != nil {
				log.Printf("[%s] batch insert error: %v", w.source.Name(), berr)
			}
			for i, evt := range events {
				id := 0
				if i < len(ids) {
					id = ids[i]
				}
				if id == 0 {
					continue // skipped (validation failure) or batch aborted
				}
				evt.ID = id
				if w.resolver != nil {
					if cid := w.resolver.Resolve(ctx, evt); cid > 0 {
						evt.CompanyID = &cid
					}
				}
				w.broker.Publish(broker.Event{
					ID:         id,
					CompanyID:  evt.CompanyID,
					Source:     evt.Source,
					EventType:  evt.EventType,
					OccurredAt: evt.OccurredAt,
				})
			}

			if rerr := w.store.RecordPoll(ctx, w.source.Name(), hasNewData); rerr != nil {
				log.Printf("[%s] record poll: %v", w.source.Name(), rerr)
			}
			if hasNewData {
				if serr := w.store.SetSourceStatus(ctx, w.source.Name(), "healthy"); serr != nil {
					log.Printf("[%s] set status healthy: %v", w.source.Name(), serr)
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-w.clock.After(w.interval):
		}
	}
}

// pollWithRecovery calls source.Poll and converts any panic into an error.
func (w *PollWorker) pollWithRecovery(ctx context.Context) (events []db.Event, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in source %s: %v", w.source.Name(), r)
		}
	}()
	return w.source.Poll(ctx)
}
