package server

import (
	"context"
	"net/http"
	"time"
)

type Config struct {
	Addr    string
	Version string
}

type Server struct {
	cfg     Config
	http    *http.Server
	started time.Time
}

func New(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	s := &Server{cfg: cfg, started: time.Now()}

	mux := http.NewServeMux()
	s.routes(mux)

	s.http = &http.Server{
		Addr:              cfg.Addr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.handleHealth)
}

func (s *Server) Start() error {
	err := s.http.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
