package db

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// PersonProfile is the deep stat sheet for a single representative.
//
// Every field is computed directly from congressional_trades — no inference,
// no scoring, no estimation beyond the explicit "midpoint of disclosed amount
// range" rule used for dollar volume. Where the disclosure used a single
// amount instead of a range, that value is used as-is.
type PersonProfile struct {
	PersonID         int                 `json:"person_id"`
	Slug             string              `json:"slug"`
	Name             string              `json:"name"`
	Party            *string             `json:"party,omitempty"`
	State            *string             `json:"state,omitempty"`
	Tier             int                 `json:"tier"`

	Trades           int                 `json:"trades"`
	Tickers          int                 `json:"tickers"`
	Buys             int                 `json:"buys"`
	Sells            int                 `json:"sells"`

	EstVolumeUSD     int64               `json:"est_volume_usd"`     // midpoint sum
	EstBuyVolumeUSD  int64               `json:"est_buy_volume_usd"`
	EstSellVolumeUSD int64               `json:"est_sell_volume_usd"`

	FirstTrade       *time.Time          `json:"first_trade,omitempty"`
	LastTrade        *time.Time          `json:"last_trade,omitempty"`
	MedianLatencyDays int                `json:"median_latency_days"`
	LatePct          float64             `json:"late_pct"`

	TopTickers       []TickerStat        `json:"top_tickers"`
	SoloTickers      []TickerStat        `json:"solo_tickers"` // tickers only this rep traded
	BiggestTrade     *BiggestTrade       `json:"biggest_trade,omitempty"`
	MonthlyVolume    []MonthlyVolumePoint `json:"monthly_volume"`
	SwarmCount       int                 `json:"swarm_count"` // # weeks where rep participated in a ≥4-rep cluster
	PartyHistory     []PartyPeriod       `json:"party_history,omitempty"`
	NetFlowUSD       int64               `json:"net_flow_usd"` // buy_volume - sell_volume (positive = net accumulator)
	OwnerBreakdown   []OwnerSlice        `json:"owner_breakdown"`
	ConcentrationHHI float64             `json:"concentration_hhi"` // 0..1 — closer to 1 = portfolio in one ticker
	TopHoldingPct    float64             `json:"top_holding_pct"`   // share of $ volume in their #1 ticker
	Committees       []CommitteeMembership `json:"committees,omitempty"`
}

// OwnerSlice is one row of (owner_type, count, $) — self vs joint vs spouse vs dependent.
type OwnerSlice struct {
	OwnerType string `json:"owner_type"`
	Trades    int    `json:"trades"`
	Volume    int64  `json:"volume"`
}

// PartyPeriod is one (party, date range) row from party_history. ended_at nil
// means the rep is still in that party today. Sorted oldest → newest.
type PartyPeriod struct {
	Party     string  `json:"party"`
	StartedAt *string `json:"started_at,omitempty"`
	EndedAt   *string `json:"ended_at,omitempty"`
	Note      *string `json:"note,omitempty"`
}

// TickerStat is one row of a per-ticker rollup for a single rep.
type TickerStat struct {
	Ticker     string `json:"ticker"`
	Trades     int    `json:"trades"`
	EstVolume  int64  `json:"est_volume"`
	Buys       int    `json:"buys"`
	Sells      int    `json:"sells"`
}

// BiggestTrade is the single trade with the highest disclosed midpoint.
type BiggestTrade struct {
	Ticker    string    `json:"ticker"`
	TradeType string    `json:"trade_type"`
	EstAmount int64     `json:"est_amount"`
	TradedAt  time.Time `json:"traded_at"`
}

// MonthlyVolumePoint is one bucket of (month, dollars) for a sparkline.
type MonthlyVolumePoint struct {
	Month     string `json:"month"`      // "2023-04"
	Volume    int64  `json:"volume"`
	BuyVolume int64  `json:"buy_volume"`
	Trades    int    `json:"trades"`
}

// midpointExpr is the SQL expression that turns an amount range into a single
// dollar number. It uses the average of low and high when both are present,
// falls back to whichever side exists, and finally to zero. This is the only
// place we "estimate" anything — and the rule is documented in plain SQL.
const midpointExpr = `
COALESCE(
  CASE
    WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
      THEN (ct.amount_range_low + ct.amount_range_high) / 2
    WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
    WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
    ELSE 0
  END,
  0
)
`

