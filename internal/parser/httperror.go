package parser

import "fmt"

// HTTPStatusError is returned by source Poll implementations when an upstream
// HTTP endpoint responds with a non-2xx status. The worker layer inspects it
// via errors.As to record the status code on source_meta so the dashboard can
// surface a user-friendly banner (e.g. "Senate EFDS — upstream 503").
type HTTPStatusError struct {
	Source     string // short source tag, e.g. "efds", "edgar"
	StatusCode int
	Detail     string // optional extra context (e.g. "for date 2024-01-02")
}

func (e *HTTPStatusError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s: unexpected status %d (%s)", e.Source, e.StatusCode, e.Detail)
	}
	return fmt.Sprintf("%s: unexpected status %d", e.Source, e.StatusCode)
}
