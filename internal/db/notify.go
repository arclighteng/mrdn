package db

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

const notifyChannel = "new_event"

// NotifyNewEvent sends a Postgres NOTIFY on the new_event channel with the event ID
// as the payload. It is called by the ingestion worker after a successful InsertEvent.
// eventID is an int so the fmt.Sprintf is not a SQL-injection risk.
func NotifyNewEvent(ctx context.Context, pool *pgxpool.Pool, eventID int) error {
	_, err := pool.Exec(ctx, fmt.Sprintf("NOTIFY %s, '%d'", notifyChannel, eventID))
	if err != nil {
		return fmt.Errorf("NOTIFY new_event: %w", err)
	}
	return nil
}

// ListenNewEvents starts a LISTEN on the new_event channel using a dedicated
// connection acquired from pool. It returns a channel of event IDs. The channel
// is closed when ctx is cancelled or when the underlying connection fails.
//
// The caller must not close the returned channel. Cancelling ctx is sufficient
// to trigger a clean shutdown.
func ListenNewEvents(ctx context.Context, pool *pgxpool.Pool) (<-chan int, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring connection for LISTEN: %w", err)
	}

	if _, err := conn.Exec(ctx, "LISTEN "+notifyChannel); err != nil {
		conn.Release()
		return nil, fmt.Errorf("LISTEN %s: %w", notifyChannel, err)
	}

	ch := make(chan int, 256)
	go func() {
		defer conn.Release()
		defer close(ch)
		for {
			notification, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return // clean shutdown via context cancellation
				}
				log.Printf("db: LISTEN error on channel %s: %v", notifyChannel, err)
				return
			}
			id, err := strconv.Atoi(notification.Payload)
			if err != nil {
				log.Printf("db: invalid NOTIFY payload: %q", notification.Payload)
				continue
			}
			select {
			case ch <- id:
			default:
				// Buffer full — drop the oldest to make room for the newest.
				select {
				case <-ch:
				default:
				}
				ch <- id
			}
		}
	}()

	return ch, nil
}