// GetPersonProfile assembles every signal we can compute for a single rep.
//
// Implemented as multiple small queries instead of one giant CTE — it's
// roughly the same wall-clock cost (the data is small) and a thousand times
// easier to debug when one piece breaks.
func (s *Store) GetPersonProfile(ctx context.Context, slug string) (PersonProfile, error) {
	p, err := s.GetPersonBySlug(ctx, slug)
	if err != nil {
		return PersonProfile{}, err
	}
	prof := PersonProfile{
		PersonID: p.ID, Slug: p.Slug, Name: p.Name,
		Party: p.Party, State: p.State, Tier: p.Tier,
	}

	// 1. Headline counts + estimated $ volume.
	var firstTradeStr, lastTradeStr *string
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(DISTINCT ct.ticker),
			SUM(CASE WHEN ct.trade_type = 'purchase' THEN 1 ELSE 0 END),
			SUM(CASE WHEN ct.trade_type LIKE 'sale%' THEN 1 ELSE 0 END),
			COALESCE(SUM(`+midpointExpr+`), 0),
			COALESCE(SUM(CASE WHEN ct.trade_type = 'purchase' THEN `+midpointExpr+` ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN ct.trade_type LIKE 'sale%' THEN `+midpointExpr+` ELSE 0 END), 0),
			MIN(ct.traded_at),
			MAX(ct.traded_at)
		FROM congressional_trades ct
		WHERE ct.person_id = ?
	`, p.ID).Scan(
		&prof.Trades, &prof.Tickers, &prof.Buys, &prof.Sells,
		&prof.EstVolumeUSD, &prof.EstBuyVolumeUSD, &prof.EstSellVolumeUSD,
		&firstTradeStr, &lastTradeStr,
	); err != nil {
		return prof, fmt.Errorf("profile headline: %w", err)
	}
	prof.FirstTrade = scanTimePtr(firstTradeStr)
	prof.LastTrade = scanTimePtr(lastTradeStr)

	// 2. Latency stats (only for trades with both dates).
	// Fetch all latency values as a JSON array and compute percentiles in Go,
	// since SQLite does not support PERCENTILE_CONT.
	var latencyJSON *string
	var lateCount int
	var totalCount int
	_ = s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE('[' || GROUP_CONCAT(CAST(ROUND(julianday(filed_at) - julianday(traded_at)) AS INTEGER)) || ']', '[]'),
			SUM(CASE WHEN (julianday(filed_at) - julianday(traded_at)) > 45 THEN 1 ELSE 0 END),
			COUNT(*)
		FROM congressional_trades
		WHERE person_id = ? AND filed_at IS NOT NULL AND traded_at IS NOT NULL AND filed_at >= traded_at
	`, p.ID).Scan(&latencyJSON, &lateCount, &totalCount)
	if latencyJSON != nil && *latencyJSON != "" && *latencyJSON != "[]" {
		var days []float64
		if err := json.Unmarshal([]byte(*latencyJSON), &days); err == nil {
			sort.Float64s(days)
			prof.MedianLatencyDays = int(percentile(days, 0.5) + 0.5)
		}
	}
	if totalCount > 0 {
		prof.LatePct = float64(lateCount) / float64(totalCount)
	}

	// 3. Top 10 tickers by trade count.
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			ct.ticker,
			COUNT(*),
			COALESCE(SUM(`+midpointExpr+`), 0),
			SUM(CASE WHEN ct.trade_type = 'purchase' THEN 1 ELSE 0 END),
			SUM(CASE WHEN ct.trade_type LIKE 'sale%' THEN 1 ELSE 0 END)
		FROM congressional_trades ct
		WHERE ct.person_id = ? AND ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
		GROUP BY ct.ticker
		ORDER BY COUNT(*) DESC, SUM(`+midpointExpr+`) DESC
		LIMIT 10
	`, p.ID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t TickerStat
			if err := rows.Scan(&t.Ticker, &t.Trades, &t.EstVolume, &t.Buys, &t.Sells); err == nil {
				prof.TopTickers = append(prof.TopTickers, t)
			}
		}
	}

	// 4. Solo tickers — tickers ONLY this rep ever traded.
	soloRows, err := s.db.QueryContext(ctx, `
		SELECT
			ct.ticker,
			COUNT(*),
			COALESCE(SUM(`+midpointExpr+`), 0),
			SUM(CASE WHEN ct.trade_type = 'purchase' THEN 1 ELSE 0 END),
			SUM(CASE WHEN ct.trade_type LIKE 'sale%' THEN 1 ELSE 0 END)
		FROM congressional_trades ct
		WHERE ct.person_id = ?
		  AND ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
		  AND NOT EXISTS (
			SELECT 1 FROM congressional_trades ct2
			WHERE ct2.ticker = ct.ticker AND ct2.person_id <> ? AND ct2.person_id IS NOT NULL
		  )
		GROUP BY ct.ticker
		ORDER BY SUM(`+midpointExpr+`) DESC, COUNT(*) DESC
		LIMIT 10
	`, p.ID, p.ID)
	if err == nil {
		defer soloRows.Close()
		for soloRows.Next() {
			var t TickerStat
			if err := soloRows.Scan(&t.Ticker, &t.Trades, &t.EstVolume, &t.Buys, &t.Sells); err == nil {
				prof.SoloTickers = append(prof.SoloTickers, t)
			}
		}
	}

	// 5. Biggest single trade.
	var bt BiggestTrade
	var ticker, ttype *string
	var tradedAtStr string
	if err := s.db.QueryRowContext(ctx, `
		SELECT ct.ticker, ct.trade_type, `+midpointExpr+` AS est, ct.traded_at
		FROM congressional_trades ct
		WHERE ct.person_id = ? AND ct.traded_at IS NOT NULL
		ORDER BY est DESC, ct.traded_at DESC
		LIMIT 1
	`, p.ID).Scan(&ticker, &ttype, &bt.EstAmount, &tradedAtStr); err == nil && bt.EstAmount > 0 {
		if ticker != nil {
			bt.Ticker = *ticker
		}
		if ttype != nil {
			bt.TradeType = *ttype
		}
		if t, err := scanTime(tradedAtStr); err == nil {
			bt.TradedAt = t
		}
		prof.BiggestTrade = &bt
	}

	// 6. Monthly volume timeline.
	monthRows, err := s.db.QueryContext(ctx, `
		SELECT
			strftime('%Y-%m', ct.traded_at),
			COALESCE(SUM(`+midpointExpr+`), 0),
			COALESCE(SUM(CASE WHEN ct.trade_type = 'purchase' THEN `+midpointExpr+` ELSE 0 END), 0),
			COUNT(*)
		FROM congressional_trades ct
		WHERE ct.person_id = ? AND ct.traded_at IS NOT NULL
		GROUP BY 1
		ORDER BY 1 ASC
	`, p.ID)
	if err == nil {
		defer monthRows.Close()
		for monthRows.Next() {
			var m MonthlyVolumePoint
			if err := monthRows.Scan(&m.Month, &m.Volume, &m.BuyVolume, &m.Trades); err == nil {
				prof.MonthlyVolume = append(prof.MonthlyVolume, m)
			}
		}
	}

	// 7. Swarm participation — # of (ticker, week) buckets where this rep was
	// part of a ≥4-rep cluster.
	// SQLite has no date_trunc; use ISO week Monday via date(x, 'weekday 0', '-6 days').
	_ = s.db.QueryRowContext(ctx, `
		WITH clusters AS (
			SELECT ct.ticker,
			       date(ct.traded_at, 'weekday 0', '-6 days') AS wk,
			       COUNT(DISTINCT ct.person_id) AS reps
			FROM congressional_trades ct
			WHERE ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.traded_at IS NOT NULL
			GROUP BY ct.ticker, date(ct.traded_at, 'weekday 0', '-6 days')
			HAVING COUNT(DISTINCT ct.person_id) >= 4
		)
		SELECT COUNT(DISTINCT ct.ticker || '|' || date(ct.traded_at, 'weekday 0', '-6 days'))
		FROM congressional_trades ct
		JOIN clusters c ON c.ticker = ct.ticker
		  AND c.wk = date(ct.traded_at, 'weekday 0', '-6 days')
		WHERE ct.person_id = ?
	`, p.ID).Scan(&prof.SwarmCount)

	// 7b. Net flow + concentration (HHI on dollar volume per ticker).
	prof.NetFlowUSD = prof.EstBuyVolumeUSD - prof.EstSellVolumeUSD
	hhiRows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(SUM(`+midpointExpr+`), 0)
		FROM congressional_trades ct
		WHERE ct.person_id = ? AND ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
		GROUP BY ct.ticker
	`, p.ID)
	if err == nil {
		var perTicker []float64
		var total float64
		for hhiRows.Next() {
			var v int64
			if err := hhiRows.Scan(&v); err == nil {
				perTicker = append(perTicker, float64(v))
				total += float64(v)
			}
		}
		hhiRows.Close()
		if total > 0 {
			var topShare float64
			for _, v := range perTicker {
				share := v / total
				prof.ConcentrationHHI += share * share
				if share > topShare {
					topShare = share
				}
			}
			prof.TopHoldingPct = topShare
		}
	}

	// 7c. Owner-type breakdown (self / joint / spouse / dependent).
	ownerRows, err := s.db.QueryContext(ctx, `
		SELECT
		  COALESCE(NULLIF(NULLIF(ct.owner_type, ''), '--'), 'unspecified'),
		  COUNT(*),
		  COALESCE(SUM(`+midpointExpr+`), 0)
		FROM congressional_trades ct
		WHERE ct.person_id = ?
		GROUP BY 1
		ORDER BY 3 DESC
	`, p.ID)
	if err == nil {
		defer ownerRows.Close()
		for ownerRows.Next() {
			var o OwnerSlice
			if err := ownerRows.Scan(&o.OwnerType, &o.Trades, &o.Volume); err == nil {
				prof.OwnerBreakdown = append(prof.OwnerBreakdown, o)
			}
		}
	}

	// 8. Party history — only populated for known switchers; current-party-only
	// reps return zero rows and we leave PartyHistory nil.
	// Synthesize a "current" period from persons.party so the UI can show a
	// continuous timeline (prior periods from party_history + current from persons).
	phRows, err := s.db.QueryContext(ctx, `
		SELECT party, strftime('%Y-%m-%d', started_at), strftime('%Y-%m-%d', ended_at), note
		FROM party_history
		WHERE person_id = ?
		ORDER BY started_at ASC
	`, p.ID)
	if err == nil {
		defer phRows.Close()
		for phRows.Next() {
			var pp PartyPeriod
			if err := phRows.Scan(&pp.Party, &pp.StartedAt, &pp.EndedAt, &pp.Note); err == nil {
				prof.PartyHistory = append(prof.PartyHistory, pp)
			}
		}
		// If we found prior periods, append the current party as the open-ended row.
		if len(prof.PartyHistory) > 0 && p.Party != nil {
			prof.PartyHistory = append(prof.PartyHistory, PartyPeriod{Party: *p.Party})
		}
	}

	// 9. Committee memberships.
	cmRows, err := s.db.QueryContext(ctx, `
		SELECT committee_name, committee_code, start_date, end_date
		FROM person_committees
		WHERE person_id = ?
		ORDER BY start_date ASC
	`, p.ID)
	if err == nil {
		defer cmRows.Close()
		for cmRows.Next() {
			var cm CommitteeMembership
			if err := cmRows.Scan(&cm.Name, &cm.Code, &cm.StartDate, &cm.EndDate); err == nil {
				prof.Committees = append(prof.Committees, cm)
			}
		}
	}

	return prof, nil
}

