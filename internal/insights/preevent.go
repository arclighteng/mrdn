package insights

import (
	"context"
	"fmt"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectPreEvent(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		SELECT ct.ticker, p.name, p.slug, ct.trade_type,
			COALESCE(
				CASE
					WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
						THEN (ct.amount_range_low + ct.amount_range_high) / 2
					WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
					WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
					ELSE 0
				END, 0) AS est_amount,
			ct.traded_at,
			e.event_type,
			e.occurred_at,
			CAST(julianday(e.occurred_at) - julianday(ct.traded_at) AS INTEGER) AS days_gap
		FROM congressional_trades ct
		JOIN companies c ON c.ticker = ct.ticker
		JOIN events e ON e.company_id = c.id
		JOIN persons p ON p.id = ct.person_id
		WHERE ct.traded_at IS NOT NULL
		  AND e.event_type IN ('sec_litigation', 'government_contract', 'sanctions', 'regulatory_action', 'tariff_action')
		  AND julianday(e.occurred_at) - julianday(ct.traded_at) BETWEEN 1 AND 14
		ORDER BY days_gap ASC
		LIMIT 50
	`)
	if err != nil {
		return nil, fmt.Errorf("pre-event detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	seen := map[string]bool{} // dedupe by ticker+trade_date
	for rows.Next() {
		var ticker, name, slug, tradeType, tradedAt, eventType, eventOccurred string
		var estAmount int64
		var daysGap int
		if err := rows.Scan(&ticker, &name, &slug, &tradeType, &estAmount, &tradedAt, &eventType, &eventOccurred, &daysGap); err != nil {
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

		prettyEvent := prettyEventType(eventType)
		headline := fmt.Sprintf("Pre-event: %s traded %s %dd before %s", name, ticker, daysGap, prettyEvent)

		ts, _ := parseTime(tradedAt)
		// Guard against short eventOccurred strings
		eventDate := eventOccurred
		if len(eventDate) > 10 {
			eventDate = eventDate[:10]
		}
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("pre_event-%s-%s", ticker, ts.Format("20060102")),
			Type:        "pre_event",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":       name,
				"ticker":     ticker,
				"trade_type": tradeType,
				"amount":     formatDollars(estAmount),
				"trade_date": ts.Format("2006-01-02"),
				"event_type": eventType,
				"event_date": eventDate,
				"days_gap":   daysGap,
			}),
			Link: mustJSON(map[string]string{"view": "company", "ticker": ticker}),
		})
	}
	return findings, rows.Err()
}

func prettyEventType(t string) string {
	switch t {
	case "government_contract":
		return "a government contract"
	case "sec_litigation":
		return "an SEC litigation"
	case "sanctions":
		return "a sanctions action"
	case "regulatory_action":
		return "a regulatory action"
	case "tariff_action":
		return "a tariff action"
	default:
		return t
	}
}
