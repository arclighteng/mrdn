package db

import (
	"context"
	"database/sql"
	"fmt"
)

// DBTX is implemented by *sql.DB and *sql.Tx.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db DBTX
}

func NewStore(db DBTX) *Store {
	return &Store{db: db}
}

// DB returns the underlying DBTX for direct queries.
func (s *Store) DB() DBTX { return s.db }

type Company struct {
	ID              int     `json:"id"`
	Ticker          string  `json:"ticker"`
	Name            string  `json:"name"`
	Sector          *string `json:"sector,omitempty"`
	Subsector       *string `json:"subsector,omitempty"`
	NAICSCode       *string `json:"naics_code,omitempty"`
	MarketCapBucket *string `json:"market_cap_bucket,omitempty"`
}

type CompanyFilter struct {
	Sector       string
	Ticker       string   // partial match (LIKE)
	MinComposite *float64 // latest composite score >= this
	MaxComposite *float64 // latest composite score <= this
	Limit        int
	Offset       int
}

// StrPtr is a helper for creating *string values in test code and seed data.
func StrPtr(s string) *string { return &s }
func IntPtr(i int) *int       { return &i }

// EnsureCompany inserts the company if it doesn't exist yet, but never
// overwrites an existing row.
func (s *Store) EnsureCompany(ctx context.Context, c Company) (Company, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO companies (ticker, name, sector, subsector, naics_code, market_cap_bucket)
		VALUES (?, ?, ?, ?, ?, ?)
	`, c.Ticker, c.Name, c.Sector, c.Subsector, c.NAICSCode, c.MarketCapBucket)
	if err != nil {
		return Company{}, fmt.Errorf("ensuring company %s: %w", c.Ticker, err)
	}
	return s.GetCompanyByTicker(ctx, c.Ticker)
}

func (s *Store) UpsertCompany(ctx context.Context, c Company) (Company, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO companies (ticker, name, sector, subsector, naics_code, market_cap_bucket)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (ticker) DO UPDATE SET
			name = excluded.name,
			sector = excluded.sector,
			subsector = excluded.subsector,
			naics_code = excluded.naics_code,
			market_cap_bucket = excluded.market_cap_bucket
	`, c.Ticker, c.Name, c.Sector, c.Subsector, c.NAICSCode, c.MarketCapBucket)
	if err != nil {
		return Company{}, fmt.Errorf("upserting company %s: %w", c.Ticker, err)
	}
	return s.GetCompanyByTicker(ctx, c.Ticker)
}

func (s *Store) GetCompanyByTicker(ctx context.Context, ticker string) (Company, error) {
	var c Company
	err := s.db.QueryRowContext(ctx, `
		SELECT id, ticker, name, sector, subsector, naics_code, market_cap_bucket
		FROM companies WHERE ticker = ?
	`, ticker).Scan(&c.ID, &c.Ticker, &c.Name, &c.Sector,
		&c.Subsector, &c.NAICSCode, &c.MarketCapBucket)
	if err != nil {
		return Company{}, fmt.Errorf("getting company %s: %w", ticker, err)
	}
	return c, nil
}

func buildCompanyWhere(f CompanyFilter) (conditions string, args []any) {
	conditions = "WHERE 1=1"

	if f.Sector != "" {
		conditions += " AND c.sector = ?"
		args = append(args, f.Sector)
	}
	if f.Ticker != "" {
		conditions += " AND c.ticker LIKE ?"
		args = append(args, "%"+f.Ticker+"%")
	}
	if f.MinComposite != nil {
		conditions += " AND ls.composite_score >= ?"
		args = append(args, *f.MinComposite)
	}
	if f.MaxComposite != nil {
		conditions += " AND ls.composite_score <= ?"
		args = append(args, *f.MaxComposite)
	}
	return conditions, args
}

func (s *Store) ListCompanies(ctx context.Context, f CompanyFilter) ([]Company, error) {
	useCTE := f.MinComposite != nil || f.MaxComposite != nil

	var query string
	conditions, args := buildCompanyWhere(f)

	if useCTE {
		query = `WITH latest_scores AS (
			SELECT company_id, composite_score FROM (
				SELECT company_id, composite_score,
				       ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY computed_at DESC) AS rn
				FROM scores
			) WHERE rn = 1
		)
		SELECT c.id, c.ticker, c.name, c.sector, c.subsector, c.naics_code, c.market_cap_bucket
		FROM companies c
		JOIN latest_scores ls ON ls.company_id = c.id
		` + conditions
	} else {
		query = "SELECT c.id, c.ticker, c.name, c.sector, c.subsector, c.naics_code, c.market_cap_bucket FROM companies c " + conditions
	}

	query += " ORDER BY c.ticker"

	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}
	if f.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, f.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing companies: %w", err)
	}
	defer rows.Close()

	companies := make([]Company, 0)
	for rows.Next() {
		var c Company
		if err := rows.Scan(&c.ID, &c.Ticker, &c.Name, &c.Sector,
			&c.Subsector, &c.NAICSCode, &c.MarketCapBucket); err != nil {
			return nil, fmt.Errorf("scanning company: %w", err)
		}
		companies = append(companies, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating companies: %w", err)
	}
	return companies, nil
}

func (s *Store) CountCompanies(ctx context.Context, f CompanyFilter) (int, error) {
	useCTE := f.MinComposite != nil || f.MaxComposite != nil

	conditions, args := buildCompanyWhere(f)

	var query string
	if useCTE {
		query = `WITH latest_scores AS (
			SELECT company_id, composite_score FROM (
				SELECT company_id, composite_score,
				       ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY computed_at DESC) AS rn
				FROM scores
			) WHERE rn = 1
		)
		SELECT COUNT(*)
		FROM companies c
		JOIN latest_scores ls ON ls.company_id = c.id
		` + conditions
	} else {
		query = "SELECT COUNT(*) FROM companies c " + conditions
	}

	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting companies: %w", err)
	}
	return count, nil
}

func (s *Store) DeleteCompany(ctx context.Context, id int) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM companies WHERE id = ?", id)
	return err
}

// ListTickers returns all company tickers from the companies table, sorted
// alphabetically. It implements the parser.TickerLister interface.
func (s *Store) ListTickers(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ticker FROM companies ORDER BY ticker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tickers []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tickers = append(tickers, t)
	}
	return tickers, rows.Err()
}
