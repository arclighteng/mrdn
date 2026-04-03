package api

import (
	"fmt"
	"sync"
)

// SSEManager tracks concurrent SSE connections per IP, per API key, and globally.
type SSEManager struct {
	mu        sync.Mutex
	perIP     map[string]int
	perKey    map[string]int
	global    int
	maxPerIP  int
	maxPerKey int
	maxGlobal int
}

// NewSSEManager returns an SSEManager that enforces the given connection limits.
// maxPerIP limits anonymous connections from a single IP address.
// maxPerKey limits connections from a single authenticated API key.
// maxGlobal caps total concurrent SSE connections across all clients.
func NewSSEManager(maxPerIP, maxPerKey, maxGlobal int) *SSEManager {
	return &SSEManager{
		perIP:     make(map[string]int),
		perKey:    make(map[string]int),
		maxPerIP:  maxPerIP,
		maxPerKey: maxPerKey,
		maxGlobal: maxGlobal,
	}
}

// Acquire reserves an SSE connection slot. apiKey is empty for anonymous clients,
// in which case the IP address is used as the per-client key.
// Returns a release function the caller must invoke on disconnect, or an error
// if any limit (per-IP, per-key, or global) has been reached.
func (m *SSEManager) Acquire(ip, apiKey string) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.global >= m.maxGlobal {
		return nil, fmt.Errorf("global SSE connection limit reached")
	}

	// Authenticated clients are bucketed by API key; anonymous by IP.
	limit := m.maxPerIP
	counter := m.perIP
	key := ip
	if apiKey != "" {
		limit = m.maxPerKey
		counter = m.perKey
		key = apiKey
	}

	if counter[key] >= limit {
		return nil, fmt.Errorf("SSE connection limit reached for %s", key)
	}

	counter[key]++
	m.global++

	released := false
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if released {
			return
		}
		released = true
		counter[key]--
		if counter[key] <= 0 {
			delete(counter, key)
		}
		m.global--
	}, nil
}

// Count returns the current global connection count.
func (m *SSEManager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.global
}