// CommitteeMembership represents one committee assignment for a person.
type CommitteeMembership struct {
	Name      string  `json:"name"`
	Code      *string `json:"code,omitempty"`
	StartDate *string `json:"start_date,omitempty"`
	EndDate   *string `json:"end_date,omitempty"`
}

// FirstMoverRow is one ticker's chronological cascade: who got in first, then
// who followed and how many days behind.
type FirstMoverRow struct {
	Ticker      string             `json:"ticker"`
	TotalBuyers int                `json:"total_buyers"`
	FirstBuyer  string             `json:"first_buyer"`
	FirstParty  *string            `json:"first_party,omitempty"`
	FirstDate   time.Time          `json:"first_date"`
	Followers   []FirstMoverFollow `json:"followers"`
}

// FirstMoverFollow is one follower of a first-mover.
type FirstMoverFollow struct {
	Name     string  `json:"name"`
	Party    *string `json:"party,omitempty"`
	LagDays  int     `json:"lag_days"`
	Date     string  `json:"date"`
}

// FirstMovers returns tickers with ≥minBuyers distinct congressional buyers,
// listing the first buyer and the next followers chronologically.
func (s *Store) FirstMovers(ctx context.Context, minBuyers, limit int) ([]FirstMoverRow, error) {
	if minBuyers < 2 {
		minBuyers = 3
	}
	if limit <= 0 {
		limit = 50
	}

	// SQLite does not support DISTINCT ON; use ROW_NUMBER() to deduplicate to
	// one row per (ticker, person_id) — the earliest purchase.
	// julianday arithmetic replaces EXTRACT(EPOCH FROM ...) / 86400.
	const q = `
WITH first_buy AS (
  -- One row per (ticker, person): the first time that person bought that ticker.
  SELECT ticker, person_id, name, party, traded_at
  FROM (
    SELECT ct.ticker, ct.person_id, p.name, p.party, ct.traded_at,
           ROW_NUMBER() OVER (PARTITION BY ct.ticker, ct.person_id ORDER BY ct.traded_at ASC) AS rn
    FROM congressional_trades ct
    JOIN persons p ON p.id = ct.person_id
    WHERE ct.trade_type = 'purchase'
      AND ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'
      AND ct.traded_at IS NOT NULL
      AND ct.traded_at >= '2000-01-01'
      AND ct.traded_at <  '2100-01-01'
  ) sub
  WHERE rn = 1
),
totals AS (
  SELECT ticker, COUNT(*) AS total
  FROM first_buy
  GROUP BY ticker
  HAVING COUNT(*) >= ?
),
ranked AS (
  SELECT fb.ticker, fb.name, fb.party, fb.traded_at, t.total,
         ROW_NUMBER() OVER (PARTITION BY fb.ticker ORDER BY fb.traded_at ASC) AS rn,
         FIRST_VALUE(fb.traded_at) OVER (PARTITION BY fb.ticker ORDER BY fb.traded_at ASC) AS first_date
  FROM first_buy fb
  JOIN totals t ON t.ticker = fb.ticker
)
SELECT ticker, name, party, traded_at, total,
       julianday(traded_at) - julianday(first_date) AS lag_days
FROM ranked
WHERE rn <= 5
ORDER BY total DESC, ticker, traded_at ASC
LIMIT ?
`

	rows, err := s.db.QueryContext(ctx, q, minBuyers, limit*5)
	if err != nil {
		return nil, fmt.Errorf("first movers query: %w", err)
	}
	defer rows.Close()

	byTicker := map[string]*FirstMoverRow{}
	order := []string{}
	for rows.Next() {
		var ticker, name string
		var party *string
		var tradedAtStr string
		var total int64
		var lagDays float64
		if err := rows.Scan(&ticker, &name, &party, &tradedAtStr, &total, &lagDays); err != nil {
			return nil, fmt.Errorf("scanning first mover: %w", err)
		}
		tradedAt, err := scanTime(tradedAtStr)
		if err != nil {
			return nil, fmt.Errorf("parsing first mover traded_at %q: %w", tradedAtStr, err)
		}
		row, ok := byTicker[ticker]
		if !ok {
			row = &FirstMoverRow{
				Ticker:      ticker,
				TotalBuyers: int(total),
				FirstBuyer:  name,
				FirstParty:  party,
				FirstDate:   tradedAt,
			}
			byTicker[ticker] = row
			order = append(order, ticker)
		} else {
			row.Followers = append(row.Followers, FirstMoverFollow{
				Name: name, Party: party,
				LagDays: int(lagDays + 0.5),
				Date:    tradedAt.Format("2006-01-02"),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating first movers: %w", err)
	}

	// Sort by total buyers descending, then most recent first move.
	out := make([]FirstMoverRow, 0, len(order))
	for _, t := range order {
		out = append(out, *byTicker[t])
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			a, b := out[i], out[j]
			if b.TotalBuyers > a.TotalBuyers || (b.TotalBuyers == a.TotalBuyers && b.FirstDate.After(a.FirstDate)) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
