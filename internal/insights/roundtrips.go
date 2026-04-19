package insights

import (
	"context"
	"fmt"
	"math"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectRoundTrips(ctx context.Context, store *db.Store) ([]Finding, error) {
	// Step 1: Wide query to build per-person median hold days (365-day window, no min amount)
	allRTs, err := store.RoundTrips(ctx, 365, 0, 1000)
	if err != nil {
		return nil, fmt.Errorf("round-trip detector (wide): %w", err)
	}
	personHolds := map[int][]int{}
	for _, rt := range allRTs {
		personHolds[rt.PersonID] = append(personHolds[rt.PersonID], rt.HoldDays)
	}
	personMedianHold := map[int]float64{}
	for pid, holds := range personHolds {
		if len(holds) < 5 {
			continue
		}
		sortInts(holds)
		personMedianHold[pid] = float64(holds[len(holds)/2])
	}

	// Step 2: Narrow query for fast round-trips to score as findings
	rts, err := store.RoundTrips(ctx, 30, 10000, 100)
	if err != nil {
		return nil, fmt.Errorf("round-trip detector (narrow): %w", err)
	}

	var findings []Finding
	for _, rt := range rts {
		medianHold, ok := personMedianHold[rt.PersonID]
		if !ok || medianHold <= 0 {
			// No baseline — score purely on hold duration
			if rt.HoldDays > 14 {
				continue
			}
			score := clampScore(50 + (14-rt.HoldDays)*3)
			headline := fmt.Sprintf("Fast round-trip: %s bought then sold %s in %d days",
				rt.Name, rt.Ticker, rt.HoldDays)
			ts, _ := parseTime(rt.SellDate)
			findings = append(findings, Finding{
				ID:          fmt.Sprintf("round_trip-%s-%s-%s", rt.Slug, rt.Ticker, ts.Format("20060102")),
				Type:        "round_trip",
				Headline:    headline,
				RarityScore: score,
				Timestamp:   ts,
				Data: mustJSON(map[string]any{
					"name":      rt.Name,
					"ticker":    rt.Ticker,
					"hold_days": rt.HoldDays,
					"buy_date":  rt.BuyDate,
					"sell_date": rt.SellDate,
					"amount":    formatDollars(rt.BuyAmount),
				}),
				Link: mustJSON(map[string]string{"view": "signals", "tab": "round-trips", "search": rt.Name}),
			})
			continue
		}

		deviation := (medianHold - float64(rt.HoldDays)) / medianHold
		if deviation <= 0.3 {
			continue // not anomalous enough
		}
		amtFactor := 0
		if rt.BuyAmount > 500000 {
			amtFactor = 25
		} else if rt.BuyAmount > 100000 {
			amtFactor = 15
		}
		score := clampScore(50 + int(math.Min(25, deviation*25)) + amtFactor)

		headline := fmt.Sprintf("Fast round-trip: %s bought then sold %s in %d days (median: %.0fd)",
			rt.Name, rt.Ticker, rt.HoldDays, medianHold)
		ts, _ := parseTime(rt.SellDate)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("round_trip-%s-%s-%s", rt.Slug, rt.Ticker, ts.Format("20060102")),
			Type:        "round_trip",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":        rt.Name,
				"ticker":      rt.Ticker,
				"hold_days":   rt.HoldDays,
				"median_hold": fmt.Sprintf("%.0f", medianHold),
				"buy_date":    rt.BuyDate,
				"sell_date":   rt.SellDate,
				"amount":      formatDollars(rt.BuyAmount),
			}),
			Link: mustJSON(map[string]string{"view": "signals", "tab": "round-trips", "search": rt.Name}),
		})
	}
	return findings, nil
}
