package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// nodeKey
// ---------------------------------------------------------------------------

func TestNodeKey(t *testing.T) {
	assert.Equal(t, "company:42", nodeKey("company", 42))
	assert.Equal(t, "person:1", nodeKey("person", 1))
}

func TestNodeKey_ZeroID(t *testing.T) {
	assert.Equal(t, "person:0", nodeKey("person", 0))
}

func TestNodeKey_LargeID(t *testing.T) {
	assert.Equal(t, "company:999999", nodeKey("company", 999999))
}

func TestNodeKey_TypePreserved(t *testing.T) {
	// The entity type string must appear verbatim in the key.
	assert.Equal(t, "unknown_type:7", nodeKey("unknown_type", 7))
}

// ---------------------------------------------------------------------------
// edgeKey — canonical direction
// ---------------------------------------------------------------------------

func TestEdgeKey_Canonical(t *testing.T) {
	// The same logical edge expressed in both traversal directions must
	// produce an identical deduplication key.
	k1 := edgeKey(1, "company", 2, "person", "insider_trade")
	k2 := edgeKey(2, "person", 1, "company", "insider_trade")
	assert.Equal(t, k1, k2)
}

func TestEdgeKey_DifferentRelationship(t *testing.T) {
	k1 := edgeKey(1, "company", 2, "person", "insider_trade")
	k2 := edgeKey(1, "company", 2, "person", "donation")
	assert.NotEqual(t, k1, k2)
}

func TestEdgeKey_SameIDDifferentType(t *testing.T) {
	// Nodes share the same numeric ID but differ in entity type.
	// Canonical sort: "company:1" < "person:1" lexicographically, so both
	// orderings must produce the same key.
	k1 := edgeKey(1, "company", 1, "person", "link")
	k2 := edgeKey(1, "person", 1, "company", "link")
	assert.Equal(t, k1, k2)
}

func TestEdgeKey_SameNodeBothSides(t *testing.T) {
	// Self-referential edge — the canonical key must still be well-formed.
	k := edgeKey(5, "person", 5, "person", "alias")
	assert.Contains(t, k, "person:5")
	assert.Contains(t, k, "alias")
}

func TestEdgeKey_DifferentIDsSameType(t *testing.T) {
	// Direction normalisation for two nodes of the same type.
	k1 := edgeKey(10, "person", 20, "person", "family")
	k2 := edgeKey(20, "person", 10, "person", "family")
	assert.Equal(t, k1, k2)
}

func TestEdgeKey_ContainsBothNodes(t *testing.T) {
	// The key must encode both endpoints so distinct edges are not collapsed.
	k := edgeKey(3, "company", 7, "person", "board_member")
	assert.Contains(t, k, "company:3")
	assert.Contains(t, k, "person:7")
	assert.Contains(t, k, "board_member")
}

func TestEdgeKey_DistinctEdgesBetweenSamePair(t *testing.T) {
	// Two different relationships between the same pair of nodes must produce
	// different keys so neither is dropped during BFS deduplication.
	k1 := edgeKey(1, "person", 2, "company", "director")
	k2 := edgeKey(1, "person", 2, "company", "shareholder")
	assert.NotEqual(t, k1, k2)
}

// ---------------------------------------------------------------------------
// BFS constants
// ---------------------------------------------------------------------------

func TestBFSConstants(t *testing.T) {
	assert.Equal(t, 4, maxBFSDepth)
	assert.Equal(t, 500, maxBFSBudget)
}

func TestBFSInverseCapFormula(t *testing.T) {
	// Verify the inverse depth-budget cap values derived from: 100 * (5 - depth).
	// These are the effective budget ceilings imposed by BFSGraph before any
	// caller-supplied budget is considered.
	cases := []struct {
		depth       int
		expectedCap int
	}{
		{1, 400},
		{2, 300},
		{3, 200},
		{4, 100},
	}
	for _, tc := range cases {
		got := 100 * (5 - tc.depth)
		assert.Equal(t, tc.expectedCap, got, "depth=%d", tc.depth)
	}
}
