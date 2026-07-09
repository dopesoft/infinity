package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
)

// errCloudUnavailable is a sentinel so LoadAll can SKIP a cli extension
// (leave its row untouched) when the cloud workspace is asleep at boot,
// rather than flapping it to status=error. Register surfaces it so the
// agent learns the workspace is down right now.
var errCloudUnavailable = errors.New("cloud workspace unavailable")

// persistDir is the one persistent location in the cloud workspace (the
// Railway volume mounts at /workspace; $HOME and /usr/local/bin are
// ephemeral). cli installs + credentials are pinned here so they survive
// redeploys - the whole point of "cloud-resident, Mac-independent tools".
const persistDir = "/workspace/.jarvis"

// EnvFilePath is the sourced shell file that pins HOME/PATH/XDG onto the
// persistent volume. Both the Manager (install/check) and the agent (when
// it runs the tool via bash_run) source it so they share one install +
// credential store. Exported so the CLIProvider can tell the agent to
// source it.
const EnvFilePath = persistDir + "/env.sh"

// envFileBody is written once per workspace. Idempotent: re-running is safe.
// HOME points at the volume so device-OAuth credentials (~/.config/<tool>)
// and ~/.local/bin installs persist across container restarts.
const envFileBody = `export HOME=` + persistDir + `
export XDG_CONFIG_HOME="$HOME/.config"
export XDG_DATA_HOME="$HOME/.local/share"
export XDG_CACHE_HOME="$HOME/.cache"
export PATH="$HOME/.local/bin:$HOME/bin:$PATH"
# Python (pip) installs. The cloud workspace's system Python is "externally
# managed" (PEP 668), so a bare or even ` + "`--user`" + ` pip install is REFUSED
# with externally-managed-environment — which is exactly why the yt-dlp
# extension couldn't activate. This workspace is a disposable container, NOT the
# boss's machine, so overriding PEP 668 here is safe: pin pip to a user install
# on the persistent volume ($HOME/.local, already on PATH) and allow it past the
# guard. Net effect: any ` + "`pip install <tool>`" + ` extension just works and
# survives redeploys, with zero per-tool flags for the agent to remember.
export PIP_BREAK_SYSTEM_PACKAGES=1
export PIP_USER=1
export PIP_DISABLE_PIP_VERSION_CHECK=1
mkdir -p "$HOME/.local/bin" "$HOME/bin" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME" 2>/dev/null || true
# Site cookies for scraper CLIs (yt-dlp etc). Sites bot-wall datacenter IPs;
# a one-time cookie export from the boss's logged-in browser, saved here in
# Netscape format as <site>.txt, is the standing workaround. Persistent: on
# the volume, survives redeploys, one export lasts months.
mkdir -p "$HOME/cookies" 2>/dev/null || true
`

// urlRe pulls the first http(s) URL out of an auth command's stdout - the
// device-login link the boss opens.
var urlRe = regexp.MustCompile(`https?://[^\s'"<>)]+`)

// cloudBridge resolves the cloud workspace bridge. cli tools are always
// cloud-resident (Mac-independent is the requirement), so we pin PrefCloud.
func (m *Manager) cloudBridge(ctx context.Context) (bridge.Bridge, error) {
	if m == nil || m.router == nil {
		return nil, fmt.Errorf("%w: no bridge router configured", errCloudUnavailable)
	}
	b, why, err := m.router.For(ctx, bridge.PrefCloud)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errCloudUnavailable, why)
	}
	return b, nil
}

// bashResult is the workspace /bash response shape.
type bashResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

// cliBash runs a command on the cloud workspace with the persistent env
// sourced first, so installs + credentials land on the volume. Returns
// (output, exitCode, reached) - reached=false means the workspace HTTP call
// itself failed (not a non-zero exit).
func (m *Manager) cliBash(ctx context.Context, b bridge.Bridge, cmd, cwd string, timeoutSec int) (string, int, bool) {
	wrapped := "source " + EnvFilePath + " 2>/dev/null; " + cmd
	body, status, ok := b.Post(ctx, "/bash", map[string]any{
		"cmd":         wrapped,
		"cwd":         cwd,
		"timeout_sec": timeoutSec,
	})
	if !ok || status >= 300 {
		return string(body), -1, false
	}
	var res bashResult
	if err := json.Unmarshal(body, &res); err != nil {
		return string(body), -1, false
	}
	return res.Output, res.ExitCode, true
}

