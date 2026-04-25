package db

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
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
	LateCount   int     `json:"late_count"`  // trades disclosed > 45 days after the transaction
	LatePct     float64 `json:"late_pct"`    // late_count / trades
}

// percentile computes the p-th percentile (0.0–1.0) of a pre-sorted slice
// using linear interpolation, matching PERCENTILE_CONT behaviour.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := p * float64(len(sorted)-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
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

	// Fetch per-person aggregate stats.  All latency values are collected as a
	// JSON array so percentiles can be computed in Go (SQLite lacks
	// PERCENTILE_CONT).  The worst-ticker correlated subquery avoids
	// DISTINCT ON, which SQLite does not support.
	const q = `
WITH scored AS (
  SELECT
    ct.person_id,
    ct.ticker,
    CAST(ROUND(julianday(ct.filed_at) - julianday(ct.traded_at)) AS INTEGER) AS days
  FROM congressional_trades ct
  WHERE ct.person_id IS NOT NULL
    AND ct.filed_at  IS NOT NULL
    AND ct.traded_at IS NOT NULL
    AND ct.filed_at >= ct.traded_at
    AND ct.traded_at >= '2000-01-01'
    AND ct.filed_at  <  '2100-01-01'
),
agg AS (
  SELECT
    person_id,
    COUNT(*)                                                          AS trades,
    MAX(days)                                                         AS worst_days,
    SUM(CASE WHEN days > 45 THEN 1 ELSE 0 END)                        AS late_count,
    COALESCE('[' || GROUP_CONCAT(CAST(ROUND(days) AS INTEGER)) || ']', '[]') AS days_json
  FROM scored
  GROUP BY person_id
)
SELECT
  p.id, p.slug, p.name, p.party, p.state,
  a.trades,
  a.worst_days,
  a.late_count,
  a.days_json,
  (SELECT s2.ticker
   FROM scored s2
   WHERE s2.person_id = a.person_id
   ORDER BY s2.days DESC
   LIMIT 1)                                                           AS worst_ticker
FROM agg a
JOIN persons p ON p.id = a.person_id
WHERE a.trades >= ?
`

	rows, err := s.db.QueryContext(ctx, q, minTrades)
	if err != nil {
		return nil, fmt.Errorf("latency leaderboard query: %w", err)
	}
	defer rows.Close()

	type rawRow struct {
		r        LatencyRow
		daysJSON string
	}

	var raw []rawRow
	for rows.Next() {
		var rr rawRow
		if err := rows.Scan(
			&rr.r.PersonID, &rr.r.Slug, &rr.r.Name, &rr.r.Party, &rr.r.State,
			&rr.r.Trades, &rr.r.WorstDays, &rr.r.LateCount,
			&rr.daysJSON, &rr.r.WorstTicker,
		); err != nil {
			return nil, fmt.Errorf("scanning latency row: %w", err)
		}
		raw = append(raw, rr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating latency rows: %w", err)
	}

	out := make([]LatencyRow, 0, len(raw))
	for _, rr := range raw {
		r := rr.r

		// Parse and sort the latency days, then compute percentiles.
		var idays []int
		if err := json.Unmarshal([]byte(rr.daysJSON), &idays); err != nil {
			return nil, fmt.Errorf("parsing days JSON for person %d: %w", r.PersonID, err)
		}
		fdays := make([]float64, len(idays))
		for i, d := range idays {
			fdays[i] = float64(d)
		}
		sort.Float64s(fdays)

		r.MedianDays = int(math.Round(percentile(fdays, 0.5)))
		r.P90Days = int(math.Round(percentile(fdays, 0.9)))

		if r.Trades > 0 {
			r.LatePct = float64(r.LateCount) / float64(r.Trades)
		}

		out = append(out, r)
	}

	// Sort: median desc, then late_pct desc, then trades desc — matching the
	// original ORDER BY clause.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MedianDays != out[j].MedianDays {
			return out[i].MedianDays > out[j].MedianDays
		}
		if out[i].LatePct != out[j].LatePct {
			return out[i].LatePct > out[j].LatePct
		}
		return out[i].Trades > out[j].Trades
	})

	// Apply limit in Go (after in-process sort).
	if limit < len(out) {
		out = out[:limit]
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
	// Collect every scoreable latency value as a JSON array so the median can
	// be computed in Go (SQLite lacks PERCENTILE_CONT).
	const q = `
SELECT
  COUNT(*),
  COALESCE(SUM(CASE WHEN days > 45 THEN 1 ELSE 0 END), 0),
  COALESCE(CAST(ROUND(MAX(days)) AS INTEGER), 0),
  COALESCE('[' || GROUP_CONCAT(CAST(ROUND(days) AS INTEGER)) || ']', '[]')
FROM (
  SELECT
    CAST(ROUND(julianday(filed_at) - julianday(traded_at)) AS INTEGER) AS days
  FROM congressional_trades
  WHERE filed_at  IS NOT NULL
    AND traded_at IS NOT NULL
    AND filed_at >= traded_at
    AND traded_at >= '2000-01-01'
    AND filed_at  <  '2100-01-01'
)
`
	var (
		total     int
		lateCount int
		worstDays int
		daysJSON  string
	)
	if err := s.db.QueryRowContext(ctx, q).Scan(
		&total, &lateCount, &worstDays, &daysJSON,
	); err != nil {
		return LatencySummary{}, fmt.Errorf("latency summary: %w", err)
	}

	var idays []int
	if err := json.Unmarshal([]byte(daysJSON), &idays); err != nil {
		return LatencySummary{}, fmt.Errorf("parsing summary days JSON: %w", err)
	}
	fdays := make([]float64, len(idays))
	for i, d := range idays {
		fdays[i] = float64(d)
	}
	sort.Float64s(fdays)

	var latePct float64
	if total > 0 {
		latePct = float64(lateCount) / float64(total)
	}

	return LatencySummary{
		TotalScoreable: total,
		MedianDays:     int(math.Round(percentile(fdays, 0.5))),
		LateCount:      lateCount,
		LatePct:        latePct,
		WorstDays:      worstDays,
	}, nil
}

