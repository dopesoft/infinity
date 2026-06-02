package sentinel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
)

// agentTurnTimeout bounds a sentinel-fired agent turn. Matches cron's agent
// default; sentinels fire rarely (cooldown-gated) so a generous ceiling is fine.
const agentTurnTimeout = 30 * time.Minute

// LogDispatcher is a no-op dispatcher used as a fallback. It simply prints
// the action chain to stderr. Swap with a richer dispatcher (skills runner /
// memory store / notifier) when those integrations land.
type LogDispatcher struct{}

func (LogDispatcher) Dispatch(ctx context.Context, s Sentinel, payload map[string]any) error {
	var actions []Action
	_ = json.Unmarshal(s.ActionChain, &actions)
	pj, _ := json.Marshal(payload)
	fmt.Printf("sentinel.fire name=%q type=%s actions=%d payload=%s\n",
		s.Name, s.WatchType, len(actions), string(pj))
	return nil
}

// SkillDispatcher executes a sentinel's action chain. Two action kinds do real
// work; anything else is best-effort logged. serve.go wires this up when the
// skills runner + agent loop are available.
//
//   - kind "agent_turn": runs args["prompt"] through a fresh agent turn (the
//     same loop crons use). This is the GENERIC path — the agent has every
//     tool (notify, surface_item, skills_invoke, …), so a sentinel can do
//     ANYTHING in natural language: "Notify the boss that X", "Run the
//     inbox-triage skill", "Draft a summary and save it". It also correctly
//     runs instruction-only (recipe) skills, which the direct "skill" path
//     below cannot (Runner.Invoke only executes code skills).
//   - kind "skill": invokes an EXECUTABLE skill directly via the runner. Kept
//     for power users / code skills; the convenience tool path prefers
//     agent_turn because it works for every skill type.
type SkillDispatcher struct {
	Inner   Dispatcher // optional fallback (LogDispatcher)
	Invoker SkillInvoker
	Agent   AgentRunner // optional; enables kind "agent_turn"
}

type SkillInvoker interface {
	InvokeSkill(ctx context.Context, name string, args map[string]any) (string, error)
}

// AgentRunner runs a single agent turn for a prompt. The dispatcher hands it a
// fresh session id per fire (an isolated turn, like cron's
// isolated_agent_turn). The serve.go impl wraps *agent.Loop.
type AgentRunner interface {
	RunAgentTurn(ctx context.Context, sessionID, prompt string) error
}

func (d SkillDispatcher) Dispatch(ctx context.Context, s Sentinel, payload map[string]any) error {
	var actions []Action
	if err := json.Unmarshal(s.ActionChain, &actions); err != nil {
		return fmt.Errorf("decode action_chain: %w", err)
	}
	for _, a := range actions {
		switch a.Kind {
		case "agent_turn":
			prompt, _ := a.Args["prompt"].(string)
			if prompt == "" {
				return fmt.Errorf("agent_turn action missing prompt")
			}
			// Hand the agent the event detail so it can act on what actually
			// tripped (the unread count, the changed file, the goal, …).
			if len(payload) > 0 {
				if pj, err := json.Marshal(payload); err == nil {
					prompt += "\n\nTrigger detail (the event that fired this sentinel): " + string(pj)
				}
			}
			if d.Agent == nil {
				if d.Inner != nil {
					return d.Inner.Dispatch(ctx, s, payload)
				}
				return fmt.Errorf("agent_turn action but no agent runner configured")
			}
			// Run the agent turn DETACHED, for two reasons:
			//   1. The poll loop (runtimes.go) calls Dispatch synchronously for
			//      every sentinel each tick — a multi-minute turn would stall
			//      every other watcher. Returning immediately keeps the poller
			//      cheap, and lets Trigger stamp the cooldown right away so the
			//      next tick doesn't re-fire the same turn.
			//   2. The webhook path passes the HTTP request ctx, which is
			//      cancelled the moment the handler responds — a synchronous
			//      turn on that ctx would be killed mid-thought. A fresh
			//      Background ctx (with its own timeout) outlives both.
			// Fresh session id per fire = an isolated turn (nothing leaks
			// between firings or into the boss's live chat).
			runner, name := d.Agent, s.Name
			sessionID := uuid.NewString()
			go func() {
				bg, cancel := context.WithTimeout(context.Background(), agentTurnTimeout)
				defer cancel()
				if err := runner.RunAgentTurn(bg, sessionID, prompt); err != nil {
					fmt.Fprintf(os.Stderr, "sentinel %q agent_turn: %v\n", name, err)
				}
			}()
		case "skill":
			name, _ := a.Args["name"].(string)
			args, _ := a.Args["args"].(map[string]any)
			if args == nil {
				args = map[string]any{}
			}
			args["__sentinel_payload"] = payload
			if d.Invoker != nil {
				if _, err := d.Invoker.InvokeSkill(ctx, name, args); err != nil {
					return fmt.Errorf("skill %s: %w", name, err)
				}
			}
		default:
			// Fall back to logging; future kinds plug in here.
			if d.Inner != nil {
				if err := d.Inner.Dispatch(ctx, s, payload); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
