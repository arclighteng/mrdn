// Package broker provides an in-process, channel-based pub/sub broker for
// distributing events to SSE subscribers without touching the database.
package broker

import (
	"fmt"
	"sync"
	"time"
)

const subscriberBufferSize = 64

// Event carries the minimal fields needed to push an update to subscribers.
type Event struct {
	ID         int
	CompanyID  *int
	Ticker     string
	Source     string
	EventType  string
	OccurredAt time.Time
}

// Broker fans out published events to all registered subscribers.
// It is safe for concurrent use by multiple goroutines.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string]chan Event
	maxGlobal   int
}

// New returns a Broker that allows at most maxGlobal concurrent subscribers.
func New(maxGlobal int) *Broker {
	return &Broker{
		subscribers: make(map[string]chan Event),
		maxGlobal:   maxGlobal,
	}
}

// Subscribe registers a new subscriber identified by id and returns a
// receive-only channel on which events will be delivered. The channel is
// buffered (64 items). Subscribe returns an error if the broker is at its
// global subscriber cap.
func (b *Broker) Subscribe(id string) (<-chan Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.subscribers) >= b.maxGlobal {
		return nil, fmt.Errorf("broker: subscriber cap reached (%d)", b.maxGlobal)
	}

	ch := make(chan Event, subscriberBufferSize)
	b.subscribers[id] = ch
	return ch, nil
}

// Unsubscribe removes the subscriber with the given id and closes its channel.
// It is safe to call Unsubscribe for an id that does not exist.
func (b *Broker) Unsubscribe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch, ok := b.subscribers[id]
	if !ok {
		return
	}
	delete(b.subscribers, id)
	close(ch)
}

// Publish delivers evt to every current subscriber. Delivery is non-blocking:
// if a subscriber's buffer is full, the oldest item is dropped to make room
// before the new event is enqueued.
func (b *Broker) Publish(evt Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
			// Buffer full: drain the oldest item, then send.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- evt:
			default:
			}
		}
	}
}

// Close closes every subscriber channel and removes all subscribers.
// After Close, no further events can be delivered.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	for id, ch := range b.subscribers {
		delete(b.subscribers, id)
		close(ch)
	}
}

// Count returns the number of currently registered subscribers.
func (b *Broker) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}
