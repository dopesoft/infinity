package sentinel

import (
	"context"
	"encoding/json"
	"fmt"
)

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

// SkillDispatcher invokes a skill from the action chain when kind == "skill".
// Other kinds are best-effort logged. This is what serve.go wires up when
// the skills runner is available.
type SkillDispatcher struct {
	Inner   Dispatcher // optional fallback (LogDispatcher)
	Invoker SkillInvoker
}

type SkillInvoker interface {
	InvokeSkill(ctx context.Context, name string, args map[string]any) (string, error)
}

func (d SkillDispatcher) Dispatch(ctx context.Context, s Sentinel, payload map[string]any) error {
	var actions []Action
	if err := json.Unmarshal(s.ActionChain, &actions); err != nil {
		return fmt.Errorf("decode action_chain: %w", err)
	}
	for _, a := range actions {
		switch a.Kind {
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
