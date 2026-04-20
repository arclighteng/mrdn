package db

import (
	"context"
	"fmt"
)

// TickerLeaderRow is one row of "most-traded tickers across Congress" — every
// number derived directly from congressional_trades. No estimation beyond the
// midpoint rule on disclosed amount ranges.
type TickerLeaderRow struct {
	Ticker        string  `json:"ticker"`
	Sector        *string `json:"sector,omitempty"`
	Trades        int     `json:"trades"`
	DistinctReps  int     `json:"distinct_reps"`
	Buyers        int     `json:"buyers"`
	Sellers       int     `json:"sellers"`
	EstVolumeUSD  int64   `json:"est_volume_usd"`
	BuyVolumeUSD  int64   `json:"buy_volume_usd"`
	SellVolumeUSD int64   `json:"sell_volume_usd"`
	RBuyers       int     `json:"r_buyers"`
	DBuyers       int     `json:"d_buyers"`
	FirstTrade    *string `json:"first_trade,omitempty"`
	LastTrade     *string `json:"last_trade,omitempty"`
}

// TopTickers returns the most actively traded tickers across all of Congress.
// "Activity" is ranked by distinct rep count first, then by total trades — so a
// ticker bought by 30 reps once each beats a ticker bought by one rep 100 times.
func (s *Store) TopTickers(ctx context.Context, limit int) ([]TickerLeaderRow, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `
SELECT
  ct.ticker,
  MAX(c.sector),
  COUNT(*),
  COUNT(DISTINCT ct.person_id),
  COUNT(DISTINCT CASE WHEN ct.trade_type = 'purchase' THEN ct.person_id END),
  COUNT(DISTINCT CASE WHEN ct.trade_type LIKE 'sale%' THEN ct.person_id END),
  COALESCE(SUM(` + midpointExpr + `), 0),
  COALESCE(SUM(CASE WHEN ct.trade_type = 'purchase' THEN ` + midpointExpr + ` ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN ct.trade_type LIKE 'sale%' THEN ` + midpointExpr + ` ELSE 0 END), 0),
  COUNT(DISTINCT CASE WHEN ct.trade_type = 'purchase' AND p.party = 'R' THEN ct.person_id END),
  COUNT(DISTINCT CASE WHEN ct.trade_type = 'purchase' AND p.party = 'D' THEN ct.person_id END),
  strftime('%Y-%m-%d', MIN(ct.traded_at)),
  strftime('%Y-%m-%d', MAX(ct.traded_at))
FROM congressional_trades ct
LEFT JOIN persons p ON p.id = ct.person_id
LEFT JOIN companies c ON c.id = ct.company_id
WHERE ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
  AND ct.traded_at >= '2000-01-01'
  AND ct.traded_at <  '2100-01-01'
GROUP BY ct.ticker
ORDER BY COUNT(DISTINCT ct.person_id) DESC, COUNT(*) DESC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("top tickers: %w", err)
	}
	defer rows.Close()
	out := make([]TickerLeaderRow, 0)
	for rows.Next() {
		var r TickerLeaderRow
		if err := rows.Scan(&r.Ticker, &r.Sector, &r.Trades, &r.DistinctReps,
			&r.Buyers, &r.Sellers,
			&r.EstVolumeUSD, &r.BuyVolumeUSD, &r.SellVolumeUSD,
			&r.RBuyers, &r.DBuyers, &r.FirstTrade, &r.LastTrade,
		); err != nil {
			return nil, fmt.Errorf("scan top ticker: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CoTraderRow is one other rep who traded the same ticker within a short
// window of a target rep. "Shared" is a count of distinct tickers both touched
// within WindowDays of each other. Nothing inferred — pure time proximity.
type CoTraderRow struct {
	PersonID      int     `json:"person_id"`
	Slug          string  `json:"slug"`
	Name          string  `json:"name"`
	Party         *string `json:"party,omitempty"`
	State         *string `json:"state,omitempty"`
	SharedTickers int     `json:"shared_tickers"`
	Overlaps      int     `json:"overlaps"`
	SampleTicker  string  `json:"sample_ticker"`
}

// CoTraders finds reps whose trades cluster in time with the given person's
// trades on the same ticker. A pair is an "overlap" when both reps traded the
// same ticker within windowDays of each other.
func (s *Store) CoTraders(ctx context.Context, slug string, windowDays, limit int) ([]CoTraderRow, error) {
	if windowDays <= 0 {
		windowDays = 14
	}
	if limit <= 0 {
		limit = 25
	}
	q := `
WITH target AS (
  SELECT ct.ticker, ct.traded_at, ct.person_id
  FROM congressional_trades ct
  JOIN persons p ON p.id = ct.person_id
  WHERE p.slug = ?
    AND ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
    AND ct.traded_at >= '2000-01-01'
    AND ct.traded_at <  '2100-01-01'
),
pairs AS (
  SELECT o.person_id AS other_id, t.ticker,
         MIN(ABS(julianday(o.traded_at) - julianday(t.traded_at))) AS day_gap
  FROM target t
  JOIN congressional_trades o
    ON o.ticker = t.ticker
   AND o.person_id <> t.person_id
   AND ABS(julianday(o.traded_at) - julianday(t.traded_at)) <= ?
  WHERE o.traded_at >= '2000-01-01'
    AND o.traded_at <  '2100-01-01'
  GROUP BY o.person_id, t.ticker
)
SELECT p.id, p.slug, p.name, p.party, p.state,
       COUNT(DISTINCT pairs.ticker) AS shared,
       COUNT(*) AS overlap_n,
       MIN(pairs.ticker) AS sample
FROM pairs
JOIN persons p ON p.id = pairs.other_id
GROUP BY p.id, p.slug, p.name, p.party, p.state
ORDER BY shared DESC, overlap_n DESC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, slug, windowDays, limit)
	if err != nil {
		return nil, fmt.Errorf("co-traders: %w", err)
	}
	defer rows.Close()
	out := make([]CoTraderRow, 0)
	for rows.Next() {
		var r CoTraderRow
		if err := rows.Scan(&r.PersonID, &r.Slug, &r.Name, &r.Party, &r.State,
			&r.SharedTickers, &r.Overlaps, &r.SampleTicker); err == nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// TickerDetail is the per-ticker drilldown — all reps who touched it plus a
// chronological event feed.
type TickerDetail struct {
	Ticker  string           `json:"ticker"`
	Summary TickerLeaderRow  `json:"summary"`
	ByRep   []TickerByRepRow `json:"by_rep"`
	Recent  []TickerEvent    `json:"recent"`
}

// TickerByRepRow is one rep's activity on a single ticker.
type TickerByRepRow struct {
	PersonID   int     `json:"person_id"`
	Slug       string  `json:"slug"`
	Name       string  `json:"name"`
	Party      *string `json:"party,omitempty"`
	State      *string `json:"state,omitempty"`
	Trades     int     `json:"trades"`
	Buys       int     `json:"buys"`
	Sells      int     `json:"sells"`
	EstVolume  int64   `json:"est_volume"`
	FirstTrade string  `json:"first_trade"`
	LastTrade  string  `json:"last_trade"`
}

// TickerEvent is one trade row on this ticker, ready for a timeline feed.
type TickerEvent struct {
	PersonID  int     `json:"person_id"`
	Slug      string  `json:"slug"`
	Name      string  `json:"name"`
	Party     *string `json:"party,omitempty"`
	TradeType string  `json:"trade_type"`
	OwnerType *string `json:"owner_type,omitempty"`
	EstAmount int64   `json:"est_amount"`
	TradedAt  string  `json:"traded_at"`
	FiledAt   *string `json:"filed_at,omitempty"`
}

// GetTickerDetail returns a complete drilldown for a single ticker.
func (s *Store) GetTickerDetail(ctx context.Context, ticker string, recentLimit int) (TickerDetail, error) {
	if recentLimit <= 0 {
		recentLimit = 50
	}
	d := TickerDetail{Ticker: ticker}
	d.Summary.Ticker = ticker

	// Headline summary.
	err := s.db.QueryRowContext(ctx, `
SELECT
  COUNT(*),
  COUNT(DISTINCT ct.person_id),
  COUNT(DISTINCT CASE WHEN ct.trade_type = 'purchase' THEN ct.person_id END),
  COUNT(DISTINCT CASE WHEN ct.trade_type LIKE 'sale%' THEN ct.person_id END),
  COALESCE(SUM(`+midpointExpr+`), 0),
  COALESCE(SUM(CASE WHEN ct.trade_type = 'purchase' THEN `+midpointExpr+` ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN ct.trade_type LIKE 'sale%' THEN `+midpointExpr+` ELSE 0 END), 0),
  COUNT(DISTINCT CASE WHEN ct.trade_type = 'purchase' AND p.party = 'R' THEN ct.person_id END),
  COUNT(DISTINCT CASE WHEN ct.trade_type = 'purchase' AND p.party = 'D' THEN ct.person_id END),
  strftime('%Y-%m-%d', MIN(ct.traded_at)),
  strftime('%Y-%m-%d', MAX(ct.traded_at))
FROM congressional_trades ct
LEFT JOIN persons p ON p.id = ct.person_id
WHERE ct.ticker = ?
  AND ct.traded_at >= '2000-01-01'
  AND ct.traded_at <  '2100-01-01'
`, ticker).Scan(
		&d.Summary.Trades, &d.Summary.DistinctReps,
		&d.Summary.Buyers, &d.Summary.Sellers,
		&d.Summary.EstVolumeUSD, &d.Summary.BuyVolumeUSD, &d.Summary.SellVolumeUSD,
		&d.Summary.RBuyers, &d.Summary.DBuyers,
		&d.Summary.FirstTrade, &d.Summary.LastTrade,
	)
	if err != nil {
		return d, fmt.Errorf("ticker summary: %w", err)
	}

	// Per-rep rollup.
	rows, err := s.db.QueryContext(ctx, `
SELECT
  p.id, p.slug, p.name, p.party, p.state,
  COUNT(*),
  SUM(CASE WHEN ct.trade_type = 'purchase' THEN 1 ELSE 0 END),
  SUM(CASE WHEN ct.trade_type LIKE 'sale%' THEN 1 ELSE 0 END),
  COALESCE(SUM(`+midpointExpr+`), 0),
  strftime('%Y-%m-%d', MIN(ct.traded_at)),
  strftime('%Y-%m-%d', MAX(ct.traded_at))
FROM congressional_trades ct
JOIN persons p ON p.id = ct.person_id
WHERE ct.ticker = ?
  AND ct.traded_at >= '2000-01-01'
  AND ct.traded_at <  '2100-01-01'
GROUP BY p.id, p.slug, p.name, p.party, p.state
ORDER BY COALESCE(SUM(`+midpointExpr+`), 0) DESC, COUNT(*) DESC
`, ticker)
	if err != nil {
		return d, fmt.Errorf("ticker by rep: %w", err)
	}
	for rows.Next() {
		var r TickerByRepRow
		if err := rows.Scan(&r.PersonID, &r.Slug, &r.Name, &r.Party, &r.State,
			&r.Trades, &r.Buys, &r.Sells, &r.EstVolume, &r.FirstTrade, &r.LastTrade,
		); err == nil {
			d.ByRep = append(d.ByRep, r)
		}
	}
	rows.Close()

	// Recent event feed.
	evRows, err := s.db.QueryContext(ctx, `
SELECT
  p.id, p.slug, p.name, p.party,
  ct.trade_type, ct.owner_type,
  `+midpointExpr+`,
  strftime('%Y-%m-%d', ct.traded_at),
  strftime('%Y-%m-%d', ct.filed_at)
FROM congressional_trades ct
JOIN persons p ON p.id = ct.person_id
WHERE ct.ticker = ?
  AND ct.traded_at >= '2000-01-01'
  AND ct.traded_at <  '2100-01-01'
ORDER BY ct.traded_at DESC
LIMIT ?
`, ticker, recentLimit)
	if err != nil {
		return d, fmt.Errorf("ticker recent: %w", err)
	}
	defer evRows.Close()
	for evRows.Next() {
		var e TickerEvent
		if err := evRows.Scan(&e.PersonID, &e.Slug, &e.Name, &e.Party,
			&e.TradeType, &e.OwnerType, &e.EstAmount, &e.TradedAt, &e.FiledAt,
		); err == nil {
			d.Recent = append(d.Recent, e)
		}
	}
	return d, evRows.Err()
}

// RoundTripRow is one suspiciously fast buy → sell pair on the same ticker by
// the same rep. Holding period is the calendar days between the buy and the
// sell, computed from real traded_at values. Both legs include their disclosed
// midpoint dollar amount.
type RoundTripRow struct {
	PersonID   int     `json:"person_id"`
	Slug       string  `json:"slug"`
	Name       string  `json:"name"`
	Party      *string `json:"party,omitempty"`
	Ticker     string  `json:"ticker"`
	BuyDate    string  `json:"buy_date"`
	SellDate   string  `json:"sell_date"`
	HoldDays   int     `json:"hold_days"`
	BuyAmount  int64   `json:"buy_amount"`
	SellAmount int64   `json:"sell_amount"`
}

// RoundTrips finds the fastest buy→sell turnarounds in the dataset. The query
// pairs each purchase with the next sale of the same ticker by the same rep,
// then surfaces only pairs where the gap is short AND the dollar amounts are
// non-trivial. Pure data — no inference of intent.
func (s *Store) RoundTrips(ctx context.Context, maxHoldDays, minAmount, limit int) ([]RoundTripRow, error) {
	if maxHoldDays <= 0 {
		maxHoldDays = 90
	}
	if limit <= 0 {
		limit = 100
	}
	if minAmount < 0 {
		minAmount = 0
	}
	// For each purchase, find the earliest subsequent sale of the same ticker
	// by the same rep via a correlated subquery (SQLite does not support
	// JOIN LATERAL). sell_at and sell_amt are each fetched with their own
	// correlated subquery so we stay within standard SQL.
	q := `
WITH buys AS (
  SELECT ct.id, ct.person_id, ct.ticker, ct.traded_at AS buy_at,
         ` + midpointExpr + ` AS buy_amt
  FROM congressional_trades ct
  WHERE ct.trade_type = 'purchase'
    AND ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
    AND ct.traded_at >= '2000-01-01'
    AND ct.traded_at <  '2100-01-01'
),
matched AS (
  SELECT b.person_id, b.ticker, b.buy_at, b.buy_amt,
         (SELECT ct2.traded_at
          FROM congressional_trades ct2
          WHERE ct2.person_id = b.person_id
            AND ct2.ticker = b.ticker
            AND ct2.trade_type LIKE 'sale%'
            AND ct2.traded_at > b.buy_at
            AND ct2.traded_at < '2100-01-01'
          ORDER BY ct2.traded_at ASC
          LIMIT 1) AS sell_at,
         (SELECT COALESCE(
                   CASE
                     WHEN ct2.amount_range_low IS NOT NULL AND ct2.amount_range_high IS NOT NULL
                       THEN (ct2.amount_range_low + ct2.amount_range_high) / 2
                     WHEN ct2.amount_range_low IS NOT NULL THEN ct2.amount_range_low
                     WHEN ct2.amount_range_high IS NOT NULL THEN ct2.amount_range_high
                     ELSE 0
                   END, 0)
          FROM congressional_trades ct2
          WHERE ct2.person_id = b.person_id
            AND ct2.ticker = b.ticker
            AND ct2.trade_type LIKE 'sale%'
            AND ct2.traded_at > b.buy_at
            AND ct2.traded_at < '2100-01-01'
          ORDER BY ct2.traded_at ASC
          LIMIT 1) AS sell_amt
  FROM buys b
)
SELECT m.person_id, p.slug, p.name, p.party,
       m.ticker,
       strftime('%Y-%m-%d', m.buy_at),
       strftime('%Y-%m-%d', m.sell_at),
       CAST(julianday(m.sell_at) - julianday(m.buy_at) AS INTEGER),
       m.buy_amt, m.sell_amt
FROM matched m
JOIN persons p ON p.id = m.person_id
WHERE m.sell_at IS NOT NULL
  AND CAST(julianday(m.sell_at) - julianday(m.buy_at) AS INTEGER) BETWEEN 0 AND ?
  AND m.buy_amt >= ?
ORDER BY CAST(julianday(m.sell_at) - julianday(m.buy_at) AS INTEGER) ASC,
         m.buy_amt DESC
LIMIT ?
`
	rows, err := s.db.QueryContext(ctx, q, maxHoldDays, minAmount, limit)
	if err != nil {
		return nil, fmt.Errorf("round trips: %w", err)
	}
	defer rows.Close()
	out := make([]RoundTripRow, 0)
	for rows.Next() {
		var r RoundTripRow
		if err := rows.Scan(&r.PersonID, &r.Slug, &r.Name, &r.Party,
			&r.Ticker, &r.BuyDate, &r.SellDate, &r.HoldDays,
			&r.BuyAmount, &r.SellAmount,
		); err == nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}
