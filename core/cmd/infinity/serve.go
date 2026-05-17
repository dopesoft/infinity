package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dopesoft/infinity/core/config"
	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/auth"
	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/connectors"
	"github.com/dopesoft/infinity/core/internal/cron"
	"github.com/dopesoft/infinity/core/internal/dashboard"
	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/dopesoft/infinity/core/internal/eval"
	"github.com/dopesoft/infinity/core/internal/extensions"
	"github.com/dopesoft/infinity/core/internal/honcho"
	"github.com/dopesoft/infinity/core/internal/hooks"
	"github.com/dopesoft/infinity/core/internal/initiative"
	"github.com/dopesoft/infinity/core/internal/intent"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/plasticity"
	"github.com/dopesoft/infinity/core/internal/proactive"
	"github.com/dopesoft/infinity/core/internal/push"
	"github.com/dopesoft/infinity/core/internal/sentinel"
	"github.com/dopesoft/infinity/core/internal/server"
	"github.com/dopesoft/infinity/core/internal/sessions"
	"github.com/dopesoft/infinity/core/internal/settings"
	"github.com/dopesoft/infinity/core/internal/skills"
	"github.com/dopesoft/infinity/core/internal/soul"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/dopesoft/infinity/core/internal/voice"
	"github.com/dopesoft/infinity/core/internal/voyager"
	"github.com/dopesoft/infinity/core/internal/workflow"
	"github.com/dopesoft/infinity/core/internal/worldmodel"
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

			var (
				pool                *pgxpool.Pool
				store               *memory.Store
				searcher            *memory.Searcher
				compressor          *memory.Compressor
				procedural          *memory.ProceduralStore
				pipeline            *hooks.Pipeline
				embedder            embed.Embedder
				llmRegistry         *llm.Registry
				activeBridgeRouter  *bridge.Router
				activeBridgePrefs   tools.PreferenceFetcher
				notifySkillPromoted func(name, description string)
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
					procedural = memory.NewProceduralStore(p, embedder)
					searcher.AttachProcedural(procedural)

					if provider == nil && llm.IsOpenAIOAuth() {
						oauthStore := llm.NewOAuthStore(p)
						provider = llm.NewOpenAIOAuth(oauthStore, llm.ModelForVendor("openai_oauth"))
						fmt.Printf("  llm: openai_oauth provider attached (paste-flow connect via Studio)\n")
					}

					oauthStoreShared := llm.NewOAuthStore(p)
					llmRegistry = llm.BuildRegistry(oauthStoreShared)
					fmt.Printf("  llm: registered %v\n", llmRegistry.Available())

					if a, ok := provider.(*llm.Anthropic); ok {
						summarizerModel := os.Getenv("LLM_SUMMARIZE_MODEL")
						summarizer := llm.NewAnthropicSummarizer(a, summarizerModel)
						compressor = memory.NewCompressor(p, embedder, memory.NewSummarizer(summarizer))
					}

					pipeline = hooks.NewPipeline()
					hooks.RegisterDefaults(pipeline, p, store, embedder, compressor)

					predictions := memory.NewPredictionStore(p)
					hooks.NewPredictionRecorder(predictions).Register(pipeline)

					tools.RegisterMemoryTools(registry, p, embedder, searcher)
					tools.RegisterTraceTools(registry, p)
					tools.RegisterSkillTools(registry, p)
					tools.RegisterDashboardTools(registry, p)
					tools.RegisterCuriosityTools(registry, p)
					tools.RegisterSystemMap(registry, p)
					tools.RegisterDomainHintTools(registry, p)

					macURL := strings.TrimSpace(os.Getenv("CLAUDE_CODE_TUNNEL_URL"))
					cloudURL := strings.TrimSpace(os.Getenv("WORKSPACE_BRIDGE_URL"))
					if cloudURL == "" && strings.TrimSpace(os.Getenv("RAILWAY_ENVIRONMENT_NAME")) != "" {
						cloudURL = "http://workspace.railway.internal:8080"
					}
					macBridge := bridge.NewMacBridge(
						macURL,
						os.Getenv("CF_ACCESS_CLIENT_ID"),
						os.Getenv("CF_ACCESS_CLIENT_SECRET"),
					)
					cloudBridge := bridge.NewCloudBridge(
						cloudURL,
						os.Getenv("WORKSPACE_BRIDGE_TOKEN"),
					)
					activeBridgeRouter = bridge.NewRouter(macBridge, cloudBridge)
					activeBridgePrefs = tools.NewDBPreferenceFetcher(p)
					tools.RegisterBridgeTools(registry, activeBridgeRouter, activeBridgePrefs)
					tools.RegisterArtifactTools(registry, p)
					tools.RegisterProjectTools(registry, p, activeBridgeRouter, activeBridgePrefs)
					macStatusStr := "unset"
					if macURL != "" {
						macStatusStr = "configured"
					}
					cloudStatusStr := "unset"
					if cloudURL != "" {
						cloudStatusStr = "configured"
					}
					fmt.Printf("  bridges: mac=%s cloud=%s\n", macStatusStr, cloudStatusStr)
					tools.RegisterMemSubstrate(registry, p)
					tools.RegisterSurfaceTools(registry, p)
					tools.RegisterWorkflowTools(registry, p)
					eval.RegisterTools(registry, eval.NewStore(p, slog.Default()))
					worldmodel.RegisterTools(registry, worldmodel.NewStore(p, slog.Default()))

					fmt.Printf("  memory: enabled (embedder=%s, compressor=%v, procedural=on, predictions=on)\n", embedder.Name(), compressor != nil)
				}
			} else {
				fmt.Fprintf(os.Stderr, "  memory: disabled (no DATABASE_URL)\n")
			}

			skillsRoot := os.Getenv("INFINITY_SKILLS_ROOT")
			if skillsRoot == "" {
				skillsRoot = "./skills"
			}
			if planted, err := config.MaterializeScaffoldSkills(skillsRoot); err != nil {
				fmt.Fprintf(os.Stderr, "warning: materialize scaffold skills: %v\n", err)
			} else if len(planted) > 0 {
				fmt.Printf("  skills: seeded scaffolds %v\n", planted)
			}
			skillRegistry := skills.NewRegistry(skillsRoot)
			var skillStore *skills.Store
			if pool != nil {
				skillStore = skills.NewStore(pool)
				skillRegistry.AttachStore(skillStore)

				mctx, mcancel := context.WithTimeout(cmd.Context(), 10*time.Second)
				if written, err := skills.MaterializeActiveSkills(mctx, pool, skillsRoot); err != nil {
					fmt.Fprintf(os.Stderr, "warning: materialize skills: %v\n", err)
				} else if written > 0 {
					fmt.Printf("  skills: re-materialized %d auto-evolved skill(s) from Postgres\n", written)
				}
				mcancel()
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

			if pool != nil {
				extManager := extensions.NewManager(
					extensions.NewStore(pool, slog.Default()), registry, mcp, slog.Default(),
				)
				if n, err := extManager.LoadAll(cmd.Context()); err != nil {
					fmt.Fprintf(os.Stderr, "warning: extensions load: %v\n", err)
				} else if n > 0 {
					fmt.Printf("  extensions: re-activated %d runtime extension(s)\n", n)
				}
				extensions.RegisterTools(registry, extManager)
			}

			soulPrompt, soulSource := soul.Load()
			fmt.Printf("  soul: %s (%d chars)\n", soulSource, len(soulPrompt))

			var earlyTrust *proactive.TrustStore
			if pool != nil {
				earlyTrust = proactive.NewTrustStore(pool)
			}

			honchoClient := honcho.FromEnv()
			if honchoClient.Enabled() {
				fmt.Printf("  honcho: enabled (workspace=%s peer=%s)\n",
					honchoClient.Workspace(), honchoClient.Peer())
				if pipeline != nil {
					honchoMirror := func(ctx context.Context, ev hooks.Event) error {
						role := "user"
						if ev.Name == hooks.TaskCompleted {
							role = "assistant"
						}
						return honchoClient.PostMessage(ctx, honcho.Message{
							SessionID: ev.SessionID,
							Content:   ev.Text,
							Role:      role,
						})
					}
					pipeline.RegisterFunc("honcho.user", honchoMirror, hooks.UserPromptSubmit)
					pipeline.RegisterFunc("honcho.assistant", honchoMirror, hooks.TaskCompleted)
				}
			} else {
				fmt.Printf("  honcho: disabled (set HONCHO_BASE_URL to enable)\n")
			}

			var sessionNamer *sessions.Namer
			if pool != nil {
				if a, ok := provider.(*llm.Anthropic); ok {
					sessionNamer = sessions.NewNamer(pool, a, os.Getenv("INFINITY_SESSION_NAME_MODEL"))
					fmt.Printf("  sessions: auto-naming via Haiku enabled\n")
				}
			}

			composioKeyFn := func() string {
				if v := strings.TrimSpace(os.Getenv("COMPOSIO_ADMIN_API_KEY")); v != "" {
					return v
				}
				return strings.TrimSpace(os.Getenv("COMPOSIO_API_KEY"))
			}
			var (
				connectorsCache *connectors.Cache
				composioExec    *connectors.ExecuteClient
			)
			if pool != nil {
				connectorsCache = connectors.New(pool, composioKeyFn)
				composioExec = connectors.NewExecuteClient(composioKeyFn)
				tools.RegisterConnectorTools(registry, connectorsCache)
			}

			var usageStore *sessions.UsagePersistence
			if pool != nil {
				usageStore = sessions.NewUsagePersistence(pool)
			}

			var loop *agent.Loop
			if provider != nil {
				cfg := agent.Config{LLM: provider, Tools: registry, Skills: skillRegistry, SystemPrompt: soulPrompt, Namer: sessionNamer}
				if connectorsCache != nil {
					cfg.Accounts = connectorsCache
				}
				if usageStore != nil {
					cfg.UsageStore = usageStore
				}
				memProviders := []agent.MemoryProvider{}
				if searcher != nil {
					memProviders = append(memProviders, searcher)
				}
				if pool != nil {
					memProviders = append(memProviders, plasticity.NewProvider(pool))
				}
				if honchoClient.Enabled() {
					memProviders = append(memProviders, honcho.NewMemoryProvider(honchoClient))
				}
				if activeBridgeRouter != nil {
					memProviders = append(memProviders, &bridge.MemoryProvider{
						Router: activeBridgeRouter,
						Prefs:  bridge.PrefFetcher(activeBridgePrefs),
					})
				}
				if len(memProviders) > 0 {
					cfg.Memory = agent.NewCompositeMemory(memProviders...)
				}
				if pipeline != nil {
					cfg.Hooks = &hooks.PipelineAdapter{P: pipeline}
				}
				if earlyTrust != nil {
					cfg.Gate = agent.NewGateChain(
						proactive.NewClaudeCodeGate(earlyTrust),
						proactive.NewGitHubGate(earlyTrust),
						proactive.NewComposioGate(earlyTrust),
						proactive.NewBridgeGate(earlyTrust),
					)
				}
				loop = agent.New(cfg)
				registry.Register(&agent.Delegate{Loop: loop})
				registry.Register(&agent.DelegateParallel{Loop: loop})
				if pool != nil && provider != nil {
					convCompactor := memory.NewConversationCompactor(store, provider)
					registry.Register(&agent.CompactContext{Loop: loop, Compactor: convCompactor})
					loop.SetCompactor(convCompactor)
				}
				if pool != nil {
					loop.SetTurnRecorder(turnRecorderAdapter{store: memory.NewTurnStore(pool)})
				}
				if activeBridgeRouter != nil {
					loop.SetToolVisibility(makeBridgeToolVisibility(activeBridgeRouter, activeBridgePrefs))
				}
			}

			if pool != nil && loop != nil {
				wfStore := workflow.NewStore(pool, slog.Default())
				wfExec := &workflowExecutor{
					registry:    registry,
					skillRunner: skillRunner,
					loop:        loop,
				}
				wfEngine := workflow.NewEngine(wfStore, wfExec, slog.Default())
				wfEngine = wfEngine.WithCheckpointSurfacer(
					&checkpointSurfacer{store: surface.NewStore(pool, slog.Default())},
				)
				wfEngine = wfEngine.WithEvalRecorder(
					&workflowEvalRecorder{store: eval.NewStore(pool, slog.Default())},
				)
				wfEngine.Start(cmd.Context())
				fmt.Printf("  workflows: engine started (durable, resumable)\n")
			}

			var (
				intentDetector *intent.Detector
				intentDB       *intent.Store
				heartbeat      *proactive.Heartbeat
				trustStore     *proactive.TrustStore
				proactiveAPI   *proactive.API
				walStore       *proactive.WAL
				workingBuf     *proactive.WorkingBuffer
			)
			if pool != nil {
				intentDB = intent.NewStore(pool)
				if earlyTrust != nil {
					trustStore = earlyTrust
				} else {
					trustStore = proactive.NewTrustStore(pool)
				}
				heartbeat = proactive.NewHeartbeat(pool, heartbeatInterval(),
					proactive.ComposeChecklists(
						proactive.DefaultChecklist(pool),
						proactive.CuriosityChecklist(pool),
						proactive.AgentGoalChecklist(pool),
						proactive.SubstrateSurfaceChecklist(pool),
						proactive.ConnectorIdentityChecklist(connectorsCache),
					))
				heartbeat.Start(cmd.Context())
				if a, ok := provider.(*llm.Anthropic); ok {
					intentDetector = intent.New(intent.Config{
						Provider: a,
						Model:    os.Getenv("INFINITY_INTENT_MODEL"),
					})
				}
				walStore = proactive.NewWAL(pool)
				workingBuf = proactive.NewWorkingBuffer(pool, 0)
				proactiveAPI = proactive.NewAPI(pool, heartbeat, trustStore, intentDB)
				fmt.Printf("  proactive: heartbeat every %s, intent=%v, trust=ready, wal=on, buffer=on\n",
					heartbeat.Interval(), intentDetector != nil)
			}

			var connectorPoller *connectors.Poller
			if pool != nil && composioExec != nil {
				connectorPoller = connectors.NewPoller(pool, composioExec, pipeline)
				fmt.Println("  connector poller: ready (composio tools.execute)")
			}

			var (
				cronScheduler *cron.Scheduler
				sentinelMgr   *sentinel.Manager
				cronAPI       *cron.API
				sentinelAPI   *sentinel.API
			)
			if pool != nil {
				var agentExec cron.Executor
				if loop != nil {
					agentExec = cron.NewAgentExecutor(loop, settings.New(pool))
				}
				var connectorExec cron.Executor
				if connectorPoller != nil {
					connectorExec = cron.NewConnectorExecutor(connectorPoller)
				}
				cronScheduler = cron.New(pool, cron.NewCompositeExecutor(agentExec, connectorExec))
				if err := cronScheduler.Start(cmd.Context()); err != nil {
					fmt.Fprintf(os.Stderr, "warning: cron start: %v\n", err)
				}
				cronAPI = cron.NewAPI(cronScheduler)
				tools.RegisterCronTools(registry, cronSchedulerAdapter{s: cronScheduler}, pool)

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
					pipeline.RegisterFunc("voyager.source_extract", voyagerMgr.OnSessionEndSource, hooks.SessionEnd)
				}
				voyagerMgr.OnSkillPromoted(func(ctx context.Context, name, description, skillMD string) {
					if procedural != nil {
						if err := procedural.UpsertFromSkill(ctx, name, description, skillMD, 7); err != nil {
							fmt.Fprintf(os.Stderr, "warning: procedural upsert %s: %v\n", name, err)
						}
					}
					if notifySkillPromoted != nil {
						notifySkillPromoted(name, description)
					}
				})
				voyagerAPI = voyager.NewAPI(voyagerMgr)
				fmt.Printf("  voyager: %s\n", voyagerMgr.Status())

				autoTrigger := voyager.NewAutoTrigger(voyagerMgr, voyager.NewOptimizer())
				if autoTrigger.Enabled() {
					autoTrigger.Start(cmd.Context())
					fmt.Printf("  voyager.autotrigger: on\n")
				} else {
					fmt.Printf("  voyager.autotrigger: off (set GEPA_URL to enable)\n")
				}
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

			voiceMinter := voice.New()
			if voiceMinter != nil {
				fmt.Printf("  voice: realtime enabled (model=%s, voice=%s)\n", voiceMinter.Model(), voiceMinter.Voice())
			}

			var pushAPI *push.API
			var pushSender *push.Sender
			if pool != nil {
				pushStore := push.NewStore(pool)
				s, perr := push.NewSenderFromEnv(pushStore, nil)
				if perr != nil {
					fmt.Printf("  push: store ready; sender disabled (%v)\n", perr)
					pushAPI = push.NewAPI(pushStore, nil, nil)
				} else {
					fmt.Println("  push: VAPID configured, ready to deliver")
					pushSender = s
					pushAPI = push.NewAPI(pushStore, s, nil)
				}
			}

			if trustStore != nil && pushSender != nil {
				trustStore.SetNotifier(push.NewTrustAdapter(pushSender))
				fmt.Println("  push: trust → notification wired")
			}

			if pool != nil {
				initStore := initiative.NewStore(pool, slog.Default())
				initDeliverer := &initiativeDeliverer{
					sender:  pushSender,
					surface: surface.NewStore(pool, slog.Default()),
				}
				initNotifier := initiative.NewNotifier(initStore, initDeliverer, slog.Default())
				initiative.RegisterTools(registry, initNotifier, initStore)
				fmt.Println("  initiative: notify + cost tools wired")
			}

			var dashboardAPI *dashboard.API
			if pool != nil {
				dashboardAPI = dashboard.NewAPI(pool, nil)
				fmt.Println("  dashboard: aggregator wired")
			}

			var turnStore *memory.TurnStore
			if pool != nil {
				turnStore = memory.NewTurnStore(pool)
			}
			srv := server.New(server.Config{
				Addr:           addr,
				Version:        version,
				Loop:           loop,
				MCP:            mcp,
				Pool:           pool,
				Store:          store,
				Searcher:       searcher,
				SkillsAPI:      skillsAPI,
				ProactiveAPI:   proactiveAPI,
				CronAPI:        cronAPI,
				SentinelAPI:    sentinelAPI,
				VoyagerAPI:     voyagerAPI,
				Auth:           authVerifier,
				Trust:          trustStore,
				Namer:          sessionNamer,
				IntentDetector: intentDetector,
				IntentStore:    intentDB,
				WAL:            walStore,
				WorkingBuffer:  workingBuf,
				Heartbeat:      heartbeat,
				LLMRegistry:    llmRegistry,
				Connectors:     connectorsCache,
				Voice:          voiceMinter,
				PushAPI:        pushAPI,
				DashboardAPI:   dashboardAPI,
				BridgeRouter:   activeBridgeRouter,
				BridgePrefs:    activeBridgePrefs,
				Turns:          turnStore,
			})

			notifySkillPromoted = srv.BroadcastSkillPromoted

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			if connectorsCache != nil {
				connectorsCache.Start(ctx)
				defer connectorsCache.Stop()
			}

			if connectorsCache != nil && composioExec != nil {
				verbSync := &tools.ComposioVerbSync{
					Reg:   registry,
					Cache: connectorsCache,
					Exec:  composioExec,
					KeyFn: composioKeyFn,
				}
				syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
				added, _, toolkits, err := verbSync.Sync(syncCtx)
				syncCancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: composio verb sync: %v\n", err)
				}
				if added > 0 {
					fmt.Printf("  composio: registered %d verbs across %d toolkit(s) (dormant in catalog)\n", added, toolkits)
				}
				connectorsCache.SetOnChange(func() {
					reCtx, reCancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer reCancel()
					if _, _, _, err := verbSync.Sync(reCtx); err != nil {
						fmt.Fprintf(os.Stderr, "warning: composio verb resync: %v\n", err)
					}
				})
			}

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

