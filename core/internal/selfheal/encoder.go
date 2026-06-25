// Package selfheal closes the fail → fix → LEARN → guard loop.
//
// The reactive self-heal in the agent loop (core/internal/agent/selfheal.go)
// makes Jarvis fix + verify a failure inside the same turn instead of punting
// to the boss. But fixing once and forgetting is the chatbot failure mode — the
// same class of failure recurs next week. This package is the missing half:
// when a self-heal RESOLVES (or EXHAUSTS on) a failure, it deterministically
//
//  1. writes the boss-facing RECEIPT — a surface item carrying the first-person
//     post-mortem (what happened / what I tried / what happened then / what I
//     did / how I fixed it / what I learned), so every self-heal is visible;
//  2. writes the durable GUARD — a procedural memory injected into every future
//     turn via RRF, so the lesson informs the agent's independence and the same
//     problem can't recur silently.
//
// Both are guaranteed HERE in code, fired off the SelfHealResolved /
// SelfHealExhausted hooks — they do NOT depend on the runtime LLM "remembering"
// to write a memory, which it routinely drops (CLAUDE.md Rule #1b: mechanics
// live in code, judgment lives in prose).
package selfheal

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/hooks"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Encoder is the SelfHealResolved / SelfHealExhausted listener. Nil-safe and
// best-effort throughout: encoding a lesson must never block or fail the turn.
type Encoder struct {
	pool       *pgxpool.Pool
	surface    *surface.Store
	procedural *memory.ProceduralStore
	drafter    llm.Drafter // optional; nil → deterministic post-mortem
	model      string
	log        *log.Logger
}

// NewEncoder builds the listener. drafter may be nil (a deterministic
// post-mortem is assembled from the recorded evidence instead).
func NewEncoder(pool *pgxpool.Pool, procedural *memory.ProceduralStore, drafter llm.Drafter, model string) *Encoder {
	if pool == nil {
		return nil
	}
	return &Encoder{
		pool:       pool,
		surface:    surface.NewStore(pool, slog.Default()),
		procedural: procedural,
		drafter:    drafter,
		model:      model,
		log:        log.New(os.Stdout, "", log.LstdFlags),
	}
}

// OnSelfHealEvent handles both SelfHealResolved and SelfHealExhausted. Register:
//
//	pipeline.RegisterFunc("selfheal.encode", enc.OnSelfHealEvent, hooks.SelfHealResolved, hooks.SelfHealExhausted)
func (e *Encoder) OnSelfHealEvent(ctx context.Context, ev hooks.Event) error {
	if e == nil || e.pool == nil || ev.SessionID == "" {
		return nil
	}
	resolved := ev.Name == hooks.SelfHealResolved
	if v, ok := ev.Payload["resolved"].(bool); ok {
		resolved = v
	}

	evi := e.collectEvidence(ctx, ev.SessionID)
	pm := e.buildPostmortem(ctx, resolved, strings.TrimSpace(ev.Text), evi)

	// 1) Durable guard — the "never recurs / informs future independence"
	// encode. Written for resolved AND exhausted heals: even a fix the agent
	// couldn't fully land teaches what to watch for next time.
	if e.procedural != nil && strings.TrimSpace(pm.GuardLesson) != "" {
		title := "guard: " + truncate(strings.TrimSpace(pm.GuardTitle), 80)
		if _, err := e.procedural.UpsertDismissalProcedural(ctx, title, pm.GuardLesson, 8); err != nil {
			e.log.Printf("[selfheal] guard upsert: %v", err)
		}
	}

	// 2) Receipt — the visible post-mortem for THIS issue.
	e.surfaceReceipt(ctx, ev, resolved, pm)
	return nil
}

// evidence is the recorded signal from the turn's observations that grounds the
// post-mortem — never fabricated.
type evidence struct {
	transcript  string
	editedFiles []string
	structural  bool // the agent edited its own source during the heal
	failures    []string
}

