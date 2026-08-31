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

	mu       sync.Mutex
	inflight map[string]struct{}

	// modelFn resolves the boss's live Studio model selection (the same
	// resolver the agent loop uses). When set and non-empty it overrides
	// the static fallback above so titles use whatever model is picked in
	// Settings. Guarded because naming runs on detached goroutines.
	modelMu sync.RWMutex
	modelFn func(ctx context.Context) string

	// providersFn resolves the brains to try, IN ORDER, for one title. A LIST
	// and not a single provider, because "use the plan unless it is down or
	// does not work" cannot be expressed by picking one brain up front: you
	// only learn a brain does not work by asking it. Set at boot; nil falls
	// back to the fixed provider.
	provMu      sync.RWMutex
	providersFn func() []llm.Provider
}

// SetProvidersFn wires the namer to a live, ORDERED brain resolver. Wired once
// at boot. Earlier entries are preferred; draftName walks the list and moves on
// whenever a brain refuses the work.
func (n *Namer) SetProvidersFn(fn func() []llm.Provider) {
	if n == nil {
		return
	}
	n.provMu.Lock()
	n.providersFn = fn
	n.provMu.Unlock()
}

// brain resolves the provider for one drafting call, preferring the live
// resolver and falling back to the one handed in at construction.
func (n *Namer) brains() []llm.Provider {
	n.provMu.RLock()
	fn := n.providersFn
	n.provMu.RUnlock()
	if fn != nil {
		out := make([]llm.Provider, 0, 4)
		for _, p := range fn() {
			if p != nil {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if n.provider != nil {
		return []llm.Provider{n.provider}
	}
	return nil
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
// selection, else "" so whichever brain is being asked picks its own default.
//
// There is deliberately NO env-var override any more. INFINITY_SESSION_NAME_MODEL
// used to sit in this fallback, which meant the answer to "what names my
// sessions" lived in a Railway variable invisible from the code, could name a
// model the chosen brain does not serve, and silently outranked the setting the
// boss actually looks at. The code and his Settings decide.
func (n *Namer) activeModel(ctx context.Context) string {
	n.modelMu.RLock()
	fn := n.modelFn
	n.modelMu.RUnlock()
	if fn != nil {
		if m := strings.TrimSpace(fn(ctx)); m != "" {
			return m
		}
	}
	return ""
}

func NewNamer(pool *pgxpool.Pool, provider llm.Provider) *Namer {
	return &Namer{
		pool:     pool,
		provider: provider,
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
	// Try the brains in order and move on the moment one REFUSES the work.
	//
	// The boss's rule: the ChatGPT subscription first, because a seven-word
	// title on a plan he already pays for is free - and his Settings model
	// only when that plan is down or does not work. "Does not work" is the
	// half that was missing. Falling through was wired for a spent plan and
	// nothing else, so when his account started refusing the model outright
	// (a 400, not a quota error) naming did not move to the next brain: it
	// just failed, every time, for two days.
	//
	// A refusal is any of: out of usage, the model rejected, or the
	// credential dead. None of those say anything about this session, and all
	// of them are answered the same way - ask the next brain.
	brains := n.brains()
	if len(brains) == 0 {
		return "", errors.New("no brain available to draft a session title")
	}
	var lastErr error
	for _, brain := range brains {
		// The Settings model id belongs to the Settings VENDOR. Naming may be
		// running on a different one, and handing "deepseek-v4-pro" to OpenAI
		// is a guaranteed 400. Pass the id only when it belongs to this
		// provider's family; otherwise let the provider use its own default.
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
		if serr == nil {
			if title := cleanTitle(resp.Text); title != "" {
				return title, nil
			}
			lastErr = fmt.Errorf("%s returned an empty title", brain.Name())
			continue
		}
		lastErr = serr
		if refusedTitling(serr) {
			log.Printf("sessions: %s will not title this (%s); trying the next brain",
				brain.Name(), truncate(serr.Error(), 160))
			continue
		}
		// Anything else is a genuine failure of the call rather than the brain
		// declining it. Stop: retrying the same request on another vendor
		// would just spend a second plan on the same problem.
		return "", serr
	}
	return "", lastErr
}

// refusedTitling reports whether a brain declined the work for a reason that
// has nothing to do with this session - so the right answer is the next brain,
// not a failed title. Kept as one predicate so every caller agrees on what
// "this brain will not do it" means.
func refusedTitling(err error) bool {
	if err == nil {
		return false
	}
	if _, isQuota := llm.AsQuota(err); isQuota {
		return true
	}
	return llm.IsUnsupportedModel(err.Error()) || llm.IsAuthFailure(err.Error())
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
