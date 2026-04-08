package db

import (
	"context"
	"fmt"
)

// HeatmapEntry summarises the average composite score and company count for a
// single sector. Only companies that have at least one score are included.
type HeatmapEntry struct {
	Sector       string  `json:"sector"`
	AvgMarket    float64 `json:"avg_market"`
	AvgPolicy    float64 `json:"avg_policy"`
	AvgInsider   float64 `json:"avg_insider"`
	AvgComposite float64 `json:"avg_composite"`
	CompanyCount int     `json:"company_count"`
}

// GetScoreHeatmap returns the average latest scores and company count per
// sector. Companies with no scores are excluded. Sectors are ordered by
// average composite score descending.
func (s *Store) GetScoreHeatmap(ctx context.Context) ([]HeatmapEntry, error) {
	rows, err := s.db.Query(ctx, `
		WITH latest_scores AS (
			SELECT DISTINCT ON (company_id)
				company_id, market_score, policy_score, insider_score, composite_score
			FROM scores
			ORDER BY company_id, computed_at DESC
		)
		SELECT
			c.sector,
			AVG(ls.market_score)     AS avg_market,
			AVG(ls.policy_score)     AS avg_policy,
			AVG(ls.insider_score)    AS avg_insider,
			AVG(ls.composite_score)  AS avg_composite,
			COUNT(DISTINCT c.id)     AS company_count
		FROM companies c
		JOIN latest_scores ls ON ls.company_id = c.id
		WHERE c.sector IS NOT NULL
		GROUP BY c.sector
		ORDER BY avg_composite DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("getting score heatmap: %w", err)
	}
	defer rows.Close()

	entries := make([]HeatmapEntry, 0)
	for rows.Next() {
		var e HeatmapEntry
		if err := rows.Scan(&e.Sector, &e.AvgMarket, &e.AvgPolicy, &e.AvgInsider,
			&e.AvgComposite, &e.CompanyCount); err != nil {
			return nil, fmt.Errorf("scanning heatmap entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating heatmap entries: %w", err)
	}
	return entries, nil
}
