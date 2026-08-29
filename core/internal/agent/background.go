package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/google/uuid"
)

// BackgroundAgent is the model-callable "kick off a real build and walk
// away" tool. It exists to keep expensive, metered surfaces (the GPT
// Realtime voice session) OUT of long engineering work: instead of the
// realtime model orchestrating a build turn-by-turn (every tool result
// fed back through metered realtime tokens), it calls background_build
// once, gets an immediate "started" ack, and the actual work runs on the
// boss's SETTINGS model (gpt-5.4 over the OAuth subscription) in a
// detached goroutine. When it finishes, OnDone fires - the server turns
// that into a live chat bubble + a push notification, so the boss can
// literally hang up the voice call and get pinged when it's done.
//
// Unlike delegate (synchronous, returns the summary inline, capped at a
// few minutes because the caller blocks), background_build:
//
//   - returns immediately with a run_id; the caller never waits
//   - runs on context.Background() so it survives the triggering voice /
//     chat session ending
//   - books a mem_runs row (status running → ok/error) so Studio shows a
//     persistent spinner + the final summary, surviving navigation /
//     refresh / second device
//   - notifies on completion via OnDone (chat + push)
//
// It runs the FULL default tool loadout by default (read/write/exec) so
// it can actually build - the read-only delegate default would be useless
// for "build me X". Pass allowed_tools to narrow it.
//
// WHICH BRAIN CODES (the boss's contract, 2026-08-28): on the MAC bridge the
// build runs on Claude Code (`claude -p`) under his Claude Max subscription,
// through the same tools.ClaudeCodeRunner as code_agent - subscription proof,
// API-key guard, delete gate, quota ledger, live progress included. The
// settings-model loop below is the CLOUD path only, where there is no Claude
// Code. Before this, background_build ran a full ChatGPT-billed coding loop
// on the Mac too (13 minutes on 2026-08-27 15:17, again 2026-08-28 01:58) -
// the leak that spent his ChatGPT plan while he was "connected to the Mac".
type BackgroundAgent struct {
	Loop   *Loop
	Bridge bridge.PrefFetcher
	// Code launches Claude Code on the Mac (tools.ClaudeCodeRunner). Nil
	// (not wired) falls back to the settings-model loop on every bridge.
	Code CodeRunner
	// OnDone is invoked once, after the background run finishes (success
	// or failure). Wired in serve.go to broadcast a chat bubble + send a
	// push. nil-safe: when unset, the run still completes + persists to
	// mem_runs, it just doesn't actively notify.
	OnDone func(ctx context.Context, r BackgroundResult)
	// OnProgress receives best-effort stage updates for the live parent
	// chat session while the detached run is still executing. Wired in
	// serve.go to broadcast proactive chat bubbles that share the run id,
	// so Studio can render an updating progress card tied to mem_runs.
	OnProgress func(ctx context.Context, p BackgroundProgress)
	// OnStep receives each individual step the build takes, for the boss's
	// chat ledger. Wired in serve.go to the SAME sink the Mac path uses, so a
	// cloud build and a Mac build are equally visible — the bridge picks which
	// model codes, never how much of the work he can see.
	OnStep func(ctx context.Context, step tools.NestedStep)

	// inflight is the one background build allowed per originating chat
	// session (parent → run id). 2026-08-26: three builds of the SAME feature
	// ran side by side, each a full agent loop on the boss's plan, until the
	// plan was spent. One conversation, one build; the rest are told to watch.
	inflightMu sync.Mutex
	inflight   map[string]string
}

// CodeRunner is the Mac-bridge Claude Code launcher (implemented by
// *tools.ClaudeCodeRunner); declared here so the agent package depends on
// the behaviour, not the concrete type, and tests can fake it.
type CodeRunner interface {
	ActiveBridge(ctx context.Context) (bridge.Bridge, string, error)
	DefaultModel() string
	DefaultEffort() string
	Run(ctx context.Context, job tools.ClaudeCodeJob) (string, error)
}

// BackgroundResult is the completion payload handed to OnDone.
type BackgroundResult struct {
	RunID         string
	ParentSession string
	Task          string
	Summary       string
	Err           string
	// StillRunning: the build did NOT finish and did NOT fail — its inline wait
	// window closed while the worker kept going on the Mac. Err is empty in this
	// case, deliberately: every consumer that branches on "did it error?" must
	// see "no", because settling a plan step failed or pushing "Build failed"
	// here is a lie about work that is still landing on disk. Consumers that
	// need to distinguish "finished" from "still going" read THIS field.
	StillRunning bool
	DurationMS   int64
}

