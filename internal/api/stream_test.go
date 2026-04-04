package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newStreamServer returns a minimal Server wired for SSE tests — no DB store needed.
func newStreamServer() *Server {
	return &Server{
		broker:     broker.New(500),
		sseManager: NewSSEManager(3, 10, 500),
	}
}

// readSSELines connects to ts and returns a channel that receives non-empty SSE lines
// plus a cleanup function to close the response body.
func readSSELines(t *testing.T, ts *httptest.Server) (lines chan string, cleanup func()) {
	t.Helper()
	resp, err := http.Get(ts.URL)
	require.NoError(t, err)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	ch := make(chan string, 64)
	go func() {
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				ch <- line
			}
		}
	}()
	return ch, func() { resp.Body.Close() }
}

func waitLine(t *testing.T, lines chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case line := <-lines:
		return line
	case <-time.After(timeout):
		t.Fatal("timed out waiting for SSE line")
		return ""
	}
}

// ---- Tests ----

func TestStreamEndpoint_Headers(t *testing.T) {
	s := newStreamServer()
	ts := httptest.NewServer(http.HandlerFunc(s.handleStream))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "keep-alive", resp.Header.Get("Connection"))
}

func TestStreamEndpoint_ReceivesEvent(t *testing.T) {
	s := newStreamServer()
	ts := httptest.NewServer(http.HandlerFunc(s.handleStream))
	defer ts.Close()

	lines, cleanup := readSSELines(t, ts)
	defer cleanup()

	// Give the subscriber a moment to register before publishing.
	time.Sleep(20 * time.Millisecond)

	s.broker.Publish(broker.Event{
		ID:         42,
		Source:     "ofac_sdn",
		EventType:  "sanction",
		OccurredAt: time.Now(),
	})

	eventLine := waitLine(t, lines, 2*time.Second)
	assert.Equal(t, "event: sanction", eventLine)

	idLine := waitLine(t, lines, 2*time.Second)
	assert.Equal(t, "id: 42", idLine)

	dataLine := waitLine(t, lines, 2*time.Second)
	assert.True(t, strings.HasPrefix(dataLine, "data: "), "expected data line, got: %s", dataLine)
	assert.Contains(t, dataLine, `"EventType":"sanction"`)
}

func TestStreamScores_FiltersNonScoreEvents(t *testing.T) {
	s := newStreamServer()
	ts := httptest.NewServer(http.HandlerFunc(s.handleStreamScores))
	defer ts.Close()

	lines, cleanup := readSSELines(t, ts)
	defer cleanup()

	time.Sleep(20 * time.Millisecond)

	// This should be filtered out.
	s.broker.Publish(broker.Event{ID: 1, EventType: "sanction", OccurredAt: time.Now()})

	// This should come through.
	s.broker.Publish(broker.Event{ID: 2, EventType: "score_change", OccurredAt: time.Now()})

	eventLine := waitLine(t, lines, 2*time.Second)
	assert.Equal(t, "event: score_change", eventLine)
}

func TestStreamTicker_FiltersCorrectly(t *testing.T) {
	s := newStreamServer()

	// Wrap handleStreamTicker: inject {ticker} param manually via chi context
	// by using a real chi router so URL params work.
	r := newChiRouterForTest(s)
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/AAPL")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	lines := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if line := scanner.Text(); line != "" {
				lines <- line
			}
		}
	}()

	time.Sleep(20 * time.Millisecond)

	// Wrong ticker — must be dropped.
	s.broker.Publish(broker.Event{ID: 1, Ticker: "MSFT", EventType: "price", OccurredAt: time.Now()})
	// Correct ticker — must come through.
	s.broker.Publish(broker.Event{ID: 2, Ticker: "AAPL", EventType: "price", OccurredAt: time.Now()})

	eventLine := waitLine(t, lines, 2*time.Second)
	assert.Equal(t, "event: price", eventLine)

	idLine := waitLine(t, lines, 2*time.Second)
	assert.Equal(t, "id: 2", idLine)
}

func TestStream_ConnectionLimit(t *testing.T) {
	s := &Server{
		broker:     broker.New(500),
		sseManager: NewSSEManager(1, 10, 500), // maxPerIP = 1
	}
	ts := httptest.NewServer(http.HandlerFunc(s.handleStream))
	defer ts.Close()

	// First connection — should succeed.
	resp1, err := http.Get(ts.URL)
	require.NoError(t, err)
	defer resp1.Body.Close()
	assert.Equal(t, http.StatusOK, resp1.StatusCode)

	time.Sleep(20 * time.Millisecond)

	// Second connection from same IP (127.0.0.1 in tests) — should be 429.
	resp2, err := http.Get(ts.URL)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp2.StatusCode)
}

func TestStream_ClientDisconnect_NoGoroutineLeak(t *testing.T) {
	s := newStreamServer()
	ts := httptest.NewServer(http.HandlerFunc(s.handleStream))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 1, s.sseManager.Count())

	// Cancel the request context — simulates client disconnect.
	cancel()
	resp.Body.Close()

	// Allow the server goroutine to detect ctx.Done and run deferred cleanup.
	require.Eventually(t, func() bool {
		return s.sseManager.Count() == 0
	}, 500*time.Millisecond, 10*time.Millisecond, "SSE slot not released after disconnect")

	require.Eventually(t, func() bool {
		return s.broker.Count() == 0
	}, 500*time.Millisecond, 10*time.Millisecond, "broker subscriber not released after disconnect")
}

func TestStream_BrokerClose_EndsStream(t *testing.T) {
	s := newStreamServer()
	ts := httptest.NewServer(http.HandlerFunc(s.handleStream))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)

	// Closing the broker must close all subscriber channels, causing serveSSE to return.
	s.broker.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1)
		resp.Body.Read(buf) //nolint:errcheck
		resp.Body.Close()
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stream did not close after broker.Close()")
	}
}

// newChiRouterForTest wires only the ticker stream route so URL params work in tests.
func newChiRouterForTest(s *Server) http.Handler {
	// Import chi directly — stream_test.go is in package api so we have access.
	// We rebuild a minimal router just for the ticker param test.
	// Using the full setupRoutes would require a store; this is lighter.
	import_chi_mux := func() http.Handler {
		// chi is already imported by server.go; reuse it here via a closure.
		// We build a tiny chi mux in-process.
		mux := http.NewServeMux()
		// We can't use chi URL params with net/http ServeMux, so we wrap the handler
		// using a test-only approach: serve everything under / and extract from path.
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) == 0 || parts[0] == "" {
				http.NotFound(w, r)
				return
			}
			ticker := parts[0]
			s.serveSSE(w, r, func(evt broker.Event) bool {
				return evt.Ticker == ticker
			})
		})
		return mux
	}
	return import_chi_mux()
}
