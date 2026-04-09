package score

import (
	"context"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

// InsiderWindow is the lookback period used for all insider sub-score inputs.
const InsiderWindow = 90 * 24 * time.Hour

// Insider trade score thresholds (SEC Form 4 filing count within the insider window).
const (
	insiderTradesNeutral = 0  // 0 filings → 50 (neutral)
	insiderTradesLow     = 3  // 1–3 filings → 65
	insiderTradesMid     = 10 // 4–10 filings → 80
	// 11+ filings → 90
)

// Donation amount score thresholds (total donation amount in cents within the insider window).
const (
	// insiderDonationThresholdLow is $10K in cents; amounts below this score 60.
	insiderDonationThresholdLow int64 = 10_000 * 100 // $10K in cents
	// insiderDonationThresholdHigh is $100K in cents; amounts at or above this score 90.
	insiderDonationThresholdHigh int64 = 100_000 * 100 // $100K in cents
)

// Sub-score component weights within the insider scorer (must sum to 1.0).
const (
	insiderWeightTrades    = 0.50
	insiderWeightDonations = 0.50
)

// InsiderScorer computes the insider sub-score (0–100) for a company using
// SEC Form 4 insider trade filings and political donation amounts.
type InsiderScorer struct {
	store ScoreStore
}

// NewInsiderScorer constructs an InsiderScorer backed by the given store.
func NewInsiderScorer(store ScoreStore) *InsiderScorer {
	return &InsiderScorer{store: store}
}

// Score returns an insider sub-score in [0, 100] for the given company at now.
// Returns 50.0 (neutral) when no data is available in either category.
func (is *InsiderScorer) Score(ctx context.Context, companyID int, now time.Time) (float64, error) {
	since := now.Add(-InsiderWindow)

	trades, err := is.store.GetInsiderTradesRange(ctx, companyID, since, now)
	if err != nil {
		return 0, err
	}
	donations, err := is.store.GetDonationsRange(ctx, companyID, since, now)
	if err != nil {
		return 0, err
	}

	tradeScore := insiderTradesScore(len(trades))
	donationScore := insiderDonationsScore(donations)

	return clamp(
		insiderWeightTrades*tradeScore +
			insiderWeightDonations*donationScore,
	), nil
}

// insiderTradesScore maps a Form 4 filing count to a component score.
// 0 filings → 50 (neutral); higher counts indicate elevated insider activity.
func insiderTradesScore(count int) float64 {
	switch {
	case count == insiderTradesNeutral:
		return 50.0
	case count <= insiderTradesLow:
		return 65.0
	case count <= insiderTradesMid:
		return 80.0
	default:
		return 90.0
	}
}

// insiderDonationsScore maps total donation amount (in cents) to a component score.
// $0 → 50 (neutral), $1–$10K → 60, $10K–$100K → 75, $100K+ → 90.
func insiderDonationsScore(donations []db.Donation) float64 {
	var totalCents int64
	for _, d := range donations {
		if d.AmountCents != nil {
			totalCents += *d.AmountCents
		}
	}
	switch {
	case totalCents == 0:
		return 50.0
	case totalCents < insiderDonationThresholdLow:
		return 60.0
	case totalCents < insiderDonationThresholdHigh:
		return 75.0
	default:
		return 90.0
	}
}
