package db

import (
	"context"
	"fmt"
	"time"
)

// GetMarketDataRange returns market_data rows for a company within [since, until].
// Rows are ordered by recorded_at ascending.
func (s *Store) GetMarketDataRange(ctx context.Context, companyID int, since, until time.Time) ([]MarketDataRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, company_id, source, data_type, price_cents, volume, change_pct, recorded_at
		FROM market_data
		WHERE company_id = $1
		  AND recorded_at >= $2
		  AND recorded_at <= $3
		ORDER BY recorded_at
	`, companyID, since, until)
	if err != nil {
		return nil, fmt.Errorf("querying market data range for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]MarketDataRow, 0)
	for rows.Next() {
		var m MarketDataRow
		if err := rows.Scan(&m.ID, &m.CompanyID, &m.Source, &m.DataType,
			&m.PriceCents, &m.Volume, &m.ChangePct, &m.RecordedAt); err != nil {
			return nil, fmt.Errorf("scanning market data row: %w", err)
		}
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
	rows, err := s.db.Query(ctx, `
		SELECT id, event_id, company_id, filer_name, filer_title, trade_type, shares, price_cents, filed_at, traded_at
		FROM insider_trades
		WHERE company_id = $1
		  AND filed_at IS NOT NULL
		  AND filed_at >= $2
		  AND filed_at <= $3
		ORDER BY filed_at
	`, companyID, since, until)
	if err != nil {
		return nil, fmt.Errorf("querying insider trades range for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]InsiderTrade, 0)
	for rows.Next() {
		var t InsiderTrade
		if err := rows.Scan(&t.ID, &t.EventID, &t.CompanyID, &t.FilerName, &t.FilerTitle,
			&t.TradeType, &t.Shares, &t.PriceCents, &t.FiledAt, &t.TradedAt); err != nil {
			return nil, fmt.Errorf("scanning insider trade row: %w", err)
		}
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating insider trade rows: %w", err)
	}
	return result, nil
}

// GetSanctionsRange returns sanctions rows for a company within [since, until].
// Rows with a NULL added_at are excluded. Results are ordered by added_at ascending.
func (s *Store) GetSanctionsRange(ctx context.Context, companyID int, since, until time.Time) ([]Sanction, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_id, company_id, entity_name, entity_type, program, country, added_at
		FROM sanctions
		WHERE company_id = $1
		  AND added_at IS NOT NULL
		  AND added_at >= $2
		  AND added_at <= $3
		ORDER BY added_at
	`, companyID, since, until)
	if err != nil {
		return nil, fmt.Errorf("querying sanctions range for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]Sanction, 0)
	for rows.Next() {
		var sn Sanction
		if err := rows.Scan(&sn.ID, &sn.EventID, &sn.CompanyID, &sn.EntityName,
			&sn.EntityType, &sn.Program, &sn.Country, &sn.AddedAt); err != nil {
			return nil, fmt.Errorf("scanning sanction row: %w", err)
		}
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
	rows, err := s.db.Query(ctx, `
		SELECT id, event_id, company_id, agency, amount_cents, action_type, description, awarded_at
		FROM contracts
		WHERE company_id = $1
		  AND awarded_at IS NOT NULL
		  AND awarded_at >= $2
		  AND awarded_at <= $3
		ORDER BY awarded_at
	`, companyID, since, until)
	if err != nil {
		return nil, fmt.Errorf("querying contracts range for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]Contract, 0)
	for rows.Next() {
		var c Contract
		if err := rows.Scan(&c.ID, &c.EventID, &c.CompanyID, &c.Agency,
			&c.AmountCents, &c.ActionType, &c.Description, &c.AwardedAt); err != nil {
			return nil, fmt.Errorf("scanning contract row: %w", err)
		}
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
	rows, err := s.db.Query(ctx, `
		SELECT id, event_id, company_id, donor_name, donor_type, donor_employer,
		       recipient, recipient_person_id, recipient_type, amount_cents, donated_at
		FROM donations
		WHERE company_id = $1
		  AND donated_at IS NOT NULL
		  AND donated_at >= $2
		  AND donated_at <= $3
		ORDER BY donated_at
	`, companyID, since, until)
	if err != nil {
		return nil, fmt.Errorf("querying donations range for company %d: %w", companyID, err)
	}
	defer rows.Close()

	result := make([]Donation, 0)
	for rows.Next() {
		var d Donation
		if err := rows.Scan(&d.ID, &d.EventID, &d.CompanyID, &d.DonorName, &d.DonorType,
			&d.DonorEmployer, &d.Recipient, &d.RecipientPersonID, &d.RecipientType,
			&d.AmountCents, &d.DonatedAt); err != nil {
			return nil, fmt.Errorf("scanning donation row: %w", err)
		}
		result = append(result, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating donation rows: %w", err)
	}
	return result, nil
}