// classifyBackgroundRun turns a finished worker's (summary, error) into what
// the run row and the completion payload should say. Pure, so the one rule that
// matters — STILL WORKING IS NEVER A FAILURE — is testable, and so it is
// impossible to state it in two places that drift.
//
// Detection is by TYPE (tools.IsStillRunning → errors.As), never by scanning
// the message. That is the actual bug being fixed: serve.go's isRecoverableErr
// matched on substrings ("timeout", "eof", …) and this sentinel's wording is
// none of them, so "still working" fell through to the failure branch.
//
// A genuine error is untouched: errText carries it, stillWorking is false, and
// the caller closes the row 'error' exactly as before.
func classifyBackgroundRun(summary string, runErr error) (finalSummary string, stillWorking bool, errText string) {
	if runErr == nil {
		return summary, false, ""
	}
	if tools.IsStillRunning(runErr) {
		if strings.TrimSpace(summary) == "" {
			summary = tools.StillRunningMessage(runErr)
		}
		return summary, true, ""
	}
	return summary, false, runErr.Error()
}

// BackgroundProgress is a best-effort live status update emitted while a
// background build is still running. Stage progress is coarse by design:
// the loop does not expose an exact task DAG, so we surface the current
// tool/stage label plus optional 0..1 progress when available.
type BackgroundProgress struct {
	RunID         string
	ParentSession string
	Task          string
	Label         string
	Status        string
	Progress      *float32
	// Step is the running tool-call count (1-based) for the current stage,
	// 0 for setup/wrap-up phases that aren't tied to a tool call.
	Step int
	// Action is the cleaned verb for the active tool (edit / write / bash /
	// read / thinking …) so Studio can render a chip without re-parsing Label.
	Action string
	// Detail is the most informative target of the active step — the file
	// being written/edited/read, or a clipped command for shell calls.
	Detail string
}

const (
	backgroundDefaultTimeout = 30 * time.Minute
	// backgroundTimeoutCeiling matches tools.codeAgentMaxWait so the two
	// engines give the same work the same amount of room. It was 60 minutes
	// against code_agent's 20, which meant the same build had two different
	// lifetimes depending on which door it came through.
	backgroundTimeoutCeiling  = 90 * time.Minute
	backgroundSessionIDPrefix = "background:"
)

func (b *BackgroundAgent) Name() string { return "background_build" }

func (b *BackgroundAgent) Description() string {
	return "Run a heavy, multi-step engineering task (writing or editing code, building a feature, " +
		"refactoring, a build/test cycle, large multi-file work) autonomously IN THE BACKGROUND. On the Mac " +
		"bridge it runs on Claude Code under the boss's Claude Max subscription (the same engine as code_agent); " +
		"on the Cloud bridge it runs on the boss's settings model. Returns immediately with a run_id - it does NOT block. The work runs " +
		"to completion detached from this conversation and the boss is notified (chat + push) when done, so " +
		"he can walk away or hang up a voice call. Use this for anything that would otherwise take many tool " +
		"turns. Do NOT use it for quick one-shot actions (a single file read, a calendar lookup, sending one " +
		"message) - just do those inline."
}

func (b *BackgroundAgent) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type": "string",
				"description": "Complete, self-contained description of what to build. The background agent does NOT " +
					"see this conversation - include every detail it needs: what to build, where (repo / path / project), " +
					"constraints, and what 'done' looks like.",
			},
			"context_brief": map[string]any{
				"type":        "string",
				"description": "Optional extra background: relevant file paths, identifiers, prior decisions, or links the agent should know.",
			},
			"repo": map[string]any{
				"type":        "string",
				"description": "Absolute path of the repo to work in (e.g. /Users/<you>/Dev/infinity on the Mac, /workspace/infinity on the cloud). Strongly recommended: Claude Code reads that repo's CLAUDE.md from here. Inferred from the task text when omitted.",
			},
			"allowed_tools": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional exact tool names to restrict the background agent to. Leave empty for the full default loadout (read/write/exec) - usually correct for a build.",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Wall-clock ceiling for the whole background run (default 1800, max 3600).",
				"default":     1800,
			},
		},
		"required": []string{"task"},
	}
}

