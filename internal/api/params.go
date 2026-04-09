package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func parseInt(r *http.Request, key string, defaultVal int) (int, error) {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: must be an integer", key)
	}
	return v, nil
}

func parseFloat(r *http.Request, key string, defaultVal float64) (float64, error) {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: must be a number", key)
	}
	return v, nil
}

func parseTime(r *http.Request, key string) (*time.Time, error) {
	s := r.URL.Query().Get(key)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: must be RFC3339 format", key)
	}
	return &t, nil
}

// maxTimeRange is the maximum allowed span for parseTimeRange (1 year).
const maxTimeRange = 365 * 24 * time.Hour

// parseTimeRange parses the optional `since` and `until` query parameters
// as RFC3339 timestamps. Either, both, or neither may be present. If both
// are set, until must be strictly after since, and the span must not
// exceed maxTimeRange (1 year). Returns (nil, nil, nil) when neither is set.
func parseTimeRange(r *http.Request) (since, until *time.Time, err error) {
	since, err = parseTime(r, "since")
	if err != nil {
		return nil, nil, err
	}
	until, err = parseTime(r, "until")
	if err != nil {
		return nil, nil, err
	}
	if since != nil && until != nil {
		if !until.After(*since) {
			return nil, nil, fmt.Errorf("invalid range: until must be after since")
		}
		if until.Sub(*since) > maxTimeRange {
			return nil, nil, fmt.Errorf("invalid range: span must not exceed 1 year")
		}
	}
	return since, until, nil
}

func parseString(r *http.Request, key, defaultVal string) string {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	return s
}

func parsePagination(r *http.Request) (limit, offset int, err error) {
	limit, err = parseInt(r, "limit", 50)
	if err != nil {
		return 0, 0, err
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	offset, err = parseInt(r, "offset", 0)
	if err != nil {
		return 0, 0, err
	}
	if offset < 0 {
		offset = 0
	}

	return limit, offset, nil
}
