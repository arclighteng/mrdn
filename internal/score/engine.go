// Package score computes composite risk scores for companies from typed domain
// table data. Each sub-scorer (market, policy, insider) returns a value in
// [0, 100] where 50 is always the neutral baseline for missing data. The
// engine combines them via configurable weights and clamps the composite to
// [0, 100].
package score

import (
	"context"
	"fmt"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

// Weights for the composite score calculation.
// The three weights must sum to 1.0 for the composite to remain in [0, 100].
const (
	// DefaultMarketWeight is the fraction of the composite driven by market signals.
	DefaultMarketWeight = 0.35
	// DefaultPolicyWeight is the fraction of the composite driven by policy signals
	// (sanctions, contracts, donations).
	DefaultPolicyWeight = 0.40
	// DefaultInsiderWeight is the fraction of the composite driven by insider
	// activity (SEC Form 4 filings and political donations).
	DefaultInsiderWeight = 0.25
)

// Weights holds the sub-score multipliers used by Engine.Compute.
type Weights struct {
	Market  float64
	Policy  float64
	Insider float64
}

// DefaultWeights returns the production weight configuration.
func DefaultWeights() Weights {
	return Weights{
		Market:  DefaultMarketWeight,
		Policy:  DefaultPolicyWeight,
		Insider: DefaultInsiderWeight,
	}
}

// ScoreStore defines the data-access methods required by the score engine.
// *db.Store satisfies this interface.
type ScoreStore interface {
	GetMarketDataRange(ctx context.Context, companyID int, since, until time.Time) ([]db.MarketDataRow, error)
	GetInsiderTradesRange(ctx context.Context, companyID int, since, until time.Time) ([]db.InsiderTrade, error)
	GetSanctionsRange(ctx context.Context, companyID int, since, until time.Time) ([]db.Sanction, error)
	GetContractsRange(ctx context.Context, companyID int, since, until time.Time) ([]db.Contract, error)
	GetDonationsRange(ctx context.Context, companyID int, since, until time.Time) ([]db.Donation, error)
	InsertScore(ctx context.Context, sc db.Score) error
}

// SubScorer computes a single sub-score in [0, 100] for one company.
// Implementations must return 50.0 (neutral) when no data is available.
type SubScorer interface {
	Score(ctx context.Context, companyID int, now time.Time) (float64, error)
}

// Engine computes and persists composite risk scores.
type Engine struct {
	store   ScoreStore
	market  SubScorer
	policy  SubScorer
	insider SubScorer
	weights Weights
}

// NewEngine constructs an Engine with explicitly injected sub-scorers and weights.
func NewEngine(store ScoreStore, market, policy, insider SubScorer, weights Weights) *Engine {
	return &Engine{
		store:   store,
		market:  market,
		policy:  policy,
		insider: insider,
		weights: weights,
	}
}

// Compute calculates all three sub-scores and the weighted composite for the
// given company at point-in-time now. The returned Score is ready to pass to
// InsertScore; the caller decides whether to persist it.
func (e *Engine) Compute(ctx context.Context, companyID int, now time.Time) (*db.Score, error) {
	m, err := e.market.Score(ctx, companyID, now)
	if err != nil {
		return nil, fmt.Errorf("market score: %w", err)
	}
	p, err := e.policy.Score(ctx, companyID, now)
	if err != nil {
		return nil, fmt.Errorf("policy score: %w", err)
	}
	i, err := e.insider.Score(ctx, companyID, now)
	if err != nil {
		return nil, fmt.Errorf("insider score: %w", err)
	}

	composite := clamp(e.weights.Market*m + e.weights.Policy*p + e.weights.Insider*i)

	return &db.Score{
		CompanyID:      companyID,
		MarketScore:    m,
		PolicyScore:    p,
		InsiderScore:   i,
		CompositeScore: composite,
		WeightVersion:  1,
		ComputedAt:     now,
	}, nil
}

// ComputeAndStore computes the score and persists it. Satisfies the
// ingestion.ScoreComputer interface.
func (e *Engine) ComputeAndStore(ctx context.Context, companyID int, now time.Time) error {
	sc, err := e.Compute(ctx, companyID, now)
	if err != nil {
		return err
	}
	return e.store.InsertScore(ctx, *sc)
}

// clamp restricts v to [0, 100].
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