// ensureEnvFile writes the persistent env.sh once. Cheap; idempotent.
func (m *Manager) ensureEnvFile(ctx context.Context, b bridge.Bridge) {
	write := fmt.Sprintf("mkdir -p %s && cat > %s <<'INFINITY_ENV_EOF'\n%sINFINITY_ENV_EOF",
		persistDir, EnvFilePath, envFileBody)
	_, _, _ = b.Post(ctx, "/bash", map[string]any{"cmd": write, "timeout_sec": 10})
}

// binaryPresent reports whether `command -v <binary>` succeeds.
func (m *Manager) binaryPresent(ctx context.Context, b bridge.Bridge, binary string) bool {
	_, code, ok := m.cliBash(ctx, b, "command -v "+shellQuoteCLI(binary)+" >/dev/null 2>&1", "", 15)
	return ok && code == 0
}

// cliReady reports whether the tool is installed AND authed: CheckCmd exit
// 0, or (no CheckCmd) the binary is on PATH.
func (m *Manager) cliReady(ctx context.Context, b bridge.Bridge, cfg CLIConfig) bool {
	if strings.TrimSpace(cfg.CheckCmd) == "" {
		return m.binaryPresent(ctx, b, cfg.Binary)
	}
	_, code, ok := m.cliBash(ctx, b, cfg.CheckCmd, cfg.Cwd, 30)
	return ok && code == 0
}

// activateCLI installs (if needed) and probes a cli extension, setting
// ext.Status to active or pending_auth. doInstall=false skips the install
// step (used on boot, where the persistent volume should already have it).
// Returns errCloudUnavailable (wrapped) when the workspace is unreachable.
func (m *Manager) activateCLI(ctx context.Context, ext *Extension, doInstall bool) error {
	cfg, err := parseCLIConfig(ext.Config)
	if err != nil {
		return err
	}
	b, err := m.cloudBridge(ctx)
	if err != nil {
		return err // errCloudUnavailable
	}
	m.ensureEnvFile(ctx, b)

	// Ensure the binary is installed.
	if !m.binaryPresent(ctx, b, cfg.Binary) {
		if !doInstall || strings.TrimSpace(cfg.Install) == "" {
			return fmt.Errorf("cli %q is not installed%s", cfg.Binary,
				ternary(cfg.Install == "", " and no install command was provided", ""))
		}
		out, code, ok := m.cliBash(ctx, b, cfg.Install, cfg.Cwd, 600)
		if !ok {
			return fmt.Errorf("%w: install command couldn't reach the workspace", errCloudUnavailable)
		}
		if code != 0 {
			return fmt.Errorf("install failed (exit %d): %s", code, tailLines(out, 12))
		}
		if !m.binaryPresent(ctx, b, cfg.Binary) {
			return fmt.Errorf("%q still not on PATH after install - does the installer honor $HOME/.local/bin? (HOME is pinned to %s)", cfg.Binary, persistDir)
		}
	} else if doInstall && m.cliStale(ext) && strings.TrimSpace(cfg.Install) != "" {
		// Present but STALE: re-run the install (idempotent — the seeded
		// commands are `pip install -U ...` / re-fetch shapes). Install-once-
		// never-update is how yt-dlp sat at a 4-month-old build until YouTube's
		// bot checks stopped accepting its client signatures (2026-07-09) and
		// the boss's transcript request died on a tool that "was installed".
		// Scraper-facing CLIs rot on a timescale of weeks; boot is the natural,
		// already-serialized place to freshen them. Best-effort: a failed
		// update never takes down a working binary.
		if out, code, ok := m.cliBash(ctx, b, cfg.Install, cfg.Cwd, 600); ok && code == 0 {
			m.logger.Info("refreshed stale cli extension", "name", ext.Name)
		} else if ok {
			m.logger.Warn("stale cli refresh failed; keeping current version",
				"name", ext.Name, "exit", code, "tail", tailLines(out, 4))
		}
	}

	// Ready? (installed + authed)
	if m.cliReady(ctx, b, cfg) {
		ext.Status = StatusActive
		ext.AuthURL = ""
		ext.AuthInstructions = ""
		ext.LastError = ""
		return nil
	}

	// Not ready - try to start the human-in-the-loop auth flow.
	if strings.TrimSpace(cfg.AuthCmd) != "" {
		// CRITICAL: an OAuth `auth login` must keep RUNNING to receive its
		// sign-in callback (localhost redirect) or to keep polling (device
		// code). The old code ran it with a 90s timeout that KILLED it before
		// the boss could finish - so even a captured URL led nowhere. Instead
		// launch it DETACHED on the cloud box (survives this call, holds the
		// callback/poll) and tail its log for the sign-in URL. The matching
		// browser completion happens in Jarvis's OWN cloud browser (Studio
		// opens cfg.AuthURL there), so the callback lands back on this same box.
		url := m.startDetachedAuth(ctx, b, cfg)
		ext.Status = StatusPendingAuth
		ext.AuthURL = url
		if url != "" {
			ext.AuthInstructions = "Sign in to finish - I'll open the page in my own browser and pick up automatically once you're done."
		} else {
			ext.AuthInstructions = fmt.Sprintf("Sign-in for %q is running on the cloud workspace but didn't print a link yet. Open the Terminal and run `%s` to see it.", cfg.Binary, cfg.AuthCmd)
		}
		ext.LastError = ""
		return nil // pending_auth is a valid state, not a failure
	}

	return fmt.Errorf("%q is installed but its check command failed and no auth_cmd is configured to fix it", cfg.Binary)
}

