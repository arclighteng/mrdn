package api

import (
	"io/fs"
	"net/http"

	"github.com/arclighteng/mrdn/internal/broker"
	"github.com/arclighteng/mrdn/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server is the HTTP API server. It owns the chi router, rate limiter,
// SSE connection manager, and a reference to the event broker.
type Server struct {
	store       *db.Store
	router      chi.Router
	rateLimiter *RateLimiter
	broker      *broker.Broker
	sseManager  *SSEManager
}

// NewServer constructs a Server with a default broker (500 subscribers) and
// SSE connection limits (3 per IP, 10 per API key, 500 global).
// Call SetBroker to replace the default broker before serving if the broker
// must be shared with an ingest process.
func NewServer(store *db.Store) *Server {
	s := &Server{
		store:      store,
		broker:     broker.New(500),
		sseManager: NewSSEManager(3, 10, 500),
	}
	s.setupRoutes()
	return s
}

// SetBroker replaces the internal broker. Must be called before the server
// starts serving requests (not safe for concurrent use).
func (s *Server) SetBroker(b *broker.Broker) {
	s.broker = b
}

// Broker returns the server's event broker so that external producers
// (e.g. the ingest CLI command) can publish events to SSE subscribers.
func (s *Server) Broker() *broker.Broker {
	return s.broker
}

// SetStaticFS sets the embedded filesystem for serving the frontend dashboard.
// The fsys must contain a "static" directory with the frontend assets.
func (s *Server) SetStaticFS(fsys fs.FS) {
	sub, err := fs.Sub(fsys, "static")
	if err != nil {
		return
	}
	s.router.Handle("/*", http.FileServer(http.FS(sub)))
}

func (s *Server) setupRoutes() {
	r := chi.NewRouter()
	r.Use(SecurityHeaders)
	r.Use(CORSMiddleware)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Health is outside all middleware groups — no JSON content-type, no rate limit.
	r.Get("/health", s.handleHealth)

	// SSE endpoints — no JSON content-type middleware, no rate limiter.
	// Routes are ordered so that /scores (static) is matched before /{ticker} (param).
	r.Route("/api/v1/stream", func(r chi.Router) {
		r.Get("/", s.handleStream)
		r.Get("/scores", s.handleStreamScores)
		r.Get("/{ticker}", s.handleStreamTicker)
	})

	s.rateLimiter = NewRateLimiter(s.store)

	// REST API routes — JSON content-type header + rate limiter applied here only.
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.SetHeader("Content-Type", "application/json"))
		r.Use(s.rateLimiter.Middleware())

		r.Get("/companies", s.handleListCompanies)
		r.Get("/companies/{ticker}", s.handleGetCompany)
		r.Get("/companies/{ticker}/scores", s.handleCompanyScores)
		r.Get("/companies/{ticker}/events", s.handleCompanyEvents)

		r.Get("/events", s.handleListEvents)
		r.Get("/events/latest", s.handleLatestEvents)
		r.Get("/events/{id}", s.handleGetEvent)

		r.Get("/scores/rankings", s.handleScoreRankings)
		r.Get("/scores/movers", s.handleScoreMovers)

		r.Get("/sources", s.handleListSources)
		r.Get("/sources/{name}", s.handleGetSource)
	})

	s.router = r
}

// Handler returns the root http.Handler for use with http.Server.
func (s *Server) Handler() http.Handler {
	return s.router
}

// Shutdown cleans up background goroutines. Call after the HTTP server has
// finished draining connections.
func (s *Server) Shutdown() {
	if s.rateLimiter != nil {
		s.rateLimiter.Shutdown()
	}
	if s.broker != nil {
		s.broker.Close()
	}
}
