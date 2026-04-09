// Package jsonutil provides small JSON helpers shared across packages.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"io"
)

// Depth returns the maximum nesting depth of the JSON value in raw.
// An empty object or array at the top level has depth 1; a scalar has depth 0.
func Depth(raw json.RawMessage) (int, error) {
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