func (b *BackgroundAgent) Execute(ctx context.Context, input map[string]any) (string, error) {
	if b.Loop == nil {
		return "", errors.New("background_build: no loop wired")
	}
	task, _ := input["task"].(string)
	task = strings.TrimSpace(task)
	if task == "" {
		return `{"error":"task is required and must be non-empty"}`, nil
	}
	brief, _ := input["context_brief"].(string)

	allowedRaw, _ := input["allowed_tools"].([]any)
	allowed := make([]string, 0, len(allowedRaw))
	for _, v := range allowedRaw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			allowed = append(allowed, strings.TrimSpace(s))
		}
	}

	timeout := backgroundDefaultTimeout
	if v, ok := input["timeout_seconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
	}
	if timeout > backgroundTimeoutCeiling {
		timeout = backgroundTimeoutCeiling
	}

	// Capture the parent (calling) session NOW, while the request context
	// still carries it. The detached goroutine runs on context.Background()
	// and would otherwise lose it. Collapse a background child to the chat
	// session that owns it, so a build started from inside a build still
	// binds its plan/dock to the boss's conversation, never to a throwaway
	// child id (that is how four ownerless "active" plans appeared).
	parentSession := tools.SessionForPublish(tools.SessionIDFromContext(ctx))
	if IsSyntheticSessionID(parentSession) {
		// A delegate / peer / unbound child has no conversation to report
		// into: its build would be invisible and unownable. Do the work here.
		return `{"error":"background_build is only available from a chat session; you are a sub-agent. Do the work in this session instead (fs_edit / bash_run / claude_code__*), or return it to the parent as your result."}`, nil
	}

	runID := uuid.NewString()
	if existing, busy := b.claimSlot(parentSession, runID); busy {
		out, _ := json.Marshal(map[string]any{
			"status":  "already_running",
			"run_id":  existing,
			"message": "A background build is already running for this conversation (run " + existing + "). Do NOT start another one: " +
				"the same job twice just spends the boss's plan twice. Watch that run (watch_until on its run id) or wait for its result.",
		})
		return string(out), nil
	}
	bridgeKind := b.activeBridgeKind(ctx, parentSession)
	// The runner knows the bridge that is ACTUALLY active (health-routed),
	// not just the session's preference; on the Mac it is the engine too.
	var macBridge bridge.Bridge
	if b.Code != nil {
		if br, _, err := b.Code.ActiveBridge(ctx); err == nil && br != nil {
			bridgeKind = string(br.Name())
			if br.Name() == bridge.KindMac {
				macBridge = br
			}
		}
	}
	repo, _ := input["repo"].(string)
	if repo = strings.TrimSpace(repo); repo == "" {
		repo = inferRepoPath(task + "\n" + brief)
	}
	label := backgroundLabel(task, bridgeKind)
	message := "Build started in the background on the settings model. It'll keep running even if this session ends - the boss gets a chat message and a push when it's done."
	if macBridge != nil {
		label = "Claude Code: " + backgroundLabel(task, "")
		message = "Build started in the background on Claude Code (the boss's Claude subscription, on the Mac). It'll keep running even if this session ends - the boss gets a chat message and a push when it's done."
	}

	go b.runDetached(parentSession, runID, label, task, brief, repo, allowed, timeout, bridgeKind, macBridge)

	resp := map[string]any{
		"status":  "started",
		"run_id":  runID,
		"message": message,
	}
	out, _ := json.Marshal(resp)
	return string(out), nil
}

