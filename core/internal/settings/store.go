// Package settings persists user-controllable configuration that Studio
// edits at runtime (model selection, etc.). The store wraps the existing
// infinity_meta key-value table so we don't pay for an extra migration
// for what is effectively a tiny key/value bag.
//
// Keys are namespaced with a `setting.` prefix so they don't collide
// with the schema's own bookkeeping rows (schema_initialized_at,
// canvas_projects_initialized_at, etc).
//
// Reads are intentionally hot-path safe: a 1-row indexed lookup on the
// primary key. We don't add an in-memory cache because the WS turn path
// already absorbs a few-millisecond DB read without flinching, and a
// cache would complicate cross-process invalidation when Settings PUTs
// a new value.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// keyPrefix namespaces every setting row so they never collide with
// the schema's own infinity_meta bookkeeping (schema_initialized_at,
// canvas_projects_initialized_at, etc).
const keyPrefix = "setting."

// KeyModel is the active model id the agent loop should use for the
// next turn. Empty / missing means "use the provider's boot default".
const KeyModel = keyPrefix + "model"

// KeyProvider is the active LLM provider id (anthropic / openai /
// openai_oauth / google). Empty / missing means "fall back to the
// LLM_PROVIDER env at boot". This is intentionally separate from any
// stored OAuth credentials: switching provider here doesn't touch
// mem_provider_tokens, so flipping anthropic → openai_oauth → anthropic
// never requires re-auth.
const KeyProvider = keyPrefix + "provider"

// KeyChat stores user-facing controls for the /live chat experience,
// including agent-team orchestration policy.
const KeyChat = keyPrefix + "chat"

type ChatSettings struct {
	AgentTeams            string `json:"agent_teams"`
	TeamAggressiveness    string `json:"team_aggressiveness"`
	ShowTeamActivity      string `json:"show_team_activity"`
	DefaultTeamCardState  string `json:"default_team_card_state"`
	MaxAgentsPerTeam      int    `json:"max_agents_per_team"`
	MaxParallelTeams      int    `json:"max_parallel_teams"`
	MaxRuntimeSeconds     int    `json:"max_runtime_seconds"`
	MaxTeamTokens         int    `json:"max_team_tokens"`
	MaxToolCalls          int    `json:"max_tool_calls"`
	AllowArtifactAgents   bool   `json:"allow_artifact_agents"`
	AllowCodeAgents       bool   `json:"allow_code_agents"`
	AllowConnectorAgents  bool   `json:"allow_connector_agents"`
	RequireActionApproval bool   `json:"require_action_approval"`
	ModelPolicy           string `json:"model_policy"`
	ShowTokenUsage        bool   `json:"show_token_usage"`
	ShowWorkerSummaries   bool   `json:"show_worker_summaries"`
	ShowArtifacts         bool   `json:"show_artifacts"`
}

func DefaultChatSettings() ChatSettings {
	return ChatSettings{
		AgentTeams:            "auto",
		TeamAggressiveness:    "full_tilt",
		ShowTeamActivity:      "detailed",
		DefaultTeamCardState:  "expanded",
		MaxAgentsPerTeam:      6,
		MaxParallelTeams:      2,
		MaxRuntimeSeconds:     600,
		MaxTeamTokens:         120000,
		MaxToolCalls:          120,
		AllowArtifactAgents:   true,
		AllowCodeAgents:       true,
		AllowConnectorAgents:  true,
		RequireActionApproval: true,
		ModelPolicy:           "same_as_chat",
		ShowTokenUsage:        true,
		ShowWorkerSummaries:   true,
		ShowArtifacts:         true,
	}
}

// Store reads and writes user-controllable settings persisted in the
// infinity_meta table. Nil-safe: methods on a nil receiver return
// sensible defaults so wiring without a DB (tests, doctor command)
// doesn't panic.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Get returns the raw string value for the given key (no prefix -
// caller passes the full key constant). Returns ("", false, nil) when
// no row exists for the key. Errors are propagated so the caller can
// distinguish "unset" from "DB unavailable".
func (s *Store) Get(ctx context.Context, key string) (string, bool, error) {
	if s == nil || s.pool == nil {
		return "", false, nil
	}
	var value string
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM infinity_meta WHERE key = $1`, key,
	).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("settings.Get(%s): %w", key, err)
	}
	return value, true, nil
}

// Set upserts the row for `key`. Empty values are allowed and represent
// "explicitly cleared by the user" - distinct from "never set". Callers
// that want fallback-on-empty behavior should branch in their own code.
func (s *Store) Set(ctx context.Context, key, value string) error {
	if s == nil || s.pool == nil {
		return errors.New("settings store not configured")
	}
	if !strings.HasPrefix(key, keyPrefix) {
		return fmt.Errorf("settings.Set: key %q must use %q prefix", key, keyPrefix)
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO infinity_meta (key, value, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (key) DO UPDATE
		   SET value = EXCLUDED.value, updated_at = NOW()`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("settings.Set(%s): %w", key, err)
	}
	return nil
}

