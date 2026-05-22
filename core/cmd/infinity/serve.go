package main

import (
	"context"
	"fmt"
	"log"
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
	"github.com/dopesoft/infinity/core/internal/browser"
	"github.com/dopesoft/infinity/core/internal/calendar"
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
	"github.com/dopesoft/infinity/core/internal/maintenance"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/plasticity"
	"github.com/dopesoft/infinity/core/internal/proactive"
	"github.com/dopesoft/infinity/core/internal/proposals"
	"github.com/dopesoft/infinity/core/internal/push"
	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/dopesoft/infinity/core/internal/sentinel"
	"github.com/dopesoft/infinity/core/internal/server"
	"github.com/dopesoft/infinity/core/internal/sessions"
	"github.com/dopesoft/infinity/core/internal/settings"
	"github.com/dopesoft/infinity/core/internal/skills"
	"github.com/dopesoft/infinity/core/internal/soul"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/dopesoft/infinity/core/internal/triage"
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
				pool                *pgxpool.Pool
				store               *memory.Store
				searcher            *memory.Searcher
				compressor          *memory.Compressor
				reflector           *memory.Reflector
				procedural          *memory.ProceduralStore
				pipeline            *hooks.Pipeline
				embedder            embed.Embedder
				llmRegistry         *llm.Registry
				activeBridgeRouter  *bridge.Router
				activeBridgePrefs   tools.PreferenceFetcher
				browserReg          *browser.Registry
				docCreate           *tools.DocumentCreate
				workspaceRawBase    string // cloud workspace URL for the doc download proxy
				workspaceToken      string
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

					// Stranded-turn recovery: every `mem_turns` row still flagged
					// in_flight at boot is by definition orphaned (single-process
					// core — there is no concurrent instance still running it).
					// Mark each errored AND insert a synthetic TaskCompleted
					// observation so the chat reload at /api/sessions/:id/messages
					// renders a clear "(interrupted)" assistant turn where the
					// silent gap used to be. Without this, the boss reloads after
					// a deploy / crash and sees only their own prompt — no reply,
					// no error, no signal that the agent isn't still thinking. Run
					// it before the WS handlers come online so the very first
					// reconnect already sees the recovered state.
					rctx, rcancel := context.WithTimeout(cmd.Context(), 10*time.Second)
					if n, err := memory.NewTurnStore(p).RecoverStranded(rctx); err != nil {
						log.Printf("turn recovery: %v", err)
					} else if n > 0 {
						infoLog := log.New(os.Stdout, "", log.LstdFlags)
						infoLog.Printf("turn recovery: closed %d stranded in_flight turn(s) from prior boot", n)
					}
					rcancel()

					// Learning hub - turns dashboard interactions into
					// procedural memories. When the boss bulk-dismisses
					// N+ questions in the same pattern_key, a row in
					// mem_memories tier='procedural' lands with
					// "don't emit X-class without a fundamentally new
					// signal" content. Detectors consult the hub via
					// IsPatternSuppressed before emitting; the agent's
					// system prompt also surfaces the memory via RRF so
					// reasoning naturally avoids the topic. Single
					// package-level hub so the proactive package's emit
					// + dismiss endpoints don't have to be re-wired.
					proactive.SetHub(proactive.NewLearningHub(p, procedural, slog.Default()))

					// Defense-in-depth: also sweep stale questions/findings
					// during nightly consolidation. Heartbeat already does
					// this every tick (minutes); this catches the
					// degenerate case where heartbeat is paused for some
					// reason. Single source of cleanup truth.
					proactive.RegisterAsConsolidateHook(memory.RegisterConsolidateHook, slog.Default())

					// Self-model rollup: every nightly pass aggregates
					// today's quality / cost / surprise / per-skill /
					// per-tool numbers into mem_agent_metrics, then
					// recomputes the 14-day baseline. The
					// SelfModelProvider reads this at turn time and only
					// surfaces a block when drift > 2σ - the agent
					// notices itself, but only when it should.
					memory.RegisterConsolidateHook(memory.RollupAgentMetrics)

					// Opt 7: every UpsertCandidate writes a mem_memories
					// twin (tier='working') for the draft body. Embedded,
					// auto-linked via A-MEM associative edges. Agent
					// reasoning can RRF-recall "I have a pending refinement
					// to gmail_triage that adds retries" mid-turn instead
					// of forgetting drafts exist between sessions.
					proposals.SetDraftMemoryWriter(memory.NewSkillDraftMemoryWriter(p, embedder, slog.Default()))

					// Opt 3: when skill_optimize produces a merge with
					// flagged conflicts, hand (existing | proposed |
					// merged) to a Haiku critic for an empirical pick
					// instead of blindly trusting the merge. Same
					// Anthropic provider the merge itself uses.
					if a, ok := llm.Unwrap(provider).(*llm.Anthropic); ok {
						proposals.SetMergeEvaluator(proposals.NewHaikuMergeEvaluator(a, slog.Default()))
					}
					// runs.SetGlobal arms the server-tracked progress
					// substrate (mem_runs). Every long action across the
					// codebase resolves runs.Track via this global; if
					// the pool is missing, Track no-ops cleanly. See
					// CLAUDE.md → "Server-tracked progress".
					runs.SetGlobal(p)

					// OAuth-backed OpenAI provider needs a pool-backed token
					// store; FromEnv returns nil for this case so we build it
					// here once the pool is up. The first inference call will
					// surface a clean error if the user hasn't connected yet
					// via Studio's "Connect ChatGPT" flow.
					if provider == nil && llm.IsOpenAIOAuth() {
						oauthStore := llm.NewOAuthStore(p)
						// WrapNoDashes mirrors the universal sanitizer
						// applied by Registry.Register and FromEnv -
						// this construction path bypasses both, so
						// wrap here to keep the em-dash ban universal.
						provider = llm.WrapNoDashes(llm.NewOpenAIOAuth(oauthStore, llm.ModelForVendor("openai_oauth")))
						fmt.Printf("  llm: openai_oauth provider attached (paste-flow connect via Studio)\n")
					}

					// Build the multi-provider registry so the Settings PUT
					// can hot-swap between any vendor whose creds are wired.
					// The OAuthStore is shared so flipping anthropic ↔
					// openai_oauth never wipes mem_provider_tokens - re-auth
					// is not required to switch back.
					oauthStoreShared := llm.NewOAuthStore(p)
					llmRegistry = llm.BuildRegistry(oauthStoreShared)
					fmt.Printf("  llm: registered %v\n", llmRegistry.Available())

					// Compressor needs an Anthropic client; wire only if the
					// active provider is Anthropic so we don't pin a 2nd key.
					// llm.Unwrap walks through the no-dashes sanitizer
					// wrapper so the type assertion finds the underlying
					// *llm.Anthropic regardless of how many wrappers are in
					// the stack.
					if a, ok := llm.Unwrap(provider).(*llm.Anthropic); ok {
						summarizerModel := os.Getenv("LLM_SUMMARIZE_MODEL")
						summarizer := llm.NewAnthropicSummarizer(a, summarizerModel)
						compressor = memory.NewCompressor(p, embedder, memory.NewSummarizer(summarizer))
						critic := llm.NewAnthropicCritic(a, os.Getenv("INFINITY_REFLECT_MODEL"))
						reflector = memory.NewReflector(p, embedder, memory.NewCritic(critic))
					}

					pipeline = hooks.NewPipeline()
					hooks.RegisterDefaults(pipeline, p, store, embedder, compressor)

					// Predict-then-act: every PreToolUse writes an expected
					// outcome; PostToolUse resolves with a surprise score.
					// High-surprise rows feed the curiosity scanner + Voyager
					// curriculum. JEPA discipline without a generative world
					// model - see core/internal/memory/predictions.go.
					predictions := memory.NewPredictionStore(p)
					if a, ok := llm.Unwrap(provider).(*llm.Anthropic); ok {
						hooks.NewPredictionRecorderWithDrafter(predictions, a, os.Getenv("INFINITY_PREDICTION_MODEL")).Register(pipeline)
					} else {
						hooks.NewPredictionRecorder(predictions).Register(pipeline)
					}

					tools.RegisterMemoryTools(registry, p, embedder, searcher)
					// LangSmith-style trace tools - read mem_turns +
					// mem_observations + mem_predictions per turn so the
					// self-improve-from-finding skill can diagnose from
					// real evidence, not summaries.
					tools.RegisterTraceTools(registry, p)
					// Pass the Haiku drafter so skill_optimize can MERGE
					// the new body into any existing open candidate for
					// the same parent_skill (one active draft per skill,
					// per migration 039). nil-safe: without an Anthropic
					// provider, skill_optimize still upserts but the
					// merge degrades to "latest body wins."
					var skillDrafter tools.SkillToolsDrafter
					if a, ok := llm.Unwrap(provider).(*llm.Anthropic); ok {
						skillDrafter = a
					}
					tools.RegisterSkillTools(registry, p, skillDrafter)
					tools.RegisterDashboardTools(registry, p)
					// Curiosity questions tools (question_list / question_decide).
					// The "Questions" card on the dashboard is backed by
					// mem_curiosity_questions, NOT mem_surface_items - without
					// these the agent can't dismiss the items rendered there.
					tools.RegisterCuriosityTools(registry, p)
					// system_map - runtime introspection. Pinned + always-
					// available so the agent never guesses which list/
					// mutate tool maps to which surface.
					tools.RegisterSystemMap(registry, p)
					// domain_hint_add / _list - the agent extends the
					// system_map topology itself, persisted to
					// mem_domain_hints. Closes the autonomy loop on
					// introspection: a new irregular table needs no Go
					// edit, just one domain_hint_add call.
					tools.RegisterDomainHintTools(registry, p)
					// Bridge router + primitive tools. The router decides
					// per session whether fs/bash/git ops land on the Mac
					// bridge (Cloudflare tunnel to home Mac, also home of
					// the Anthropic-Max-billed Claude Code sub-agent) or
					// the Cloud bridge (docker/workspace on Railway private
					// net, no sub-agent - Jarvis's own cognition via
					// ChatGPT subscription handles everything). Auto-route
					// prefers Mac when its /health responds. The fs_/bash_/
					// git_ tool names work uniformly across both bridges.
					macURL := strings.TrimSpace(os.Getenv("CLAUDE_CODE_TUNNEL_URL"))
					cloudURL := strings.TrimSpace(os.Getenv("WORKSPACE_BRIDGE_URL"))
					if cloudURL == "" && strings.TrimSpace(os.Getenv("RAILWAY_ENVIRONMENT_NAME")) != "" {
						// Sensible default on Railway: hit the sibling
						// service over the private network. Override by
						// setting the env explicitly.
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
					// Generic artifact CRUD + high-level project_create.
					// project_create is the boss-asked-for end-to-end
					// app-bootstrap tool; it routes through the bridge
					// internally and indexes itself into mem_artifacts.
					tools.RegisterArtifactTools(registry, p)
					tools.RegisterProjectTools(registry, p, activeBridgeRouter, activeBridgePrefs)
					// document_create — generate .xlsx/.docx/.pptx/.pdf/.md via
					// the workspace bridge's baked helpers (openpyxl/docx-js/
					// pptxgenjs/reportlab). Cloud-only (the stack lives in the
					// workspace image), so it targets cloudURL directly.
					workspaceRawBase = cloudURL
					workspaceToken = os.Getenv("WORKSPACE_BRIDGE_TOKEN")
					if dc := tools.NewDocumentCreate(cloudURL, workspaceToken); dc != nil {
						docCreate = dc
						registry.Register(dc)
						fmt.Println("  document_create: configured")
					}
					macStatusStr := "unset"
					if macURL != "" {
						macStatusStr = "configured"
					}
					cloudStatusStr := "unset"
					if cloudURL != "" {
						cloudStatusStr = "configured"
					}
					fmt.Printf("  bridges: mac=%s cloud=%s\n", macStatusStr, cloudStatusStr)

					// Browser bridge — the observe/act/extract verb set with a
					// live screencast the boss watches in Studio's Preview pane.
					// Generic building block (Rule #1): no per-site logic, the
					// "how to browse" recipe is the seeded browser skill. The
					// engine is chosen by env:
					//   - Camoufox anti-detect server (camofox-browser) on the
					//     home Mac (residential IP — preferred) and/or the Cloud
					//     (Railway). Mac-first because anti-detect spoofs the
					//     fingerprint but not the IP, and a residential IP is what
					//     actually beats Cloudflare/DataDome.
					//   - the legacy chromedp/Chromium sidecar (docker/browser) as
					//     fallback when no Camoufox URL is configured.
					var browserBackend browser.Backend
					camoKey := os.Getenv("CAMOFOX_API_KEY")
					camoUser := os.Getenv("CAMOFOX_USER_ID")
					cfID := os.Getenv("CF_ACCESS_CLIENT_ID")
					cfSecret := os.Getenv("CF_ACCESS_CLIENT_SECRET")
					camoCloudURL := strings.TrimSpace(os.Getenv("CAMOFOX_URL"))
					if camoCloudURL == "" && camoKey != "" && strings.TrimSpace(os.Getenv("RAILWAY_ENVIRONMENT_NAME")) != "" {
						camoCloudURL = "http://camofox.railway.internal:9377"
					}
					camoMacURL := strings.TrimSpace(os.Getenv("CAMOFOX_URL_MAC"))

					var camoMac, camoCloud browser.Backend
					if b := browser.NewCamofoxBackend(camoMacURL, camoKey, camoUser, cfID, cfSecret); b != nil {
						camoMac = b
					}
					if b := browser.NewCamofoxBackend(camoCloudURL, camoKey, camoUser, "", ""); b != nil {
						camoCloud = b
					}

					switch {
					case camoMac != nil && camoCloud != nil:
						// Browser follows the session's bridge selector (mac/cloud
						// pin honoured, auto = Mac-first by health). Adapt the
						// bridge PreferenceFetcher to a plain-string func so the
						// browser package stays decoupled from the bridge package.
						browserPref := func(ctx context.Context, chatID string) string {
							if activeBridgePrefs == nil {
								return ""
							}
							return string(activeBridgePrefs(ctx, chatID))
						}
						browserBackend = browser.NewRoutingBackend(camoMac, camoCloud, browserPref)
						fmt.Printf("  browser: camoufox routed (mac=%s + cloud=%s, follows bridge selector; auto=mac-first)\n", camoMacURL, camoCloudURL)
					case camoMac != nil:
						browserBackend = camoMac
						fmt.Printf("  browser: camoufox mac (%s)\n", camoMacURL)
					case camoCloud != nil:
						browserBackend = camoCloud
						fmt.Printf("  browser: camoufox cloud (%s)\n", camoCloudURL)
					default:
						browserURL := strings.TrimSpace(os.Getenv("BROWSER_SIDECAR_URL"))
						if browserURL == "" && strings.TrimSpace(os.Getenv("RAILWAY_ENVIRONMENT_NAME")) != "" {
							browserURL = "http://browser.railway.internal:8080"
						}
						if bc := browser.New(browserURL, os.Getenv("BROWSER_BRIDGE_TOKEN")); bc != nil {
							browserBackend = bc
							fmt.Printf("  browser: chromedp sidecar (%s)\n", browserURL)
						}
					}

					if browserBackend != nil {
						browserReg = browser.NewRegistry(browserBackend, runs.New(p))
						for _, t := range browserReg.AllTools() {
							registry.Register(t)
						}
					} else {
						fmt.Printf("  browser: unset (set CAMOFOX_URL / CAMOFOX_URL_MAC or BROWSER_SIDECAR_URL to enable)\n")
					}
					// mem_substrate - mem_list / mem_act / action_register
					// / action_list. The generic, bounded read/write
					// surface over every mem_* table. Combined with
					// system_map this is the AGI substrate: any new
					// mem_X table becomes fully actionable from chat
					// the moment its action schemas are registered.
					// Zero new Go tools per domain.
					tools.RegisterMemSubstrate(registry, p)
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
			//
			// Skills that aren't project scaffolds (e.g.
			// self-improve-from-finding) are seeded directly into the
			// Postgres store via a migration instead - the store is the
			// durable home, and MaterializeActiveSkills derives them to
			// disk at boot. No rebuild needed to ship or evolve those.
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
				// trust_batch_assign: lets skills group recent pending
				// contracts under a fresh batch_id so the boss can
				// bulk-approve from Studio's Trust panel. Inbox triage
				// is the first consumer; works for any skill that
				// queues multiple contracts in one run.
				proactive.RegisterTrustTools(registry, earlyTrust)
			}

			// LoopGate + awareness: catch retry loops and runaway sessions
			// without depending on USD spend. The same gate instance feeds
			// both the gate chain (safety net - blocks at the threshold)
			// and the LoopAwarenessProvider (primary defense - surfaces the
			// rate so the agent self-throttles BEFORE the gate fires).
			loopGate := initiative.NewLoopGate(earlyTrust)

			// Honcho - optional dialectic peer-modelling sidecar. When
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
					fmt.Printf("  sessions: auto-naming enabled (follows Settings model, Haiku fallback)\n")
				}
			}

			// Connectors cache: live picture of Composio connected accounts +
			// boss-assigned aliases. Powers the multi-account routing block
			// the agent loop injects into its system prompt so the model
			// can pick the right account when a tool exposes per-account
			// `connected_account_id` parameters. Started later so the
			// background refresh ticker is tied to the serve context.
			//
			// composioKeyFn is shared across the cache, the execute client,
			// and the toolkit-verb registrar so a Railway env hot-swap
			// propagates everywhere without restart.
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
				// connector_identity_set - generic write-back the agent
				// uses after it has resolved an account's real upstream
				// identity (Gmail's emailAddress, Slack's handle, etc.).
				// Zero toolkit knowledge in Go; the system prompt nudges
				// the agent to discover the right verb on its own.
				tools.RegisterConnectorTools(registry, connectorsCache)
			}

			// Persisted token usage. Migration 013 added the columns;
			// UsagePersistence reads/writes them so the context meter
			// survives Railway container rotation. Nil-safe in the
			// agent loop, so we only build when the pool exists.
			var usageStore *sessions.UsagePersistence
			var initStore *initiative.Store
			if pool != nil {
				usageStore = sessions.NewUsagePersistence(pool)
				initStore = initiative.NewStore(pool, slog.Default())
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
				if initStore != nil {
					cfg.Costs = costRecorder{store: initStore}
				}
				// Compose memory providers: Infinity's RRF searcher always
				// runs first, Honcho's peer representation folds in second
				// when configured. Order matters - searcher emits the boss
				// profile primer + relevant memory; Honcho's reasoning sits
				// below it in the system prompt for clear separation.
				memProviders := []agent.MemoryProvider{}
				if searcher != nil {
					memProviders = append(memProviders, searcher)
				}
				if pool != nil {
					memProviders = append(memProviders, plasticity.NewProvider(pool, embedder))
				}
				// Agent goals: the model's own durable objectives (mem_agent_goals)
				// folded into every turn so the agent reasons knowing what it's
				// supposed to be doing across sessions, not just the current
				// message.
				if pool != nil {
					memProviders = append(memProviders, worldmodel.NewGoalsProvider(pool))
				}
				// Cross-session lessons: mem_reflection_chains clusters repeated
				// reflection lessons across sessions. This provider round-robins
				// among the top-confidence chains so the agent picks up patterns
				// it has noticed before without seeing the same lesson on every
				// turn.
				if pool != nil {
					memProviders = append(memProviders, memory.NewReflectionChainsProvider(pool))
				}
				// Self-model: mem_agent_metrics is rolled up nightly with
				// today's behaviour vs. the 14-day baseline. The provider
				// emits a block only when something has drifted >2σ - the
				// agent notices itself when it should, stays silent
				// otherwise.
				if pool != nil {
					memProviders = append(memProviders, memory.NewSelfModelProvider(pool))
				}
				// Loop awareness: surface the session's own tool-call rate
				// every turn once it crosses 50% of the ceiling, so the
				// agent self-throttles BEFORE the LoopGate trips. Quiet
				// during healthy sessions.
				if awareness := initiative.NewLoopAwarenessProvider(loopGate); awareness != nil {
					memProviders = append(memProviders, awareness)
				}
				if honchoClient.Enabled() {
					memProviders = append(memProviders, honcho.NewMemoryProvider(honchoClient))
				}
				// Bridge overlay - emits "Active bridge: Mac/Cloud, here's
				// what tools work right now" every turn so Jarvis answers
				// device questions truthfully and picks the right tool the
				// first time.
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
					// Gate chain: per-MCP authorization policies, all sharing
					// the same TrustStore so the boss sees a single approval
					// queue in Studio. Order matters only for tools that match
					// multiple gates (none today) - first non-allow decision
					// wins.
					//
					//   ClaudeCodeGate → claude_code__*  (home Mac shell/edit/write)
					//   GitHubGate     → github__*       (direct github-mcp-server)
					//   ComposioGate   → composio__*     (Composio gateway, all SaaS
					//                                     toolkits - pattern-based
					//                                     write-verb detection)
					cfg.Gate = agent.NewGateChain(
						proactive.NewClaudeCodeGate(earlyTrust),
						proactive.NewGitHubGate(earlyTrust),
						proactive.NewComposioGate(earlyTrust),
						proactive.NewBridgeGate(earlyTrust),
						// BrowserGate → browser_*. Normal browsing runs
						// unattended; only transactional acts (buy/pay/
						// checkout/delete-account) queue a Trust contract.
						proactive.NewBrowserGate(earlyTrust),
						// LoopGate is the safety net: hard-blocks the same
						// exact tool+input repeating ≥3 times in 60s, and
						// routes through Trust when a session blows past
						// 50 tool calls in 5 min. The agent sees its own
						// rate every turn via LoopAwarenessProvider and is
						// expected to self-throttle before this trips.
						loopGate,
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
				if pool != nil {
					registry.Register(&agent.AgentTeam{Loop: loop, Pool: pool, Settings: settings.New(pool)})
				}
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
				// LangSmith-style turn tracing - opens a mem_turns row at
				// turn entry, threads turn_id into every fireHook payload,
				// closes the row on TaskCompleted / interrupt / error.
				if pool != nil {
					loop.SetTurnRecorder(turnRecorderAdapter{store: memory.NewTurnStore(pool)})
				}
				// Tool visibility - hide claude_code__* on Cloud-routed
				// sessions so the model can't accidentally edit the Mac
				// filesystem when working in the Cloud workspace. The
				// nudges in the bridge system-prompt overlay were not
				// enough; the only reliable fix is making the schemas
				// physically invisible to the model. Mac sessions see
				// the full toolset unchanged.
				if activeBridgeRouter != nil {
					loop.SetToolVisibility(makeBridgeToolVisibility(activeBridgeRouter, activeBridgePrefs))
				}
				// Central active-model resolver. Every Loop.Run that
				// passes an empty model string now picks up the boss's
				// Studio selection here - cron, workflow executor,
				// delegate sub-agents that don't override, heartbeat
				// turns, voice tool turns, ws resume. Without this
				// resolver an openai_oauth deploy falls into the
				// provider's boot default (gpt-5-codex) and breaks any
				// recipe that needs standard GPT chat. This makes the
				// boss's selection authoritative across EVERY execution
				// path with one wire - no per-call-site plumbing.
				if pool != nil {
					modelSettings := settings.New(pool)
					loop.SetActiveModelFn(modelSettings.GetModel)
					// Route session auto-naming through the same active-model
					// resolver so titles are drafted with the boss's Studio
					// selection instead of being pinned to Haiku. Falls back
					// to Haiku when no model override is set.
					if sessionNamer != nil {
						sessionNamer.SetActiveModelFn(modelSettings.GetModel)
					}
				}
			}

			// Durable workflow engine - Phase 2 substrate. The agent
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
				if initStore != nil {
					wfEngine = wfEngine.WithCostRecorder(workflowCostRecorder{store: initStore})
				}
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
				if skillRunner != nil {
					skillRunner.SetRiskGate(skillRiskGate{trust: trustStore})
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
						// own goals that are blocked, due soon, or stalled -
						// so a goal it set and forgot gets revisited.
						proactive.AgentGoalChecklist(pool),
						// Goal × entity urgency: when an active goal references
						// a world-model entity and that entity shows up in a
						// recent observation, the agent learns about it. This
						// is what makes goal pursuit feel responsive to the
						// world instead of polling on a timer.
						proactive.GoalEntityUrgencyChecklist(pool),
						// Calendar prep: read mem_calendar_events (Composio
						// poller keeps it fresh) for anything in the next 60
						// min and surface a Finding with attendee context +
						// recent memory. Turns "you have a meeting in 30
						// min" from manual mental work into a heartbeat hit.
						proactive.CalendarPrepChecklist(pool),
						// Substrate → dashboard: mirror the agent's goals and
						// anything broken (failed extensions, regressed
						// capabilities) onto the generic surface contract, so
						// they render on the dashboard with zero bespoke
						// Studio code. Rule #1 applied to the substrate itself.
						proactive.SubstrateSurfaceChecklist(pool),
						// Connector identities: count active Composio accounts
						// missing their real upstream identity and emit a
						// finding pointing at the `resolve-connector-identities`
						// skill. The skill carries the toolkit-agnostic
						// cognition (find the profile verb, call it, persist).
						// Together they close the loop: connect a new account
						// → next heartbeat tick notices → skill fires → identity
						// shows in every later turn. Zero per-toolkit Go code.
						proactive.ConnectorIdentityChecklist(connectorsCache),
						// Healing: scan mem_crons for error-tagged last_run_status
						// and mem_observations for tools that have failed 3+ times
						// in the last 24h. Each detection writes a row into
						// mem_curiosity_questions with source_kind='cron_failure'
						// or 'repeated_tool_error', which surfaces in /lab's
						// Fix this tab with an Approve-and-fix path. Dedupe is
						// the schema-level unique index on (question) WHERE
						// status='open' so re-runs across ticks are idempotent.
						proactive.HealingChecklist(pool, func() bool {
							// Mac-bridge probe: when the boss is on cloud and the
							// Mac is asleep, every claude_code__* tool call fails.
							// The healer uses this to skip those failures so the
							// dashboard doesn't fill up with "fs_read failed N
							// times" curiosity questions for expected outages.
							if activeBridgeRouter == nil {
								return false
							}
							return activeBridgeRouter.MacBridgeHealthy()
						}),
						// Skill self-authoring: detect repeated multi-step
						// recipes the agent runs but hasn't crystallized into a
						// skill, and detect divergent (successful) paths the
						// boss steered when an installed skill fired. Each
						// detection surfaces as a curiosity question in /lab
						// Fix-this with an Approve-and-fix path that routes to
						// the propose-skill-from-pattern or
						// evolve-skill-from-deviation default skill (migration
						// 037), which calls skill_propose / skill_optimize -
						// rendered inline in chat by SkillProposalCard. Closes
						// the AGI loop: notice -> propose -> approve -> install
						// without leaving chat.
						proactive.SkillAuthoringChecklist(pool),
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
				 * Both are stateless pool wrappers - safe to share
				 * across the WS handler and Studio API readers. */
				walStore = proactive.NewWAL(pool)
				workingBuf = proactive.NewWorkingBuffer(pool, 0)
				proactiveAPI = proactive.NewAPI(pool, heartbeat, trustStore, intentDB)
				fmt.Printf("  proactive: heartbeat every %s, intent=%v, trust=ready, wal=on, buffer=on\n",
					heartbeat.Interval(), intentDetector != nil)
			}
			/* IntentFlow is now wired into the WS turn handler - every
			 * user message is classified async (Haiku JSON call),
			 * persisted to mem_intent_decisions, and emitted as an
			 * `intent` WS frame for Studio's IntentStream panel. The
			 * detector is passed into server.Config below. */

			// Connector poller - deterministic Composio tools.execute path
			// (no LLM) that connector_poll cron jobs ride on. Reads the
			// same admin/consumer key resolution the connectors cache uses
			// so a Railway env swap propagates without restart.
			var connectorPoller *connectors.Poller
			if pool != nil && composioExec != nil {
				connectorPoller = connectors.NewPoller(pool, composioExec, pipeline)
				// Wire the followup triager so newly-polled emails get
				// metadata.intent / mode / classification chips populated
				// asynchronously - same Gmail cron, one extra classify call
				// per inbound message on the boss's SELECTED model (no
				// hardcoded Haiku; INFINITY_TRIAGE_MODEL overrides). Anthropic
				// provider required to construct (the unwrap below); other
				// providers degrade to no-classification, row still lands.
				if a, ok := llm.Unwrap(provider).(*llm.Anthropic); ok {
					connectorPoller.SetTriager(triage.New(triage.Config{
						Provider: a,
						Model:    os.Getenv("INFINITY_TRIAGE_MODEL"),
					}))
					fmt.Println("  connector poller: ready (composio tools.execute, +triage)")
				} else {
					fmt.Println("  connector poller: ready (composio tools.execute, no triage)")
				}
			}

			// Native Google Calendar sync. Uses Composio's tools.proxy to
			// pass through to Google Calendar v3 with the connected_account's
			// OAuth token attached server-side (token never leaves Composio).
			// Incremental syncToken pattern means each 2-min tick is one
			// thin HTTP call per account returning only deltas. Provides
			// the substrate for the dashboard Upcoming card + Accept/Decline
			// modal + agent calendar_* tools.
			var (
				calendarSyncer *calendar.Syncer
				calendarTicker *calendar.Ticker
				calendarAPI    *calendar.API
			)
			if pool != nil && composioExec != nil && connectorsCache != nil {
				selfEmailFor := func(accountID string) string {
					return connectorsCache.Identities()[accountID]
				}
				// userIDFor: Composio's execute API requires the entity_id
				// (user_id) alongside connected_account_id on every call -
				// it 1811s otherwise. The cache's Account.UserID carries
				// the entity slug the boss connected with ("dopesoft",
				// "mr khaya", ...), looked up per account on each call.
				userIDFor := func(accountID string) string {
					for _, accs := range connectorsCache.AccountsByToolkit() {
						for _, a := range accs {
							if a != nil && a.ID == accountID {
								return a.UserID
							}
						}
					}
					return ""
				}
				calProvider := calendar.NewGoogleProvider(composioExec, selfEmailFor, userIDFor)
				calStore := calendar.NewStore(pool)
				calendarSyncer = calendar.NewSyncer(calProvider, calStore, pipeline)

				// Ticker: every 2 min, walk every connected googlecalendar
				// account. Account discovery routes through the connectors
				// cache so a new Google grant lights up sync within one
				// cache refresh cycle (~60s) - no restart needed.
				listAccounts := func() []string {
					by := connectorsCache.AccountsByToolkit()
					ids := []string{}
					for _, a := range by["googlecalendar"] {
						if a != nil && a.Status != "" && a.Status != "disabled" {
							ids = append(ids, a.ID)
						}
					}
					return ids
				}
				calendarTicker = calendar.NewTicker(calendarSyncer, listAccounts, 2*time.Minute)

				// Studio-facing accounts list folds in the per-account
				// sync-state row (last_tick_at + last_error) so the UI can
				// surface "last synced 30s ago" / "auth expired."
				listAccountDTOs := func() []calendar.AccountDTO {
					accounts := connectorsCache.AccountsByToolkit()["googlecalendar"]
					out := make([]calendar.AccountDTO, 0, len(accounts))
					idents := connectorsCache.Identities()
					for _, a := range accounts {
						if a == nil {
							continue
						}
						dto := calendar.AccountDTO{
							ID:    a.ID,
							Alias: a.Alias,
							Email: idents[a.ID],
						}
						st, _ := calStore.LoadSyncState(context.Background(), a.ID, "primary")
						if st != nil {
							if !st.LastTickAt.IsZero() {
								dto.LastSyncAt = st.LastTickAt.Format(time.RFC3339)
							}
							dto.LastError = st.LastError
							dto.SyncedCount = st.EventsSeen
						}
						out = append(out, dto)
					}
					return out
				}
				calendarAPI = calendar.NewAPI(calendarSyncer, listAccountDTOs)

				// Agent-facing calendar tools (calendar_respond, _sync_now,
				// _create, _patch, _delete). Jarvis routes through these
				// instead of raw composio__GOOGLECALENDAR_* verbs - same
				// Provider path the ticker uses, so behaviour is identical
				// across automated sync, agent intent, and Studio clicks.
				calendar.RegisterTools(registry, calendarSyncer)

				fmt.Println("  calendar: native Google sync ready (Composio proxy, 2m tick)")
			}

			// Cron scheduler + Sentinel manager. Both degrade gracefully when
			// no DB pool is configured. The scheduler now runs a composite
			// executor - agent jobs (system_event / isolated_agent_turn) go
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
					// Active-model selection is the loop's job (see
					// SetActiveModelFn above) - cron just hands the
					// loop a session and prompt, no settings plumbing.
					agentExec = cron.NewAgentExecutor(loop, pool)
				}
				var connectorExec cron.Executor
				if connectorPoller != nil {
					connectorExec = cron.NewConnectorExecutor(connectorPoller)
				}
				systemExec := cron.NewSystemExecutor(maintenance.Deps{
					Pool:       pool,
					Reflector:  reflector,
					Compressor: compressor,
					Surface:    surface.NewStore(pool, slog.Default()),
					Embedder:   embedder,
					Logger:     slog.Default(),
				})
				cronScheduler = cron.New(pool, cron.NewCompositeExecutor(agentExec, connectorExec, systemExec))
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
				sentinelMgr.Start(cmd.Context())
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
					// source_extract is the third Voyager hook - drafts a
					// code-refactor proposal when the boss visibly fought
					// the same file during a session. Lands rows in
					// mem_code_proposals for review in Studio.
					pipeline.RegisterFunc("voyager.source_extract", voyagerMgr.OnSessionEndSource, hooks.SessionEnd)
				}
				// Promotion → (procedural memory + live chat bubble).
				// One callback handles both side-effects:
				//   1. UpsertFromSkill on the procedural tier (CoALA).
				//   2. notifySkillPromoted, late-bound to the server's
				//      BroadcastSkillPromoted so the chat surface gets a
				//      "🤖 skill learned" bubble in real time. The server
				//      doesn't exist yet at this point - we bind the var
				//      below right after server.New().
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

				// Auto-trigger: when GEPA_URL is configured, run a background
				// ticker that watches mem_skill_runs and fires the optimizer
				// for any skill whose recent failure rate crosses the
				// threshold. This is the close-the-loop step Voyager was
				// missing - without it, GEPA only fires when someone POSTs
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
						fmt.Printf("  auth: enabled (JWKS) - no owner claimed yet, first signup wins\n")
					} else {
						fmt.Printf("  auth: enabled (JWKS) - owner=%s\n", owner)
					}
				} else {
					fmt.Printf("  auth: DISABLED (set SUPABASE_URL to enable)\n")
				}
			}

			// Voice (OpenAI Realtime over WebRTC). nil-safe - when
			// OPENAI_API_KEY isn't set, voice.New() returns nil and
			// the /api/voice/* endpoints simply 503.
			voiceMinter := voice.New()
			if voiceMinter != nil {
				fmt.Printf("  voice: realtime enabled (model=%s, voice=%s)\n", voiceMinter.Model(), voiceMinter.Voice())
			}

			// Push notifications. Sender requires VAPID env vars; when
			// they're missing we still expose the API so Studio can show
			// "not configured" instead of 404'ing. Store works whenever
			// the pool is up - subscriptions can land in advance of the
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
			if pool != nil && initStore != nil {
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
				// Lazy email-body fetcher for GET /api/followups/message:
				// pulls the full HTML on open (never at poll time) so the
				// ObjectViewer can render the real email. Nil-safe - without
				// Composio the Message pane falls back to preview text.
				if composioExec != nil {
					dashboardAPI.Fetcher = connectors.NewMessageFetcher(composioExec, connectorsCache)
				}
				// read_email: lets the agent pull a follow-up's full email body
				// on demand (when the summary in context isn't enough). Shares
				// dashboard.FullEmailText with the Discuss-seed enrichment.
				registry.Register(&tools.ReadEmail{Fetch: dashboardAPI.FullEmailText})
				fmt.Println("  dashboard: aggregator wired (+read_email)")
			}

			var turnStore *memory.TurnStore
			if pool != nil {
				turnStore = memory.NewTurnStore(pool)
			}
			// browserClose backs Studio's "Stop" button on the Preview pane (live browser).
			// nil when the browser backend isn't configured (route 503s).
			var browserClose func(ctx context.Context, sessionID string) error
			if browserReg != nil {
				browserClose = browserReg.Close
			}
			srv := server.New(server.Config{
				Addr:             addr,
				Version:          version,
				Loop:             loop,
				MCP:              mcp,
				Pool:             pool,
				Store:            store,
				Searcher:         searcher,
				SkillsAPI:        skillsAPI,
				ProactiveAPI:     proactiveAPI,
				CronAPI:          cronAPI,
				SentinelAPI:      sentinelAPI,
				VoyagerAPI:       voyagerAPI,
				Auth:             authVerifier,
				Trust:            trustStore,
				Namer:            sessionNamer,
				IntentDetector:   intentDetector,
				IntentStore:      intentDB,
				WAL:              walStore,
				WorkingBuffer:    workingBuf,
				Heartbeat:        heartbeat,
				LLMRegistry:      llmRegistry,
				Connectors:       connectorsCache,
				Voice:            voiceMinter,
				PushAPI:          pushAPI,
				DashboardAPI:     dashboardAPI,
				BridgeRouter:     activeBridgeRouter,
				BridgePrefs:      activeBridgePrefs,
				Turns:            turnStore,
				RunsAPI:          runs.NewAPI(pool),
				CalendarAPI:      calendarAPI,
				BrowserClose:     browserClose,
				WorkspaceRawBase: workspaceRawBase,
				WorkspaceToken:   workspaceToken,
			})

			// Late-bind the Voyager auto-promote → chat-bubble notifier. The
			// callback was registered earlier (so we don't drop events that
			// arrive before this point) but its target needed the server
			// instance to exist.
			notifySkillPromoted = srv.BroadcastSkillPromoted

			// Late-bind the browser frame sink now that the server (which
			// owns the per-session WS broadcaster) exists. Frames stream to
			// the chat session's Studio tab for the whole browser session.
			if browserReg != nil {
				browserReg.SetSink(func(chatID string, f browser.Frame) {
					srv.EmitBrowserFrame(chatID, f.Seq, f.Frame, f.URL, f.BrowserID)
				})
			}
			// Late-bind document_create's "open in a new tab" emitter to the
			// server's per-session broadcaster.
			if docCreate != nil {
				docCreate.Emit = func(sessionID, format, filename, path, markdown, pdfPath string, bytes int64) {
					srv.EmitDocumentCreated(sessionID, format, filename, path, markdown, pdfPath, bytes)
				}
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// Start the connectors cache background refresh now that the
			// serve context exists. Synchronously primes once so the first
			// turn after boot already sees connected-account state.
			if connectorsCache != nil {
				connectorsCache.Start(ctx)
				defer connectorsCache.Stop()
			}

			// Calendar sync ticker starts after the cache so the very first
			// tick already has the connected-accounts snapshot to walk.
			if calendarTicker != nil {
				calendarTicker.Start(ctx)
				defer calendarTicker.Stop()
			}

			// Pre-register every Composio toolkit verb the boss has
			// CONNECTED as a dormant tool in the registry - so the agent
			// finds `composio__GMAIL_SEND_EMAIL` etc. via the normal
			// `tool_search` + `load_tools` path instead of being forced
			// into the MCP gateway's SEARCH_TOOLS + MULTI_EXECUTE dance
			// (which is what produced empty replies / Python repr leaks
			// when the model gave up and ran REMOTE_WORKBENCH). The
			// catalog block collapses each toolkit to one line, so 100+
			// new dormant entries cost ~10 prompt lines, not 100. Per-turn
			// active-set schema cost stays bounded by load_tools.
			// Composio verb sync: stateful diff between connected
			// toolkits (cache snapshot) and registered verbs. First
			// pass at boot brings everything online dormant; the
			// cache's onChange callback keeps it live thereafter,
			// so when the boss connects a new toolkit via Settings
			// → Connectors its verbs appear in the agent's catalog
			// within one cache refresh (~60s) - no redeploy, no
			// restart. Disconnecting an account removes its verbs
			// the same way. Runtime adaptation, per Rule #1.
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
				// Subscribe to cache changes so future toolkit
				// connect/disconnect events trigger a re-sync.
				// Each onChange runs in its own goroutine - the
				// sync's mutex serializes overlapping fires.
				connectorsCache.SetOnChange(func() {
					reCtx, reCancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer reCancel()
					if _, _, _, err := verbSync.Sync(reCtx); err != nil {
						fmt.Fprintf(os.Stderr, "warning: composio verb resync: %v\n", err)
					}
				})
			}

			// Memory maintenance ticker. Heartbeat + voyager autotrigger run
			// constantly, each turn generating PreToolUse/PostToolUse rows. With no
			// scheduled prune, mem_observations grows by ~500-1000/day. This runs
			// the same ConsolidateNightly pass the CLI exposes - decay, hot-reset,
			// auto-forget (including observation retention via pruneObservations).
			// Fires 30s after boot to clean up any startup-time backlog, then
			// every 6h. Override via INFINITY_CONSOLIDATE_INTERVAL.
			if pool != nil {
				go runMaintenanceTicker(ctx, pool)
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

// makeBridgeToolVisibility returns an agent.ToolVisibilityFunc that hides
// the Mac-only `claude_code__*` toolset from the model when the session's
// active bridge is the Cloud workspace. This is the structural fix for
// the long-standing failure mode where the model would happily call
// `claude_code__Edit` on a Cloud-routed session and silently edit the
// boss's Mac filesystem instead of the workspace volume he was looking
// at. The system-prompt overlay was nudging against this; making the
// schemas physically invisible is the only reliable solution.
//
// Mac sessions, or sessions where the bridge selection is indeterminate
// (no router state, no preference, errored router), see the full
// toolset unchanged - the filter only ACTS when we're confident Cloud
// is in charge.
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
		// Hide the entire claude_code__* family. Names match the
		// MCP-registered tool ids in tools/defaults.go DefaultLoadedTools
		// plus the dormant catalog (Agent/Grep/Glob/LS/NotebookEdit/etc).
		// We use a name-prefix match via the registry's catalog rather
		// than a hardcoded list - that way any new claude_code__X tool
		// added later inherits the filter automatically.
		hidden := map[string]struct{}{}
		for _, n := range allClaudeCodeToolNames {
			hidden[n] = struct{}{}
		}
		return hidden
	}
}

