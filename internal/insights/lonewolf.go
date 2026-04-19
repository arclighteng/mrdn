package insights

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectLoneWolf(ctx context.Context, store *db.Store) ([]Finding, error) {
	// Step 1: compute per-person median trade amount (persons with ≥5 trades)
	medianRows, err := store.DB().QueryContext(ctx, `
		SELECT person_id, GROUP_CONCAT(est_amt) AS amounts, COUNT(*) AS cnt
		FROM (
			SELECT person_id,
				COALESCE(
					CASE
						WHEN amount_range_low IS NOT NULL AND amount_range_high IS NOT NULL
							THEN (amount_range_low + amount_range_high) / 2
						WHEN amount_range_low IS NOT NULL THEN amount_range_low
						WHEN amount_range_high IS NOT NULL THEN amount_range_high
						ELSE 0
					END, 0) AS est_amt
			FROM congressional_trades
			WHERE amount_range_low IS NOT NULL OR amount_range_high IS NOT NULL
		)
		GROUP BY person_id
		HAVING cnt >= 5
	`)
	if err != nil {
		return nil, fmt.Errorf("lone wolf medians: %w", err)
	}
	defer medianRows.Close()

	type personMedian struct {
		personID int
		median   float64
	}
	var medians []personMedian
	for medianRows.Next() {
		var pid, cnt int
		var amountsCSV string
		if err := medianRows.Scan(&pid, &amountsCSV, &cnt); err != nil {
			return nil, err
		}
		amounts := parseIntCSV(amountsCSV)
		if len(amounts) < 5 {
			continue
		}
		sortInts(amounts)
		med := float64(amounts[len(amounts)/2])
		medians = append(medians, personMedian{personID: pid, median: med})
	}
	if err := medianRows.Err(); err != nil {
		return nil, err
	}

	// Step 2: for each person with a median, find their largest outlier trades
	var findings []Finding
	for _, pm := range medians {
		if pm.median <= 0 {
			continue
		}
		rows, err := store.DB().QueryContext(ctx, `
			SELECT ct.id, p.name, p.slug, ct.ticker, ct.trade_type,
				COALESCE(
					CASE
						WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
							THEN (ct.amount_range_low + ct.amount_range_high) / 2
						WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
						WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
						ELSE 0
					END, 0) AS est_amt,
				ct.traded_at
			FROM congressional_trades ct
			JOIN persons p ON p.id = ct.person_id
			WHERE ct.person_id = ?
			  AND ct.traded_at IS NOT NULL
			  AND ct.ticker IS NOT NULL AND ct.ticker != '' AND ct.ticker != '--'
			ORDER BY est_amt DESC
			LIMIT 3
		`, pm.personID)
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			var tradeID int
			var name, slug, ticker, tradeType, tradedAt string
			var estAmt int64
			if err := rows.Scan(&tradeID, &name, &slug, &ticker, &tradeType, &estAmt, &tradedAt); err != nil {
				rows.Close()
				return nil, err
			}
			ratio := float64(estAmt) / pm.median
			if ratio < 4.0 {
				continue
			}
			score := clampScore(int(50 + 25*math.Log10(ratio)))

			headline := fmt.Sprintf("Lone wolf: %s traded $%s in %s — %.0f× their typical size",
				name, formatDollars(estAmt), ticker, ratio)

			ts, _ := parseTime(tradedAt)
			findings = append(findings, Finding{
				ID:          fmt.Sprintf("lone_wolf-%s-%s", slug, ts.Format("20060102")),
				Type:        "lone_wolf",
				Headline:    headline,
				RarityScore: score,
				Timestamp:   ts,
				Data: mustJSON(map[string]any{
					"name":       name,
					"ticker":     ticker,
					"trade_type": tradeType,
					"amount":     formatDollars(estAmt),
					"ratio":      fmt.Sprintf("%.1f×", ratio),
					"date":       ts.Format("2006-01-02"),
				}),
				Link: mustJSON(map[string]string{"view": "person", "slug": slug}),
			})
		}
		rows.Close()
	}
	return findings, nil
}

func parseIntCSV(s string) []int {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		var v int
		if _, err := fmt.Sscan(strings.TrimSpace(p), &v); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func formatDollars(cents int64) string {
	if cents >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(cents)/1000000)
	}
	if cents >= 1000 {
		return fmt.Sprintf("%.0fK", float64(cents)/1000)
	}
	return fmt.Sprintf("%d", cents)
}
