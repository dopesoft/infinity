// Package sessions owns session-level metadata that lives outside the agent
// loop's in-memory map. Right now that's auto-naming - turning a
// freshly-minted session's first exchange into a 3-5 word title so the
// Live header drawer doesn't show "chs3-djnc" garbage. Titling runs through
// the boss's active Studio model selection (see SetActiveModelFn); when no
// override is set it falls back to the cheap Haiku default below.
package sessions

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// haikuModel is the fallback model for naming, used only when the boss has
// no active model selection wired (SetActiveModelFn) or it resolves empty.
// Override the fallback itself with INFINITY_SESSION_NAME_MODEL.
const haikuModel = "claude-haiku-4-5-20251001"

// Drafter is the minimal LLM dependency we need. Implemented by *llm.Anthropic
// via its Draft method (declared as a method on Anthropic; we narrow the
// interface here so the namer doesn't pull the whole llm.Provider surface).
type Drafter interface {
	Draft(ctx context.Context, model, system, userPrompt string, maxTokens int64) (string, error)
}

// Namer renames sessions whose `name` column is NULL by asking a small model
// to summarize the first user→assistant exchange in 3-5 words. Best-effort,
// asynchronous, and idempotent - losing a name race never blocks the agent.
type Namer struct {
	pool    *pgxpool.Pool
	drafter Drafter
	model   string

	mu       sync.Mutex
	inflight map[string]struct{}

	// modelFn resolves the boss's live Studio model selection (the same
	// resolver the agent loop uses). When set and non-empty it overrides
	// the static fallback above so titles use whatever model is picked in
	// Settings. Guarded because naming runs on detached goroutines.
	modelMu sync.RWMutex
	modelFn func(ctx context.Context) string
}

// SetActiveModelFn wires the namer to the boss's live model selection so
// session titles are drafted with the same model Studio chats use. fn returns
// the selected model id (empty when no override is set, in which case naming
// falls back to the Haiku default). Wired once at boot.
func (n *Namer) SetActiveModelFn(fn func(ctx context.Context) string) {
	if n == nil {
		return
	}
	n.modelMu.Lock()
	n.modelFn = fn
	n.modelMu.Unlock()
}

// activeModel returns the model to title with: the boss's Studio selection
// when wired and non-empty, else the cheap Haiku fallback so naming never
// gets pinned to an expensive model just because none was explicitly chosen.
func (n *Namer) activeModel(ctx context.Context) string {
	n.modelMu.RLock()
	fn := n.modelFn
	n.modelMu.RUnlock()
	if fn != nil {
		if m := strings.TrimSpace(fn(ctx)); m != "" {
			return m
		}
	}
	return n.model
}

func NewNamer(pool *pgxpool.Pool, drafter Drafter, model string) *Namer {
	if model == "" {
		model = haikuModel
	}
	return &Namer{
		pool:     pool,
		drafter:  drafter,
		model:    model,
		inflight: map[string]struct{}{},
	}
}

// MaybeName fires off a background naming attempt for the given session,
// using the captured user prompt and assistant reply. Skips quickly when:
//
//   - the session already has a name (looked up against mem_sessions)
//   - another naming attempt is in flight for the same session
//   - either text is empty
//
// The work runs on a detached context so the request lifecycle ending
// (WebSocket disconnect, etc.) does not cancel the Haiku call mid-flight.
func (n *Namer) MaybeName(sessionID, userMsg, assistantMsg string) {
	if n == nil || n.pool == nil || n.drafter == nil {
		return
	}
	if sessionID == "" || strings.TrimSpace(userMsg) == "" {
		return
	}

	n.mu.Lock()
	if _, busy := n.inflight[sessionID]; busy {
		n.mu.Unlock()
		return
	}
	n.inflight[sessionID] = struct{}{}
	n.mu.Unlock()

	go func() {
		defer func() {
			n.mu.Lock()
			delete(n.inflight, sessionID)
			n.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var existing *string
		if err := n.pool.QueryRow(ctx,
			`SELECT name FROM mem_sessions WHERE id = $1::uuid`, sessionID).Scan(&existing); err != nil {
			// Row missing is fine - the loop creates the row on first
			// observation insert; we'll try again next turn.
			return
		}
		if existing != nil && strings.TrimSpace(*existing) != "" {
			return
		}

		name, err := n.draftName(ctx, userMsg, assistantMsg)
		if err != nil || name == "" {
			if err != nil {
				log.Printf("sessions.namer: draft err session=%s: %v", sessionID, err)
			}
			return
		}

		if _, err := n.pool.Exec(ctx,
			`UPDATE mem_sessions SET name = $2 WHERE id = $1::uuid AND name IS NULL`,
			sessionID, name); err != nil {
			log.Printf("sessions.namer: update err session=%s: %v", sessionID, err)
			return
		}
		log.Printf("sessions.namer: session=%s named %q", sessionID, name)
	}()
}

// Rename forcibly sets the session name. Used by /api/sessions/:id/rename
// when the boss wants to override the auto-generated title.
func (n *Namer) Rename(ctx context.Context, sessionID, name string) error {
	if n == nil || n.pool == nil {
		return fmt.Errorf("namer not configured")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		// Empty = clear, so auto-name will fire again on next exchange.
		_, err := n.pool.Exec(ctx,
			`UPDATE mem_sessions SET name = NULL WHERE id = $1::uuid`, sessionID)
		return err
	}
	if len(name) > 80 {
		name = name[:80]
	}
	_, err := n.pool.Exec(ctx,
		`UPDATE mem_sessions SET name = $2 WHERE id = $1::uuid`, sessionID, name)
	return err
}

const namingSystem = `You generate concise session titles for an AI coding-and-thinking workspace.

Rules:
  - 3 to 7 words, no trailing punctuation.
  - Sentence case ("Building chat app with Vite"), not Title Case.
  - Capture what the user is *doing*, not what they said verbatim.
  - No quotes, no emojis, no markdown, no period at the end.
  - If the exchange is a casual greeting or a tiny clarification, return:
      "Quick chat"

Return ONLY the title - nothing else.`

func (n *Namer) draftName(ctx context.Context, userMsg, assistantMsg string) (string, error) {
	if assistantMsg == "" {
		assistantMsg = "(no reply yet)"
	}
	prompt := fmt.Sprintf(
		"User said:\n%s\n\nAssistant replied:\n%s\n\nWrite the session title.",
		truncate(userMsg, 1200),
		truncate(assistantMsg, 1200),
	)
	raw, err := n.drafter.Draft(ctx, n.activeModel(ctx), namingSystem, prompt, 60)
	if err != nil {
		return "", err
	}
	return cleanTitle(raw), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// cleanTitle strips quotes, trailing punctuation, and leading "Title:" labels
// that Haiku occasionally returns despite the instructions.
func cleanTitle(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "\"'`")
	// Take the first non-empty line.
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			s = t
			break
		}
	}
	for _, prefix := range []string{"Title:", "title:", "Session title:", "Name:"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimSpace(strings.TrimPrefix(s, prefix))
			break
		}
	}
	s = strings.TrimRight(s, ".!?,;")
	s = strings.Trim(s, "\"'`")
	if len([]rune(s)) > 80 {
		runes := []rune(s)
		s = string(runes[:80])
	}
	return s
}
