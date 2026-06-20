package db

import (
	"context"
	"fmt"
	"time"
)

// GetMarketDataRange returns market_data rows for a company within [since, until].
// Rows are ordered by recorded_at ascending.
func (s *Store) GetMarketDataRange(ctx context.Context, companyID int, since, until time.Time) ([]MarketDataRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, company_id, source, data_type, price_cents, volume, change_pct, recorded_at
		FROM market_data
		WHERE company_id = ?
		  AND recorded_at >= ?
		  AND recorded_at <= ?
		ORDER BY recorded_at
	`, companyID, formatTime(since), formatTime(until))
	if err != nil {
		return nil, fmt.Errorf("querying market data range for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]MarketDataRow, 0)
	for rows.Next() {
		var m MarketDataRow
		var recordedAt string
		if err := rows.Scan(&m.ID, &m.CompanyID, &m.Source, &m.DataType,
			&m.PriceCents, &m.Volume, &m.ChangePct, &recordedAt); err != nil {
			return nil, fmt.Errorf("scanning market data row: %w", err)
		}
		m.RecordedAt, _ = scanTime(recordedAt)
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating market data rows: %w", err)
	}
	return result, nil
}

// GetInsiderTradesRange returns insider_trades rows for a company within [since, until].
// Rows with a NULL filed_at are excluded. Results are ordered by filed_at ascending.
func (s *Store) GetInsiderTradesRange(ctx context.Context, companyID int, since, until time.Time) ([]InsiderTrade, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, company_id, filer_name, filer_title, trade_type, shares, price_cents, filed_at, traded_at
		FROM insider_trades
		WHERE company_id = ?
		  AND filed_at IS NOT NULL
		  AND filed_at >= ?
		  AND filed_at <= ?
		ORDER BY filed_at
	`, companyID, formatTime(since), formatTime(until))
	if err != nil {
		return nil, fmt.Errorf("querying insider trades range for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]InsiderTrade, 0)
	for rows.Next() {
		var t InsiderTrade
		var filedAt, tradedAt *string
		if err := rows.Scan(&t.ID, &t.EventID, &t.CompanyID, &t.FilerName, &t.FilerTitle,
			&t.TradeType, &t.Shares, &t.PriceCents, &filedAt, &tradedAt); err != nil {
			return nil, fmt.Errorf("scanning insider trade row: %w", err)
		}
		t.FiledAt = scanTimePtr(filedAt)
		t.TradedAt = scanTimePtr(tradedAt)
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating insider trade rows: %w", err)
	}
	return result, nil
}

// GetCongressionalTradesForCompany returns congressional_trades rows for a company
// within [since, until]. Matches by company_id. Results ordered by traded_at ascending.
func (s *Store) GetCongressionalTradesForCompany(ctx context.Context, companyID int, since, until time.Time) ([]CongressionalTrade, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, person_id, company_id, owner_type, ticker, trade_type,
		       amount_range_low, amount_range_high, filed_at, traded_at
		FROM congressional_trades
		WHERE company_id = ?
		  AND traded_at IS NOT NULL
		  AND traded_at >= ?
		  AND traded_at <= ?
		ORDER BY traded_at
	`, companyID, formatTime(since), formatTime(until))
	if err != nil {
		return nil, fmt.Errorf("querying congressional trades for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]CongressionalTrade, 0)
	for rows.Next() {
		var t CongressionalTrade
		var filedAt, tradedAt *string
		if err := rows.Scan(&t.ID, &t.EventID, &t.PersonID, &t.CompanyID, &t.OwnerType,
			&t.Ticker, &t.TradeType, &t.AmountRangeLow, &t.AmountRangeHigh,
			&filedAt, &tradedAt); err != nil {
			return nil, fmt.Errorf("scanning congressional trade row: %w", err)
		}
		t.FiledAt = scanTimePtr(filedAt)
		t.TradedAt = scanTimePtr(tradedAt)
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating congressional trade rows: %w", err)
	}
	return result, nil
}

// GetSanctionsRange returns sanctions rows for a company within [since, until].
// Rows with a NULL added_at are excluded. Results are ordered by added_at ascending.
func (s *Store) GetSanctionsRange(ctx context.Context, companyID int, since, until time.Time) ([]Sanction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, company_id, entity_name, entity_type, program, country, added_at
		FROM sanctions
		WHERE company_id = ?
		  AND added_at IS NOT NULL
		  AND added_at >= ?
		  AND added_at <= ?
		ORDER BY added_at
	`, companyID, formatTime(since), formatTime(until))
	if err != nil {
		return nil, fmt.Errorf("querying sanctions range for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]Sanction, 0)
	for rows.Next() {
		var sn Sanction
		var addedAt *string
		if err := rows.Scan(&sn.ID, &sn.EventID, &sn.CompanyID, &sn.EntityName,
			&sn.EntityType, &sn.Program, &sn.Country, &addedAt); err != nil {
			return nil, fmt.Errorf("scanning sanction row: %w", err)
		}
		sn.AddedAt = scanTimePtr(addedAt)
		result = append(result, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating sanction rows: %w", err)
	}
	return result, nil
}

// GetContractsRange returns contracts rows for a company within [since, until].
// Rows with a NULL awarded_at are excluded. Results are ordered by awarded_at ascending.
func (s *Store) GetContractsRange(ctx context.Context, companyID int, since, until time.Time) ([]Contract, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, company_id, agency, amount_cents, action_type, description, awarded_at
		FROM contracts
		WHERE company_id = ?
		  AND awarded_at IS NOT NULL
		  AND awarded_at >= ?
		  AND awarded_at <= ?
		ORDER BY awarded_at
	`, companyID, formatTime(since), formatTime(until))
	if err != nil {
		return nil, fmt.Errorf("querying contracts range for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]Contract, 0)
	for rows.Next() {
		var c Contract
		var awardedAt *string
		if err := rows.Scan(&c.ID, &c.EventID, &c.CompanyID, &c.Agency,
			&c.AmountCents, &c.ActionType, &c.Description, &awardedAt); err != nil {
			return nil, fmt.Errorf("scanning contract row: %w", err)
		}
		c.AwardedAt = scanTimePtr(awardedAt)
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating contract rows: %w", err)
	}
	return result, nil
}

// GetDonationsRange returns donations rows for a company within [since, until].
// Rows with a NULL donated_at are excluded. Results are ordered by donated_at ascending.
func (s *Store) GetDonationsRange(ctx context.Context, companyID int, since, until time.Time) ([]Donation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, company_id, donor_name, donor_type, donor_employer,
		       recipient, recipient_person_id, recipient_type, amount_cents, donated_at
		FROM donations
		WHERE company_id = ?
		  AND donated_at IS NOT NULL
		  AND donated_at >= ?
		  AND donated_at <= ?
		ORDER BY donated_at
	`, companyID, formatTime(since), formatTime(until))
	if err != nil {
		return nil, fmt.Errorf("querying donations range for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]Donation, 0)
	for rows.Next() {
		var d Donation
		var donatedAt *string
		if err := rows.Scan(&d.ID, &d.EventID, &d.CompanyID, &d.DonorName, &d.DonorType,
			&d.DonorEmployer, &d.Recipient, &d.RecipientPersonID, &d.RecipientType,
			&d.AmountCents, &donatedAt); err != nil {
			return nil, fmt.Errorf("scanning donation row: %w", err)
		}
		d.DonatedAt = scanTimePtr(donatedAt)
		result = append(result, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating donation rows: %w", err)
	}
	return result, nil
}

// LobbyingRow is one row of lobbying data exported for the frontend.
type LobbyingRow struct {
	ID             int     `json:"id"`
	Registrant     *string `json:"registrant,omitempty"`
	Client         *string `json:"client,omitempty"`
	SpecificIssues *string `json:"specific_issues,omitempty"`
	Amount         *string `json:"amount,omitempty"`
	PeriodStart    *string `json:"period_start,omitempty"`
	PeriodEnd      *string `json:"period_end,omitempty"`
	FiledAt        *string `json:"filed_at,omitempty"`
	Ticker         *string `json:"ticker,omitempty"`
	CompanyName    *string `json:"company_name,omitempty"`
}

// ListLobbyingRecords returns lobbying records ordered by filed_at descending,
// joined with companies to include ticker/name when a client_company_id is set.
func (s *Store) ListLobbyingRecords(ctx context.Context, limit int) ([]LobbyingRow, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			l.id,
			l.registrant,
			l.client,
			l.specific_issues,
			CASE WHEN l.amount_cents IS NOT NULL
				THEN '$' || printf('%,d', l.amount_cents / 100)
				ELSE NULL
			END,
			l.period_start,
			l.period_end,
			l.filed_at,
			c.ticker,
			c.name
		FROM lobbying l
		LEFT JOIN companies c ON c.id = l.client_company_id
		ORDER BY l.filed_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing lobbying records: %w", err)
	}
	defer rows.Close()

	result := make([]LobbyingRow, 0)
	for rows.Next() {
		var r LobbyingRow
		if err := rows.Scan(&r.ID, &r.Registrant, &r.Client, &r.SpecificIssues,
			&r.Amount, &r.PeriodStart, &r.PeriodEnd, &r.FiledAt,
			&r.Ticker, &r.CompanyName); err != nil {
			return nil, fmt.Errorf("scanning lobbying row: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating lobbying rows: %w", err)
	}
	return result, nil
}
