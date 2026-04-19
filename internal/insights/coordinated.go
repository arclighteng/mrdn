package insights

import (
	"context"
	"fmt"
	"strings"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectCoordinated(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		WITH weekly AS (
			SELECT ct.ticker,
				date(ct.traded_at, 'weekday 0', '-6 days') AS week_start,
				p.name AS rep_name,
				p.slug AS rep_slug,
				ct.trade_type,
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
			WHERE ct.traded_at IS NOT NULL
		)
		SELECT ticker, week_start,
			GROUP_CONCAT(DISTINCT rep_name) AS rep_names,
			COUNT(DISTINCT rep_name) AS rep_count,
			GROUP_CONCAT(DISTINCT trade_type) AS trade_types,
			MIN(traded_at) AS first_trade,
			MAX(traded_at) AS last_trade
		FROM weekly
		GROUP BY ticker, week_start
		HAVING COUNT(DISTINCT rep_name) >= 3
		ORDER BY rep_count DESC, week_start DESC
		LIMIT 50
	`)
	if err != nil {
		return nil, fmt.Errorf("coordinated detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var ticker, weekStart, repNamesCSV, tradeTypesCSV, firstTrade, lastTrade string
		var repCount int
		if err := rows.Scan(&ticker, &weekStart, &repNamesCSV, &repCount, &tradeTypesCSV, &firstTrade, &lastTrade); err != nil {
			return nil, err
		}

		repNames := strings.Split(repNamesCSV, ",")
		tradeTypes := strings.Split(tradeTypesCSV, ",")
		sameDirection := len(tradeTypes) == 1
		dirBonus := 0
		if sameDirection {
			dirBonus = 10
		}
		score := clampScore(40 + (repCount-3)*15 + dirBonus)

		direction := "traded"
		if sameDirection {
			if tradeTypes[0] == "sell" || tradeTypes[0] == "sale_full" || tradeTypes[0] == "sale_partial" {
				direction = "sold"
			} else {
				direction = "bought"
			}
		}

		headline := fmt.Sprintf("Coordinated: %d reps %s %s in the same week", repCount, direction, ticker)

		type dataRow struct {
			Name string `json:"name"`
			Date string `json:"date"`
		}
		var data []dataRow
		for _, n := range repNames {
			data = append(data, dataRow{Name: n})
		}

		ts, _ := parseTime(firstTrade)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("coordinated-%s-%s", ticker, strings.ReplaceAll(weekStart, "-", "")),
			Type:        "coordinated_trades",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data:        mustJSON(data),
			Link:        mustJSON(map[string]string{"view": "signals", "tab": "swarms", "search": ticker}),
		})
	}
	return findings, rows.Err()
}
