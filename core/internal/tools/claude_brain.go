package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/llm"
)

// Converse: Claude Code driven as a CONVERSATIONAL brain rather than a coding
// job.
//
// The coding path (code_agent.go) is job-shaped: it takes a task and a git
// repo, books a run row, forwards steps, and can detach and be followed in the
// background. A chat turn is none of those things - it is one prompt, watched
// to completion, streamed into the reply - so this is a sibling loop over the
// same primitives (the job files, the detached launch, the stream-json poll)
// rather than a sixth set of options bolted onto Run. Both still go through
// the SAME subscription proof, because that rule has no exceptions.
//
// What makes it a real brain rather than a sandboxed one is the --mcp-config
// written below: it points the session at Core's own MCP endpoint, so Claude
// Code can call Infinity's whole tool registry - memory, surface, connectors -
// through the same gates and hooks a chat turn uses.

var brainInfoLog = log.New(os.Stdout, "", log.LstdFlags)

const (
	// brainTmpDir is where a conversation turn's stream, status and config
	// files live on the Mac. Separate from the coding path's tmp dir so a
	// chat turn and a build can never collide on a file name.
	brainTmpDir = "/tmp/inf-brain"

	// brainWorkspaceMac is the working directory a conversation runs in ON THE
	// MAC. Claude Code always has a cwd; a chat turn has no repo, so it gets a
	// stable scratch directory of its own rather than being turned loose in
	// whatever the boss last built. Created and git-initialised on first use,
	// because several of Claude Code's affordances assume a repository.
	//
	// Spelled with "~", never "$HOME": this string is both pasted into a shell
	// script AND sent as a bridge cwd, and the bridge stats the cwd literally.
	// A "$HOME/..." here made every Mac chat turn come back as 400 "cwd not a
	// directory: $HOME/.infinity/brain" (2026-08-31).
	brainWorkspaceMac = "~/.infinity/brain"

	// bridgeHome is home on either box, in the one spelling the bridge
	// resolves. It is the cwd for the two calls that must NOT assume the
	// workspace exists yet: the sign-in probe (it reads ~/.claude.json and
	// does not care where it runs) and the launch, which is what creates it.
	bridgeHome = "~"

	// brainWorkspaceCloud is the cloud box's own persistent disk. Here the
	// opposite is right: /workspace IS Jarvis's computer, and a brain that
	// cannot see the files it has been building all week is the amnesia this
	// whole path exists to avoid.
	brainWorkspaceCloud = "/workspace"

	// brainPollEach is the cadence of the stream read, and it is what the
	// boss experiences as typing speed.
	//
	// The bridge is request/response, so text can only arrive as fast as we
	// ask for it. Claude Code emits real token deltas (--include-partial-
	// messages), so at this cadence they land in roughly third-of-a-second
	// batches: visibly streaming, at three round trips a second, which the
	// bridge carries comfortably.
	brainPollEach = 300 * time.Millisecond

	// brainMaxWait bounds one turn. Generous because the whole point of this
	// brain is long research and multi-step work, but finite so a wedged
	// session cannot hold a chat open forever.
	brainMaxWait = 20 * time.Minute

	// brainStartGrace is how long a launched turn may go with NO stream file
	// at all before the turn is called dead. The launch script opens the
	// stream file in its first millisecond (the `> out` redirection runs
	// before claude does), so a file still missing this long after "LAUNCHED"
	// means the process never existed: the shell that was meant to start it
	// failed, or something reaped it. Before this guard that case was
	// indistinguishable from a slow model - the poll said RUNNING because the
	// status file was absent, the stream was empty because the stream file
	// was absent, and the boss watched "thinking" for the whole 20-minute
	// ceiling (2026-09-06: every cloud launch for a day, none of which ever
	// ran). A brain that did not start must say so in seconds.
	brainStartGrace = 20 * time.Second

	// The read window, and the three numbers that keep one huge line from
	// silencing an entire turn.
	//
	// The failure this fixes (2026-09-01): the slice was read as
	// `tail -n +N | head -n 400 | head -c 48000` with no per-line clamp. When
	// the FIRST line at the cursor was itself bigger than the byte cap, the
	// slice came back with no newline in it at all, so nothing was complete,
	// the read position never advanced, and every poll for the rest of the
	// turn re-read the same truncated bytes and returned nothing. Claude kept
	// working; the boss watched "Working..." for ten minutes. A stream that
	// goes silent must never be indistinguishable from a brain that stopped.
	//
	// The coding path has had all three of these from the start and says why
	// in its own comment (claudeLineMaxCols / claudeChunkBytes /
	// claudeMaxDrains, code_agent.go). This is the same defence, same names,
	// because it is the same problem on the same kind of stream.
	//
	// brainLineMaxCols clamps EVERY line before the byte cap, so no single
	// event can fill the window on its own. A clamped line stops being valid
	// JSON and is skipped, which is the right trade: the one line big enough
	// to hit this is an assembled message whose live half already streamed.
	brainLineMaxCols = 8000
	// brainInitHeadBytes is how much of the HEAD of the stream is read, on
	// every poll until the session id is known, to find the
	// `system/init` line. That line is NOT small: on the boss's Mac it is
	// 24,616 chars (470 MCP tools, 101 slash commands, 64 skills, 26 agents
	// listed by name), so the streaming slice's per-line clamp above cuts it
	// off, it stops being JSON, and the session id was never seen. Every turn
	// then started cold - 54 separate Claude sessions for 54 messages, each
	// re-writing 80-105K tokens of cache (2026-09-04). The init line sits
	// after the SessionStart hook events (around line 7), so the head has to
	// cover several of those plus the whole init line.
	brainInitHeadBytes = 32000
	// brainLinesPerPoll / brainChunkBytes bound one read.
	brainLinesPerPoll = 400
	brainChunkBytes   = 48000
	// brainMaxDrains lets a BURST catch up inside one poll instead of
	// trickling out over the following seconds. A turn that emits 2,000 events
	// in a second should not take five seconds to show them.
	brainMaxDrains = 4
)

// brainWorkspace resolves the working directory for the bridge in play.
func brainWorkspace(b bridge.Bridge) string {
	if b != nil && b.Name() == bridge.KindCloud {
		return brainWorkspaceCloud
	}
	return brainWorkspaceMac
}

// brainHome is the cwd for the launch round trip, in the spelling THAT
// bridge resolves. The Mac bridge expands "~"; the cloud bridge does not - it
// joins a relative path under /workspace and stats the result, so "~" became
// "/workspace/~", the chdir failed, and the launch script never ran. The
// bridge reported that as HTTP 200 with exit_code -1 and empty output, which
// the launch used to accept as started (2026-09-06).
func brainHome(b bridge.Bridge) string {
	if b != nil && b.Name() == bridge.KindCloud {
		return brainWorkspaceCloud
	}
	return bridgeHome
}

// brainBoxName is how the box is named in a sentence to the boss.
func brainBoxName(b bridge.Bridge) string {
	if b != nil && b.Name() == bridge.KindCloud {
		return "the cloud box"
	}
	return "your Mac"
}

// BrainWiring is what Converse needs beyond the bridge: where Core is
// reachable from the Mac, and how to mint a token for it.
type BrainWiring struct {
	// CoreURL is Core's PUBLIC origin (INFINITY_PUBLIC_URL). The Mac reaches
	// Core over the internet, not the Railway private network, so an internal
	// hostname here would leave the brain with no tools at all.
	CoreURL string
	// MintToken issues a bearer for one session's MCP access.
	MintToken func(sessionID string) string
	// SubscriptionToken returns the stored CLAUDE_CODE_OAUTH_TOKEN, or "" if
	// none is saved. This is what lets the CLOUD box answer: it has no Claude
	// sign-in of its own, so the subscription travels as a token minted by
	// `claude setup-token` and pasted into Settings. Empty on the Mac path,
	// which uses the Mac's own sign-in instead and must never see a token
	// (one would outrank the sign-in and change which credential pays).
	SubscriptionToken func(ctx context.Context) string
}

