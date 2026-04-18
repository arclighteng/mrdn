package db_test

import (
	"context"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertCompany(t *testing.T) {
	store := setupTestTx(t)
	ctx := context.Background()

	c, err := store.UpsertCompany(ctx, db.Company{
		Ticker:    "TEST",
		Name:      "Test Corp",
		Sector:    db.StrPtr("Technology"),
		Subsector: db.StrPtr("Software"),
	})
	require.NoError(t, err)
	assert.Equal(t, "TEST", c.Ticker)
	assert.Greater(t, c.ID, 0)

	// Upsert again — should update, not duplicate
	c2, err := store.UpsertCompany(ctx, db.Company{
		Ticker: "TEST",
		Name:   "Test Corp Updated",
		Sector: db.StrPtr("Technology"),
	})
	require.NoError(t, err)
	assert.Equal(t, c.ID, c2.ID)
	assert.Equal(t, "Test Corp Updated", c2.Name)
	assert.Nil(t, c2.NAICSCode) // NULL columns scan correctly
}

func TestGetCompanyByTicker(t *testing.T) {
	store := setupTestTx(t)
	ctx := context.Background()

	_, err := store.UpsertCompany(ctx, db.Company{
		Ticker: "LOOK",
		Name:   "Lookup Corp",
		Sector: db.StrPtr("Technology"),
	})
	require.NoError(t, err)

	found, err := store.GetCompanyByTicker(ctx, "LOOK")
	require.NoError(t, err)
	assert.Equal(t, "Lookup Corp", found.Name)

	_, err = store.GetCompanyByTicker(ctx, "NOPE")
	assert.Error(t, err)
}

func TestListCompanies(t *testing.T) {
	store := setupTestTx(t)
	ctx := context.Background()

	testSector := "TestSector_List"
	_, err := store.UpsertCompany(ctx, db.Company{Ticker: "LST1", Name: "List One", Sector: db.StrPtr(testSector)})
	require.NoError(t, err)
	_, err = store.UpsertCompany(ctx, db.Company{Ticker: "LST2", Name: "List Two", Sector: db.StrPtr(testSector)})
	require.NoError(t, err)

	companies, err := store.ListCompanies(ctx, db.CompanyFilter{Sector: testSector, Limit: 10})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(companies), 2)
}

func TestCountCompanies(t *testing.T) {
	store := setupTestTx(t)
	ctx := context.Background()

	_, err := store.UpsertCompany(ctx, db.Company{
		Ticker: "CNT1",
		Name:   "Count Corp",
		Sector: db.StrPtr("CountSector"),
	})
	require.NoError(t, err)

	count, err := store.CountCompanies(ctx, db.CompanyFilter{Sector: "CountSector"})
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Non-existent sector
	count, err = store.CountCompanies(ctx, db.CompanyFilter{Sector: "NonExistent_XYZ"})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestListCompanies_TickerFilter(t *testing.T) {
	store := setupTestTx(t)
	ctx := context.Background()

	_, err := store.UpsertCompany(ctx, db.Company{
		Ticker: "NVDA",
		Name:   "NVIDIA Corp",
		Sector: db.StrPtr("Technology"),
	})
	require.NoError(t, err)

	companies, err := store.ListCompanies(ctx, db.CompanyFilter{Ticker: "NVD", Limit: 10})
	require.NoError(t, err)
	assert.Greater(t, len(companies), 0)
	assert.Equal(t, "NVDA", companies[0].Ticker)
}