func (e *Encoder) collectEvidence(ctx context.Context, sessionID string) evidence {
	var evi evidence
	rows, err := e.pool.Query(ctx, `
		SELECT hook_name, COALESCE(raw_text,''), payload
		  FROM mem_observations
		 WHERE session_id = $1
		 ORDER BY created_at DESC
		 LIMIT 60
	`, sessionID)
	if err != nil {
		return evi
	}
	defer rows.Close()

	seen := map[string]bool{}
	var lines []string // newest-first while scanning; reversed below
	for rows.Next() {
		var hook, raw string
		var payload []byte
		if err := rows.Scan(&hook, &raw, &payload); err != nil {
			continue
		}
		switch hook {
		case "PreToolUse":
			name, input := parseTool(payload)
			if isCodeEdit(name) {
				if p := filePath(input); p != "" && !seen[p] {
					seen[p] = true
					evi.editedFiles = append(evi.editedFiles, p)
					evi.structural = true
				}
			}
		case "PostToolUseFailure":
			if raw != "" {
				evi.failures = append(evi.failures, truncate(raw, 200))
			}
		}
		if raw != "" && len(lines) < 24 {
			lines = append(lines, hook+": "+truncate(raw, 240))
		}
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	evi.transcript = strings.Join(lines, "\n")
	return evi
}

// postmortem is the structured output the receipt + guard are built from.
type postmortem struct {
	Narrative   string `json:"narrative"`    // first-person what/tried/result/fix/learned
	GuardTitle  string `json:"guard_title"`  // short imperative for the procedural row
	GuardLesson string `json:"guard_lesson"` // the durable rule + the thing to watch for
}

const postmortemSystem = `You are Jarvis writing a short, honest, FIRST-PERSON post-mortem of a problem you just hit and worked on, the way you'd tell the boss right after — warm and plain, never a status-code dump.

You get the tail of the session transcript (tools you ran, errors, your edits) and whether you RESOLVED it or RAN OUT of attempts.

Return ONLY this JSON, no fences:
{
  "narrative": "4-6 sentences covering, in order: what I was doing, what broke (name the real thing in plain words), what I tried, what happened then, what I did that worked (or where I'm stuck), and what I learned. If I RAN OUT, end by naming exactly what I need from the boss or the options.",
  "guard_title": "<=70 chars imperative — the rule to remember ('Finalize cron runs on a fresh context')",
  "guard_lesson": "1-3 sentences: the durable rule AND the thing known to go haywire to guard against next time. Concrete and named. This becomes a memory you read on every future turn, so write it as advice to your future self."
}

Anchor every claim to the transcript. Never invent a failure. Keep it tight.`

func (e *Encoder) buildPostmortem(ctx context.Context, resolved bool, finalReply string, evi evidence) postmortem {
	// LLM path: a real first-person narrative grounded in the transcript.
	if e.drafter != nil && strings.TrimSpace(evi.transcript) != "" {
		outcome := "RESOLVED"
		if !resolved {
			outcome = "RAN OUT of attempts (still unresolved)"
		}
		user := fmt.Sprintf("Outcome: %s\n\nMy final words this turn:\n%s\n\nSession tail:\n%s",
			outcome, truncate(finalReply, 800), truncate(evi.transcript, 6000))
		if len(evi.editedFiles) > 0 {
			user += "\n\nFiles I edited: " + strings.Join(evi.editedFiles, ", ")
		}
		raw, err := e.drafter.Draft(ctx, e.model, postmortemSystem, user, 700)
		if err == nil {
			if pm, ok := parsePostmortem(raw); ok {
				return pm
			}
		} else {
			e.log.Printf("[selfheal] postmortem draft: %v", err)
		}
	}
	// Deterministic fallback — honest, just less polished.
	return deterministicPostmortem(resolved, finalReply, evi)
}

func deterministicPostmortem(resolved bool, finalReply string, evi evidence) postmortem {
	var b strings.Builder
	if len(evi.failures) > 0 {
		fmt.Fprintf(&b, "I hit a problem and worked it rather than handing it back: %s. ", firstLine(evi.failures[0]))
	} else {
		b.WriteString("I caught a failure mid-turn and worked it rather than handing it back. ")
	}
	if evi.structural {
		fmt.Fprintf(&b, "I dug in with my own tools and changed my source (%s). ", strings.Join(evi.editedFiles, ", "))
	} else {
		b.WriteString("I diagnosed it and tried a different approach with my own tools. ")
	}
	guardTitle := "Watch the failure I just healed"
	if resolved {
		b.WriteString("I verified the fix worked before moving on.")
		if len(evi.editedFiles) > 0 {
			guardTitle = "Guard the fix in " + firstLine(evi.editedFiles[0])
		}
	} else {
		b.WriteString("I couldn't fully close it this turn — I need your call on how to proceed, or which option to take.")
		guardTitle = "Unresolved: needs a decision"
	}
	lesson := "When this failure shape shows up again, go straight to the fix I just used instead of rediscovering it."
	if evi.structural && len(evi.editedFiles) > 0 {
		lesson = "When touching " + strings.Join(evi.editedFiles, ", ") + ", remember this failure mode and apply the same fix; watch for it before it bites again."
	}
	return postmortem{Narrative: strings.TrimSpace(b.String()), GuardTitle: guardTitle, GuardLesson: lesson}
}

func (e *Encoder) surfaceReceipt(ctx context.Context, ev hooks.Event, resolved bool, pm postmortem) {
	if e.surface == nil {
		return
	}
	title := "Self-heal — fixed it myself"
	imp := 28
	var actions []surface.Action
	var expires *time.Time
	if resolved {
		// Informational: self-clears after 36h like the cron outcome FYIs.
		exp := time.Now().UTC().Add(36 * time.Hour)
		expires = &exp
	} else {
		// Couldn't quite land it → reads as "needs you", with a one-tap retry.
		title = "Self-heal — I need your call"
		imp = 70
		actions = []surface.Action{{
			ID:     "retry",
			Label:  "Have another go",
			Intent: "Re-attempt the fix you were working on in the last turn, then tell me what happened.",
			Style:  "primary",
		}}
	}
	body := strings.TrimSpace(pm.Narrative)
	item := &surface.Item{
		Surface:          "runs",
		Kind:             "self_heal",
		Source:           "selfheal",
		ExternalID:       "selfheal:" + turnID(ev),
		Title:            title,
		Body:             body,
		Importance:       &imp,
		ImportanceReason: firstLine(body),
		Metadata: map[string]any{
			"session_id": ev.SessionID,
			"resolved":   resolved,
			"guard":      pm.GuardTitle,
		},
		Status:    surface.StatusOpen,
		Actions:   actions,
		ExpiresAt: expires,
		// New information every time; a dismissed prior receipt must not swallow
		// the next one.
		Reopen: true,
	}
	if _, err := e.surface.Upsert(ctx, item); err != nil {
		e.log.Printf("[selfheal] surface receipt: %v", err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

func parsePostmortem(raw string) (postmortem, bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if s == "" {
		return postmortem{}, false
	}
	var pm postmortem
	if err := json.Unmarshal([]byte(s), &pm); err != nil {
		return postmortem{}, false
	}
	if strings.TrimSpace(pm.Narrative) == "" {
		return postmortem{}, false
	}
	return pm, true
}

func parseTool(payload []byte) (string, map[string]any) {
	if len(payload) == 0 {
		return "", nil
	}
	var p map[string]any
	if json.Unmarshal(payload, &p) != nil {
		return "", nil
	}
	name, _ := p["name"].(string)
	input, _ := p["input"].(map[string]any)
	return name, input
}

func isCodeEdit(name string) bool {
	n := strings.ToLower(name)
	return n == "claude_code__edit" || n == "claude_code__write"
}

func filePath(input map[string]any) string {
	if input == nil {
		return ""
	}
	for _, k := range []string{"file_path", "path", "filename"} {
		if v, ok := input[k].(string); ok {
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func turnID(ev hooks.Event) string {
	if ev.Payload != nil {
		if t, ok := ev.Payload["turn_id"].(string); ok && t != "" {
			return t
		}
	}
	return ev.SessionID
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
