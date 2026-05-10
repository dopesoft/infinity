package server

import (
	"context"
	"net/http"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/proactive"
	"github.com/dopesoft/infinity/core/internal/skills"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Addr         string
	Version      string
	Loop         *agent.Loop
	MCP          *tools.MCPManager
	Pool         *pgxpool.Pool
	Store        *memory.Store
	Searcher     *memory.Searcher
	SkillsAPI    *skills.API
	ProactiveAPI *proactive.API
}

type Server struct {
	cfg       Config
	http      *http.Server
	loop      *agent.Loop
	mcp       *tools.MCPManager
	pool      *pgxpool.Pool
	store     *memory.Store
	searcher  *memory.Searcher
	skillsAPI *skills.API
	started   time.Time
}

func New(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	s := &Server{
		cfg:       cfg,
		loop:      cfg.Loop,
		mcp:       cfg.MCP,
		pool:      cfg.Pool,
		store:     cfg.Store,
		searcher:  cfg.Searcher,
		skillsAPI: cfg.SkillsAPI,
		started:   time.Now(),
	}

	mux := http.NewServeMux()
	s.routes(mux)
	if s.skillsAPI != nil {
		s.skillsAPI.Routes(mux)
	}
	if cfg.ProactiveAPI != nil {
		cfg.ProactiveAPI.Routes(mux)
	}

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
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/tools", s.handleTools)
	mux.HandleFunc("/api/mcp", s.handleMCP)
	mux.HandleFunc("/api/memory/counts", s.handleMemoryCounts)
	mux.HandleFunc("/api/memory/search", s.handleMemorySearch)
	mux.HandleFunc("/api/memory/observations", s.handleObservations)
	mux.HandleFunc("/api/memory/memories", s.handleMemoryList)
	mux.HandleFunc("/api/memory/cite/", s.handleMemoryCite)
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
