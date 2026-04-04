package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupDSN returns the DATABASE_URL env var, skipping the test if it is not set.
func setupDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	return dsn
}

func setupTestDB(t *testing.T) *db.Store {
	t.Helper()
	dsn := setupDSN(t)
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, pool))
	t.Cleanup(func() { pool.Close() })
	return db.NewStore(pool)
}

func TestUpsertCompany(t *testing.T) {
	store := setupTestDB(t)
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

	// Clean up
	store.DeleteCompany(ctx, c.ID)
}

func TestGetCompanyByTicker(t *testing.T) {
	store := setupTestDB(t)
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

	store.DeleteCompany(ctx, found.ID)
}

func TestListCompanies(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	testSector := "TestSector_List"
	_, err := store.UpsertCompany(ctx, db.Company{Ticker: "LST1", Name: "List One", Sector: db.StrPtr(testSector)})
	require.NoError(t, err)
	_, err = store.UpsertCompany(ctx, db.Company{Ticker: "LST2", Name: "List Two", Sector: db.StrPtr(testSector)})
	require.NoError(t, err)

	companies, err := store.ListCompanies(ctx, db.CompanyFilter{Sector: testSector, Limit: 10})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(companies), 2)

	// Clean up
	for _, c := range companies {
		if c.Ticker == "LST1" || c.Ticker == "LST2" {
			store.DeleteCompany(ctx, c.ID)
		}
	}
}

func TestCountCompanies(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Use existing seeded companies
	count, err := store.CountCompanies(ctx, db.CompanyFilter{Sector: "Technology"})
	require.NoError(t, err)
	assert.Greater(t, count, 0)

	// Non-existent sector
	count, err = store.CountCompanies(ctx, db.CompanyFilter{Sector: "NonExistent_XYZ"})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestListCompanies_TickerFilter(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	companies, err := store.ListCompanies(ctx, db.CompanyFilter{Ticker: "NVD", Limit: 10})
	require.NoError(t, err)
	assert.Greater(t, len(companies), 0)
	assert.Equal(t, "NVDA", companies[0].Ticker)
}
