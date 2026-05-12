package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/auth"
	"github.com/dopesoft/infinity/core/internal/cron"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/proactive"
	"github.com/dopesoft/infinity/core/internal/sentinel"
	"github.com/dopesoft/infinity/core/internal/sessions"
	"github.com/dopesoft/infinity/core/internal/settings"
	"github.com/dopesoft/infinity/core/internal/skills"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/dopesoft/infinity/core/internal/voyager"
	"github.com/jackc/pgx/v5/pgxpool"
)

// turnState tracks one in-flight agent turn keyed by session_id so the WS
// handler can interrupt it or feed it mid-turn steering messages without
// blocking the read loop. The cancel func tears the turn down; the steer
// channel is drained by the agent loop between iterations. Buffer is
// sized for normal human typing cadence; overflow surfaces a soft error
// to the client rather than blocking the WS read goroutine.
type turnState struct {
	cancel context.CancelFunc
	steer  chan string
}

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
	CronAPI      *cron.API
	SentinelAPI  *sentinel.API
	VoyagerAPI   *voyager.API
	Auth         *auth.Verifier
	// Trust is the durable approval store. Canvas's git mutations and
	// file saves queue contracts here and block on user approval before
	// touching the home Mac. Same store the agent gate uses.
	Trust *proactive.TrustStore
	// Namer auto-renames sessions via Haiku after the first complete
	// exchange. Nil-safe: the rename endpoint just returns 503 when
	// unconfigured.
	Namer *sessions.Namer
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
	trust     *proactive.TrustStore
	namer     *sessions.Namer
	auth      *auth.Verifier
	settings  *settings.Store
	started   time.Time

	// turnsMu guards the per-session in-flight turn registry. Lookups
	// happen on every WS frame so we keep the critical sections trivial
	// (map ops only) and never hold the lock across send() or cancel().
	turnsMu sync.Mutex
	turns   map[string]*turnState
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
		trust:     cfg.Trust,
		namer:     cfg.Namer,
		auth:      cfg.Auth,
		settings:  settings.New(cfg.Pool),
		started:   time.Now(),
		turns:     make(map[string]*turnState),
	}

	mux := http.NewServeMux()
	s.routes(mux)
	if s.skillsAPI != nil {
		s.skillsAPI.Routes(mux)
	}
	if cfg.ProactiveAPI != nil {
		cfg.ProactiveAPI.Routes(mux)
	}
	if cfg.CronAPI != nil {
		cfg.CronAPI.Routes(mux)
	}
	if cfg.SentinelAPI != nil {
		cfg.SentinelAPI.Routes(mux)
	}
	if cfg.VoyagerAPI != nil {
		cfg.VoyagerAPI.Routes(mux)
	}

	// Auth middleware. /health and /auth/* stay open so the studio can
	// probe liveness and complete the signup handshake before holding a
	// token. WS authorizes inside handleWebSocket (it needs to send a
	// 401 on the upgrade response, which middleware-401s break).
	var handler http.Handler = mux
	if cfg.Auth != nil {
		handler = cfg.Auth.HTTPMiddleware([]string{"/health", "/auth/", "/ws"})(handler)
	}

	s.http = &http.Server{
		Addr:              cfg.Addr,
		Handler:           withCORS(handler),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/", s.handleSessionMessages)
	mux.HandleFunc("/api/messages/", s.handleMessageFeedback)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/tools", s.handleTools)
	mux.HandleFunc("/api/mcp", s.handleMCP)
	mux.HandleFunc("/api/memory/counts", s.handleMemoryCounts)
	mux.HandleFunc("/api/memory/search", s.handleMemorySearch)
	mux.HandleFunc("/api/memory/observations", s.handleObservations)
	mux.HandleFunc("/api/memory/memories", s.handleMemoryList)
	mux.HandleFunc("/api/memory/cite/", s.handleMemoryCite)
	mux.HandleFunc("/api/memory/audit", s.handleAuditLog)
	mux.HandleFunc("/api/memory/profile", s.handleProfile)
	mux.HandleFunc("/api/memory/graph", s.handleGraph)
	mux.HandleFunc("/api/canvas/fs/ls", s.handleCanvasFSList)
	mux.HandleFunc("/api/canvas/fs/read", s.handleCanvasFSRead)
	mux.HandleFunc("/api/canvas/fs/save", s.handleCanvasFSSave)
	mux.HandleFunc("/api/canvas/git/status", s.handleCanvasGitStatus)
	mux.HandleFunc("/api/canvas/git/diff", s.handleCanvasGitDiff)
	mux.HandleFunc("/api/canvas/git/stage", s.handleCanvasGitStage)
	mux.HandleFunc("/api/canvas/git/commit", s.handleCanvasGitCommit)
	mux.HandleFunc("/api/canvas/git/push", s.handleCanvasGitPush)
	mux.HandleFunc("/api/canvas/git/pull", s.handleCanvasGitPull)
	mux.HandleFunc("/api/canvas/config", s.handleCanvasConfig)
	mux.HandleFunc("/api/canvas/debug", s.handleCanvasDebug)
	mux.HandleFunc("/api/canvas/project/start", s.handleCanvasProjectStart)
	mux.HandleFunc("/api/canvas/project/stop", s.handleCanvasProjectStop)
	mux.HandleFunc("/api/canvas/project/active", s.handleCanvasProjectActive)
	mux.HandleFunc("/api/canvas/project/status", s.handleCanvasProjectStatus)
	mux.HandleFunc("/api/settings/model", s.handleSettingsModel)
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
