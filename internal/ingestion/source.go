package ingestion

import (
	"context"

	"github.com/arclighteng/mrdn/internal/db"
)

// Source represents a pollable data source that produces normalized events.
type Source interface {
	Name() string
	Poll(ctx context.Context) ([]db.Event, error)
}

// StreamSource represents a persistent-connection data source (e.g., WebSocket).
type StreamSource interface {
	Name() string
	Connect(ctx context.Context) error
	Recv(ctx context.Context) ([]db.Event, error)
	Close() error
}
