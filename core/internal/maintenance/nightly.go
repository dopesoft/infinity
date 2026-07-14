// Package maintenance owns deterministic background maintenance jobs.
//
// These jobs are substrate, not cognition: they call existing typed loops
// (reflection, consolidation, Gym extraction) and write a generic surface
// report. The agent can reason over the report later, but this runner does
// not embed judgment in Go.
package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/plasticity"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/dopesoft/infinity/core/internal/worldmodel"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Deps struct {
	Pool       *pgxpool.Pool
	Reflector  *memory.Reflector
	Compressor *memory.Compressor
	Surface    *surface.Store
	Embedder   embed.Embedder
	Logger     *slog.Logger
	// Drafter is the optional one-shot LLM seam (matches llm.Drafter — the
	// boss's active model). When set, the nightly digest's lead sentence is
	// drafted in Jarvis's voice from the run's real highlights; on any
	// failure the deterministic lead ships instead. Numbers and quoted
	// lessons always come from code, never the LLM.
	Drafter Drafter
}

// Drafter mirrors llm.Drafter locally so maintenance stays free of the llm
// package dependency.
type Drafter interface {
	Draft(ctx context.Context, model, system, userPrompt string, maxTokens int64) (string, error)
}

type Options struct {
	ReflectWindow time.Duration `json:"reflect_window"`
	ReflectLimit  int           `json:"reflect_limit"`
	Compress      bool          `json:"compress"`
	CompressBatch int           `json:"compress_batch"`
	GymLimit      int           `json:"gym_limit"`
}

type StageError struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

func (e StageError) Error() string {
	if strings.TrimSpace(e.Stage) == "" {
		return strings.TrimSpace(e.Message)
	}
	if strings.TrimSpace(e.Message) == "" {
		return strings.TrimSpace(e.Stage)
	}
	return strings.TrimSpace(e.Stage) + ": " + strings.TrimSpace(e.Message)
}

func (e StageError) SummaryLine() string {
	return e.Error()
}

type Report struct {
	StartedAt              time.Time                `json:"started_at"`
	EndedAt                time.Time                `json:"ended_at"`
	ReflectedSessions      int                      `json:"reflected_sessions"`
	ReflectionChains       int                      `json:"reflection_chains"`
	CompressedObservations int                      `json:"compressed_observations"`
	Consolidate            memory.ConsolidateReport `json:"consolidate"`
	TrainingExamples       plasticity.ExtractResult `json:"training_examples"`
	TrainingEmbedded       int                      `json:"training_embedded"`
	WorldModel             worldmodel.ExtractReport `json:"world_model"`
	// People is who he knows: the phone book synced into the world model, plus
	// the durable facts learned about them from what was actually said.
	People worldmodel.PeopleReport `json:"people"`
	// SurfaceExpired counts inbox cards dismissed because their TTL passed —
	// the informational run outcomes and self-heal receipts that carry a 36h
	// expiry precisely so they don't accumulate in "Surfaced by Jarvis".
	SurfaceExpired int          `json:"surface_expired"`
	Errors         []StageError `json:"errors,omitempty"`
	Options        Options      `json:"options"`
	// Highlights carries the run's REAL artifacts (lesson texts, memory
	// titles, entity names) so the boss-facing digest quotes what was
	// actually learned instead of counter soup.
	Highlights Highlights `json:"highlights,omitempty"`
	// Digest is an optional LLM-polished lead sentence (Jarvis voice). Empty
	// when no Drafter is wired or the draft failed; Summary() then falls back
	// to the deterministic lead.
	Digest string `json:"digest,omitempty"`
}

func (r Report) HasCoreChanges() bool {
	return r.ReflectedSessions > 0 ||
		r.ReflectionChains > 0 ||
		r.CompressedObservations > 0 ||
		r.Consolidate.ClustersFound > 0 ||
		r.Consolidate.ContradictionsFound > 0 ||
		r.Consolidate.AssociativePruned > 0 ||
		r.Consolidate.WeakAssocPurged > 0 ||
		r.Consolidate.ProceduralReweighted > 0 ||
		r.Consolidate.Forget.TTLExpired > 0 ||
		r.Consolidate.Forget.LowValue > 0 ||
		r.Consolidate.Forget.OverProjectCap > 0 ||
		r.Consolidate.Forget.ObsTraceTrimmed > 0 ||
		r.Consolidate.Forget.ObsConversationTrimmed > 0
}

