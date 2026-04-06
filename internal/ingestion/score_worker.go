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

// ScoreComputer computes and persists a score for a company.
type ScoreComputer interface {
	ComputeAndStore(ctx context.Context, companyID int, now time.Time) error
}

// ScoreWorker subscribes to the broker and triggers score recomputation for
// companies whenever new events arrive, using a debounce map to coalesce
// multiple events for the same company within a flush window.
type ScoreWorker struct {
	computer ScoreComputer // may be nil (tests, or engine not ready)
	store    *db.Store     // kept for backward compat with nil-computer path
	broker   *broker.Broker
	clock    Clock

	// Per-company cooldown: skip recompute if last compute was < cooldown ago.
	cooldown    time.Duration
	lastCompute map[int]time.Time // companyID → last compute time

	// Global budget: max recomputes per window.
	budgetLimit  int
	budgetCount  int
	budgetReset  time.Time
	budgetWindow time.Duration
}

// NewScoreWorker constructs a ScoreWorker. store may be nil for tests; when nil
// and no ScoreComputer is set, the DB insert step is skipped but broker
// publication still occurs.
func NewScoreWorker(store *db.Store, b *broker.Broker, clock Clock) *ScoreWorker {
	return &ScoreWorker{
		store:        store,
		broker:       b,
		clock:        clock,
		cooldown:     5 * time.Minute,
		lastCompute:  make(map[int]time.Time),
		budgetLimit:  100,
		budgetWindow: time.Minute,
	}
}

// SetComputer attaches a ScoreComputer to the worker. When set, recomputeScore
// delegates to it instead of inserting a zero-value placeholder score.
func (w *ScoreWorker) SetComputer(c ScoreComputer) {
	w.computer = c
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

// recomputeScore applies per-company cooldown and global budget guards before
// delegating to the ScoreComputer (if set) or falling back to a zero-value
// placeholder insert for backward compatibility. It always publishes a
// score_change event to the broker on success.
func (w *ScoreWorker) recomputeScore(ctx context.Context, companyID int) {
	now := w.clock.Now()

	// Per-company cooldown: skip if computed too recently.
	if last, ok := w.lastCompute[companyID]; ok && now.Sub(last) < w.cooldown {
		return
	}

	// Global budget: reset counter when the window has elapsed.
	if now.After(w.budgetReset) {
		w.budgetCount = 0
		w.budgetReset = now.Add(w.budgetWindow)
	}
	if w.budgetCount >= w.budgetLimit {
		log.Printf("[score-worker] budget exhausted, skipping company %d", companyID)
		return
	}

	w.budgetCount++
	w.lastCompute[companyID] = now

	if w.computer != nil {
		if err := w.computer.ComputeAndStore(ctx, companyID, now); err != nil {
			log.Printf("[score-worker] compute score for company %d: %v", companyID, err)
			return
		}
	} else if w.store != nil {
		// Legacy placeholder path: insert a zero-value score row.
		sc := db.Score{
			CompanyID:      companyID,
			CompositeScore: 0,
			WeightVersion:  0,
		}
		if err := w.store.InsertScore(ctx, sc); err != nil {
			log.Printf("[score-worker] insert score for company %d: %v", companyID, err)
			return
		}
	}

	id := companyID
	w.broker.Publish(broker.Event{
		CompanyID: &id,
		EventType: "score_change",
		Source:    "score_worker",
	})
}
