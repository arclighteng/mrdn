package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidTicker(t *testing.T) {
	tests := []struct {
		ticker string
		valid  bool
	}{
		// Valid tickers — 1 to 5 uppercase letters
		{"A", true},
		{"AAPL", true},
		{"GOOG", true},
		{"GOOGL", true},
		{"BRK", true},

		// Invalid tickers
		{"", false},         // empty
		{"aapl", false},     // lowercase
		{"Aapl", false},     // mixed case
		{"TOOLONG", false},  // 7 chars
		{"SIXXX", true},     // 5 chars — still valid (boundary)
		{"SIXXXX", false},   // 6 chars — exceeds max
		{"AA1", false},      // digit in ticker
		{"AA-B", false},     // dash not allowed
		{"AA B", false},     // space not allowed
		{strings.Repeat("A", 5), true},  // exactly 5 — valid boundary
		{strings.Repeat("A", 6), false}, // 6 — invalid boundary
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.ticker, func(t *testing.T) {
			assert.Equal(t, tt.valid, validTicker(tt.ticker), "validTicker(%q)", tt.ticker)
		})
	}
}

// TestValidSlugForConnections exercises the slug validator from the perspective
// of the connections/person/{slug} handler.  The validator itself is shared with
// persons.go; these cases focus on the boundary inputs most likely to be
// supplied via a URL path segment.
func TestValidSlugForConnections(t *testing.T) {
	tests := []struct {
		name  string
		slug  string
		valid bool
	}{
		{"typical-politician", "nancy-pelosi", true},
		{"single-char", "a", true},
		{"alphanumeric-suffix", "romney-2", true},
		{"empty", "", false},
		{"leading-dash", "-bad", false},
		{"digit-start", "1digit", false},
		{"uppercase", "CamelCase", false},
		{"path-traversal", "../etc/passwd", false},
		{"spaces", "first last", false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, validSlug(tt.slug), "validSlug(%q)", tt.slug)
		})
	}
}