func (r Report) ErrorSummary() string {
	if len(r.Errors) == 0 {
		return ""
	}
	parts := make([]string, 0, len(r.Errors))
	for _, stageErr := range r.Errors {
		line := strings.TrimSpace(stageErr.SummaryLine())
		if line == "" {
			continue
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "; ")
}

func DefaultOptions() Options {
	return Options{
		ReflectWindow: 24 * time.Hour,
		ReflectLimit:  20,
		Compress:      true,
		CompressBatch: 50,
		GymLimit:      100,
	}
}

func ParseOptions(raw json.RawMessage) Options {
	opts := DefaultOptions()
	if len(raw) == 0 {
		return opts
	}
	var in struct {
		ReflectWindow string `json:"reflect_window"`
		ReflectLimit  int    `json:"reflect_limit"`
		Compress      *bool  `json:"compress"`
		CompressBatch int    `json:"compress_batch"`
		GymLimit      int    `json:"gym_limit"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return opts
	}
	if d, err := time.ParseDuration(strings.TrimSpace(in.ReflectWindow)); err == nil && d > 0 {
		opts.ReflectWindow = d
	}
	if in.ReflectLimit > 0 {
		opts.ReflectLimit = in.ReflectLimit
	}
	if in.Compress != nil {
		opts.Compress = *in.Compress
	}
	if in.CompressBatch > 0 {
		opts.CompressBatch = in.CompressBatch
	}
	if in.GymLimit > 0 {
		opts.GymLimit = in.GymLimit
	}
	return opts
}

func RunNightlyCognition(ctx context.Context, deps Deps, opts Options) (Report, error) {
	if opts.ReflectWindow <= 0 {
		opts = DefaultOptions()
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	report := Report{
		StartedAt: time.Now().UTC(),
		Options:   opts,
	}
	addErr := func(stage string, err error) {
		if err == nil {
			return
		}
		entry := StageError{Stage: strings.TrimSpace(stage), Message: strings.TrimSpace(err.Error())}
		report.Errors = append(report.Errors, entry)
		deps.Logger.Warn("nightly cognition stage failed", "stage", entry.Stage, "err", entry.Message)
	}

	if deps.Reflector != nil {
		n, err := deps.Reflector.ReflectRecent(ctx, opts.ReflectWindow, opts.ReflectLimit)
		if err != nil {
			addErr("reflect", err)
		} else {
			report.ReflectedSessions = n
		}
		chains, err := deps.Reflector.BuildReflectionChains(ctx, 200)
		if err != nil {
			addErr("reflection_chains", err)
		} else {
			report.ReflectionChains = chains
		}
	}

	if opts.Compress && deps.Compressor != nil {
		n, err := deps.Compressor.CompressNewObservations(ctx, opts.CompressBatch)
		if err != nil {
			addErr("compress", err)
		} else {
			report.CompressedObservations = n
		}
	}

	// Retire inbox cards whose TTL has passed. Informational outcomes ("run
	// done", "self-heal fixed it") are written with a 36h expiry so the boss's
	// inbox stays decisions-only; without this call they never clear and the
	// TTL is decorative. Follow-up emails are boss-owned and exempt inside
	// SweepExpired itself.
	if deps.Surface != nil {
		n, err := deps.Surface.SweepExpired(ctx)
		if err != nil {
			addErr("surface_expire", err)
		} else {
			report.SurfaceExpired = n
		}
	}

	if deps.Pool != nil {
		rep, err := memory.ConsolidateNightly(ctx, deps.Pool)
		if err != nil {
			addErr("consolidate", err)
		} else {
			report.Consolidate = rep
		}
		gymStore := plasticity.NewStore(deps.Pool)
		extract, err := gymStore.ExtractExamples(ctx, opts.GymLimit)
		if err != nil {
			addErr("gym_extract", err)
		} else {
			report.TrainingExamples = extract
		}
		// Embed any rows still missing a vector. Newly inserted rows above
		// land here, plus any backfill from before migration 057. Bounded
		// at 200 per pass to keep embedder load predictable.
		if deps.Embedder != nil {
			n, err := gymStore.EmbedPending(ctx, deps.Embedder, 200)
			if err != nil {
				addErr("gym_embed", err)
			}
			report.TrainingEmbedded = n
		}
		wm := worldmodel.NewStore(deps.Pool, deps.Logger)
		world, err := wm.ExtractFromRecentObservations(ctx, 100)
		if err != nil {
			if report.HasCoreChanges() {
				addErr("worldmodel_extract", err)
			} else {
				deps.Logger.Warn("nightly cognition skipped world-model extract after no-op run", "err", err)
			}
		} else {
			report.WorldModel = world
		}

		// The people in his life. The extractor above is regex-only: it can see an
		// email address, never a person, so everyone the boss actually knows was
		// invisible to the world model. This learns them, and the durable facts
		// about them (a birthday, a relationship), which is what lets Jarvis say
		// "Phumi's birthday is on Friday" a year after the call that taught him.
		if n, err := wm.SyncContacts(ctx); err != nil {
			addErr("contacts_sync", err)
		} else {
			report.People.Contacts = n
		}
		if deps.Drafter != nil {
			people, err := wm.ExtractPeopleFacts(ctx, deps.Drafter, 60)
			if err != nil {
				addErr("people_extract", err)
			} else {
				people.Contacts = report.People.Contacts
				report.People = people
			}
		}
	}

	report.EndedAt = time.Now().UTC()
	// Pull the run's real artifacts back out so the digest can QUOTE them —
	// the lessons written, the memories promoted, the entities now tracked.
	report.Highlights = CollectHighlights(ctx, deps.Pool, report.StartedAt, report.EndedAt)
	// Optional voice pass: let the active model write the one-sentence lead
	// from the real highlights. Deterministic fallback inside Summary().
	report.Digest = draftDigest(ctx, deps.Drafter, report)
	// The run's boss-facing outcome is surfaced generically by the cron
	// scheduler (surfaceRunOutcome → "Surfaced by Jarvis"), so nightly no
	// longer writes its own bespoke 'system'-surface card — that would
	// double-post and land in the Activity stream instead of the inbox.
	if len(report.Errors) > 0 {
		return report, fmt.Errorf("nightly cognition failed stages: %s", report.ErrorSummary())
	}
	return report, nil
}

// Summary renders the nightly-cognition Report as the boss-facing digest for
// the mem_runs row + the "Surfaced by Jarvis" inbox. Shape:
//
//	<one-sentence lead in plain first-person English>
//
//	What I learned:
//	- <actual lesson text quoted from the run's reflections>
//
//	Worth remembering:
//	- <title of a new durable memory>
//
//	Now tracking: <new world-model entity names>
//
//	Tidy-up: <the maintenance counts, written like a person>
//
// The first line doubles as the inbox row's one-line reason; the labeled
// sections render as cards in the Studio modal (parseLabeledBody). No counter
// soup, no jargon, no em-dashes — the boss reads this, not a dev.
func (r Report) Summary() string {
	lead := strings.TrimSpace(r.Digest)
	if lead == "" {
		lead = r.lead()
	}
	sections := r.sections()
	if sections == "" {
		return lead
	}
	return lead + "\n\n" + sections
}

// lead is the deterministic one-sentence gist (the Digest's fallback).
func (r Report) lead() string {
	lessons := len(r.Highlights.Lessons)
	hasTrouble := len(r.Errors) > 0
	var s string
	switch {
	case lessons > 0 && r.ReflectedSessions > 0:
		s = fmt.Sprintf("Last night I went back over %s and came away with %s, then tidied up my memory.",
			countOf(r.ReflectedSessions, "of yesterday's sessions", "of yesterday's sessions"),
			countOf(lessons, "new lesson", "new lessons"))
	case lessons > 0:
		s = fmt.Sprintf("Last night I picked up %s and tidied up my memory.",
			countOf(lessons, "new lesson", "new lessons"))
	case r.tidyUp() != "":
		s = "Nothing new to learn last night, but I gave my memory a good tidy-up."
	default:
		s = "A quiet night. I checked my memory and there was nothing new to learn."
	}
	if hasTrouble {
		s += " I hit some trouble along the way; details below."
	}
	return s
}

// sections renders the labeled detail blocks. Empty when there is genuinely
// nothing to show (a fully quiet night).
func (r Report) sections() string {
	var blocks []string
	if len(r.Highlights.Lessons) > 0 {
		blocks = append(blocks, "What I learned:\n"+bulleted(r.Highlights.Lessons))
	}
	if len(r.Highlights.Memories) > 0 {
		blocks = append(blocks, "Worth remembering:\n"+bulleted(r.Highlights.Memories))
	}
	if len(r.Highlights.Entities) > 0 {
		blocks = append(blocks, "Now tracking: "+strings.Join(r.Highlights.Entities, ", "))
	}
	if tidy := r.tidyUp(); tidy != "" {
		blocks = append(blocks, "Tidy-up: "+tidy)
	}
	if errSummary := r.ErrorSummary(); errSummary != "" {
		blocks = append(blocks,
			"Trouble: I could not finish everything. These steps hit problems: "+errSummary+". I will try them again tomorrow night.")
	}
	return strings.Join(blocks, "\n\n")
}

// tidyUp turns the maintenance counters into one human sentence. Zero-count
// operations stay silent so a quiet night reads clean.
func (r Report) tidyUp() string {
	c := r.Consolidate
	f := c.Forget
	forgotten := f.TTLExpired + f.LowValue + f.OverProjectCap + f.ObsTraceTrimmed +
		f.ObsConversationTrimmed + f.LessonsTrimmed + f.OperationalTrimmed + f.GraphTrimmed
	var clauses []string
	add := func(n int, singular, plural string) {
		if n > 0 {
			clauses = append(clauses, countOf(n, singular, plural))
		}
	}
	add(r.CompressedObservations, "raw note turned into a lasting memory", "raw notes turned into lasting memories")
	add(c.Decayed, "stale memory left to fade", "stale memories left to fade")
	add(c.HotReset, "memory refreshed because it came up again", "memories refreshed because they came up again")
	add(c.ClustersFound, "set of related memories grouped together", "sets of related memories grouped together")
	add(c.ContradictionsFound, "conflicting fact settled", "conflicting facts settled")
	add(c.AssociativePruned+c.WeakAssocPurged, "weak link between memories trimmed", "weak links between memories trimmed")
	add(c.ProceduralReweighted, "skill re-ranked by how well it has been working", "skills re-ranked by how well they have been working")
	add(forgotten, "memory I no longer need cleared out", "memories I no longer need cleared out")
	add(r.TrainingExamples.Inserted, "new example saved to learn from", "new examples saved to learn from")
	add(r.WorldModel.Upserted, "thing I track about your world updated", "things I track about your world updated")
	if len(clauses) == 0 {
		return ""
	}
	return joinAnd(clauses) + "."
}

// countOf renders "1 <singular>" / "N <plural>".
func countOf(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// joinAnd renders a human comma list: "a", "a and b", "a, b, and c".
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
}

func bulleted(items []string) string {
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("- ")
		b.WriteString(it)
	}
	return b.String()
}

// draftDigest asks the active model for the one-sentence lead, grounded in the
// run's REAL highlights and counts. Strictly optional: any failure, empty
// output, or out-of-shape output (too long, multi-paragraph) returns "" and
// the deterministic lead ships. The LLM never produces numbers or lessons —
// only the lead's wording.
func draftDigest(ctx context.Context, d Drafter, r Report) string {
	if d == nil {
		return ""
	}
	dctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	system := "You are Jarvis, the boss's AI companion, writing the one-line headline of your overnight memory review. " +
		"Write ONE sentence (two short ones at most), first person, plain conversational English, refined and dry. " +
		"Synthesize the THEME of what you learned from the material given; do not list counts, do not enumerate, " +
		"do not use jargon, bullet points, em-dashes, exclamation marks, or greetings. Return ONLY the sentence."
	user := "Deterministic lead (fallback you are replacing): " + r.lead() + "\n\n" +
		"Lessons I wrote down tonight:\n" + bulleted(r.Highlights.Lessons) + "\n\n" +
		"New memories worth keeping:\n" + bulleted(r.Highlights.Memories)
	out, err := d.Draft(dctx, "", system, user, 200)
	if err != nil {
		return ""
	}
	out = strings.TrimSpace(strings.Trim(strings.TrimSpace(out), `"`))
	if out == "" || len(out) > 300 || strings.Contains(out, "\n") {
		return ""
	}
	return out
}

// Changed reports whether the run actually touched anything — memory, lessons,
// training examples, or the world model. The cron executor reads this to label
// the run's outcome "did work" vs. "nothing needed" for the boss's inbox.
func (r Report) Changed() bool {
	return r.HasCoreChanges() ||
		r.TrainingExamples.Inserted > 0 ||
		r.WorldModel.Upserted > 0
}
