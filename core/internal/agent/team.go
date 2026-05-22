package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/settings"
	itools "github.com/dopesoft/infinity/core/internal/tools"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AgentTeam struct {
	Loop     *Loop
	Pool     *pgxpool.Pool
	Settings *settings.Store
}

func (t *AgentTeam) Name() string { return "agent_team_start" }
func (t *AgentTeam) Description() string {
	return "Start a visible, budget-capped team of specialist agents for complex multi-part work. " +
		"Use for wide tasks that benefit from parallel roles: multimedia packages, competing hypotheses, " +
		"research + synthesis, code + review, artifact generation, or cross-domain workflows. Do not use for simple linear requests."
}
func (t *AgentTeam) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"goal": map[string]any{
				"type":        "string",
				"description": "The overall team objective, written as a self-contained brief.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Why this needs an agent team instead of one normal turn.",
			},
			"members": map[string]any{
				"type":        "array",
				"description": "Specialist members to spawn. Keep roles concrete and non-overlapping.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"role":       map[string]any{"type": "string"},
						"task":       map[string]any{"type": "string"},
						"capability": map[string]any{"type": "string", "description": "general, research, writing, image, code, connector, critic, synthesis, etc."},
						"model":      map[string]any{"type": "string", "description": "Optional model override. Leave empty to use the active chat model."},
					},
					"required": []string{"role", "task"},
				},
			},
			"confirmed": map[string]any{
				"type":        "boolean",
				"description": "Set true only when the boss explicitly approved a team after being asked. Required when Settings says ask first.",
			},
			"max_runtime_seconds": map[string]any{"type": "integer"},
		},
		"required": []string{"goal", "members"},
	}
}

type teamMemberInput struct {
	Role       string `json:"role"`
	Task       string `json:"task"`
	Capability string `json:"capability"`
	Model      string `json:"model"`
}

