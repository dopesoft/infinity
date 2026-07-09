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
	// Deliberately does NOT tell the model to source the env or pass
	// bridge="cloud". bash_run now does both itself for any binary in this
	// catalog (tools.CloudCLICommand), and a mechanic stated as prose is a
	// mechanic that gets dropped — this overlay said "prefix with `source
	// env.sh &&`" for months while the model skipped it. Pass the bare command.
	b.WriteString(
		"Command-line tools installed and ready in your cloud workspace. Run one by passing the bare " +
			"command to bash_run — it is already on PATH with its saved credentials, on the right machine. " +
			"You do not need to install, source, or route anything.\n")
	b.WriteString(
		"SIGN-IN RULE (always): to authenticate any of these, call extension_activate \"<name>\" then " +
			"browser_open the auth_url it returns, so the sign-in page is LIVE in the boss's Preview pane and he " +
			"approves it there - this works even when he's on his phone. The sign-in ALWAYS happens inside YOUR " +
			"cloud browser. NEVER run `<tool> auth login` / `<tool> login` via bash_run (that opens a browser on " +
			"the wrong machine and auths the wrong box), and NEVER hand the boss a URL to open on his own computer.\n")
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
			"- %s: INSTALLED BUT AWAITING SIGN-IN. To finish it YOURSELF: call extension_activate %q, then "+
				"browser_open the auth_url it returns so the sign-in page is live in the boss's Preview pane and he "+
				"approves it there (works from his phone). Do NOT run its `auth login`/`login` via bash_run and do "+
				"NOT hand him a URL or open it on his computer.\n",
			e.Name, e.Name))
	}
	b.WriteString("</installed_cli_tools>")
	return b.String(), nil
}
