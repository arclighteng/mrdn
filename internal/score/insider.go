package score

import (
	"context"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

// InsiderWindow is the lookback period used for all insider sub-score inputs.
const InsiderWindow = 90 * 24 * time.Hour

// Sub-score component weights within the insider scorer.
// These are the "ideal" weights when all three data sources have data.
// When a source is empty, its weight is redistributed proportionally.
const (
	insiderWeightSECTrades          = 0.30 // SEC Form 4 insider filings
	insiderWeightCongressionalTrades = 0.50 // Congressional trades on this company
	insiderWeightDonations          = 0.20 // Political donations
)

// InsiderScorer computes the insider sub-score (0–100) for a company using
// SEC Form 4 insider trade filings, congressional trading activity, and
// political donation amounts. When a data source has no data, its weight
// is redistributed to sources that do, so the score reflects available
// information rather than defaulting to neutral.
type InsiderScorer struct {
	store ScoreStore
}

// NewInsiderScorer constructs an InsiderScorer backed by the given store.
func NewInsiderScorer(store ScoreStore) *InsiderScorer {
	return &InsiderScorer{store: store}
}

// Score returns an insider sub-score in [0, 100] for the given company at now.
// Returns 50.0 (neutral) only when no data is available in any category.
func (is *InsiderScorer) Score(ctx context.Context, companyID int, now time.Time) (float64, error) {
	since := now.Add(-InsiderWindow)

	trades, err := is.store.GetInsiderTradesRange(ctx, companyID, since, now)
	if err != nil {
		return 0, err
	}
	congTrades, err := is.store.GetCongressionalTradesForCompany(ctx, companyID, since, now)
	if err != nil {
		return 0, err
	}
	donations, err := is.store.GetDonationsRange(ctx, companyID, since, now)
	if err != nil {
		return 0, err
	}

	// Compute raw scores for each component.
	secScore := secTradesScore(len(trades))
	congScore := congressionalTradesScore(len(congTrades))
	donScore := donationAmountScore(donations)

	// Determine which components have data and redistribute weights.
	type component struct {
		score  float64
		weight float64
		has    bool
	}
	components := []component{
		{secScore, insiderWeightSECTrades, len(trades) > 0},
		{congScore, insiderWeightCongressionalTrades, len(congTrades) > 0},
		{donScore, insiderWeightDonations, len(donations) > 0},
	}

	// Count how many have data.
	var activeWeight float64
	for _, c := range components {
		if c.has {
			activeWeight += c.weight
		}
	}

	// If nothing has data, return neutral.
	if activeWeight == 0 {
		return 50.0, nil
	}

	// Weighted sum, redistributing empty-source weight to active sources.
	var total float64
	for _, c := range components {
		if c.has {
			total += (c.weight / activeWeight) * c.score
		}
	}

	return clamp(total), nil
}

// secTradesScore maps a SEC Form 4 filing count to a component score.
// 0 filings → 50 (neutral); higher counts indicate elevated insider activity.
func secTradesScore(count int) float64 {
	switch {
	case count == 0:
		return 50.0
	case count <= 3:
		return 65.0
	case count <= 10:
		return 80.0
	default:
		return 90.0
	}
}

// congressionalTradesScore maps a congressional trade count to a component score.
// More congressional attention to a company → higher score.
func congressionalTradesScore(count int) float64 {
	switch {
	case count == 0:
		return 50.0
	case count <= 2:
		return 60.0
	case count <= 5:
		return 70.0
	case count <= 10:
		return 80.0
	default:
		return 90.0
	}
}

// donationAmountScore maps total donation amount (in cents) to a component score.
func donationAmountScore(donations []db.Donation) float64 {
	var totalCents int64
	for _, d := range donations {
		if d.AmountCents != nil {
			totalCents += *d.AmountCents
		}
	}
	switch {
	case totalCents == 0:
		return 50.0
	case totalCents < 10_000*100: // < $10K
		return 60.0
	case totalCents < 100_000*100: // < $100K
		return 75.0
	default:
		return 90.0
	}
}
