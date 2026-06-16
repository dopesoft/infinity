package extensions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// Manager activates runtime-registered extensions into the live tool
// registry. It is the bridge between the mem_extensions store and the
// running process - on boot it re-activates everything enabled; at runtime
// it activates whatever the agent registers via the extension_* tools.
type Manager struct {
	store    *Store
	registry *tools.Registry
	mcp      *tools.MCPManager
	router   *bridge.Router // for kind=cli: where install/check commands run
	logger   *slog.Logger
	// onAuthComplete fires the instant a pending_auth cli extension is verified
	// (via the /check probe), so the agent resumes what it set the tool up for
	// WITHOUT the boss having to message "ok I'm signed in". Wired in the server
	// layer to broadcast the resume as an unprompted turn (same path the
	// heartbeat ExtensionAuthChecklist uses). Nil-safe.
	onAuthComplete func(context.Context, *Extension)
}

// SetOnAuthComplete registers the resume callback (server wiring). Mirrors the
// OnSkillPromoted seam: the Manager stays decoupled from proactive/server while
// the callback builds + broadcasts the resume turn.
func (m *Manager) SetOnAuthComplete(fn func(context.Context, *Extension)) {
	if m != nil {
		m.onAuthComplete = fn
	}
}

func (m *Manager) fireAuthComplete(ctx context.Context, ext *Extension) {
	if m != nil && m.onAuthComplete != nil && ext != nil {
		m.onAuthComplete(ctx, ext)
	}
}

func NewManager(store *Store, registry *tools.Registry, mcpManager *tools.MCPManager, router *bridge.Router, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{store: store, registry: registry, mcp: mcpManager, router: router, logger: logger}
}

// LoadAll activates every enabled extension from the DB. Called once on
// boot, AFTER the embedded mcp.yaml is connected - so a runtime-registered
// extension survives restarts. Returns the count successfully activated.
func (m *Manager) LoadAll(ctx context.Context) (int, error) {
	if m == nil || m.store == nil {
		return 0, nil
	}
	exts, err := m.store.ListEnabled(ctx)
	if err != nil {
		return 0, err
	}
	loaded := 0
	for _, ext := range exts {
		if err := m.activate(ctx, ext); err != nil {
			// Cloud workspace asleep at boot: leave the cli row untouched
			// (don't flap it to error) - the heartbeat / next use re-probes.
			if errors.Is(err, errCloudUnavailable) {
				m.logger.Info("extensions: skipping cli activate (cloud workspace down)", "name", ext.Name)
				continue
			}
			m.logger.Error("extensions: activate failed", "name", ext.Name, "err", err)
			_ = m.store.SetStatus(ctx, ext.Name, StatusError, err.Error())
			continue
		}
		// activate may have set a non-active status (cli pending_auth) plus
		// auth fields - persist via the full upsert so they're not lost.
		if ext.Kind == KindCLI {
			_ = m.store.SetAuthState(ctx, ext.Name, ext.Status, ext.AuthURL, ext.AuthInstructions, ext.LastError)
		} else {
			_ = m.store.SetStatus(ctx, ext.Name, StatusActive, "")
		}
		loaded++
	}
	return loaded, nil
}

// Register persists a new extension and activates it live - the runtime
// self-extension path. The capability is usable this session and reloads
// on the next boot. A failed activation is still persisted (status=error)
// so the boss can see and fix it.
func (m *Manager) Register(ctx context.Context, ext *Extension) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("extensions: no store configured")
	}
	if !ext.Kind.Valid() {
		return fmt.Errorf("extensions: invalid kind %q (want mcp | http_tool)", ext.Kind)
	}
	if ext.Source == "" {
		ext.Source = "agent"
	}
	ext.Enabled = true

	if err := m.activate(ctx, ext); err != nil {
		ext.Status = StatusError
		ext.LastError = err.Error()
		_, _ = m.store.Upsert(ctx, ext)
		return err
	}
	// activate sets ext.Status for cli (active | pending_auth) and may set
	// auth fields. For mcp/http_tool it leaves status unset → default active.
	if ext.Status == "" {
		ext.Status = StatusActive
		ext.LastError = ""
	}
	if _, err := m.store.Upsert(ctx, ext); err != nil {
		return err
	}
	return nil
}

