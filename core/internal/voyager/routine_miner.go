package voyager

// Routine miner.
//
// Where the existing Voyager hooks watch TOOL sequences (OnSessionEnd builds
// a SKILL.md from one tool-rich session; OnPostToolUse counts triplets across
// sessions) and SkillAuthoringChecklist watches 3-tool windows from
// PostToolUse observations, this miner watches REPEATED USER REQUESTS
// themselves. The boss may ask "summarize my unread mail" / "draft a tweet
// about X" / "check what's on the calendar tomorrow" twenty times across
// twenty sessions without the tool fingerprint repeating in a way the other
// detectors catch (different mailboxes, different deltas, different sub-
// tools). That's still a routine worth crystallizing.
//
// The miner clusters UserPromptSubmit observations by a normalized
// content-keyword fingerprint, scores each cluster on hit count + distinct-
// session/day breadth + recency, drops anything an active skill already
// covers via the existing dedup gate, and lands a proposal in
// `mem_skill_proposals` (the canonical store — never a parallel table)
// plus a generic surface card under surface='routines' so it appears in the
// "Surfaced by Jarvis" inbox without bespoke Studio code (Rule #1b).
//
// Two entry points, both call the same MineAndPropose core:
//
//   - RunNightly(ctx, pool): registered as a memory.RegisterConsolidateHook
//     so a fresh sweep runs as the last stage of every nightly cognition.
//   - OnUserPrompt(ctx, ev): registered on hooks.UserPromptSubmit so when a
//     new prompt closes a cluster across the threshold mid-conversation, a
//     proactive chat bubble goes out via the wired OnChatNotify callback.
//
// Provenance is mandatory. Every proposal's `reasoning` field carries the
// session IDs and observation IDs that fed it, and the surface card's
// metadata carries the same plus the cluster fingerprint and example
// prompts — so the boss can trace any routine card back to the actual
// requests that produced it. No skill is ever auto-installed; the boss
// approves via the existing Voyager Decide path.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dopesoft/infinity/core/internal/hooks"
	"github.com/dopesoft/infinity/core/internal/proposals"
	"github.com/dopesoft/infinity/core/internal/surface"
)

// Mining thresholds. Conservative on purpose: a routine proposal is a card
// the boss has to act on, so a false positive is more expensive than a
// missed catch. Widen later from real signal, not guesses.
const (
	routineLookback            = 30 * 24 * time.Hour
	routineMinHits             = 3
	routineMinDistinctSessions = 2
	routineMinDistinctDays     = 2
	routineMaxObservations     = 2000
	routineKeywordsPerPrompt   = 4
	routineSurfaceKey          = "routines"
	routineSurfaceKind         = "skill_proposal"
	routineSurfaceSource       = "routine-miner"
	routineProposalKind        = "routine"
)

// Notifier is the chat-stream delivery seam. RoutineMiner calls it with the
// new proposal's session, name, and a short Markdown body when a UserPromptSubmit
// observation closes a cluster across the threshold mid-conversation. serve.go
// binds it to Server.BroadcastRoutineProposed; the test wiring binds a no-op.
type Notifier func(sessionID, name, markdown string)

// RoutineMiner is constructed once at boot and registered against both the
// hooks pipeline (UserPromptSubmit, active-conversation path) and
// memory.RegisterConsolidateHook (nightly sweep path). Safe to construct
// even when LLM / pool is nil; methods degrade quietly.
type RoutineMiner struct {
	pool     *pgxpool.Pool
	llm      Drafter
	logger   *slog.Logger
	notifyMu sync.RWMutex
	notify   Notifier

	// recentNotify is a process-local de-dup: don't push the same routine
	// signature into the same session more than once per process lifetime
	// even if the cluster keeps tripping. The card stays on the dashboard
	// either way; the chat bubble is the interruption signal.
	recentMu sync.Mutex
	recent   map[string]time.Time // key: sessionID + "|" + signature
}

// NewRoutineMiner constructs the miner. logger may be nil; defaults to slog.Default.
func NewRoutineMiner(pool *pgxpool.Pool, llm Drafter, logger *slog.Logger) *RoutineMiner {
	if logger == nil {
		logger = slog.Default()
	}
	return &RoutineMiner{
		pool:   pool,
		llm:    llm,
		logger: logger,
		recent: map[string]time.Time{},
	}
}

// SetNotifier wires the chat-stream callback. Late-bound so serve.go can hook
// Server.BroadcastRoutineProposed after the Server is constructed.
func (m *RoutineMiner) SetNotifier(n Notifier) {
	if m == nil {
		return
	}
	m.notifyMu.Lock()
	m.notify = n
	m.notifyMu.Unlock()
}

// MineReport summarises one sweep. Returned for tests + observability.
type MineReport struct {
	ClustersDetected int      `json:"clusters_detected"`
	ProposalsCreated int      `json:"proposals_created"`
	ProposalsMerged  int      `json:"proposals_merged"`
	SurfacedItems    int      `json:"surfaced_items"`
	Skipped          int      `json:"skipped_already_skilled"`
	ProposalIDs      []string `json:"proposal_ids,omitempty"`
}

