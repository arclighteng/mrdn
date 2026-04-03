package parser_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arclighteng/mrdn/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEventData_Valid(t *testing.T) {
	raw := json.RawMessage(`{"ticker":"AAPL","price":189.5,"tags":["q1","earnings"]}`)
	err := parser.ValidateEventData(raw)
	require.NoError(t, err)
}

func TestValidateEventData_Oversize(t *testing.T) {
	// Build a JSON string value that exceeds 64 KB.
	// The string content alone is > 65536 bytes, so the full JSON object is too.
	inner := strings.Repeat("x", 66000)
	raw := json.RawMessage(`{"data":"` + inner + `"}`)
	err := parser.ValidateEventData(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestValidateEventData_Malformed(t *testing.T) {
	raw := json.RawMessage(`{invalid json`)
	err := parser.ValidateEventData(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestValidateEventData_TooDeep(t *testing.T) {
	// Build 12 levels of nesting: {"a":{"a":{"a":...}}}
	depth := 12
	raw := strings.Repeat(`{"a":`, depth) + `1` + strings.Repeat(`}`, depth)
	err := parser.ValidateEventData(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nesting exceeds")
}

func TestValidateEventData_AtLimit(t *testing.T) {
	// Exactly MaxEventDataDepth (10) levels — must not error.
	depth := parser.MaxEventDataDepth
	raw := strings.Repeat(`{"a":`, depth) + `1` + strings.Repeat(`}`, depth)
	err := parser.ValidateEventData(json.RawMessage(raw))
	require.NoError(t, err)
}
