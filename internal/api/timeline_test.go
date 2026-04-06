package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompanyTimeline_InvalidTicker verifies that a ticker containing invalid
// characters is rejected with 400 before the handler touches the database.
func TestCompanyTimeline_InvalidTicker(t *testing.T) {
	srv, _ := setupTestServer(t)

	cases := []struct {
		name   string
		ticker string
	}{
		{"lowercase", "aapl"},
		{"mixed-case", "Aapl"},
		{"with-digit", "AA1"},
		{"too-long", "TOOLONG"},
		{"with-dash", "AA-B"},
		{"with-bang", "AA!"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/companies/"+tc.ticker+"/timeline", nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			assert.Equal(t, 400, w.Code, "expected 400 for ticker %q", tc.ticker)

			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, "BAD_REQUEST", body["code"])
		})
	}
}

// TestCompanyTimeline_ValidTicker_NotFound verifies that a well-formed ticker
// that does not exist in the database returns 404, confirming that valid-format
// tickers reach the store lookup.
func TestCompanyTimeline_ValidTicker_NotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/companies/ZZZZZ/timeline", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, 404, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "NOT_FOUND", body["code"])
}

// TestCompanyTimeline_InvalidLimitParam verifies that a non-integer limit
// parameter returns 400 after the ticker passes validation.
// A real company is seeded so the store lookup succeeds and the handler
// reaches parseInt for the limit parameter.
func TestCompanyTimeline_InvalidLimitParam(t *testing.T) {
	srv, store := setupTestServer(t)
	ctx := context.Background()

	_, err := store.UpsertCompany(ctx, db.Company{
		Ticker: "XTIM",
		Name:   "Timeline Limit Test",
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/v1/companies/XTIM/timeline?limit=abc", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "BAD_REQUEST", body["code"])
}
