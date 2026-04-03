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
	Sector       string
	Ticker       string   // partial match (ILIKE)
	MinComposite *float64 // latest composite score >= this
	MaxComposite *float64 // latest composite score <= this
	Limit        int
	Offset       int
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

// buildCompanyWhere constructs the WHERE clause args and conditions for company
// filters. It returns the WHERE fragment (starting with "WHERE 1=1"), args slice,
// and the next arg index. When MinComposite or MaxComposite are set, useCTE must
// be true and the caller is responsible for prefixing the appropriate CTE and
// qualifying the composite_score column as "ls.composite_score".
func buildCompanyWhere(f CompanyFilter) (conditions string, args []any, argN int) {
	argN = 1
	conditions = "WHERE 1=1"

	if f.Sector != "" {
		conditions += fmt.Sprintf(" AND c.sector = $%d", argN)
		args = append(args, f.Sector)
		argN++
	}
	if f.Ticker != "" {
		conditions += fmt.Sprintf(" AND c.ticker ILIKE $%d", argN)
		args = append(args, "%"+f.Ticker+"%")
		argN++
	}
	if f.MinComposite != nil {
		conditions += fmt.Sprintf(" AND ls.composite_score >= $%d", argN)
		args = append(args, *f.MinComposite)
		argN++
	}
	if f.MaxComposite != nil {
		conditions += fmt.Sprintf(" AND ls.composite_score <= $%d", argN)
		args = append(args, *f.MaxComposite)
		argN++
	}
	return conditions, args, argN
}

func (s *Store) ListCompanies(ctx context.Context, f CompanyFilter) ([]Company, error) {
	useCTE := f.MinComposite != nil || f.MaxComposite != nil

	var query string
	conditions, args, argN := buildCompanyWhere(f)

	if useCTE {
		query = `WITH latest_scores AS (
			SELECT DISTINCT ON (company_id) company_id, composite_score
			FROM scores ORDER BY company_id, computed_at DESC
		)
		SELECT c.id, c.ticker, c.name, c.sector, c.subsector, c.naics_code, c.market_cap_bucket
		FROM companies c
		JOIN latest_scores ls ON ls.company_id = c.id
		` + conditions
	} else {
		// Replace "c." qualifier with bare column names for the simple query.
		// buildCompanyWhere always uses "c." prefix; for the no-CTE path we
		// alias the table so the same conditions work.
		query = "SELECT c.id, c.ticker, c.name, c.sector, c.subsector, c.naics_code, c.market_cap_bucket FROM companies c " + conditions
	}

	query += " ORDER BY c.ticker"

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

// CountCompanies returns the total number of companies matching the filter,
// applying the same WHERE logic as ListCompanies (including the CTE join when
// composite score bounds are specified).
func (s *Store) CountCompanies(ctx context.Context, f CompanyFilter) (int, error) {
	useCTE := f.MinComposite != nil || f.MaxComposite != nil

	conditions, args, _ := buildCompanyWhere(f)

	var query string
	if useCTE {
		query = `WITH latest_scores AS (
			SELECT DISTINCT ON (company_id) company_id, composite_score
			FROM scores ORDER BY company_id, computed_at DESC
		)
		SELECT COUNT(*)
		FROM companies c
		JOIN latest_scores ls ON ls.company_id = c.id
		` + conditions
	} else {
		query = "SELECT COUNT(*) FROM companies c " + conditions
	}

	var count int
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting companies: %w", err)
	}
	return count, nil
}

func (s *Store) DeleteCompany(ctx context.Context, id int) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM companies WHERE id = $1", id)
	return err
}
