package insights

import (
	"context"
	"fmt"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectAccumulation(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		SELECT p.name, p.slug, ct.ticker,
			COUNT(*) AS purchase_count,
			MIN(ct.traded_at) AS first_buy,
			MAX(ct.traded_at) AS last_buy,
			CAST(julianday(MAX(ct.traded_at)) - julianday(MIN(ct.traded_at)) AS INTEGER) AS span_days,
			SUM(COALESCE(CASE
				WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
					THEN (ct.amount_range_low + ct.amount_range_high) / 2
				WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
				WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
				ELSE 0
			END, 0)) AS total_est,
			AVG(COALESCE(CASE
				WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
					THEN (ct.amount_range_low + ct.amount_range_high) / 2
				WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
				WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
				ELSE 0
			END, 0)) AS avg_per_trade
		FROM congressional_trades ct
		JOIN persons p ON p.id = ct.person_id
		WHERE ct.trade_type = 'purchase'
		  AND ct.traded_at IS NOT NULL
		  AND ct.ticker IS NOT NULL AND ct.ticker != '' AND ct.ticker != '--'
		GROUP BY ct.person_id, ct.ticker
		HAVING purchase_count >= 3
		  AND span_days <= 60
		  AND span_days > 0
		  AND avg_per_trade < 50000
		  AND avg_per_trade > 0
		ORDER BY purchase_count DESC, total_est DESC
		LIMIT 30
	`)
	if err != nil {
		return nil, fmt.Errorf("accumulation detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var name, slug, ticker, firstBuy, lastBuy string
		var purchaseCount, spanDays int
		var totalEst, avgPerTrade int64
		if err := rows.Scan(&name, &slug, &ticker, &purchaseCount, &firstBuy, &lastBuy, &spanDays, &totalEst, &avgPerTrade); err != nil {
			return nil, err
		}

		score := clampScore(40 + purchaseCount*10 + int(totalEst/50000))

		headline := fmt.Sprintf("Quiet accumulation: %s made %d small buys of %s over %dd — total %s",
			name, purchaseCount, ticker, spanDays, formatDollars(totalEst))

		ts, _ := parseTime(lastBuy)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("quiet_accumulation-%s-%s", slug, ticker),
			Type:        "quiet_accumulation",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":           name,
				"ticker":         ticker,
				"purchase_count": purchaseCount,
				"first_buy":      firstBuy,
				"last_buy":       lastBuy,
				"span_days":      spanDays,
				"total_est":      formatDollars(totalEst),
				"avg_per_trade":  formatDollars(avgPerTrade),
			}),
			Link: mustJSON(map[string]string{"view": "person", "slug": slug}),
		})
	}
	return findings, rows.Err()
}