// AttachBrain wires the conversational path. Called once from serve.go. Until
// it is called, Converse refuses rather than running a brain with no tools.
func (r *ClaudeCodeRunner) AttachBrain(w BrainWiring) {
	r.brain = w
}

// brainReady reports whether the MCP half is wired.
func (r *ClaudeCodeRunner) brainReady() bool {
	return r != nil && strings.TrimSpace(r.brain.CoreURL) != "" && r.brain.MintToken != nil
}

// Converse runs one chat turn on the Mac's Claude Code and streams it back.
// It implements llm.BrainRunner.
func (r *ClaudeCodeRunner) Converse(ctx context.Context, turn llm.BrainTurn, out chan<- llm.StreamEvent) (llm.Response, error) {
	// Where the wait before the first word actually goes.
	//
	// The boss: "as of now I wait 2-3 mins before seeing something." Five
	// things happen before Claude sees a token - a bridge health probe, a full
	// bash round trip to prove the subscription, the launch round trip, an MCP
	// handshake over the public internet (--strict-mcp-config, so the whole
	// tool registry is enumerated first), and Claude's own cold start. Cutting
	// the wrong one costs an evening, so each is timed and the numbers go in
	// one line per turn. Measure, then cut.
	t0 := time.Now()
	var tBridge, tAuth, tLaunch time.Duration

	b, _, err := r.ActiveBridge(ctx)
	tBridge = time.Since(t0)
	if err != nil {
		return llm.Response{}, err
	}
	if b == nil {
		return llm.Response{}, errors.New("Neither your Mac nor the cloud box is reachable right now, so there's nowhere for Claude to run.")
	}
	if !r.brainReady() {
		return llm.Response{}, errors.New("Claude Max isn't wired to Infinity's tools yet (INFINITY_PUBLIC_URL is unset), so it would answer with no memory. Set it and restart Core.")
	}

	jobID := fmt.Sprintf("brain-%d", time.Now().UnixNano())
	files := newBrainFiles(jobID)
	workspace := brainWorkspace(b)

	// Prove the subscription BEFORE launching. The proof differs by bridge but
	// the RULE does not: this brain spends the Max plan or it does not run.
	tAuthStart := time.Now()
	token, err := r.brainSubscription(ctx, b)
	tAuth = time.Since(tAuthStart)
	if err != nil {
		return llm.Response{}, err
	}

	// Minted against THIS conversation, so every tool the brain calls back
	// through MCP is attributed to it.
	mcpToken := r.brain.MintToken(turn.SessionID)
	if mcpToken == "" {
		return llm.Response{}, errors.New("couldn't mint a tool token for this turn, so I'd be answering with no memory. Nothing was sent.")
	}

	script := brainLaunchScript(files, turn, brainLaunch{
		workspace: workspace,
		coreURL:   r.brain.CoreURL,
		mcpToken:  mcpToken,
		subToken:  token,
		cloud:     b.Name() == bridge.KindCloud,
	})
	tLaunchStart := time.Now()
	body, code, ok := b.Post(ctx, "/bash", map[string]any{
		"cmd": script, "cwd": brainHome(b), "timeout_sec": 30,
	})
	tLaunch = time.Since(tLaunchStart)
	if !ok || code >= 300 {
		msg, _ := bridgeBashOutput(body)
		return llm.Response{}, fmt.Errorf("I couldn't start Claude on %s (the bridge said %d): %s", brainBoxName(b), code, strings.TrimSpace(msg))
	}
	if err := brainLaunched(b, body); err != nil {
		return llm.Response{}, err
	}
	brainInfoLog.Printf("claude_max: launched turn %s in %s (bridge=%s auth=%s launch=%s) resume=%q model=%q",
		jobID, time.Since(t0).Round(time.Millisecond),
		tBridge.Round(time.Millisecond), tAuth.Round(time.Millisecond), tLaunch.Round(time.Millisecond),
		turn.Resume, turn.Model)

	p := &brainPoll{
		b:         b,
		workspace: workspace,
		files:     files,
		turn:      turn,
		out:       out,
		line:      1,
		started:   time.Now(),
		setupTook: time.Since(t0),
		// The brain's own TodoWrite lands on the conversation's plan, the
		// same dock a coding job's checklist reaches (syncOwnPlan).
		plans:         r.plans,
		parentSession: turn.SessionID,
	}
	defer p.cleanup()
	return p.wait(ctx)
}

// brainLaunched checks that the launch script actually RAN. A 200 from the
// bridge only means the request was served; the shell it ran can still fail
// before the first line (a cwd it cannot enter, a profile that aborts), and
// the bridge reports that as exit_code -1 with no output. Accepting it as a
// launch is how a turn polls a stream that will never exist for twenty
// minutes. The script's last line is the marker, so its presence plus a zero
// exit is proof the whole thing ran.
func brainLaunched(b bridge.Bridge, body []byte) error {
	shellOut, exit := bridgeBashOutput(body)
	if exit == 0 && strings.Contains(shellOut, "LAUNCHED") {
		return nil
	}
	detail := strings.TrimSpace(shellOut)
	if detail == "" {
		detail = "it printed nothing"
	}
	return fmt.Errorf("I couldn't start Claude on %s: the launch shell exited %d before Claude ran (%s). Nothing was sent.", brainBoxName(b), exit, detail)
}

// --- files -------------------------------------------------------------------

type brainFiles struct {
	out      string
	err      string
	status   string
	settings string
	mcp      string
}

func newBrainFiles(jobID string) brainFiles {
	return brainFiles{
		out:      fmt.Sprintf("%s/%s.out", brainTmpDir, jobID),
		err:      fmt.Sprintf("%s/%s.err", brainTmpDir, jobID),
		status:   fmt.Sprintf("%s/%s.status", brainTmpDir, jobID),
		settings: fmt.Sprintf("%s/%s.settings.json", brainTmpDir, jobID),
		mcp:      fmt.Sprintf("%s/%s.mcp.json", brainTmpDir, jobID),
	}
}

// MCPToolTimeoutMillis is the per-tool-call timeout handed to Claude Code, in
// the milliseconds its MCP settings take: the coding job's full lifetime.
//
// Claude Code's timer for an HTTP MCP server is max(60s, the server's
// "timeout", MCP_TOOL_TIMEOUT). Unset, that is sixty seconds, and a tool call
// it aborts is a CANCELLED request on Core's side - which code_agent read as
// the Stop button and killed the job it was running for the brain, at 58-62s,
// seven times in a row (2026-09-02). Both the per-server key and the env var
// are set from this one number so the wall is never the first thing hit.
func MCPToolTimeoutMillis() int64 {
	return (codeAgentMaxWait + codeAgentJobGrace).Milliseconds()
}

// brainMCPConfig is the --mcp-config Claude Code reads: one HTTP MCP server,
// Infinity itself, carrying a bearer minted for this turn.
func brainMCPConfig(coreURL, token string) string {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"infinity": map[string]any{
				"type":    "http",
				"url":     strings.TrimRight(coreURL, "/") + "/api/mcp/server",
				"headers": map[string]string{"Authorization": "Bearer " + token},
				// Per-server tool-call timeout (ms). Without it Claude aborts
				// every call to Infinity at sixty seconds - see
				// MCPToolTimeoutMillis.
				"timeout": MCPToolTimeoutMillis(),
			},
		},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// brainLaunch is everything the launch script needs that is not the turn.
