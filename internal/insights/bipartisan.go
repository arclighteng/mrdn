package insights

import (
	"context"
	"fmt"
	"strings"

	"github.com/arclighteng/mrdn/internal/db"
)

func detectBipartisan(ctx context.Context, store *db.Store) ([]Finding, error) {
	rows, err := store.DB().QueryContext(ctx, `
		WITH weekly AS (
			SELECT ct.ticker,
				date(ct.traded_at, 'weekday 0', '-6 days') AS week_start,
				p.party,
				p.name,
				ct.trade_type
			FROM congressional_trades ct
			JOIN persons p ON p.id = ct.person_id
			WHERE ct.traded_at IS NOT NULL
			  AND ct.ticker IS NOT NULL AND ct.ticker != '' AND ct.ticker != '--'
			  AND p.party IN ('R', 'D')
		)
		SELECT ticker, week_start,
			COUNT(DISTINCT CASE WHEN party = 'R' THEN name END) AS r_count,
			COUNT(DISTINCT CASE WHEN party = 'D' THEN name END) AS d_count,
			COUNT(DISTINCT name) AS total_reps,
			GROUP_CONCAT(DISTINCT name) AS rep_names,
			GROUP_CONCAT(DISTINCT trade_type) AS trade_types
		FROM weekly
		GROUP BY ticker, week_start
		HAVING r_count >= 1 AND d_count >= 1 AND total_reps >= 3
		ORDER BY total_reps DESC, week_start DESC
		LIMIT 30
	`)
	if err != nil {
		return nil, fmt.Errorf("bipartisan detector: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var ticker, weekStart, repNames, tradeTypes string
		var rCount, dCount, totalReps int
		if err := rows.Scan(&ticker, &weekStart, &rCount, &dCount, &totalReps, &repNames, &tradeTypes); err != nil {
			return nil, err
		}

		// Check if all trade types are the same direction
		types := strings.Split(tradeTypes, ",")
		sameDirection := true
		if len(types) > 1 {
			for i := 1; i < len(types); i++ {
				if types[i] != types[0] {
					sameDirection = false
					break
				}
			}
		}
		sameDirectionBonus := 0
		if sameDirection {
			sameDirectionBonus = 1
		}

		score := clampScore(50 + totalReps*8 + sameDirectionBonus*15)

		action := "traded"
		if sameDirection && len(types) > 0 {
			if types[0] == "purchase" {
				action = "bought"
			} else {
				action = "sold"
			}
		}

		headline := fmt.Sprintf("Bipartisan: %dR + %dD %s %s same week",
			rCount, dCount, action, ticker)

		ts, _ := parseTime(weekStart)
		findings = append(findings, Finding{
			ID:          fmt.Sprintf("bipartisan_convergence-%s-%s", ticker, ts.Format("20060102")),
			Type:        "bipartisan_convergence",
			Headline:    headline,
			RarityScore: score,
			Timestamp:   ts,
			Data: mustJSON(map[string]any{
				"ticker":         ticker,
				"week_start":     weekStart,
				"r_count":        rCount,
				"d_count":        dCount,
				"total_reps":     totalReps,
				"rep_names":      repNames,
				"trade_types":    tradeTypes,
				"same_direction": sameDirection,
			}),
			Link: mustJSON(map[string]string{"view": "signals", "tab": "swarms", "search": ticker}),
		})
	}
	return findings, rows.Err()
}
