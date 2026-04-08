package db

import (
	"context"
	"fmt"
)

// LatencyRow is one row of the STOCK Act disclosure-latency leaderboard.
//
// The federal STOCK Act requires members of Congress to disclose covered
// stock transactions within 45 days of the trade. This struct exposes how
// far behind that legal deadline a given person actually files, computed
// purely from (filed_at - traded_at) on real congressional_trades rows.
type LatencyRow struct {
	PersonID    int     `json:"person_id"`
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Party       *string `json:"party,omitempty"`
	State       *string `json:"state,omitempty"`
	Trades      int     `json:"trades"`
	MedianDays  int     `json:"median_days"`
	P90Days     int     `json:"p90_days"`
	WorstDays   int     `json:"worst_days"`
	WorstTicker *string `json:"worst_ticker,omitempty"`
	LateCount   int     `json:"late_count"`     // trades disclosed > 45 days after the transaction
	LatePct     float64 `json:"late_pct"`       // late_count / trades
}

// LatencyLeaderboard returns persons ranked by how badly they violate the
// STOCK Act's 45-day disclosure window. Only persons with at least minTrades
// scoreable trades (both filed_at and traded_at populated) are included.
//
// Sorted by median latency descending, then late_pct, so the worst offenders
// surface first.
func (s *Store) LatencyLeaderboard(ctx context.Context, minTrades, limit int) ([]LatencyRow, error) {
	if minTrades < 1 {
		minTrades = 1
	}
	if limit <= 0 {
		limit = 100
	}

	const q = `
WITH scored AS (
  SELECT
    ct.person_id,
    ct.ticker,
    EXTRACT(EPOCH FROM (ct.filed_at - ct.traded_at)) / 86400.0 AS days
  FROM congressional_trades ct
  WHERE ct.person_id IS NOT NULL
    AND ct.filed_at IS NOT NULL
    AND ct.traded_at IS NOT NULL
    AND ct.filed_at >= ct.traded_at
    AND ct.traded_at >= '2000-01-01'::timestamptz
    AND ct.filed_at  <  '2100-01-01'::timestamptz
),
agg AS (
  SELECT
    person_id,
    COUNT(*)                                                  AS trades,
    PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY days)         AS median_days,
    PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY days)         AS p90_days,
    MAX(days)                                                 AS worst_days,
    SUM(CASE WHEN days > 45 THEN 1 ELSE 0 END)                AS late_count
  FROM scored
  GROUP BY person_id
),
worst AS (
  SELECT DISTINCT ON (person_id) person_id, ticker, days
  FROM scored
  ORDER BY person_id, days DESC
)
SELECT
  p.id, p.slug, p.name, p.party, p.state,
  a.trades,
  ROUND(a.median_days)::INT,
  ROUND(a.p90_days)::INT,
  ROUND(a.worst_days)::INT,
  w.ticker,
  a.late_count,
  (a.late_count::float / NULLIF(a.trades, 0))
FROM agg a
JOIN persons p ON p.id = a.person_id
LEFT JOIN worst w ON w.person_id = a.person_id
WHERE a.trades >= $1
ORDER BY a.median_days DESC, (a.late_count::float / NULLIF(a.trades, 0)) DESC, a.trades DESC
LIMIT $2
`

	rows, err := s.db.Query(ctx, q, minTrades, limit)
	if err != nil {
		return nil, fmt.Errorf("latency leaderboard query: %w", err)
	}
	defer rows.Close()

	out := make([]LatencyRow, 0)
	for rows.Next() {
		var r LatencyRow
		if err := rows.Scan(
			&r.PersonID, &r.Slug, &r.Name, &r.Party, &r.State,
			&r.Trades, &r.MedianDays, &r.P90Days, &r.WorstDays,
			&r.WorstTicker, &r.LateCount, &r.LatePct,
		); err != nil {
			return nil, fmt.Errorf("scanning latency row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating latency rows: %w", err)
	}
	return out, nil
}

// LatencySummary is the headline aggregate across the entire dataset.
type LatencySummary struct {
	TotalScoreable int     `json:"total_scoreable"`
	MedianDays     int     `json:"median_days"`
	LateCount      int     `json:"late_count"`
	LatePct        float64 `json:"late_pct"`
	WorstDays      int     `json:"worst_days"`
}

// LatencySummaryAll returns dataset-wide STOCK Act compliance stats.
func (s *Store) LatencySummaryAll(ctx context.Context) (LatencySummary, error) {
	const q = `
WITH scored AS (
  SELECT EXTRACT(EPOCH FROM (filed_at - traded_at)) / 86400.0 AS days
  FROM congressional_trades
  WHERE filed_at IS NOT NULL AND traded_at IS NOT NULL AND filed_at >= traded_at
    AND traded_at >= '2000-01-01'::timestamptz
    AND filed_at  <  '2100-01-01'::timestamptz
)
SELECT
  COUNT(*),
  COALESCE(ROUND(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY days))::INT, 0),
  COALESCE(SUM(CASE WHEN days > 45 THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN days > 45 THEN 1 ELSE 0 END)::float / NULLIF(COUNT(*), 0), 0),
  COALESCE(ROUND(MAX(days))::INT, 0)
FROM scored
`
	var s2 LatencySummary
	if err := s.db.QueryRow(ctx, q).Scan(
		&s2.TotalScoreable, &s2.MedianDays, &s2.LateCount, &s2.LatePct, &s2.WorstDays,
	); err != nil {
		return LatencySummary{}, fmt.Errorf("latency summary: %w", err)
	}
	return s2, nil
}
