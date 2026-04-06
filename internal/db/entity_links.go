package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
func (s *Store) InsertEntityLink(ctx context.Context, l EntityLink) (EntityLink, error) {
	var result EntityLink
	err := s.db.QueryRow(ctx, `
		INSERT INTO entity_links (from_entity, from_type, to_entity, to_type, relationship, evidence_event_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, from_entity, from_type, to_entity, to_type, relationship, evidence_event_id, discovered_at
	`, l.FromEntity, l.FromType, l.ToEntity, l.ToType, l.Relationship, l.EvidenceEventID,
	).Scan(
		&result.ID, &result.FromEntity, &result.FromType,
		&result.ToEntity, &result.ToType, &result.Relationship,
		&result.EvidenceEventID, &result.DiscoveredAt,
	)
	if err != nil {
		return EntityLink{}, fmt.Errorf("inserting entity link %s/%d -> %s/%d: %w",
			l.FromType, l.FromEntity, l.ToType, l.ToEntity, err)
	}
	return result, nil
}

// GetEntityLinks returns all links where the given entity appears as either the
// source (from) or target (to) of the relationship.
func (s *Store) GetEntityLinks(ctx context.Context, entityID int, entityType string) ([]EntityLink, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, from_entity, from_type, to_entity, to_type, relationship, evidence_event_id, discovered_at
		FROM entity_links
		WHERE (from_entity = $1 AND from_type = $2) OR (to_entity = $1 AND to_type = $2)
		ORDER BY discovered_at DESC
	`, entityID, entityType)
	if err != nil {
		return nil, fmt.Errorf("getting entity links for %s/%d: %w", entityType, entityID, err)
	}
	defer rows.Close()

	links := make([]EntityLink, 0)
	for rows.Next() {
		var l EntityLink
		if err := rows.Scan(
			&l.ID, &l.FromEntity, &l.FromType,
			&l.ToEntity, &l.ToType, &l.Relationship,
			&l.EvidenceEventID, &l.DiscoveredAt,
		); err != nil {
			return nil, fmt.Errorf("scanning entity link: %w", err)
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating entity links: %w", err)
	}
	return links, nil
}

// InsertEntityAlias persists a new alias. If the (entity_type, LOWER(alias))
// combination already exists the insert is silently ignored (DO NOTHING), and
// the function returns the zero EntityAlias with a nil error. Callers that need
// the existing row's ID must fetch it separately via GetEntityAliases.
func (s *Store) InsertEntityAlias(ctx context.Context, a EntityAlias) (EntityAlias, error) {
	var result EntityAlias
	err := s.db.QueryRow(ctx, `
		INSERT INTO entity_aliases (entity_id, entity_type, alias, source, confidence)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (entity_type, (LOWER(alias))) DO NOTHING
		RETURNING id, entity_id, entity_type, alias, source, confidence
	`, a.EntityID, a.EntityType, a.Alias, a.Source, a.Confidence,
	).Scan(
		&result.ID, &result.EntityID, &result.EntityType,
		&result.Alias, &result.Source, &result.Confidence,
	)
	if err != nil {
		// pgx returns ErrNoRows when ON CONFLICT DO NOTHING suppresses the insert.
		if err == pgx.ErrNoRows {
			return EntityAlias{}, nil
		}
		return EntityAlias{}, fmt.Errorf("inserting entity alias %q for %s/%d: %w",
			a.Alias, a.EntityType, a.EntityID, err)
	}
	return result, nil
}

// GetEntityAliases returns all aliases registered for the given entity.
func (s *Store) GetEntityAliases(ctx context.Context, entityID int, entityType string) ([]EntityAlias, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, entity_id, entity_type, alias, source, confidence
		FROM entity_aliases
		WHERE entity_id = $1 AND entity_type = $2
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
// case-insensitive alias match. Returns a wrapped pgx.ErrNoRows when no
// matching alias exists.
func (s *Store) GetCompanyByAlias(ctx context.Context, alias string) (CompanyLookup, error) {
	var c CompanyLookup
	err := s.db.QueryRow(ctx, `
		SELECT c.id, c.ticker, c.name
		FROM entity_aliases ea
		JOIN companies c ON c.id = ea.entity_id
		WHERE ea.entity_type = 'company' AND LOWER(ea.alias) = LOWER($1)
		LIMIT 1
	`, alias).Scan(&c.ID, &c.Ticker, &c.Name)
	if err != nil {
		return CompanyLookup{}, fmt.Errorf("getting company by alias %q: %w", alias, err)
	}
	return c, nil
}