type brainLaunch struct {
	workspace string
	coreURL   string
	// mcpToken authorises the session against Infinity's tool registry.
	mcpToken string
	// subToken is the CLAUDE_CODE_OAUTH_TOKEN. Set on the CLOUD box, which
	// has no Claude sign-in of its own. Empty on the Mac, where exporting one
	// would OUTRANK the sign-in and change which credential pays.
	subToken string
	cloud    bool
}

// brainLaunchScript starts the detached turn.
//
// It mirrors claudeLaunchScript's hard-won shape - clear the API key so only
// the subscription can answer, export the prompt through the environment so
// no quoting can break on it, `set -m` so the whole tree stays reapable - and
// adds the two things a conversation needs: a workspace that exists, and the
// MCP config that gives the brain Infinity's tools.
//
// --strict-mcp-config is deliberate: the session gets Infinity's registry and
// nothing else. Without it Claude Code would also load whatever project and
// user scoped servers happen to sit on the box, which is both a surprise and
// a prompt-cache invalidator every time that set changes.
func brainLaunchScript(f brainFiles, turn llm.BrainTurn, l brainLaunch) string {
	inner := fmt.Sprintf(
		`claude -p "$INF_PROMPT" ${INF_RESUME:+--resume "$INF_RESUME"} ${INF_MODEL:+--model "$INF_MODEL"} `+
			`${INF_EFFORT:+--effort "$INF_EFFORT"} ${INF_MAX_TURNS:+--max-turns "$INF_MAX_TURNS"} --output-format stream-json --verbose --include-partial-messages `+
			`--permission-mode bypassPermissions --mcp-config %s --strict-mcp-config --settings %s `+
			`> %s 2> %s; echo $? > %s`,
		f.mcp, f.settings, f.out, f.err, f.status,
	)
	steps := []string{
		"mkdir -p " + brainTmpDir,
		"mkdir -p " + l.workspace,
		// git init is idempotent and makes the directory behave like the
		// repository several of Claude Code's affordances assume.
		"cd " + l.workspace + " && [ -d .git ] || git init -q " + l.workspace,
		"cat > " + f.settings + " <<'INFEOF'\n" + codeAgentSettingsJSON() + "\nINFEOF",
		"cat > " + f.mcp + " <<'INFEOF'\n" + brainMCPConfig(l.coreURL, l.mcpToken) + "\nINFEOF",
		// Both files carry a live credential. Nobody else on the box needs
		// to read either one.
		"chmod 600 " + f.mcp,
		// An API key in the shell would pre-empt the subscription and bill
		// per token. True on both bridges.
		"unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN",
		// The default per-call MCP timeout is a coding job's lifetime, not
		// sixty seconds. Belt to the mcp-config's braces.
		fmt.Sprintf("export MCP_TOOL_TIMEOUT=%d", MCPToolTimeoutMillis()),
	}
	if l.cloud && l.subToken != "" {
		// The cloud box has no browser and no sign-in of its own, so the
		// subscription travels as a token. Exported for this command only -
		// it is never written to the image, a dotfile, or the shell profile.
		steps = append(steps, "export CLAUDE_CODE_OAUTH_TOKEN="+shellQuote(l.subToken))
		// The cloud box runs as root, and Claude Code refuses
		// bypassPermissions as root unless it is told it is in a sandbox.
		// Without this line every cloud turn would exit on its first
		// millisecond with "--dangerously-skip-permissions cannot be used
		// with root/sudo privileges". See claudeSandboxExport.
		steps = append(steps, claudeSandboxExport(true))
	} else {
		// On the Mac the sign-in is the credential, and a stray token in the
		// environment would silently outrank it.
		steps = append(steps, "unset CLAUDE_CODE_OAUTH_TOKEN")
	}
	steps = append(steps,
		"export INF_PROMPT="+shellQuote(turn.Prompt),
		"export INF_MODEL="+shellQuote(turn.Model),
		"export INF_EFFORT="+shellQuote(turn.Effort),
		"export INF_RESUME="+shellQuote(turn.Resume),
		// Empty means no cap: the ${VAR:+...} expansion drops the flag, the
		// same way an empty resume drops --resume.
		"export INF_MAX_TURNS="+shellQuote(brainMaxTurns(turn.MaxTurns)),
		"cd "+l.workspace,
		"set -m",
		"nohup bash -c "+shellQuote(inner)+" >/dev/null 2>&1 &",
		"echo LAUNCHED",
	)
	return strings.Join(steps, "\n")
}

