package score

import (
	"context"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

// policyWindow is the lookback period used for all policy sub-score inputs.
const policyWindow = 90 * 24 * time.Hour

// Sanctions score thresholds (count within the policy window).
const (
	sanctionsNeutral = 0  // 0 sanctions → 50 (neutral)
	sanctionsLow     = 2  // 1–2 sanctions → 70
	sanctionsMid     = 5  // 3–5 sanctions → 85
	// 6+ sanctions → 95
)

// Contracts score thresholds (total awarded dollar value within the policy window).
const (
	// contractsThresholdMid is $100M expressed in cents; values below this score 75.
	contractsThresholdMid int64 = 100_000_000 * 100 // $100M in cents
)

// Donations score thresholds (count within the policy window).
const (
	donationsNeutral = 0  // 0 donations → 50 (neutral)
	donationsLow     = 5  // 1–5 donations → 60
	donationsMid     = 20 // 6–20 donations → 70
	// 21+ donations → 80
)

// Sub-score component weights within the policy scorer (must sum to 1.0).
const (
	policyWeightSanctions = 0.40
	policyWeightContracts = 0.35
	policyWeightDonations = 0.25
)

// PolicyScorer computes the policy sub-score (0–100) for a company using
// sanctions exposure, government contract value, and political donation signals.
type PolicyScorer struct {
	store ScoreStore
}

// NewPolicyScorer constructs a PolicyScorer backed by the given store.
func NewPolicyScorer(store ScoreStore) *PolicyScorer {
	return &PolicyScorer{store: store}
}

// Score returns a policy sub-score in [0, 100] for the given company at now.
// Each category independently returns 50.0 (neutral) when no data is available.
func (ps *PolicyScorer) Score(ctx context.Context, companyID int, now time.Time) (float64, error) {
	since := now.Add(-policyWindow)

	sanctions, err := ps.store.GetSanctionsRange(ctx, companyID, since, now)
	if err != nil {
		return 0, err
	}
	contracts, err := ps.store.GetContractsRange(ctx, companyID, since, now)
	if err != nil {
		return 0, err
	}
	donations, err := ps.store.GetDonationsRange(ctx, companyID, since, now)
	if err != nil {
		return 0, err
	}

	sanctionScore := sanctionsScore(len(sanctions))
	contractScore := contractsScore(contracts)
	donationScore := donationsScore(len(donations))

	return clamp(
		policyWeightSanctions*sanctionScore +
			policyWeightContracts*contractScore +
			policyWeightDonations*donationScore,
	), nil
}

// sanctionsScore maps a sanction count to a component score.
// 0 sanctions → 50 (neutral); higher counts indicate elevated regulatory exposure.
func sanctionsScore(count int) float64 {
	switch {
	case count == sanctionsNeutral:
		return 50.0
	case count <= sanctionsLow:
		return 70.0
	case count <= sanctionsMid:
		return 85.0
	default:
		return 95.0
	}
}

// contractsScore maps total awarded contract value (in cents) to a component score.
// $0 → 50 (neutral), up to $100M → 75, $100M+ → 90.
func contractsScore(contracts []db.Contract) float64 {
	var totalCents int64
	for _, c := range contracts {
		if c.AmountCents != nil {
			totalCents += *c.AmountCents
		}
	}
	switch {
	case totalCents == 0:
		return 50.0
	case totalCents < contractsThresholdMid:
		// $0–$100M range: linear interpolation from 50 to 75.
		ratio := float64(totalCents) / float64(contractsThresholdMid)
		return 50.0 + ratio*25.0
	default:
		return 90.0
	}
}

// donationsScore maps a donation count to a component score.
// 0 donations → 50 (neutral); higher counts indicate elevated political activity.
func donationsScore(count int) float64 {
	switch {
	case count == donationsNeutral:
		return 50.0
	case count <= donationsLow:
		return 60.0
	case count <= donationsMid:
		return 70.0
	default:
		return 80.0
	}
}
