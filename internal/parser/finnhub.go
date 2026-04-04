package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/arclighteng/mrdn/internal/db"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	// finnhubWSURL is the Finnhub WebSocket endpoint. wss:// is required;
	// the token query parameter is appended at runtime and never logged.
	finnhubWSURL = "wss://ws.finnhub.io"

	// finnhubSourceName is the canonical source identifier for Finnhub events.
	finnhubSourceName = "finnhub"

	// maxWSMessageBytes caps incoming WebSocket message size at 1 MB.
	maxWSMessageBytes = 1 << 20
)

// FinnhubSource implements ingestion.StreamSource for the Finnhub WebSocket
// trade feed. It is safe for concurrent use: Rebalance may be called while
// Recv is blocked waiting for the next message.
type FinnhubSource struct {
	apiKey  string
	symbols []string
	conn    *websocket.Conn
	mu      sync.Mutex // protects conn and symbols
}

// NewFinnhubSource returns a FinnhubSource for the given API key and initial
// symbol list. Connect must be called before Recv.
func NewFinnhubSource(apiKey string, symbols []string) *FinnhubSource {
	cp := make([]string, len(symbols))
	copy(cp, symbols)
	return &FinnhubSource{
		apiKey:  apiKey,
		symbols: cp,
	}
}

// Name implements ingestion.StreamSource.
func (f *FinnhubSource) Name() string { return finnhubSourceName }

// Connect dials the Finnhub WebSocket endpoint and subscribes to all symbols.
// It enforces wss:// only. The full URL (which contains the API key) is never
// logged; errors refer to the host only.
func (f *FinnhubSource) Connect(ctx context.Context) error {
	// Enforce wss:// — reject any attempt to use plain ws://.
	if strings.HasPrefix(f.apiKey, "ws://") {
		return fmt.Errorf("finnhub: insecure scheme rejected; wss:// required")
	}

	// Build URL without logging it. The constant guarantees wss://.
	url := finnhubWSURL + "?token=" + f.apiKey

	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		// Do not include url in the error — it contains the API key.
		return fmt.Errorf("finnhub: dial %s: %w", finnhubWSURL, err)
	}
	conn.SetReadLimit(maxWSMessageBytes)

	f.mu.Lock()
	f.conn = conn
	symbols := make([]string, len(f.symbols))
	copy(symbols, f.symbols)
	f.mu.Unlock()

	for _, sym := range symbols {
		msg := map[string]string{"type": "subscribe", "symbol": sym}
		if err := wsjson.Write(ctx, conn, msg); err != nil {
			_ = conn.Close(websocket.StatusInternalError, "subscribe failed")
			return fmt.Errorf("finnhub: subscribe %s: %w", sym, err)
		}
	}
	return nil
}

// Recv reads one WebSocket message, validates its size, and parses it into
// db.Events. It returns an empty slice (and no error) for ping messages.
func (f *FinnhubSource) Recv(ctx context.Context) ([]db.Event, error) {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("finnhub: not connected")
	}

	_, raw, err := conn.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("finnhub: read: %w", err)
	}

	if len(raw) > maxWSMessageBytes {
		return nil, fmt.Errorf("finnhub: message too large (%d bytes)", len(raw))
	}

	return ParseFinnhubTrade(raw)
}

// Close shuts down the underlying WebSocket connection.
func (f *FinnhubSource) Close() error {
	f.mu.Lock()
	conn := f.conn
	f.conn = nil
	f.mu.Unlock()

	if conn == nil {
		return nil
	}
	return conn.Close(websocket.StatusNormalClosure, "closing")
}

// Rebalance updates the subscription list, unsubscribing symbols that were
// removed and subscribing symbols that were added. It is safe to call while
// Recv is blocked.
func (f *FinnhubSource) Rebalance(symbols []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	conn := f.conn
	if conn == nil {
		// Not connected yet; just update the list for the next Connect.
		f.symbols = make([]string, len(symbols))
		copy(f.symbols, symbols)
		return nil
	}

	// Build sets for efficient lookup.
	oldSet := make(map[string]struct{}, len(f.symbols))
	for _, s := range f.symbols {
		oldSet[s] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(symbols))
	for _, s := range symbols {
		newSet[s] = struct{}{}
	}

	ctx := context.Background()

	// Unsubscribe symbols no longer needed.
	for _, sym := range f.symbols {
		if _, keep := newSet[sym]; !keep {
			msg := map[string]string{"type": "unsubscribe", "symbol": sym}
			if err := wsjson.Write(ctx, conn, msg); err != nil {
				return fmt.Errorf("finnhub: unsubscribe %s: %w", sym, err)
			}
		}
	}

	// Subscribe new symbols.
	for _, sym := range symbols {
		if _, exists := oldSet[sym]; !exists {
			msg := map[string]string{"type": "subscribe", "symbol": sym}
			if err := wsjson.Write(ctx, conn, msg); err != nil {
				return fmt.Errorf("finnhub: subscribe %s: %w", sym, err)
			}
		}
	}

	f.symbols = make([]string, len(symbols))
	copy(f.symbols, symbols)
	return nil
}

// finnhubMessage is the top-level WebSocket message envelope from Finnhub.
type finnhubMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// finnhubTrade is a single trade entry within a "trade" type message.
type finnhubTrade struct {
	Symbol    string   `json:"s"`
	Price     float64  `json:"p"`
	Volume    float64  `json:"v"`
	Timestamp int64    `json:"t"` // Unix milliseconds
	Conditions []string `json:"c"`
}

// ParseFinnhubTrade parses a raw Finnhub WebSocket message and returns one
// db.Event per trade entry. Ping messages return an empty slice and nil error.
// Unknown message types also return an empty slice and nil error.
func ParseFinnhubTrade(data []byte) ([]db.Event, error) {
	var msg finnhubMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("finnhub: unmarshal message: %w", err)
	}

	switch msg.Type {
	case "ping":
		return nil, nil
	case "trade":
		// fall through to parsing below
	default:
		// Unknown message type — ignore gracefully.
		return nil, nil
	}

	var trades []finnhubTrade
	if err := json.Unmarshal(msg.Data, &trades); err != nil {
		return nil, fmt.Errorf("finnhub: unmarshal trades: %w", err)
	}

	events := make([]db.Event, 0, len(trades))
	for _, trade := range trades {
		raw, err := json.Marshal(trade)
		if err != nil {
			return nil, fmt.Errorf("finnhub: re-marshal trade symbol=%s: %w", trade.Symbol, err)
		}
		if err := ValidateEventData(raw); err != nil {
			return nil, fmt.Errorf("finnhub: trade symbol=%s: %w", trade.Symbol, err)
		}

		tsStr := fmt.Sprintf("%d", trade.Timestamp)
		occurredAt := time.UnixMilli(trade.Timestamp).UTC()

		events = append(events, db.Event{
			Source:     finnhubSourceName,
			SourceID:   sourceID(finnhubSourceName, trade.Symbol, tsStr),
			EventType:  "market_trade",
			EventData:  raw,
			OccurredAt: occurredAt,
		})
	}
	return events, nil
}
