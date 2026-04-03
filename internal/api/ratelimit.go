package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"golang.org/x/time/rate"
)

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter implements per-key token bucket rate limiting as chi middleware.
// Anonymous requests are limited by IP; authenticated requests by API key hash.
type RateLimiter struct {
	store   *db.Store
	mu      sync.Mutex
	entries map[string]*limiterEntry
	cancel  context.CancelFunc
}

// NewRateLimiter creates a rate limiter that uses store to look up API keys.
// Call Shutdown when the server is stopping to clean up the background goroutine.
func NewRateLimiter(store *db.Store) *RateLimiter {
	ctx, cancel := context.WithCancel(context.Background())
	rl := &RateLimiter{
		store:   store,
		entries: make(map[string]*limiterEntry),
		cancel:  cancel,
	}
	go rl.cleanup(ctx)
	return rl
}

// Shutdown stops the background cleanup goroutine.
func (rl *RateLimiter) Shutdown() {
	rl.cancel()
}

func (rl *RateLimiter) cleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for k, e := range rl.entries {
				if e.lastSeen.Before(cutoff) {
					delete(rl.entries, k)
				}
			}
			rl.mu.Unlock()
		}
	}
}

func (rl *RateLimiter) getLimiter(key string, ratePerMin int) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if e, ok := rl.entries[key]; ok {
		e.lastSeen = time.Now()
		return e.limiter
	}

	// token bucket: rate = ratePerMin/60 per second, burst = ratePerMin
	l := rate.NewLimiter(rate.Limit(float64(ratePerMin)/60.0), ratePerMin)
	rl.entries[key] = &limiterEntry{limiter: l, lastSeen: time.Now()}
	return l
}

// Middleware returns chi-compatible middleware that rate-limits requests.
func (rl *RateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			var limiterKey string
			var ratePerMin int

			if apiKey != "" {
				hash := hashAPIKey(apiKey)
				if rl.store == nil {
					writeError(w, 401, "INVALID_KEY", "invalid API key")
					return
				}
				key, err := rl.store.GetAPIKey(r.Context(), hash)
				if err != nil {
					writeError(w, 401, "INVALID_KEY", "invalid API key")
					return
				}
				limiterKey = "key:" + hash
				ratePerMin = key.RateLimit
			} else {
				ip := clientIP(r)
				limiterKey = "ip:" + ip
				ratePerMin = 60
			}

			limiter := rl.getLimiter(limiterKey, ratePerMin)

			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", ratePerMin))

			if !limiter.Allow() {
				w.Header().Set("Retry-After", "60")
				w.Header().Set("X-RateLimit-Remaining", "0")
				writeError(w, 429, "RATE_LIMITED", "rate limit exceeded")
				return
			}

			// Approximate remaining tokens
			remaining := int(limiter.Tokens())
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

			next.ServeHTTP(w, r)
		})
	}
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", h)
}

// clientIP extracts the client IP, using X-Forwarded-For (rightmost non-private)
// with fallback to RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		// Walk from right to find rightmost non-private IP
		for i := len(parts) - 1; i >= 0; i-- {
			ip := strings.TrimSpace(parts[i])
			parsed := net.ParseIP(ip)
			if parsed != nil && !isPrivateIP(parsed) {
				return ip
			}
		}
		// All are private — use leftmost as fallback
		return strings.TrimSpace(parts[0])
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isPrivateIP(ip net.IP) bool {
	privateRanges := []struct {
		start net.IP
		end   net.IP
	}{
		{net.ParseIP("10.0.0.0"), net.ParseIP("10.255.255.255")},
		{net.ParseIP("172.16.0.0"), net.ParseIP("172.31.255.255")},
		{net.ParseIP("192.168.0.0"), net.ParseIP("192.168.255.255")},
		{net.ParseIP("127.0.0.0"), net.ParseIP("127.255.255.255")},
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	for _, r := range privateRanges {
		if bytesInRange(ip4, r.start.To4(), r.end.To4()) {
			return true
		}
	}
	return false
}

func bytesInRange(ip, start, end net.IP) bool {
	for i := 0; i < 4; i++ {
		if ip[i] < start[i] {
			return false
		}
		if ip[i] > end[i] {
			return false
		}
	}
	return true
}
