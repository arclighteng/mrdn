package ingestion

import (
	"context"
	"fmt"
	"log"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/arclighteng/mrdn/internal/db"
)

// StreamWorker drives a StreamSource with a persistent connection, inserting
// received events into the store and publishing them to the broker. It applies
// exponential backoff on connect or recv failures and automatically reconnects.
type StreamWorker struct {
	source  StreamSource
	store   *db.Store
	broker  *broker.Broker
	backoff *Backoff
	clock   Clock
}

// NewStreamWorker constructs a StreamWorker for the given source. clock is
// injectable for testing.
func NewStreamWorker(source StreamSource, store *db.Store, b *broker.Broker, clock Clock) *StreamWorker {
	return &StreamWorker{
		source:  source,
		store:   store,
		broker:  b,
		backoff: NewBackoff(clock),
		clock:   clock,
	}
}

// Run starts the connect-recv loop. It returns when ctx is cancelled.
func (w *StreamWorker) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		if err := w.source.Connect(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[%s] connect error: %v", w.source.Name(), err)
			if werr := w.backoff.Wait(ctx); werr != nil {
				return
			}
			continue
		}

		log.Printf("[%s] connected", w.source.Name())
		if w.store != nil {
			if rerr := w.store.RecordPoll(ctx, w.source.Name(), false); rerr != nil {
				log.Printf("[%s] record poll: %v", w.source.Name(), rerr)
			}
		}
		w.recvLoop(ctx)

		// recvLoop returned — either ctx cancelled or recv error.
		if ctx.Err() != nil {
			_ = w.source.Close()
			return
		}

		// Recv error: close and backoff before reconnecting.
		if err := w.source.Close(); err != nil {
			log.Printf("[%s] close error: %v", w.source.Name(), err)
		}
		if werr := w.backoff.Wait(ctx); werr != nil {
			return
		}
	}
}

// recvLoop reads events from the source until an error occurs or ctx is done.
func (w *StreamWorker) recvLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		events, err := w.recvWithRecovery(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[%s] recv error: %v", w.source.Name(), err)
			return // signal caller to reconnect
		}

		// Empty batch (e.g. ping message) — do not reset backoff, just continue.
		if len(events) == 0 {
			continue
		}

		// Successful non-empty recv: persist and publish.
		w.backoff.Reset()

		for _, evt := range events {
			if w.store != nil {
				id, ierr := w.store.InsertEvent(ctx, evt)
				if ierr != nil {
					log.Printf("[%s] insert error: %v", w.source.Name(), ierr)
					continue
				}
				evt.ID = id
			}
			w.broker.Publish(broker.Event{
				ID:         evt.ID,
				CompanyID:  evt.CompanyID,
				Source:     evt.Source,
				EventType:  evt.EventType,
				OccurredAt: evt.OccurredAt,
			})
		}

		if w.store != nil {
			if rerr := w.store.RecordPoll(ctx, w.source.Name(), true); rerr != nil {
				log.Printf("[%s] record poll: %v", w.source.Name(), rerr)
			}
		}
	}
}

// recvWithRecovery calls source.Recv and converts any panic into an error.
func (w *StreamWorker) recvWithRecovery(ctx context.Context) (events []db.Event, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in source %s: %v", w.source.Name(), r)
		}
	}()
	return w.source.Recv(ctx)
}
