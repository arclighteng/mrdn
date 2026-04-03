package api

import (
	"net/http"

	"github.com/arclighteng/mrdn/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	store  *db.Store
	router chi.Router
}

func NewServer(store *db.Store) *Server {
	s := &Server{store: store}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.SetHeader("Content-Type", "application/json"))

	r.Get("/health", s.handleHealth)

	s.router = r
}

func (s *Server) Handler() http.Handler {
	return s.router
}
