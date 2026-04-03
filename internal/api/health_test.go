package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arclighteng/mrdn/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestHealthEndpoint(t *testing.T) {
	srv := api.NewServer(nil) // nil store — health doesn't need DB
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "ok")
}
