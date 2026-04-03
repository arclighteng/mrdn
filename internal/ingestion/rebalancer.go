package ingestion

import (
	"context"
	"log"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

// Rebalanceable is the subset of FinnhubSource the Rebalancer needs.
type Rebalanceable interface {
	Rebalance(symbols []string) error
}

// RankFunc fetches a ranked list of companies from the store, limited to the
// given count. The signature matches db.Store.GetScoreRankings so the method
// value can be passed directly in production.
type RankFunc func(ctx context.Context, limit int) ([]db.ScoreRanking, error)

const (
	rebalancerTopN   = 25 // primary subscription slots
	rebalancerBuffer = 5  // buffer slots to absorb churn
)

// Rebalancer periodically queries score rankings and instructs a Rebalanceable
// target (e.g. the Finnhub WebSocket source) to subscribe to the top-ranked
// symbols.
type Rebalancer struct {
	rankFn   RankFunc
	target   Rebalanceable
	clock    Clock
	interval time.Duration
}

// NewRebalancer constructs a Rebalancer. In production pass store.GetScoreRankings
// as rankFn and the FinnhubSource as target.
func NewRebalancer(rankFn RankFunc, target Rebalanceable, clock Clock, interval time.Duration) *Rebalancer {
	return &Rebalancer{
		rankFn:   rankFn,
		target:   target,
		clock:    clock,
		interval: interval,
	}
}

// Run starts the rebalance loop and blocks until ctx is cancelled.
func (r *Rebalancer) Run(ctx context.Context) {
	for {
		r.tick(ctx)

		select {
		case <-ctx.Done():
			return
		case <-r.clock.After(r.interval):
		}
	}
}

// tick performs a single rebalance cycle: fetch rankings, extract tickers, and
// call target.Rebalance. Errors are logged but do not abort the loop.
func (r *Rebalancer) tick(ctx context.Context) {
	limit := rebalancerTopN + rebalancerBuffer

	rankings, err := r.rankFn(ctx, limit)
	if err != nil {
		log.Printf("[rebalancer] fetch rankings: %v", err)
		return
	}

	symbols := make([]string, 0, len(rankings))
	for _, rk := range rankings {
		symbols = append(symbols, rk.Ticker)
	}

	if err := r.target.Rebalance(symbols); err != nil {
		log.Printf("[rebalancer] rebalance call: %v", err)
		return
	}

	log.Printf("[rebalancer] rebalanced: %d symbol(s) selected", len(symbols))
}
