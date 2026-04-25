package score

import (
	"testing"
)

func TestAccountabilityScore_PerfectRecord(t *testing.T) {
	in := AccountabilityInput{
		MedianLatencyDays:   10,
		LatePct:             0,
		CommitteeTradeRatio: 0,
		RoundTripCount:      0,
		PreEventCount:       0,
		TradeCount:          50,
	}
	score := AccountabilityScore(in)
	if score > 15 {
		t.Errorf("expected score ≤ 15 for perfect record, got %.2f", score)
	}
}

func TestAccountabilityScore_WorstCase(t *testing.T) {
	in := AccountabilityInput{
		MedianLatencyDays:   120,
		LatePct:             0.8,
		CommitteeTradeRatio: 0.6,
		RoundTripCount:      15,
		PreEventCount:       10,
		TradeCount:          100,
	}
	score := AccountabilityScore(in)
	if score < 80 {
		t.Errorf("expected score ≥ 80 for worst case, got %.2f", score)
	}
}

func TestAccountabilityScore_Clamped(t *testing.T) {
	in := AccountabilityInput{
		MedianLatencyDays:   9999,
		LatePct:             99.9,
		CommitteeTradeRatio: 99.9,
		RoundTripCount:      9999,
		PreEventCount:       9999,
		TradeCount:          10000,
	}
	score := AccountabilityScore(in)
	if score > 100 {
		t.Errorf("expected score ≤ 100 for extreme inputs, got %.2f", score)
	}
}

func TestAccountabilityScore_NoTrades(t *testing.T) {
	in := AccountabilityInput{
		MedianLatencyDays:   120,
		LatePct:             1.0,
		CommitteeTradeRatio: 1.0,
		RoundTripCount:      50,
		PreEventCount:       50,
		TradeCount:          0,
	}
	score := AccountabilityScore(in)
	if score != 0 {
		t.Errorf("expected score == 0 when TradeCount is 0, got %.2f", score)
	}
}

func TestAccountabilityScore_LatencyDominates(t *testing.T) {
	// Only latency elevated: MedianLatency=100 → raw=80, 30% weight → 24.0
	in := AccountabilityInput{
		MedianLatencyDays:   100,
		LatePct:             0,
		CommitteeTradeRatio: 0,
		RoundTripCount:      0,
		PreEventCount:       0,
		TradeCount:          50,
	}
	score := AccountabilityScore(in)
	// Expected ~24.0 (within ±2 tolerance)
	if score < 22 || score > 26 {
		t.Errorf("expected score ≈ 24 when only latency elevated (100 days), got %.2f", score)
	}
}
