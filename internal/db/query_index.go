package db

import "context"

// ActiveTrader is a person with at least one congressional trade.
type ActiveTrader struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Party string `json:"party"`
	Role  string `json:"role"`
}

// ListActiveTraders returns persons who have at least one congressional trade.
func (s *Store) ListActiveTraders(ctx context.Context, limit int) ([]ActiveTrader, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT p.slug, p.name, COALESCE(p.party, '') AS party, COALESCE(p.role, '') AS role
		FROM persons p
		JOIN congressional_trades ct ON ct.person_id = p.id
		ORDER BY p.name
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ActiveTrader
	for rows.Next() {
		var t ActiveTrader
		if err := rows.Scan(&t.Slug, &t.Name, &t.Party, &t.Role); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// DistinctAgencies returns distinct agency names from the contracts table.
func (s *Store) DistinctAgencies(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT agency FROM contracts
		WHERE agency IS NOT NULL AND agency != ''
		ORDER BY agency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// DistinctSectors returns distinct sector names from the companies table.
func (s *Store) DistinctSectors(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT sector FROM companies
		WHERE sector IS NOT NULL AND sector != ''
		ORDER BY sector`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

// DistinctPrograms returns distinct OFAC program names from the sanctions table.
func (s *Store) DistinctPrograms(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT program FROM sanctions
		WHERE program IS NOT NULL AND program != ''
		ORDER BY program`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// CommitteeEntry represents a congressional committee for the autocomplete index.
type CommitteeEntry struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// ListCommittees returns all distinct committees from person_committees.
func (s *Store) ListCommittees(ctx context.Context) ([]CommitteeEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT committee_code, committee_name
		FROM person_committees
		WHERE committee_code IS NOT NULL
		ORDER BY committee_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []CommitteeEntry
	for rows.Next() {
		var c CommitteeEntry
		if err := rows.Scan(&c.Code, &c.Name); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
