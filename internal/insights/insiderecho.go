package insights

import (
	"context"
	"fmt"
	"strings"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectInsiderEcho(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		SELECT p.name AS rep_name, p.slug, ct.ticker, ct.trade_type AS rep_trade_type, ct.traded_at AS rep_date,
			it.filer_name AS insider_name, it.filer_title AS insider_title, it.trade_type AS insider_trade_type,
			it.traded_at AS insider_date,
			ABS(CAST(julianday(ct.traded_at) - julianday(it.traded_at) AS INTEGER)) AS days_apart,
			COALESCE(CASE
				WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
					THEN (ct.amount_range_low + ct.amount_range_high) / 2
				WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
				WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
				ELSE 0
			END, 0) AS rep_est_amount
		FROM congressional_trades ct
		JOIN persons p ON p.id = ct.person_id
		JOIN companies c ON c.ticker = ct.ticker
		JOIN insider_trades it ON it.company_id = c.id
		WHERE ct.traded_at IS NOT NULL AND it.traded_at IS NOT NULL
		  AND ct.ticker IS NOT NULL AND ct.ticker != '' AND ct.ticker != '--'
		  AND ABS(julianday(ct.traded_at) - julianday(it.traded_at)) <= 14
		ORDER BY days_apart ASC
		LIMIT 50
	`)
	if err != nil {
		return nil, fmt.Errorf("insider echo detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	seen := map[string]bool{}
	for rows.Next() {
		var repName, slug, ticker, repTradeType, repDate string
		var insiderName, insiderTitle, insiderTradeType, insiderDate string
		var daysApart int
		var repEstAmount int64
		if err := rows.Scan(&repName, &slug, &ticker, &repTradeType, &repDate,
			&insiderName, &insiderTitle, &insiderTradeType, &insiderDate,
			&daysApart, &repEstAmount); err != nil {
			return nil, err
		}

		// Dedupe by slug+ticker+rep_date
		repDateKey := repDate
		if len(repDateKey) > 10 {
			repDateKey = repDateKey[:10]
		}
		key := fmt.Sprintf("%s-%s-%s", slug, ticker, repDateKey)
		if seen[key] {
			continue
		}
		seen[key] = true

		// Same direction check: both buying or both selling
		repIsBuy := repTradeType == "purchase"
		insiderIsBuy := strings.Contains(strings.ToLower(insiderTradeType), "buy") ||
			strings.Contains(strings.ToLower(insiderTradeType), "purchase")
		repIsSell := strings.Contains(repTradeType, "sale")
		insiderIsSell := strings.Contains(strings.ToLower(insiderTradeType), "sale") ||
			strings.Contains(strings.ToLower(insiderTradeType), "sell")

		sameDirectionBonus := 0
		if (repIsBuy && insiderIsBuy) || (repIsSell && insiderIsSell) {
			sameDirectionBonus = 1
		}

		score := clampScore(55 + (14-daysApart)*3 + sameDirectionBonus*10)

		headline := fmt.Sprintf("Insider echo: %s and insider %s (%s) both traded %s %dd apart",
			repName, insiderName, insiderTitle, ticker, daysApart)

		ts, _ := parseTime(repDate)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("insider_echo-%s-%s-%s", slug, ticker, ts.Format("20060102")),
			Type:        "insider_echo",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"rep_name":           repName,
				"ticker":            ticker,
				"rep_trade_type":    repTradeType,
				"rep_date":          repDate,
				"rep_amount":        formatDollars(repEstAmount),
				"insider_name":      insiderName,
				"insider_title":     insiderTitle,
				"insider_trade_type": insiderTradeType,
				"insider_date":      insiderDate,
				"days_apart":        daysApart,
				"same_direction":    sameDirectionBonus == 1,
			}),
			Link: mustJSON(map[string]string{"view": "company", "ticker": ticker}),
		})
	}
	return findings, rows.Err()
}