// AccountabilityRow holds the raw inputs for one person's accountability score.
type AccountabilityRow struct {
	PersonID            int     `json:"person_id"`
	Slug                string  `json:"slug"`
	Name                string  `json:"name"`
	Party               *string `json:"party,omitempty"`
	State               *string `json:"state,omitempty"`
	TradeCount          int     `json:"trade_count"`
	MedianLatencyDays   int     `json:"median_latency_days"`
	LatePct             float64 `json:"late_pct"`
	CommitteeTradeCount int     `json:"committee_trade_count"`
	RoundTripCount      int     `json:"round_trip_count"`
	PreEventCount       int     `json:"pre_event_count"`
}

// AccountabilityInputs returns raw accountability metrics for all persons
// with at least minTrades congressional trades.
func (s *Store) AccountabilityInputs(ctx context.Context, minTrades int) ([]AccountabilityRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH trade_counts AS (
			SELECT person_id, COUNT(*) as cnt
			FROM congressional_trades
			WHERE person_id IS NOT NULL
			GROUP BY person_id
			HAVING cnt >= ?
		),
		latency AS (
			SELECT person_id,
				CAST(julianday(filed_at) - julianday(traded_at) AS INTEGER) AS days
			FROM congressional_trades
			WHERE filed_at IS NOT NULL AND traded_at IS NOT NULL AND person_id IS NOT NULL
		),
		latency_agg AS (
			SELECT l.person_id,
				COUNT(*) as scoreable,
				SUM(CASE WHEN l.days > 45 THEN 1 ELSE 0 END) as late_count
			FROM latency l
			JOIN trade_counts tc ON tc.person_id = l.person_id
			GROUP BY l.person_id
		),
		committee_trades AS (
			SELECT ct.person_id, COUNT(*) as cnt
			FROM congressional_trades ct
			JOIN person_committees pc ON pc.person_id = ct.person_id
			JOIN companies c ON c.ticker = ct.ticker
			WHERE ct.person_id IS NOT NULL
			AND (
				(pc.committee_name LIKE '%Armed%' AND c.sector IN ('Industrials', 'Defense'))
				OR (pc.committee_name LIKE '%Banking%' AND c.sector = 'Financials')
				OR (pc.committee_name LIKE '%Energy%' AND c.sector IN ('Energy', 'Utilities'))
				OR (pc.committee_name LIKE '%Health%' AND c.sector = 'Health Care')
				OR (pc.committee_name LIKE '%Commerce%' AND c.sector IN ('Technology', 'Communication Services'))
			)
			GROUP BY ct.person_id
		),
		round_trips AS (
			SELECT buy.person_id, COUNT(*) as cnt
			FROM congressional_trades buy
			JOIN congressional_trades sell
				ON buy.person_id = sell.person_id
				AND buy.ticker = sell.ticker
				AND buy.trade_type IN ('Purchase', 'purchase')
				AND sell.trade_type IN ('Sale (Full)', 'Sale (Partial)', 'sale_full', 'sale_partial', 'Sale')
				AND julianday(sell.traded_at) - julianday(buy.traded_at) BETWEEN 1 AND 60
			WHERE buy.person_id IS NOT NULL
			GROUP BY buy.person_id
		),
		pre_events AS (
			SELECT ct.person_id, COUNT(DISTINCT ct.id) as cnt
			FROM congressional_trades ct
			JOIN companies c ON c.ticker = ct.ticker
			JOIN events e ON e.company_id = c.id
			WHERE ct.traded_at IS NOT NULL
				AND ct.person_id IS NOT NULL
				AND e.event_type IN ('sec_litigation', 'government_contract', 'sanctions', 'regulatory_action', 'tariff_action')
				AND julianday(e.occurred_at) - julianday(ct.traded_at) BETWEEN 1 AND 14
			GROUP BY ct.person_id
		)
		SELECT p.id, p.slug, p.name, p.party, p.state,
			tc.cnt,
			COALESCE(la.scoreable, 0),
			COALESCE(la.late_count, 0),
			COALESCE(cmt.cnt, 0),
			COALESCE(rt.cnt, 0),
			COALESCE(pe.cnt, 0)
		FROM persons p
		JOIN trade_counts tc ON tc.person_id = p.id
		LEFT JOIN latency_agg la ON la.person_id = p.id
		LEFT JOIN committee_trades cmt ON cmt.person_id = p.id
		LEFT JOIN round_trips rt ON rt.person_id = p.id
		LEFT JOIN pre_events pe ON pe.person_id = p.id
		ORDER BY tc.cnt DESC
	`, minTrades)
	if err != nil {
		return nil, fmt.Errorf("accountability inputs: %w", err)
	}
	defer rows.Close()

	var result []AccountabilityRow
	for rows.Next() {
		var r AccountabilityRow
		var scoreable, lateCount int
		if err := rows.Scan(&r.PersonID, &r.Slug, &r.Name, &r.Party, &r.State,
			&r.TradeCount, &scoreable, &lateCount,
			&r.CommitteeTradeCount, &r.RoundTripCount, &r.PreEventCount,
		); err != nil {
			return nil, err
		}
		if scoreable > 0 {
			r.LatePct = float64(lateCount) / float64(scoreable)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
