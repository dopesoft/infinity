package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/dashboard"
	"github.com/google/uuid"
)

// handleSessionsSeed creates a fresh session pre-bound to a dashboard
// item so the agent loop can hydrate the artifact as a sticky Context
// Block on the first turn.
//
//	POST /api/sessions/seed
//	  { "kind": "followup", "id": "<uuid>" }
//	→ { "id": "<new-session-uuid>" }
//
// The seeded_from JSONB column carries {kind, id}. The agent loop reads
// it when building the system prompt for turn 1 and emits a "Context"
// block with the artifact's native form - the same shape Studio's
// ObjectViewer renders.
//
// The Discuss-with-Jarvis CTA in ObjectViewer hits this endpoint and
// navigates the user to /live?session=<id>. From there everything is
// normal session behavior - replies stream over the WS, and Jarvis closes the
// source artifact only after the work is actually resolved.
func (s *Server) handleSessionsSeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "no database")
		return
	}
	var body struct {
		Kind     string          `json:"kind"`
		ID       string          `json:"id"`
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Kind == "" {
		writeError(w, http.StatusBadRequest, "kind required")
		return
	}
	// id can be empty for kinds where the artifact doesn't have a stable
	// uuid yet (e.g. "scratch" seed). For everything else, require it so
	// the agent loop can actually fetch the artifact.
	if !isSeedKindWithoutID(body.Kind) && body.ID == "" {
		writeError(w, http.StatusBadRequest, "id required for this kind")
		return
	}

	seed := map[string]any{
		"kind": body.Kind,
		"id":   body.ID,
	}
	if len(body.Snapshot) > 0 && string(body.Snapshot) != "null" {
		seed["snapshot"] = body.Snapshot
	}
	seedJSON, _ := json.Marshal(seed)

	id := uuid.New()
	_, err := s.pool.Exec(r.Context(), `
		INSERT INTO mem_sessions (id, started_at, seeded_from)
		VALUES ($1, NOW(), $2::jsonb)
	`, id, string(seedJSON))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create session: %v", err))
		return
	}

	// Make the seed visible in the chat transcript immediately and durable
	// for turn-one hydration. The next user reply follows this context in the
	// same session, so the agent sees exactly what the boss is responding to.
	//
	// hook_name is 'DashboardSeed' (not 'UserPromptSubmit') on purpose: this
	// is injected context, not something the boss typed. The distinct hook
	// lets the messages API render it as a "from dashboard" context card and
	// keeps the Voyager extractors from mistaking it for a real user prompt.
	// hydrateLoopSession still maps it to a user-role turn so the agent
	// treats it as the opening of the conversation.
	rawText := formatSeedContext(body.Kind, body.ID, body.Snapshot)
	// For an email follow-up, hydrate the FULL email body into turn-1 context
	// so Jarvis sees the whole message (not just the dashboard summary) the
	// moment the boss hits "Discuss with Jarvis". The body is fetched via the
	// same path the viewer uses; degrade silently if unavailable.
	if body.Kind == "followup" && s.cfg.DashboardAPI != nil {
		origin := originFromSnapshot(body.Snapshot)
		if full, ferr := s.cfg.DashboardAPI.FullEmailText(r.Context(), body.ID, origin); ferr == nil {
			if full = strings.TrimSpace(full); full != "" {
				rawText += "\n\nFull email body:\n" + full
			}
		}
	}
	// For a generated document/artifact, hydrate the actual CONTENT into turn-1
	// context so "Discuss with Jarvis" lands the boss in a chat that can answer
	// questions about the doc immediately — not one holding a bare file path.
	// Written docs carry markdown; spreadsheets carry an HTML preview we flatten
	// through the workspace proxy; office/PDF binaries get a clear path plus an
	// instruction to open them. Best-effort: degrade silently if unavailable.
	if body.Kind == "artifact" && s.cfg.DashboardAPI != nil {
		if md, htmlPath, storagePath, format, aerr := s.cfg.DashboardAPI.FullArtifactText(r.Context(), body.ID); aerr == nil {
			switch {
			case md != "":
				rawText += "\n\nFull document content:\n" + clampSeedDoc(md)
			case htmlPath != "":
				if text := strings.TrimSpace(dashboard.HTMLToText(s.fetchWorkspaceRaw(r.Context(), htmlPath))); text != "" {
					rawText += "\n\nFull document content (rendered from preview):\n" + clampSeedDoc(text)
				} else if storagePath != "" {
					rawText += "\n\n" + openDocNote(storagePath, format)
				}
			case storagePath != "":
				rawText += "\n\n" + openDocNote(storagePath, format)
			}
		}
	}
	if _, err := s.pool.Exec(r.Context(), `
		INSERT INTO mem_observations (session_id, hook_name, payload, raw_text, importance, created_at)
		VALUES ($1::uuid, 'DashboardSeed', $2::jsonb, $3, 8, NOW())
	`, id, string(seedJSON), rawText); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("seed context: %v", err))
		return
	}

	writeJSONResponse(w, http.StatusOK, map[string]any{
		"id":         id.String(),
		"seededFrom": map[string]any{"kind": body.Kind, "id": body.ID},
	})
}

