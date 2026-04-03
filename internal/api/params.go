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
