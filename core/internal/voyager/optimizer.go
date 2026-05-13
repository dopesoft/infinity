package voyager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Optimizer wraps the GEPA sidecar (docker/gepa.Dockerfile). Pulls failure
// traces from mem_skill_runs for a target skill, asks GEPA to evolve the
// SKILL.md, and writes the WHOLE Pareto frontier back into mem_skill_proposals
// so the boss can promote any candidate via the Trust queue.
//
// Per GEPA (Agrawal et al., ICLR 2026 Oral, arXiv 2507.19457): keeping a
// frontier of prompts and sampling stochastically generalizes better than
// picking a single champion. We persist every viable candidate with its
// score + pareto_rank; the SampleFromFrontier helper draws weighted by score
// when an agent needs an active variant.
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

// OptimizeResult is the structured return shape from RunOptimizer. Callers
// (HTTP API, sentinel, cron) get the frontier_run_id + per-candidate metadata
// so they can render the frontier in Studio or auto-promote the top-ranked.
type OptimizeResult struct {
	FrontierRunID string             `json:"frontier_run_id"`
	SkillName     string             `json:"skill_name"`
	Calls         int                `json:"calls"`
	Candidates    []FrontierEntryDTO `json:"candidates"`
}

// FrontierEntryDTO is one entry in the Pareto frontier with its persisted
// proposal id. Callers use ProposalID to drive the existing /decide flow.
type FrontierEntryDTO struct {
	ProposalID string  `json:"proposal_id"`
	Score      float64 `json:"score"`
	SizeChars  int     `json:"size_chars"`
	ParetoRank int     `json:"pareto_rank"`
	Rationale  string  `json:"rationale"`
}

// RunOptimizer pulls recent traces for the named skill, ships them to GEPA,
// and persists every viable candidate as a Pareto frontier row. Returns the
// frontier run id + the ranked candidates so the caller can decide what to
// surface.
func (m *Manager) RunOptimizer(ctx context.Context, opt *Optimizer, skillName string, traceLimit int) (*OptimizeResult, error) {
	if !opt.Enabled() {
		return nil, errors.New("voyager: GEPA optimizer not configured (set GEPA_URL)")
	}
	if m == nil || m.skillsReg == nil {
		return nil, errors.New("voyager: no skills registry")
	}
	skill, ok := m.skillsReg.Get(skillName)
	if !ok {
		return nil, fmt.Errorf("voyager: unknown skill %q", skillName)
	}
	skillMD, err := readSkillMD(skill.Path)
	if err != nil {
		return nil, fmt.Errorf("read skill md: %w", err)
	}
	if traceLimit <= 0 || traceLimit > 50 {
		traceLimit = 20
	}
	traces, err := m.recentSkillTraces(ctx, skillName, traceLimit)
	if err != nil {
		return nil, fmt.Errorf("load traces: %w", err)
	}

	body, _ := json.Marshal(optimizeReq{
		SkillName: skillName,
		SkillMD:   skillMD,
		Traces:    traces,
		Budget:    optimizeBudget{MaxCandidates: 6, MaxCalls: 24},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opt.url+"/optimize", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if opt.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+opt.apiKey)
	}
	resp, err := opt.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("gepa %d: %s", resp.StatusCode, string(raw))
	}
	var out optimizeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	frontier := paretoFrontier(out.Candidates, skillMD)
	if len(frontier) == 0 {
		return nil, errors.New("no candidate passed the hard gates")
	}

	runID := uuid.NewString()
	entries := make([]FrontierEntryDTO, 0, len(frontier))
	for i, c := range frontier {
		proposalID, perr := m.insertFrontierProposal(ctx, skillName, skill.Version, runID, i, c)
		if perr != nil {
			fmt.Printf("[voyager] persist frontier entry %d: %v\n", i, perr)
			continue
		}
		entries = append(entries, FrontierEntryDTO{
			ProposalID: proposalID,
			Score:      c.Score,
			SizeChars:  c.SizeChars,
			ParetoRank: i,
			Rationale:  c.Rationale,
		})
	}
	if len(entries) == 0 {
		return nil, errors.New("voyager: all frontier entries failed to persist")
	}

	return &OptimizeResult{
		FrontierRunID: runID,
		SkillName:     skillName,
		Calls:         out.Calls,
		Candidates:    entries,
	}, nil
}

