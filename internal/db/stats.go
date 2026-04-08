package db

import "context"

// EventCategoryCount is a per-category aggregate of recent events.
type EventCategoryCount struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// ActivityStats summarizes recent activity for the dashboard.
type ActivityStats struct {
	EventsLast24h    int                  `json:"events_last_24h"`
	EventsLast7d     int                  `json:"events_last_7d"`
	CompaniesScored  int                  `json:"companies_scored"`
	CompaniesTotal   int                  `json:"companies_total"`
	CategoriesLast24 []EventCategoryCount `json:"categories_last_24h"`
}

// GetActivityStats returns aggregate counts used by the dashboard activity strip.
func (s *Store) GetActivityStats(ctx context.Context) (*ActivityStats, error) {
	stats := &ActivityStats{}

	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE occurred_at >= NOW() - INTERVAL '24 hours'`).
		Scan(&stats.EventsLast24h); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE occurred_at >= NOW() - INTERVAL '7 days'`).
		Scan(&stats.EventsLast7d); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(ctx,
		`SELECT count(DISTINCT company_id) FROM scores`).
		Scan(&stats.CompaniesScored); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM companies`).
		Scan(&stats.CompaniesTotal); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx,
		`SELECT event_type, count(*) AS c
		 FROM events
		 WHERE occurred_at >= NOW() - INTERVAL '24 hours'
		 GROUP BY event_type
		 ORDER BY c DESC
		 LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var ec EventCategoryCount
		if err := rows.Scan(&ec.Category, &ec.Count); err != nil {
			return nil, err
		}
		stats.CategoriesLast24 = append(stats.CategoriesLast24, ec)
	}
	return stats, rows.Err()
}

// ActivityHeatCell is a single day-of-week × month bucket of trade filings.
// Dow: 0 = Sunday .. 6 = Saturday. Month: 1..12. Count: number of trades
// whose traded_at falls in that bucket. Pure fact — no inference.
type ActivityHeatCell struct {
	Dow   int `json:"dow"`
	Month int `json:"month"`
	Count int `json:"count"`
}