// GetModel is a typed convenience that returns the current model id
// for the agent loop. Empty string means "no override; fall back to the
// provider's boot default" - callers should pass it straight through to
// the LLM provider's Stream call which knows what to do.
func (s *Store) GetModel(ctx context.Context) string {
	v, _, err := s.Get(ctx, KeyModel)
	if err != nil {
		// Treat DB errors as "unset" - the agent loop's default model
		// is a perfectly safe fallback and we'd rather degrade than
		// stall a turn over a transient settings read.
		return ""
	}
	return strings.TrimSpace(v)
}

// SetModel updates the active model id. Empty value clears the override
// (next turn uses the provider's boot default). No validation beyond
// trim - the agent loop already surfaces a clean error when the LLM
// provider rejects an unknown id, so the UI gets feedback either way.
func (s *Store) SetModel(ctx context.Context, model string) error {
	return s.Set(ctx, KeyModel, strings.TrimSpace(model))
}

// GetProvider returns the persisted active provider override (or empty
// string when none is set, meaning "ride the LLM_PROVIDER boot default").
func (s *Store) GetProvider(ctx context.Context) string {
	v, _, err := s.Get(ctx, KeyProvider)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(v))
}

// SetProvider updates the active provider override. Pass "" to clear it
// and revert to the boot default. NOTE: this only flips the provider the
// agent loop uses - stored OAuth credentials in mem_provider_tokens are
// untouched, so the user can switch back later without re-authenticating.
func (s *Store) SetProvider(ctx context.Context, provider string) error {
	return s.Set(ctx, KeyProvider, strings.ToLower(strings.TrimSpace(provider)))
}

func (s *Store) GetChatSettings(ctx context.Context) ChatSettings {
	cfg := DefaultChatSettings()
	raw, ok, err := s.Get(ctx, KeyChat)
	if err != nil || !ok || strings.TrimSpace(raw) == "" {
		return cfg
	}
	_ = json.Unmarshal([]byte(raw), &cfg)
	return normalizeChatSettings(cfg)
}

func (s *Store) SetChatSettings(ctx context.Context, cfg ChatSettings) error {
	cfg = normalizeChatSettings(cfg)
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.Set(ctx, KeyChat, string(b))
}

func normalizeChatSettings(cfg ChatSettings) ChatSettings {
	def := DefaultChatSettings()
	switch cfg.AgentTeams {
	case "off", "ask", "auto":
	default:
		cfg.AgentTeams = def.AgentTeams
	}
	switch cfg.TeamAggressiveness {
	case "conservative", "balanced", "full_tilt":
	default:
		cfg.TeamAggressiveness = def.TeamAggressiveness
	}
	switch cfg.ShowTeamActivity {
	case "off", "compact", "detailed":
	default:
		cfg.ShowTeamActivity = def.ShowTeamActivity
	}
	switch cfg.DefaultTeamCardState {
	case "collapsed", "expanded":
	default:
		cfg.DefaultTeamCardState = def.DefaultTeamCardState
	}
	if cfg.MaxAgentsPerTeam <= 0 {
		cfg.MaxAgentsPerTeam = def.MaxAgentsPerTeam
	}
	if cfg.MaxAgentsPerTeam > 12 {
		cfg.MaxAgentsPerTeam = 12
	}
	if cfg.MaxParallelTeams <= 0 {
		cfg.MaxParallelTeams = def.MaxParallelTeams
	}
	if cfg.MaxRuntimeSeconds <= 0 {
		cfg.MaxRuntimeSeconds = def.MaxRuntimeSeconds
	}
	if cfg.MaxRuntimeSeconds > 3600 {
		cfg.MaxRuntimeSeconds = 3600
	}
	if cfg.MaxTeamTokens <= 0 {
		cfg.MaxTeamTokens = def.MaxTeamTokens
	}
	if cfg.MaxToolCalls <= 0 {
		cfg.MaxToolCalls = def.MaxToolCalls
	}
	if cfg.ModelPolicy == "" {
		cfg.ModelPolicy = def.ModelPolicy
	}
	return cfg
}