// cliRefreshAfter is how old a cli install may grow before boot re-runs its
// install command. A week keeps scraper-facing tools (yt-dlp) inside the
// window site operators tolerate, without re-installing on every restart.
const cliRefreshAfter = 7 * 24 * time.Hour

// Mac-availability cache TTLs. Positive answers are stable (an installed brew
// binary doesn't vanish); negative answers re-probe sooner because "absent"
// often just means "the Mac was asleep when we asked".
const (
	macProbeTTLPresent = 10 * time.Minute
	macProbeTTLAbsent  = 2 * time.Minute
)

// MacHasBinary reports whether the boss's Mac also has this binary on PATH,
// probing over the Mac bridge with a TTL cache. This is what makes per-bridge
// tool availability a live FACT in the agent's catalog instead of a guess:
// the extension registry only knows about the manager's own cloud installs,
// but the Mac's residential IP is the standing answer to bot-walled sites, so
// the agent must be able to see when a tool exists there too. False when the
// Mac bridge is down or unconfigured — never an error; absence of knowledge
// degrades to the cloud-only behavior.
func (m *Manager) MacHasBinary(ctx context.Context, binary string) bool {
	binary = strings.TrimSpace(binary)
	if m == nil || m.router == nil || binary == "" {
		return false
	}
	m.macAvailMu.Lock()
	if p, ok := m.macAvail[binary]; ok {
		ttl := macProbeTTLAbsent
		if p.present {
			ttl = macProbeTTLPresent
		}
		if time.Since(p.at) < ttl {
			m.macAvailMu.Unlock()
			return p.present
		}
	}
	m.macAvailMu.Unlock()

	present := false
	if b, _, err := m.router.For(ctx, bridge.PrefMac); err == nil && b != nil {
		// The Mac bridge shell may not source brew's profile; include the
		// standard Homebrew locations alongside PATH. cliBash's cloud env.sh
		// prefix no-ops on the Mac (2>/dev/null + `;`), so it's safe here.
		cmd := `PATH="/opt/homebrew/bin:/usr/local/bin:$PATH" command -v ` + shellQuoteCLI(binary) + ` >/dev/null 2>&1`
		_, code, ok := m.cliBash(ctx, b, cmd, "", 10)
		present = ok && code == 0
	}
	m.macAvailMu.Lock()
	if m.macAvail == nil {
		m.macAvail = make(map[string]macProbe)
	}
	m.macAvail[binary] = macProbe{present: present, at: time.Now()}
	m.macAvailMu.Unlock()
	return present
}

// cliStale reports whether this extension's install hasn't been freshened
// within cliRefreshAfter. LastCheckedAt is bumped by SetChecked on activation,
// so it tracks "last time the manager touched the real binary".
func (m *Manager) cliStale(ext *Extension) bool {
	if ext == nil {
		return false
	}
	t := ext.UpdatedAt
	if ext.LastCheckedAt != nil && ext.LastCheckedAt.After(t) {
		t = *ext.LastCheckedAt
	}
	return !t.IsZero() && time.Since(t) > cliRefreshAfter
}