func makeBridgeToolVisibility(router *bridge.Router, prefs tools.PreferenceFetcher) agent.ToolVisibilityFunc {
	if router == nil {
		return nil
	}
	return func(ctx context.Context, sessionID string) map[string]struct{} {
		pref := bridge.PrefAuto
		if prefs != nil {
			pref = prefs(ctx, sessionID)
		}
		active, _, err := router.For(ctx, pref)
		if err != nil || active == nil {
			return nil
		}
		if active.Name() != bridge.KindCloud {
			return nil
		}
		hidden := map[string]struct{}{}
		for _, n := range allClaudeCodeToolNames {
			hidden[n] = struct{}{}
		}
		return hidden
	}
}

var allClaudeCodeToolNames = []string{
	"claude_code__Agent",
	"claude_code__AskUserQuestion",
	"claude_code__Bash",
	"claude_code__CronCreate",
	"claude_code__CronDelete",
	"claude_code__CronList",
	"claude_code__Edit",
	"claude_code__EnterPlanMode",
	"claude_code__EnterWorktree",
	"claude_code__ExitPlanMode",
	"claude_code__ExitWorktree",
	"claude_code__Glob",
	"claude_code__Grep",
	"claude_code__LS",
	"claude_code__Monitor",
	"claude_code__NotebookEdit",
	"claude_code__PushNotification",
	"claude_code__Read",
	"claude_code__RemoteTrigger",
	"claude_code__ScheduleWakeup",
	"claude_code__ShareOnboardingGuide",
	"claude_code__Skill",
	"claude_code__TaskOutput",
	"claude_code__TaskStop",
	"claude_code__TodoWrite",
	"claude_code__ToolSearch",
	"claude_code__WebFetch",
	"claude_code__WebSearch",
	"claude_code__Write",
}

