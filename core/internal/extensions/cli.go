package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

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
mkdir -p "$HOME/.local/bin" "$HOME/bin" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME" 2>/dev/null || true
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
		out, _, _ := m.cliBash(ctx, b, cfg.AuthCmd, cfg.Cwd, 90)
		url := urlRe.FindString(out)
		ext.Status = StatusPendingAuth
		ext.AuthURL = url
		if url != "" {
			ext.AuthInstructions = fmt.Sprintf("Open %s to sign in. I'll keep checking and pick up automatically once you're done.", url)
		} else {
			// No URL captured - surface whatever the auth command said so
			// the boss/agent can act on it.
			ext.AuthInstructions = strings.TrimSpace(tailLines(out, 8))
			if ext.AuthInstructions == "" {
				ext.AuthInstructions = fmt.Sprintf("Run `%s` on the cloud workspace to authenticate.", cfg.AuthCmd)
			}
		}
		ext.LastError = ""
		return nil // pending_auth is a valid state, not a failure
	}

	return fmt.Errorf("%q is installed but its check command failed and no auth_cmd is configured to fix it", cfg.Binary)
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
	return m.store.SetAuthState(ctx, name, StatusActive, "", "", "")
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
