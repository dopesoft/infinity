// Package sessions owns session-level metadata that lives outside the agent
// loop's in-memory map. Right now that's auto-naming - turning a
// freshly-minted session's first exchange into a 3-5 word title so the
// Live header drawer doesn't show "chs3-djnc" garbage. Titling runs through
// the boss's ACTIVE provider + model (the same one chat uses, via
// SetActiveModelFn) - so whatever model is set in Settings (gpt-5.4,
// Sonnet, …) names the session. Provider-agnostic: it calls the standard
// llm.Provider.Stream, not a vendor-specific draft method.
package sessions

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// infoLog writes to stdout so Railway tags these lines severity=info
// instead of the severity=error it stamps on stderr (stdlib log's
// default). Reserve the default log.Printf for genuine failures.
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

// Namer renames sessions whose `name` column is NULL by asking a small model
// to summarize the first user→assistant exchange in 3-5 words. Best-effort,
// asynchronous, and idempotent - losing a name race never blocks the agent.
type Namer struct {
	pool     *pgxpool.Pool
	provider llm.Provider
	model    string // optional INFINITY_SESSION_NAME_MODEL override; "" = active/default

	mu       sync.Mutex
	inflight map[string]struct{}

	// modelFn resolves the boss's live Studio model selection (the same
	// resolver the agent loop uses). When set and non-empty it overrides
	// the static fallback above so titles use whatever model is picked in
	// Settings. Guarded because naming runs on detached goroutines.
	modelMu sync.RWMutex
	modelFn func(ctx context.Context) string

	// providerFn resolves the brain to draft titles on, per call. Set at
	// boot to "the cheapest healthy provider", so a title never fails
	// because one plan is spent - which is exactly how naming died on
	// 2026-08-30, with two sessions burning all three attempts against an
	// exhausted ChatGPT plan. nil falls back to the fixed provider.
	provMu     sync.RWMutex
	providerFn func() llm.Provider
}

// SetProviderFn wires the namer to a live brain resolver. Wired once at boot.
func (n *Namer) SetProviderFn(fn func() llm.Provider) {
	if n == nil {
		return
	}
	n.provMu.Lock()
	n.providerFn = fn
	n.provMu.Unlock()
}

// brain resolves the provider for one drafting call, preferring the live
// resolver and falling back to the one handed in at construction.
func (n *Namer) brain() llm.Provider {
	n.provMu.RLock()
	fn := n.providerFn
	n.provMu.RUnlock()
	if fn != nil {
		if p := fn(); p != nil {
			return p
		}
	}
	return n.provider
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

// activeModel returns the model to title with: the boss's live Studio
// selection (the same model chat uses) when wired, else an optional
// INFINITY_SESSION_NAME_MODEL override, else "" so the active provider picks
// its own default. No vendor coupling - whatever the boss set names the session.
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

func NewNamer(pool *pgxpool.Pool, provider llm.Provider, model string) *Namer {
	return &Namer{
		pool:     pool,
		provider: provider,
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
	if n == nil || n.pool == nil || n.provider == nil {
		return
	}
	if sessionID == "" || strings.TrimSpace(userMsg) == "" {
		return
	}
	// A non-uuid id is a synthetic session (delegate child, background worker).
	// It has no mem_sessions row by design, so every query below would error.
	// Skip it deliberately rather than erroring once per turn forever.
	if _, err := uuid.Parse(sessionID); err != nil {
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
			// The row usually just hasn't committed yet: it's created
			// asynchronously by the capture pipeline on the first observation,
			// and this runs the moment the turn ends.
			//
			// This used to be the end of the story ("we'll try again next
			// turn"), which quietly assumed there would BE a next turn. A
			// scheduled run has exactly one, so its session stayed nameless
			// forever, and that assumption is most of why the boss's list
			// filled up with hex slugs. SweepUnnamed now owns the retry for
			// every one of these, however long after the fact.
			if !errors.Is(err, pgx.ErrNoRows) {
				log.Printf("sessions.namer: lookup session=%s: %v (the sweep will retry)", sessionID, err)
			}
			return
		}
		if existing != nil && strings.TrimSpace(*existing) != "" {
			return
		}

		name, err := n.draftName(ctx, userMsg, assistantMsg)
		if err != nil || name == "" {
			// Not the end of the road any more: record the attempt so the
			// sweep retries it (and gives up after a bounded number rather
			// than burning calls on a session that can never be titled).
			reason := "empty draft"
			if err != nil {
				reason = err.Error()
				log.Printf("sessions.namer: draft err session=%s: %v", sessionID, err)
			}
			n.countNameAttempt(ctx, sessionID, reason)
			return
		}

		if _, err := n.pool.Exec(ctx,
			`UPDATE mem_sessions SET name = $2, auto_named = TRUE WHERE id = $1::uuid AND name IS NULL`,
			sessionID, name); err != nil {
			log.Printf("sessions.namer: update err session=%s: %v", sessionID, err)
			return
		}
		infoLog.Printf("sessions.namer: session=%s named %q", sessionID, name)
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
			`UPDATE mem_sessions SET name = NULL, auto_named = FALSE WHERE id = $1::uuid`, sessionID)
		return err
	}
	if len(name) > 80 {
		name = name[:80]
	}
	// Boss-chosen title: mark auto_named FALSE so nothing overwrites it.
	_, err := n.pool.Exec(ctx,
		`UPDATE mem_sessions SET name = $2, auto_named = FALSE WHERE id = $1::uuid`, sessionID, name)
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
	// One-shot completion via the active provider's Stream (the only method on
	// llm.Provider). We don't want the token stream - just the final text - so
	// we drain the event channel and read Response.Text. The caller owns the
	// channel and closes it after Stream returns (same convention as the loop).
	brain := n.brain()
	if brain == nil {
		return "", errors.New("no brain available to draft a session title")
	}
	// The Settings model id belongs to the Settings VENDOR. Naming may be
	// running on a different one (the cheapest healthy brain), and handing
	// "deepseek-v4-pro" to OpenAI is a guaranteed 400. Pass the id only when
	// it belongs to this provider's family; otherwise let the provider use
	// its own default.
	model := n.activeModel(ctx)
	if model != "" && !llm.ModelFamilyMatches(brain.Name(), model) {
		model = ""
	}
	out := make(chan llm.StreamEvent, 64)
	var resp llm.Response
	var serr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, serr = brain.Stream(
			ctx,
			model,
			namingSystem,
			[]llm.Message{{Role: llm.RoleUser, Content: prompt}},
			nil,
			out,
		)
		close(out)
	}()
	for range out {
		// drain - we only need the final aggregated text
	}
	<-done
	if serr != nil {
		return "", serr
	}
	return cleanTitle(resp.Text), nil
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
