package api

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseInt(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		key     string
		def     int
		want    int
		wantErr bool
	}{
		{"default", "", "limit", 50, 50, false},
		{"valid", "limit=10", "limit", 50, 10, false},
		{"invalid", "limit=abc", "limit", 50, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/?"+tt.query, nil)
			got, err := parseInt(r, tt.key, tt.def)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseFloat(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		key     string
		def     float64
		want    float64
		wantErr bool
	}{
		{"default", "", "min_score", 0, 0, false},
		{"valid", "min_score=72.5", "min_score", 0, 72.5, false},
		{"invalid", "min_score=abc", "min_score", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/?"+tt.query, nil)
			got, err := parseFloat(r, tt.key, tt.def)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.InDelta(t, tt.want, got, 0.001)
			}
		})
	}
}

func TestParseTime(t *testing.T) {
	r := httptest.NewRequest("GET", "/?since=2026-04-01T00:00:00Z", nil)
	got, err := parseTime(r, "since")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 2026, got.Year())

	// Missing
	r = httptest.NewRequest("GET", "/", nil)
	got, err = parseTime(r, "since")
	require.NoError(t, err)
	assert.Nil(t, got)

	// Invalid
	r = httptest.NewRequest("GET", "/?since=not-a-date", nil)
	_, err = parseTime(r, "since")
	assert.Error(t, err)
}

func TestParseString(t *testing.T) {
	r := httptest.NewRequest("GET", "/?source=polygon", nil)
	assert.Equal(t, "polygon", parseString(r, "source", ""))

	r = httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, "default", parseString(r, "source", "default"))
}

func TestParsePagination(t *testing.T) {
	// Defaults
	r := httptest.NewRequest("GET", "/", nil)
	limit, offset, err := parsePagination(r)
	require.NoError(t, err)
	assert.Equal(t, 50, limit)
	assert.Equal(t, 0, offset)

	// Custom values
	r = httptest.NewRequest("GET", "/?limit=25&offset=10", nil)
	limit, offset, err = parsePagination(r)
	require.NoError(t, err)
	assert.Equal(t, 25, limit)
	assert.Equal(t, 10, offset)

	// Clamp limit too high
	r = httptest.NewRequest("GET", "/?limit=500", nil)
	limit, _, err = parsePagination(r)
	require.NoError(t, err)
	assert.Equal(t, 100, limit)

	// Clamp limit too low
	r = httptest.NewRequest("GET", "/?limit=-5", nil)
	limit, _, err = parsePagination(r)
	require.NoError(t, err)
	assert.Equal(t, 1, limit)

	// Clamp negative offset
	r = httptest.NewRequest("GET", "/?offset=-10", nil)
	_, offset, err = parsePagination(r)
	require.NoError(t, err)
	assert.Equal(t, 0, offset)

	// Invalid limit
	r = httptest.NewRequest("GET", "/?limit=abc", nil)
	_, _, err = parsePagination(r)
	assert.Error(t, err)
}
