package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

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
	Sector string
	Limit  int
	Offset int
}

// StrPtr is a helper for creating *string values in test code and seed data.
func StrPtr(s string) *string { return &s }

func (s *Store) UpsertCompany(ctx context.Context, c Company) (Company, error) {
	var result Company
	err := s.pool.QueryRow(ctx, `
		INSERT INTO companies (ticker, name, sector, subsector, naics_code, market_cap_bucket)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (ticker) DO UPDATE SET
			name = EXCLUDED.name,
			sector = EXCLUDED.sector,
			subsector = EXCLUDED.subsector,
			naics_code = EXCLUDED.naics_code,
			market_cap_bucket = EXCLUDED.market_cap_bucket
		RETURNING id, ticker, name, sector, subsector, naics_code, market_cap_bucket
	`, c.Ticker, c.Name, c.Sector, c.Subsector, c.NAICSCode, c.MarketCapBucket,
	).Scan(&result.ID, &result.Ticker, &result.Name, &result.Sector,
		&result.Subsector, &result.NAICSCode, &result.MarketCapBucket)
	if err != nil {
		return Company{}, fmt.Errorf("upserting company %s: %w", c.Ticker, err)
	}
	return result, nil
}

func (s *Store) GetCompanyByTicker(ctx context.Context, ticker string) (Company, error) {
	var c Company
	err := s.pool.QueryRow(ctx, `
		SELECT id, ticker, name, sector, subsector, naics_code, market_cap_bucket
		FROM companies WHERE ticker = $1
	`, ticker).Scan(&c.ID, &c.Ticker, &c.Name, &c.Sector,
		&c.Subsector, &c.NAICSCode, &c.MarketCapBucket)
	if err != nil {
		return Company{}, fmt.Errorf("getting company %s: %w", ticker, err)
	}
	return c, nil
}

func (s *Store) ListCompanies(ctx context.Context, f CompanyFilter) ([]Company, error) {
	query := "SELECT id, ticker, name, sector, subsector, naics_code, market_cap_bucket FROM companies WHERE 1=1"
	args := []any{}
	argN := 1

	if f.Sector != "" {
		query += fmt.Sprintf(" AND sector = $%d", argN)
		args = append(args, f.Sector)
		argN++
	}

	query += " ORDER BY ticker"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, f.Limit)
		argN++
	}
	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing companies: %w", err)
	}
	defer rows.Close()

	var companies []Company
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

func (s *Store) DeleteCompany(ctx context.Context, id int) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM companies WHERE id = $1", id)
	return err
}
