package extensions

import (
	"context"
	"fmt"
	"strings"
)

// CLIProvider injects the catalog of installed cli extensions into the
// system prompt each turn, so Jarvis KNOWS the tools exist and how to run
// them. An mcp/http_tool extension self-describes via its registered tool
// schema; a cli is just a binary on the cloud workspace, so it needs this
// overlay to be discoverable.
//
// It also surfaces any pending_auth tools with their auth URL, so Jarvis
// re-surfaces the link to the boss instead of forgetting a half-set-up
// capability.
//
// Implements agent.MemoryProvider. Stays silent (returns "") when there's
// nothing installed, per the provider contract.
type CLIProvider struct {
	mgr *Manager
}

func NewCLIProvider(mgr *Manager) *CLIProvider { return &CLIProvider{mgr: mgr} }

func (p *CLIProvider) BuildSystemPrefix(ctx context.Context, _ string, _ string) (string, error) {
	if p == nil || p.mgr == nil {
		return "", nil
	}
	active, err := p.mgr.ListActive(ctx)
	if err != nil {
		return "", nil // never block a turn on this overlay
	}
	pending, _ := p.mgr.ListPendingAuth(ctx)

	var b strings.Builder
	var activeCLIs []*Extension
	for _, e := range active {
		if e.Kind == KindCLI {
			activeCLIs = append(activeCLIs, e)
		}
	}
	if len(activeCLIs) == 0 && len(pending) == 0 {
		return "", nil
	}

	b.WriteString("<installed_cli_tools>\n")
	b.WriteString(fmt.Sprintf(
		"Command-line tools installed in your cloud workspace. Run them via bash_run; "+
			"prefix the command with `source %s && ` so they use the persistent install + saved credentials.\n",
		EnvFilePath))
	for _, e := range activeCLIs {
		cfg, perr := parseCLIConfig(e.Config)
		if perr != nil {
			continue
		}
		line := "- " + e.Name
		if e.Description != "" {
			line += ": " + e.Description
		}
		if strings.TrimSpace(cfg.Usage) != "" {
			line += " | usage: " + strings.TrimSpace(cfg.Usage)
		} else if cfg.Binary != "" {
			line += " | binary: " + cfg.Binary
		}
		b.WriteString(line + "\n")
	}
	for _, e := range pending {
		if e.Kind != KindCLI {
			continue
		}
		b.WriteString(fmt.Sprintf(
			"- %s: INSTALLED BUT AWAITING THE BOSS'S SIGN-IN. If it's relevant, remind him: %s\n",
			e.Name, pendingLine(e)))
	}
	b.WriteString("</installed_cli_tools>")
	return b.String(), nil
}

func pendingLine(e *Extension) string {
	if e.AuthURL != "" {
		return "open " + e.AuthURL + " to finish setup"
	}
	if e.AuthInstructions != "" {
		return e.AuthInstructions
	}
	return "authentication still needed"
}
