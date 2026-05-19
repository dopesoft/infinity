// Package skills implements the Infinity skills system.
//
// A "skill" is a packaged capability the agent can invoke at runtime. Skills
// live as `<name>/SKILL.md` directories under a configurable root (default
// `./skills`) with YAML frontmatter + Markdown body. Inspired by OpenClaw and
// Hermes - same path convention, so OpenClaw skills can be symlinked in
// unmodified.
package skills

import (
	"time"
)

// RiskLevel maps directly to a sandbox tier when the skill executes.
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

func (r RiskLevel) Valid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	}
	return false
}

// Source records where the skill came from. auto_evolved is set by the Voyager loop.
type Source string

const (
	SourceManual    Source = "manual"
	SourceOpenClaw  Source = "openclaw_imported"
	SourceHermes    Source = "hermes_imported"
	SourceAuto      Source = "auto_evolved"
	SourceCandidate Source = "curriculum_proposed"
	// SourceAgent marks a skill the agent authored at runtime via the
	// skill_create tool - the closed-loop self-authoring path.
	SourceAgent Source = "agent_authored"
)

// Status tracks the agent-facing lifecycle. Active skills are invocable;
// candidates require Trust-Contract approval (gated by Trust Contract); archived skills are
// kept for history but never invoked.
type Status string

const (
	StatusActive    Status = "active"
	StatusCandidate Status = "candidate"
	StatusArchived  Status = "archived"
)

// IODef declares an input or output parameter on a skill. Loosely typed -
// "string", "int", "float", "bool", "object" - with optional default.
type IODef struct {
	Name     string `yaml:"name" json:"name"`
	Type     string `yaml:"type" json:"type"`
	Default  any    `yaml:"default,omitempty" json:"default,omitempty"`
	Required bool   `yaml:"required,omitempty" json:"required"`
	Doc      string `yaml:"doc,omitempty" json:"doc,omitempty"`
}

// Frontmatter is the strict-parsed YAML block at the top of a SKILL.md file.
type Frontmatter struct {
	Name           string    `yaml:"name"`
	Version        string    `yaml:"version"`
	Description    string    `yaml:"description"`
	TriggerPhrases []string  `yaml:"trigger_phrases"`
	Inputs         []IODef   `yaml:"inputs"`
	Outputs        []IODef   `yaml:"outputs"`
	RiskLevel      RiskLevel `yaml:"risk_level"`
	NetworkEgress  any       `yaml:"network_egress"` // "none" | []string
	LastEvolved    string    `yaml:"last_evolved,omitempty"`
	Confidence     float64   `yaml:"confidence,omitempty"`
}

// Skill is the runtime view of a skill - frontmatter + body + provenance.
type Skill struct {
	Name             string    `json:"name"`
	Version          string    `json:"version"`
	Description      string    `json:"description"`
	TriggerPhrases   []string  `json:"trigger_phrases"`
	Inputs           []IODef   `json:"inputs"`
	Outputs          []IODef   `json:"outputs"`
	RiskLevel        RiskLevel `json:"risk_level"`
	NetworkEgress    []string  `json:"network_egress"` // empty = none
	Confidence       float64   `json:"confidence"`
	Importance       int       `json:"importance"`
	ImportanceReason string    `json:"importance_reason,omitempty"`
	LastEvolved      string    `json:"last_evolved,omitempty"`

	Body         string `json:"body"`
	ImplPath     string `json:"impl_path,omitempty"` // optional implementation file
	ImplLanguage string `json:"impl_language,omitempty"`

	Source   Source    `json:"source"`
	Status   Status    `json:"status"`
	Path     string    `json:"path,omitempty"`
	LoadedAt time.Time `json:"loaded_at,omitempty"`
}

// SkillSummary is the row shape returned by skills.list and the Studio
// Skills tab list view.
type SkillSummary struct {
	Name             string     `json:"name"`
	Version          string     `json:"version"`
	Description      string     `json:"description"`
	RiskLevel        RiskLevel  `json:"risk_level"`
	Confidence       float64    `json:"confidence"`
	Source           Source     `json:"source"`
	Status           Status     `json:"status"`
	NetworkEgress    []string   `json:"network_egress"`
	Importance       int        `json:"importance"`
	ImportanceReason string     `json:"importance_reason,omitempty"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`
	SuccessRate      float64    `json:"success_rate"`
}

// SkillDetailDTO is the shape returned by GET /api/skills/:name. It
// embeds the in-memory Skill so every existing field stays available
// to the Studio detail surface, plus extras the store knows about
// (created_at / updated_at from mem_skills) and a sample of the most
// recent run so the Overview tab can render the boss's "show me what
// the last run looked like" expectation without a second HTTP call.
type SkillDetailDTO struct {
	*Skill
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	LastRun   *Run       `json:"last_run,omitempty"`
	TotalRuns int        `json:"total_runs"`
}

// VersionEntry is one row in the skill's version history. Returned by
// GET /api/skills/:name/versions. Active marks the currently-live
// version per the mem_skill_active pointer.
type VersionEntry struct {
	Version    string    `json:"version"`
	SkillMD    string    `json:"skill_md"`
	CreatedAt  time.Time `json:"created_at"`
	Active     bool      `json:"active"`
	Source     string    `json:"source,omitempty"`
	Confidence float64   `json:"confidence"`
}

// Match is a skill matched by trigger fuzzy-matching against a user message.
type Match struct {
	Skill  *Skill  `json:"skill"`
	Score  float64 `json:"score"`
	Phrase string  `json:"phrase"`
}

// SandboxOpts is what the runner hands to a sandbox tier.
type SandboxOpts struct {
	Timeout      time.Duration
	MemoryMB     int
	CPULimit     float64
	AllowedEnv   []string
	NetworkAllow []string
	WorkDir      string
}

// Result is the execution result of a skill run.
type Result struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr,omitempty"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Success    bool   `json:"success"`
}

// Run is the durable record of a single skill execution. Persisted to
// mem_skill_runs for the run-history view.
type Run struct {
	ID            string         `json:"id"`
	SkillName     string         `json:"skill_name"`
	Version       string         `json:"version,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	TriggerSource string         `json:"trigger_source"`
	Input         map[string]any `json:"input"`
	Output        string         `json:"output"`
	Success       bool           `json:"success"`
	DurationMS    int64          `json:"duration_ms"`
	StartedAt     time.Time      `json:"started_at"`
	EndedAt       *time.Time     `json:"ended_at,omitempty"`
}

type TestCase struct {
	ID          string         `json:"id"`
	SkillName   string         `json:"skill_name"`
	Description string         `json:"description"`
	Inputs      map[string]any `json:"inputs"`
	Expected    string         `json:"expected"`
	LastRunAt   *time.Time     `json:"last_run_at,omitempty"`
	LastPassed  *bool          `json:"last_passed,omitempty"`
	Source      string         `json:"source"`
	CreatedAt   time.Time      `json:"created_at"`
}
