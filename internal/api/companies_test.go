package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/arclighteng/mrdn/internal/api"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestServer(t *testing.T) (*api.Server, *db.Store) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, pool))
	t.Cleanup(func() { pool.Close() })
	store := db.NewStore(pool)
	srv := api.NewServer(store)
	t.Cleanup(func() { srv.Shutdown() })
	return srv, store
}

func TestListCompanies_200(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/companies?limit=5", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotNil(t, resp["data"])
	assert.NotNil(t, resp["pagination"])

	pagination := resp["pagination"].(map[string]any)
	assert.Equal(t, float64(5), pagination["limit"])
	assert.Equal(t, float64(0), pagination["offset"])
}

func TestGetCompany_200(t *testing.T) {
	srv, store := setupTestServer(t)
	ctx := context.Background()

	// Seed a company
	_, err := store.UpsertCompany(ctx, db.Company{Ticker: "XGET", Name: "Get Test", Sector: db.StrPtr("Technology")})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/v1/companies/XGET", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].(map[string]any)
	assert.Equal(t, "XGET", data["ticker"])
	assert.Equal(t, "Get Test", data["name"])
}

func TestGetCompany_404(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/companies/ZZZZZ", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, 404, w.Code)
}

func TestGetCompany_InvalidTicker(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/companies/invalid!", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
}

func TestCompanyScores_200(t *testing.T) {
	srv, store := setupTestServer(t)
	ctx := context.Background()

	c, err := store.UpsertCompany(ctx, db.Company{Ticker: "XSCR", Name: "Score Test", Sector: db.StrPtr("Technology")})
	require.NoError(t, err)
	require.NoError(t, store.InsertScore(ctx, db.Score{CompanyID: c.ID, CompositeScore: 75.0, WeightVersion: 1}))

	req := httptest.NewRequest("GET", "/api/v1/companies/XSCR/scores", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].([]any)
	assert.GreaterOrEqual(t, len(data), 1)
}

func TestCompanyEvents_200(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/companies/AAPL/events?limit=5", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// AAPL exists from seed data — should return 200 even if no events
	assert.Equal(t, 200, w.Code)
}

func TestHealthEndpoint_NotRateLimited(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Health should always be accessible even if rate limit is hit
	for i := 0; i < 65; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		req.RemoteAddr = "99.99.99.99:1234"
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		assert.Equal(t, 200, w.Code)
	}
}
