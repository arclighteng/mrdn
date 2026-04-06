package api_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScoreHeatmap_RouteExists verifies that GET /api/v1/scores/heatmap is
// registered and returns a well-formed JSON envelope.  The handler has no
// path or query parameters to validate, so this is purely a route-existence
// and response-shape test.
func TestScoreHeatmap_RouteExists(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/scores/heatmap", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// The envelope must have a "data" key; it may be an empty array when the
	// database has no scored companies, but it must not be absent.
	assert.Contains(t, resp, "data", "response envelope must include 'data' key")
}