// RunNightly is the memory.RegisterConsolidateHook signature: best-effort
// sweep over the trailing lookback window, logged via the package logger.
// Returns nothing because the consolidate-hook seam discards return values.
func (m *RoutineMiner) RunNightly(ctx context.Context, pool *pgxpool.Pool) {
	if m == nil {
		return
	}
	// The miner is constructed with its own pool, but ConsolidateNightly
	// passes one through. Prefer the constructor pool for stability but
	// fall back to the passed pool if the constructor pool was nil — that
	// keeps the hook robust to wiring order changes.
	use := m.pool
	if use == nil {
		use = pool
	}
	if use == nil {
		return
	}
	rep, err := m.mineAndPropose(ctx, use, time.Now().UTC().Add(-routineLookback), nil)
	if err != nil {
		m.logger.Warn("routine miner nightly sweep", "err", err)
		return
	}
	if rep.ProposalsCreated > 0 || rep.ProposalsMerged > 0 || rep.SurfacedItems > 0 {
		m.logger.Info("routine miner nightly sweep",
			"clusters", rep.ClustersDetected,
			"proposed", rep.ProposalsCreated,
			"merged", rep.ProposalsMerged,
			"surfaced", rep.SurfacedItems,
			"skipped_already_skilled", rep.Skipped)
	}
}

// MineAndPropose is the exported core. Exposed for tests and an optional CLI.
// `since` is the lower bound on observation created_at. `notifySession` is the
// session id to push a chat bubble into when a fresh cluster crosses the
// threshold from THIS sweep (typically empty for the nightly path; set by
// OnUserPrompt to the current session).
func (m *RoutineMiner) MineAndPropose(ctx context.Context, since time.Time, notifySession string) (MineReport, error) {
	if m == nil || m.pool == nil {
		return MineReport{}, nil
	}
	return m.mineAndPropose(ctx, m.pool, since, &notifySession)
}

func (m *RoutineMiner) mineAndPropose(ctx context.Context, pool *pgxpool.Pool, since time.Time, notifySession *string) (MineReport, error) {
	rep := MineReport{}
	if pool == nil {
		return rep, nil
	}
	prompts, err := m.collectPrompts(ctx, pool, since)
	if err != nil {
		return rep, fmt.Errorf("collect prompts: %w", err)
	}
	clusters := ClusterPrompts(prompts)
	rep.ClustersDetected = len(clusters)
	if len(clusters) == 0 {
		return rep, nil
	}
	// Reconcile surface: track which signatures we're about to keep open
	// so any stale routine card whose cluster no longer surfaces can be
	// dismissed at the end of the sweep.
	desired := map[string]bool{}

	for _, cl := range clusters {
		if !cl.meetsThreshold() {
			continue
		}
		if m.coveredByActiveSkill(ctx, pool, cl) {
			rep.Skipped++
			continue
		}
		// dedup-before-create against the existing skill catalog: route into
		// the canonical skill's draft if a duplicate is detected. Same gate
		// the session extractor uses.
		propID, isNew, err := m.persistProposal(ctx, pool, cl)
		if err != nil {
			m.logger.Warn("routine miner: persist proposal", "signature", cl.Signature, "err", err)
			continue
		}
		if isNew {
			rep.ProposalsCreated++
		} else {
			rep.ProposalsMerged++
		}
		if propID != "" {
			rep.ProposalIDs = append(rep.ProposalIDs, propID)
		}
		if surfaced := m.surfaceCard(ctx, pool, cl, propID); surfaced {
			rep.SurfacedItems++
			desired[cl.externalID()] = true
		}
		if notifySession != nil && strings.TrimSpace(*notifySession) != "" {
			m.maybeNotifyChat(*notifySession, cl, propID)
		}
	}

	// Reconcile: dismiss routine cards whose signature is no longer in the
	// open set. Mirrors the SubstrateSurfaceChecklist pattern.
	m.reconcile(ctx, pool, desired)
	return rep, nil
}

// OnUserPrompt is the hooks.UserPromptSubmit handler. Fired before the agent
// turn runs. We inline a focused cheap check: does this new prompt push a
// cluster across the threshold? If so the full MineAndPropose pass runs
// scoped to the trailing lookback window with the session id wired in,
// so a chat bubble surfaces immediately — and the proposal lands in the
// same canonical store the nightly sweep uses.
//
// The bound here is one DB round-trip + an in-memory cluster pass per
// substantive user prompt; the threshold of routineMinHits keeps misfires
// rare. Non-substantive prompts (greetings, "ok", single-word acks) are
// filtered by tokenisation returning <2 keywords.
func (m *RoutineMiner) OnUserPrompt(ctx context.Context, ev hooks.Event) error {
	if m == nil || m.pool == nil {
		return nil
	}
	text := strings.TrimSpace(ev.Text)
	if text == "" {
		return nil
	}
	keys := promptKeywords(text)
	if len(keys) < 2 {
		return nil // greetings, acks, tiny replies — not a routine signal
	}
	// Cheap pre-check: is THIS prompt's signature already past threshold across
	// recent observations? Avoid an unconditional MineAndPropose call on every
	// turn.
	sig := signatureFromKeywords(keys)
	hits, err := m.countSignatureHits(ctx, sig, time.Now().UTC().Add(-routineLookback))
	if err != nil {
		return nil
	}
	if hits < routineMinHits-1 {
		// The current prompt brings the count to hits+1; only proceed when
		// that crosses or exceeds routineMinHits.
		return nil
	}
	go func(sessID string) {
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := m.MineAndPropose(bg, time.Now().UTC().Add(-routineLookback), sessID); err != nil {
			m.logger.Warn("routine miner active-conversation sweep", "err", err)
		}
	}(ev.SessionID)
	return nil
}

