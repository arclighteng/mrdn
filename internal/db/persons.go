package db

import (
	"context"
	"fmt"
)

// Person represents a political figure or government official tracked by MRDN.
type Person struct {
	ID                 int     `json:"id"`
	Slug               string  `json:"slug"`
	Name               string  `json:"name"`
	Role               string  `json:"role"`
	Tier               int     `json:"tier"`
	Branch             *string `json:"branch,omitempty"`
	State              *string `json:"state,omitempty"`
	Party              *string `json:"party,omitempty"`
	BioguideID         *string `json:"bioguide_id,omitempty"`
	LinkedPersonID     *int    `json:"linked_person_id,omitempty"`
	LinkedRelationship *string `json:"linked_relationship,omitempty"`
	DisclosureSource   *string `json:"disclosure_source,omitempty"`
	TradeCount         int     `json:"trade_count"`
	TickersTouched     int     `json:"tickers_touched"`
	EstVolumeUSD       int64   `json:"est_volume_usd"`
}

// PersonFilter controls which persons are returned by ListPersons and CountPersons.
type PersonFilter struct {
	Tier   *int
	Branch string
	Role   string
	State  string
	Party  string
	Sort   string
	Limit  int
	Offset int
}

// UpsertPerson inserts or updates a person by slug, returning the persisted row.
func (s *Store) UpsertPerson(ctx context.Context, p Person) (Person, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO persons (slug, name, role, tier, branch, state, party, bioguide_id, linked_person_id, linked_relationship, disclosure_source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (slug) DO UPDATE SET
			name                = excluded.name,
			role                = excluded.role,
			tier                = excluded.tier,
			branch              = COALESCE(excluded.branch, persons.branch),
			state               = COALESCE(excluded.state, persons.state),
			party               = COALESCE(excluded.party, persons.party),
			bioguide_id         = COALESCE(excluded.bioguide_id, persons.bioguide_id),
			linked_person_id    = COALESCE(excluded.linked_person_id, persons.linked_person_id),
			linked_relationship = COALESCE(excluded.linked_relationship, persons.linked_relationship),
			disclosure_source   = COALESCE(excluded.disclosure_source, persons.disclosure_source)
	`, p.Slug, p.Name, p.Role, p.Tier, p.Branch, p.State, p.Party, p.BioguideID,
		p.LinkedPersonID, p.LinkedRelationship, p.DisclosureSource)
	if err != nil {
		return Person{}, fmt.Errorf("upserting person %s: %w", p.Slug, err)
	}
	return s.GetPersonBySlug(ctx, p.Slug)
}

// GetPersonBySlug returns the person with the given slug.
func (s *Store) GetPersonBySlug(ctx context.Context, slug string) (Person, error) {
	var p Person
	err := s.db.QueryRowContext(ctx, `
		SELECT id, slug, name, role, tier, branch, state, party, bioguide_id, linked_person_id, linked_relationship, disclosure_source
		FROM persons WHERE slug = ?
	`, slug).Scan(
		&p.ID, &p.Slug, &p.Name, &p.Role, &p.Tier,
		&p.Branch, &p.State, &p.Party, &p.BioguideID,
		&p.LinkedPersonID, &p.LinkedRelationship, &p.DisclosureSource,
	)
	if err != nil {
		return Person{}, fmt.Errorf("getting person %s: %w", slug, err)
	}
	return p, nil
}

func buildPersonWhere(f PersonFilter) (conditions string, args []any) {
	conditions = "WHERE 1=1"

	if f.Tier != nil {
		conditions += " AND p.tier = ?"
		args = append(args, *f.Tier)
	}
	if f.Branch != "" {
		conditions += " AND p.branch = ?"
		args = append(args, f.Branch)
	}
	if f.Role != "" {
		conditions += " AND p.role = ?"
		args = append(args, f.Role)
	}
	if f.State != "" {
		conditions += " AND p.state = ?"
		args = append(args, f.State)
	}
	if f.Party != "" {
		conditions += " AND p.party = ?"
		args = append(args, f.Party)
	}
	return conditions, args
}

// ListPersons returns persons matching the filter.
func (s *Store) ListPersons(ctx context.Context, f PersonFilter) ([]Person, error) {
	conditions, args := buildPersonWhere(f)

	orderBy := " ORDER BY p.name"
	if f.Sort == "influence" {
		orderBy = ` ORDER BY (
			SELECT COALESCE(SUM(
				COALESCE(
					CASE
						WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
							THEN (ct.amount_range_low + ct.amount_range_high) / 2
						WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
						WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
						ELSE 0
					END, 0)
			), 0) FROM congressional_trades ct WHERE ct.person_id = p.id
		) DESC, (
			SELECT COUNT(*) FROM congressional_trades ct WHERE ct.person_id = p.id
		) DESC, p.tier ASC, p.name ASC`
	}

	query := `SELECT p.id, p.slug, p.name, p.role, p.tier, p.branch, p.state, p.party,
		p.bioguide_id, p.linked_person_id, p.linked_relationship, p.disclosure_source,
		COALESCE((SELECT COUNT(*) FROM congressional_trades ct WHERE ct.person_id = p.id), 0),
		COALESCE((SELECT COUNT(DISTINCT ct.ticker) FROM congressional_trades ct WHERE ct.person_id = p.id AND ct.ticker IS NOT NULL AND ct.ticker <> '' AND ct.ticker <> '--'), 0),
		COALESCE((SELECT SUM(
			COALESCE(
				CASE
					WHEN ct.amount_range_low IS NOT NULL AND ct.amount_range_high IS NOT NULL
						THEN (ct.amount_range_low + ct.amount_range_high) / 2
					WHEN ct.amount_range_low IS NOT NULL THEN ct.amount_range_low
					WHEN ct.amount_range_high IS NOT NULL THEN ct.amount_range_high
					ELSE 0
				END, 0)
		) FROM congressional_trades ct WHERE ct.person_id = p.id), 0)
		FROM persons p ` + conditions + orderBy

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
		return nil, fmt.Errorf("listing persons: %w", err)
	}
	defer rows.Close()

	persons := make([]Person, 0)
	for rows.Next() {
		var p Person
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.Name, &p.Role, &p.Tier,
			&p.Branch, &p.State, &p.Party, &p.BioguideID,
			&p.LinkedPersonID, &p.LinkedRelationship, &p.DisclosureSource,
			&p.TradeCount, &p.TickersTouched, &p.EstVolumeUSD,
		); err != nil {
			return nil, fmt.Errorf("scanning person: %w", err)
		}
		persons = append(persons, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating persons: %w", err)
	}
	return persons, nil
}

// CountPersons returns the total number of persons matching the filter.
func (s *Store) CountPersons(ctx context.Context, f PersonFilter) (int, error) {
	conditions, args := buildPersonWhere(f)

	query := "SELECT COUNT(*) FROM persons p " + conditions

	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting persons: %w", err)
	}
	return count, nil
}

// DeletePerson removes the person with the given slug.
func (s *Store) DeletePerson(ctx context.Context, slug string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM persons WHERE slug = ?", slug)
	if err != nil {
		return fmt.Errorf("deleting person %s: %w", slug, err)
	}
	return nil
}
