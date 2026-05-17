package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dopesoft/infinity/core/internal/proactive"
	"github.com/dopesoft/infinity/core/internal/skills"
)

type skillRiskGate struct {
	trust *proactive.TrustStore
}

func (g skillRiskGate) AuthorizeCriticalSkill(ctx context.Context, sessionID string, skill *skills.Skill, args map[string]any) (bool, string) {
	if g.trust == nil || skill == nil {
		return false, "critical skill requires Trust approval but Trust is unavailable"
	}
	id, err := g.trust.Queue(ctx, &proactive.TrustContract{
		Title:     "Run critical skill " + skill.Name,
		RiskLevel: "critical",
		Source:    "skill_runner",
		ActionSpec: map[string]any{
			"tool":           "skills_invoke",
			"session_id":     sessionID,
			"skill":          skill.Name,
			"args":           args,
			"network_egress": skill.NetworkEgress,
			"impl_path":      skill.ImplPath,
		},
		Reasoning: "Critical-risk executable skills are gated before container execution.",
		Preview:   fmt.Sprintf("Run %s in the container sandbox", skill.Name),
	})
	if err != nil {
		return false, "could not queue Trust contract for critical skill: " + err.Error()
	}
	if id == "" {
		return false, "Trust queue unavailable; critical skill was not run"
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return false, "critical skill timed out waiting for Trust approval"
		case <-ticker.C:
			status, _, toolName, err := g.trust.LookupForGate(waitCtx, id)
			if err != nil {
				continue
			}
			switch status {
			case "approved":
				if toolName != "" {
					_, _ = g.trust.ConsumeApprovedForTool(waitCtx, sessionID, toolName)
				}
				return true, "approved"
			case "denied", "rejected":
				return false, "critical skill denied in Trust"
			case "snoozed":
				return false, "critical skill snoozed in Trust"
			}
		}
	}
}