// brainMaxTurns renders the turn cap for the launch environment; zero or
// less is "no cap" and renders empty so the flag is omitted.
func brainMaxTurns(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// brainSubscription proves the Max plan is what will pay, and returns the
// token to export when the bridge needs one.
//
// The two bridges hold the credential differently and that is the ONLY
// difference. The Mac has a real Claude sign-in, so it is read back and
// checked. The cloud box has no browser, so the subscription arrives as a
// CLAUDE_CODE_OAUTH_TOKEN the boss minted with `claude setup-token` - which
// Anthropic issues only against a Pro/Max/Team/Enterprise plan, so its mere
// presence IS the proof. Neither path can fall back to an API key.
func (r *ClaudeCodeRunner) brainSubscription(ctx context.Context, b bridge.Bridge) (string, error) {
	if b.Name() == bridge.KindCloud {
		if r.brain.SubscriptionToken == nil {
			return "", errors.New("The cloud box has no Claude sign-in saved, so it can't run this brain. Add your Claude token in Settings, or wake the Mac.")
		}
		token := strings.TrimSpace(r.brain.SubscriptionToken(ctx))
		if token == "" {
			return "", errors.New("The cloud box needs your Claude token before it can think on your Max plan. Run `claude setup-token` on your Mac and paste the result into Settings.")
		}
		return token, nil
	}
	// A proof that is still warm is still true. See ClaudeCodeRunner.authOK:
	// this probe is a full round trip to the Mac and it was paid before every
	// message, launched or not.
	if r.authProven() {
		return "", nil
	}
	// Probed from home, not the workspace: the workspace does not exist until
	// the launch script makes it, and a probe that cwd's into a directory it
	// is about to create fails every FIRST turn on a fresh Mac.
	auth, err := r.probeAuth(ctx, b, bridgeHome)
	if err != nil {
		// probeAuth speaks for the coding tool. A chat turn is not a build,
		// and "code_agent: ..." arriving mid-conversation reads as a bug in
		// something the boss never asked for.
		return "", fmt.Errorf("I couldn't read the Claude sign-in on your Mac, so I didn't start the turn. %s", brainProbeDetail(err))
	}
	if !auth.Subscription() {
		return "", &notSubscriptionError{auth: auth}
	}
	r.rememberAuth()
	return "", nil
}

// brainAuthTTL is how long a proven Mac subscription is trusted without
// re-asking. Short enough that signing out is noticed within a message or two,
// long enough that a back-and-forth conversation pays for the proof once.
const brainAuthTTL = 90 * time.Second

func (r *ClaudeCodeRunner) authProven() bool {
	r.authMu.Lock()
	defer r.authMu.Unlock()
	return !r.authOK.IsZero() && time.Since(r.authOK) < brainAuthTTL
}

func (r *ClaudeCodeRunner) rememberAuth() {
	r.authMu.Lock()
	r.authOK = time.Now()
	r.authMu.Unlock()
}

// --- poll --------------------------------------------------------------------

// brainPoll watches one conversation turn and turns its stream into events.
type brainPoll struct {
	b bridge.Bridge
	// workspace is the cwd every poll and cleanup round trip runs in. Held
	// per-poll because it differs by bridge and a turn never changes bridge
	// mid-flight.
	workspace string
	files     brainFiles
	turn      llm.BrainTurn
	out       chan<- llm.StreamEvent
	line      int
	started   time.Time

	// setupTook is everything paid before Claude was launched, carried so the
	// first-token line can report the whole wait in one place.
	setupTook time.Duration
	// firstEvent marks the first time this turn produced anything at all, so
	// "time to first word" is measured rather than guessed.
	firstEvent time.Time

	sessionSeen bool
	// plans mirrors the brain's own TodoWrite list onto the conversation's
	// plan; parentSession is that conversation; planPrint is the last list
	// written so an unchanged one is not rewritten on every poll (see
	// syncOwnPlan). Nil plans means no dock is wired and the path is inert.
	plans         NestedPlanSink
	parentSession string
	planPrint     string
	// toolNames maps a call id to its tool, so the result half of the pair
	// can be reported under the name the boss saw on the call.
	toolNames map[string]string
	// streamed accumulates every text delta already sent. finish() checks it
	// so the answer is never printed twice, and so a turn that streamed
	// nothing still produces a reply.
	streamed string
	// sentCalls are the tool calls already posted from the LIVE path, so the
	// assembled message that repeats them cannot double-post.
	sentCalls map[string]bool
	// openBlock is the tool_use block currently being written, so an
	// arguments delta knows which call it belongs to (the delta itself
	// carries no id).
	openBlock string
}

// wait polls to completion, emitting events as the stream produces them.
func (p *brainPoll) wait(ctx context.Context) (llm.Response, error) {
	deadline := time.Now().Add(brainMaxWait)
	ticker := time.NewTicker(brainPollEach)
	defer ticker.Stop()

	// Read once before the first tick. The ticker used to fire first, so every
	// turn began with a third of a second of guaranteed silence for no reason.
	first := true
	for {
		if first {
			first = false
			if err := ctx.Err(); err != nil {
				return llm.Response{}, err
			}
		} else {
			select {
			case <-ctx.Done():
				// The boss stopped the turn (or it timed out). Leave the
				// Claude session resumable rather than tearing it down: his
				// next message picks the thread back up.
				return llm.Response{}, ctx.Err()
			case <-ticker.C:
			}
		}
		read, ok := p.pollOnce(ctx)
		if !ok {
			// One missed poll is not a verdict about the turn - the bridge
			// blinked. Try again.
			if time.Now().After(deadline) {
				return llm.Response{}, errors.New("lost contact with the Mac while Claude was working; the turn may still be running")
			}
			continue
		}
		// The head carries the whole init line; the streaming slice carries
		// it clamped. Either is enough for the tolerant parser, and the head
		// is tried first because it is the one that is guaranteed complete.
		p.noteSession(read.init)
		p.noteSession(read.fresh)
		if p.firstEvent.IsZero() && strings.TrimSpace(read.fresh) != "" {
			p.firstEvent = time.Now()
			// The number the boss actually feels: from him pressing send to
			// the first thing appearing. Broken out so it is obvious which
			// half to attack - our setup, or Claude's own cold start.
			brainInfoLog.Printf("claude_max: first output after %s (setup %s + claude %s)",
				(p.setupTook + time.Since(p.started)).Round(time.Millisecond),
				p.setupTook.Round(time.Millisecond),
				time.Since(p.started).Round(time.Millisecond))
		}
		p.emit(read.fresh)

		if res, done := claudeTerminalResult(read.last); done {
			return p.finish(res)
		}
		if strings.Contains(read.head, "DONE:") {
			// The process is gone. Before calling that a failure, read the
			// last line again without the poll's byte cap.
			//
			// The routine poll clamps the tail to claudeLastLineBytes so a
			// long reply cannot dominate a 300 ms read. But the terminal
			// `result` line carries the whole answer plus its usage blob, and
			// a reply longer than that cap comes back truncated, fails to
			// parse as JSON, and reads as "no result" - so a turn that
			// SUCCEEDED was reported to the boss as "Claude stopped before
			// answering". Paying for one uncapped read at the very end costs
			// a single round trip and removes that whole class of false
			// failure. (Same law as the honesty guards in CLAUDE.md, pointed
			// the other way: a success must never be reported as a failure
			// either.)
			if res, done := p.finalResult(ctx); done {
				return p.finish(res)
			}
			// Genuinely nothing to parse: a crash, a killed session, or an
			// auth failure. Read what it managed to write and report it.
			return llm.Response{}, p.failure(ctx)
		}
		if strings.Contains(read.head, brainNoStreamMarker) && time.Since(p.started) > brainStartGrace {
			// The launch said LAUNCHED and then nothing was ever written:
			// the process never came up. Say so now, not at the ceiling.
			return llm.Response{}, fmt.Errorf("Claude never started on %s: %s after the launch there is still no stream from it, so it died before it could say anything. Nothing was sent.",
				brainBoxName(p.b), brainStartGrace)
		}
		if time.Now().After(deadline) {
			return llm.Response{}, fmt.Errorf("Claude has been working on this for %s and hasn't finished. It's still going on the Mac; ask me again and I'll pick up the same thread.", time.Since(p.started).Round(time.Second))
		}
	}
}

type brainRead struct {
	head  string
	last  string
	fresh string
	// init is the head of the stream, read only until the session id is
	// known (see brainInitHeadBytes). Empty once it is.
	init string
}

// pollOnce reads the completion signals and everything new since the last
// read, draining up to brainMaxDrains times so a burst lands in one go.
func (p *brainPoll) pollOnce(ctx context.Context) (brainRead, bool) {
	var acc brainRead
	for drain := 0; drain < brainMaxDrains; drain++ {
		r, full, ok := p.readSlice(ctx)
		if !ok {
			if drain == 0 {
				return brainRead{}, false
			}
			break // one blink mid-drain: keep what we already read
		}
		acc.head, acc.last = r.head, r.last
		if acc.init == "" {
			acc.init = r.init
		}
		if r.fresh != "" {
			if acc.fresh == "" {
				acc.fresh = r.fresh
			} else {
				acc.fresh += "\n" + r.fresh
			}
		}
		if !full {
			break // the stream is caught up
		}
	}
	return acc, true
}

// readSlice performs ONE read. full reports that the slice filled its budget,
// which is what tells consume a trailing half-line can never complete.
func (p *brainPoll) readSlice(ctx context.Context) (r brainRead, full bool, ok bool) {
	// Same three-part shape as the coding path's poll, and for the same
	// reason: the bridge truncates a long reply from the tail, so the signals
	// go first. `cut -c 1-N` clamps each line BEFORE the byte cap - see
	// brainLineMaxCols for the turn-long silence its absence caused.
	//
	// The HEAD read sits right behind the one-line status, ahead of the
	// tail and the slice, and only until the session id is known. It is
	// what finds the `system/init` line whole: the slice below clamps every
	// line to brainLineMaxCols, and the init line is three times that (see
	// brainInitHeadBytes). Without it every turn started cold.
	headRead := ""
	if !p.sessionSeen {
		headRead = fmt.Sprintf(`echo "%s"
head -c %d %s 2>/dev/null
echo ""
`, brainHeadMarker, brainInitHeadBytes, p.files.out)
	}
	script := fmt.Sprintf(`if [ -f %s ]; then echo "DONE:$(cat %s)"; elif [ -f %s ]; then echo RUNNING; else echo NOSTREAM; fi
%secho "===LAST==="
tail -n 1 %s 2>/dev/null | head -c %d
echo ""
echo "===NEW==="
tail -n +%d %s 2>/dev/null | head -n %d | cut -c 1-%d | head -c %d`,
		p.files.status, p.files.status, p.files.out,
		headRead,
		p.files.out, claudeLastLineBytes,
		p.line, p.files.out, brainLinesPerPoll, brainLineMaxCols, brainChunkBytes)

	body, code, okPost := p.b.Post(ctx, "/bash", map[string]any{
		"cmd": script, "cwd": p.workspace, "timeout_sec": 15,
	})
	if !okPost || code >= 300 {
		return brainRead{}, false, false
	}
	raw, _ := bridgeBashOutput(body)
	head, rest := splitMarker(raw, "", "===LAST===")
	// Peel the head read off the status so "DONE:" is only ever matched
	// against the status line, never against Claude's own output.
	head, initHead := splitBrainHead(head)
	last, region := splitMarker(rest, "", "===NEW===")
	region = strings.TrimPrefix(region, "\n")
	full = len(region) >= brainChunkBytes
	return brainRead{head: head, last: last, fresh: p.consume(region, full), init: initHead}, full, true
}

// brainHeadMarker separates the status line from the head read in one poll.
const brainHeadMarker = "===HEAD==="

// brainNoStreamMarker is the poll's status when neither the status file nor
// the stream file exists: the launch never produced a process.
const brainNoStreamMarker = "NOSTREAM"

// splitBrainHead separates the status line from the head read that follows
// it. A poll without a head read comes back unchanged.
func splitBrainHead(s string) (status, initHead string) {
	i := strings.Index(s, brainHeadMarker)
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+len(brainHeadMarker):]
}