// countSignatureHits asks the database how many distinct sessions a
// signature's keyword set appears across in the lookback window. Used as a
// cheap gate before firing a full sweep on UserPromptSubmit.
func (m *RoutineMiner) countSignatureHits(ctx context.Context, sig string, since time.Time) (int, error) {
	if m == nil || m.pool == nil {
		return 0, nil
	}
	// We can't run the full cluster algorithm in SQL, so the gate is
	// approximate: count UserPromptSubmit observations whose normalized
	// text contains all of the signature's tokens. False positives only
	// mean we fire a real sweep — the real sweep will resolve correctly.
	tokens := strings.Split(sig, " ")
	if len(tokens) == 0 {
		return 0, nil
	}
	conds := []string{"hook_name = 'UserPromptSubmit'", "created_at >= $1"}
	args := []any{since}
	for _, t := range tokens {
		if t == "" {
			continue
		}
		args = append(args, "%"+t+"%")
		conds = append(conds, fmt.Sprintf("LOWER(COALESCE(raw_text,'')) LIKE $%d", len(args)))
	}
	q := "SELECT COUNT(DISTINCT session_id) FROM mem_observations WHERE " + strings.Join(conds, " AND ")
	var n int
	if err := m.pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// collectPrompts pulls every UserPromptSubmit observation from the lookback
// window. The cap is deliberately high enough to cover ~weeks of normal
// activity but not unbounded.
func (m *RoutineMiner) collectPrompts(ctx context.Context, pool *pgxpool.Pool, since time.Time) ([]promptRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT id::text, COALESCE(session_id::text, ''), COALESCE(raw_text, ''), created_at
		  FROM mem_observations
		 WHERE hook_name = 'UserPromptSubmit'
		   AND created_at >= $1
		   AND COALESCE(raw_text, '') <> ''
		 ORDER BY created_at DESC
		 LIMIT $2
	`, since, routineMaxObservations)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []promptRow
	for rows.Next() {
		var p promptRow
		if err := rows.Scan(&p.ID, &p.SessionID, &p.Text, &p.At); err != nil {
			return nil, err
		}
		p.Text = strings.TrimSpace(p.Text)
		if p.Text == "" {
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// promptRow is one observation row we feed to the clusterer.
type promptRow struct {
	ID        string
	SessionID string
	Text      string
	At        time.Time
}

// Cluster is one detected routine: a set of prompts that share a normalized
// keyword fingerprint. Exposed for tests + readability.
type Cluster struct {
	Signature        string    // canonical "k1 k2 k3 k4" lowercased
	Keywords         []string  // ordered keywords that produced the signature
	Hits             int       // total prompt observations in this cluster
	DistinctSessions int       // distinct session ids
	DistinctDays     int       // distinct UTC days the cluster fired on
	FirstAt          time.Time // earliest observation
	LastAt           time.Time // most recent observation
	SessionIDs       []string  // up to 12 for provenance display
	ObservationIDs   []string  // up to 24 for provenance display
	Examples         []string  // up to 5 verbatim prompt strings
}

// ClusterPrompts groups prompts by their normalized keyword signature and
// returns clusters sorted by descending hit count then recency. Pure
// function; deterministic; safe to test independently.
//
// Clustering is two-pass so per-prompt vocabulary noise ("this morning",
// "asap", "please could you") doesn't fracture a real routine into
// near-duplicate clusters:
//
//  1. Pass 1: count token frequency across the entire corpus, after
//     stopwords + length filter.
//  2. Pass 2: for each prompt, take the intersection of its tokens with
//     the corpus-frequent tokens (those that appear in ≥2 prompts), then
//     pick the alpha-sorted top-routineKeywordsPerPrompt of that
//     intersection as the signature. Tokens unique to one prompt fall
//     out of the signature — the shared vocabulary is what defines the
//     routine.
//
// When the corpus is too small for a frequency signal (≤2 prompts), we
// fall back to per-prompt keyword selection so the algorithm still
// produces clusters during early sweeps.
func ClusterPrompts(prompts []promptRow) []Cluster {
	if len(prompts) == 0 {
		return nil
	}
	// Pass 1: corpus token frequency over surviving keywords.
	freq := map[string]int{}
	cached := make([]map[string]struct{}, len(prompts))
	for i, p := range prompts {
		keys := promptKeywordsRaw(p.Text)
		if len(keys) == 0 {
			continue
		}
		cached[i] = map[string]struct{}{}
		for _, k := range keys {
			if _, dup := cached[i][k]; dup {
				continue
			}
			cached[i][k] = struct{}{}
			freq[k]++
		}
	}
	type acc struct {
		keys           []string
		hits           int
		sessions       map[string]struct{}
		days           map[string]struct{}
		firstAt        time.Time
		lastAt         time.Time
		sessionList    []string
		observationIDs []string
		examples       []string
	}
	groups := map[string]*acc{}
	useFreq := len(prompts) >= 3
	for i, p := range prompts {
		// Build the signature from corpus-frequent tokens when there's enough
		// signal; fall back to per-prompt picking on tiny corpora.
		var keys []string
		if useFreq {
			keys = signatureKeywordsFromFreq(cached[i], freq)
		} else {
			keys = promptKeywords(p.Text)
		}
		if len(keys) < 2 {
			continue
		}
		sig := signatureFromKeywords(keys)
		g, ok := groups[sig]
		if !ok {
			g = &acc{
				keys:     keys,
				sessions: map[string]struct{}{},
				days:     map[string]struct{}{},
				firstAt:  p.At,
				lastAt:   p.At,
			}
			groups[sig] = g
		}
		g.hits++
		if p.SessionID != "" {
			if _, seen := g.sessions[p.SessionID]; !seen {
				g.sessions[p.SessionID] = struct{}{}
				if len(g.sessionList) < 12 {
					g.sessionList = append(g.sessionList, p.SessionID)
				}
			}
		}
		day := p.At.UTC().Format("2006-01-02")
		g.days[day] = struct{}{}
		if p.At.Before(g.firstAt) {
			g.firstAt = p.At
		}
		if p.At.After(g.lastAt) {
			g.lastAt = p.At
		}
		if len(g.observationIDs) < 24 {
			g.observationIDs = append(g.observationIDs, p.ID)
		}
		if len(g.examples) < 5 {
			g.examples = append(g.examples, p.Text)
		}
	}
	out := make([]Cluster, 0, len(groups))
	for sig, g := range groups {
		out = append(out, Cluster{
			Signature:        sig,
			Keywords:         g.keys,
			Hits:             g.hits,
			DistinctSessions: len(g.sessions),
			DistinctDays:     len(g.days),
			FirstAt:          g.firstAt,
			LastAt:           g.lastAt,
			SessionIDs:       g.sessionList,
			ObservationIDs:   g.observationIDs,
			Examples:         g.examples,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Hits != out[j].Hits {
			return out[i].Hits > out[j].Hits
		}
		return out[i].LastAt.After(out[j].LastAt)
	})
	return out
}

// meetsThreshold gates a cluster on the hit + breadth thresholds. Tested
// independently so the tuning surfaces in one place.
func (c Cluster) meetsThreshold() bool {
	if c.Hits < routineMinHits {
		return false
	}
	if c.DistinctSessions < routineMinDistinctSessions {
		return false
	}
	if c.DistinctDays < routineMinDistinctDays {
		return false
	}
	return true
}

// Confidence returns a 0-100 strategic-value score. Combines hit count
// (saturates at 20), distinct-day breadth (saturates at 14), and recency.
// Exported for tests + visibility in metadata.
func (c Cluster) Confidence() int {
	if c.Hits <= 0 {
		return 0
	}
	hitPart := float64(c.Hits)
	if hitPart > 20 {
		hitPart = 20
	}
	hitScore := (hitPart / 20.0) * 60.0 // 0..60
	dayPart := float64(c.DistinctDays)
	if dayPart > 14 {
		dayPart = 14
	}
	dayScore := (dayPart / 14.0) * 30.0 // 0..30
	recencyScore := 0.0
	if !c.LastAt.IsZero() {
		age := time.Since(c.LastAt)
		switch {
		case age < 24*time.Hour:
			recencyScore = 10
		case age < 7*24*time.Hour:
			recencyScore = 7
		case age < 30*24*time.Hour:
			recencyScore = 3
		}
	}
	score := int(hitScore + dayScore + recencyScore)
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}

// externalID is the surface card's stable external_id so re-runs upsert in
// place. Hash the signature so a long key stays compact.
func (c Cluster) externalID() string {
	sum := sha1.Sum([]byte(c.Signature))
	return "routine:" + hex.EncodeToString(sum[:8])
}

// proposedName turns the cluster keywords into a snake_cased candidate skill
// name. Used both for the mem_skill_proposals row and the surface title.
func (c Cluster) proposedName() string {
	if len(c.Keywords) == 0 {
		return "routine"
	}
	parts := make([]string, 0, len(c.Keywords))
	for _, k := range c.Keywords {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		parts = append(parts, k)
		if len(parts) >= 4 {
			break
		}
	}
	if len(parts) == 0 {
		return "routine"
	}
	return "routine_" + strings.Join(parts, "_")
}

// readableTitle is the surface card's title. Capitalises the first keyword.
func (c Cluster) readableTitle() string {
	if len(c.Examples) > 0 {
		ex := strings.TrimSpace(c.Examples[0])
		if ex != "" {
			if len(ex) > 80 {
				ex = ex[:80] + "..."
			}
			return "Repeated routine: " + ex
		}
	}
	pretty := strings.Join(c.Keywords, " ")
	return "Repeated routine: " + pretty
}

// coveredByActiveSkill skips clusters that any installed active skill already
// addresses, judged by overlap between the cluster's keyword set and the
// active skill names/descriptions. Cheap; deliberately weak so a real new
// routine still surfaces — a false negative is fine, a noisy duplicate is not.
func (m *RoutineMiner) coveredByActiveSkill(ctx context.Context, pool *pgxpool.Pool, cl Cluster) bool {
	if pool == nil {
		return false
	}
	type sk struct{ name, desc string }
	rows, err := pool.Query(ctx, `SELECT name, COALESCE(description,'') FROM mem_skills WHERE status='active'`)
	if err != nil {
		return false
	}
	var catalog []sk
	for rows.Next() {
		var s sk
		if err := rows.Scan(&s.name, &s.desc); err == nil {
			catalog = append(catalog, s)
		}
	}
	rows.Close()
	if len(catalog) == 0 {
		return false
	}
	keySet := map[string]struct{}{}
	for _, k := range cl.Keywords {
		keySet[strings.ToLower(k)] = struct{}{}
	}
	for _, s := range catalog {
		hay := strings.ToLower(s.name + " " + s.desc)
		hits := 0
		for k := range keySet {
			if strings.Contains(hay, k) {
				hits++
			}
		}
		// Heuristic threshold: at least half the cluster's keywords appear in
		// an active skill's name/description text.
		if hits >= (len(keySet)+1)/2 && hits >= 2 {
			return true
		}
	}
	return false
}

// persistProposal lands the cluster as a routine-kind row in
// mem_skill_proposals. Routes through proposals.UpsertCandidate when an
// active skill name overlaps (parent_skill path) so a routine the agent
// already has becomes a merge candidate instead of a duplicate row. Brand
// new clusters insert as `proposal_kind='routine'` with a stub SKILL.md.
// Returns (id, isNew, err).
func (m *RoutineMiner) persistProposal(ctx context.Context, pool *pgxpool.Pool, cl Cluster) (string, bool, error) {
	name := cl.proposedName()
	description := truncate("Recurring user request: "+strings.Join(cl.Keywords, " "), 200)
	reasoning := cl.provenanceMarkdown()
	skillMD := cl.draftSkillMD()

	// Dedup gate: if the proposed name maps to an existing skill, route the
	// row through the merge path so the boss gets one accumulating card per
	// skill, not a duplicate.
	if match := proposals.FindDuplicateSkill(ctx, pool, m.llm, name, description); match != "" {
		res, err := proposals.UpsertCandidate(ctx, pool, m.llm, m.logger, proposals.CandidateDraft{
			Name:             match + "-routine-update",
			ParentSkill:      match,
			Description:      description,
			Reasoning:        reasoning,
			SkillMD:          skillMD,
			RiskLevel:        "low",
			Importance:       cl.Confidence(),
			ImportanceReason: "Recurring user request worth crystallizing",
			Source:           routineSurfaceSource,
		})
		if err != nil {
			return "", false, err
		}
		return res.ID, res.IsNew, nil
	}

	// Skip clusters that already produced an OPEN routine proposal so a
	// re-run of the nightly sweep refreshes (via surfaceCard) instead of
	// piling fresh candidate rows.
	if existingID, ok := m.findOpenRoutineByName(ctx, pool, name); ok {
		// Bump importance to match the latest score, keep reasoning fresh.
		_, _ = pool.Exec(ctx, `
			UPDATE mem_skill_proposals
			   SET importance = $2,
			       reasoning = $3,
			       last_merged_at = NOW()
			 WHERE id = $1
		`, existingID, cl.Confidence(), truncate(reasoning, 1500))
		return existingID, false, nil
	}

	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO mem_skill_proposals
		  (name, description, reasoning, skill_md, risk_level, importance, importance_reason,
		   status, proposal_kind)
		VALUES ($1, $2, $3, $4, 'low', $5, $6, 'candidate', $7)
		RETURNING id::text
	`, name, description, truncate(reasoning, 1500), skillMD,
		cl.Confidence(), "Recurring user request worth crystallizing", routineProposalKind).Scan(&id)
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func (m *RoutineMiner) findOpenRoutineByName(ctx context.Context, pool *pgxpool.Pool, name string) (string, bool) {
	var id string
	err := pool.QueryRow(ctx, `
		SELECT id::text FROM mem_skill_proposals
		 WHERE name = $1 AND status = 'candidate' AND proposal_kind = $2
		 LIMIT 1
	`, name, routineProposalKind).Scan(&id)
	if err != nil {
		return "", false
	}
	return id, id != ""
}

// surfaceCard lands a generic surface item under surface='routines' so the
// boss sees the routine in the unified "Surfaced by Jarvis" inbox. Per the
// surface routing rule, surface='system' folds into Activity instead of the
// inbox — so a distinct key (here, 'routines') is required. Returns true if
// the card was upserted.
func (m *RoutineMiner) surfaceCard(ctx context.Context, pool *pgxpool.Pool, cl Cluster, proposalID string) bool {
	if pool == nil {
		return false
	}
	store := surface.NewStore(pool, m.logger)
	imp := cl.Confidence()
	subtitle := fmt.Sprintf("%d hits across %d sessions, %d days",
		cl.Hits, cl.DistinctSessions, cl.DistinctDays)
	metadata := map[string]any{
		"signature":         cl.Signature,
		"keywords":          cl.Keywords,
		"hits":              cl.Hits,
		"distinct_sessions": cl.DistinctSessions,
		"distinct_days":     cl.DistinctDays,
		"first_seen":        cl.FirstAt.UTC().Format(time.RFC3339),
		"last_seen":         cl.LastAt.UTC().Format(time.RFC3339),
		"session_ids":       cl.SessionIDs,
		"observation_ids":   cl.ObservationIDs,
		"examples":          cl.Examples,
		"proposal_id":       proposalID,
	}
	_, err := store.Upsert(ctx, &surface.Item{
		Surface:          routineSurfaceKey,
		Kind:             routineSurfaceKind,
		Source:           routineSurfaceSource,
		ExternalID:       cl.externalID(),
		Title:            cl.readableTitle(),
		Subtitle:         subtitle,
		Body:             cl.provenanceMarkdown(),
		Importance:       &imp,
		ImportanceReason: "A routine the boss runs often, worth crystallizing into a skill for review",
		Metadata:         metadata,
		Reopen:           true,
		Actions: []surface.Action{
			{
				ID:     "review_proposal",
				Label:  "Review proposal",
				Intent: "Open Voyager proposal " + proposalID + " for review (routine miner).",
				Style:  "primary",
			},
			{
				ID:     "dismiss",
				Label:  "Not a routine",
				Intent: "Dismiss this routine card and remember this signature is not a routine.",
			},
		},
	})
	if err != nil {
		m.logger.Warn("routine miner: surface upsert", "signature", cl.Signature, "err", err)
		return false
	}
	return true
}

// reconcile dismisses routine cards whose signature isn't in the freshly
// detected set anymore. Mirrors the SubstrateSurfaceChecklist pattern so a
// cluster that has stopped firing fades from the dashboard rather than
// becoming permanent noise.
func (m *RoutineMiner) reconcile(ctx context.Context, pool *pgxpool.Pool, desired map[string]bool) {
	store := surface.NewStore(pool, m.logger)
	items, err := store.ListBySurface(ctx, routineSurfaceKey, 200)
	if err != nil {
		return
	}
	dismissed := surface.StatusDismissed
	for _, it := range items {
		if it.Source != routineSurfaceSource || it.ExternalID == "" {
			continue
		}
		if desired[it.ExternalID] {
			continue
		}
		// Don't auto-dismiss freshly created cards from the same sweep — the
		// reconcile pass only fires for items that pre-existed. The desired
		// map already covers everything we touched this sweep.
		_ = store.Update(ctx, it.ID, surface.Patch{Status: &dismissed})
	}
}

// maybeNotifyChat pushes a chat bubble into the active session ONCE per
// (session, signature). When SetNotifier wasn't called, this is a quiet
// no-op (e.g. CLI runs of MineAndPropose don't push to chat).
func (m *RoutineMiner) maybeNotifyChat(sessionID string, cl Cluster, proposalID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	m.notifyMu.RLock()
	notify := m.notify
	m.notifyMu.RUnlock()
	if notify == nil {
		return
	}
	key := sessionID + "|" + cl.Signature
	m.recentMu.Lock()
	if last, ok := m.recent[key]; ok && time.Since(last) < 6*time.Hour {
		m.recentMu.Unlock()
		return
	}
	m.recent[key] = time.Now()
	m.recentMu.Unlock()

	notify(sessionID, cl.proposedName(), cl.chatBubble(proposalID))
}

// chatBubble is the Markdown the boss sees when a routine fires across the
// threshold mid-conversation. Plain, factual, no jargon.
func (c Cluster) chatBubble(proposalID string) string {
	var b strings.Builder
	b.WriteString("**Noticed a routine.**\n\n")
	fmt.Fprintf(&b, "You've made this kind of request %d times across %d sessions on %d different days. ",
		c.Hits, c.DistinctSessions, c.DistinctDays)
	b.WriteString("I drafted a skill proposal for it so you can review and approve it once instead of typing the same setup every time. ")
	b.WriteString("Nothing is installed yet.\n\n")
	if len(c.Examples) > 0 {
		b.WriteString("**Recent examples**\n")
		for i, ex := range c.Examples {
			if i >= 3 {
				break
			}
			ex = strings.TrimSpace(ex)
			if ex == "" {
				continue
			}
			if len(ex) > 140 {
				ex = ex[:140] + "..."
			}
			fmt.Fprintf(&b, "- %s\n", ex)
		}
	}
	if proposalID != "" {
		fmt.Fprintf(&b, "\nProposal id: `%s`. Review in /skills to approve, edit, or reject.", proposalID)
	}
	return b.String()
}

// provenanceMarkdown is what lands in mem_skill_proposals.reasoning and the
// surface card body. Provenance is mandatory: a future reviewer must be able
// to trace any proposal back to the prompts that produced it.
func (c Cluster) provenanceMarkdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Cluster signature**: `%s`\n\n", c.Signature)
	fmt.Fprintf(&b, "**Why this is a routine**: %d prompt observations across %d distinct sessions on %d distinct days (most recent %s).\n\n",
		c.Hits, c.DistinctSessions, c.DistinctDays, c.LastAt.UTC().Format(time.RFC3339))
	if len(c.Keywords) > 0 {
		fmt.Fprintf(&b, "**Cluster keywords**: %s\n\n", strings.Join(c.Keywords, ", "))
	}
	if len(c.Examples) > 0 {
		b.WriteString("**Example prompts**:\n")
		for i, ex := range c.Examples {
			if i >= 5 {
				break
			}
			ex = strings.TrimSpace(ex)
			if ex == "" {
				continue
			}
			if len(ex) > 200 {
				ex = ex[:200] + "..."
			}
			fmt.Fprintf(&b, "- %q\n", ex)
		}
		b.WriteString("\n")
	}
	if len(c.SessionIDs) > 0 {
		b.WriteString("**Provenance sessions**: ")
		b.WriteString(strings.Join(c.SessionIDs, ", "))
		b.WriteString("\n")
	}
	if len(c.ObservationIDs) > 0 {
		b.WriteString("**Provenance observations**: ")
		b.WriteString(strings.Join(c.ObservationIDs, ", "))
		b.WriteString("\n")
	}
	return b.String()
}

