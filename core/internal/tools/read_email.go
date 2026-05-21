package tools

import (
	"context"
	"fmt"
	"strings"
)

// ReadEmail lets the agent pull the FULL text of a follow-up email on demand -
// e.g. when the boss mentions an email in chat and the summary in context
// isn't enough. The resolve+fetch logic lives in dashboard.API.FullEmailText,
// injected here as Fetch so the tools package stays decoupled from the
// dashboard/connectors packages.
type ReadEmail struct {
	Fetch func(ctx context.Context, id, origin string) (string, error)
}

func (t *ReadEmail) Name() string { return "read_email" }

func (t *ReadEmail) Description() string {
	return "Read the full body of a follow-up email by its dashboard id. Use when you need the entire message rather than the summary you were given. Pass the follow-up id and its origin ('followup' for connector-poll rows, 'surface' for agent-surfaced rows; defaults to 'followup')."
}

func (t *ReadEmail) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "The follow-up's dashboard id (a UUID).",
			},
			"origin": map[string]any{
				"type":        "string",
				"enum":        []string{"followup", "surface"},
				"description": "Which store the row came from. Default 'followup'.",
			},
		},
		"required": []string{"id"},
	}
}

// ReadOnly: fetching an email body is a pure read.
func (t *ReadEmail) ReadOnly() bool { return true }

func (t *ReadEmail) Execute(ctx context.Context, input map[string]any) (string, error) {
	if t.Fetch == nil {
		return "", fmt.Errorf("read_email: not configured (no dashboard fetcher)")
	}
	id := strings.TrimSpace(strString(input, "id"))
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	origin := strings.TrimSpace(strString(input, "origin"))
	body, err := t.Fetch(ctx, id, origin)
	if err != nil {
		return "", fmt.Errorf("read_email: %w", err)
	}
	if strings.TrimSpace(body) == "" {
		return "No readable email body found for that item (it may be attachment-only or already dismissed).", nil
	}
	return body, nil
}
