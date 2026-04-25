package score

import "math"

// AccountabilityInput holds the behavioral signals used to rate a politician's
// accountability on financial disclosure and potential conflict-of-interest.
type AccountabilityInput struct {
	MedianLatencyDays   int     // STOCK Act filing delay (median days)
	LatePct             float64 // fraction of trades filed >45 days late
	CommitteeTradeRatio float64 // fraction of trades in own committee's sectors
	RoundTripCount      int     // number of short-hold round-trip trades
	PreEventCount       int     // trades within 14 days before corp events
	TradeCount          int     // total trades (used as denominator)
}

// AccountabilityScore rates a politician 0-100 on concerning behavior patterns.
// Higher scores indicate more concerning patterns. Returns 0 when TradeCount == 0.
//
// Weights:
//   - 30% filing latency  (0 at ≤20 days, 100 at 120+ days)
//   - 10% late pct        (direct mapping of fraction → 0-100)
//   - 25% committee ratio (0.5+ ratio → max score)
//   - 20% round trips     (log-scaled: 30 + 40*log2(count) if count > 0)
//   - 15% pre-event       (log-scaled: 30 + 45*log2(count) if count > 0)
func AccountabilityScore(in AccountabilityInput) float64 {
	if in.TradeCount == 0 {
		return 0
	}

	latency := clamp100(float64(in.MedianLatencyDays-20))
	late := clamp100(in.LatePct * 100)
	committee := clamp100(in.CommitteeTradeRatio / 0.5 * 100)

	var roundTrip float64
	if in.RoundTripCount > 0 {
		roundTrip = clamp100(30 + 40*math.Log2(float64(in.RoundTripCount)))
	}

	var preEvent float64
	if in.PreEventCount > 0 {
		preEvent = clamp100(30 + 45*math.Log2(float64(in.PreEventCount)))
	}

	composite := 0.30*latency +
		0.10*late +
		0.25*committee +
		0.20*roundTrip +
		0.15*preEvent

	return clamp100(composite)
}

// clamp100 clamps v to the range [0, 100].
func clamp100(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