// runDetached owns the whole background lifecycle on a fresh, detached
// context so it survives the triggering session closing.
func (b *BackgroundAgent) runDetached(parentSession, runID, label, task, brief, repo string, allowed []string, timeout time.Duration, bridgeKind string, macBridge bridge.Bridge) {
	defer b.releaseSlot(parentSession, runID)
	// Cap the absolute lifetime so a wedged build can't leak a goroutine
	// forever. context.Background() (not the request ctx) is the point:
	// hanging up the voice call must NOT cancel the build.
	//
	// A Claude Code run is never killed by this deadline (2026-08-27: three
	// 14-minute runs were guillotined at a 30-minute budget with the work
	// half done); we poll it for the full ceiling and, if it is still going,
	// say so instead of pretending it finished or stopping it.
	if macBridge != nil && timeout < backgroundTimeoutCeiling {
		timeout = backgroundTimeoutCeiling
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()

	// mem_runs row: status=running now, ok/error + summary on finish.
	// Studio's useRuns() shows this live and it survives refresh / device
	// switch. We use Begin/Finish (not Track) so we can push live
	// Handle.Progress updates mid-flight — that's what keeps the pinned
	// BackgroundJobDock's progress bar + current-step durable across
	// navigation, refresh, and a second device (it reads mem_runs, not the
	// transient WS stream).
	handle := runs.BeginGlobal(ctx, runs.KindBackgroundBuild, runID, label, runs.SourceAgent)
	// The chat this build belongs to, and the repo it works in, ON THE ROW.
	// Without them a build that ends without a verdict is a row nothing can
	// act on: the continuation poller reads rows, not contexts, so a run with
	// no session has no address to report back to and gets skipped silently.
	// Same meta keys code_agent and the cron scheduler already use.
	handle.SetMetaString(ctx, "session_id", parentSession)
	handle.SetMetaString(ctx, "repo", repo)
	if b.OnProgress != nil {
		phase := float32(0.05)
		b.OnProgress(context.Background(), BackgroundProgress{
			RunID:         runID,
			ParentSession: parentSession,
			Task:          task,
			Label:         "queued background worker",
			Status:        "running",
			Progress:      &phase,
			Action:        "queued",
		})
	}
	handle.Progress(ctx, 0.05, "queued")

	summary, runErr := b.runToCompletion(ctx, parentSession, runID, task, brief, repo, allowed, handle, bridgeKind, macBridge)
	summary, stillWorking, errText := classifyBackgroundRun(summary, runErr)
	// Close on a detached context: a timed-out run's ctx is already
	// cancelled by the defer above, but the row must still flip to a terminal
	// state (otherwise it spins until the next boot's RecoverStranded sweep).
	if stillWorking {
		// The worker is STILL GOING. Closing this row 'error' is what produced
		// the red step + "Build failed" push for work that was landing on disk.
		// FinishInterrupted closes it 'ok' + meta.stopped_reason='still_working'
		// with a summary that says plainly it hasn't finished, so nothing
		// downstream can read it as either a failure OR a success.
		handle.FinishInterrupted(context.Background(), runs.StoppedStillWorking, summary)
	} else {
		handle.Finish(context.Background(), runErr, "")
	}

	res := BackgroundResult{
		RunID:         runID,
		ParentSession: parentSession,
		Task:          task,
		Summary:       summary,
		StillRunning:  stillWorking,
		Err:           errText,
		DurationMS:    time.Since(start).Milliseconds(),
	}
	if b.OnDone != nil {
		// Detached notify ctx - the run ctx may already be cancelled by
		// the timeout/defer above by the time we get here.
		b.OnDone(context.Background(), res)
	}
}

// claimSlot reserves the parent session's single build slot for runID.
// Returns the running run id and busy=true when one is already in flight.
func (b *BackgroundAgent) claimSlot(parent, runID string) (string, bool) {
	if parent == "" {
		return "", false
	}
	b.inflightMu.Lock()
	defer b.inflightMu.Unlock()
	if b.inflight == nil {
		b.inflight = map[string]string{}
	}
	if existing, ok := b.inflight[parent]; ok && existing != "" {
		return existing, true
	}
	b.inflight[parent] = runID
	return runID, false
}

// releaseSlot frees the parent's slot when the run that holds it finishes.
func (b *BackgroundAgent) releaseSlot(parent, runID string) {
	if parent == "" {
		return
	}
	b.inflightMu.Lock()
	defer b.inflightMu.Unlock()
	if b.inflight[parent] == runID {
		delete(b.inflight, parent)
	}
}

// runToCompletion spins an ephemeral session, runs the agent loop on the
// DEFAULT (settings) model to completion, and returns the final text.
// Mirrors delegate.run, minus the synchronous-return contract.
func (b *BackgroundAgent) runToCompletion(ctx context.Context, parentSession, runID, task, brief, repo string, allowed []string, handle *runs.Handle, bridgeKind string, macBridge bridge.Bridge) (string, error) {
	// Mac bridge: Claude Code does the coding on the boss's subscription.
	if macBridge != nil && b.Code != nil {
		return b.runOnClaudeCode(ctx, parentSession, runID, task, brief, repo, handle, macBridge)
	}
	childID := backgroundSessionIDPrefix + uuid.NewString()
	child := b.Loop.GetOrCreateSession(childID)
	defer b.Loop.ClearSession(childID)

	// Bind this child session to its mem_runs row so the native todo_write
	// tool (executing inside this loop) can find the row to write its checklist
	// onto. Bridge-agnostic: keyed by session id, independent of Mac/Cloud.
	tools.RegisterRunForSession(childID, runID, parentSession)
	defer tools.UnregisterRunForSession(childID)

	// Full default loadout unless the caller narrowed it - a build needs
	// write/exec, so we do NOT fall back to the read-only delegate set.
	if len(allowed) > 0 {
		child.Active.Replace(allowed)
	}
	// Always keep todo_write loaded so the live checklist works in both the
	// default loadout and a narrowed allowed_tools (permanent: ttl=0).
	child.Active.Load([]string{"todo_write"}, 0)

	// persist mirrors a live progress beat onto the mem_runs row so the dock
	// (which reads useRuns, not the WS) stays current — but ONLY while the
	// agent hasn't taken over with its own todo_write checklist. Once todos
	// exist, todo_write owns progress + progress_label (real X/Y), and the
	// tool-call heuristic must not clobber it.
	persist := func(fraction float32, progressLabel string) {
		if tools.SessionHasTodos(childID) {
			return
		}
		handle.Progress(ctx, fraction, progressLabel)
	}

	base := backgroundBasePrompt
	if trimmed := strings.TrimSpace(brief); trimmed != "" {
		base = base + "\n\n## Brief\n" + trimmed
	}
	child.SystemPromptOverride = base
	if bridgeKind != "" {
		handle.SetMetaString(ctx, "worker", backgroundWorkerLabel(bridgeKind))
		handle.SetMetaString(ctx, "backend", backgroundBackendLabel(bridgeKind))
	}

	events := make(chan RunEvent, 256)
	var (
		finalText     strings.Builder
		runErr        error
		wg            sync.WaitGroup
		toolCallCount int
	)
	publishProgress := b.progressPublisher(runID, parentSession, task, persist,
		func(k, v string) { handle.SetMetaString(ctx, k, v) })
	publishProgress("starting background build", "setup", "", 0, func() *float32 { v := float32(0.1); return &v }())
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range events {
			switch ev.Kind {
			case EventDelta:
				finalText.WriteString(ev.TextDelta)
				if strings.TrimSpace(ev.TextDelta) != "" {
					phase := float32(0.95)
					publishProgress("writing final summary", "summary", "", toolCallCount, &phase)
				}
			case EventToolCall:
				if ev.ToolCall != nil {
					toolCallCount++
					phase := progressForToolCalls(toolCallCount)
					action := backgroundActionName(ev.ToolCall.Name)
					detail := backgroundProgressDetail(ev.ToolCall.Input)
					// Record the live current target (file/command) on the run
					// row so the expanded dock shows it under the checklist,
					// independent of whether a todo is in_progress. Works for
					// both bridges (claude_code__* file_path + fs_*/bash_run path/cmd).
					if detail != "" {
						handle.SetMetaString(ctx, "currentFile", detail)
					}
					publishProgress(backgroundProgressLabel(ev.ToolCall.Name, toolCallCount), action, detail, toolCallCount, &phase)
					// SAME VISIBILITY ON BOTH BRIDGES. A Mac build forwards
					// Claude Code's own steps into the boss's chat; a cloud
					// build runs the settings model in a CHILD session whose
					// tool calls never reached him at all, so the identical
					// work read as one progress label on one bridge and a full
					// worklog on the other. The bridge decides which model
					// codes — never how much of it he gets to see.
					b.forwardStep(runID, parentSession, tools.NestedStep{
						CallID:    ev.ToolCall.ID,
						Tool:      ev.ToolCall.Name,
						Input:     ev.ToolCall.Input,
						StartedAt: startedOr(ev.ToolCall.StartedAt),
					})
				}
			case EventToolResult:
				if ev.ToolResult != nil {
					b.forwardStep(runID, parentSession, tools.NestedStep{
						CallID:    ev.ToolResult.ID,
						Tool:      ev.ToolResult.Name,
						Output:    ev.ToolResult.Output,
						IsError:   ev.ToolResult.IsError,
						Done:      true,
						StartedAt: startedOr(ev.ToolResult.StartedAt),
						EndedAt:   startedOr(ev.ToolResult.EndedAt),
					})
				}
			case EventThinking:
				if text := strings.TrimSpace(ev.ThinkingDelta); text != "" {
					phase := progressForToolCalls(toolCallCount)
					publishProgress(text, "thinking", "", toolCallCount, &phase)
				}
			case EventError:
				runErr = errors.New(ev.Error)
			}
		}
	}()

	// Empty model string → the loop's default provider = settings model
	// (gpt-5.4 over the OAuth subscription). The realtime API is never
	// touched here.
	err := b.Loop.Run(ctx, childID, task, "", nil, events)
	close(events)
	wg.Wait()

	if err != nil && runErr == nil {
		runErr = err
	}
	summary := strings.TrimSpace(finalText.String())
	if summary == "" && runErr == nil {
		summary = "Background build completed (no summary text produced)."
	}
	return summary, runErr
}