// draftSkillMD is the SKILL.md stub the boss reviews. Deliberately minimal:
// the cognition for what the skill actually DOES is the boss's call when he
// approves it. The miner only crystallizes the recurrence, not the recipe.
func (c Cluster) draftSkillMD() string {
	name := c.proposedName()
	desc := truncate("Recurring user request: "+strings.Join(c.Keywords, " "), 120)
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	b.WriteString("version: '0.1.0'\n")
	fmt.Fprintf(&b, "description: %s\n", desc)
	b.WriteString("trigger_phrases:\n")
	for i, ex := range c.Examples {
		if i >= 3 {
			break
		}
		// Strip newlines and quotes from trigger phrases.
		clean := strings.ReplaceAll(ex, "\n", " ")
		clean = strings.ReplaceAll(clean, "'", "")
		if len(clean) > 80 {
			clean = clean[:80]
		}
		fmt.Fprintf(&b, "  - '%s'\n", clean)
	}
	b.WriteString("inputs: []\n")
	b.WriteString("outputs: []\n")
	b.WriteString("risk_level: low\n")
	b.WriteString("network_egress: 'none'\n")
	b.WriteString("confidence: 0.5\n")
	b.WriteString("source: routine-miner\n")
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", name)
	b.WriteString("## When to use\n\n")
	b.WriteString("The boss makes this kind of request recurringly. Use this skill instead of re-deriving the steps each time.\n\n")
	b.WriteString("## Steps\n\n")
	b.WriteString("1. Re-read the boss's verbatim ask in this turn.\n")
	b.WriteString("2. Identify which concrete action class it falls into (read/write/summarize/draft).\n")
	b.WriteString("3. Run the existing tool that already covers that action class, scoped to the parameters in the ask.\n")
	b.WriteString("4. Surface a clean human result.\n\n")
	b.WriteString("## Notes\n\n")
	b.WriteString("This SKILL.md is a stub drafted by the routine miner from observed recurrence. Replace the steps with the actual recipe before promoting.\n")
	return b.String()
}

