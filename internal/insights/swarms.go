package insights

import (
	"context"
	"fmt"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectSwarmOutliers(ctx context.Context, store *db.Store) ([]Finding, error) {
	swarms, err := store.SwarmDetector(ctx, 3, 200)
	if err != nil {
		return nil, fmt.Errorf("swarm outlier detector: %w", err)
	}

	// Aggregate by ticker: total reps across weeks + count of distinct weeks
	type agg struct {
		totalReps     int
		distinctWeeks int
		latestWeek    string
		repNames      []string
	}
	byTicker := map[string]*agg{}
	for _, s := range swarms {
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
	}

	var findings []Finding
	for ticker, a := range byTicker {
		if ticker == "" || ticker == "--" {
			continue
		}
		score := clampScore(30 + a.totalReps*5 + a.distinctWeeks*10)
		if score < 50 {
			continue
		}

		headline := fmt.Sprintf("Swarm: %d reps traded %s across %d weeks",
			a.totalReps, ticker, a.distinctWeeks)

		ts, _ := parseTime(a.latestWeek)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("swarm_outlier-%s-%s", ticker, ts.Format("20060102")),
			Type:        "swarm_outlier",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"ticker":      ticker,
				"total_reps":  a.totalReps,
				"weeks":       a.distinctWeeks,
				"latest_week": a.latestWeek,
			}),
			Link: mustJSON(map[string]string{"view": "signals", "tab": "swarms", "search": ticker}),
		})
	}
	return findings, nil
}
