package db

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	maxBFSDepth  = 4
	maxBFSBudget = 500

	// CoTraderMonthWindow is the rolling look-back period (in months) used when
	// detecting co-trading pairs. A 24-month window captures two full congressional
	// sessions, balancing recency against sample size.
	CoTraderMonthWindow = 24
)

// GraphNode is a single vertex in the entity relationship graph.
// It represents either a company or a person resolved from the database.
type GraphNode struct {
	ID     int    `json:"id"`
	Type   string `json:"type"`             // "company" or "person"
	Label  string `json:"label"`            // display name
	Ticker string `json:"ticker,omitempty"` // companies only
	Slug   string `json:"slug,omitempty"`   // persons only
}

// GraphEdge is a directed relationship between two graph nodes.
// From/To refer to node IDs within the accompanying GraphResult.Nodes slice.
type GraphEdge struct {
	From         int    `json:"from"`
	FromType     string `json:"from_type"`
	To           int    `json:"to"`
	ToType       string `json:"to_type"`
	Relationship string `json:"relationship"`
}

// GraphResult is the complete BFS output: a deduplicated set of nodes and edges.
type GraphResult struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// BFSGraph performs a breadth-first traversal of the entity_links graph starting
// from the given seed node. It expands up to depth BFS levels and stops after
// budget nodes have been collected (exclusive of already-visited nodes).
//
// Depth is clamped to [1, maxBFSDepth]. Budget is clamped to [1, maxBFSBudget]
// and further reduced by the inverse depth-budget formula: min(budget, 100*(5-depth)).
//
// A 5-second context timeout is applied as defense-in-depth on top of any timeout
// already present in ctx.
func (s *Store) BFSGraph(ctx context.Context, seedID int, seedType string, depth, budget int) (*GraphResult, error) {
	// Clamp depth.
	if depth < 1 {
		depth = 1
	}
	if depth > maxBFSDepth {
		depth = maxBFSDepth
	}

	// Clamp budget, then apply inverse depth-budget formula.
	if budget < 1 {
		budget = 200
	}
	if budget > maxBFSBudget {
		budget = maxBFSBudget
	}
	inverseCap := 100 * (5 - depth)
	if budget > inverseCap {
		budget = inverseCap
	}

	// Defense-in-depth timeout — guards against slow index scans when entity_links
	// grows large. The caller's context may already carry a tighter deadline.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// BFS state.
	type frontierNode struct {
		id  int
		typ string
	}

	// visited tracks nodes that have been added to result.Nodes, keyed as "type:id".
	visited := make(map[string]bool)

	// edgeSeen prevents duplicate edges in the output, keyed as
	// "fromType:from->toType:to:relationship".
	edgeSeen := make(map[string]bool)

	result := &GraphResult{
		Nodes: make([]GraphNode, 0),
		Edges: make([]GraphEdge, 0),
	}

	// Resolve and enqueue the seed node.
	seedNode, err := s.resolveNode(ctx, seedID, seedType)
	if err != nil {
		return nil, fmt.Errorf("resolving seed node: %w", err)
	}

	seedKey := nodeKey(seedType, seedID)
	visited[seedKey] = true
	result.Nodes = append(result.Nodes, seedNode)

	frontier := []frontierNode{{seedID, seedType}}

	// BFS loop — one level per iteration.
	for level := 0; level < depth && len(frontier) > 0; level++ {
		var nextFrontier []frontierNode

		for _, cur := range frontier {
			if len(result.Nodes) >= budget {
				break
			}

			edges, err := s.getEdgesFrom(ctx, cur.id, cur.typ)
			if err != nil {
				return nil, fmt.Errorf("BFS level %d: %w", level, err)
			}

			for _, e := range edges {
				// Determine which side of the link is the neighbour.
				neighborID, neighborType := e.ToEntity, e.ToType
				if e.ToEntity == cur.id && e.ToType == cur.typ {
					neighborID, neighborType = e.FromEntity, e.FromType
				}

				// Self-referential link: both sides resolve to the same node.
				// The visited check below will catch this, but be explicit.
				if neighborID == cur.id && neighborType == cur.typ {
					continue
				}

				ek := edgeKey(cur.id, cur.typ, neighborID, neighborType, e.Relationship)

				neighborNodeKey := nodeKey(neighborType, neighborID)
				if visited[neighborNodeKey] {
					// Both endpoints are in the graph — record the cross-link edge
					// (if we have not seen this edge before).
					if !edgeSeen[ek] {
						edgeSeen[ek] = true
						result.Edges = append(result.Edges, GraphEdge{
							From:         cur.id,
							FromType:     cur.typ,
							To:           neighborID,
							ToType:       neighborType,
							Relationship: e.Relationship,
						})
					}
					continue
				}

				if len(result.Nodes) >= budget {
					break
				}

				// Try to resolve the neighbour. Skip (no edge) if unresolvable.
				neighborNode, err := s.resolveNode(ctx, neighborID, neighborType)
				if err != nil {
					continue
				}

				// CRITICAL: only commit the node and edge together, after a
				// successful resolution. This maintains edge-node integrity.
				visited[neighborNodeKey] = true
				result.Nodes = append(result.Nodes, neighborNode)

				if !edgeSeen[ek] {
					edgeSeen[ek] = true
					result.Edges = append(result.Edges, GraphEdge{
						From:         cur.id,
						FromType:     cur.typ,
						To:           neighborID,
						ToType:       neighborType,
						Relationship: e.Relationship,
					})
				}

				nextFrontier = append(nextFrontier, frontierNode{neighborID, neighborType})
			}
		}

		frontier = nextFrontier
	}

	return result, nil
}

