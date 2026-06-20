package insights

import (
	"context"
	"fmt"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectSectorRotation(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		SELECT c.sector,
			SUM(CASE WHEN ct.trade_type = 'purchase'
				THEN COALESCE(
					CASE
						WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
							THEN (ct.amount_range_low + ct.amount_range_high) / 2
						WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
						WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
						ELSE 0
					END, 0)
				ELSE 0 END) AS buy_volume,
			SUM(CASE WHEN ct.trade_type IN ('sale_full','sale_partial')
				THEN COALESCE(
					CASE
						WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
							THEN (ct.amount_range_low + ct.amount_range_high) / 2
						WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
						WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
						ELSE 0
					END, 0)
				ELSE 0 END) AS sell_volume,
			strftime('%Y-%m', ct.traded_at) AS month
		FROM congressional_trades ct
		JOIN companies c ON c.ticker = ct.ticker
		WHERE ct.traded_at IS NOT NULL AND c.sector IS NOT NULL AND c.sector != ''
		  AND ct.traded_at >= date('now', '-4 months')
		GROUP BY c.sector, strftime('%Y-%m', ct.traded_at)
		ORDER BY month DESC, buy_volume DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("sector-rotation detector: %w", err)
	}
	defer rows.Close()

	type sectorMonth struct {
		sector    string
		buyVol    int64
		sellVol   int64
		month     string
	}
	var records []sectorMonth
	for rows.Next() {
		var sm sectorMonth
		if err := rows.Scan(&sm.sector, &sm.buyVol, &sm.sellVol, &sm.month); err != nil {
			return nil, err
		}
		records = append(records, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return nil, nil
	}

	// Determine current month and prior months
	currentMonth := time.Now().Format("2006-01")

	// Aggregate buy volumes by sector for current month and prior months
	currentBuys := map[string]int64{}
	priorBuys := map[string]int64{}
	priorMonthCount := map[string]int{} // track how many prior months each sector appears in

	for _, r := range records {
		if r.month == currentMonth {
			currentBuys[r.sector] += r.buyVol
		} else {
			priorBuys[r.sector] += r.buyVol
			priorMonthCount[r.sector]++ // each record is one month for one sector
		}
	}

	// Compute totals
	var currentTotal int64
	for _, v := range currentBuys {
		currentTotal += v
	}
	var priorTotal int64
	for _, v := range priorBuys {
		priorTotal += v
	}

	if currentTotal == 0 || priorTotal == 0 {
		return nil, nil
	}

	// Compute percentage of buys per sector
	var findings []Finding
	for sector, curVol := range currentBuys {
		curPct := float64(curVol) / float64(currentTotal) * 100
		priorVol := priorBuys[sector]
		avgPct := float64(priorVol) / float64(priorTotal) * 100

		shift := curPct - avgPct
		if shift < 10 {
			continue
		}

		score := clampScore(50 + int(shift*2))
		headline := fmt.Sprintf("Sector rotation: Congress is piling into %s — %.0f%% of buys vs %.0f%% average",
			sector, curPct, avgPct)

		findings = append(findings, Finding{
			ID:          fmt.Sprintf("sector_rotation-%s-%s", sector, currentMonth),
			Type:        "sector_rotation",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   time.Now(),
			Data: mustJSON(map[string]any{
				"sector":      sector,
				"current_pct": fmt.Sprintf("%.1f%%", curPct),
				"avg_pct":     fmt.Sprintf("%.1f%%", avgPct),
				"shift_pp":    fmt.Sprintf("%.1f", shift),
				"month":       currentMonth,
			}),
			Link: mustJSON(map[string]string{"view": "signals", "tab": "swarms"}),
		})
	}
	return findings, nil
}
