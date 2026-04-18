package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// EntityLink records a directed relationship between two entities in the graph.
// Queries always check both directions (from OR to) when looking up links for
// a given entity.
type EntityLink struct {
	ID              int       `json:"id"`
	FromEntity      int       `json:"from_entity"`
	FromType        string    `json:"from_type"`
	ToEntity        int       `json:"to_entity"`
	ToType          string    `json:"to_type"`
	Relationship    string    `json:"relationship"`
	EvidenceEventID *int      `json:"evidence_event_id,omitempty"`
	DiscoveredAt    time.Time `json:"discovered_at"`
}

// EntityAlias maps a text alias to an entity, enabling fuzzy name resolution.
// The unique index idx_entity_aliases_unique prevents the same alias from being
// assigned to more than one entity of the same type.
type EntityAlias struct {
	ID         int      `json:"id"`
	EntityID   int      `json:"entity_id"`
	EntityType string   `json:"entity_type"`
	Alias      string   `json:"alias"`
	Source     *string  `json:"source,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
}

// InsertEntityLink persists a new entity link and returns the stored row.
// SQLite does not support RETURNING, so we use ExecContext + LastInsertId
// followed by a SELECT to retrieve the full stored row.
func (s *Store) InsertEntityLink(ctx context.Context, l EntityLink) (EntityLink, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO entity_links (from_entity, from_type, to_entity, to_type, relationship, evidence_event_id)
		VALUES (?, ?, ?, ?, ?, ?)
	`, l.FromEntity, l.FromType, l.ToEntity, l.ToType, l.Relationship, l.EvidenceEventID)
	if err != nil {
		return EntityLink{}, fmt.Errorf("inserting entity link %s/%d -> %s/%d: %w",
			l.FromType, l.FromEntity, l.ToType, l.ToEntity, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return EntityLink{}, fmt.Errorf("getting entity link id: %w", err)
	}

	var result EntityLink
	var discoveredAt string
	err = s.db.QueryRowContext(ctx, `
		SELECT id, from_entity, from_type, to_entity, to_type, relationship, evidence_event_id, discovered_at
		FROM entity_links WHERE id = ?
	`, id).Scan(
		&result.ID, &result.FromEntity, &result.FromType,
		&result.ToEntity, &result.ToType, &result.Relationship,
		&result.EvidenceEventID, &discoveredAt,
	)
	if err != nil {
		return EntityLink{}, fmt.Errorf("fetching inserted entity link %d: %w", id, err)
	}
	result.DiscoveredAt, _ = scanTime(discoveredAt)
	return result, nil
}

// GetEntityLinks returns all links where the given entity appears as either the
// source (from) or target (to) of the relationship.
func (s *Store) GetEntityLinks(ctx context.Context, entityID int, entityType string) ([]EntityLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, from_entity, from_type, to_entity, to_type, relationship, evidence_event_id, discovered_at
		FROM entity_links
		WHERE (from_entity = ? AND from_type = ?) OR (to_entity = ? AND to_type = ?)
		ORDER BY discovered_at DESC
	`, entityID, entityType, entityID, entityType)
	if err != nil {
		return nil, fmt.Errorf("getting entity links for %s/%d: %w", entityType, entityID, err)
	}
	defer rows.Close()

	links := make([]EntityLink, 0)
	for rows.Next() {
		var l EntityLink
		var discoveredAt string
		if err := rows.Scan(
			&l.ID, &l.FromEntity, &l.FromType,
			&l.ToEntity, &l.ToType, &l.Relationship,
			&l.EvidenceEventID, &discoveredAt,
		); err != nil {
			return nil, fmt.Errorf("scanning entity link: %w", err)
		}
		l.DiscoveredAt, _ = scanTime(discoveredAt)
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating entity links: %w", err)
	}
	return links, nil
}

// InsertEntityAlias persists a new alias. The SQLite unique index on
// (entity_type, alias COLLATE NOCASE) enforces case-insensitive uniqueness.
// INSERT OR IGNORE is used so that a duplicate silently does nothing.
// When 0 rows are affected (duplicate), the zero EntityAlias is returned
// with a nil error. Callers that need the existing row's ID must fetch it
// separately via GetEntityAliases.
func (s *Store) InsertEntityAlias(ctx context.Context, a EntityAlias) (EntityAlias, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO entity_aliases (entity_id, entity_type, alias, source, confidence)
		VALUES (?, ?, ?, ?, ?)
	`, a.EntityID, a.EntityType, a.Alias, a.Source, a.Confidence)
	if err != nil {
		return EntityAlias{}, fmt.Errorf("inserting entity alias %q for %s/%d: %w",
			a.Alias, a.EntityType, a.EntityID, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return EntityAlias{}, fmt.Errorf("checking rows affected for alias %q: %w", a.Alias, err)
	}
	if affected == 0 {
		// Duplicate — silently ignored.
		return EntityAlias{}, nil
	}

	id, err := res.LastInsertId()
	if err != nil {
		return EntityAlias{}, fmt.Errorf("getting entity alias id: %w", err)
	}

	var result EntityAlias
	err = s.db.QueryRowContext(ctx, `
		SELECT id, entity_id, entity_type, alias, source, confidence
		FROM entity_aliases WHERE id = ?
	`, id).Scan(
		&result.ID, &result.EntityID, &result.EntityType,
		&result.Alias, &result.Source, &result.Confidence,
	)
	if err != nil {
		return EntityAlias{}, fmt.Errorf("fetching inserted entity alias %d: %w", id, err)
	}
	return result, nil
}

// GetEntityAliases returns all aliases registered for the given entity.
func (s *Store) GetEntityAliases(ctx context.Context, entityID int, entityType string) ([]EntityAlias, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, entity_id, entity_type, alias, source, confidence
		FROM entity_aliases
		WHERE entity_id = ? AND entity_type = ?
		ORDER BY alias
	`, entityID, entityType)
	if err != nil {
		return nil, fmt.Errorf("getting entity aliases for %s/%d: %w", entityType, entityID, err)
	}
	defer rows.Close()

	aliases := make([]EntityAlias, 0)
	for rows.Next() {
		var a EntityAlias
		if err := rows.Scan(
			&a.ID, &a.EntityID, &a.EntityType,
			&a.Alias, &a.Source, &a.Confidence,
		); err != nil {
			return nil, fmt.Errorf("scanning entity alias: %w", err)
		}
		aliases = append(aliases, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating entity aliases: %w", err)
	}
	return aliases, nil
}

// GetCompanyByAlias looks up a company via the entity_aliases table using a
// case-insensitive alias match (COLLATE NOCASE). Returns a wrapped
// sql.ErrNoRows when no matching alias exists.
func (s *Store) GetCompanyByAlias(ctx context.Context, alias string) (CompanyLookup, error) {
	var c CompanyLookup
	err := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.ticker, c.name
		FROM entity_aliases ea
		JOIN companies c ON c.id = ea.entity_id
		WHERE ea.entity_type = 'company' AND ea.alias = ? COLLATE NOCASE
		LIMIT 1
	`, alias).Scan(&c.ID, &c.Ticker, &c.Name)
	if err != nil {
		if err == sql.ErrNoRows {
			return CompanyLookup{}, fmt.Errorf("getting company by alias %q: %w", alias, err)
		}
		return CompanyLookup{}, fmt.Errorf("getting company by alias %q: %w", alias, err)
	}
	return c, nil
}
