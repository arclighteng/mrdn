package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSEManager_AcquireRelease(t *testing.T) {
	m := NewSSEManager(3, 10, 500)
	release, err := m.Acquire("1.2.3.4", "")
	require.NoError(t, err)
	assert.Equal(t, 1, m.Count())
	release()
	assert.Equal(t, 0, m.Count())
}

func TestSSEManager_PerIPLimit(t *testing.T) {
	m := NewSSEManager(2, 10, 500)
	r1, err := m.Acquire("1.2.3.4", "")
	require.NoError(t, err)
	r2, err := m.Acquire("1.2.3.4", "")
	require.NoError(t, err)

	_, err = m.Acquire("1.2.3.4", "")
	assert.Error(t, err)

	// A different IP is unaffected.
	r3, err := m.Acquire("5.6.7.8", "")
	require.NoError(t, err)

	r1()
	r2()
	r3()
	assert.Equal(t, 0, m.Count())
}

func TestSSEManager_PerKeyLimit(t *testing.T) {
	m := NewSSEManager(1, 2, 500)
	r1, err := m.Acquire("1.2.3.4", "key-abc")
	require.NoError(t, err)
	r2, err := m.Acquire("5.6.7.8", "key-abc") // same key, different IP
	require.NoError(t, err)

	_, err = m.Acquire("9.9.9.9", "key-abc")
	assert.Error(t, err)

	r1()
	r2()
	assert.Equal(t, 0, m.Count())
}

func TestSSEManager_GlobalLimit(t *testing.T) {
	m := NewSSEManager(100, 100, 2)
	r1, err := m.Acquire("1.1.1.1", "")
	require.NoError(t, err)
	r2, err := m.Acquire("2.2.2.2", "")
	require.NoError(t, err)

	_, err = m.Acquire("3.3.3.3", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "global")

	r1()
	r2()
	assert.Equal(t, 0, m.Count())
}

func TestSSEManager_DoubleRelease(t *testing.T) {
	m := NewSSEManager(3, 10, 500)
	release, err := m.Acquire("1.2.3.4", "")
	require.NoError(t, err)

	release()
	release() // must not panic or decrement below zero

	assert.Equal(t, 0, m.Count())
}

func TestSSEManager_ReleaseCleansUpMap(t *testing.T) {
	m := NewSSEManager(3, 10, 500)
	release, err := m.Acquire("1.2.3.4", "")
	require.NoError(t, err)
	release()

	// After release the IP entry must be removed so the map does not grow unboundedly.
	m.mu.Lock()
	_, exists := m.perIP["1.2.3.4"]
	m.mu.Unlock()
	assert.False(t, exists)
}

func TestSSEManager_AnonymousAndKeyIndependent(t *testing.T) {
	// maxPerIP=1, maxPerKey=1 — anonymous and keyed slots are counted separately.
	m := NewSSEManager(1, 1, 500)
	rAnon, err := m.Acquire("1.2.3.4", "")
	require.NoError(t, err)
	rKey, err := m.Acquire("1.2.3.4", "my-key")
	require.NoError(t, err)
	assert.Equal(t, 2, m.Count())
	rAnon()
	rKey()
}
