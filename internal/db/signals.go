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
// Only considers trades from the last 12 months to keep results fresh.
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

// SwarmEnrichment holds cross-referenced insider trading and market volume
// data for a single swarm (ticker + week). This answers whether a cluster
// of congressional trading coincided with corporate insider activity or
// elevated market volume — i.e. "was this a general market move, or only
// rich and connected people?"
type SwarmEnrichment struct {
	InsiderTrades int      `json:"insider_trades"`
	InsiderNames  []string `json:"insider_names"`
	InsiderBuys   int      `json:"insider_buys"`
	InsiderSells  int      `json:"insider_sells"`
	VolumeRatio   *float64 `json:"volume_ratio"`
	MarketMovePct *float64 `json:"market_move_pct"`
}

// EnrichedSwarmRow extends SwarmRow with cross-referenced insider and
// market data for the same ticker and week window.
type EnrichedSwarmRow struct {
	SwarmRow
	SwarmEnrichment
}

// GetInsiderActivityForTickerWeek returns insider trading activity for a given
// ticker during the week starting at weekStart (YYYY-MM-DD).
func (s *Store) GetInsiderActivityForTickerWeek(ctx context.Context, ticker, weekStart string) (SwarmEnrichment, error) {
	const q = `
SELECT
  it.filer_name,
  it.filer_title,
  it.trade_type
FROM insider_trades it
JOIN companies c ON c.id = it.company_id
WHERE c.ticker = ?
  AND it.traded_at >= ?
  AND it.traded_at < date(?, '+7 days')
`
	rows, err := s.db.QueryContext(ctx, q, ticker, weekStart, weekStart)
	if err != nil {
		return SwarmEnrichment{}, fmt.Errorf("insider activity query: %w", err)
	}
	defer rows.Close()

	var e SwarmEnrichment
	for rows.Next() {
		var filerName, filerTitle, tradeType *string
		if err := rows.Scan(&filerName, &filerTitle, &tradeType); err != nil {
			return SwarmEnrichment{}, fmt.Errorf("scanning insider row: %w", err)
		}
		e.InsiderTrades++

		label := ""
		if filerTitle != nil && *filerTitle != "" {
			label = *filerTitle
		}
		if filerName != nil && *filerName != "" {
			if label != "" {
				label += " " + *filerName
			} else {
				label = *filerName
			}
		}
		if label != "" {
			e.InsiderNames = append(e.InsiderNames, label)
		}

		if tradeType != nil {
			switch *tradeType {
			case "purchase", "buy":
				e.InsiderBuys++
			case "sale", "sell", "sale_full", "sale_partial":
				e.InsiderSells++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return SwarmEnrichment{}, fmt.Errorf("iterating insider rows: %w", err)
	}
	return e, nil
}

// GetVolumeRatioForTickerWeek returns the volume ratio (week avg / 90-day
// baseline avg) and the price change percentage during the given week.
// Returns nil pointers when no market data exists for the ticker.
func (s *Store) GetVolumeRatioForTickerWeek(ctx context.Context, ticker, weekStart string) (*float64, *float64, error) {
	// Baseline: average daily volume for the 90 days before the week.
	const baselineQ = `
SELECT AVG(md.volume)
FROM market_data md
JOIN companies c ON c.id = md.company_id
WHERE c.ticker = ?
  AND md.volume IS NOT NULL
  AND md.recorded_at >= date(?, '-90 days')
  AND md.recorded_at < ?
`
	var baselineAvg *float64
	if err := s.db.QueryRowContext(ctx, baselineQ, ticker, weekStart, weekStart).Scan(&baselineAvg); err != nil {
		return nil, nil, fmt.Errorf("baseline volume query: %w", err)
	}

	// Week average volume.
	const weekQ = `
SELECT AVG(md.volume)
FROM market_data md
JOIN companies c ON c.id = md.company_id
WHERE c.ticker = ?
  AND md.volume IS NOT NULL
  AND md.recorded_at >= ?
  AND md.recorded_at < date(?, '+7 days')
`
	var weekAvg *float64
	if err := s.db.QueryRowContext(ctx, weekQ, ticker, weekStart, weekStart).Scan(&weekAvg); err != nil {
		return nil, nil, fmt.Errorf("week volume query: %w", err)
	}

	var volumeRatio *float64
	if baselineAvg != nil && weekAvg != nil && *baselineAvg > 0 {
		r := *weekAvg / *baselineAvg
		volumeRatio = &r
	}

	// Price change: last price minus first price during the week.
	const priceQ = `
SELECT
  (SELECT md.price_cents FROM market_data md
   JOIN companies c ON c.id = md.company_id
   WHERE c.ticker = ? AND md.price_cents IS NOT NULL
     AND md.recorded_at >= ? AND md.recorded_at < date(?, '+7 days')
   ORDER BY md.recorded_at ASC LIMIT 1) AS first_price,
  (SELECT md.price_cents FROM market_data md
   JOIN companies c ON c.id = md.company_id
   WHERE c.ticker = ? AND md.price_cents IS NOT NULL
     AND md.recorded_at >= ? AND md.recorded_at < date(?, '+7 days')
   ORDER BY md.recorded_at DESC LIMIT 1) AS last_price
`
	var firstPrice, lastPrice *int64
	if err := s.db.QueryRowContext(ctx, priceQ,
		ticker, weekStart, weekStart,
		ticker, weekStart, weekStart,
	).Scan(&firstPrice, &lastPrice); err != nil {
		return volumeRatio, nil, nil // non-fatal
	}

	var marketMovePct *float64
	if firstPrice != nil && lastPrice != nil && *firstPrice > 0 {
		pct := float64(*lastPrice-*firstPrice) / float64(*firstPrice) * 100.0
		marketMovePct = &pct
	}

	return volumeRatio, marketMovePct, nil
}

// EnrichSwarms cross-references each swarm with insider trades and market
// volume data for the same ticker and week. Enrichment failures for
// individual swarms are logged but do not fail the batch.
func (s *Store) EnrichSwarms(ctx context.Context, swarms []SwarmRow) ([]EnrichedSwarmRow, error) {
	out := make([]EnrichedSwarmRow, len(swarms))
	for i, sw := range swarms {
		out[i].SwarmRow = sw

		insider, err := s.GetInsiderActivityForTickerWeek(ctx, sw.Ticker, sw.WeekStart)
		if err == nil {
			out[i].SwarmEnrichment = insider
		}

		volRatio, movePct, err := s.GetVolumeRatioForTickerWeek(ctx, sw.Ticker, sw.WeekStart)
		if err == nil {
			out[i].VolumeRatio = volRatio
			out[i].MarketMovePct = movePct
		}
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
    AND ct.traded_at >= '2000-01-01'
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