// isSeedKindWithoutID lists discriminated-union kinds that can spawn a
// seeded session without a concrete artifact id (e.g. "scratch" notes).
// Most dashboard items DO require an id - keep this list short and
// explicit.
func isSeedKindWithoutID(kind string) bool {
	switch kind {
	case "scratch":
		return true
	default:
		return false
	}
}

// originFromSnapshot pulls the follow-up's `origin` ("followup" | "surface")
// out of the seed snapshot so FullEmailText queries the right table. Defaults
// to "followup" when absent.
func originFromSnapshot(snapshot json.RawMessage) string {
	if len(snapshot) == 0 {
		return "followup"
	}
	var s struct {
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(snapshot, &s); err == nil && strings.TrimSpace(s.Origin) != "" {
		return s.Origin
	}
	return "followup"
}

func formatSeedContext(kind, artifactID string, snapshot json.RawMessage) string {
	var b strings.Builder
	b.WriteString("Dashboard context for discussion\n")
	b.WriteString("Kind: ")
	b.WriteString(kind)
	if artifactID != "" {
		b.WriteString("\nID: ")
		b.WriteString(artifactID)
	}
	if len(snapshot) > 0 && string(snapshot) != "null" {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, snapshot, "", "  "); err == nil {
			b.WriteString("\n\nArtifact:\n")
			b.Write(pretty.Bytes())
		}
	}
	b.WriteString("\n\nI want to discuss this with Jarvis.")
	return b.String()
}

// maxSeedDocChars caps a hydrated document so a large file can't blow the
// agent's turn-1 context. Matches the spirit of FullEmailText's 12k cap, a
// touch higher since written docs run longer than emails.
const maxSeedDocChars = 16000

// clampSeedDoc trims and truncates hydrated document text for the seed context.
func clampSeedDoc(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxSeedDocChars {
		return s[:maxSeedDocChars] + "\n…[truncated]"
	}
	return s
}

// openDocNote is the fallback for office/PDF binaries we can't flatten to text:
// hand the agent the concrete path and tell it to open the file rather than
// answer from the metadata, so "Discuss" never degrades into a guess.
func openDocNote(storagePath, format string) string {
	suffix := ""
	if f := strings.TrimSpace(format); f != "" {
		suffix = " (" + f + ")"
	}
	return "The document file" + suffix + " is stored at " + storagePath +
		". Open or read it with your file/canvas tools before answering any question " +
		"about its contents — do not guess from the metadata above."
}

// fetchWorkspaceRaw pulls a file's raw text from the cloud workspace bridge —
// the same proxy handleWorkspaceDownload streams through. Best-effort: returns
// "" on any failure so seed enrichment degrades silently. Capped so a giant
// preview can't blow the agent's context before it's even flattened.
func (s *Server) fetchWorkspaceRaw(ctx context.Context, path string) string {
	base := strings.TrimRight(s.cfg.WorkspaceRawBase, "/")
	if base == "" || strings.TrimSpace(path) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/fs/raw?path="+url.QueryEscape(path), nil)
	if err != nil {
		return ""
	}
	if s.cfg.WorkspaceToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.WorkspaceToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b)
}

// Local helpers - server.go has its own writeJSON/writeErr but they're
// package-internal and named differently. Mirror them here so this file
// stays self-contained without cross-file knowledge.

func writeJSONResponse(w http.ResponseWriter, status int, body any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSONResponse(w, status, map[string]any{"error": msg})
}
