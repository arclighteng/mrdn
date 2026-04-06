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
}

// PersonFilter controls which persons are returned by ListPersons and CountPersons.
type PersonFilter struct {
	Tier   *int
	Branch string
	Role   string
	State  string
	Party  string
	Limit  int
	Offset int
}

// UpsertPerson inserts or updates a person by slug, returning the persisted row.
func (s *Store) UpsertPerson(ctx context.Context, p Person) (Person, error) {
	var result Person
	err := s.db.QueryRow(ctx, `
		INSERT INTO persons (slug, name, role, tier, branch, state, party, bioguide_id, linked_person_id, linked_relationship, disclosure_source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (slug) DO UPDATE SET
			name                = EXCLUDED.name,
			role                = EXCLUDED.role,
			tier                = EXCLUDED.tier,
			branch              = EXCLUDED.branch,
			state               = EXCLUDED.state,
			party               = EXCLUDED.party,
			bioguide_id         = EXCLUDED.bioguide_id,
			linked_person_id    = EXCLUDED.linked_person_id,
			linked_relationship = EXCLUDED.linked_relationship,
			disclosure_source   = EXCLUDED.disclosure_source
		RETURNING id, slug, name, role, tier, branch, state, party, bioguide_id, linked_person_id, linked_relationship, disclosure_source
	`, p.Slug, p.Name, p.Role, p.Tier, p.Branch, p.State, p.Party, p.BioguideID,
		p.LinkedPersonID, p.LinkedRelationship, p.DisclosureSource,
	).Scan(
		&result.ID, &result.Slug, &result.Name, &result.Role, &result.Tier,
		&result.Branch, &result.State, &result.Party, &result.BioguideID,
		&result.LinkedPersonID, &result.LinkedRelationship, &result.DisclosureSource,
	)
	if err != nil {
		return Person{}, fmt.Errorf("upserting person %s: %w", p.Slug, err)
	}
	return result, nil
}

// GetPersonBySlug returns the person with the given slug.
// Returns a wrapped pgx.ErrNoRows when no matching row exists.
func (s *Store) GetPersonBySlug(ctx context.Context, slug string) (Person, error) {
	var p Person
	err := s.db.QueryRow(ctx, `
		SELECT id, slug, name, role, tier, branch, state, party, bioguide_id, linked_person_id, linked_relationship, disclosure_source
		FROM persons WHERE slug = $1
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

// buildPersonWhere constructs the WHERE clause and args for person filters.
// All column references use the "p." table alias. Returns the WHERE fragment
// (starting with "WHERE 1=1"), the args slice, and the next arg index.
func buildPersonWhere(f PersonFilter) (conditions string, args []any, argN int) {
	argN = 1
	conditions = "WHERE 1=1"

	if f.Tier != nil {
		conditions += fmt.Sprintf(" AND p.tier = $%d", argN)
		args = append(args, *f.Tier)
		argN++
	}
	if f.Branch != "" {
		conditions += fmt.Sprintf(" AND p.branch = $%d", argN)
		args = append(args, f.Branch)
		argN++
	}
	if f.Role != "" {
		conditions += fmt.Sprintf(" AND p.role = $%d", argN)
		args = append(args, f.Role)
		argN++
	}
	if f.State != "" {
		conditions += fmt.Sprintf(" AND p.state = $%d", argN)
		args = append(args, f.State)
		argN++
	}
	if f.Party != "" {
		conditions += fmt.Sprintf(" AND p.party = $%d", argN)
		args = append(args, f.Party)
		argN++
	}
	return conditions, args, argN
}

// ListPersons returns persons matching the filter, ordered by name.
func (s *Store) ListPersons(ctx context.Context, f PersonFilter) ([]Person, error) {
	conditions, args, argN := buildPersonWhere(f)

	query := `SELECT p.id, p.slug, p.name, p.role, p.tier, p.branch, p.state, p.party,
		p.bioguide_id, p.linked_person_id, p.linked_relationship, p.disclosure_source
		FROM persons p ` + conditions + " ORDER BY p.name"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, f.Limit)
		argN++
	}
	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := s.db.Query(ctx, query, args...)
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

// CountPersons returns the total number of persons matching the filter,
// applying the same WHERE logic as ListPersons.
func (s *Store) CountPersons(ctx context.Context, f PersonFilter) (int, error) {
	conditions, args, _ := buildPersonWhere(f)

	query := "SELECT COUNT(*) FROM persons p " + conditions

	var count int
	if err := s.db.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting persons: %w", err)
	}
	return count, nil
}

// DeletePerson removes the person with the given slug. Used for test cleanup.
func (s *Store) DeletePerson(ctx context.Context, slug string) error {
	_, err := s.db.Exec(ctx, "DELETE FROM persons WHERE slug = $1", slug)
	if err != nil {
		return fmt.Errorf("deleting person %s: %w", slug, err)
	}
	return nil
}