// allClaudeCodeToolNames enumerates the claude_code__* tool ids the MCP
// proxy registers on boot (see Railway log line "mcp: claude_code
// connected (26 tools)"). Hardcoding the list keeps the visibility
// filter dependency-free; if the proxy adds a new verb, list it here.
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

// turnRecorderAdapter bridges *memory.TurnStore → agent.TurnRecorder. The
// agent package can't import memory directly (would pull pgx into the
// loop-pure boundary), so we translate the field structs here. Stateless
// pass-through with zero copy overhead on the success path.
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

// cronSchedulerAdapter bridges *cron.Scheduler → tools.CronScheduler.
// The tools package can't import cron directly (would cycle through
// agent), so we translate between cron.Job and tools.CronJob here. The
// adapter is stateless - every call passes through to the wrapped
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

// runMaintenanceTicker runs ConsolidateNightly on an interval so observation
// pruning happens without depending on a manual `infinity consolidate` call or
// an external cron. Failures log to stdout but never kill the server.
func runMaintenanceTicker(ctx context.Context, pool *pgxpool.Pool) {
	interval := 6 * time.Hour
	if v := strings.TrimSpace(os.Getenv("INFINITY_CONSOLIDATE_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		}
	}
	infoLog := log.New(os.Stdout, "", log.LstdFlags)

	runOnce := func() {
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		report, err := memory.ConsolidateNightly(runCtx, pool)
		if err != nil {
			fmt.Fprintf(os.Stderr, "maintenance: consolidate: %v\n", err)
			return
		}
		infoLog.Printf("maintenance: consolidate decayed=%d hot_reset=%d clusters=%d contradictions=%d assoc_pruned=%d weak_purged=%d procedural=%d obs_trace=%d obs_convo=%d ttl=%d low_value=%d",
			report.Decayed, report.HotReset, report.ClustersFound,
			report.ContradictionsFound, report.AssociativePruned,
			report.WeakAssocPurged, report.ProceduralReweighted,
			report.Forget.ObsTraceTrimmed, report.Forget.ObsConversationTrimmed,
			report.Forget.TTLExpired, report.Forget.LowValue,
		)
	}

	// First pass shortly after boot so a fresh deploy sweeps any backlog.
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	runOnce()

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runOnce()
		}
	}
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