// consume advances the read position past the COMPLETE lines in a slice and
// returns just those lines.
//
// Two things leave a trailing half-line, and they need opposite handling.
// Claude being mid-write when the poll landed: the half completes in a moment,
// so leave the position alone and re-read it whole. The slice filling its
// budget: that half can NEVER complete inside the window, so stepping over it
// is the only way the read ever moves again. Conflating them is what wedged
// this stream for whole turns at a time.
//
// Newlines are counted, not split pieces: a slice ending in "\n" splits into
// one more piece than it has lines, which walked the position a line too far
// and dropped an event on every poll. The coding path learned this first
// (claudePoll.consume); this is the same accounting, written the same way.
func (p *brainPoll) consume(slice string, full bool) string {
	if slice == "" {
		return ""
	}
	cut := strings.LastIndexByte(slice, '\n')
	if cut < 0 {
		if !full {
			return "" // still being written; re-read it whole next poll
		}
		// One line, longer than the whole window. It cannot arrive complete,
		// so step over it rather than reading it forever.
		p.line++
		return ""
	}
	whole := slice[:cut+1]
	p.line += strings.Count(whole, "\n")
	return strings.TrimSuffix(whole, "\n")
}

// noteSession reports Claude Code's own session id the first time it appears,
// so the next turn in this conversation resumes it.
func (p *brainPoll) noteSession(stream string) {
	if p.sessionSeen || stream == "" {
		return
	}
	// ONLY the init line. Every event in the stream carries a session_id, and
	// a sub-agent's events carry ITS session - an id that is not resumable
	// and, once stored, breaks the boss's next message with "No conversation
	// found". The conversation's own id is the one Claude Code announces when
	// the session opens.
	id := parseClaudeInitSessionID(stream)
	if id == "" {
		return
	}
	p.sessionSeen = true
	if p.turn.OnSession != nil {
		p.turn.OnSession(id)
	}
}

// parseClaudeInitSessionID returns the session id off the
// `{"type":"system","subtype":"init","session_id":…}` line and nothing else.
//
// It is tolerant of a CUT line on purpose. The init line is tens of
// thousands of chars (it names every tool, command, skill and agent on the
// box) and reaches this parser clamped by the poll's per-line cap, or cut
// mid-byte by the head read. json.Unmarshal refuses both, which is exactly
// how the id went unseen for 54 turns in a row. So the line is recognised by
// its `"type":"system"` and `"subtype":"init"` markers and the id is lifted
// with a regexp - Claude Code writes `session_id` right after `cwd`, well
// inside the first 200 bytes, so a cut anywhere past that still yields it.
// Whole JSON is still tried first, because it is the stronger check.
//
// Only the init line. Every event carries a session_id, and a sub-agent's
// events carry ITS id, which is not resumable.
func parseClaudeInitSessionID(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		if !strings.Contains(line, `"subtype":"init"`) || !strings.Contains(line, `"type":"system"`) {
			continue
		}
		var ev struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil {
			if ev.Type == "system" && ev.Subtype == "init" && isClaudeSessionID(ev.SessionID) {
				return ev.SessionID
			}
			continue
		}
		if m := claudeInitSessionIDRe.FindStringSubmatch(line); m != nil && isClaudeSessionID(m[1]) {
			return m[1]
		}
	}
	return ""
}

// claudeInitSessionIDRe lifts the id off an init line that no longer parses
// as JSON. The class is the uuid alphabet Claude Code uses; isClaudeSessionID
// then checks the shape.
var claudeInitSessionIDRe = regexp.MustCompile(`"session_id":"([0-9a-fA-F-]{36})"`)

// brainEvent is the slice of stream-json this path reads. The shapes are
// taken from a real run, not guessed: `stream_event` carries the raw
// Anthropic deltas (this is what --include-partial-messages turns on) and
// `assistant` carries the assembled message afterwards.
type brainEvent struct {
	Type string `json:"type"`
	// Subtype + EstimatedTokens carry `system/thinking_tokens`, which is how
	// this brain reports reasoning progress now that the reasoning text is
	// redacted.
	Subtype         string `json:"subtype"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Event           struct {
		Type  string `json:"type"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
			// PartialJSON is the tool's arguments arriving as the model
			// writes them, before the call is complete.
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
	} `json:"event"`
}

