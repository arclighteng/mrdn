package db_test

import (
	"context"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertAndGetLatestScore(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	c, err := store.UpsertCompany(ctx, db.Company{Ticker: "SCR1", Name: "Score Test", Sector: db.StrPtr("Technology")})
	require.NoError(t, err)
	defer store.DeleteCompany(ctx, c.ID)

	err = store.InsertScore(ctx, db.Score{
		CompanyID:      c.ID,
		MarketScore:    50.0,
		PolicyScore:    75.0,
		InsiderScore:   30.0,
		CompositeScore: 55.0,
		WeightVersion:  1,
	})
	require.NoError(t, err)

	score, err := store.GetLatestScore(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, 50.0, score.MarketScore)
	assert.Equal(t, 75.0, score.PolicyScore)
	assert.Equal(t, 55.0, score.CompositeScore)
}

func TestGetScoreRankings(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	c1, _ := store.UpsertCompany(ctx, db.Company{Ticker: "RNK1", Name: "Rank One", Sector: db.StrPtr("TestSector_Rank")})
	c2, _ := store.UpsertCompany(ctx, db.Company{Ticker: "RNK2", Name: "Rank Two", Sector: db.StrPtr("TestSector_Rank")})
	defer store.DeleteCompany(ctx, c1.ID)
	defer store.DeleteCompany(ctx, c2.ID)

	store.InsertScore(ctx, db.Score{CompanyID: c1.ID, CompositeScore: 80.0, WeightVersion: 1})
	store.InsertScore(ctx, db.Score{CompanyID: c2.ID, CompositeScore: 60.0, WeightVersion: 1})

	rankings, err := store.GetScoreRankings(ctx, 100)
	require.NoError(t, err)

	var rnk1Idx, rnk2Idx int
	rnk1Idx, rnk2Idx = -1, -1
	for i, r := range rankings {
		if r.Ticker == "RNK1" {
			rnk1Idx = i
		}
		if r.Ticker == "RNK2" {
			rnk2Idx = i
		}
	}
	require.NotEqual(t, -1, rnk1Idx, "RNK1 not found in rankings")
	require.NotEqual(t, -1, rnk2Idx, "RNK2 not found in rankings")
	assert.Less(t, rnk1Idx, rnk2Idx, "RNK1 (80) should rank higher than RNK2 (60)")
}

func TestGetScoreMovers(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Create a company with two scores to guarantee a mover
	c, err := store.UpsertCompany(ctx, db.Company{Ticker: "MVR1", Name: "Mover Test", Sector: db.StrPtr("TestSector_Mover")})
	require.NoError(t, err)
	defer store.DeleteCompany(ctx, c.ID)

	require.NoError(t, store.InsertScore(ctx, db.Score{CompanyID: c.ID, CompositeScore: 40.0, WeightVersion: 1}))
	require.NoError(t, store.InsertScore(ctx, db.Score{CompanyID: c.ID, CompositeScore: 70.0, WeightVersion: 1}))

	movers, err := store.GetScoreMovers(ctx, 24, 20)
	require.NoError(t, err)
	assert.NotNil(t, movers)

	// Find our test mover
	var found bool
	for _, m := range movers {
		if m.Ticker == "MVR1" {
			found = true
			assert.InDelta(t, 30.0, m.AbsChange, 0.1)
			assert.InDelta(t, 70.0, m.CurrentScore, 0.1)
			assert.InDelta(t, 40.0, m.PreviousScore, 0.1)
		}
	}
	assert.True(t, found, "MVR1 should appear in movers")
}
