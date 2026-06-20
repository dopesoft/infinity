package voyager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
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
	// Semantic-drift gate: reject any survivor whose embedding has wandered
	// too far from the original skill's meaning. No-op (keeps all) when no
	// real embedder is wired - see filterByDrift.
	frontier = m.filterByDrift(ctx, skillMD, frontier)
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
// descending. We keep every candidate that passes - Pareto here is uni-axis
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
		// Contract-preservation gate: GEPA may reword the body but must not
		// amputate the skill's identity (name), its declared wiring
		// (required env vars / toolsets), its structure (## sections), or
		// shrink it below half the original (a truncated procedure). A
		// candidate that drops any of these "optimized away" the skill's
		// guarantees - reject it, log why, keep going.
		if ok, reason := preservesContract(md, orig); !ok {
			fmt.Printf("[voyager] gepa: dropped candidate (contract): %s\n", reason)
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

const (
	// defaultMinSemanticSimilarity is the floor on cosine(original, candidate)
	// for the semantic-drift gate. Below this the candidate has wandered too
	// far from the skill's original meaning. Override via
	// INFINITY_GEPA_MIN_SIMILARITY (a 0..1 float).
	defaultMinSemanticSimilarity = 0.82
	// minContractLengthRatio rejects a candidate shorter than this fraction of
	// the original SKILL.md - a guard against the optimizer truncating the
	// procedure to chase a higher score.
	minContractLengthRatio = 0.50
)

// contractKeys are the frontmatter keys that wire a skill into the runtime
// (which env vars it needs, which toolsets it requires or backs up). If the
// original declares one, the candidate MUST still declare it - dropping it
// silently breaks the skill even though the prose may read better.
var contractKeys = []string{
	"required_environment_variables",
	"requires_toolsets",
	"fallback_for_toolsets",
}

// preservesContract reports whether a candidate SKILL.md keeps the original's
// load-bearing guarantees. Returns (false, reason) on the first violation so
// the caller can log exactly what was dropped. Pure string-level checks - no
// YAML dependency, robust to reformatting.
func preservesContract(candidate, original string) (bool, string) {
	if float64(len(candidate)) < float64(len(original))*minContractLengthRatio {
		return false, fmt.Sprintf("candidate %d bytes < %.0f%% of original %d bytes",
			len(candidate), minContractLengthRatio*100, len(original))
	}

	origFM := frontmatterBlock(original)
	candFM := frontmatterBlock(candidate)

	// Identity: the skill's name must not change.
	if on := frontmatterValue(origFM, "name"); on != "" {
		if cn := frontmatterValue(candFM, "name"); cn != on {
			return false, fmt.Sprintf("name changed %q -> %q", on, cn)
		}
	}
	// description must survive (non-empty).
	if frontmatterValue(origFM, "description") != "" && frontmatterValue(candFM, "description") == "" {
		return false, "description removed"
	}
	// Wiring keys present in the original must still be present.
	for _, k := range contractKeys {
		if frontmatterHasKey(origFM, k) && !frontmatterHasKey(candFM, k) {
			return false, "dropped frontmatter key " + k
		}
	}
	// Every ## section heading in the original must survive (don't delete
	// "## Procedure" / "## Verification" etc.).
	for _, h := range sectionHeadings(original) {
		if !containsHeading(candidate, h) {
			return false, "dropped section " + h
		}
	}
	return true, ""
}

// frontmatterBlock returns the text between the opening "---" and the next
// "---" line. Empty when there's no well-formed frontmatter.
func frontmatterBlock(md string) string {
	md = strings.TrimSpace(md)
	if !strings.HasPrefix(md, "---") {
		return ""
	}
	rest := strings.TrimPrefix(md, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// frontmatterValue returns the trimmed value of a top-level `key:` line in a
// frontmatter block (first match), or "".
func frontmatterValue(fm, key string) string {
	for _, line := range strings.Split(fm, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(t, key+":"))
		}
	}
	return ""
}

// frontmatterHasKey reports whether `key` appears as a key anywhere in the
// frontmatter block (top-level or nested under metadata).
func frontmatterHasKey(fm, key string) bool {
	for _, line := range strings.Split(fm, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+":") {
			return true
		}
	}
	return false
}

// sectionHeadings returns every "## ..." heading line (trimmed) in the doc.
func sectionHeadings(md string) []string {
	var out []string
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			out = append(out, t)
		}
	}
	return out
}

func containsHeading(md, heading string) bool {
	for _, line := range strings.Split(md, "\n") {
		if strings.TrimSpace(line) == heading {
			return true
		}
	}
	return false
}