// emit turns new stream lines into stream events.
//
// It reads the DELTAS, not the assembled messages, which is what makes this
// brain type like every other one. The assembled `assistant` event repeats
// everything the deltas already carried, so consuming it too would print the
// whole reply a second time.
//
// Tool calls and their RESULTS come off the assembled message lines, through
// parseNestedEvents - the SAME parser the coding path has used since the
// "TOTALLY NOT TRANSPARENT" build, rather than a second half-version here.
// This path originally read only content_block_start, which carries the call
// and never the result, so a conversation on this brain showed what it decided
// to do and never what came back, and memory recorded the same half-story.
// That is the whole of "why doesn”'t it work like my other models": on every
// other brain our own loop runs the tool, so the result is ours by
// construction.
func (p *brainPoll) emit(fresh string) {
	if fresh == "" || p.out == nil {
		return
	}
	for _, line := range strings.Split(fresh, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var ev brainEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		// EVIDENCE THAT IT IS ALIVE. Claude Code redacts the reasoning itself
		// (every thinking_delta arrives with an empty string) and reports
		// progress as a running token count instead. Without this the boss
		// watches a row that says "Thinking" over an empty box for two
		// minutes, which is indistinguishable from a hang - and he read it as
		// one, repeatedly. The count is real and it moves.
		if ev.Type == "system" && ev.Subtype == "thinking_tokens" && ev.EstimatedTokens > 0 {
			p.send(llm.StreamEvent{Kind: llm.StreamThinking, ThinkingTokens: ev.EstimatedTokens})
			continue
		}
		if ev.Type != "stream_event" {
			continue
		}
		switch ev.Event.Type {
		case "content_block_start":
			// THE TOOL, THE MOMENT IT IS DECIDED. Claude Code announces a
			// tool_use block with its name before it writes a single argument,
			// and this path used to ignore that and wait for the ASSEMBLED
			// message instead - which only lands once the whole block is
			// finished. So the boss watched a bare "Thinking" for minutes
			// while Claude was telling us, second by second, that it was
			// running `git log` and reading his migrations. His words:
			// "so i just sit there watch it reasoning with no context??
			// unlike my other models". On every other brain the row appears
			// as the model writes the call, because our own loop streams it.
			// Now this one does too.
			cb := ev.Event.ContentBlock
			if cb.Type != "tool_use" || cb.ID == "" || cb.Name == "" {
				continue
			}
			name := nestedToolName(cb.Name)
			p.noteTool(cb.ID, name)
			p.markSent(cb.ID)
			p.send(llm.StreamEvent{Kind: llm.StreamToolCall, ToolCall: &llm.ToolCall{
				ID: cb.ID, Name: name, Input: map[string]any{},
			}})
			p.openBlock = cb.ID
		case "content_block_stop":
			p.openBlock = ""
		case "content_block_delta":
			switch ev.Event.Delta.Type {
			case "text_delta":
				if ev.Event.Delta.Text != "" {
					p.streamed += ev.Event.Delta.Text
					p.send(llm.StreamEvent{Kind: llm.StreamText, TextDelta: ev.Event.Delta.Text})
				}
			case "thinking_delta":
				if ev.Event.Delta.Thinking != "" {
					p.send(llm.StreamEvent{Kind: llm.StreamThinking, ThinkingDelta: ev.Event.Delta.Thinking})
				}
			case "input_json_delta":
				// The arguments as they are typed, so the row fills in with
				// the actual command / file instead of sitting there named but
				// blank. Same event every other brain already emits.
				if ev.Event.Delta.PartialJSON != "" && p.openBlock != "" {
					p.send(llm.StreamEvent{
						Kind:       llm.StreamToolInputDelta,
						ToolCallID: p.openBlock,
						ToolName:   p.toolFor(p.openBlock),
						InputDelta: ev.Event.Delta.PartialJSON,
					})
				}
			}
		}
	}

	// The brain's checklist, onto the dock above the composer. A coding job's
	// TodoWrite already lands there (syncNestedPlan); the chat brain's did
	// not, so a conversation that laid out five steps showed the boss
	// nothing to track them by.
	p.syncOwnPlan(fresh)

	// Both halves of every tool the brain ran itself, in order.
	for _, n := range parseNestedEvents(fresh) {
		if !n.result && p.alreadySent(n.callID) {
			// Already on his screen from content_block_start, with its
			// arguments streamed in. The assembled copy carries the same id,
			// so re-sending it would be a duplicate row.
			continue
		}
		if n.result {
			p.send(llm.StreamEvent{
				Kind:       llm.StreamToolResult,
				ToolCallID: n.callID,
				ToolName:   p.toolFor(n.callID),
				ToolOutput: n.output,
				ToolError:  n.isError,
			})
			continue
		}
		p.noteTool(n.callID, n.tool)
		p.send(llm.StreamEvent{Kind: llm.StreamToolCall, ToolCall: &llm.ToolCall{
			ID:    n.callID,
			Name:  n.tool,
			Input: n.input,
		}})
	}
}

// syncOwnPlan mirrors the newest TodoWrite in this slice of the brain's
// stream onto the conversation's own plan.
//
// The brain IS the conversation, so this goes through the plan the
// conversation owns (SyncOwn → plan.Store.SyncChecklist, the seam todo_write
// uses), not the nested variant a coding job uses - a brain turn has no run
// row to own a plan with. Settlement needs nothing here: the loop settles the
// conversation's plan on every turn exit, brain or not.
func (p *brainPoll) syncOwnPlan(slice string) {
	if p == nil || p.plans == nil || strings.TrimSpace(slice) == "" {
		return
	}
	// A real conversation, or nothing: an ephemeral sub-agent session has no
	// dock to draw into (same guard as syncNestedPlan).
	if !isClaudeSessionID(p.parentSession) || isSubAgentSession(p.parentSession) {
		return
	}
	items, ok := newestNestedChecklist(slice)
	if !ok {
		return
	}
	fp := checklistFingerprint(items)
	if fp == p.planPrint {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.plans.SyncOwn(ctx, p.parentSession, brainPlanTitle(p.turn.Prompt), items); err != nil {
		// Never fatal: a reply must not fail because its checklist could not
		// be mirrored. Logged, because a dock that quietly stopped tracking
		// is the false-green shape we don't ship.
		log.Printf("claude_max: mirror the brain's checklist: %v", err)
		return
	}
	p.planPrint = fp
}

// brainPlanTitle names the mirrored plan after what the boss asked for: the
// last line of the prompt that reads like his message, clipped.
func brainPlanTitle(prompt string) string {
	lines := strings.Split(strings.TrimSpace(prompt), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || strings.HasPrefix(l, "<") || strings.HasPrefix(l, "#") {
			continue
		}
		if r := []rune(l); len(r) > 60 {
			return string(r[:60]) + "…"
		}
		return l
	}
	return "Claude's plan"
}

// markSent / alreadySent remember which calls went out from the LIVE path
// (content_block_start), so the assembled message that repeats them later
// cannot post a second row for the same work.
func (p *brainPoll) markSent(callID string) {
	if p.sentCalls == nil {
		p.sentCalls = map[string]bool{}
	}
	p.sentCalls[callID] = true
}

func (p *brainPoll) alreadySent(callID string) bool { return p.sentCalls[callID] }

// noteTool / toolFor remember which tool a call id belongs to, because the
// result half of the pair carries only the id. Bounded by the turn.
func (p *brainPoll) noteTool(callID, tool string) {
	if callID == "" || tool == "" {
		return
	}
	if p.toolNames == nil {
		p.toolNames = map[string]string{}
	}
	p.toolNames[callID] = tool
}

func (p *brainPoll) toolFor(callID string) string {
	return p.toolNames[callID]
}

// brainSendWait caps how long one event waits on a full channel. The consumer
// is the agent loop, which drains until the stream closes, so this only ever
// absorbs a burst - it is not a deadline for the turn.
const brainSendWait = 5 * time.Second

// send hands one event to the agent loop, waiting for room rather than
// throwing the event away.
//
// The old version dropped on a full buffer, and this brain is the one that
// fills a buffer. It does not stream at writing pace: it reads the transcript
// file on a 300 ms poll and emits everything new in one go, so a single poll
// can push a hundred events at a consumer holding sixty-four slots. Everything
// past the sixty-fourth vanished - including the StreamComplete that finish()
// sends, which is the frame that tells the browser the turn is over.
//
// That is why the boss had to refresh to see any of it, and why only THIS
// brain behaved that way. See emit() in internal/agent/loop.go for the same
// fault one layer down and the same fix.
func (p *brainPoll) send(ev llm.StreamEvent) {
	if p.out == nil {
		return
	}
	select {
	case p.out <- ev:
		return
	default:
	}
	t := time.NewTimer(brainSendWait)
	defer t.Stop()
	select {
	case p.out <- ev:
	case <-t.C:
		brainInfoLog.Printf("claude_max: consumer stalled %s, dropped a %s event", brainSendWait, ev.Kind)
	}
}

// finish renders the terminal result into a Response.
func (p *brainPoll) finish(res claudeResult) (llm.Response, error) {
	if res.IsError {
		detail := strings.TrimSpace(res.Result)
		if detail == "" {
			detail = res.Subtype
		}
		return llm.Response{}, fmt.Errorf("Claude stopped without finishing: %s", detail)
	}
	text := strings.TrimSpace(res.Result)
	// The deltas already printed the answer. Only fall back to sending the
	// terminal result when nothing streamed at all - an older Claude Code
	// that ignores --include-partial-messages, or a turn whose output was
	// swallowed. Better a late reply than a blank one.
	if text != "" && strings.TrimSpace(p.streamed) == "" {
		p.send(llm.StreamEvent{Kind: llm.StreamText, TextDelta: text})
	}
	usage := brainUsage(res)
	p.send(llm.StreamEvent{Kind: llm.StreamComplete, StopReason: "end_turn", Usage: &usage})
	// Guard the zero start time so the log never prints a 292-year duration.
	took := time.Duration(0)
	if !p.started.IsZero() {
		took = time.Since(p.started).Round(time.Second)
	}
	// cold says whether this turn resumed a Claude session or opened a new
	// one. Every cold turn re-writes the whole prefix (cache_write shows the
	// bill), so a run of cold=true across one conversation is the signal
	// that resume capture has broken again - it must be visible, not
	// inferred from token counts days later.
	brainInfoLog.Printf("claude_max: turn finished in %s (cold=%t cache_read=%d cache_write=%d input=%d output=%d)",
		took, p.turn.Resume == "", usage.CacheRead, usage.CacheWrite, usage.Input, usage.Output)
	return llm.Response{Text: text, Usage: usage, StopReason: "end_turn"}, nil
}

// finalResult re-reads the stream's last line with no byte cap, for the one
// moment it matters: the process has exited and the routine poll's clamped
// tail did not parse. Called once per turn, never on the hot path.
func (p *brainPoll) finalResult(ctx context.Context) (claudeResult, bool) {
	body, code, ok := p.b.Post(ctx, "/bash", map[string]any{
		"cmd": "tail -n 1 " + p.files.out + " 2>/dev/null", "cwd": p.workspace, "timeout_sec": 15,
	})
	if !ok || code >= 300 {
		return claudeResult{}, false
	}
	out, _ := bridgeBashOutput(body)
	return claudeTerminalResult(strings.TrimSpace(out))
}

// failure reads whatever the run left behind when it exited without a result.
func (p *brainPoll) failure(ctx context.Context) error {
	body, _, ok := p.b.Post(ctx, "/bash", map[string]any{
		"cmd": "tail -c 2000 " + p.files.err, "cwd": p.workspace, "timeout_sec": 10,
	})
	detail := ""
	if ok {
		detail, _ = bridgeBashOutput(body)
	}
	detail = strings.TrimSpace(detail)
	if human, isAuth := claudeAuthFailure(detail, p.b.Name() == bridge.KindCloud); isAuth {
		return errors.New(human)
	}
	if detail == "" {
		detail = "it exited without saying why"
	}
	return fmt.Errorf("Claude stopped before answering: %s", detail)
}

// cleanup removes the turn's files, the MCP config with its live token first.
func (p *brainPoll) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.b.Post(ctx, "/bash", map[string]any{
		"cmd": fmt.Sprintf("rm -f %s %s %s %s %s", p.files.mcp, p.files.settings, p.files.out, p.files.err, p.files.status),
		"cwd": p.workspace, "timeout_sec": 10,
	})
}

