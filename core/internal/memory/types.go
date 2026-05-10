package memory

import "time"

type Tier string

const (
	TierWorking    Tier = "working"
	TierEpisodic   Tier = "episodic"
	TierSemantic   Tier = "semantic"
	TierProcedural Tier = "procedural"
)

type Status string

const (
	StatusActive     Status = "active"
	StatusSuperseded Status = "superseded"
	StatusArchived   Status = "archived"
)

type Session struct {
	ID        string         `json:"id"`
	Project   string         `json:"project,omitempty"`
	StartedAt time.Time      `json:"started_at"`
	EndedAt   *time.Time     `json:"ended_at,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type Observation struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id"`
	HookName   string         `json:"hook_name"`
	Payload    map[string]any `json:"payload,omitempty"`
	RawText    string         `json:"raw_text"`
	Importance int            `json:"importance"`
	CreatedAt  time.Time      `json:"created_at"`
}

type Memory struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Content        string    `json:"content"`
	Tier           Tier      `json:"tier"`
	Version        int       `json:"version"`
	SupersededBy   *string   `json:"superseded_by,omitempty"`
	Status         Status    `json:"status"`
	Strength       float32   `json:"strength"`
	Importance     int       `json:"importance"`
	Project        string    `json:"project,omitempty"`
	ForgetAfter    *time.Time `json:"forget_after,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	LastAccessedAt time.Time `json:"last_accessed_at"`
}

type Relation struct {
	ID         string    `json:"id"`
	SourceID   string    `json:"source_id"`
	TargetID   string    `json:"target_id"`
	Type       string    `json:"relation_type"`
	Confidence float32   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}

type GraphNode struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	StaleFlag bool           `json:"stale_flag"`
	CreatedAt time.Time      `json:"created_at"`
}

type GraphEdge struct {
	ID         string    `json:"id"`
	SourceID   string    `json:"source_id"`
	TargetID   string    `json:"target_id"`
	EdgeType   string    `json:"edge_type"`
	Confidence float32   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}

type AuditEntry struct {
	ID          string         `json:"id"`
	Operation   string         `json:"operation"`
	TargetTable string         `json:"target_table"`
	TargetID    string         `json:"target_id"`
	Actor       string         `json:"actor"`
	Diff        map[string]any `json:"diff,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type SearchResult struct {
	ObservationID string  `json:"observation_id"`
	SessionID     string  `json:"session_id"`
	HookName      string  `json:"hook_name"`
	RawText       string  `json:"raw_text"`
	CreatedAt     time.Time `json:"created_at"`
	Score         float64 `json:"score"`
	Streams       []string `json:"streams"` // ["bm25","vector","graph"]
}

type SearchOpts struct {
	Limit            int
	Project          string
	TimeWindow       time.Duration
	IncludeBreakdown bool
}

func (o SearchOpts) limit() int {
	if o.Limit <= 0 {
		return 10
	}
	return o.Limit
}

type ProvenanceChain struct {
	Memory     Memory       `json:"memory"`
	Sources    []Provenance `json:"sources"`
	Confidence float64      `json:"confidence"`
}

type Provenance struct {
	ObservationID string    `json:"observation_id"`
	SessionID     string    `json:"session_id"`
	Excerpt       string    `json:"excerpt"`
	CreatedAt     time.Time `json:"created_at"`
	Confidence    float64   `json:"confidence"`
}
