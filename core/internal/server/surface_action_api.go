package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/dopesoft/infinity/core/internal/surface"
)

// handleSurfaceAction is the SURFACE RETURN-PATH endpoint. When the boss taps
// an action button on a dashboard card, Studio POSTs {id, action_id} here. We
// look up the item + the chosen action, then seed an AUTONOMOUS agent turn
// prompted with the action's intent + the item's full context. The turn is
// tracked in mem_runs (kind=surface.action, target=item id) so Studio's
// useRuns()/RunIndicator shows live progress that survives navigation,
// refresh, and a second device - per the "server-tracked progress" rule.
//
// The work runs in the background; we return 202 immediately with the run's
// kind + target so the client can subscribe. Generic: ANY surfaced item with
// actions flows through here, with no per-action code.
func (s *Server) handleSurfaceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.loop == nil || s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "agent loop not available")
		return
	}
	var req struct {
		ID        string `json:"id"`
		ActionID  string `json:"action_id"`
		DraftText string `json:"draft_text,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	req.ActionID = strings.TrimSpace(req.ActionID)
	if req.ID == "" || req.ActionID == "" {
		writeError(w, http.StatusBadRequest, "id and action_id are required")
		return
	}

	store := surface.NewStore(s.pool, slog.Default())
	it, err := store.Get(r.Context(), req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load item")
		return
	}
	if it == nil {
		writeError(w, http.StatusNotFound, "no such surface item")
		return
	}
	var action *surface.Action
	for i := range it.Actions {
		if it.Actions[i].ID == req.ActionID {
			action = &it.Actions[i]
			break
		}
	}
	if action == nil && req.ActionID == "send_reply" && isEmailSurfaceItem(it) && strings.TrimSpace(req.DraftText) != "" {
		action = &surface.Action{
			ID:     "send_reply",
			Label:  "Send reply",
			Style:  "primary",
			Intent: "Send the boss-edited reply text on this same email thread using the connected Gmail account. Use an existing draft id from metadata when available; otherwise send it as a reply in the same thread. After the send succeeds, call surface_update with status done.",
		}
	}
	if action == nil {
		writeError(w, http.StatusBadRequest, "unknown action for this item")
		return
	}

	// Persist the sent reply text deterministically the moment Send is tapped —
	// a mechanic, not LLM prose (Rule #1b). This is what the viewer renders as a
	// read-only "Response" section, and because it lives in metadata it survives
	// refresh and shows on a second device. The agent turn below still performs
	// the real Gmail send and marks the item done.
	if req.ActionID == "send_reply" {
		if txt := strings.TrimSpace(req.DraftText); txt != "" {
			_ = store.Update(r.Context(), it.ID, surface.Patch{
				MetadataMerge: map[string]any{
					"sent_reply": txt,
					"sent_at":    time.Now().UTC().Format(time.RFC3339),
				},
			})
		}
	}

	prompt := buildSurfaceActionPrompt(it, action, req.DraftText)
	label := action.Label
	if it.Title != "" {
		label = action.Label + " · " + it.Title
	}
	sessionID := "surface-action-" + it.ID

	// Fire in the background; the mem_runs row + realtime carry progress to
	// the UI. We intentionally do NOT block the HTTP response on the agent
	// turn - it can take minutes, and the client watches via useRuns().
	go func() {
		_ = runs.Track(context.Background(), runs.KindSurfaceAction, it.ID, label, runs.SourceAgent, func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			out := make(chan agent.RunEvent, 256)
			go func() {
				for ev := range out {
					sendRunEventToWS(func(frame wsServerEvent) {
						s.broadcastAll(frame)
					}, ev)
				}
			}()
			err := s.loop.Run(ctx, sessionID, prompt, "", nil, out)
			close(out)
			if err != nil {
				s.broadcastAll(wsServerEvent{Type: "error", SessionID: sessionID, Message: err.Error()})
			}
			return err
		})
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":         true,
		"kind":       string(runs.KindSurfaceAction),
		"target_id":  it.ID,
		"session_id": sessionID,
	})
}

// buildSurfaceActionPrompt composes the action turn's user message from the
// chosen action's intent + the item's context, so the agent has
// everything it needs to act without re-fetching.
func buildSurfaceActionPrompt(it *surface.Item, a *surface.Action, draftText string) string {
	var b strings.Builder
	b.WriteString("The boss tapped the \"")
	b.WriteString(a.Label)
	b.WriteString("\" action on a dashboard item. This is an explicit boss-clicked action, not an unattended cron. Carry out this instruction now using your tools, then report the real outcome. If your action resolves the item, surface_update it (pass the id below); otherwise leave it open.\n\n")
	b.WriteString("INSTRUCTION: ")
	b.WriteString(a.Intent)
	if a.ID == "draft_reply" {
		b.WriteString("\n\nDRAFT RESULT CONTRACT: after creating the Gmail draft, call surface_update with this item id and metadata {\"draft\":\"<the exact reply text you drafted>\",\"draft_id\":\"<gmail draft id if the tool returned one>\"}. Do not mark the item done or dismissed; the boss still needs to review/send it.")
	}
	if a.ID == "send_reply" {
		if txt := strings.TrimSpace(draftText); txt != "" {
			b.WriteString("\n\nBOSS-EDITED REPLY TEXT TO SEND:\n")
			b.WriteString(txt)
			b.WriteString("\n\nUse this exact edited text as the reply body unless a required upstream field forces harmless formatting.")
		}
		b.WriteString("\n\nSEND RESULT CONTRACT: sending email is destructive, so use the normal tool approval gate if the selected Gmail send tool requires it. Only call surface_update status=done after the send succeeds.")
	}
	b.WriteString("\n\nITEM CONTEXT:\n")
	fmt.Fprintf(&b, "- id: %s   (use this for surface_update)\n", it.ID)
	fmt.Fprintf(&b, "- surface/kind: %s / %s\n", it.Surface, it.Kind)
	if it.Title != "" {
		fmt.Fprintf(&b, "- title: %s\n", it.Title)
	}
	if it.Subtitle != "" {
		fmt.Fprintf(&b, "- subtitle: %s\n", it.Subtitle)
	}
	if it.Body != "" {
		fmt.Fprintf(&b, "- body: %s\n", it.Body)
	}
	if it.URL != "" {
		fmt.Fprintf(&b, "- url: %s\n", it.URL)
	}
	if it.ExternalID != "" {
		fmt.Fprintf(&b, "- external_id: %s\n", it.ExternalID)
	}
	if len(it.Metadata) > 0 {
		if mj, err := json.Marshal(it.Metadata); err == nil {
			fmt.Fprintf(&b, "- metadata: %s\n", string(mj))
		}
	}
	return b.String()
}

func isEmailSurfaceItem(it *surface.Item) bool {
	if it == nil {
		return false
	}
	surf := strings.ToLower(strings.TrimSpace(it.Surface))
	kind := strings.ToLower(strings.TrimSpace(it.Kind))
	return kind == "email" && (surf == "followups" || surf == "inbox")
}