// brainUsage pulls the turn's token counts off the terminal result.
//
// This is where the subscription's prompt cache becomes visible: a resumed
// session reports most of its prompt under cache_read, which is the whole
// reason one Claude session is pinned per conversation.
func brainUsage(res claudeResult) llm.TokenUsage {
	var u llm.TokenUsage
	if len(res.RawUsage) == 0 {
		return u
	}
	type iteration struct {
		Input      int `json:"input_tokens"`
		CacheRead  int `json:"cache_read_input_tokens"`
		CacheWrite int `json:"cache_creation_input_tokens"`
	}
	var raw struct {
		Input      int         `json:"input_tokens"`
		Output     int         `json:"output_tokens"`
		CacheRead  int         `json:"cache_read_input_tokens"`
		CacheWrite int         `json:"cache_creation_input_tokens"`
		Iterations []iteration `json:"iterations"`
	}
	if json.Unmarshal(res.RawUsage, &raw) != nil {
		return u
	}
	u = llm.TokenUsage{
		Input:      raw.Input,
		Output:     raw.Output,
		CacheRead:  raw.CacheRead,
		CacheWrite: raw.CacheWrite,
	}
	// The top-level counts are the SUM over every API call this turn made, and
	// this brain makes many: it runs its own tool loop, so one of our turns is
	// a dozen round trips, each re-reading the cached prefix. Billing wants
	// that sum. The context meter does not - it wants the DEEPEST single
	// prompt, which is the widest the window ever got.
	//
	// A real turn of the boss's reported 2,172,488 cache-read tokens over 13
	// calls while the largest single prompt was 172,498. Fed to the meter as
	// one number it read 217% of a 1M window, went red, and stayed red - and
	// auto-compaction kept firing on a session a fifth full.
	for _, it := range raw.Iterations {
		if n := it.Input + it.CacheRead + it.CacheWrite; n > u.ContextTokens {
			u.ContextTokens = n
		}
	}
	return u
}

// --- connection status -------------------------------------------------------

// BrainStatus is what the Settings card renders. Every field is written to be
// read by the boss, not by a developer: no enum names, no shell, no paths.
type BrainStatus struct {
	// Connected is the only thing the card branches on.
	Connected bool `json:"connected"`
	// Account is the signed-in email, shown so he can see WHICH Claude
	// account is about to be spent.
	Account string `json:"account,omitempty"`
	// Plan reads "Max", "Pro", and so on.
	Plan string `json:"plan,omitempty"`
	// Where is "Mac" or "cloud": which machine is carrying the sign-in. The
	// boss needs it because one of those answers depends on his laptop being
	// awake and the other does not.
	Where string `json:"where,omitempty"`
	// Detail is one plain sentence: what is true, or what to do about it.
	Detail string `json:"detail"`
	// MacReady / CloudReady say which machines can carry a turn. The card
	// needs both separately: "working" and "working with the laptop shut"
	// are different promises, and only the second one survives him closing
	// the lid.
	MacReady   bool `json:"mac_ready"`
	CloudReady bool `json:"cloud_ready"`
}

// BrainStatus reports whether the Claude Max Plan brain can answer, and from
// where.
//
// It runs the SAME proof the launcher runs, so the card cannot go green over
// a brain that would then refuse - the false-green this codebase keeps having
// to fix. Two places can hold the credential, and the card says which one is
// carrying it, because "it works" and "it works only while your laptop is
// awake" are different facts.
func (r *ClaudeCodeRunner) BrainStatus(ctx context.Context) BrainStatus {
	if !r.brainReady() {
		return BrainStatus{
			Detail: "Claude can sign in, but Infinity can't hand it my tools yet. I need my own public address set before this brain is worth using.",
		}
	}
	mac := r.macBrainStatus(ctx)
	cloud := r.cloudBrainStatus(ctx)

	mac.MacReady, cloud.MacReady = mac.Connected, mac.Connected
	mac.CloudReady, cloud.CloudReady = cloud.Connected, cloud.Connected

	switch {
	case mac.Connected && cloud.Connected:
		mac.Detail = "Ready on your Mac and on the cloud box, so this keeps working when your laptop is shut."
		return mac
	case mac.Connected:
		mac.Detail = "Ready, running on your Mac. Add your Claude token below and it keeps working when the Mac is asleep."
		return mac
	case cloud.Connected:
		return cloud
	default:
		// Neither. Lead with whichever failure the boss can actually act on.
		if strings.TrimSpace(cloud.Detail) != "" {
			return cloud
		}
		return mac
	}
}

