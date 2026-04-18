package db

import (
	"context"
	"fmt"
	"time"
)

// CongressionalTrade maps to the congressional_trades table.
type CongressionalTrade struct {
	ID              int        `json:"id"`
	EventID         *int       `json:"event_id,omitempty"`
	PersonID        *int       `json:"person_id,omitempty"`
	CompanyID       *int       `json:"company_id,omitempty"`
	OwnerType       *string    `json:"owner_type,omitempty"`
	Ticker          *string    `json:"ticker,omitempty"`
	TradeType       *string    `json:"trade_type,omitempty"`
	AmountRangeLow  *int       `json:"amount_range_low,omitempty"`
	AmountRangeHigh *int       `json:"amount_range_high,omitempty"`
	FiledAt         *time.Time `json:"filed_at,omitempty"`
	TradedAt        *time.Time `json:"traded_at,omitempty"`
}

// InsertCongressionalTrade inserts a row into congressional_trades.
func (s *Store) InsertCongressionalTrade(ctx context.Context, t CongressionalTrade) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO congressional_trades
			(event_id, person_id, company_id, owner_type, ticker, trade_type,
			 amount_range_low, amount_range_high, filed_at, traded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, t.EventID, t.PersonID, t.CompanyID, t.OwnerType, t.Ticker, t.TradeType,
		t.AmountRangeLow, t.AmountRangeHigh, formatTimePtr(t.FiledAt), formatTimePtr(t.TradedAt))
	if err != nil {
		return fmt.Errorf("inserting congressional trade: %w", err)
	}
	return nil
}

// Contract maps to the contracts table.
type Contract struct {
	ID          int        `json:"id"`
	EventID     *int       `json:"event_id,omitempty"`
	CompanyID   *int       `json:"company_id,omitempty"`
	Agency      *string    `json:"agency,omitempty"`
	AmountCents *int64     `json:"amount_cents,omitempty"`
	ActionType  *string    `json:"action_type,omitempty"`
	Description *string    `json:"description,omitempty"`
	AwardedAt   *time.Time `json:"awarded_at,omitempty"`
}

// InsertContract inserts a row into contracts.
func (s *Store) InsertContract(ctx context.Context, c Contract) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO contracts
			(event_id, company_id, agency, amount_cents, action_type, description, awarded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, c.EventID, c.CompanyID, c.Agency, c.AmountCents, c.ActionType, c.Description, formatTimePtr(c.AwardedAt))
	if err != nil {
		return fmt.Errorf("inserting contract: %w", err)
	}
	return nil
}

// Sanction maps to the sanctions table.
type Sanction struct {
	ID         int        `json:"id"`
	EventID    *int       `json:"event_id,omitempty"`
	CompanyID  *int       `json:"company_id,omitempty"`
	EntityName *string    `json:"entity_name,omitempty"`
	EntityType *string    `json:"entity_type,omitempty"`
	Program    *string    `json:"program,omitempty"`
	Country    *string    `json:"country,omitempty"`
	AddedAt    *time.Time `json:"added_at,omitempty"`
}

// InsertSanction inserts a row into sanctions.
func (s *Store) InsertSanction(ctx context.Context, sn Sanction) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sanctions
			(event_id, company_id, entity_name, entity_type, program, country, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, sn.EventID, sn.CompanyID, sn.EntityName, sn.EntityType, sn.Program, sn.Country, formatTimePtr(sn.AddedAt))
	if err != nil {
		return fmt.Errorf("inserting sanction: %w", err)
	}
	return nil
}

// InsiderTrade maps to the insider_trades table.
type InsiderTrade struct {
	ID         int        `json:"id"`
	EventID    *int       `json:"event_id,omitempty"`
	CompanyID  *int       `json:"company_id,omitempty"`
	FilerName  *string    `json:"filer_name,omitempty"`
	FilerTitle *string    `json:"filer_title,omitempty"`
	TradeType  *string    `json:"trade_type,omitempty"`
	Shares     *int       `json:"shares,omitempty"`
	PriceCents *int64     `json:"price_cents,omitempty"`
	FiledAt    *time.Time `json:"filed_at,omitempty"`
	TradedAt   *time.Time `json:"traded_at,omitempty"`
}

