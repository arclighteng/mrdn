package jsonutil

import (
	"encoding/json"
	"testing"
)

func TestDepth(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"scalar", `42`, 0},
		{"empty object", `{}`, 1},
		{"empty array", `[]`, 1},
		{"flat object", `{"a":1,"b":2}`, 1},
		{"nested object", `{"a":{"b":{"c":1}}}`, 3},
		{"nested array", `[[[1]]]`, 3},
		{"mixed", `{"a":[{"b":1}]}`, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Depth(json.RawMessage(tc.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Depth(%s) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