// macBrainStatus reads the Mac's own Claude sign-in.
func (r *ClaudeCodeRunner) macBrainStatus(ctx context.Context) BrainStatus {
	b := r.bridgeNamed(ctx, bridge.KindMac)
	if b == nil {
		return BrainStatus{Detail: "Your Mac isn't reachable right now."}
	}
	auth, err := r.probeAuth(ctx, b, bridgeHome)
	if err != nil {
		return BrainStatus{Detail: "I couldn't read the Claude sign-in on your Mac just now. " + brainProbeDetail(err)}
	}
	if !auth.Subscription() {
		return BrainStatus{
			Detail: "Claude Code on your Mac isn't signed in to your subscription (I found: " + auth.Label() + "). Run `claude` on the Mac once and sign in with the account your Max plan is on.",
		}
	}
	return BrainStatus{Connected: true, Where: "Mac", Account: auth.Email, Plan: auth.planName()}
}

// cloudBrainStatus reports whether the cloud box has a subscription token.
func (r *ClaudeCodeRunner) cloudBrainStatus(ctx context.Context) BrainStatus {
	if r.brain.SubscriptionToken == nil || strings.TrimSpace(r.brain.SubscriptionToken(ctx)) == "" {
		return BrainStatus{
			Detail: "The cloud box can't think on your plan yet. Run `claude setup-token` on your Mac and paste the token below, then this works with the laptop shut.",
		}
	}
	return BrainStatus{
		Connected: true,
		Where:     "cloud",
		Detail:    "Ready on the cloud box, so this works whether or not your Mac is awake.",
	}
}

// bridgeNamed returns the named bridge when it is actually reachable.
func (r *ClaudeCodeRunner) bridgeNamed(ctx context.Context, kind bridge.Kind) bridge.Bridge {
	b, _, err := r.ActiveBridge(ctx)
	if err != nil || b == nil || b.Name() != kind {
		return nil
	}
	return b
}

// brainProbeDetail restates a coding-path error in the voice of a chat turn.
func brainProbeDetail(err error) string {
	return strings.TrimPrefix(errDetail(err), "code_agent: ")
}

func errDetail(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

// claudeAuthFailure recognises a sign-in failure and says what it means.
//
// The raw text here is Claude Code's own ("Invalid API key · Please run
// /login", "OAuth token has expired"), which tells the boss nothing he can
// act on and reads like a bug in Infinity rather than a credential that ran
// out. An expired token is the single most likely way this brain ever stops
// working, and it will land a year from now when nobody remembers setting it
// up, so it gets a sentence that names the fix.
func claudeAuthFailure(text string, cloud bool) (string, bool) {
	low := strings.ToLower(text)
	authish := strings.Contains(low, "please run /login") ||
		strings.Contains(low, "invalid api key") ||
		strings.Contains(low, "authentication_error") ||
		strings.Contains(low, "oauth token has expired") ||
		strings.Contains(low, "token has expired") ||
		strings.Contains(low, "login expired") ||
		strings.Contains(low, "unauthorized")
	if !authish {
		return "", false
	}
	if cloud {
		return "The Claude token the cloud machine signs in with is no longer being accepted, most likely expired: " +
			"they last a year. Run `claude setup-token` on your Mac and paste the new one into Settings. " +
			"Until then this only works while the Mac is awake.", true
	}
	return "Claude Code on your Mac is signed out, so it can't run on your subscription. " +
		"Open a terminal there and run `claude`, then sign in with the account your Max plan is on.", true
}

// ── putting the boss's files where the brain can open them ───────────────

// PlaceFile writes one attachment onto the box this brain is running on and
// returns the path it landed at. It implements llm.BrainFilePlacer.
//
// WHY IT HAS TO EXIST. Every other brain receives an image as a native image
// block. Claude Code takes a PROMPT and physically cannot, so an image was
// named to it and never seen - the boss attached a screenshot and got an
// answer written about a file nobody had opened. What Claude Code CAN do is
// open a file: its Read tool renders an image exactly as a vision model sees
// one. So the bytes go to the box, and the prompt says where.
//
// Over /bash rather than /fs/save, deliberately: /fs/save takes JSON string
// content, and a PNG is not valid UTF-8. base64 through the shell is the one
// shape that carries arbitrary bytes and works UNCHANGED on the Mac and the
// cloud box, which is the requirement - "every model I use on mac or cloud
// bridge", not whichever one we wired first. It also needs no new bridge
// endpoint, so it works against the bridge already installed on his Mac.
func (r *ClaudeCodeRunner) PlaceFile(ctx context.Context, id, name string, data []byte) (string, error) {
	if r == nil || len(data) == 0 {
		return "", errors.New("nothing to place")
	}
	if len(data) > maxPlacedFileBytes {
		return "", fmt.Errorf("the file is %s, over the %s a prompt can carry to your machine",
			humanBytes(int64(len(data))), humanBytes(maxPlacedFileBytes))
	}
	b, _, err := r.ActiveBridge(ctx)
	if err != nil {
		return "", err
	}
	if b == nil {
		return "", errors.New("neither your Mac nor the cloud box is reachable")
	}
	path := placedFilePath(id, name)

	// Already there from an earlier turn in this conversation? Then the write
	// is skipped and only the check crosses the wire. A 5MB base64 payload
	// re-sent on every turn of a long chat is the kind of waste that turns
	// into a slow chat nobody can explain.
	script := fmt.Sprintf(
		"mkdir -p %s"+"\n"+
			"if [ -s %s ]; then echo PLACED; else printf '%%s' %s | base64 -d > %s && echo PLACED; fi",
		shellQuote(placedFileDir), shellQuote(path),
		shellQuote(base64.StdEncoding.EncodeToString(data)), shellQuote(path),
	)
	body, code, ok := b.Post(ctx, "/bash", map[string]any{
		"cmd": script, "cwd": bridgeHome, "timeout_sec": 60,
	})
	if !ok || code >= 300 {
		return "", fmt.Errorf("%s could not take the file (status %d): %s", b.Name(), code, bridgeErrorDetail(body))
	}
	if !strings.Contains(string(body), "PLACED") {
		return "", fmt.Errorf("%s did not confirm the file was written", b.Name())
	}
	return path, nil
}

const (
	// placedFileDir is where the boss's attachments land on either box. Under
	// /tmp on purpose: these are conversation inputs, not artifacts, and the
	// durable copy is already in mem_attachments.
	placedFileDir = "/tmp/inf-attach"
	// maxPlacedFileBytes bounds one file. Base64 inflates by 4/3, and the
	// bridge caps a request body at 32MB, so this leaves real headroom.
	maxPlacedFileBytes = 20 << 20
)

// placedFilePath is the stable path one attachment always lands at, so the
// same file is written once per box and a resumed session's earlier reference
// still resolves.
func placedFilePath(id, name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, base)
	if base == "" || base == "." || base == ".." {
		base = "file"
	}
	if len(base) > 80 {
		base = base[len(base)-80:]
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = "att"
	}
	if len(id) > 12 {
		id = id[:12]
	}
	return placedFileDir + "/" + id + "-" + base
}

// humanBytes renders a size the way a person says it.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
