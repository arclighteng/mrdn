package db

import (
	"context"
	"fmt"
	"strings"
)

// SwarmRow is one cluster: a ticker traded by multiple reps in a tight window.
//
// Computed by bucketing congressional_trades by (ticker, week) and surfacing
// any bucket with ≥minReps distinct persons. No prediction, no inference —
// just real same-week activity from the House Clerk PTR feed.
type SwarmRow struct {
	Ticker    string   `json:"ticker"`
	WeekStart string   `json:"week_start"`
	Reps      int      `json:"reps"`
	Trades    int      `json:"trades"`
	Buys      int      `json:"buys"`
	Sells     int      `json:"sells"`
	RCount    int      `json:"r_count"`
	DCount    int      `json:"d_count"`
	RepNames  []string `json:"rep_names"`
}

// SwarmDetector returns weekly clusters of same-ticker trading activity.
//
// minReps is the minimum number of distinct representatives required to
// surface a cluster (default 4). Results are ordered by week descending,
// then by reps descending — most recent + biggest first.
func (s *Store) SwarmDetector(ctx context.Context, minReps, limit int) ([]SwarmRow, error) {
	if minReps < 2 {
		minReps = 4
	}
	if limit <= 0 {
		limit = 100
	}

	const q = `
WITH t AS (
  SELECT
    ct.ticker,
    date(ct.traded_at, 'weekday 0', '-6 days') AS week_start,
    ct.person_id,
    ct.trade_type,
    p.name,
    p.party
  FROM congressional_trades ct
  JOIN persons p ON p.id = ct.person_id
  WHERE ct.ticker IS NOT NULL
    AND ct.ticker <> ''
    AND ct.ticker <> '--'
    AND ct.traded_at IS NOT NULL
    AND ct.traded_at >= '2000-01-01'
    AND ct.traded_at <  '2100-01-01'
)
SELECT
  ticker,
  week_start,
  COUNT(DISTINCT person_id)                                      AS reps,
  COUNT(*)                                                       AS trades,
  SUM(CASE WHEN trade_type = 'purchase' THEN 1 ELSE 0 END)       AS buys,
  SUM(CASE WHEN trade_type LIKE 'sale%' THEN 1 ELSE 0 END)       AS sells,
  COUNT(DISTINCT CASE WHEN party = 'R' THEN person_id END)       AS r_count,
  COUNT(DISTINCT CASE WHEN party = 'D' THEN person_id END)       AS d_count,
  COALESCE(GROUP_CONCAT(DISTINCT name), '')                       AS rep_names
FROM t
GROUP BY ticker, week_start
HAVING COUNT(DISTINCT person_id) >= ?
ORDER BY week_start DESC, reps DESC
LIMIT ?
`

	rows, err := s.db.QueryContext(ctx, q, minReps, limit)
	if err != nil {
		return nil, fmt.Errorf("swarm detector query: %w", err)
	}
	defer rows.Close()

	out := make([]SwarmRow, 0)
	for rows.Next() {
		var r SwarmRow
		var repNamesStr string
		if err := rows.Scan(
			&r.Ticker, &r.WeekStart, &r.Reps, &r.Trades,
			&r.Buys, &r.Sells, &r.RCount, &r.DCount, &repNamesStr,
		); err != nil {
			return nil, fmt.Errorf("scanning swarm row: %w", err)
		}
		if repNamesStr != "" {
			r.RepNames = strings.Split(repNamesStr, ",")
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating swarm rows: %w", err)
	}
	return out, nil
}

// PartisanRow describes how partisan or bipartisan a single ticker is.
//
// All counts are distinct-rep counts (so a single rep buying 10 times
// counts as one buyer). Score is signed: positive = Republican-leaning,
// negative = Democratic-leaning, zero = perfectly split.
type PartisanRow struct {
	Ticker   string  `json:"ticker"`
	RBuyers  int     `json:"r_buyers"`
	DBuyers  int     `json:"d_buyers"`
	RSellers int     `json:"r_sellers"`
	DSellers int     `json:"d_sellers"`
	Total    int     `json:"total_reps"`
	Score    float64 `json:"score"` // -1.0 (all D activity) → +1.0 (all R activity)
	Mode     string  `json:"mode"`  // "consensus" | "contrarian" | "partisan_r" | "partisan_d"
}

// PartisanTickers returns tickers grouped by how the two parties trade them.
//
// mode controls which slice is returned:
//   - "consensus":  ≥2 reps from each party, both parties on the same side (mostly buying or mostly selling)
//   - "contrarian": ≥2 reps from each party, parties on opposite sides (R buying while D selling, or vice versa)
//   - "all":        every ticker meeting the minimum total
//
// Tickers below minReps total are excluded as noise.
func (s *Store) PartisanTickers(ctx context.Context, mode string, minReps, limit int) ([]PartisanRow, error) {
	if minReps < 2 {
		minReps = 4
	}
	if limit <= 0 {
		limit = 100
	}

	const q = `
WITH t AS (
  SELECT DISTINCT
    ct.ticker,
    ct.person_id,
    p.party,
    CASE
      WHEN ct.trade_type = 'purchase' THEN 'buy'
      WHEN ct.trade_type LIKE 'sale%' THEN 'sell'
      ELSE 'other'
    END AS side
  FROM congressional_trades ct
  JOIN persons p ON p.id = ct.person_id
  WHERE ct.ticker IS NOT NULL
    AND ct.ticker <> ''
    AND ct.ticker <> '--'
    AND p.party IN ('R', 'D')
    AND ct.trade_type IS NOT NULL
)
SELECT
  ticker,
  COUNT(DISTINCT CASE WHEN party = 'R' AND side = 'buy'  THEN person_id END) AS r_buyers,
  COUNT(DISTINCT CASE WHEN party = 'D' AND side = 'buy'  THEN person_id END) AS d_buyers,
  COUNT(DISTINCT CASE WHEN party = 'R' AND side = 'sell' THEN person_id END) AS r_sellers,
  COUNT(DISTINCT CASE WHEN party = 'D' AND side = 'sell' THEN person_id END) AS d_sellers,
  COUNT(DISTINCT person_id)                                                  AS total_reps
FROM t
GROUP BY ticker
HAVING COUNT(DISTINCT person_id) >= ?
`

	rows, err := s.db.QueryContext(ctx, q, minReps)
	if err != nil {
		return nil, fmt.Errorf("partisan tickers query: %w", err)
	}
	defer rows.Close()

	all := make([]PartisanRow, 0)
	for rows.Next() {
		var r PartisanRow
		if err := rows.Scan(&r.Ticker, &r.RBuyers, &r.DBuyers, &r.RSellers, &r.DSellers, &r.Total); err != nil {
			return nil, fmt.Errorf("scanning partisan row: %w", err)
		}
		rTotal := r.RBuyers + r.RSellers
		dTotal := r.DBuyers + r.DSellers
		if rTotal+dTotal > 0 {
			r.Score = float64(rTotal-dTotal) / float64(rTotal+dTotal)
		}
		r.Mode = classifyPartisan(r)
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating partisan rows: %w", err)
	}

	// Filter + sort by mode.
	filtered := make([]PartisanRow, 0, len(all))
	for _, r := range all {
		if mode == "all" || r.Mode == mode {
			filtered = append(filtered, r)
		}
	}
	sortPartisan(filtered, mode)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

// classifyPartisan labels a ticker as consensus / contrarian / partisan_r / partisan_d
// based on which combinations of (party, side) actually had ≥2 reps.
func classifyPartisan(r PartisanRow) string {
	hasR := r.RBuyers+r.RSellers >= 2
	hasD := r.DBuyers+r.DSellers >= 2
	if hasR && hasD {
		// Both parties active. Are they on the same side?
		rBuyDom := r.RBuyers > r.RSellers
		dBuyDom := r.DBuyers > r.DSellers
		if rBuyDom == dBuyDom {
			return "consensus"
		}
		return "contrarian"
	}
	if hasR {
		return "partisan_r"
	}
	if hasD {
		return "partisan_d"
	}
	return "noise"
}

// sortPartisan orders rows so the most interesting ones surface first per mode.
func sortPartisan(rows []PartisanRow, mode string) {
	// Bubble sort fine for ≤a few hundred rows; avoids importing sort.
	// (Replace with sort.Slice if rows ever grow.)
	n := len(rows)
	score := func(r PartisanRow) float64 {
		switch mode {
		case "contrarian":
			// Bigger split = more interesting.
			return float64(r.Total) * absFloat(r.Score)
		case "consensus":
			// More reps + closer to balanced = more interesting.
			return float64(r.Total) * (1 - absFloat(r.Score))
		default:
			return float64(r.Total)
		}
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if score(rows[j]) > score(rows[i]) {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
