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
	"github.com/dopesoft/infinity/core/internal/tools"
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
		ID       string `json:"id"`
		ActionID string `json:"action_id"`
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
	if action == nil {
		writeError(w, http.StatusBadRequest, "unknown action for this item")
		return
	}

	prompt := buildSurfaceActionPrompt(it, action)
	label := action.Label
	if it.Title != "" {
		label = action.Label + " · " + it.Title
	}

	// Fire in the background; the mem_runs row + realtime carry progress to
	// the UI. We intentionally do NOT block the HTTP response on the agent
	// turn - it can take minutes, and the client watches via useRuns().
	go func() {
		_ = runs.Track(context.Background(), runs.KindSurfaceAction, it.ID, label, runs.SourceAgent, func(ctx context.Context) error {
			ctx = tools.WithAutonomous(ctx)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			sessionID := "surface-action-" + it.ID
			out := make(chan agent.RunEvent, 256)
			go func() {
				for range out { //nolint:revive // drain; capture hooks persist what matters
				}
			}()
			return s.loop.Run(ctx, sessionID, prompt, "", nil, out)
		})
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":        true,
		"kind":      string(runs.KindSurfaceAction),
		"target_id": it.ID,
	})
}

// buildSurfaceActionPrompt composes the autonomous turn's user message from
// the chosen action's intent + the item's context, so the agent has
// everything it needs to act without re-fetching.
func buildSurfaceActionPrompt(it *surface.Item, a *surface.Action) string {
	var b strings.Builder
	b.WriteString("The boss tapped the \"")
	b.WriteString(a.Label)
	b.WriteString("\" action on a dashboard item. Carry out this instruction now using your tools, then report the real outcome. If your action resolves the item, surface_update it (pass the id below); otherwise leave it open.\n\n")
	b.WriteString("INSTRUCTION: ")
	b.WriteString(a.Intent)
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