// filterByDrift drops candidates whose embedding cosine-similarity to the
// original SKILL.md falls below the drift floor. When no real embedder is
// wired (nil or the deterministic dev stub) the gate is a logged no-op so
// optimization still works without a model - the structural contract gate in
// paretoFrontier still applies in that case.
func (m *Manager) filterByDrift(ctx context.Context, original string, cands []candidate) []candidate {
	if len(cands) == 0 {
		return cands
	}
	if m == nil || m.embedder == nil || strings.EqualFold(m.embedder.Name(), "stub") {
		fmt.Printf("[voyager] gepa: drift gate skipped (no semantic embedder)\n")
		return cands
	}
	floor := minSemanticSimilarity()
	origVec, err := m.embedder.Embed(ctx, original)
	if err != nil {
		fmt.Printf("[voyager] gepa: drift gate skipped (embed original: %v)\n", err)
		return cands
	}
	kept := make([]candidate, 0, len(cands))
	for _, c := range cands {
		vec, err := m.embedder.Embed(ctx, c.SkillMD)
		if err != nil {
			// Can't judge drift - keep it (structural gate already passed).
			kept = append(kept, c)
			continue
		}
		sim := cosine(origVec, vec)
		if sim < floor {
			fmt.Printf("[voyager] gepa: dropped candidate (drift): similarity %.3f < %.2f\n", sim, floor)
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

// minSemanticSimilarity reads the drift floor from env, falling back to the
// default. Clamped to [0,1].
func minSemanticSimilarity() float64 {
	v := strings.TrimSpace(os.Getenv("INFINITY_GEPA_MIN_SIMILARITY"))
	if v == "" {
		return defaultMinSemanticSimilarity
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 1 {
		return defaultMinSemanticSimilarity
	}
	return f
}

// cosine returns the cosine similarity of two equal-length vectors, in
// [-1,1]. Returns 0 when either is empty or lengths differ.
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// recentSkillTraces pulls recent runs for a skill out of mem_skill_runs.
// Used as the trace input to GEPA. We grab successes too - they are the
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
		  (name, description, reasoning, skill_md, risk_level, importance, importance_reason, test_pass_rate,
		   status, parent_skill, parent_version,
		   frontier_run_id, score, pareto_rank, gepa_metadata)
		VALUES ($1, $2, $3, $4, 'medium', 90, 'GEPA optimization for an existing skill; review because it can improve autonomous behavior.', $5, 'candidate', $1, NULLIF($6, ''),
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
// wants to A/B a non-champion variant - GEPA's empirical result is that
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

// SampleVariant is the runtime READ half of the GEPA Pareto frontier: at invoke
// time it epsilon-occasionally serves a high-scoring frontier candidate's recipe
// in place of the champion, so the variant gets exercised on real inputs (GEPA's
// empirical result: stochastic sampling beats champion-only out-of-distribution).
// The candidate already cleared the optimizer's size, semantic-drift and
// contract-preservation gates, so it is a REWORDED champion — not an arbitrary
// recipe. Satisfies skills.FrontierSampler (wired in serve.go via
// AttachFrontierSampler). It must NEVER disrupt an invoke: any miss/error
// returns ok=false and the champion runs.
func (m *Manager) SampleVariant(ctx context.Context, skillName string) (string, string, bool) {
	if m == nil || m.pool == nil {
		return "", "", false
	}
	eps := frontierEpsilon()
	if eps <= 0 || rand.Float64() > eps {
		return "", "", false
	}
	id, err := m.SampleFromFrontier(ctx, skillName)
	if err != nil || id == "" {
		return "", "", false
	}
	var body string
	if err := m.pool.QueryRow(ctx,
		`SELECT COALESCE(skill_md, '') FROM mem_skill_proposals WHERE id = $1`, id,
	).Scan(&body); err != nil || strings.TrimSpace(body) == "" {
		return "", "", false
	}
	return body, id, true
}

// frontierEpsilon is the probability a skill invoke A/B-serves a frontier
// variant instead of the champion. INFINITY_FRONTIER_EPSILON overrides; 0
// disables A/B entirely (champion always). Default is deliberately low — the
// variant is a reworded champion, but most invokes still run the proven body.
func frontierEpsilon() float64 {
	eps := envFloat("INFINITY_FRONTIER_EPSILON", 0.15)
	if eps < 0 {
		return 0
	}
	if eps > 1 {
		return 1
	}
	return eps
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