// metadataJSON is exposed for tests that want to introspect surface metadata
// without going through the live DB.
func (c Cluster) metadataJSON() string {
	b, _ := json.Marshal(map[string]any{
		"signature": c.Signature,
		"keywords":  c.Keywords,
		"hits":      c.Hits,
		"examples":  c.Examples,
	})
	return string(b)
}

// --- prompt tokenisation -------------------------------------------------

// promptKeywords reduces a raw prompt to its content-bearing keywords —
// the words that meaningfully discriminate one request from another. Filters
// stopwords, punctuation, very short tokens, and the agent's own
// vocabulary (proper noun "jarvis"). Returns up to routineKeywordsPerPrompt
// alphabetically sorted so two prompts with the same intent collapse to one
// signature regardless of phrasing order.
func promptKeywords(text string) []string {
	if text == "" {
		return nil
	}
	lower := strings.ToLower(text)
	// Replace non-alphanumerics with spaces (keeps emojis out as a side
	// effect).
	var rb strings.Builder
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			rb.WriteRune(r)
		default:
			rb.WriteRune(' ')
		}
	}
	tokens := strings.Fields(rb.String())
	seen := map[string]struct{}{}
	var keep []string
	for _, t := range tokens {
		if len(t) < 4 {
			continue
		}
		if _, isStop := promptStopwords[t]; isStop {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		keep = append(keep, t)
	}
	if len(keep) > routineKeywordsPerPrompt {
		// Pick the longest tokens first (a proxy for distinctiveness) before
		// alpha sorting to a canonical signature.
		sort.SliceStable(keep, func(i, j int) bool {
			if len(keep[i]) != len(keep[j]) {
				return len(keep[i]) > len(keep[j])
			}
			return keep[i] < keep[j]
		})
		keep = keep[:routineKeywordsPerPrompt]
	}
	sort.Strings(keep)
	return keep
}

