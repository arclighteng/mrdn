package db

import "time"

// SQLite datetime formats to try when scanning.
var timeFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// formatTime converts a time.Time to an ISO 8601 string for SQLite storage.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// formatTimePtr converts a *time.Time to *string for SQLite storage.
func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// scanTime parses a time string from SQLite into a time.Time.
// Handles both RFC3339 and SQLite's default datetime() format.
func scanTime(s string) (time.Time, error) {
	for _, fmt := range timeFormats {
		if t, err := time.Parse(fmt, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Parse(time.RFC3339, s)
}

// scanTimePtr parses an optional time string.
func scanTimePtr(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := scanTime(*s)
	if err != nil {
		return nil
	}
	return &t
}
