package insights

import (
	"context"
	"fmt"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectCopyTrader(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		WITH trade_returns AS (
			SELECT p.name, p.slug, ct.ticker, ct.trade_type, ct.traded_at,
				(SELECT md.price_cents FROM market_data md
				 JOIN companies c2 ON c2.id = md.company_id
				 WHERE c2.ticker = ct.ticker
				 AND md.recorded_at <= ct.traded_at
				 ORDER BY md.recorded_at DESC LIMIT 1) AS entry_price,
				(SELECT md.price_cents FROM market_data md
				 JOIN companies c2 ON c2.id = md.company_id
				 WHERE c2.ticker = ct.ticker
				 AND md.recorded_at >= date(ct.traded_at, '+25 days')
				 AND md.recorded_at <= date(ct.traded_at, '+35 days')
				 ORDER BY md.recorded_at ASC LIMIT 1) AS exit_price
			FROM congressional_trades ct
			JOIN persons p ON p.id = ct.person_id
			WHERE ct.traded_at IS NOT NULL AND ct.ticker IS NOT NULL AND ct.ticker != '' AND ct.ticker != '--'
			  AND ct.trade_type = 'purchase'
		)
		SELECT name, slug, COUNT(*) as trades,
			AVG(CASE WHEN entry_price > 0 AND exit_price > 0
				THEN (CAST(exit_price AS REAL) - entry_price) / entry_price * 100
				ELSE NULL END) AS avg_return_pct,
			GROUP_CONCAT(DISTINCT ticker) AS tickers
		FROM trade_returns
		WHERE entry_price IS NOT NULL AND exit_price IS NOT NULL AND entry_price > 0
		GROUP BY slug
		HAVING trades >= 3 AND avg_return_pct IS NOT NULL
		ORDER BY avg_return_pct DESC
		LIMIT 20
	`)
	if err != nil {
		return nil, fmt.Errorf("copy-trader detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var name, slug, tickers string
		var trades int
		var avgReturnPct float64
		if err := rows.Scan(&name, &slug, &trades, &avgReturnPct, &tickers); err != nil {
			return nil, err
		}

		score := clampScore(50 + int(avgReturnPct*3))

		headline := fmt.Sprintf("Copy-trader: %s's buys returned %.1f%% over 30 days (%d trades)",
			name, avgReturnPct, trades)

		// Use current time since this is an aggregate insight (no single trade date)
		ts := time.Now()
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("copy_trader-%s", slug),
			Type:        "copy_trader",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":           name,
				"slug":           slug,
				"trades":         trades,
				"avg_return_pct": fmt.Sprintf("%.1f%%", avgReturnPct),
				"tickers":        tickers,
			}),
			Link: mustJSON(map[string]string{"view": "person", "slug": slug}),
		})
	}
	return findings, rows.Err()
}
