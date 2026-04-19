package insights

import (
	"context"
	"fmt"
	"strings"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectCommittee(ctx context.Context, store *db.Store) ([]Finding, error) {
	// Build a set of sectors per committee for matching
	// committeeToSectors is defined in insights.go

	rows, err := store.DB().QueryContext(ctx, `
		SELECT p.name, p.slug, pc.committee_name, c.sector, ct.ticker, ct.trade_type,
			COALESCE(
				CASE
					WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
						THEN (ct.amount_range_low + ct.amount_range_high) / 2
					WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
					WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
					ELSE 0
				END, 0) AS est_amount,
			ct.traded_at
		FROM congressional_trades ct
		JOIN persons p ON p.id = ct.person_id
		JOIN person_committees pc ON pc.person_id = p.id
		JOIN companies c ON c.ticker = ct.ticker
		WHERE ct.traded_at IS NOT NULL
		  AND c.sector IS NOT NULL
		ORDER BY ct.traded_at DESC
		LIMIT 500
	`)
	if err != nil {
		return nil, fmt.Errorf("committee detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	seen := map[string]bool{}
	for rows.Next() {
		var name, slug, committee, sector, ticker, tradeType, tradedAt string
		var estAmount int64
		if err := rows.Scan(&name, &slug, &committee, &sector, &ticker, &tradeType, &estAmount, &tradedAt); err != nil {
			return nil, err
		}

		// Check if committee maps to this sector
		matched := false
		matchedCommittee := ""
		for cmtKey, sectors := range committeeToSectors {
			if !strings.Contains(strings.ToLower(committee), strings.ToLower(cmtKey)) {
				continue
			}
			for _, s := range sectors {
				if strings.EqualFold(sector, s) {
					matched = true
					matchedCommittee = cmtKey
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

		// Guard against short tradedAt strings
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
			amtFactor = 20
		} else if estAmount > 100000 {
			amtFactor = 10
		}
		score := clampScore(50 + amtFactor + 20) // base 70 for committee match + amount bonus

		headline := fmt.Sprintf("Committee overlap: %s (%s) traded %s (%s)",
			name, matchedCommittee, ticker, sector)

		ts, _ := parseTime(tradedAt)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("committee_relevant-%s-%s-%s", slug, ticker, ts.Format("20060102")),
			Type:        "committee_relevant",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"name":      name,
				"committee": matchedCommittee,
				"ticker":    ticker,
				"sector":    sector,
				"amount":    formatDollars(estAmount),
				"date":      ts.Format("2006-01-02"),
			}),
			Link: mustJSON(map[string]string{"view": "person", "slug": slug}),
		})
	}
	return findings, rows.Err()
}
