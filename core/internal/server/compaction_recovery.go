package server

import (
	"context"
	"fmt"
	"strings"
)

func (s *Server) buildRecoveryPrompt(ctx context.Context, sessionID, userMsg string) (string, bool) {
	if !wantsRecovery(userMsg) {
		return userMsg, false
	}
	var parts []string
	if s.wal != nil {
		if body, _ := s.wal.Read(ctx, sessionID); strings.TrimSpace(body) != "" {
			parts = append(parts, "SESSION STATE\n"+strings.TrimSpace(body))
		}
	}
	if s.buffer != nil {
		if body, _ := s.buffer.Read(ctx, sessionID); strings.TrimSpace(body) != "" {
			parts = append(parts, "WORKING BUFFER\n"+strings.TrimSpace(body))
		}
	}
	if recent := s.recentTurnSummaries(ctx, sessionID, 5); recent != "" {
		parts = append(parts, "RECENT TURNS\n"+recent)
	}
	if len(parts) == 0 {
		return userMsg, false
	}
	var b strings.Builder
	b.WriteString("<compaction_recovery>\n")
	b.WriteString("The boss asked to resume context. Use this recovered state before answering. ")
	b.WriteString("Briefly acknowledge that you recovered context, then continue the work.\n\n")
	b.WriteString(strings.Join(parts, "\n\n"))
	b.WriteString("\n</compaction_recovery>\n\n")
	b.WriteString(userMsg)
	return b.String(), true
}

func wantsRecovery(s string) bool {
	x := strings.ToLower(strings.TrimSpace(s))
	if strings.Contains(x, "<summary>") || strings.Contains(x, "<compaction") {
		return true
	}
	needles := []string{
		"where were we",
		"continue from last time",
		"resume this",
		"resume from last time",
		"pick up where we left off",
	}
	for _, n := range needles {
		if strings.Contains(x, n) {
			return true
		}
	}
	return false
}

func (s *Server) recentTurnSummaries(ctx context.Context, sessionID string, limit int) string {
	if s == nil || s.pool == nil || sessionID == "" {
		return ""
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(summary, ''), COALESCE(user_text, ''), COALESCE(assistant_text, '')
		  FROM mem_turns
		 WHERE session_id = $1::uuid
		 ORDER BY started_at DESC
		 LIMIT $2
	`, sessionID, limit)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var summary, user, assistant string
		if err := rows.Scan(&summary, &user, &assistant); err != nil {
			continue
		}
		text := strings.TrimSpace(summary)
		if text == "" {
			text = strings.TrimSpace(user)
		}
		if text == "" {
			text = strings.TrimSpace(assistant)
		}
		if text != "" {
			lines = append(lines, "- "+clipRecovery(text, 240))
		}
	}
	// Query was newest-first; that is fine for a compact recovery cue.
	return strings.Join(lines, "\n")
}

func clipRecovery(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return fmt.Sprintf("%s...", s[:n])
}
