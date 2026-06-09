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
	Errors                 []StageError             `json:"errors,omitempty"`
	Options                Options                  `json:"options"`
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
		world, err := worldmodel.NewStore(deps.Pool, deps.Logger).ExtractFromRecentObservations(ctx, 100)
		if err != nil {
			if report.HasCoreChanges() {
				addErr("worldmodel_extract", err)
			} else {
				deps.Logger.Warn("nightly cognition skipped world-model extract after no-op run", "err", err)
			}
		} else {
			report.WorldModel = world
		}
	}

	report.EndedAt = time.Now().UTC()
	// The run's boss-facing outcome is surfaced generically by the cron
	// scheduler (surfaceRunOutcome → "Surfaced by Jarvis"), so nightly no
	// longer writes its own bespoke 'system'-surface card — that would
	// double-post and land in the Activity stream instead of the inbox.
	if len(report.Errors) > 0 {
		return report, fmt.Errorf("nightly cognition failed stages: %s", report.ErrorSummary())
	}
	return report, nil
}

// Summary renders the nightly-cognition Report as a boss-facing narrative for
// the mem_runs row + the system surface — "what it did, op by op" instead of a
// bare "ok". The first line is the headline (reflections / compression /
// training / world-model); a second line itemises the sleep-time consolidation
// ops that actually changed something, so the kanban card and /logs run detail
// answer "what the fuck did it do" without digging into surface metadata. Any
// failed stages are named last.
func (r Report) Summary() string {
	body := fmt.Sprintf(
		"Reflected %d session(s), updated %d reflection chain(s), compressed %d observation(s), inserted %d training example(s), upserted %d world-model entit(ies).",
		r.ReflectedSessions,
		r.ReflectionChains,
		r.CompressedObservations,
		r.TrainingExamples.Inserted,
		r.WorldModel.Upserted,
	)
	// Itemise the consolidation ops that did real work; stay silent on the ones
	// that touched nothing so the line reads clean on quiet nights.
	c := r.Consolidate
	var ops []string
	addOp := func(n int, label string) {
		if n > 0 {
			ops = append(ops, fmt.Sprintf("%d %s", n, label))
		}
	}
	addOp(c.Decayed, "decayed")
	addOp(c.HotReset, "hot-reset")
	addOp(c.ClustersFound, "clusters")
	addOp(c.ContradictionsFound, "contradictions resolved")
	addOp(c.AssociativePruned, "edges pruned")
	addOp(c.WeakAssocPurged, "weak edges purged")
	addOp(c.ProceduralReweighted, "skills reweighted")
	f := c.Forget
	addOp(f.TTLExpired+f.LowValue+f.OverProjectCap+f.ObsTraceTrimmed+
		f.ObsConversationTrimmed+f.LessonsTrimmed+f.OperationalTrimmed+f.GraphTrimmed,
		"forgotten")
	if len(ops) > 0 {
		body += " Consolidation: " + strings.Join(ops, ", ") + "."
	}
	if errSummary := r.ErrorSummary(); errSummary != "" {
		body += " Failed stages: " + errSummary + "."
	}
	return body
}

// Changed reports whether the run actually touched anything — memory, lessons,
// training examples, or the world model. The cron executor reads this to label
// the run's outcome "did work" vs. "nothing needed" for the boss's inbox.
func (r Report) Changed() bool {
	return r.HasCoreChanges() ||
		r.TrainingExamples.Inserted > 0 ||
		r.WorldModel.Upserted > 0
}