// resolveNode looks up the display label for a node from its home table.
// It returns a GraphNode with ID, Type, Label, and the type-specific field
// (Ticker for companies, Slug for persons).
func (s *Store) resolveNode(ctx context.Context, id int, entityType string) (GraphNode, error) {
	node := GraphNode{ID: id, Type: entityType}
	switch entityType {
	case "company":
		err := s.db.QueryRowContext(ctx,
			`SELECT name, ticker FROM companies WHERE id = ?`, id,
		).Scan(&node.Label, &node.Ticker)
		if err != nil {
			return GraphNode{}, fmt.Errorf("resolving company %d: %w", id, err)
		}
	case "person":
		err := s.db.QueryRowContext(ctx,
			`SELECT name, slug FROM persons WHERE id = ?`, id,
		).Scan(&node.Label, &node.Slug)
		if err != nil {
			return GraphNode{}, fmt.Errorf("resolving person %d: %w", id, err)
		}
	default:
		return GraphNode{}, fmt.Errorf("unknown entity type: %s", entityType)
	}
	return node, nil
}

// getEdgesFrom returns all entity_links rows where the given entity appears on
// either side of the link. It uses the two composite indexes
// (from_entity, from_type) and (to_entity, to_type) via an OR predicate.
func (s *Store) getEdgesFrom(ctx context.Context, entityID int, entityType string) ([]EntityLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, from_entity, from_type, to_entity, to_type,
		       relationship, evidence_event_id, discovered_at
		FROM entity_links
		WHERE (from_entity = ? AND from_type = ?)
		   OR (to_entity   = ? AND to_type   = ?)
	`, entityID, entityType, entityID, entityType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []EntityLink
	for rows.Next() {
		var l EntityLink
		var discoveredAt string
		if err := rows.Scan(
			&l.ID, &l.FromEntity, &l.FromType,
			&l.ToEntity, &l.ToType,
			&l.Relationship, &l.EvidenceEventID, &discoveredAt,
		); err != nil {
			return nil, err
		}
		l.DiscoveredAt, _ = scanTime(discoveredAt)
		links = append(links, l)
	}
	return links, rows.Err()
}

// nodeKey returns the visited-map key for a graph node.
func nodeKey(entityType string, id int) string {
	return fmt.Sprintf("%s:%d", entityType, id)
}

// edgeKey returns a canonical deduplication key for an undirected edge.
// It normalises direction so that (A->B) and (B->A) with the same relationship
// produce the same key, preventing duplicate edges when a node appears on
// both sides of a link during traversal.
func edgeKey(fromID int, fromType string, toID int, toType string, relationship string) string {
	// Canonical form: smaller node key first.
	a := nodeKey(fromType, fromID)
	b := nodeKey(toType, toID)
	if a > b {
		a, b = b, a
	}
	return fmt.Sprintf("%s<->%s:%s", a, b, relationship)
}

// CoTraderNetwork finds pairs of persons who traded the same ticker within 14
// days of each other over the past 24 months, groups them by person pair, and
// counts the number of distinct shared tickers as edge weight. Only pairs with
// at least minOverlaps shared tickers are included.
//
// Node hydration uses a single batched IN query rather than per-node lookups.
// A 10-second context timeout is applied as a guard against slow self-joins.
func (s *Store) CoTraderNetwork(ctx context.Context, minOverlaps int) (*GraphResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT a.person_id, b.person_id, COUNT(DISTINCT a.ticker) AS shared_tickers
		FROM congressional_trades a
		JOIN congressional_trades b
		    ON a.ticker = b.ticker
		   AND a.person_id < b.person_id
		   AND ABS(julianday(a.traded_at) - julianday(b.traded_at)) <= 14
		WHERE a.traded_at >= date('now', '-%d months')
		  AND b.traded_at >= date('now', '-%d months')
		  AND a.traded_at IS NOT NULL
		  AND b.traded_at IS NOT NULL
		GROUP BY a.person_id, b.person_id
		HAVING shared_tickers >= ?
		ORDER BY shared_tickers DESC
		LIMIT 500
	`, CoTraderMonthWindow, CoTraderMonthWindow), minOverlaps)
	if err != nil {
		return nil, fmt.Errorf("co-trader network query: %w", err)
	}
	defer rows.Close()

	type rawEdge struct {
		fromID int
		toID   int
		weight int
	}
	var rawEdges []rawEdge
	idSet := make(map[int]bool)

	for rows.Next() {
		var e rawEdge
		if err := rows.Scan(&e.fromID, &e.toID, &e.weight); err != nil {
			return nil, fmt.Errorf("co-trader network scan: %w", err)
		}
		rawEdges = append(rawEdges, e)
		idSet[e.fromID] = true
		idSet[e.toID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("co-trader network rows: %w", err)
	}

	if len(idSet) == 0 {
		return &GraphResult{
			Nodes: []GraphNode{},
			Edges: []GraphEdge{},
		}, nil
	}

	// Batch-hydrate all person nodes in a single query.
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	inClause := strings.Join(placeholders, ", ")

	pRows, err := s.db.QueryContext(ctx,
		`SELECT id, name, slug, party FROM persons WHERE id IN (`+inClause+`)`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("co-trader network person hydration: %w", err)
	}
	defer pRows.Close()

	nodeByID := make(map[int]GraphNode, len(ids))
	for pRows.Next() {
		var (
			id    int
			name  string
			slug  string
			party *string
		)
		if err := pRows.Scan(&id, &name, &slug, &party); err != nil {
			return nil, fmt.Errorf("co-trader network person scan: %w", err)
		}
		label := name
		if party != nil && *party != "" {
			label = name + " (" + *party + ")"
		}
		nodeByID[id] = GraphNode{
			ID:    id,
			Type:  "person",
			Label: label,
			Slug:  slug,
		}
	}
	if err := pRows.Err(); err != nil {
		return nil, fmt.Errorf("co-trader network person rows: %w", err)
	}

	nodes := make([]GraphNode, 0, len(nodeByID))
	for _, n := range nodeByID {
		nodes = append(nodes, n)
	}

	edges := make([]GraphEdge, 0, len(rawEdges))
	for _, e := range rawEdges {
		edges = append(edges, GraphEdge{
			From:         e.fromID,
			FromType:     "person",
			To:           e.toID,
			ToType:       "person",
			Relationship: fmt.Sprintf("co_trader:%d", e.weight),
		})
	}

	return &GraphResult{Nodes: nodes, Edges: edges}, nil
}
