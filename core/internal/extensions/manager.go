package extensions

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// Manager activates runtime-registered extensions into the live tool
// registry. It is the bridge between the mem_extensions store and the
// running process — on boot it re-activates everything enabled; at runtime
// it activates whatever the agent registers via the extension_* tools.
type Manager struct {
	store    *Store
	registry *tools.Registry
	mcp      *tools.MCPManager
	logger   *slog.Logger
}

func NewManager(store *Store, registry *tools.Registry, mcpManager *tools.MCPManager, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{store: store, registry: registry, mcp: mcpManager, logger: logger}
}

// LoadAll activates every enabled extension from the DB. Called once on
// boot, AFTER the embedded mcp.yaml is connected — so a runtime-registered
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
			m.logger.Error("extensions: activate failed", "name", ext.Name, "err", err)
			_ = m.store.SetStatus(ctx, ext.Name, StatusError, err.Error())
			continue
		}
		_ = m.store.SetStatus(ctx, ext.Name, StatusActive, "")
		loaded++
	}
	return loaded, nil
}

// Register persists a new extension and activates it live — the runtime
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
	ext.Status = StatusActive
	ext.LastError = ""
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

	default:
		return fmt.Errorf("extensions: unknown kind %q", ext.Kind)
	}
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
