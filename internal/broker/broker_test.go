package broker_test

import (
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEvent(id int) broker.Event {
	return broker.Event{
		ID:         id,
		Ticker:     "AAPL",
		Source:     "test",
		EventType:  "price",
		OccurredAt: time.Now(),
	}
}

func TestPublishReceive(t *testing.T) {
	b := broker.New(10)
	ch, err := b.Subscribe("sub1")
	require.NoError(t, err)

	evt := makeEvent(1)
	b.Publish(evt)

	select {
	case got := <-ch:
		assert.Equal(t, evt.ID, got.ID)
		assert.Equal(t, evt.Ticker, got.Ticker)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	b := broker.New(10)

	ch1, err := b.Subscribe("sub1")
	require.NoError(t, err)
	ch2, err := b.Subscribe("sub2")
	require.NoError(t, err)
	ch3, err := b.Subscribe("sub3")
	require.NoError(t, err)

	evt := makeEvent(42)
	b.Publish(evt)

	for _, ch := range []<-chan broker.Event{ch1, ch2, ch3} {
		select {
		case got := <-ch:
			assert.Equal(t, 42, got.ID)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event on subscriber")
		}
	}
}

func TestUnsubscribe(t *testing.T) {
	b := broker.New(10)
	ch, err := b.Subscribe("sub1")
	require.NoError(t, err)

	b.Unsubscribe("sub1")

	// Channel must be closed: a receive returns the zero value immediately.
	select {
	case _, open := <-ch:
		assert.False(t, open, "channel should be closed after Unsubscribe")
	case <-time.After(time.Second):
		t.Fatal("timed out; channel was not closed")
	}

	assert.Equal(t, 0, b.Count())
}

func TestFullBufferDropsOldest(t *testing.T) {
	b := broker.New(10)
	ch, err := b.Subscribe("sub1")
	require.NoError(t, err)

	// Fill the 64-item buffer.
	for i := 0; i < 64; i++ {
		b.Publish(makeEvent(i))
	}

	// This 65th publish must not block and must result in the newest item
	// being deliverable.
	done := make(chan struct{})
	go func() {
		b.Publish(makeEvent(999))
		close(done)
	}()

	select {
	case <-done:
		// Good — Publish returned without blocking.
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a full buffer")
	}

	// Drain the channel and verify the last item is event 999.
	var last broker.Event
	for {
		select {
		case got := <-ch:
			last = got
		default:
			goto drained
		}
	}
drained:
	assert.Equal(t, 999, last.ID, "last item should be the newest event (999)")
}

func TestSubscribeAtCap(t *testing.T) {
	b := broker.New(2)

	_, err := b.Subscribe("sub1")
	require.NoError(t, err)
	_, err = b.Subscribe("sub2")
	require.NoError(t, err)

	_, err = b.Subscribe("sub3")
	require.Error(t, err, "third subscribe should fail when cap is 2")
	assert.Equal(t, 2, b.Count())
}

func TestClose(t *testing.T) {
	b := broker.New(10)

	ch1, err := b.Subscribe("sub1")
	require.NoError(t, err)
	ch2, err := b.Subscribe("sub2")
	require.NoError(t, err)

	b.Close()

	for _, ch := range []<-chan broker.Event{ch1, ch2} {
		select {
		case _, open := <-ch:
			assert.False(t, open, "channel should be closed after Close()")
		case <-time.After(time.Second):
			t.Fatal("timed out; channel was not closed by Close()")
		}
	}

	assert.Equal(t, 0, b.Count())
}
