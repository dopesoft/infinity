package proactive

import (
	"context"
	"fmt"

	"github.com/dopesoft/infinity/core/internal/extensions"
)

// ExtensionAuthChecklist closes the human-in-the-loop auth loop for cli
// extensions. When Jarvis installs a tool that needs the boss to sign in
// (device-code OAuth, a browser login), the extension parks in
// status='pending_auth' and Jarvis opens the sign-in page in the boss's
// Preview pane (browser_open) so he signs in there. This checklist re-runs the
// tool's check command on each heartbeat tick; the moment it passes, it flips
// the extension to 'active' and emits a Finding.
//
// The Finding is broadcast to any live session as an unprompted turn (the
// heartbeat's onFinding wire), which is the "...then continues" the boss
// asked for: Jarvis is told the tool is ready and what he was going to do
// with it (resume_intent), and picks up without the boss prompting again.
//
// Zero per-tool code: the cognition for HOW to check readiness lives in the
// extension's config (check_cmd); this checklist only notices completion and
// resumes. Composes via ComposeChecklists; no-ops when mgr is nil.
func ExtensionAuthChecklist(mgr *extensions.Manager) Checklist {
	return func(ctx context.Context, h *Heartbeat) ([]Finding, error) {
		if mgr == nil {
			return nil, nil
		}
		pending, err := mgr.ListPendingAuth(ctx)
		if err != nil || len(pending) == 0 {
			return nil, err
		}
		var findings []Finding
		for _, ext := range pending {
			ready, perr := mgr.ProbeCLIReady(ctx, ext)
			if perr != nil || !ready {
				continue // still waiting (or workspace asleep) - try next tick
			}
			if err := mgr.CompleteAuth(ctx, ext.Name); err != nil {
				continue
			}
			tag := "extension_auth:" + ext.Name
			if h != nil {
				ResolveSourceTag(ctx, h.pool, tag)
			}
			detail := fmt.Sprintf(
				"The %q tool is installed and authenticated in the cloud workspace - run it via bash_run (source %s first so it uses the saved credentials).",
				ext.Name, extensions.EnvFilePath)
			if ext.ResumeIntent != "" {
				detail += "\nResume what you set it up for: " + ext.ResumeIntent
			}
			findings = append(findings, Finding{
				Kind:        "outcome",
				Title:       fmt.Sprintf("%s is authenticated and ready", ext.Name),
				Detail:      detail,
				PreApproved: true,
				Source:      "extension_auth",
				SourceTag:   tag,
			})
		}
		return findings, nil
	}
}