// activate wires one extension into the live registry.
func (m *Manager) activate(ctx context.Context, ext *Extension) error {
	switch ext.Kind {
	case KindHTTPTool:
		cfg, err := parseHTTPToolConfig(ext.Config)
		if err != nil {
			return err
		}
		tool, err := NewHTTPTool(extToolName(ext.Name), ext.Description, cfg)
		if err != nil {
			return err
		}
		m.registry.Register(tool)
		return nil

	case KindMCP:
		if m.mcp == nil {
			return fmt.Errorf("extensions: no MCP manager wired")
		}
		cfg, err := parseMCPConfig(ext.Config)
		if err != nil {
			return err
		}
		return m.mcp.ConnectServer(ctx, tools.MCPServerConfig{
			Name:           ext.Name,
			Transport:      cfg.Transport,
			Enabled:        true,
			URL:            cfg.URL,
			Auth:           cfg.Auth,
			AuthTokenEnv:   cfg.AuthTokenEnv,
			AuthHeaderName: cfg.AuthHeaderName,
		}, m.registry)

	case KindCLI:
		return m.activateCLI(ctx, ext, true)

	default:
		return fmt.Errorf("extensions: unknown kind %q", ext.Kind)
	}
}

// Activate re-runs a cli extension's install + auth probe on demand and
// persists whatever state it lands in (active, or pending_auth with a freshly
// captured device-login URL). This closes the gap where a row corrected to
// pending_auth in the DB (e.g. by a migration) has no auth_url until the next
// Core reboot - the boss would see a sign-in card with no link. The /check
// endpoint only PROBES readiness; this one drives the cloud-pinned auth flow
// so the URL is captured immediately. cli-only; a no-op for other kinds.
func (m *Manager) Activate(ctx context.Context, name string) (*Extension, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("extensions: no store configured")
	}
	ext, err := m.store.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if ext == nil {
		return nil, fmt.Errorf("extensions: no extension named %q", name)
	}
	if ext.Kind != KindCLI {
		return ext, nil // nothing to drive for mcp/http_tool
	}
	// activateCLI installs (if needed), probes readiness, and on "not ready"
	// runs auth_cmd on the CLOUD-pinned bridge (cloudBridge → PrefCloud, which
	// never fails over to the Mac) to capture the device-login URL. This is the
	// structural fix for the "auth failed over to the Mac bridge" trace: auth
	// flows through the manager, not a hand-rolled bash plan step.
	if err := m.activateCLI(ctx, ext, true); err != nil {
		_ = m.store.SetStatus(ctx, name, StatusError, err.Error())
		return nil, err
	}
	if err := m.store.SetAuthState(ctx, name, ext.Status, ext.AuthURL, ext.AuthInstructions, ext.LastError); err != nil {
		return nil, err
	}
	fresh, _ := m.store.GetByName(ctx, name)
	if fresh == nil {
		fresh = ext
	}
	if fresh.Status == StatusActive {
		m.fireAuthComplete(ctx, fresh)
	}
	return fresh, nil
}

// Remove disables an extension. http_tool extensions are unregistered from
// the live registry immediately; an mcp extension stops loading on the
// next boot (its already-connected session lives until restart).
func (m *Manager) Remove(ctx context.Context, name string) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("extensions: no store configured")
	}
	ext, err := m.store.GetByName(ctx, name)
	if err != nil {
		return err
	}
	if ext == nil {
		return fmt.Errorf("extensions: no extension named %q", name)
	}
	if ext.Kind == KindHTTPTool {
		m.registry.Unregister(extToolName(name))
	}
	return m.store.Disable(ctx, name)
}

// List returns every registered extension.
func (m *Manager) List(ctx context.Context) ([]*Extension, error) {
	if m == nil || m.store == nil {
		return nil, nil
	}
	return m.store.List(ctx)
}

// extToolName namespaces an http_tool extension's generated tool name so it
// never collides with a native or MCP tool.
func extToolName(name string) string {
	return "ext_" + sanitizeName(name)
}
