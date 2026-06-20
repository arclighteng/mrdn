package insights

import (
	"context"
	"fmt"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectCourtTrade(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		SELECT p.name, p.slug, ct.ticker, ct.trade_type, ct.traded_at,
			cf.case_number, cf.court, cf.filing_type, cf.filed_at AS court_date,
			CAST(julianday(cf.filed_at) - julianday(ct.traded_at) AS INTEGER) AS days_gap,
			COALESCE(CASE
				WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
					THEN (ct.amount_range_low + ct.amount_range_high) / 2
				WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
				WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
				ELSE 0
			END, 0) AS est_amount
		FROM congressional_trades ct
		JOIN persons p ON p.id = ct.person_id
		JOIN companies c ON c.ticker = ct.ticker
		JOIN court_filings cf ON cf.company_id = c.id
		WHERE ct.traded_at IS NOT NULL
		  AND cf.filed_at IS NOT NULL
		  AND julianday(cf.filed_at) - julianday(ct.traded_at) BETWEEN 1 AND 14
		ORDER BY days_gap ASC
		LIMIT 50
	`)
	if err != nil {
		return nil, fmt.Errorf("court-trade detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	seen := map[string]bool{}
	for rows.Next() {
		var name, slug, ticker, tradeType, tradedAt string
		var caseNumber, court, filingType, courtDate string
		var daysGap int
		var estAmount int64
		if err := rows.Scan(&name, &slug, &ticker, &tradeType, &tradedAt,
			&caseNumber, &court, &filingType, &courtDate, &daysGap, &estAmount); err != nil {
			return nil, err
		}

		key := fmt.Sprintf("%s-%s-%s", slug, ticker, tradedAt)
		if seen[key] {
			continue
		}
		seen[key] = true

		amtFactor := 0
		if estAmount > 500000 {
			amtFactor = 5
		} else if estAmount > 100000 {
			amtFactor = 3
		}
		score := clampScore(60 + (14-daysGap)*3 + amtFactor)

		action := "bought"
		if tradeType == "sale_full" || tradeType == "sale_partial" {
			action = "sold"
		}
		headline := fmt.Sprintf("Court-trade: %s %s %s %dd before %s filing",
			name, action, ticker, daysGap, filingType)

		ts, _ := parseTime(tradedAt)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("court_trade-%s-%s-%s", slug, ticker, ts.Format("20060102")),
			Type:        "court_trade",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":        name,
				"ticker":      ticker,
				"trade_type":  tradeType,
				"trade_date":  ts.Format("2006-01-02"),
				"amount":      formatDollars(estAmount),
				"case_number": caseNumber,
				"court":       court,
				"filing_type": filingType,
				"court_date":  courtDate,
				"days_gap":    daysGap,
			}),
			Link: mustJSON(map[string]string{"view": "company", "ticker": ticker}),
		})
	}
	return findings, rows.Err()
}