// InsertInsiderTrade inserts a row into insider_trades.
func (s *Store) InsertInsiderTrade(ctx context.Context, t InsiderTrade) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO insider_trades
			(event_id, company_id, filer_name, filer_title, trade_type, shares, price_cents, filed_at, traded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, t.EventID, t.CompanyID, t.FilerName, t.FilerTitle, t.TradeType, t.Shares, t.PriceCents,
		formatTimePtr(t.FiledAt), formatTimePtr(t.TradedAt))
	if err != nil {
		return fmt.Errorf("inserting insider trade: %w", err)
	}
	return nil
}

// Donation maps to the donations table.
type Donation struct {
	ID                int        `json:"id"`
	EventID           *int       `json:"event_id,omitempty"`
	CompanyID         *int       `json:"company_id,omitempty"`
	DonorName         *string    `json:"donor_name,omitempty"`
	DonorType         *string    `json:"donor_type,omitempty"`
	DonorEmployer     *string    `json:"donor_employer,omitempty"`
	Recipient         *string    `json:"recipient,omitempty"`
	RecipientPersonID *int       `json:"recipient_person_id,omitempty"`
	RecipientType     *string    `json:"recipient_type,omitempty"`
	AmountCents       *int64     `json:"amount_cents,omitempty"`
	DonatedAt         *time.Time `json:"donated_at,omitempty"`
}

// InsertDonation inserts a row into donations.
func (s *Store) InsertDonation(ctx context.Context, d Donation) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO donations
			(event_id, company_id, donor_name, donor_type, donor_employer,
			 recipient, recipient_person_id, recipient_type, amount_cents, donated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, d.EventID, d.CompanyID, d.DonorName, d.DonorType, d.DonorEmployer,
		d.Recipient, d.RecipientPersonID, d.RecipientType, d.AmountCents, formatTimePtr(d.DonatedAt))
	if err != nil {
		return fmt.Errorf("inserting donation: %w", err)
	}
	return nil
}

