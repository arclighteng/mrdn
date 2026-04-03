// Package ingestion contains shared utilities used by all ingest workers:
// a Clock abstraction for testability and an exponential-backoff helper.
package ingestion

import "time"

// Clock abstracts time operations so ingestion logic can be tested without
// real sleeps.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// realClock delegates to the standard library.
type realClock struct{}

func (realClock) Now() time.Time                        { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// RealClock returns a Clock backed by the standard library.
func RealClock() Clock { return realClock{} }

// FakeClock is a manually-controlled clock for use in tests. Calling After
// immediately advances the clock by d and returns a pre-filled channel.
type FakeClock struct {
	now time.Time
}

// NewFakeClock returns a FakeClock initialised to t.
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{now: t}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time { return c.now }

// After advances the fake clock by d and returns a buffered channel that
// already contains the new time, so callers never block.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.now = c.now.Add(d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

// Advance moves the fake clock forward by d without triggering a channel.
func (c *FakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
