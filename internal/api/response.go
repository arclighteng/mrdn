package api

import (
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
)

type Freshness struct {
	Source      string     `json:"source"`
	SourceLag   string     `json:"source_lag"`
	LastUpdated *time.Time `json:"last_updated"`
	AgeSeconds  int        `json:"age_seconds"`
	Grade       string     `json:"grade"`
}

type Pagination struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

type ListResponse struct {
	Data       any         `json:"data"`
	Pagination *Pagination `json:"pagination,omitempty"`
	Freshness  any         `json:"freshness"`
}

type DetailResponse struct {
	Data      any `json:"data"`
	Freshness any `json:"freshness"`
}

type ErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorBody{Error: message, Code: code})
}

func freshnessFromSource(sm db.SourceMeta) Freshness {
	f := Freshness{
		Source:      sm.SourceName,
		SourceLag:   sm.ExpectedLag,
		LastUpdated: sm.LastNewDataAt,
	}

	if sm.LastNewDataAt != nil {
		f.AgeSeconds = int(math.Round(time.Since(*sm.LastNewDataAt).Seconds()))
	}

	// Grade logic:
	// If status is not healthy, grade is D
	// If PollIntervalSeconds is 0, we can't compute age ratio — grade D (unknown interval)
	// A = age < 2x poll_interval, B = < 5x, C = < 10x, D = older
	if sm.Status != "healthy" || sm.PollIntervalSeconds == 0 {
		f.Grade = "D"
		return f
	}

	if sm.LastNewDataAt == nil {
		f.Grade = "D"
		return f
	}

	ratio := float64(f.AgeSeconds) / float64(sm.PollIntervalSeconds)
	switch {
	case ratio < 2:
		f.Grade = "A"
	case ratio < 5:
		f.Grade = "B"
	case ratio < 10:
		f.Grade = "C"
	default:
		f.Grade = "D"
	}

	return f
}
