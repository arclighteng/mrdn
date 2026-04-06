package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildPersonWhere_NoFilters(t *testing.T) {
	f := PersonFilter{}
	conditions, args, argN := buildPersonWhere(f)
	assert.Equal(t, "WHERE 1=1", conditions)
	assert.Empty(t, args)
	assert.Equal(t, 1, argN)
}

func TestBuildPersonWhere_TierOnly(t *testing.T) {
	tier := 1
	f := PersonFilter{Tier: &tier}
	conditions, args, argN := buildPersonWhere(f)
	assert.Contains(t, conditions, "p.tier = $1")
	assert.Equal(t, []any{1}, args)
	assert.Equal(t, 2, argN)
}

func TestBuildPersonWhere_AllFilters(t *testing.T) {
	tier := 2
	f := PersonFilter{
		Tier:   &tier,
		Branch: "legislative",
		Role:   "senator",
		State:  "TX",
		Party:  "R",
	}
	conditions, args, argN := buildPersonWhere(f)
	// Five filter fields applied; argN starts at 1 and advances once per field.
	assert.Equal(t, 6, argN)
	assert.Len(t, args, 5)
	assert.Contains(t, conditions, "p.tier = $1")
	assert.Contains(t, conditions, "p.branch = $2")
	assert.Contains(t, conditions, "p.role = $3")
	assert.Contains(t, conditions, "p.state = $4")
	assert.Contains(t, conditions, "p.party = $5")
}

func TestBuildPersonWhere_BranchOnly(t *testing.T) {
	f := PersonFilter{Branch: "executive"}
	conditions, args, argN := buildPersonWhere(f)
	assert.Contains(t, conditions, "p.branch = $1")
	assert.Equal(t, []any{"executive"}, args)
	assert.Equal(t, 2, argN)
}

func TestBuildPersonWhere_RoleOnly(t *testing.T) {
	f := PersonFilter{Role: "senator"}
	conditions, args, _ := buildPersonWhere(f)
	assert.Contains(t, conditions, "p.role = $1")
	assert.Equal(t, []any{"senator"}, args)
}

func TestBuildPersonWhere_StateAndParty(t *testing.T) {
	f := PersonFilter{State: "CA", Party: "D"}
	conditions, args, argN := buildPersonWhere(f)
	assert.Contains(t, conditions, "p.state = $1")
	assert.Contains(t, conditions, "p.party = $2")
	assert.Len(t, args, 2)
	assert.Equal(t, 3, argN)
}

func TestBuildPersonWhere_TierZeroIsValid(t *testing.T) {
	// A pointer to tier 0 must still produce a filter — nil vs zero-value distinction.
	tier := 0
	f := PersonFilter{Tier: &tier}
	conditions, args, _ := buildPersonWhere(f)
	assert.Contains(t, conditions, "p.tier = $1")
	assert.Equal(t, []any{0}, args)
}

func TestBuildPersonWhere_NilTierExcluded(t *testing.T) {
	// Explicitly confirm that a nil Tier pointer is not included in the WHERE clause.
	f := PersonFilter{Branch: "judicial"}
	conditions, _, argN := buildPersonWhere(f)
	assert.NotContains(t, conditions, "p.tier")
	assert.Equal(t, 2, argN)
}

func TestBuildPersonWhere_ConditionsStartWithWhere(t *testing.T) {
	// The WHERE fragment must always begin with "WHERE 1=1" regardless of filters.
	tier := 3
	f := PersonFilter{Tier: &tier, Role: "representative"}
	conditions, _, _ := buildPersonWhere(f)
	assert.True(t, len(conditions) >= len("WHERE 1=1"))
	assert.Equal(t, "WHERE 1=1", conditions[:len("WHERE 1=1")])
}

func TestBuildPersonWhere_OrderOfClauses(t *testing.T) {
	// Verify that the fixed field ordering (tier, branch, role, state, party) is
	// preserved so that placeholder numbers align correctly with the args slice.
	tier := 1
	f := PersonFilter{Tier: &tier, Branch: "legislative", Role: "senator", State: "TX", Party: "R"}
	_, args, _ := buildPersonWhere(f)

	assert.Equal(t, 1, args[0])            // tier
	assert.Equal(t, "legislative", args[1]) // branch
	assert.Equal(t, "senator", args[2])     // role
	assert.Equal(t, "TX", args[3])          // state
	assert.Equal(t, "R", args[4])           // party
}
