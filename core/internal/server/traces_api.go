package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/jackc/pgx/v5"
)

// turnRowDTO is the wire shape for one /logs row. Maps memory.TurnRow but
// gives JSON field names the Studio client can consume directly.
type turnRowDTO struct {
	ID            string `json:"id"`
	SessionID     string `json:"session_id"`
	SessionName   string `json:"session_name,omitempty"`
	UserText      string `json:"user_text"`
	AssistantText string `json:"assistant_text,omitempty"`
	Model         string `json:"model,omitempty"`
	Status        string `json:"status"`
	StopReason    string `json:"stop_reason,omitempty"`
	Summary       string `json:"summary,omitempty"`
	Error         string `json:"error,omitempty"`
	StartedAt     string `json:"started_at"`
	EndedAt       string `json:"ended_at,omitempty"`
	InputTokens   int    `json:"input_tokens"`
	OutputTokens  int    `json:"output_tokens"`
	ToolCallCount int    `json:"tool_call_count"`
	LatencyMS     int64  `json:"latency_ms"`
}

type traceDetailDTO struct {
	Turn   turnRowDTO           `json:"turn"`
	Events []memory.TraceEvent `json:"events"`
}

func toRowDTO(r memory.TurnRow) turnRowDTO {
	return turnRowDTO{
		ID:            r.ID,
		SessionID:     r.SessionID,
		SessionName:   r.SessionName,
		UserText:      r.UserText,
		AssistantText: r.AssistantText,
		Model:         r.Model,
		Status:        r.Status,
		StopReason:    r.StopReason,
		Summary:       r.Summary,
		Error:         r.Error,
		StartedAt:     r.StartedAt,
		EndedAt:       r.EndedAt,
		InputTokens:   r.InputTokens,
		OutputTokens:  r.OutputTokens,
		ToolCallCount: r.ToolCallCount,
		LatencyMS:     r.LatencyMS,
	}
}

// handleTracesList serves GET /api/traces — list of recent turns, filtered
// optionally by session and/or status. Newest first.
func (s *Server) handleTracesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.turnStore == nil {
		writeJSON(w, http.StatusOK, []turnRowDTO{})
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	rows, err := s.turnStore.List(r.Context(), sessionID, status, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]turnRowDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toRowDTO(r))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTraceDetail serves GET /api/traces/{turn_id} — one turn plus its
// merged event timeline (observations + predictions + trust contracts).
func (s *Server) handleTraceDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.turnStore == nil {
		http.Error(w, "traces unavailable", http.StatusServiceUnavailable)
		return
	}
	// Path: /api/traces/<turn_id>
	turnID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/traces/"))
	if turnID == "" {
		http.Error(w, "turn id required", http.StatusBadRequest)
		return
	}
	row, err := s.turnStore.Get(r.Context(), turnID)
	if err != nil {
		if err == pgx.ErrNoRows {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	events, err := s.turnStore.Events(r.Context(), turnID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []memory.TraceEvent{}
	}
	writeJSON(w, http.StatusOK, traceDetailDTO{Turn: toRowDTO(row), Events: events})
}