type turnRecorderAdapter struct{ store *memory.TurnStore }

func (a turnRecorderAdapter) Open(ctx context.Context, sessionID, userText, model string) (string, error) {
	return a.store.Open(ctx, sessionID, userText, model)
}
func (a turnRecorderAdapter) Close(ctx context.Context, turnID string, f agent.TurnCloseFields) error {
	return a.store.Close(ctx, turnID, memory.CloseFields{
		AssistantText: f.AssistantText,
		StopReason:    f.StopReason,
		InputTokens:   f.InputTokens,
		OutputTokens:  f.OutputTokens,
		ToolCallCount: f.ToolCallCount,
		Status:        f.Status,
		Error:         f.Error,
		Summary:       f.Summary,
	})
}
func (a turnRecorderAdapter) IncrementToolCalls(ctx context.Context, turnID string) error {
	return a.store.IncrementToolCalls(ctx, turnID)
}

type cronSchedulerAdapter struct{ s *cron.Scheduler }

func (a cronSchedulerAdapter) toTools(j cron.Job) tools.CronJob {
	return tools.CronJob{
		ID: j.ID, Name: j.Name, Schedule: j.Schedule,
		ScheduleNatural: j.ScheduleNatural,
		JobKind:         string(j.JobKind),
		Target:          j.Target, TargetConfig: j.TargetConfig,
		Enabled: j.Enabled, MaxRetries: j.MaxRetries, BackoffSeconds: j.BackoffSeconds,
		LastRunStatus: j.LastRunStatus,
	}
}