// forwardStep sends one step of a cloud build into the chat that started it,
// addressed and id-namespaced exactly the way the Mac path does so the two
// produce identical rows. Nil-safe: unwired, the build is as opaque as before.
func (b *BackgroundAgent) forwardStep(runID, parentSession string, step tools.NestedStep) {
	if b == nil || b.OnStep == nil || strings.TrimSpace(step.CallID) == "" || strings.TrimSpace(step.Tool) == "" {
		return
	}
	// A build fired from a cron or a sub-agent has no conversation to report
	// into; forwarding there would be a dropped frame and a failed observation
	// write per step. Its run row still carries the progress, as it always did.
	if !isChatSessionID(parentSession) {
		return
	}
	step.RunID = runID
	step.SessionID = parentSession
	step.CallID = tools.NestedStepID(runID, step.CallID)
	b.OnStep(context.Background(), step)
}

// isChatSessionID reports whether sid is a real conversation (a uuid), rather
// than an ephemeral child a build cannot deliver anything into.
func isChatSessionID(sid string) bool {
	if len(sid) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := sid[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// startedOr returns t, or now when the event carried no stamp.
func startedOr(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}

// progressForToolCalls maps a running tool-call count onto the dock's bar:
// stage-based, asymptotic (setup 0.05, each call advances, wrap-up reserved).
// The curve itself lives in tools so code_agent's inline runs draw the SAME
// bar for the same amount of work - two curves would make the same job look
// like different amounts of progress depending on which engine ran it.
func progressForToolCalls(count int) float32 {
	return tools.ProgressForSteps(count)
}

// progressPublisher returns the one function both engines (the settings-model
// loop and Claude Code) call to report a stage: it broadcasts the live card
// into the parent chat (deduped on label) and, via persist, mirrors it onto
// the mem_runs row so the pinned dock stays current across navigation /
// refresh / device.
func (b *BackgroundAgent) progressPublisher(runID, parentSession, task string, persist func(float32, string), meta func(string, string)) func(label, action, detail string, step int, progress *float32) {
	lastProgress := ""
	lastActivity := ""
	return func(label, action, detail string, step int, progress *float32) {
		// THE ACTIVITY FINGERPRINT, stamped for BOTH engines at the one place
		// both of them pass through.
		//
		// It has to key on the tool and its target, never on the label: a
		// label embeds elapsed time, so it changes every beat whether or not
		// anything happened, and "no new activity for 12 minutes" would stop
		// meaning anything. That fingerprint is what the stall detector and
		// the continuation poller both read.
		//
		// The cloud path had NO fingerprint at all, so `lastActivitySQL` fell
		// back to started_at and a perfectly healthy cloud build got its
		// progress line rewritten to "no new activity for 12m" while it was
		// mid-edit — the same false status the boss keeps catching, on the
		// bridge nobody remembered to cover.
		if key := strings.TrimSpace(action) + "\x00" + strings.TrimSpace(detail); key != "\x00" && key != lastActivity {
			lastActivity = key
			if meta != nil {
				meta("activity_key", key)
				meta("activity_at", time.Now().UTC().Format(time.RFC3339))
			}
		}
		trimmed := strings.TrimSpace(label)
		if trimmed == "" || trimmed == lastProgress {
			return
		}
		lastProgress = trimmed
		if b.OnProgress != nil {
			b.OnProgress(context.Background(), BackgroundProgress{
				RunID:         runID,
				ParentSession: parentSession,
				Task:          task,
				Label:         trimmed,
				Status:        "running",
				Progress:      progress,
				Step:          step,
				Action:        strings.TrimSpace(action),
				Detail:        strings.TrimSpace(detail),
			})
		}
		if progress != nil && persist != nil {
			persist(*progress, backgroundDurableLabel(step, strings.TrimSpace(action), strings.TrimSpace(detail), trimmed))
		}
	}
}

// runOnClaudeCode is the Mac path: the whole task goes to `claude -p` on the
// boss's Claude subscription through the shared runner. The run row carries
// the engine, the subscription proof (meta.auth → meta.backend), the pinned
// model/effort, and Claude's live activity; the parent chat gets the same
// progress card the settings-model loop would post.
func (b *BackgroundAgent) runOnClaudeCode(ctx context.Context, parentSession, runID, task, brief, repo string, handle *runs.Handle, mac bridge.Bridge) (string, error) {
	model := b.Code.DefaultModel()
	effort := b.Code.DefaultEffort()
	handle.SetMetaString(ctx, "worker", "Claude Code")
	handle.SetMetaString(ctx, "engine", "claude_code")
	handle.SetMetaString(ctx, "backend", "your Claude subscription")
	handle.SetMetaString(ctx, "model", model)
	handle.SetMetaString(ctx, "effort", effort)
	handle.SetMetaString(ctx, "repo", repo)

	publish := b.progressPublisher(runID, parentSession, task, func(fraction float32, label string) {
		handle.Progress(ctx, fraction, label)
	}, func(k, v string) { handle.SetMetaString(ctx, k, v) })
	publish("starting Claude Code on the Mac", "setup", "", 0, func() *float32 { v := float32(0.1); return &v }())

	full := task
	if trimmed := strings.TrimSpace(brief); trimmed != "" {
		full += "\n\n## Brief\n" + trimmed
	}
	maxWait := backgroundTimeoutCeiling
	if dl, ok := ctx.Deadline(); ok {
		maxWait = time.Until(dl) - 15*time.Second
	}
	summary, err := b.Code.Run(ctx, tools.ClaudeCodeJob{
		Bridge: mac,
		JobID:  runID,
		Task:   full,
		Repo:   repo,
		Model:  model,
		Effort: effort,
		// A background build runs on a DETACHED context by design, so there is
		// no session on it to infer the chat from. Naming it here is what puts
		// the nested job's steps in the boss's conversation instead of nowhere
		// — the same omission that once left background builds out of the
		// activity fingerprint entirely.
		ParentSession: parentSession,
		MaxWait:       maxWait,
		KillOnCancel:  false,
		Heartbeat: func(label, action, detail string, step int) {
			// step is counted by the runner's own poll loop, so this bar and
			// code_agent's advance identically for identical work. Both used
			// to keep their own tally with slightly different rules.
			phase := progressForToolCalls(step)
			publish(label, action, detail, step, &phase)
		},
		SetMeta: func(key, value string) {
			handle.SetMetaString(ctx, key, value)
			if key == "auth" {
				handle.SetMetaString(ctx, "backend", value)
			}
		},
	})
	// STILL WORKING IS NOT A FAILURE. The wait window closed while Claude Code
	// kept going on the Mac (nohup-reparented, KillOnCancel:false above). Carry
	// its honest wording up as the summary but keep the TYPED error intact so
	// runDetached classifies it with errors.As — never by reading these words.
	if msg := tools.StillRunningMessage(err); msg != "" {
		return msg, err
	}
	if err == nil && strings.TrimSpace(summary) == "" {
		summary = "Claude Code finished (no summary text produced)."
	}
	return summary, err
}

// repoPathRe finds the first repo path a task names: the boss's Mac layout
// (~/Dev/<repo>) or the cloud workspace (/workspace/<repo>).
var repoPathRe = regexp.MustCompile(`(?:/Users/[^\s"'` + "`" + `]+/Dev/[\w.-]+|~/Dev/[\w.-]+|/workspace/[\w.-]+)`)

// inferRepoPath returns the first repo path mentioned in text, "" when none.
func inferRepoPath(text string) string {
	return strings.TrimRight(repoPathRe.FindString(text), ".,;:")
}

// backgroundLabel produces the short human string Studio shows next to the
// run spinner. Prefix with the execution venue so the run never advertises
// Claude Code; background_build always runs on the settings model.
func backgroundLabel(task, bridgeKind string) string {
	prefix := backgroundWorkerLabel(bridgeKind)
	line := task
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	const max = 80
	if len(line) > max {
		line = line[:max] + "…"
	}
	if line == "" {
		if prefix == "" {
			return "background build"
		}
		return prefix + ": background build"
	}
	if prefix == "" {
		return line
	}
	return prefix + ": " + line
}

func backgroundProgressLabel(toolName string, toolCallCount int) string {
	name := backgroundActionName(toolName)
	if name == "" {
		if toolCallCount <= 0 {
			return "working"
		}
		return fmt.Sprintf("running step %d", toolCallCount)
	}
	if toolCallCount <= 0 {
		return name
	}
	return fmt.Sprintf("step %d: %s", toolCallCount, name)
}

// backgroundActionName reduces a fully-qualified tool name to the short verb
// Studio shows as a chip — "claude_code__edit" → "edit", "composio__gmail_send"
// → "gmail send". Returns "" when the name is empty.
func backgroundActionName(toolName string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return ""
	}
	name = strings.TrimPrefix(name, "functions.")
	name = strings.TrimPrefix(name, "claude_code__")
	name = strings.TrimPrefix(name, "composio__")
	name = strings.ReplaceAll(name, "_", " ")
	return strings.TrimSpace(strings.ToLower(name))
}

// backgroundProgressDetail pulls the most informative target out of a tool
// call's input so the progress card can show WHAT the agent is touching — the
// file being written/edited/read, or a clipped command/query for shell and
// search calls. Returns "" when nothing useful is present.
func backgroundProgressDetail(input map[string]any) string {
	if input == nil {
		return ""
	}
	// File-shaped tools: surface the path verbatim (it's already concise and
	// the most useful "current file being written" signal).
	for _, k := range []string{"file_path", "path", "filename", "file", "notebook_path"} {
		if v, ok := input[k].(string); ok {
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
	}
	// Shell / search / fetch: clip the command or query to a single line.
	for _, k := range []string{"command", "cmd", "script", "pattern", "query", "url"} {
		if v, ok := input[k].(string); ok {
			if s := strings.TrimSpace(v); s != "" {
				return clipBackgroundDetail(s)
			}
		}
	}
	return ""
}

// backgroundDurableLabel builds the compact one-line string persisted to
// mem_runs.progress_label, read by the pinned dock: "step 3 · edit: path.ts".
// Falls back to the raw label when there's no action/detail (setup phases).
func backgroundDurableLabel(step int, action, detail, fallback string) string {
	body := fallback
	switch {
	case detail != "" && action != "":
		body = action + ": " + detail
	case detail != "":
		body = detail
	case action != "":
		body = action
	}
	if step > 0 {
		return fmt.Sprintf("step %d · %s", step, body)
	}
	return body
}

func clipBackgroundDetail(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	const max = 120
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

const backgroundBasePrompt = `You are the full autonomous coding agent, running in the BACKGROUND on a detached session. The boss kicked off this task by voice or chat and has moved on - he is NOT watching. You have the complete tool loadout (read, write, edit, exec, memory, bridges).

Your job:
1. Complete the task end to end. Actually write the code / make the changes / run the builds - do not just plan.
2. Use the same care and conventions you would in a live session. Read before you write; verify your work.
3. When finished, your FINAL message must be a concise plain-language summary of what you did: what you built or changed, which files, and anything the boss must know (decisions, follow-ups, anything that needs his approval). This summary is what gets sent to his phone - make it scannable, not a wall of text.

## Track your work with a checklist
At the very START of the task, call todo_write ONCE with your full ordered plan — every step as a short item with status "pending" — and pass the repo/project you're working in. As you work, call todo_write again to mark the step you're starting "in_progress" and finished steps "completed" (exactly one item in_progress at a time). Each call replaces the whole list, so always send the complete checklist. This is the ONLY way the boss sees your progress live while you run detached, so keep it current. Skip the checklist only for a genuinely trivial one-step task.

There is no one to ask mid-task, so make sensible decisions and note them in the summary rather than stopping. If a step genuinely cannot proceed (it needs a Trust-gated approval, a missing credential, or a decision only the boss can make), do as much as you safely can, then clearly state what is blocked and why in the summary.`

func (b *BackgroundAgent) activeBridgeKind(ctx context.Context, sessionID string) string {
	if b == nil || b.Bridge == nil {
		return ""
	}
	if b.Bridge(ctx, sessionID) == bridge.PrefCloud {
		return string(bridge.KindCloud)
	}
	return string(bridge.KindMac)
}

func backgroundWorkerLabel(bridgeKind string) string {
	if bridgeKind == string(bridge.KindCloud) {
		return "Cloud agent"
	}
	if bridgeKind == string(bridge.KindMac) {
		return "Mac agent"
	}
	return ""
}

func backgroundBackendLabel(string) string {
	return "settings model"
}