// startDetachedAuth launches a cli's auth_cmd as a DETACHED, long-lived
// process on the cloud workspace (so it survives this call and stays up to
// receive the OAuth callback / keep polling a device code), redirects its
// output to a log on the persistent volume, and tails that log for the sign-in
// URL. Returns the captured URL, or "" if none appeared in the grace window
// (the process keeps running regardless - the boss can finish via the Terminal).
func (m *Manager) startDetachedAuth(ctx context.Context, b bridge.Bridge, cfg CLIConfig) string {
	logPath := persistDir + "/auth_" + sanitizeName(cfg.Binary) + ".log"
	// setsid + & fully detaches from the bash invocation; </dev/null so it
	// never blocks on stdin; pkill clears any stale run so we don't stack
	// listeners on the callback port. Best-effort - ignore exit codes.
	launch := fmt.Sprintf(
		"pkill -f %s >/dev/null 2>&1; : > %s; setsid %s >%s 2>&1 </dev/null & echo launched",
		shellQuoteCLI(cfg.AuthCmd), shellQuoteCLI(logPath), cfg.AuthCmd, shellQuoteCLI(logPath))
	if _, _, ok := m.cliBash(ctx, b, launch, cfg.Cwd, 25); !ok {
		return ""
	}
	// Poll the log for a URL - device-flow CLIs print it within a few seconds.
	for i := 0; i < 10; i++ {
		out, _, _ := m.cliBash(ctx, b, "cat "+shellQuoteCLI(logPath)+" 2>/dev/null", cfg.Cwd, 10)
		if u := urlRe.FindString(out); u != "" {
			return u
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(2 * time.Second):
		}
	}
	return ""
}

// ProbeCLIReady re-runs a cli extension's check command. Used by the
// ExtensionAuthChecklist to detect when the boss has completed auth.
func (m *Manager) ProbeCLIReady(ctx context.Context, ext *Extension) (bool, error) {
	cfg, err := parseCLIConfig(ext.Config)
	if err != nil {
		return false, err
	}
	b, err := m.cloudBridge(ctx)
	if err != nil {
		return false, err
	}
	return m.cliReady(ctx, b, cfg), nil
}

// ListPendingAuth returns cli extensions parked awaiting the boss's auth.
func (m *Manager) ListPendingAuth(ctx context.Context) ([]*Extension, error) {
	if m == nil || m.store == nil {
		return nil, nil
	}
	return m.store.ListByStatus(ctx, StatusPendingAuth)
}

// ListActive returns active extensions (any kind). The CLIProvider filters
// to cli for the system-prompt catalog.
func (m *Manager) ListActive(ctx context.Context) ([]*Extension, error) {
	if m == nil || m.store == nil {
		return nil, nil
	}
	return m.store.ListByStatus(ctx, StatusActive)
}

// CompleteAuth flips a pending_auth cli extension to active and clears the
// auth fields. Called by the heartbeat once ProbeCLIReady passes.
func (m *Manager) CompleteAuth(ctx context.Context, name string) error {
	if m == nil || m.store == nil {
		return nil
	}
	return m.store.SetAuthState(ctx, name, StatusActive, "", "", "", "")
}

// shellQuoteCLI single-quotes a string for bash.
func shellQuoteCLI(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// tailLines returns the last n non-empty-trimmed lines of s, joined - used
// to surface the meaningful tail of a noisy install/auth log.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// touchChecked is a tiny helper to stamp last_checked_at without changing
// status (used when a probe runs but the tool still isn't ready).
func (m *Manager) touchChecked(ctx context.Context, name string) {
	if m == nil || m.store == nil || m.store.pool == nil {
		return
	}
	_, _ = m.store.pool.Exec(ctx,
		`UPDATE mem_extensions SET last_checked_at = NOW() WHERE name = $1`, name)
}

// CloudCLIBinaries implements tools.CLICatalog: the binary names of every
// active cli extension. These live on the cloud workspace volume and nowhere
// else, which is what lets bash_run and the wrong-bridge gate route a bare
// `yt-dlp <url>` correctly without the model having to say so.
//
// Errors return nil, never an error: a catalog miss must degrade to "route as
// usual", never fail the boss's tool call.
func (m *Manager) CloudCLIBinaries(ctx context.Context) []string {
	if m == nil {
		return nil
	}
	active, err := m.ListActive(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range active {
		if e.Kind != KindCLI {
			continue
		}
		cfg, perr := parseCLIConfig(e.Config)
		if perr != nil || strings.TrimSpace(cfg.Binary) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(cfg.Binary))
	}
	return out
}

// CloudEnvPrelude implements tools.CLICatalog: the shell prefix that puts the
// volume-resident installs on PATH. Mirrors cliBash's own wrapper, so the agent
// and the Manager share one definition of "the persistent environment".
func (m *Manager) CloudEnvPrelude() string {
	return "source " + EnvFilePath + " 2>/dev/null && "
}
