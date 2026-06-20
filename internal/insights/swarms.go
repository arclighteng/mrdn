package insights

import (
	"context"
	"fmt"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectSwarmOutliers(ctx context.Context, store *db.Store) ([]Finding, error) {
	swarms, err := store.SwarmDetector(ctx, 4, 200)
	if err != nil {
		return nil, fmt.Errorf("swarm outlier detector: %w", err)
	}

	// Enrich swarms with insider + volume data (best-effort).
	enriched, _ := store.EnrichSwarms(ctx, swarms)

	// Aggregate by ticker: total reps across weeks + count of distinct weeks
	type agg struct {
		totalReps      int
		distinctWeeks  int
		latestWeek     string
		repNames       []string
		insiderTrades  int
		insiderBuys    int
		insiderSells   int
		lowVolumeWeeks int // weeks where volume_ratio < 1.5
	}
	byTicker := map[string]*agg{}
	for _, s := range enriched {
		a, ok := byTicker[s.Ticker]
		if !ok {
			a = &agg{}
			byTicker[s.Ticker] = a
		}
		a.totalReps += s.Reps
		a.distinctWeeks++
		if a.latestWeek == "" || s.WeekStart > a.latestWeek {
			a.latestWeek = s.WeekStart
		}
		for _, n := range s.RepNames {
			a.repNames = append(a.repNames, n)
		}
		a.insiderTrades += s.InsiderTrades
		a.insiderBuys += s.InsiderBuys
		a.insiderSells += s.InsiderSells
		if s.VolumeRatio != nil && *s.VolumeRatio < 1.5 {
			a.lowVolumeWeeks++
		}
	}

	var findings []Finding
	for ticker, a := range byTicker {
		if ticker == "" || ticker == "--" {
			continue
		}
		score := clampScore(30 + a.totalReps*5 + a.distinctWeeks*10)

		// Boost: insiders also trading suggests informed-trading correlation.
		if a.insiderTrades > 0 {
			score = clampScore(score + 10)
		}

		// Boost: low volume means this was NOT a general market move —
		// only connected people were trading.
		if a.lowVolumeWeeks > 0 {
			score = clampScore(score + 5*a.lowVolumeWeeks)
		}

		if score < 50 {
			continue
		}

		headline := fmt.Sprintf("Swarm: %d reps traded %s across %d weeks",
			a.totalReps, ticker, a.distinctWeeks)

		ts, _ := parseTime(a.latestWeek)
		data := map[string]any{
			"ticker":           ticker,
			"total_reps":       a.totalReps,
			"weeks":            a.distinctWeeks,
			"latest_week":      a.latestWeek,
			"insider_trades":   a.insiderTrades,
			"insider_buys":     a.insiderBuys,
			"insider_sells":    a.insiderSells,
			"low_volume_weeks": a.lowVolumeWeeks,
		}
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("swarm_outlier-%s-%s", ticker, ts.Format("20060102")),
			Type:        "swarm_outlier",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data:        mustJSON(data),
			Link:        mustJSON(map[string]string{"view": "signals", "tab": "swarms", "search": ticker}),
		})
	}
	return findings, nil
}
