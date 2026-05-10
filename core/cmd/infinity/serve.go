package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/auth"
	"github.com/dopesoft/infinity/core/internal/cron"
	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/dopesoft/infinity/core/internal/hooks"
	"github.com/dopesoft/infinity/core/internal/intent"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/proactive"
	"github.com/dopesoft/infinity/core/internal/sentinel"
	"github.com/dopesoft/infinity/core/internal/server"
	"github.com/dopesoft/infinity/core/internal/skills"
	"github.com/dopesoft/infinity/core/internal/soul"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/dopesoft/infinity/core/internal/voyager"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var addr, mcpConfig string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Core HTTP/WebSocket server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if v := os.Getenv("PORT"); v != "" {
				addr = ":" + v
			}

			provider, err := llm.FromEnv()
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: LLM provider not ready: %v\n", err)
			}

			registry := tools.NewRegistry()
			tools.RegisterDefaults(cmd.Context(), registry)

			mcp := tools.NewMCPManager()
			if cfg, err := tools.LoadMCPConfig(mcpConfig); err != nil {
				fmt.Fprintf(os.Stderr, "warning: mcp config: %v\n", err)
			} else if cfg != nil && len(cfg.Servers) > 0 {
				connectCtx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				if err := mcp.Connect(connectCtx, cfg, registry); err != nil {
					fmt.Fprintf(os.Stderr, "warning: mcp connect: %v\n", err)
				}
				cancel()
			}

			// Memory + hooks + tools wiring (best-effort).
			var (
				pool       *pgxpool.Pool
				store      *memory.Store
				searcher   *memory.Searcher
				compressor *memory.Compressor
				pipeline   *hooks.Pipeline
				embedder   embed.Embedder
			)

			if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
				pctx, pcancel := context.WithTimeout(cmd.Context(), 10*time.Second)
				p, err := pgxpool.New(pctx, dsn)
				pcancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: db pool: %v\n", err)
				} else {
					pool = p
					embedder = embed.FromEnv()
					store = memory.NewStore(p)
					searcher = memory.NewSearcher(p, embedder)

					// Compressor needs an Anthropic client; wire only if the
					// active provider is Anthropic so we don't pin a 2nd key.
					if a, ok := provider.(*llm.Anthropic); ok {
						summarizerModel := os.Getenv("LLM_SUMMARIZE_MODEL")
						summarizer := llm.NewAnthropicSummarizer(a, summarizerModel)
						compressor = memory.NewCompressor(p, embedder, memory.NewSummarizer(summarizer))
					}

					pipeline = hooks.NewPipeline()
					hooks.RegisterDefaults(pipeline, p, store, embedder, compressor)

					tools.RegisterMemoryTools(registry, p, embedder, searcher)

					fmt.Printf("  memory: enabled (embedder=%s, compressor=%v)\n", embedder.Name(), compressor != nil)
				}
			} else {
				fmt.Fprintf(os.Stderr, "  memory: disabled (no DATABASE_URL)\n")
			}

			// Skills system: filesystem-backed registry, optional
			// store-backed persistence, agent tools + HTTP API.
			skillsRoot := os.Getenv("INFINITY_SKILLS_ROOT")
			if skillsRoot == "" {
				skillsRoot = "./skills"
			}
			skillRegistry := skills.NewRegistry(skillsRoot)
			var skillStore *skills.Store
			if pool != nil {
				skillStore = skills.NewStore(pool)
				skillRegistry.AttachStore(skillStore)
			}
			loadCtx, loadCancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			if errs, err := skillRegistry.Reload(loadCtx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: skills reload: %v\n", err)
			} else if len(errs) > 0 {
				for _, e := range errs {
					fmt.Fprintf(os.Stderr, "warning: skill load error %s: %s\n", e.Path, e.Err)
				}
			}
			loadCancel()
			skillRunner := skills.NewRunner(skillRegistry, skillStore)
			skills.RegisterTools(registry, skillRegistry, skillRunner)
			skillsAPI := skills.NewAPI(skillRegistry, skillRunner, skillStore)
			fmt.Printf("  skills: %d loaded from %s\n", len(skillRegistry.All()), skillsRoot)

			soulPrompt, soulSource := soul.Load()
			fmt.Printf("  soul: %s (%d chars)\n", soulSource, len(soulPrompt))

			var loop *agent.Loop
			if provider != nil {
				cfg := agent.Config{LLM: provider, Tools: registry, Skills: skillRegistry, SystemPrompt: soulPrompt}
				if searcher != nil {
					cfg.Memory = searcher
				}
				if pipeline != nil {
					cfg.Hooks = &hooks.PipelineAdapter{P: pipeline}
				}
				loop = agent.New(cfg)
			}

			// Proactive engine: IntentFlow + WAL + Working Buffer + Heartbeat +
			// Trust Contracts. Each component degrades gracefully when its
			// dependency (LLM provider, DB pool) is missing.
			var (
				intentDetector *intent.Detector
				intentDB       *intent.Store
				heartbeat      *proactive.Heartbeat
				trustStore     *proactive.TrustStore
				proactiveAPI   *proactive.API
			)
			if pool != nil {
				intentDB = intent.NewStore(pool)
				trustStore = proactive.NewTrustStore(pool)
				heartbeat = proactive.NewHeartbeat(pool, heartbeatInterval(),
					proactive.DefaultChecklist(pool))
				heartbeat.Start(cmd.Context())
				if a, ok := provider.(*llm.Anthropic); ok {
					intentDetector = intent.New(intent.Config{
						Provider: a,
						Model:    os.Getenv("INFINITY_INTENT_MODEL"),
					})
				}
				proactiveAPI = proactive.NewAPI(pool, heartbeat, trustStore, intentDB)
				fmt.Printf("  proactive: heartbeat every %s, intent=%v, trust=ready\n",
					heartbeat.Interval(), intentDetector != nil)
			}
			_ = intentDetector // TODO: wire into the agent loop once the WS
			// handler emits per-turn observations; the detector is already
			// invocable for Studio's intent-stream panel via API.

			// Cron scheduler + Sentinel manager. Both degrade gracefully when
			// no DB pool is configured.
			var (
				cronScheduler *cron.Scheduler
				sentinelMgr   *sentinel.Manager
				cronAPI       *cron.API
				sentinelAPI   *sentinel.API
			)
			if pool != nil {
				if loop != nil {
					cronScheduler = cron.New(pool, cron.NewAgentExecutor(loop))
				} else {
					cronScheduler = cron.New(pool, nil)
				}
				if err := cronScheduler.Start(cmd.Context()); err != nil {
					fmt.Fprintf(os.Stderr, "warning: cron start: %v\n", err)
				}
				cronAPI = cron.NewAPI(cronScheduler)

				dispatcher := sentinel.SkillDispatcher{
					Inner:   sentinel.LogDispatcher{},
					Invoker: skillInvoker{runner: skillRunner},
				}
				sentinelMgr = sentinel.NewManager(pool, dispatcher)
				_ = sentinelMgr.Reload(cmd.Context())
				sentinelAPI = sentinel.NewAPI(sentinelMgr)
				fmt.Printf("  cron+sentinel: ready (cron=%v, sentinels=%d)\n",
					cronScheduler != nil, len(sentinelMgr.List()))
			}

			// Voyager auto-skill loop. Wires hooks for SessionEnd (extractor)
			// and PostToolUse (real-time discovery). Off by default; flip
			// INFINITY_VOYAGER=true on the core service to enable.
			var voyagerAPI *voyager.API
			if pool != nil {
				vAnthropic, _ := provider.(*llm.Anthropic)
				voyagerMgr := voyager.New(voyager.Config{
					Pool:       pool,
					LLM:        vAnthropic,
					Skills:     skillRegistry,
					SkillsRoot: skillsRoot,
				})
				if pipeline != nil {
					pipeline.RegisterFunc("voyager.extract", voyagerMgr.OnSessionEnd, hooks.SessionEnd)
					pipeline.RegisterFunc("voyager.discover", voyagerMgr.OnPostToolUse, hooks.PostToolUse)
				}
				voyagerAPI = voyager.NewAPI(voyagerMgr)
				fmt.Printf("  voyager: %s\n", voyagerMgr.Status())
			}

			authVerifier, err := auth.FromEnv(cmd.Context(), pool)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: auth: %v\n", err)
			}
			if authVerifier != nil {
				if authVerifier.Enabled() {
					owner := authVerifier.Owner()
					if owner == "" {
						fmt.Printf("  auth: enabled (JWKS) — no owner claimed yet, first signup wins\n")
					} else {
						fmt.Printf("  auth: enabled (JWKS) — owner=%s\n", owner)
					}
				} else {
					fmt.Printf("  auth: DISABLED (set SUPABASE_URL to enable)\n")
				}
			}

			srv := server.New(server.Config{
				Addr:         addr,
				Version:      version,
				Loop:         loop,
				MCP:          mcp,
				Pool:         pool,
				Store:        store,
				Searcher:     searcher,
				SkillsAPI:    skillsAPI,
				ProactiveAPI: proactiveAPI,
				CronAPI:      cronAPI,
				SentinelAPI:  sentinelAPI,
				VoyagerAPI:   voyagerAPI,
				Auth:         authVerifier,
			})

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			errCh := make(chan error, 1)
			go func() { errCh <- srv.Start() }()

			fmt.Printf("infinity core %s listening on %s\n", version, addr)
			if loop != nil {
				fmt.Printf("  provider: %s · model: %s\n", loop.Provider().Name(), loop.Provider().Model())
			}
			if names := registry.Names(); len(names) > 0 {
				fmt.Printf("  tools (%d): %v\n", len(names), names)
			}

			select {
			case err := <-errCh:
				mcp.Close()
				if pool != nil {
					pool.Close()
				}
				return err
			case <-ctx.Done():
				fmt.Println("shutdown signal received")
				shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancelShutdown()
				err := srv.Shutdown(shutdownCtx)
				mcp.Close()
				if pool != nil {
					pool.Close()
				}
				return err
			}
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "listen address (or use $PORT)")
	cmd.Flags().StringVar(&mcpConfig, "mcp-config", "", "path to MCP server registry (default: $MCP_CONFIG or core/config/mcp.yaml)")
	return cmd
}

// skillInvoker bridges sentinel.SkillInvoker → skills.Runner. Tiny shim so
// the sentinel package doesn't depend on skills.
type skillInvoker struct {
	runner *skills.Runner
}

func (s skillInvoker) InvokeSkill(ctx context.Context, name string, args map[string]any) (string, error) {
	if s.runner == nil {
		return "", fmt.Errorf("no skills runner configured")
	}
	res, _, err := s.runner.Invoke(ctx, "", name, args, "sentinel")
	return res.Stdout, err
}

// heartbeatInterval reads $INFINITY_HEARTBEAT_INTERVAL (Go duration form,
// e.g. "30m"). Defaults to 30 minutes.
func heartbeatInterval() time.Duration {
	v := os.Getenv("INFINITY_HEARTBEAT_INTERVAL")
	if v == "" {
		return 30 * time.Minute
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 30 * time.Minute
	}
	return d
}
