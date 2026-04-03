package api

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashAPIKey(t *testing.T) {
	key := "test-key-123"
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	assert.Equal(t, expected, hashAPIKey(key))
}

func TestClientIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:12345"
	assert.Equal(t, "1.2.3.4", clientIP(r))
}

func TestClientIP_XForwardedFor_PublicIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "192.168.1.1, 10.0.0.1, 8.8.8.8")
	// Rightmost non-private is 8.8.8.8
	assert.Equal(t, "8.8.8.8", clientIP(r))
}

func TestClientIP_XForwardedFor_AllPrivate(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "192.168.1.1, 10.0.0.1")
	// All private, fallback to leftmost
	assert.Equal(t, "192.168.1.1", clientIP(r))
}

func TestRateLimiter_AnonymousAllowed(t *testing.T) {
	rl := &RateLimiter{
		entries: make(map[string]*limiterEntry),
	}

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assert.Equal(t, 200, w.Code)
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Limit"))
}

func TestRateLimiter_AnonymousExceedsLimit(t *testing.T) {
	rl := &RateLimiter{
		entries: make(map[string]*limiterEntry),
	}

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Send 61 requests — first 60 should succeed, 61st should be rate limited
	var lastCode int
	for i := 0; i < 61; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "5.6.7.8:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		lastCode = w.Code
	}
	assert.Equal(t, 429, lastCode)
}

func TestRateLimiter_InvalidKeyReturns401(t *testing.T) {
	// Use nil store — any DB lookup will fail, which we treat as invalid key
	rl := &RateLimiter{
		entries: make(map[string]*limiterEntry),
	}

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-API-Key", "bad-key")
	r.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assert.Equal(t, 401, w.Code)
}