// promptKeywordsRaw returns every surviving keyword (after stopword + length
// filter) without the routineKeywordsPerPrompt cap. Used in pass 1 of
// ClusterPrompts to feed the corpus frequency table; pass 2 narrows each
// prompt to its corpus-frequent intersection.
func promptKeywordsRaw(text string) []string {
	if text == "" {
		return nil
	}
	lower := strings.ToLower(text)
	var rb strings.Builder
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			rb.WriteRune(r)
		default:
			rb.WriteRune(' ')
		}
	}
	tokens := strings.Fields(rb.String())
	seen := map[string]struct{}{}
	var keep []string
	for _, t := range tokens {
		if len(t) < 4 {
			continue
		}
		if _, isStop := promptStopwords[t]; isStop {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		keep = append(keep, t)
	}
	return keep
}

// signatureKeywordsFromFreq takes a prompt's distinct tokens plus the corpus
// frequency table and returns the alpha-sorted top-routineKeywordsPerPrompt
// of the intersection between (prompt tokens) and (corpus-frequent tokens —
// those that appear in ≥2 distinct prompts). This is what collapses
// near-duplicate phrasings ("summarize my unread email this morning" vs
// "summarize unread email please") to ONE signature.
func signatureKeywordsFromFreq(prompt map[string]struct{}, freq map[string]int) []string {
	if len(prompt) == 0 {
		return nil
	}
	var shared []string
	for t := range prompt {
		if freq[t] >= 2 {
			shared = append(shared, t)
		}
	}
	if len(shared) < 2 {
		// Not enough shared vocabulary with the rest of the corpus — fall
		// back to the prompt's own keyword set so a real signal still lands
		// (its cluster will sit at hits=1 until a peer arrives).
		for t := range prompt {
			shared = append(shared, t)
		}
	}
	// Sort by (descending corpus frequency, then alphabetical) and take the
	// top-routineKeywordsPerPrompt. Frequency-first selection means the
	// signature is dominated by tokens shared across the corpus, not by
	// per-prompt accidents.
	sort.SliceStable(shared, func(i, j int) bool {
		if freq[shared[i]] != freq[shared[j]] {
			return freq[shared[i]] > freq[shared[j]]
		}
		return shared[i] < shared[j]
	})
	if len(shared) > routineKeywordsPerPrompt {
		shared = shared[:routineKeywordsPerPrompt]
	}
	sort.Strings(shared)
	return shared
}

