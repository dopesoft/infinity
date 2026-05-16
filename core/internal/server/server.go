package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/auth"
	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/dopesoft/infinity/core/internal/cron"
	"github.com/dopesoft/infinity/core/internal/dashboard"
	"github.com/dopesoft/infinity/core/internal/intent"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/proactive"
	"github.com/dopesoft/infinity/core/internal/push"
	"github.com/dopesoft/infinity/core/internal/sentinel"
	"github.com/dopesoft/infinity/core/internal/sessions"
	"github.com/dopesoft/infinity/core/internal/settings"
	"github.com/dopesoft/infinity/core/internal/skills"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/dopesoft/infinity/core/internal/voice"
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
	// Proactive subsystems wired into the live turn lifecycle. All nil-safe:
	// any nil field disables that capability without breaking chat. Together
	// these turn the agent from reactive (only replies when spoken to) into
	// proactive (classifies intent per turn, captures load-bearing fragments
	// to durable WAL, mirrors high-context conversations into a recoverable
	// buffer, and broadcasts heartbeat findings as unprompted assistant
	// turns on any active WS session).
	IntentDetector *intent.Detector
	IntentStore    *intent.Store
	WAL            *proactive.WAL
	WorkingBuffer  *proactive.WorkingBuffer
	Heartbeat      *proactive.Heartbeat
	// LLMRegistry is the map of constructable providers used by the
	// Settings PUT to hot-swap the agent loop's active provider without
	// a restart. Nil-safe — when absent, /api/settings/provider returns
	// 503 and the loop sticks with its boot provider.
	LLMRegistry *llm.Registry
	// Connectors caches the live picture of Composio connected accounts
	// + boss aliases. Nil-safe: when unset, the alias endpoints return
	// 503 and the catalog block falls back to its toolkit-summary form
	// without account-id awareness.
	Connectors *connectors.Cache
	// Voice mints OpenAI Realtime ephemeral keys so the browser can do
	// WebRTC directly. Nil-safe: when unset (OPENAI_API_KEY missing) the
	// /api/voice/* endpoints all return 503 and Studio's mic button
	// surfaces "voice not configured".
	Voice *voice.Minter
	// PushAPI registers /api/push/* — VAPID key, subscribe/unsubscribe,
	// device list, test send. Nil-safe: missing VAPID env disables push
	// and the handlers return empty / 503 cleanly.
	PushAPI *push.API
	// DashboardAPI registers GET /api/dashboard returning every section
	// (pursuits, todos, calendar, follow-ups, saved, memory stats).
	// Nil-safe: Studio falls back to its local mock when missing.
	DashboardAPI *dashboard.API
	// BridgeRouter decides per session whether fs/bash/git ops land on
	// the Mac bridge (Cloudflare tunnel to home Mac) or the Cloud bridge
	// (docker/workspace on Railway private net). Nil-safe — when unset
	// the bridge_* tools error cleanly and the /api/bridge endpoints
	// return offline status.
	BridgeRouter *bridge.Router
	// BridgePrefs resolves mem_sessions.bridge_preference for a given
	// session id; used both by the tool-side router pick and the API
	// endpoints that surface "what would this session use right now."
	BridgePrefs tools.PreferenceFetcher
	// Turns is the LangSmith-style trace store backing /api/traces and the
	// trace_* agent tools. Nil-safe — endpoints return empty / 503 cleanly
	// when no DB is wired.
	Turns *memory.TurnStore
}

type Server struct {
	cfg        Config
	http       *http.Server
	loop       *agent.Loop
	mcp        *tools.MCPManager
	pool       *pgxpool.Pool
	store      *memory.Store
	searcher   *memory.Searcher
	skillsAPI  *skills.API
	trust      *proactive.TrustStore
	namer      *sessions.Namer
	auth       *auth.Verifier
	settings   *settings.Store
	llmReg     *llm.Registry
	connectors *connectors.Cache
	voice      *voice.Minter
	started    time.Time

	intentDet *intent.Detector
	intentDB  *intent.Store
	wal       *proactive.WAL
	buffer    *proactive.WorkingBuffer
	heartbeat *proactive.Heartbeat

	bridgeRouter *bridge.Router
	bridgePrefs  tools.PreferenceFetcher
	turnStore    *memory.TurnStore

	// turnsMu guards the per-session in-flight turn registry. Lookups
	// happen on every WS frame so we keep the critical sections trivial
	// (map ops only) and never hold the lock across send() or cancel().
	turnsMu sync.Mutex
	turns   map[string]*turnState

	// activeMu guards activeSessions. Distinct from turnsMu because a
	// session can be "active" (WS connected) without a turn in flight —
	// that's exactly when we want to push unprompted assistant messages
	// from the heartbeat. Map value is the send func bound to the WS
	// writer goroutine; calling it pushes a frame to that browser tab.
	activeMu       sync.Mutex
	activeSessions map[string]func(wsServerEvent)
}

