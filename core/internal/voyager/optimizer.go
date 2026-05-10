package voyager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Optimizer wraps the GEPA sidecar (docker/gepa.Dockerfile). Pulls failure
// traces from mem_skill_runs for a target skill, asks GEPA to evolve the
// SKILL.md, and writes the winning candidate back into mem_skill_proposals
// so the boss can promote it via the Trust queue (existing /decide path).
//
// Wire when GEPA_URL is set; otherwise Enabled() returns false and the
// HTTP /optimize handler returns 503.
type Optimizer struct {
	url        string
	apiKey     string // optional bearer for the sidecar's reverse proxy
	httpClient *http.Client
}

// MaxSkillSizeBytes is the hard cap on a candidate SKILL.md, enforced before
// the proposal is persisted. Mirrors Hermes's 15KB gate.
const MaxSkillSizeBytes = 15 * 1024

func NewOptimizer() *Optimizer {
	url := strings.TrimRight(strings.TrimSpace(os.Getenv("GEPA_URL")), "/")
	if url == "" {
		return &Optimizer{}
	}
	return &Optimizer{
		url:        url,
		apiKey:     strings.TrimSpace(os.Getenv("GEPA_API_KEY")),
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (o *Optimizer) Enabled() bool {
	return o != nil && o.url != ""
}

// optimizeReq mirrors the FastAPI shape on the sidecar.
type optimizeReq struct {
	SkillName string         `json:"skill_name"`
	SkillMD   string         `json:"skill_md"`
	Traces    []traceItem    `json:"traces"`
	EvalSet   []evalCase     `json:"eval_set"`
	Budget    optimizeBudget `json:"budget"`
	Model     string         `json:"model,omitempty"`
}

type traceItem struct {
	Input   any    `json:"input"`
	Output  string `json:"output"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type evalCase struct {
	Input    any `json:"input"`
	Expected any `json:"expected"`
}

type optimizeBudget struct {
	MaxCandidates int `json:"max_candidates"`
	MaxCalls      int `json:"max_calls"`
}

type optimizeResp struct {
	Candidates []candidate `json:"candidates"`
	Model      string      `json:"model"`
	Calls      int         `json:"calls"`
	ElapsedMS  int         `json:"elapsed_ms"`
}

type candidate struct {
	SkillMD   string  `json:"skill_md"`
	Score     float64 `json:"score"`
	SizeChars int     `json:"size_chars"`
	Rationale string  `json:"rationale"`
}

// Run pulls recent failure traces for the named skill and ships them to
// GEPA. Returns the winning candidate after applying hard gates (size cap,
// non-empty, frontmatter present). The caller persists it as a proposal.
func (m *Manager) RunOptimizer(ctx context.Context, opt *Optimizer, skillName string, traceLimit int) (*candidate, int, error) {
	if !opt.Enabled() {
		return nil, 0, errors.New("voyager: GEPA optimizer not configured (set GEPA_URL)")
	}
	if m == nil || m.skillsReg == nil {
		return nil, 0, errors.New("voyager: no skills registry")
	}
	skill, ok := m.skillsReg.Get(skillName)
	if !ok {
		return nil, 0, fmt.Errorf("voyager: unknown skill %q", skillName)
	}
	skillMD, err := readSkillMD(skill.Path)
	if err != nil {
		return nil, 0, fmt.Errorf("read skill md: %w", err)
	}
	if traceLimit <= 0 || traceLimit > 50 {
		traceLimit = 20
	}
	traces, err := m.recentSkillTraces(ctx, skillName, traceLimit)
	if err != nil {
		return nil, 0, fmt.Errorf("load traces: %w", err)
	}

	body, _ := json.Marshal(optimizeReq{
		SkillName: skillName,
		SkillMD:   skillMD,
		Traces:    traces,
		Budget:    optimizeBudget{MaxCandidates: 6, MaxCalls: 24},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opt.url+"/optimize", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if opt.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+opt.apiKey)
	}
	resp, err := opt.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, 0, fmt.Errorf("gepa %d: %s", resp.StatusCode, string(raw))
	}
	var out optimizeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, err
	}
	winner := pickWinner(out.Candidates, skillMD)
	if winner == nil {
		return nil, out.Calls, errors.New("no candidate passed the hard gates")
	}
	// Persist as a proposal so the existing /api/voyager/proposals/:id/decide
	// path can promote it. Reuse the table — the source distinguishes from
	// session-derived candidates.
	proposalID, perr := m.insertOptimizationProposal(ctx, skillName, skill.Version, winner)
	if perr == nil {
		// Surface the proposal ID via the rationale so the API caller knows
		// where to find it.
		winner.Rationale = fmt.Sprintf("[proposal=%s] %s", proposalID, winner.Rationale)
	}
	return winner, out.Calls, perr
}

// pickWinner applies the hard gates Hermes uses. Returns the highest-scoring
// candidate that:
//   - is non-empty after trimming
//   - is ≤ MaxSkillSizeBytes
//   - still starts with YAML frontmatter ("---")
//   - is not byte-identical to the original (no-op rejection)
func pickWinner(cands []candidate, original string) *candidate {
	for i := range cands {
		c := &cands[i]
		md := strings.TrimSpace(c.SkillMD)
		if md == "" {
			continue
		}
		if len(md) > MaxSkillSizeBytes {
			continue
		}
		if !strings.HasPrefix(md, "---") {
			continue
		}
		if md == strings.TrimSpace(original) {
			continue
		}
		c.SkillMD = md
		c.SizeChars = len(md)
		return c
	}
	return nil
}

// recentSkillTraces pulls recent runs for a skill out of mem_skill_runs.
// Used as the trace input to GEPA. We grab successes too — they are the
// implicit eval set ("the prompt worked here, don't break it").
func (m *Manager) recentSkillTraces(ctx context.Context, skillName string, limit int) ([]traceItem, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT COALESCE(input::text, '{}'),
		       COALESCE(output, ''),
		       success
		  FROM mem_skill_runs
		 WHERE skill_name = $1
		 ORDER BY started_at DESC
		 LIMIT $2
	`, skillName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]traceItem, 0, limit)
	for rows.Next() {
		var in, output string
		var success bool
		if err := rows.Scan(&in, &output, &success); err != nil {
			return nil, err
		}
		var anyIn any
		if json.Unmarshal([]byte(in), &anyIn) != nil {
			anyIn = in
		}
		// Treat the output as the failure message when the run failed —
		// mem_skill_runs has no separate stderr column, so output carries
		// either the produced result or the error string the runner wrote.
		errMsg := ""
		if !success {
			errMsg = output
		}
		out = append(out, traceItem{
			Input:   anyIn,
			Output:  output,
			Success: success,
			Error:   errMsg,
		})
	}
	return out, rows.Err()
}

func (m *Manager) insertOptimizationProposal(ctx context.Context, skillName, version string, c *candidate) (string, error) {
	if m == nil || m.pool == nil {
		return "", errors.New("voyager: no db pool")
	}
	var id string
	err := m.pool.QueryRow(ctx, `
		INSERT INTO mem_skill_proposals
		  (name, description, reasoning, skill_md, risk_level, test_pass_rate,
		   status, parent_skill, parent_version)
		VALUES ($1, $2, $3, $4, 'medium', $5, 'candidate', $1, NULLIF($6, ''))
		RETURNING id::text
	`,
		skillName,
		fmt.Sprintf("GEPA-evolved variant of %s", skillName),
		c.Rationale,
		c.SkillMD,
		c.Score,
		version,
	).Scan(&id)
	return id, err
}

func readSkillMD(skillDir string) (string, error) {
	if skillDir == "" {
		return "", errors.New("skill has no on-disk path")
	}
	b, err := os.ReadFile(strings.TrimRight(skillDir, "/") + "/SKILL.md")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