func signatureFromKeywords(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	return strings.Join(keys, " ")
}

// promptStopwords is the routine-mining stopword list. Conservative: only
// drop words that are nearly meaningless across requests. A miss here just
// means a slightly noisier signature, not a wrong cluster.
var promptStopwords = map[string]struct{}{
	"a": {}, "about": {}, "above": {}, "after": {}, "again": {}, "all": {},
	"also": {}, "and": {}, "any": {}, "anything": {}, "are": {}, "around": {},
	"because": {}, "been": {}, "before": {}, "being": {}, "both": {}, "but": {},
	"can": {}, "could": {}, "did": {}, "does": {}, "doing": {}, "done": {},
	"down": {}, "each": {}, "few": {}, "for": {}, "from": {}, "further": {},
	"have": {}, "having": {}, "here": {}, "into": {}, "just": {}, "like": {},
	"more": {}, "most": {}, "myself": {}, "needs": {}, "next": {}, "now": {},
	"once": {}, "only": {}, "other": {}, "ours": {}, "out": {}, "over": {},
	"please": {}, "really": {}, "same": {}, "should": {}, "some": {},
	"such": {}, "than": {}, "that": {}, "their": {}, "them": {}, "then": {},
	"there": {}, "these": {}, "they": {}, "this": {}, "those": {}, "through": {},
	"under": {}, "until": {}, "very": {}, "want": {}, "wants": {}, "was": {},
	"were": {}, "what": {}, "when": {}, "where": {}, "which": {}, "while": {},
	"will": {}, "with": {}, "would": {}, "your": {}, "yours": {}, "yourself": {},
	"jarvis": {}, "thanks": {}, "thank": {},
}
