package ingestion

import (
	"context"
	"log"
	"time"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/arclighteng/mrdn/internal/db"
)

const (
	scoreFlushInterval = 5 * time.Second
	scoreWorkerSubID   = "score-worker"
)

// ScoreWorker subscribes to the broker and triggers score recomputation for
// companies whenever new events arrive, using a debounce map to coalesce
// multiple events for the same company within a flush window.
type ScoreWorker struct {
	store  *db.Store
	broker *broker.Broker
	clock  Clock
}

// NewScoreWorker constructs a ScoreWorker. store may be nil for tests; when nil
// the DB insert step is skipped but broker publication still occurs.
func NewScoreWorker(store *db.Store, b *broker.Broker, clock Clock) *ScoreWorker {
	return &ScoreWorker{
		store:  store,
		broker: b,
		clock:  clock,
	}
}

// Run subscribes to the broker and processes events until ctx is cancelled.
func (w *ScoreWorker) Run(ctx context.Context) {
	ch, err := w.broker.Subscribe(scoreWorkerSubID)
	if err != nil {
		log.Printf("[score-worker] subscribe: %v", err)
		return
	}
	defer w.broker.Unsubscribe(scoreWorkerSubID)

	// pending holds company IDs that need score recomputation.
	pending := make(map[int]struct{})

	for {
		select {
		case <-ctx.Done():
			return

		case evt, ok := <-ch:
			if !ok {
				return
			}
			if evt.CompanyID == nil {
				continue
			}
			pending[*evt.CompanyID] = struct{}{}

		case <-w.clock.After(scoreFlushInterval):
			if len(pending) == 0 {
				continue
			}
			for companyID := range pending {
				w.recomputeScore(ctx, companyID)
			}
			pending = make(map[int]struct{})
		}
	}
}

// recomputeScore is a Phase 3 placeholder. It inserts a zero-value score row
// (skipped when store is nil) and publishes a score_change event to the broker.
func (w *ScoreWorker) recomputeScore(ctx context.Context, companyID int) {
	if w.store != nil {
		sc := db.Score{
			CompanyID:      companyID,
			MarketScore:    0,
			PolicyScore:    0,
			InsiderScore:   0,
			CompositeScore: 0,
			WeightVersion:  0,
		}
		if err := w.store.InsertScore(ctx, sc); err != nil {
			log.Printf("[score-worker] insert score for company %d: %v", companyID, err)
		}
	}

	id := companyID
	w.broker.Publish(broker.Event{
		CompanyID: &id,
		EventType: "score_change",
		Source:    "score_worker",
	})
}