func New(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	s := &Server{
		cfg:            cfg,
		loop:           cfg.Loop,
		mcp:            cfg.MCP,
		pool:           cfg.Pool,
		store:          cfg.Store,
		searcher:       cfg.Searcher,
		skillsAPI:      cfg.SkillsAPI,
		trust:          cfg.Trust,
		namer:          cfg.Namer,
		auth:           cfg.Auth,
		settings:       settings.New(cfg.Pool),
		llmReg:         cfg.LLMRegistry,
		connectors:     cfg.Connectors,
		voice:          cfg.Voice,
		started:        time.Now(),
		turns:          make(map[string]*turnState),
		intentDet:      cfg.IntentDetector,
		intentDB:       cfg.IntentStore,
		wal:            cfg.WAL,
		buffer:         cfg.WorkingBuffer,
		heartbeat:      cfg.Heartbeat,
		activeSessions: make(map[string]func(wsServerEvent)),
		bridgeRouter:   cfg.BridgeRouter,
		bridgePrefs:    cfg.BridgePrefs,
		turnStore:      cfg.Turns,
	}
	if s.heartbeat != nil {
		s.heartbeat.SetOnFinding(s.onHeartbeatFinding)
	}

	// Apply the persisted provider override at boot. The agent loop was
	// constructed with the LLM_PROVIDER env value; if Studio's last save
	// flipped it elsewhere, honor that — Settings is the source of truth
	// once the user has touched the picker.
	if s.loop != nil && s.llmReg != nil && s.settings != nil {
		if persisted := s.settings.GetProvider(context.Background()); persisted != "" {
			if p, ok := s.llmReg.Get(persisted); ok {
				s.loop.SetProvider(p)
			}
		}
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
	if cfg.PushAPI != nil {
		cfg.PushAPI.Routes(mux)
	}
	if cfg.DashboardAPI != nil {
		cfg.DashboardAPI.Routes(mux)
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
	mux.HandleFunc("/api/sessions/seed", s.handleSessionsSeed)
	mux.HandleFunc("/api/sessions/", s.handleSessionMessages)
	mux.HandleFunc("/api/messages/", s.handleMessageFeedback)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/tools", s.handleTools)
	mux.HandleFunc("/api/mcp", s.handleMCP)
	mux.HandleFunc("/api/memory/counts", s.handleMemoryCounts)
	mux.HandleFunc("/api/memory/search", s.handleMemorySearch)
	mux.HandleFunc("/api/memory/observations", s.handleObservations)
	mux.HandleFunc("/api/memory/memories", s.handleMemoryList)
	mux.HandleFunc("/api/memory/reflections", s.handleMemoryReflections)
	mux.HandleFunc("/api/memory/predictions", s.handleMemoryPredictions)
	mux.HandleFunc("/api/memory/cite/", s.handleMemoryCite)
	mux.HandleFunc("/api/memory/audit", s.handleAuditLog)
	mux.HandleFunc("/api/memory/profile", s.handleProfile)
	mux.HandleFunc("/api/gym", s.handleGym)
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
	mux.HandleFunc("/api/settings/provider", s.handleSettingsProvider)
	mux.HandleFunc("/api/context/usage", s.handleContextUsage)
	mux.HandleFunc("/api/meta", s.handleMeta)
	mux.HandleFunc("/api/auth/openai/start", s.handleOpenAIOAuthStart)
	mux.HandleFunc("/api/auth/openai/exchange", s.handleOpenAIOAuthExchange)
	mux.HandleFunc("/api/auth/openai/status", s.handleOpenAIOAuthStatus)
	mux.HandleFunc("/api/auth/openai/disconnect", s.handleOpenAIOAuthDisconnect)

	// Composio Connectors — proxy endpoints for the Studio /connectors page.
	// Key never leaves core; browser sees Composio JSON shape directly.
	mux.HandleFunc("/api/connectors/composio/toolkits", s.handleComposioToolkits)
	mux.HandleFunc("/api/connectors/composio/connected", s.handleComposioConnected)
	mux.HandleFunc("/api/connectors/composio/connect", s.handleComposioConnect)
	mux.HandleFunc("/api/connectors/composio/accounts/", s.handleComposioAccount)
	mux.HandleFunc("/api/connectors/composio/aliases", s.handleComposioAliases)
	mux.HandleFunc("/api/connectors/composio/cache", s.handleComposioCacheStatus)

	// Voice — OpenAI Realtime over WebRTC. Browser holds the audio
	// pipes, Core mints the key, runs tools, and persists turns.
	mux.HandleFunc("/api/voice/session", s.handleVoiceSession)
	mux.HandleFunc("/api/voice/tool", s.handleVoiceTool)
	mux.HandleFunc("/api/voice/turn", s.handleVoiceTurn)

	// "Is the running binary behind main?" detection. Compares the
	// RAILWAY_GIT_COMMIT_SHA env (set per deploy) to GitHub's main HEAD.
	mux.HandleFunc("/api/deploy/status", s.handleDeployStatus)

	// Bridge layer: status, per-session preference, manual refresh.
	mux.HandleFunc("/api/bridge/status", s.handleBridgeStatus)
	mux.HandleFunc("/api/bridge/refresh", s.handleBridgeRefresh)
	mux.HandleFunc("/api/bridge/session/", s.handleBridgeSession)
	// Cloud workspace staleness: cloud bridge's local HEAD vs origin/<branch>.
	mux.HandleFunc("/api/bridge/workspace/git-status", s.handleBridgeWorkspaceGitStatus)

	// Library — mem_artifacts grouped by kind. The Files tab IS the library;
	// this powers the collapsible section at the top.
	mux.HandleFunc("/api/library/tree", s.handleLibraryTree)

	// LangSmith-style turn-by-turn traces. /api/traces lists rows; the
	// trailing-slash variant matches /api/traces/<turn_id> for detail.
	mux.HandleFunc("/api/traces", s.handleTracesList)
	mux.HandleFunc("/api/traces/", s.handleTraceDetail)
}

func (s *Server) Start() error {
	// Kick off deploy-staleness polling. 5-min ticker; soft-fails on
	// GitHub rate limits / transient errors so it never crashes serve.
	startDeployPoller(context.Background())
	err := s.http.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// withCORS handles cross-origin requests from Studio.
//
// Production reality: Studio runs at infinity.dopesoft.io while Core runs at
// core-production-*.up.railway.app — two different origins. Every authed
// fetch from Studio sends `Authorization: Bearer …` which is a non-simple
// header, so the browser preflights with OPTIONS before the real request.
// Any miss on the preflight → the actual call never goes out.
//
// Three things this implementation gets right that the prior version didn't:
//
//  1. **Echo the Origin** instead of wildcarding `*`. Some browsers /
//     proxies (Cloudflare in front of Railway included) drop or rewrite
//     `Access-Control-Allow-Origin: *` for credentialed-ish requests.
//     Echoing the explicit origin works everywhere.
//  2. **Reflect the request's `Access-Control-Request-Headers`** so any
//     header Studio decides to send in the future (x-request-id, etc.)
//     is preflight-approved automatically.
//  3. **`Access-Control-Max-Age`** so the browser caches the preflight
//     for an hour — fewer round-trips, less surface area for transient
//     preflight failures.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		// Echo whatever headers the browser asked to send. Falls back to a
		// sane default for clients that don't request anything specific.
		reqHeaders := r.Header.Get("Access-Control-Request-Headers")
		if reqHeaders == "" {
			reqHeaders = "Content-Type,Authorization"
		}
		w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		w.Header().Set("Access-Control-Expose-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "3600")
		// Short-circuit ALL preflight requests here so nothing downstream
		// (auth middleware, the mux, individual handlers) gets a chance
		// to swallow them and return a header-less 401/404/405.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
