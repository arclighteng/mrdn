package insights

import (
	"context"
	"fmt"
	"strings"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectHearingProximity(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		SELECT p.name, p.slug, pc.committee_name, c.sector, ct.ticker, ct.trade_type,
			ct.traded_at,
			e.event_type, e.occurred_at,
			CAST(julianday(e.occurred_at) - julianday(ct.traded_at) AS INTEGER) AS days_gap,
			COALESCE(CASE
				WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
					THEN (ct.amount_range_low + ct.amount_range_high) / 2
				WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
				WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
				ELSE 0
			END, 0) AS est_amount
		FROM congressional_trades ct
		JOIN persons p ON p.id = ct.person_id
		JOIN person_committees pc ON pc.person_id = p.id
		JOIN companies c ON c.ticker = ct.ticker
		JOIN events e ON e.company_id = c.id
		WHERE ct.traded_at IS NOT NULL
		  AND c.sector IS NOT NULL
		  AND e.occurred_at IS NOT NULL
		  AND julianday(e.occurred_at) - julianday(ct.traded_at) BETWEEN 1 AND 7
		ORDER BY days_gap ASC
		LIMIT 100
	`)
	if err != nil {
		return nil, fmt.Errorf("hearing proximity detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	seen := map[string]bool{}
	for rows.Next() {
		var name, slug, committee, sector, ticker, tradeType, tradedAt string
		var eventType, eventOccurred string
		var daysGap int
		var estAmount int64
		if err := rows.Scan(&name, &slug, &committee, &sector, &ticker, &tradeType,
			&tradedAt, &eventType, &eventOccurred, &daysGap, &estAmount); err != nil {
			return nil, err
		}

		// Check if committee maps to this sector (same logic as committee.go)
		matched := false
		for cmtKey, sectors := range committeeToSectors {
			if !strings.Contains(strings.ToLower(committee), strings.ToLower(cmtKey)) {
				continue
			}
			for _, s := range sectors {
				if strings.EqualFold(sector, s) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			continue
		}

		// Dedupe by slug+ticker+traded_at
		tradedAtKey := tradedAt
		if len(tradedAtKey) > 10 {
			tradedAtKey = tradedAtKey[:10]
		}
		key := fmt.Sprintf("%s-%s-%s", slug, ticker, tradedAtKey)
		if seen[key] {
			continue
		}
		seen[key] = true

		amtFactor := 0
		if estAmount > 500000 {
			amtFactor = 10
		} else if estAmount > 100000 {
			amtFactor = 5
		}
		score := clampScore(70 + (7-daysGap)*5 + amtFactor)

		prettyEvent := prettyEventType(eventType)
		headline := fmt.Sprintf("Committee insider: %s (%s) traded %s %dd before %s",
			name, committee, ticker, daysGap, prettyEvent)

		ts, _ := parseTime(tradedAt)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("committee_insider-%s-%s-%s", slug, ticker, ts.Format("20060102")),
			Type:        "committee_insider",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":       name,
				"committee":  committee,
				"sector":     sector,
				"ticker":     ticker,
				"trade_type": tradeType,
				"amount":     formatDollars(estAmount),
				"trade_date": ts.Format("2006-01-02"),
				"event_type": eventType,
				"event_date": eventOccurred,
				"days_gap":   daysGap,
			}),
			Link: mustJSON(map[string]string{"view": "company", "ticker": ticker}),
		})
	}
	return findings, rows.Err()
}
