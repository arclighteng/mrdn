package score

import (
	"context"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

// marketWindow is the lookback period used for all market sub-score inputs.
const marketWindow = 30 * 24 * time.Hour

// Price trend normalization constants.
// The trend is computed as the percentage change from the first to the last of
// the most recent priceTrendPoints data points.
const (
	// priceTrendPoints is the number of recent data points used to compute trend.
	priceTrendPoints = 5
	// priceTrendLow maps to a score of 0 (strong downtrend, −10 % change).
	priceTrendLow = -0.10
	// priceTrendHigh maps to a score of 100 (strong uptrend, +10 % change).
	priceTrendHigh = 0.10
)

// Volume anomaly normalization constants.
// The anomaly ratio is latest_volume / 30d_average_volume.
const (
	// volumeRatioLow maps to a score of 0 (half the average volume or less).
	volumeRatioLow = 0.5
	// volumeRatioNeutral maps to a score of 50 (volume matches the average).
	volumeRatioNeutral = 1.0
	// volumeRatioHigh maps to a score of 100 (double the average volume or more).
	volumeRatioHigh = 2.0
)

// SEC insider activity score thresholds (trade count within the market window).
const (
	insiderActivityNeutral = 0  // 0 trades → 50 (neutral)
	insiderActivityLow     = 2  // 1–2 trades → 60
	insiderActivityMid     = 5  // 3–5 trades → 75
	// 6+ trades → 90
)

// Sub-score component weights within the market scorer (must sum to 1.0).
const (
	marketWeightPriceTrend     = 0.30
	marketWeightVolumeAnomaly  = 0.30
	marketWeightInsiderActivity = 0.40
)

// MarketScorer computes the market sub-score (0–100) for a company using
// price trend, volume anomaly, and SEC insider trade activity signals.
type MarketScorer struct {
	store ScoreStore
}

// NewMarketScorer constructs a MarketScorer backed by the given store.
func NewMarketScorer(store ScoreStore) *MarketScorer {
	return &MarketScorer{store: store}
}

// Score returns a market sub-score in [0, 100] for the given company at now.
// Returns 50.0 (neutral) when no market data is available.
func (ms *MarketScorer) Score(ctx context.Context, companyID int, now time.Time) (float64, error) {
	since := now.Add(-marketWindow)

	mktData, err := ms.store.GetMarketDataRange(ctx, companyID, since, now)
	if err != nil {
		return 0, err
	}
	insiderTrades, err := ms.store.GetInsiderTradesRange(ctx, companyID, since, now)
	if err != nil {
		return 0, err
	}

	if len(mktData) == 0 && len(insiderTrades) == 0 {
		return 50.0, nil
	}

	priceTrend := ms.priceTrendScore(mktData)
	volumeAnomaly := ms.volumeAnomalyScore(mktData)
	insiderActivity := insiderActivityScore(len(insiderTrades))

	return clamp(
		marketWeightPriceTrend*priceTrend +
			marketWeightVolumeAnomaly*volumeAnomaly +
			marketWeightInsiderActivity*insiderActivity,
	), nil
}

// priceTrendScore maps the last priceTrendPoints price changes to [0, 100].
// Returns 50.0 if fewer than 2 data points have a non-nil price.
func (ms *MarketScorer) priceTrendScore(data []db.MarketDataRow) float64 {
	// Collect rows that carry a price, using the last priceTrendPoints of them.
	priced := make([]int64, 0, len(data))
	for _, row := range data {
		if row.PriceCents != nil {
			priced = append(priced, *row.PriceCents)
		}
	}
	if len(priced) < 2 {
		return 50.0
	}
	// Use the tail window.
	start := len(priced) - priceTrendPoints
	if start < 0 {
		start = 0
	}
	window := priced[start:]
	first := float64(window[0])
	last := float64(window[len(window)-1])

	if first == 0 {
		return 50.0
	}
	changePct := (last - first) / first

	// Linear interpolation from priceTrendLow→0 to priceTrendHigh→100.
	score := (changePct - priceTrendLow) / (priceTrendHigh - priceTrendLow) * 100.0
	return clamp(score)
}

// volumeAnomalyScore compares the most recent volume to the window average.
// Returns 50.0 if no volume data is present.
func (ms *MarketScorer) volumeAnomalyScore(data []db.MarketDataRow) float64 {
	var sum int64
	var count int
	var latest int64
	var hasLatest bool

	for _, row := range data {
		if row.Volume != nil {
			sum += *row.Volume
			count++
			latest = *row.Volume
			hasLatest = true
		}
	}
	if !hasLatest || count == 0 {
		return 50.0
	}

	avg := float64(sum) / float64(count)
	if avg == 0 {
		return 50.0
	}

	ratio := float64(latest) / avg

	// Linear interpolation: ratio 0.5 → 0, 1.0 → 50, 2.0 → 100.
	// The range spans volumeRatioLow to volumeRatioHigh (width = 1.5).
	score := (ratio - volumeRatioLow) / (volumeRatioHigh - volumeRatioLow) * 100.0
	return clamp(score)
}

// insiderActivityScore maps a trade count to a component score.
// 0 trades → 50 (neutral baseline); higher counts indicate elevated activity.
func insiderActivityScore(count int) float64 {
	switch {
	case count == insiderActivityNeutral:
		return 50.0
	case count <= insiderActivityLow:
		return 60.0
	case count <= insiderActivityMid:
		return 75.0
	default:
		return 90.0
	}
}
