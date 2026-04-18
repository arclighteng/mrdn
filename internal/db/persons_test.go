package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildPersonWhere_NoFilters(t *testing.T) {
	f := PersonFilter{}
	conditions, args := buildPersonWhere(f)
	assert.Equal(t, "WHERE 1=1", conditions)
	assert.Empty(t, args)
}

func TestBuildPersonWhere_TierOnly(t *testing.T) {
	tier := 1
	f := PersonFilter{Tier: &tier}
	conditions, args := buildPersonWhere(f)
	assert.Contains(t, conditions, "p.tier = ?")
	assert.Equal(t, []any{1}, args)
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
	conditions, args := buildPersonWhere(f)
	assert.Len(t, args, 5)
	assert.Contains(t, conditions, "p.tier = ?")
	assert.Contains(t, conditions, "p.branch = ?")
	assert.Contains(t, conditions, "p.role = ?")
	assert.Contains(t, conditions, "p.state = ?")
	assert.Contains(t, conditions, "p.party = ?")
}

func TestBuildPersonWhere_BranchOnly(t *testing.T) {
	f := PersonFilter{Branch: "executive"}
	conditions, args := buildPersonWhere(f)
	assert.Contains(t, conditions, "p.branch = ?")
	assert.Equal(t, []any{"executive"}, args)
}

func TestBuildPersonWhere_RoleOnly(t *testing.T) {
	f := PersonFilter{Role: "senator"}
	conditions, args := buildPersonWhere(f)
	assert.Contains(t, conditions, "p.role = ?")
	assert.Equal(t, []any{"senator"}, args)
}

func TestBuildPersonWhere_StateAndParty(t *testing.T) {
	f := PersonFilter{State: "CA", Party: "D"}
	conditions, args := buildPersonWhere(f)
	assert.Contains(t, conditions, "p.state = ?")
	assert.Contains(t, conditions, "p.party = ?")
	assert.Len(t, args, 2)
}

func TestBuildPersonWhere_TierZeroIsValid(t *testing.T) {
	tier := 0
	f := PersonFilter{Tier: &tier}
	conditions, args := buildPersonWhere(f)
	assert.Contains(t, conditions, "p.tier = ?")
	assert.Equal(t, []any{0}, args)
}

func TestBuildPersonWhere_NilTierExcluded(t *testing.T) {
	f := PersonFilter{Branch: "judicial"}
	conditions, _ := buildPersonWhere(f)
	assert.NotContains(t, conditions, "p.tier")
}

func TestBuildPersonWhere_ConditionsStartWithWhere(t *testing.T) {
	tier := 3
	f := PersonFilter{Tier: &tier, Role: "representative"}
	conditions, _ := buildPersonWhere(f)
	assert.True(t, len(conditions) >= len("WHERE 1=1"))
	assert.Equal(t, "WHERE 1=1", conditions[:len("WHERE 1=1")])
}

func TestBuildPersonWhere_OrderOfClauses(t *testing.T) {
	tier := 1
	f := PersonFilter{Tier: &tier, Branch: "legislative", Role: "senator", State: "TX", Party: "R"}
	_, args := buildPersonWhere(f)

	assert.Equal(t, 1, args[0])            // tier
	assert.Equal(t, "legislative", args[1]) // branch
	assert.Equal(t, "senator", args[2])     // role
	assert.Equal(t, "TX", args[3])          // state
	assert.Equal(t, "R", args[4])           // party
}
