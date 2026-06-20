package insights

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectLateProfitable(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		SELECT p.name, p.slug, ct.ticker, ct.trade_type, ct.traded_at, ct.filed_at,
			CAST(julianday(ct.filed_at) - julianday(ct.traded_at) AS INTEGER) AS delay_days,
			COALESCE(CASE
				WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
					THEN (ct.amount_range_low + ct.amount_range_high) / 2
				WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
				WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
				ELSE 0
			END, 0) AS est_amount,
			(SELECT md.price_cents FROM market_data md
			 JOIN companies c2 ON c2.id = md.company_id
			 WHERE c2.ticker = ct.ticker
			 AND md.recorded_at <= ct.traded_at
			 ORDER BY md.recorded_at DESC LIMIT 1) AS trade_price,
			(SELECT md.price_cents FROM market_data md
			 JOIN companies c2 ON c2.id = md.company_id
			 WHERE c2.ticker = ct.ticker
			 AND md.recorded_at <= ct.filed_at
			 ORDER BY md.recorded_at DESC LIMIT 1) AS filing_price
		FROM congressional_trades ct
		JOIN persons p ON p.id = ct.person_id
		WHERE ct.traded_at IS NOT NULL AND ct.filed_at IS NOT NULL
		  AND ct.ticker IS NOT NULL AND ct.ticker != '' AND ct.ticker != '--'
		  AND CAST(julianday(ct.filed_at) - julianday(ct.traded_at) AS INTEGER) > 45
		  AND ct.trade_type = 'purchase'
		ORDER BY delay_days DESC
		LIMIT 50
	`)
	if err != nil {
		return nil, fmt.Errorf("late-profitable detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var name, slug, ticker, tradeType, tradedAt, filedAt string
		var delayDays int
		var estAmount int64
		var tradePrice, filingPrice sql.NullInt64
		if err := rows.Scan(&name, &slug, &ticker, &tradeType, &tradedAt, &filedAt,
			&delayDays, &estAmount, &tradePrice, &filingPrice); err != nil {
			return nil, err
		}

		if !tradePrice.Valid || !filingPrice.Valid || tradePrice.Int64 <= 0 {
			continue
		}

		pricePct := (float64(filingPrice.Int64) - float64(tradePrice.Int64)) / float64(tradePrice.Int64) * 100
		if pricePct <= 0 {
			continue
		}

		score := clampScore(50 + delayDays/5 + int(pricePct*2))

		headline := fmt.Sprintf("Late & profitable: %s reported %s buy %dd late — stock up %.1f%%",
			name, ticker, delayDays, pricePct)

		ts, _ := parseTime(tradedAt)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("late_profitable-%s-%s-%s", slug, ticker, ts.Format("20060102")),
			Type:        "late_profitable",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":        name,
				"ticker":      ticker,
				"trade_type":  tradeType,
				"trade_date":  ts.Format("2006-01-02"),
				"filed_date":  filedAt,
				"delay_days":  delayDays,
				"amount":      formatDollars(estAmount),
				"price_change": fmt.Sprintf("%.1f%%", pricePct),
			}),
			Link: mustJSON(map[string]string{"view": "person", "slug": slug}),
		})
	}
	return findings, rows.Err()
}