type teamMemberResult struct {
	ID           string `json:"id"`
	Role         string `json:"role"`
	Task         string `json:"task"`
	Capability   string `json:"capability"`
	SessionID    string `json:"session_id"`
	Status       string `json:"status"`
	Summary      string `json:"summary,omitempty"`
	Error        string `json:"error,omitempty"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

func (t *AgentTeam) Execute(ctx context.Context, input map[string]any) (string, error) {
	if t == nil || t.Loop == nil {
		return "", errors.New("agent_team_start: no agent loop wired")
	}
	cfg := settings.DefaultChatSettings()
	if t.Settings != nil {
		cfg = t.Settings.GetChatSettings(ctx)
	}
	if cfg.AgentTeams == "off" {
		return `{"ok":false,"error":"agent teams are disabled in Settings -> Chat"}`, nil
	}
	confirmed, _ := input["confirmed"].(bool)
	if cfg.AgentTeams == "ask" && !confirmed {
		return `{"ok":false,"needs_confirmation":true,"message":"Settings -> Chat is set to ask before starting agent teams. Ask the boss to confirm this team, then retry with confirmed=true."}`, nil
	}

	goal := strings.TrimSpace(fmt.Sprint(input["goal"]))
	if goal == "" {
		return `{"ok":false,"error":"goal is required"}`, nil
	}
	reason := strings.TrimSpace(fmt.Sprint(input["reason"]))
	members := parseTeamMembers(input["members"])
	if len(members) == 0 {
		return `{"ok":false,"error":"members must include at least one role/task"}`, nil
	}
	if cfg.MaxAgentsPerTeam > 0 && len(members) > cfg.MaxAgentsPerTeam {
		members = members[:cfg.MaxAgentsPerTeam]
	}

	timeout := time.Duration(cfg.MaxRuntimeSeconds) * time.Second
	if v, ok := input["max_runtime_seconds"].(float64); ok && v > 0 {
		req := time.Duration(v) * time.Second
		if req < timeout || timeout <= 0 {
			timeout = req
		}
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}

	parentSessionID := itools.SessionIDFromContext(ctx)
	active := []string(nil)
	if set := itools.ActiveSetFromContext(ctx); set != nil {
		active = set.Names()
	}

	teamID := uuid.NewString()
	if t.Pool != nil {
		settingsJSON, _ := json.Marshal(cfg)
		_, _ = t.Pool.Exec(ctx, `
			INSERT INTO mem_agent_teams (id, parent_session_id, goal, reason, settings, token_budget)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		`, teamID, parentSessionID, goal, reason, string(settingsJSON), cfg.MaxTeamTokens)
		_, _ = t.Pool.Exec(ctx, `
			INSERT INTO mem_agent_team_events (team_id, event_type, message, data)
			VALUES ($1, 'team_started', $2, $3::jsonb)
		`, teamID, fmt.Sprintf("Started %d-agent team", len(members)), mustJSON(map[string]any{"members": members}))
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results := make([]teamMemberResult, len(members))
	var wg sync.WaitGroup
	for i, m := range members {
		wg.Add(1)
		go func(idx int, member teamMemberInput) {
			defer wg.Done()
			results[idx] = t.runMember(runCtx, teamID, goal, reason, member, active, cfg)
		}(i, m)
	}
	wg.Wait()

	status := "ok"
	var inputTokens, outputTokens int
	for _, r := range results {
		inputTokens += r.InputTokens
		outputTokens += r.OutputTokens
		if r.Status != "ok" {
			status = "error"
		}
	}
	if runCtx.Err() == context.DeadlineExceeded {
		status = "error"
	}
	if t.Pool != nil {
		_, _ = t.Pool.Exec(context.Background(), `
			UPDATE mem_agent_teams
			   SET status = $2, ended_at = NOW(), input_tokens = $3, output_tokens = $4,
			       error = CASE WHEN $2 = 'error' THEN 'one or more team members failed or timed out' ELSE NULL END
			 WHERE id = $1
		`, teamID, status, inputTokens, outputTokens)
		_, _ = t.Pool.Exec(context.Background(), `
			INSERT INTO mem_agent_team_events (team_id, event_type, message, data)
			VALUES ($1, 'team_done', $2, $3::jsonb)
		`, teamID, "Team finished", mustJSON(map[string]any{"status": status, "input_tokens": inputTokens, "output_tokens": outputTokens}))
	}

	out, _ := json.Marshal(map[string]any{
		"ok":            status == "ok",
		"team_id":       teamID,
		"status":        status,
		"goal":          goal,
		"reason":        reason,
		"members":       results,
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"message":       "Agent team completed. Synthesize the member artifacts into the final answer instead of dumping raw transcripts.",
	})
	return string(out), nil
}

func (t *AgentTeam) runMember(ctx context.Context, teamID, goal, reason string, member teamMemberInput, active []string, cfg settings.ChatSettings) teamMemberResult {
	memberID := uuid.NewString()
	sessionID := "team:" + teamID + ":" + uuid.NewString()
	result := teamMemberResult{
		ID:         memberID,
		Role:       member.Role,
		Task:       member.Task,
		Capability: member.Capability,
		SessionID:  sessionID,
		Status:     "running",
	}
	if t.Pool != nil {
		_, _ = t.Pool.Exec(ctx, `
			INSERT INTO mem_agent_members (id, team_id, role, task, capability, session_id, model)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		`, memberID, teamID, member.Role, member.Task, member.Capability, sessionID, member.Model)
		_, _ = t.Pool.Exec(ctx, `
			INSERT INTO mem_agent_team_events (team_id, member_id, event_type, message, data)
			VALUES ($1, $2, 'member_spawned', $3, $4::jsonb)
		`, teamID, memberID, member.Role+" spawned", mustJSON(member))
	}

	child := t.Loop.GetOrCreateSession(sessionID)
	defer t.Loop.ClearSession(sessionID)
	if len(active) > 0 {
		child.Active.Replace(active)
	}
	child.SystemPromptOverride = teamMemberPrompt(member, cfg)

	events := make(chan RunEvent, 128)
	var finalText strings.Builder
	var runErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range events {
			switch ev.Kind {
			case EventDelta:
				finalText.WriteString(ev.TextDelta)
			case EventError:
				runErr = errors.New(ev.Error)
			}
		}
	}()

	err := t.Loop.Run(ctx, sessionID, teamMemberUserPrompt(goal, reason, member, cfg), member.Model, nil, events)
	close(events)
	wg.Wait()
	if runErr == nil && err != nil {
		runErr = err
	}

	usage := child.UsageSnapshot()
	result.InputTokens = usage.TotalInputTokens
	result.OutputTokens = usage.TotalOutputTokens
	result.Summary = strings.TrimSpace(finalText.String())
	if runErr != nil {
		result.Status = "error"
		result.Error = runErr.Error()
	} else {
		result.Status = "ok"
	}

	if t.Pool != nil {
		_, _ = t.Pool.Exec(context.Background(), `
			UPDATE mem_agent_members
			   SET status = $2, ended_at = NOW(), input_tokens = $3, output_tokens = $4, error = NULLIF($5, '')
			 WHERE id = $1
		`, memberID, result.Status, result.InputTokens, result.OutputTokens, result.Error)
		_, _ = t.Pool.Exec(context.Background(), `
			INSERT INTO mem_agent_team_artifacts (team_id, member_id, kind, title, body, data)
			VALUES ($1, $2, 'member_summary', $3, $4, $5::jsonb)
		`, teamID, memberID, member.Role+" summary", result.Summary, mustJSON(map[string]any{
			"status":        result.Status,
			"capability":    result.Capability,
			"input_tokens":  result.InputTokens,
			"output_tokens": result.OutputTokens,
			"error":         result.Error,
		}))
		_, _ = t.Pool.Exec(context.Background(), `
			INSERT INTO mem_agent_team_events (team_id, member_id, event_type, message, data)
			VALUES ($1, $2, 'member_done', $3, $4::jsonb)
		`, teamID, memberID, member.Role+" finished", mustJSON(result))
	}
	return result
}

func parseTeamMembers(raw any) []teamMemberInput {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]teamMemberInput, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role := strings.TrimSpace(fmt.Sprint(m["role"]))
		task := strings.TrimSpace(fmt.Sprint(m["task"]))
		if role == "" || task == "" {
			continue
		}
		capability := strings.TrimSpace(fmt.Sprint(m["capability"]))
		if capability == "" || capability == "<nil>" {
			capability = "general"
		}
		model := strings.TrimSpace(fmt.Sprint(m["model"]))
		if model == "<nil>" || model == "null" {
			model = ""
		}
		out = append(out, teamMemberInput{Role: role, Task: task, Capability: capability, Model: model})
	}
	return out
}

func teamMemberPrompt(member teamMemberInput, cfg settings.ChatSettings) string {
	return fmt.Sprintf(`You are a specialist worker agent in an Infinity agent team.

Role: %s
Capability: %s

You have the same Infinity substrate as the lead: tools, skills, memory, connectors, and artifacts exposed through the active toolset. Use them when they move your assigned task forward.

Rules:
- Work only on your assigned task. Do not try to lead the whole team.
- Produce concrete artifacts: script sections, image prompts, research findings, code notes, QA findings, or whatever your role requires.
- Be direct about uncertainty and blockers.
- Keep token use under control. The team budget is %d tokens and %d tool calls.
- Your final answer is your handoff artifact for the lead. Make it structured and concise.
`, member.Role, member.Capability, cfg.MaxTeamTokens, cfg.MaxToolCalls)
}

func teamMemberUserPrompt(goal, reason string, member teamMemberInput, cfg settings.ChatSettings) string {
	return fmt.Sprintf(`Team goal:
%s

Why a team was started:
%s

Your role:
%s

Your task:
%s

Capability lane:
%s

Return your final handoff with:
1. Deliverables
2. Key decisions
3. Artifacts/prompts/assets produced
4. Open risks or follow-up needed
`, goal, reason, member.Role, member.Task, member.Capability)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

var _ itools.Tool = (*AgentTeam)(nil)