// MarketDataRow maps to the market_data table.
type MarketDataRow struct {
	ID         int       `json:"id"`
	CompanyID  int       `json:"company_id"`
	Source     string    `json:"source"`
	DataType   string    `json:"data_type"`
	PriceCents *int64    `json:"price_cents,omitempty"`
	Volume     *int64    `json:"volume,omitempty"`
	ChangePct  *float64  `json:"change_pct,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// InsertMarketData inserts a row into market_data.
func (s *Store) InsertMarketData(ctx context.Context, m MarketDataRow) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO market_data
			(company_id, source, data_type, price_cents, volume, change_pct, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, m.CompanyID, m.Source, m.DataType, m.PriceCents, m.Volume, m.ChangePct, formatTime(m.RecordedAt))
	if err != nil {
		return fmt.Errorf("inserting market data: %w", err)
	}
	return nil
}

// WarnFiling maps to the warn_filings table.
type WarnFiling struct {
	ID              int        `json:"id"`
	EventID         *int       `json:"event_id,omitempty"`
	CompanyID       *int       `json:"company_id,omitempty"`
	State           *string    `json:"state,omitempty"`
	City            *string    `json:"city,omitempty"`
	WorkersAffected *int       `json:"workers_affected,omitempty"`
	LayoffDate      *time.Time `json:"layoff_date,omitempty"`
	FiledAt         *time.Time `json:"filed_at,omitempty"`
}

// InsertWarnFiling inserts a row into warn_filings.
func (s *Store) InsertWarnFiling(ctx context.Context, w WarnFiling) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO warn_filings
			(event_id, company_id, state, city, workers_affected, layoff_date, filed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, w.EventID, w.CompanyID, w.State, w.City, w.WorkersAffected,
		formatTimePtr(w.LayoffDate), formatTimePtr(w.FiledAt))
	if err != nil {
		return fmt.Errorf("inserting warn filing: %w", err)
	}
	return nil
}

// LobbyingRecord maps to the lobbying table.
type LobbyingRecord struct {
	ID              int        `json:"id"`
	EventID         *int       `json:"event_id,omitempty"`
	ClientCompanyID *int       `json:"client_company_id,omitempty"`
	Registrant      *string    `json:"registrant,omitempty"`
	Client          *string    `json:"client,omitempty"`
	SpecificIssues  *string    `json:"specific_issues,omitempty"`
	AmountCents     *int64     `json:"amount_cents,omitempty"`
	PeriodStart     *time.Time `json:"period_start,omitempty"`
	PeriodEnd       *time.Time `json:"period_end,omitempty"`
	FiledAt         *time.Time `json:"filed_at,omitempty"`
}

// InsertLobbyingRecord inserts a row into lobbying.
func (s *Store) InsertLobbyingRecord(ctx context.Context, l LobbyingRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO lobbying
			(event_id, client_company_id, registrant, client, specific_issues,
			 amount_cents, period_start, period_end, filed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, l.EventID, l.ClientCompanyID, l.Registrant, l.Client, l.SpecificIssues,
		l.AmountCents, formatTimePtr(l.PeriodStart), formatTimePtr(l.PeriodEnd), formatTimePtr(l.FiledAt))
	if err != nil {
		return fmt.Errorf("inserting lobbying record: %w", err)
	}
	return nil
}

// CourtFiling maps to the court_filings table.
// Parties are stored in the court_filing_parties junction table.
type CourtFiling struct {
	ID         int        `json:"id"`
	EventID    *int       `json:"event_id,omitempty"`
	CompanyID  *int       `json:"company_id,omitempty"`
	CaseNumber *string    `json:"case_number,omitempty"`
	Court      *string    `json:"court,omitempty"`
	Parties    []string   `json:"parties,omitempty"`
	FilingType *string    `json:"filing_type,omitempty"`
	FiledAt    *time.Time `json:"filed_at,omitempty"`
}

// InsertCourtFiling inserts a row into court_filings and each party into
// court_filing_parties.
func (s *Store) InsertCourtFiling(ctx context.Context, cf CourtFiling) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO court_filings
			(event_id, company_id, case_number, court, filing_type, filed_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, cf.EventID, cf.CompanyID, cf.CaseNumber, cf.Court, cf.FilingType, formatTimePtr(cf.FiledAt))
	if err != nil {
		return fmt.Errorf("inserting court filing: %w", err)
	}

	filingID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting court filing id: %w", err)
	}

	for _, party := range cf.Parties {
		if _, err := s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO court_filing_parties (filing_id, party_name)
			VALUES (?, ?)
		`, filingID, party); err != nil {
			return fmt.Errorf("inserting court filing party %q: %w", party, err)
		}
	}

	return nil
}

// Tariff maps to the tariffs table.
// HsCodes and Countries are stored in tariff_hs_codes and tariff_countries
// junction tables respectively.
type Tariff struct {
	ID          int        `json:"id"`
	EventID     *int       `json:"event_id,omitempty"`
	ActionType  *string    `json:"action_type,omitempty"`
	EffectiveAt *time.Time `json:"effective_at,omitempty"`
	HsCodes     []string   `json:"hs_codes,omitempty"`
	Countries   []string   `json:"countries,omitempty"`
}

// InsertTariff inserts a row into tariffs and each HS code and country into
// their respective junction tables.
func (s *Store) InsertTariff(ctx context.Context, t Tariff) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO tariffs
			(event_id, action_type, effective_at)
		VALUES (?, ?, ?)
	`, t.EventID, t.ActionType, formatTimePtr(t.EffectiveAt))
	if err != nil {
		return fmt.Errorf("inserting tariff: %w", err)
	}

	tariffID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting tariff id: %w", err)
	}

	for _, hs := range t.HsCodes {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO tariff_hs_codes (tariff_id, hs_code)
			VALUES (?, ?)
		`, tariffID, hs); err != nil {
			return fmt.Errorf("inserting tariff hs code %q: %w", hs, err)
		}
	}

	for _, country := range t.Countries {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO tariff_countries (tariff_id, country)
			VALUES (?, ?)
		`, tariffID, country); err != nil {
			return fmt.Errorf("inserting tariff country %q: %w", country, err)
		}
	}

	return nil
}
