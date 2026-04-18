package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 200, map[string]string{"hello": "world"})

	assert.Equal(t, 200, w.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "world", body["hello"])
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, 400, "BAD_REQUEST", "invalid parameter")

	assert.Equal(t, 400, w.Code)

	var body ErrorBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "BAD_REQUEST", body.Code)
	assert.Equal(t, "invalid parameter", body.Error)
}

func TestFreshnessFromSource_Healthy(t *testing.T) {
	now := time.Now().Add(-30 * time.Second)
	sm := db.SourceMeta{
		SourceName:          "polygon",
		ExpectedLag:         db.StrPtr("1 day"),
		LastNewDataAt:       &now,
		PollIntervalSeconds: db.IntPtr(86400),
		Status:              "healthy",
	}

	f := freshnessFromSource(sm)
	assert.Equal(t, "polygon", f.Source)
	assert.Equal(t, db.StrPtr("1 day"), f.SourceLag)
	assert.Equal(t, "A", f.Grade)
	assert.InDelta(t, 30, f.AgeSeconds, 5)
}

func TestFreshnessFromSource_GradeB(t *testing.T) {
	// 3x poll interval → grade B
	now := time.Now().Add(-3 * 3600 * time.Second) // 3 hours ago
	sm := db.SourceMeta{
		SourceName:          "edgar_form4",
		ExpectedLag:         db.StrPtr("same day"),
		LastNewDataAt:       &now,
		PollIntervalSeconds: db.IntPtr(3600),
		Status:              "healthy",
	}

	f := freshnessFromSource(sm)
	assert.Equal(t, "B", f.Grade)
}

func TestFreshnessFromSource_GradeC(t *testing.T) {
	// 7x poll interval → grade C
	now := time.Now().Add(-7 * 3600 * time.Second)
	sm := db.SourceMeta{
		SourceName:          "edgar_form4",
		ExpectedLag:         db.StrPtr("same day"),
		LastNewDataAt:       &now,
		PollIntervalSeconds: db.IntPtr(3600),
		Status:              "healthy",
	}

	f := freshnessFromSource(sm)
	assert.Equal(t, "C", f.Grade)
}

func TestFreshnessFromSource_Degraded(t *testing.T) {
	now := time.Now().Add(-10 * time.Second)
	sm := db.SourceMeta{
		SourceName:          "finnhub",
		ExpectedLag:         db.StrPtr("seconds"),
		LastNewDataAt:       &now,
		PollIntervalSeconds: db.IntPtr(0),
		Status:              "healthy",
	}

	f := freshnessFromSource(sm)
	assert.Equal(t, "D", f.Grade) // PollIntervalSeconds=0 → can't compute ratio → D
}

func TestFreshnessFromSource_Down(t *testing.T) {
	now := time.Now().Add(-10 * time.Second)
	sm := db.SourceMeta{
		SourceName:          "polygon",
		ExpectedLag:         db.StrPtr("1 day"),
		LastNewDataAt:       &now,
		PollIntervalSeconds: db.IntPtr(86400),
		Status:              "down",
	}

	f := freshnessFromSource(sm)
	assert.Equal(t, "D", f.Grade)
}

func TestFreshnessFromSource_NilLastUpdated(t *testing.T) {
	sm := db.SourceMeta{
		SourceName:          "polygon",
		ExpectedLag:         db.StrPtr("1 day"),
		PollIntervalSeconds: db.IntPtr(86400),
		Status:              "healthy",
	}

	f := freshnessFromSource(sm)
	assert.Equal(t, "D", f.Grade)
	assert.Equal(t, 0, f.AgeSeconds)
	assert.Nil(t, f.LastUpdated)
}
