package push

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/proactive"
)

// TrustAdapter implements proactive.TrustNotifier - wire it into the
// TrustStore at boot so every queued contract turns into a banner on
// the boss's iPhone + Mac.
//
//	store := proactive.NewTrustStore(pool)
//	store.SetNotifier(push.NewTrustAdapter(sender))
type TrustAdapter struct {
	Sender *Sender
}

func NewTrustAdapter(sender *Sender) *TrustAdapter {
	return &TrustAdapter{Sender: sender}
}

// NotifyTrustQueued translates a TrustContract into a notification
// payload + delivers it to every active subscription.
//
// The notification deep-links to /trust/<id> so a tap takes the boss
// straight to the approval surface. We use the contract id as the
// `tag` so a repeat insert with the same id wouldn't double-buzz
// (this normally doesn't happen, but Queue is idempotent so the SW
// dedupe is cheap insurance).
func (a *TrustAdapter) NotifyTrustQueued(ctx context.Context, c *proactive.TrustContract) {
	if a == nil || a.Sender == nil || !a.Sender.Configured() {
		return
	}
	if c == nil {
		return
	}
	title := "Approval needed"
	if c.Title != "" {
		title = c.Title
	}
	body := ""
	if c.Preview != "" {
		body = c.Preview
	} else if c.Reasoning != "" {
		body = c.Reasoning
	} else if c.Source != "" {
		body = fmt.Sprintf("Source: %s", c.Source)
	}
	a.Sender.Notify(ctx, Notification{
		Title: title,
		Body:  body,
		Tag:   "trust:" + c.ID,
		URL:   "/trust",
		Kind:  "trust",
		ID:    c.ID,
		// Risk-high contracts require a tap before they dismiss so the
		// boss can't miss them. Everything else dismisses on its own
		// schedule like a normal notification.
		RequireInteraction: c.RiskLevel == "high" || c.RiskLevel == "critical",
	})
}

// FollowupNotifier turns a freshly-inserted mem_followups row into a push.
// Wired into the connector poller via SetFollowupNotifier; pref-gated by
// PrefFollowupNew so the boss can mute "every email lands a banner."
type FollowupNotifier struct {
	Sender *Sender
}

// NotifyFollowupNew sends a banner that deep-links to the dashboard
// follow-up surface (we don't have a /followups/<id> route yet — the
// dashboard inbox card is the destination). Tag is keyed by remote id
// so a follow-up arriving twice (re-poll) dedupes.
func (a *FollowupNotifier) NotifyFollowupNew(ctx context.Context, source, remoteID, from, subject, preview string) {
	if a == nil || a.Sender == nil || !a.Sender.Configured() {
		return
	}
	title := from
	if subject != "" {
		title = subject
	}
	if title == "" {
		title = "New " + source
	}
	body := preview
	if body == "" && from != "" {
		body = "From " + from
	}
	body = truncate(body, 120)
	tag := "followup:" + source + ":" + remoteID
	a.Sender.Notify(ctx, Notification{
		Title: title,
		Body:  body,
		Tag:   tag,
		URL:   "/",
		Kind:  string(PrefFollowupNew),
		ID:    remoteID,
	})
}

// CalendarUpcomingNotifier alerts the boss that a calendar event starts
// within the reminder window. The ticker (push.CalendarTicker) calls this
// once per event-occurrence; the Tag is keyed by event id so a single
// event only banners once per fire window.
type CalendarUpcomingNotifier struct {
	Sender *Sender
}

func (a *CalendarUpcomingNotifier) NotifyEventUpcoming(ctx context.Context, eventID, title string, startsAt time.Time, location, hangoutLink string) {
	if a == nil || a.Sender == nil || !a.Sender.Configured() {
		return
	}
	when := strings.ToLower(startsAt.Local().Format("3:04pm"))
	mins := int(time.Until(startsAt).Minutes())
	rel := fmt.Sprintf("at %s", when)
	if mins > 0 && mins <= 30 {
		rel = fmt.Sprintf("in %d min · %s", mins, when)
	}
	body := rel
	if location != "" {
		body = rel + " · " + location
	} else if hangoutLink != "" {
		body = rel + " · video"
	}
	url := "/"
	if hangoutLink != "" {
		url = hangoutLink
	}
	a.Sender.Notify(ctx, Notification{
		Title:    truncate(firstNonEmpty(title, "Upcoming event"), 80),
		Body:     body,
		Tag:      "calendar:" + eventID,
		URL:      url,
		Kind:     string(PrefCalendarUpcoming),
		ID:       eventID,
		Renotify: true,
	})
}

// RunsNotifier wires runs.Track to push. Kanban-ish surfaces (cron firings,
// skill invocations, voyager optimizer, etc.) all flow through mem_runs;
// the same notifier handles "started" and "finished" with separate pref
// kinds so the boss can opt into the noisier "started" stream without
// losing the "finished" signal.
type RunsNotifier struct {
	Sender *Sender
}

// NotifyRunStarted fires before fn runs. Pref-gated by PrefRunStarted
// (off by default — most runs are short and would spam).
func (a *RunsNotifier) NotifyRunStarted(ctx context.Context, runID, kind, label string) {
	if a == nil || a.Sender == nil || !a.Sender.Configured() {
		return
	}
	a.Sender.Notify(ctx, Notification{
		Title: "Started: " + kindLabel(kind),
		Body:  truncate(label, 120),
		Tag:   "run-started:" + runID,
		URL:   "/cron",
		Kind:  string(PrefRunStarted),
		ID:    runID,
	})
}

// NotifyRunFinished fires after fn returns. Pref-gated by PrefRunFinished
// (on by default — finishes are the signal the boss usually cares about).
func (a *RunsNotifier) NotifyRunFinished(ctx context.Context, runID, kind, label, status, errMsg string, duration time.Duration) {
	if a == nil || a.Sender == nil || !a.Sender.Configured() {
		return
	}
	verb := "done"
	if status == "error" {
		verb = "failed"
	}
	title := fmt.Sprintf("%s %s", kindLabel(kind), verb)
	body := label
	if errMsg != "" {
		body = truncate(errMsg, 140)
	} else if duration > 0 {
		body = fmt.Sprintf("%s · %s", truncate(label, 80), prettyDuration(duration))
	}
	a.Sender.Notify(ctx, Notification{
		Title: title,
		Body:  body,
		Tag:   "run-finished:" + runID,
		URL:   "/cron",
		Kind:  string(PrefRunFinished),
		ID:    runID,
	})
}

// ── small helpers ────────────────────────────────────────────────────────

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func kindLabel(kind string) string {
	switch kind {
	case "cron":
		return "Cron"
	case "skill":
		return "Skill"
	case "voyager.optimize":
		return "Voyager"
	case "voyager.extract":
		return "Voyager extract"
	case "gym.extract":
		return "Gym extract"
	case "heartbeat":
		return "Heartbeat"
	case "sentinel":
		return "Sentinel"
	case "browser.session":
		return "Browser session"
	case "extension.register":
		return "Extension"
	case "background.build":
		return "Background build"
	case "":
		return "Run"
	default:
		return strings.Title(strings.ReplaceAll(kind, ".", " "))
	}
}

func prettyDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) - mins*60
	if secs == 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dm%ds", mins, secs)
}
