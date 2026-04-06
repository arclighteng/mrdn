package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidSlug(t *testing.T) {
	tests := []struct {
		slug  string
		valid bool
	}{
		// Valid slugs
		{"nancy-pelosi", true},
		{"aoc", true},
		{"a", true},
		{"bernie-sanders", true},
		{"mitt-romney-2", true},
		{"z99", true},
		{"ab", true},
		// Exactly 81 characters (1 leading + 80 continuation) — boundary, valid
		{"a" + strings.Repeat("b", 80), true},

		// Invalid slugs
		{"", false},                              // empty
		{"Nancy-Pelosi", false},                  // uppercase letter
		{"ALLCAPS", false},                       // all uppercase
		{"-leading-dash", false},                 // starts with a dash
		{"9starts-with-number", false},           // starts with digit
		{"slug with spaces", false},              // contains spaces
		{"slug_underscore", false},               // underscore not allowed
		{"slug.dot", false},                      // dot not allowed
		{strings.Repeat("a", 82), false},         // 82 chars — exceeds max (regex allows [a-z][a-z0-9-]{0,80}, so max total is 81)
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.slug, func(t *testing.T) {
			assert.Equal(t, tt.valid, validSlug(tt.slug), "validSlug(%q)", tt.slug)
		})
	}
}