func (a cronSchedulerAdapter) toCron(j tools.CronJob) cron.Job {
	return cron.Job{
		ID: j.ID, Name: j.Name, Schedule: j.Schedule,
		ScheduleNatural: j.ScheduleNatural,
		JobKind:         cron.JobKind(j.JobKind),
		Target:          j.Target, TargetConfig: j.TargetConfig,
		Enabled: j.Enabled, MaxRetries: j.MaxRetries, BackoffSeconds: j.BackoffSeconds,
	}
}

func (a cronSchedulerAdapter) Upsert(ctx context.Context, j tools.CronJob) (string, error) {
	return a.s.Upsert(ctx, a.toCron(j))
}
func (a cronSchedulerAdapter) Delete(ctx context.Context, id string) error {
	return a.s.Delete(ctx, id)
}
func (a cronSchedulerAdapter) List(ctx context.Context) ([]tools.CronJob, error) {
	jobs, err := a.s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tools.CronJob, len(jobs))
	for i, j := range jobs {
		out[i] = a.toTools(j)
	}
	return out, nil
}
func (a cronSchedulerAdapter) RunOnce(ctx context.Context, j tools.CronJob) error {
	return a.s.RunOnce(ctx, a.toCron(j))
}
func (a cronSchedulerAdapter) Reload(ctx context.Context) error {
	return a.s.Reload(ctx)
}

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