// paretoFrontier applies the hard gates Hermes uses and then ranks by score
// descending. We keep every candidate that passes — Pareto here is uni-axis
// (score vs. size penalty via the SizeChars filter); future enhancement is to
// pull in latency/cost as additional axes and run a real non-dominated sort.
func paretoFrontier(cands []candidate, original string) []candidate {
	kept := make([]candidate, 0, len(cands))
	orig := strings.TrimSpace(original)
	for i := range cands {
		c := cands[i]
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
		if md == orig {
			continue
		}
		c.SkillMD = md
		c.SizeChars = len(md)
		kept = append(kept, c)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return kept[i].Score > kept[j].Score
	})
	return kept
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

func (m *Manager) insertFrontierProposal(ctx context.Context, skillName, version, runID string, rank int, c candidate) (string, error) {
	if m == nil || m.pool == nil {
		return "", errors.New("voyager: no db pool")
	}
	meta := map[string]any{
		"size_chars": c.SizeChars,
		"rationale":  c.Rationale,
	}
	metaJSON, _ := json.Marshal(meta)
	var id string
	err := m.pool.QueryRow(ctx, `
		INSERT INTO mem_skill_proposals
		  (name, description, reasoning, skill_md, risk_level, test_pass_rate,
		   status, parent_skill, parent_version,
		   frontier_run_id, score, pareto_rank, gepa_metadata)
		VALUES ($1, $2, $3, $4, 'medium', $5, 'candidate', $1, NULLIF($6, ''),
		        $7::uuid, $5, $8, $9::jsonb)
		RETURNING id::text
	`,
		skillName,
		fmt.Sprintf("GEPA frontier candidate #%d for %s", rank, skillName),
		c.Rationale,
		c.SkillMD,
		c.Score,
		version,
		runID,
		rank,
		string(metaJSON),
	).Scan(&id)
	return id, err
}

// SampleFromFrontier draws a candidate from the most recent Pareto frontier
// for a skill, weighted by score. Returns "" when the skill has no frontier
// (or all candidates were rejected). The agent runtime calls this when it
// wants to A/B a non-champion variant — GEPA's empirical result is that
// stochastic sampling beats champion-only on out-of-distribution inputs.
func (m *Manager) SampleFromFrontier(ctx context.Context, skillName string) (string, error) {
	if m == nil || m.pool == nil {
		return "", nil
	}
	rows, err := m.pool.Query(ctx, `
		SELECT id::text, score
		  FROM mem_skill_proposals
		 WHERE parent_skill = $1
		   AND status = 'candidate'
		   AND frontier_run_id = (
		       SELECT frontier_run_id FROM mem_skill_proposals
		        WHERE parent_skill = $1 AND frontier_run_id IS NOT NULL
		        ORDER BY created_at DESC LIMIT 1
		   )
	`, skillName)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	type entry struct {
		id    string
		score float64
	}
	var pool []entry
	var total float64
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.score); err != nil {
			return "", err
		}
		if e.score < 0 {
			e.score = 0
		}
		pool = append(pool, e)
		total += e.score
	}
	if len(pool) == 0 {
		return "", nil
	}
	if total <= 0 {
		return pool[rand.Intn(len(pool))].id, nil
	}
	r := rand.Float64() * total
	cum := 0.0
	for _, e := range pool {
		cum += e.score
		if r <= cum {
			return e.id, nil
		}
	}
	return pool[len(pool)-1].id, nil
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
