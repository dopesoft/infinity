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

			// Memory + hooks + tools wiring (best-effort).
			var (
				pool        *pgxpool.Pool
				store       *memory.Store
				searcher    *memory.Searcher
				compressor  *memory.Compressor
				procedural  *memory.ProceduralStore
				pipeline    *hooks.Pipeline
				embedder    embed.Embedder
				llmRegistry *llm.Registry
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

					// OAuth-backed OpenAI provider needs a pool-backed token
					// store; FromEnv returns nil for this case so we build it
					// here once the pool is up. The first inference call will
					// surface a clean error if the user hasn't connected yet
					// via Studio's "Connect ChatGPT" flow.
					if provider == nil && llm.IsOpenAIOAuth() {
						oauthStore := llm.NewOAuthStore(p)
						provider = llm.NewOpenAIOAuth(oauthStore, llm.ModelForVendor("openai_oauth"))
						fmt.Printf("  llm: openai_oauth provider attached (paste-flow connect via Studio)\n")
					}

					// Build the multi-provider registry so the Settings PUT
					// can hot-swap between any vendor whose creds are wired.
					// The OAuthStore is shared so flipping anthropic ↔
					// openai_oauth never wipes mem_provider_tokens — re-auth
					// is not required to switch back.
					oauthStoreShared := llm.NewOAuthStore(p)
					llmRegistry = llm.BuildRegistry(oauthStoreShared)
					fmt.Printf("  llm: registered %v\n", llmRegistry.Available())

					// Compressor needs an Anthropic client; wire only if the
					// active provider is Anthropic so we don't pin a 2nd key.
					if a, ok := provider.(*llm.Anthropic); ok {
						summarizerModel := os.Getenv("LLM_SUMMARIZE_MODEL")
						summarizer := llm.NewAnthropicSummarizer(a, summarizerModel)
						compressor = memory.NewCompressor(p, embedder, memory.NewSummarizer(summarizer))
					}

					pipeline = hooks.NewPipeline()
					hooks.RegisterDefaults(pipeline, p, store, embedder, compressor)

					// Predict-then-act: every PreToolUse writes an expected
					// outcome; PostToolUse resolves with a surprise score.
					// High-surprise rows feed the curiosity scanner + Voyager
					// curriculum. JEPA discipline without a generative world
					// model — see core/internal/memory/predictions.go.
					predictions := memory.NewPredictionStore(p)
					hooks.NewPredictionRecorder(predictions).Register(pipeline)

					tools.RegisterMemoryTools(registry, p, embedder, searcher)
					tools.RegisterSkillTools(registry, p)
					tools.RegisterDashboardTools(registry, p)
					// Generic dashboard surface contract (Rule #1 substrate):
					// surface_item / surface_update. The standard boundary any
					// skill recipe / connector / cron writes through to put a
					// ranked, structured item in front of the boss.
					tools.RegisterSurfaceTools(registry, p)
					// Durable workflow tools (Phase 2 substrate): workflow_create
					// / _run / _status / _resume / _cancel / _list / _validate.
					// The agent assembles multi-step processes; the engine
					// (wired below, after the loop exists) runs them.
					tools.RegisterWorkflowTools(registry, p)
					// Verification substrate (Phase 4): eval_record /
					// eval_scorecard. How the agent learns whether what it
					// assembled actually works, and catches regressions.
					eval.RegisterTools(registry, eval.NewStore(p, slog.Default()))
					// World model + agent-owned goals (Phase 5): entity_* +
					// goal_* tools. A structured model of the boss's world,
					// and the agent's own durable objectives.
					worldmodel.RegisterTools(registry, worldmodel.NewStore(p, slog.Default()))

					fmt.Printf("  memory: enabled (embedder=%s, compressor=%v, procedural=on, predictions=on)\n", embedder.Name(), compressor != nil)
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
			// Seed default scaffold skills (scaffold-nextjs, -vite-react,
			// -static-html, -ios-swift, -capacitor) into the on-disk root
			// when they're missing. Never overwrites a file the boss has
			// touched. On Railway's ephemeral filesystem this means the
			// canonical agent-facing scaffolds are always present.
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

				// Re-hydrate auto-evolved skills from Postgres BEFORE the
				// filesystem walk. Voyager writes promoted skills to both
				// disk and mem_skill_versions; this materializer re-creates
				// the disk file from the DB whenever the file is missing
				// or drifted, so Railway's ephemeral container filesystem
				// never causes skill loss between deploys.
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

			// Runtime self-extension (Phase 3 substrate). The agent wires
			// new MCP servers / REST-API tools at runtime via the
			// extension_* tools; LoadAll re-activates everything a prior
			// session registered. Runs AFTER the embedded mcp.yaml connect
			// so a runtime extension layers cleanly on top.
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

			// trustStore is created below with the rest of the proactive
			// stack, but the agent gate needs it now. Build it eagerly so the
			// gate can route claude_code__* calls through the Trust queue.
			var earlyTrust *proactive.TrustStore
			if pool != nil {
				earlyTrust = proactive.NewTrustStore(pool)
			}

			// Honcho — optional dialectic peer-modelling sidecar. When
			// HONCHO_BASE_URL is set we register a memory provider that folds
			// the boss's peer representation into the system prefix, plus a
			// hook that mirrors user/assistant turns into Honcho so its
			// reasoning pipeline keeps the representation fresh.
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

			// Session auto-naming. Uses Haiku to turn the first user/assistant
			// exchange into a 3-7 word title so the Live sessions drawer
			// stops showing `chs3-djnc`-style hex slugs. Cheap (~1 Haiku
			// call per new session, async, idempotent). Requires Anthropic
			// provider + DB pool; degrades to no-op otherwise.
			var sessionNamer *sessions.Namer
			if pool != nil {
				if a, ok := provider.(*llm.Anthropic); ok {
					sessionNamer = sessions.NewNamer(pool, a, os.Getenv("INFINITY_SESSION_NAME_MODEL"))
					fmt.Printf("  sessions: auto-naming via Haiku enabled\n")
				}
			}

			// Connectors cache: live picture of Composio connected accounts +
			// boss-assigned aliases. Powers the multi-account routing block
			// the agent loop injects into its system prompt so the model
			// can pick the right account when a tool exposes per-account
			// `connected_account_id` parameters. Started later so the
			// background refresh ticker is tied to the serve context.
			var connectorsCache *connectors.Cache
			if pool != nil {
				connectorsCache = connectors.New(pool, func() string {
					if v := strings.TrimSpace(os.Getenv("COMPOSIO_ADMIN_API_KEY")); v != "" {
						return v
					}
					return strings.TrimSpace(os.Getenv("COMPOSIO_API_KEY"))
				})
			}

			// Persisted token usage. Migration 013 added the columns;
			// UsagePersistence reads/writes them so the context meter
			// survives Railway container rotation. Nil-safe in the
			// agent loop, so we only build when the pool exists.
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
				// Compose memory providers: Infinity's RRF searcher always
				// runs first, Honcho's peer representation folds in second
				// when configured. Order matters — searcher emits the boss
				// profile primer + relevant memory; Honcho's reasoning sits
				// below it in the system prompt for clear separation.
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
				if len(memProviders) > 0 {
					cfg.Memory = agent.NewCompositeMemory(memProviders...)
				}
				if pipeline != nil {
					cfg.Hooks = &hooks.PipelineAdapter{P: pipeline}
				}
				if earlyTrust != nil {
					// Gate chain: per-MCP authorization policies, all sharing
					// the same TrustStore so the boss sees a single approval
					// queue in Studio. Order matters only for tools that match
					// multiple gates (none today) — first non-allow decision
					// wins.
					//
					//   ClaudeCodeGate → claude_code__*  (home Mac shell/edit/write)
					//   GitHubGate     → github__*       (direct github-mcp-server)
					//   ComposioGate   → composio__*     (Composio gateway, all SaaS
					//                                     toolkits — pattern-based
					//                                     write-verb detection)
					cfg.Gate = agent.NewGateChain(
						proactive.NewClaudeCodeGate(earlyTrust),
						proactive.NewGitHubGate(earlyTrust),
						proactive.NewComposioGate(earlyTrust),
					)
				}
				loop = agent.New(cfg)
				// Register the delegate + delegate_parallel sub-agent
				// spawners now that the loop exists. They live in the
				// agent package (need direct Loop access) but register
				// into the same tools.Registry the loop uses, so the
				// model sees them like any other tool.
				registry.Register(&agent.Delegate{Loop: loop})
				registry.Register(&agent.DelegateParallel{Loop: loop})
				// Compaction tool: rewrites the active session's
				// message history, folding older turns into
				// mem_observations (which the compressor promotes to
				// mem_memories). Auto-trigger also reads this struct
				// via Loop.SetCompactor for the >= 80% threshold path.
				if pool != nil && provider != nil {
					convCompactor := memory.NewConversationCompactor(store, provider)
					registry.Register(&agent.CompactContext{Loop: loop, Compactor: convCompactor})
					loop.SetCompactor(convCompactor)
				}
			}

			// Durable workflow engine — Phase 2 substrate. The agent
			// assembles multi-step processes via the workflow_* tools; this
			// background worker advances each run one step per tick,
			// persisting after every step so a restart resumes mid-flow.
			// Checkpoint steps surface a card on the dashboard via the
			// surface contract.
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
				// Auto-record every run's outcome to the verification
				// substrate, so workflow scorecards sit next to skill/tool ones.
				wfEngine = wfEngine.WithEvalRecorder(
					&workflowEvalRecorder{store: eval.NewStore(pool, slog.Default())},
				)
				wfEngine.Start(cmd.Context())
				fmt.Printf("  workflows: engine started (durable, resumable)\n")
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
				walStore       *proactive.WAL
				workingBuf     *proactive.WorkingBuffer
			)
			if pool != nil {
				intentDB = intent.NewStore(pool)
				// Reuse the early trust store so gate + API see the same
				// instance. NewTrustStore is stateless (just a pool wrapper)
				// so this is safe even if earlyTrust wasn't built.
				if earlyTrust != nil {
					trustStore = earlyTrust
				} else {
					trustStore = proactive.NewTrustStore(pool)
				}
				heartbeat = proactive.NewHeartbeat(pool, heartbeatInterval(),
					proactive.ComposeChecklists(
						proactive.DefaultChecklist(pool),
						// Curiosity gap-scan: scan memory for low-confidence
						// nodes, unresolved contradictions, uncovered graph
						// mentions, and high-surprise predictions, then
						// surface the top-K as Findings. CoALA's active
						// learning loop, populated.
						proactive.CuriosityChecklist(pool),
						// Autonomous pursuit (Phase 5): resurface the agent's
						// own goals that are blocked, due soon, or stalled —
						// so a goal it set and forgot gets revisited.
						proactive.AgentGoalChecklist(pool),
						// Substrate → dashboard: mirror the agent's goals and
						// anything broken (failed extensions, regressed
						// capabilities) onto the generic surface contract, so
						// they render on the dashboard with zero bespoke
						// Studio code. Rule #1 applied to the substrate itself.
						proactive.SubstrateSurfaceChecklist(pool),
					))
				heartbeat.Start(cmd.Context())
				if a, ok := provider.(*llm.Anthropic); ok {
					intentDetector = intent.New(intent.Config{
						Provider: a,
						Model:    os.Getenv("INFINITY_INTENT_MODEL"),
					})
				}
				/* WAL + WorkingBuffer are the durable substrates for
				 * compaction-recovery and load-bearing-fragment capture.
				 * Both are stateless pool wrappers — safe to share
				 * across the WS handler and Studio API readers. */
				walStore = proactive.NewWAL(pool)
				workingBuf = proactive.NewWorkingBuffer(pool, 0)
				proactiveAPI = proactive.NewAPI(pool, heartbeat, trustStore, intentDB)
				fmt.Printf("  proactive: heartbeat every %s, intent=%v, trust=ready, wal=on, buffer=on\n",
					heartbeat.Interval(), intentDetector != nil)
			}
			/* IntentFlow is now wired into the WS turn handler — every
			 * user message is classified async (Haiku JSON call),
			 * persisted to mem_intent_decisions, and emitted as an
			 * `intent` WS frame for Studio's IntentStream panel. The
			 * detector is passed into server.Config below. */

			// Connector poller — deterministic Composio tools.execute path
			// (no LLM) that connector_poll cron jobs ride on. Reads the
			// same admin/consumer key resolution the connectors cache uses
			// so a Railway env swap propagates without restart.
			var connectorPoller *connectors.Poller
			if pool != nil {
				execClient := connectors.NewExecuteClient(func() string {
					if v := strings.TrimSpace(os.Getenv("COMPOSIO_ADMIN_API_KEY")); v != "" {
						return v
					}
					return strings.TrimSpace(os.Getenv("COMPOSIO_API_KEY"))
				})
				connectorPoller = connectors.NewPoller(pool, execClient, pipeline)
				fmt.Println("  connector poller: ready (composio tools.execute)")
			}

			// Cron scheduler + Sentinel manager. Both degrade gracefully when
			// no DB pool is configured. The scheduler now runs a composite
			// executor — agent jobs (system_event / isolated_agent_turn) go
			// to the agent loop, connector_poll jobs to the poller. Either
			// half is optional; missing handlers surface as last_run_status.
			var (
				cronScheduler *cron.Scheduler
				sentinelMgr   *sentinel.Manager
				cronAPI       *cron.API
				sentinelAPI   *sentinel.API
			)
			if pool != nil {
				var agentExec cron.Executor
				if loop != nil {
					agentExec = cron.NewAgentExecutor(loop)
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
					// source_extract is the third Voyager hook — drafts a
					// code-refactor proposal when the boss visibly fought
					// the same file during a session. Lands rows in
					// mem_code_proposals for review in Studio.
					pipeline.RegisterFunc("voyager.source_extract", voyagerMgr.OnSessionEndSource, hooks.SessionEnd)
				}
				// Promotion → procedural memory: every promoted skill writes
				// a procedural-tier memory so the agent retrieves it through
				// the same RRF pathway as semantic facts. CoALA's procedural
				// tier, activated.
				if procedural != nil {
					voyagerMgr.OnSkillPromoted(func(ctx context.Context, name, description, skillMD string) {
						if err := procedural.UpsertFromSkill(ctx, name, description, skillMD, 7); err != nil {
							fmt.Fprintf(os.Stderr, "warning: procedural upsert %s: %v\n", name, err)
						}
					})
				}
				voyagerAPI = voyager.NewAPI(voyagerMgr)
				fmt.Printf("  voyager: %s\n", voyagerMgr.Status())

				// Auto-trigger: when GEPA_URL is configured, run a background
				// ticker that watches mem_skill_runs and fires the optimizer
				// for any skill whose recent failure rate crosses the
				// threshold. This is the close-the-loop step Voyager was
				// missing — without it, GEPA only fires when someone POSTs
				// /api/voyager/optimize by hand.
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

			// Voice (OpenAI Realtime over WebRTC). nil-safe — when
			// OPENAI_API_KEY isn't set, voice.New() returns nil and
			// the /api/voice/* endpoints simply 503.
			voiceMinter := voice.New()
			if voiceMinter != nil {
				fmt.Printf("  voice: realtime enabled (model=%s, voice=%s)\n", voiceMinter.Model(), voiceMinter.Voice())
			}

			// Push notifications. Sender requires VAPID env vars; when
			// they're missing we still expose the API so Studio can show
			// "not configured" instead of 404'ing. Store works whenever
			// the pool is up — subscriptions can land in advance of the
			// VAPID key being provisioned.
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

			// Wire trust → push so an approval queued for the boss
			// surfaces as a banner on every subscribed device. No-op
			// when sender isn't configured.
			if trustStore != nil && pushSender != nil {
				trustStore.SetNotifier(push.NewTrustAdapter(pushSender))
				fmt.Println("  push: trust → notification wired")
			}

			// Initiative + economics substrate (Phase 6, final). The agent
			// reaches the boss through an urgency policy (notify), batches
			// low-priority updates (notification_digest), and tracks what it
			// spends (cost_record / budget_status). Wired here, after the
			// push Sender exists, so urgent notifications can reach the phone.
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

			// Dashboard aggregator. Reads from migration-014 tables;
			// 200 OK with empty arrays when those tables are empty so
			// Studio can fall back to its local mock fixture.
			var dashboardAPI *dashboard.API
			if pool != nil {
				dashboardAPI = dashboard.NewAPI(pool, nil)
				fmt.Println("  dashboard: aggregator wired")
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
			})

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// Start the connectors cache background refresh now that the
			// serve context exists. Synchronously primes once so the first
			// turn after boot already sees connected-account state.
			if connectorsCache != nil {
				connectorsCache.Start(ctx)
				defer connectorsCache.Stop()
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

// cronSchedulerAdapter bridges *cron.Scheduler → tools.CronScheduler.
// The tools package can't import cron directly (would cycle through
// agent), so we translate between cron.Job and tools.CronJob here. The
// adapter is stateless — every call passes through to the wrapped
// scheduler with minimal copy overhead.
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
