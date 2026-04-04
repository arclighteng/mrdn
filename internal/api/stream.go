package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/go-chi/chi/v5"
)

// sseSubCounter is a monotonically increasing counter for unique subscriber IDs.
var sseSubCounter atomic.Uint64

// safeEventType matches only characters safe for SSE event names.
var safeEventType = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// handleStream streams all events to the client without filtering.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, func(evt broker.Event) bool { return true })
}

// handleStreamTicker streams only events whose Ticker matches the URL parameter.
func (s *Server) handleStreamTicker(w http.ResponseWriter, r *http.Request) {
	ticker := chi.URLParam(r, "ticker")
	s.serveSSE(w, r, func(evt broker.Event) bool {
		return evt.Ticker == ticker
	})
}

// handleStreamScores streams only score_change events.
func (s *Server) handleStreamScores(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, func(evt broker.Event) bool {
		return evt.EventType == "score_change"
	})
}

// serveSSE is the shared SSE loop. It:
//  1. Validates flusher support
//  2. Acquires a connection slot (429 if at cap)
//  3. Subscribes to the broker
//  4. Sends SSE-formatted events that pass filter
//  5. Emits a keepalive comment every 15 s
//  6. Closes after 30 minutes with a reconnect signal
//  7. Returns cleanly on client disconnect
func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request, filter func(broker.Event) bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "SSE_NOT_SUPPORTED", "streaming not supported")
		return
	}

	ip := clientIP(r)
	apiKey := r.Header.Get("X-API-Key")

	release, err := s.sseManager.Acquire(ip, apiKey)
	if err != nil {
		writeError(w, 429, "SSE_LIMIT_REACHED", err.Error())
		return
	}
	defer release()

	// Unique subscriber ID uses an atomic counter to guarantee no collisions.
	subID := fmt.Sprintf("sse-%s-%d", ip, sseSubCounter.Add(1))
	ch, err := s.broker.Subscribe(subID)
	if err != nil {
		writeError(w, 429, "SSE_LIMIT_REACHED", err.Error())
		return
	}
	defer s.broker.Unsubscribe(subID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	maxDuration := time.NewTimer(30 * time.Minute)
	defer maxDuration.Stop()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return

		case <-maxDuration.C:
			fmt.Fprintf(w, "event: reconnect\ndata: {}\n\n")
			flusher.Flush()
			return

		case <-heartbeat.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()

		case evt, ok := <-ch:
			if !ok {
				return // broker closed channel
			}
			if !filter(evt) {
				continue
			}
			// Sanitize event type to prevent SSE injection via newlines or control chars.
			eventType := evt.EventType
			if !safeEventType.MatchString(eventType) {
				eventType = "unknown"
			}
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", eventType, evt.ID, data)
			flusher.Flush()
		}
	}
}
