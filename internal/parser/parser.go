package parser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	MaxEventDataSize  = 65536 // 64 KB
	MaxEventDataDepth = 10
)

// ValidateEventData checks that raw is well-formed JSON within size and depth limits.
// Parsers should call this before passing an event to db.Store.InsertEvent so that
// problems are surfaced before the database round-trip.
func ValidateEventData(raw json.RawMessage) error {
	if len(raw) > MaxEventDataSize {
		return fmt.Errorf("event_data exceeds %d bytes (got %d)", MaxEventDataSize, len(raw))
	}
	if !json.Valid(raw) {
		return fmt.Errorf("event_data is not valid JSON")
	}
	depth, err := jsonDepth(raw)
	if err != nil {
		return fmt.Errorf("event_data depth check: %w", err)
	}
	if depth > MaxEventDataDepth {
		return fmt.Errorf("event_data nesting exceeds %d levels (got %d)", MaxEventDataDepth, depth)
	}
	return nil
}

// jsonDepth returns the maximum nesting depth of the JSON value in raw.
// An empty object or array at the top level has depth 1.
// A scalar (string, number, bool, null) has depth 0.
func jsonDepth(raw json.RawMessage) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	maxDepth := 0
	currentDepth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, err
		}
		switch t {
		case json.Delim('{'), json.Delim('['):
			currentDepth++
			if currentDepth > maxDepth {
				maxDepth = currentDepth
			}
		case json.Delim('}'), json.Delim(']'):
			currentDepth--
		}
	}
	return maxDepth, nil
}
