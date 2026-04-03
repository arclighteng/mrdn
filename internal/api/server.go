package api

import (
	"net/http"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	store       *db.Store
	router      chi.Router
	rateLimiter *RateLimiter
}

func NewServer(store *db.Store) *Server {
	s := &Server{store: store}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	r := chi.NewRouter()
	r.Use(SecurityHeaders)
	r.Use(CORSMiddleware)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.SetHeader("Content-Type", "application/json"))

	r.Get("/health", s.handleHealth)

	s.rateLimiter = NewRateLimiter(s.store)

	r.Route("/api/v1", func(r chi.Router) {
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

func (s *Server) Handler() http.Handler {
	return s.router
}

// Shutdown cleans up background goroutines.
func (s *Server) Shutdown() {
	if s.rateLimiter != nil {
		s.rateLimiter.Shutdown()
	}
}