// GetActivityHeatmap returns congressional-trade counts bucketed by
// day-of-week × calendar month of traded_at (UTC).
func (s *Store) GetActivityHeatmap(ctx context.Context, days int) ([]ActivityHeatCell, error) {
	if days <= 0 || days > 3650 {
		days = 365
	}
	rows, err := s.db.Query(ctx, `
		SELECT EXTRACT(DOW   FROM traded_at)::int AS dow,
		       EXTRACT(MONTH FROM traded_at)::int AS month,
		       COUNT(*)::int
		FROM congressional_trades
		WHERE traded_at >= NOW() - make_interval(days => $1)
		  AND traded_at >= '2000-01-01'::timestamptz
		  AND traded_at <  '2100-01-01'::timestamptz
		GROUP BY dow, month
		ORDER BY dow, month
	`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ActivityHeatCell, 0, 168)
	for rows.Next() {
		var c ActivityHeatCell
		if err := rows.Scan(&c.Dow, &c.Month, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TradeDrillRow is one congressional trade flattened for drill-down display.
type TradeDrillRow struct {
	ID         int64   `json:"id"`
	TradedAt   string  `json:"traded_at"`
	FiledAt    *string `json:"filed_at,omitempty"`
	PersonSlug string  `json:"person_slug"`
	PersonName string  `json:"person_name"`
	Party      *string `json:"party,omitempty"`
	State      *string `json:"state,omitempty"`
	Ticker     *string `json:"ticker,omitempty"`
	TradeType  *string `json:"trade_type,omitempty"`
	AmountLow  *int64  `json:"amount_low,omitempty"`
	AmountHigh *int64  `json:"amount_high,omitempty"`
	AmountMid  int64   `json:"amount_mid"`
}

// TradesByDowMonth returns trades whose traded_at falls on the given
// day-of-week (0..6, Sunday=0) and calendar month (1..12). Ordered by
// traded_at desc. Used by the activity heatmap drill-down.
func (s *Store) TradesByDowMonth(ctx context.Context, dow, month, days, limit int) ([]TradeDrillRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if days <= 0 || days > 3650 {
		days = 3650
	}
	q := `
SELECT ct.id,
       to_char(ct.traded_at, 'YYYY-MM-DD'),
       to_char(ct.filed_at,  'YYYY-MM-DD'),
       p.slug, p.name, p.party, p.state,
       ct.ticker, ct.trade_type,
       ct.amount_range_low, ct.amount_range_high,
       ` + midpointExpr + `
FROM congressional_trades ct
JOIN persons p ON p.id = ct.person_id
WHERE EXTRACT(DOW   FROM ct.traded_at)::int = $1
  AND EXTRACT(MONTH FROM ct.traded_at)::int = $2
  AND ct.traded_at >= NOW() - make_interval(days => $3)
  AND ct.traded_at >= '2000-01-01'::timestamptz
  AND ct.traded_at <  '2100-01-01'::timestamptz
ORDER BY ct.traded_at DESC, ct.id DESC
LIMIT $4`
	rows, err := s.db.Query(ctx, q, dow, month, days, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TradeDrillRow, 0, limit)
	for rows.Next() {
		var r TradeDrillRow
		if err := rows.Scan(&r.ID, &r.TradedAt, &r.FiledAt, &r.PersonSlug, &r.PersonName,
			&r.Party, &r.State, &r.Ticker, &r.TradeType,
			&r.AmountLow, &r.AmountHigh, &r.AmountMid); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TradesByPersonTicker returns congressional trades for a given person slug
// and ticker, ordered newest first.
func (s *Store) TradesByPersonTicker(ctx context.Context, slug, ticker string, limit int) ([]TradeDrillRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `
SELECT ct.id,
       to_char(ct.traded_at, 'YYYY-MM-DD'),
       to_char(ct.filed_at,  'YYYY-MM-DD'),
       p.slug, p.name, p.party, p.state,
       ct.ticker, ct.trade_type,
       ct.amount_range_low, ct.amount_range_high,
       ` + midpointExpr + `
FROM congressional_trades ct
JOIN persons p ON p.id = ct.person_id
WHERE p.slug = $1
  AND ct.ticker = $2
ORDER BY ct.traded_at DESC, ct.id DESC
LIMIT $3`
	rows, err := s.db.Query(ctx, q, slug, ticker, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TradeDrillRow, 0, limit)
	for rows.Next() {
		var r TradeDrillRow
		if err := rows.Scan(&r.ID, &r.TradedAt, &r.FiledAt, &r.PersonSlug, &r.PersonName,
			&r.Party, &r.State, &r.Ticker, &r.TradeType,
			&r.AmountLow, &r.AmountHigh, &r.AmountMid); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PartySectorCell is one (party, canonical sector) bucket with a trade count
// and midpoint $ volume. Used by the redesigned sector activity heatmap.
type PartySectorCell struct {
	Party      string `json:"party"`
	Sector     string `json:"sector"`
	TradeCount int    `json:"trade_count"`
	VolumeMid  int64  `json:"volume_mid"`
}

// GetPartySectorHeatmap returns trade counts and midpoint volume for every
// (party × sector) combination. Unknown parties collapse to "?", companies
// without a sector are excluded.
func (s *Store) GetPartySectorHeatmap(ctx context.Context) ([]PartySectorCell, error) {
	rows, err := s.db.Query(ctx, `
		SELECT COALESCE(NULLIF(p.party,''), '?') AS party,
		       c.sector,
		       COUNT(*) AS trade_count,
		       COALESCE(SUM(`+midpointExpr+`), 0)::bigint AS volume_mid
		FROM congressional_trades ct
		JOIN persons   p ON p.id = ct.person_id
		JOIN companies c ON c.id = ct.company_id
		WHERE c.sector IS NOT NULL AND c.sector <> ''
		GROUP BY party, c.sector
		ORDER BY party, c.sector
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PartySectorCell, 0)
	for rows.Next() {
		var c PartySectorCell
		if err := rows.Scan(&c.Party, &c.Sector, &c.TradeCount, &c.VolumeMid); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RepMonthCell is one (rep, month) bucket with a trade count and midpoint $
// volume. Used by the redesigned rep activity heatmap.
type RepMonthCell struct {
	PersonSlug string `json:"person_slug"`
	PersonName string `json:"person_name"`
	Party      string `json:"party"`
	Month      string `json:"month"` // YYYY-MM
	TradeCount int    `json:"trade_count"`
	VolumeMid  int64  `json:"volume_mid"`
}

// GetRepMonthHeatmap returns trade counts for the top N most-active reps
// across the last 12 months. Dense matrix, includes zeros only where the rep
// actually has other trades in the window.
func (s *Store) GetRepMonthHeatmap(ctx context.Context, topN int) ([]RepMonthCell, error) {
	if topN <= 0 || topN > 50 {
		topN = 15
	}
	rows, err := s.db.Query(ctx, `
		WITH window_trades AS (
			SELECT ct.*
			FROM congressional_trades ct
			WHERE ct.traded_at >= (date_trunc('month', NOW()) - interval '11 months')
			  AND ct.traded_at <  (date_trunc('month', NOW()) + interval '1 month')
		),
		top_reps AS (
			SELECT person_id
			FROM window_trades
			WHERE person_id IS NOT NULL
			GROUP BY person_id
			ORDER BY COUNT(*) DESC
			LIMIT $1
		)
		SELECT p.slug, p.name, COALESCE(NULLIF(p.party,''), '?') AS party,
		       to_char(date_trunc('month', ct.traded_at), 'YYYY-MM') AS month,
		       COUNT(*) AS trade_count,
		       COALESCE(SUM(`+midpointExpr+`), 0)::bigint AS volume_mid
		FROM window_trades ct
		JOIN top_reps  tr ON tr.person_id = ct.person_id
		JOIN persons   p  ON p.id         = ct.person_id
		GROUP BY p.slug, p.name, p.party, month
		ORDER BY p.name, month
	`, topN)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RepMonthCell, 0)
	for rows.Next() {
		var c RepMonthCell
		if err := rows.Scan(&c.PersonSlug, &c.PersonName, &c.Party, &c.Month, &c.TradeCount, &c.VolumeMid); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TradesByPartySector returns individual trades for drill-down from the
// party×sector heatmap.
func (s *Store) TradesByPartySector(ctx context.Context, party, sector string, limit int) ([]TradeDrillRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `
SELECT ct.id,
       to_char(ct.traded_at, 'YYYY-MM-DD'),
       to_char(ct.filed_at,  'YYYY-MM-DD'),
       p.slug, p.name, p.party, p.state,
       ct.ticker, ct.trade_type,
       ct.amount_range_low, ct.amount_range_high,
       ` + midpointExpr + `
FROM congressional_trades ct
JOIN persons   p ON p.id = ct.person_id
JOIN companies c ON c.id = ct.company_id
WHERE COALESCE(NULLIF(p.party,''), '?') = $1
  AND c.sector = $2
ORDER BY ct.traded_at DESC, ct.id DESC
LIMIT $3`
	rows, err := s.db.Query(ctx, q, party, sector, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TradeDrillRow, 0, limit)
	for rows.Next() {
		var r TradeDrillRow
		if err := rows.Scan(&r.ID, &r.TradedAt, &r.FiledAt, &r.PersonSlug, &r.PersonName,
			&r.Party, &r.State, &r.Ticker, &r.TradeType,
			&r.AmountLow, &r.AmountHigh, &r.AmountMid); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TradesByPersonMonth returns individual trades for drill-down from the
// rep×month heatmap. `month` is "YYYY-MM".
func (s *Store) TradesByPersonMonth(ctx context.Context, slug, month string, limit int) ([]TradeDrillRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `
SELECT ct.id,
       to_char(ct.traded_at, 'YYYY-MM-DD'),
       to_char(ct.filed_at,  'YYYY-MM-DD'),
       p.slug, p.name, p.party, p.state,
       ct.ticker, ct.trade_type,
       ct.amount_range_low, ct.amount_range_high,
       ` + midpointExpr + `
FROM congressional_trades ct
JOIN persons p ON p.id = ct.person_id
WHERE p.slug = $1
  AND to_char(date_trunc('month', ct.traded_at), 'YYYY-MM') = $2
ORDER BY ct.traded_at DESC, ct.id DESC
LIMIT $3`
	rows, err := s.db.Query(ctx, q, slug, month, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TradeDrillRow, 0, limit)
	for rows.Next() {
		var r TradeDrillRow
		if err := rows.Scan(&r.ID, &r.TradedAt, &r.FiledAt, &r.PersonSlug, &r.PersonName,
			&r.Party, &r.State, &r.Ticker, &r.TradeType,
			&r.AmountLow, &r.AmountHigh, &r.AmountMid); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RepTickerHeatmapCell is one (rep, ticker) bucket with a trade count.
type RepTickerHeatmapCell struct {
	PersonID   int    `json:"person_id"`
	PersonSlug string `json:"person_slug"`
	PersonName string `json:"person_name"`
	Ticker     string `json:"ticker"`
	Count      int    `json:"count"`
}

// GetRepTickerHeatmap returns a dense matrix of the top N reps (by trade
// count) crossed with the top N tickers they traded (by trade count). Only
// cells where that rep actually traded that ticker are returned — callers
// should treat missing pairs as zero.
func (s *Store) GetRepTickerHeatmap(ctx context.Context, n int) ([]RepTickerHeatmapCell, error) {
	if n <= 0 || n > 100 {
		n = 25
	}
	rows, err := s.db.Query(ctx, `
		WITH top_persons AS (
			SELECT ct.person_id
			FROM congressional_trades ct
			WHERE ct.person_id IS NOT NULL AND ct.ticker IS NOT NULL AND ct.ticker <> ''
			GROUP BY ct.person_id
			ORDER BY COUNT(*) DESC
			LIMIT $1
		),
		top_tickers AS (
			SELECT ct.ticker
			FROM congressional_trades ct
			JOIN top_persons tp ON tp.person_id = ct.person_id
			WHERE ct.ticker IS NOT NULL AND ct.ticker <> ''
			GROUP BY ct.ticker
			ORDER BY COUNT(*) DESC
			LIMIT $1
		)
		SELECT ct.person_id, p.slug, p.name, ct.ticker, COUNT(*)
		FROM congressional_trades ct
		JOIN top_persons  tp ON tp.person_id = ct.person_id
		JOIN top_tickers  tt ON tt.ticker    = ct.ticker
		JOIN persons      p  ON p.id         = ct.person_id
		GROUP BY ct.person_id, p.slug, p.name, ct.ticker
		ORDER BY COUNT(*) DESC
	`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]RepTickerHeatmapCell, 0)
	for rows.Next() {
		var c RepTickerHeatmapCell
		if err := rows.Scan(&c.PersonID, &c.PersonSlug, &c.PersonName, &c.Ticker, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
