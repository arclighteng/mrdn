package ingestion

import (
	"context"
	"math/rand/v2"
	"time"
)

const (
	defaultBase = 5 * time.Second
	defaultMax  = 15 * time.Minute
	maxJitter   = 3 * time.Second
)

// Backoff computes exponential back-off durations with random jitter.
// The zero value is not usable; create instances with NewBackoff.
type Backoff struct {
	Base    time.Duration
	Max     time.Duration
	attempt int
	clock   Clock
}

// NewBackoff returns a Backoff with Base=5s, Max=15min, using the provided
// clock for time operations.
func NewBackoff(clock Clock) *Backoff {
	return &Backoff{
		Base:  defaultBase,
		Max:   defaultMax,
		clock: clock,
	}
}

// Next returns the duration to wait before the next attempt and increments the
// internal attempt counter. The formula is:
//
//	min(Base * 2^attempt + jitter, Max)
//
// where jitter is a random value in [0, 3s).
func (b *Backoff) Next() time.Duration {
	exp := time.Duration(1) << b.attempt // 2^attempt
	d := b.Base * exp
	jitter := time.Duration(rand.Int64N(int64(maxJitter)))
	d += jitter
	if d > b.Max {
		d = b.Max
	}
	b.attempt++
	return d
}

// Reset sets the attempt counter back to zero.
func (b *Backoff) Reset() {
	b.attempt = 0
}

// Wait sleeps for the duration returned by Next. It returns ctx.Err() if the
// context is cancelled before the timer fires, and nil otherwise.
func (b *Backoff) Wait(ctx context.Context) error {
	d := b.Next()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.clock.After(d):
		return nil
	}
}
