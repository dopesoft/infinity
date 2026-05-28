package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CaptureHook implements the privacy-first capture pipeline (PDF p.20):
//
//  1. SHA-256 dedup with 5-minute window
//  2. privacy.StripSecrets (regex + <private> tags)
//  3. Insert raw observation
//  4. (Optional) compress to mem_memories via Claude Haiku - gated by
//     INFINITY_AUTO_COMPRESS=true. Async; never blocks the agent loop.
//  5. Generate vector embedding (best-effort)
//  6. Audit
type CaptureHook struct {
	pool       *pgxpool.Pool
	store      *memory.Store
	embedder   embed.Embedder
	auditor    *memory.Auditor
	compressor *memory.Compressor
	dedup      *dedupCache
	autoCompress bool
}

// CaptureOptions wires the optional pieces in. Compressor may be nil.
type CaptureOptions struct {
	Compressor *memory.Compressor
}

func NewCaptureHook(pool *pgxpool.Pool, store *memory.Store, embedder embed.Embedder, opts CaptureOptions) *CaptureHook {
	return &CaptureHook{
		pool:         pool,
		store:        store,
		embedder:     embedder,
		auditor:      memory.NewAuditor(pool),
		compressor:   opts.Compressor,
		dedup:        newDedupCache(5 * time.Minute),
		autoCompress: opts.Compressor != nil && envTrue("INFINITY_AUTO_COMPRESS"),
	}
}

func envTrue(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func (c *CaptureHook) Name() string { return "capture" }

func (c *CaptureHook) Fire(ctx context.Context, ev Event) error {
	if c.pool == nil || c.store == nil {
		return nil
	}

	// Skip persisting events whose only purpose is to fan out to other
	// listeners (predict recorder, voyager session-end, etc). These rows
	// either duplicate state already on mem_sessions/mem_turns or have no
	// retrieval value, and at 30-min heartbeat × N tool calls per turn they
	// dominate mem_observations row count. The pipeline still emits them,
	// so non-capture hooks downstream are unaffected.
	if !shouldCaptureObservation(ev.Name) {
		return nil
	}

	raw := strings.TrimSpace(ev.Text)
	if raw == "" {
		raw = previewPayload(ev.Payload)
	}
	if raw == "" {
		return nil
	}

	hash := sha256Hex(string(ev.Name) + "|" + ev.SessionID + "|" + raw)
	if c.dedup.seen(hash) {
		return nil
	}

	cleaned, _ := memory.StripSecrets(raw)

	sessionID := ev.SessionID
	if sessionID == "" {
		// Sessionless background work (calendar sync, connector poller)
		// fires PostToolUse hooks with no chat session - there's nothing
		// to capture against a session, so skip silently rather than
		// logging an error every tick. Mirrors PredictionStore's no-session
		// behaviour.
		return nil
	}
	if _, err := c.store.EnsureSession(ctx, sessionID, ev.Project); err != nil {
		return err
	}

	var emb []float32
	if c.embedder != nil {
		emb, _ = c.embedder.Embed(ctx, cleaned)
	}

	// turn_id flows through the payload from the agent loop's fireHookT
	// helper. Pulled out + stamped onto the observation so /logs detail
	// pages can rebuild the timeline by joining on turn_id.
	turnID, _ := ev.Payload["turn_id"].(string)

	obsID, err := c.store.InsertObservation(ctx, memory.ObservationInput{
		SessionID:  sessionID,
		HookName:   string(ev.Name),
		Payload:    ev.Payload,
		RawText:    cleaned,
		Embedding:  emb,
		Importance: importanceFor(ev.Name),
		TurnID:     turnID,
	})
	if err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}

	_ = c.auditor.Log(ctx, "create", "mem_observations", obsID, "hook:"+string(ev.Name),
		map[string]any{"hook": ev.Name, "session": sessionID})

	// Async compression. Never blocks; failures log and drop.
	if c.autoCompress && c.compressor != nil && shouldCompress(ev.Name) {
		go func(id, project string) {
			cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := c.compressor.Compress(cctx, id, project); err != nil {
				fmt.Printf("[capture] compress %s: %v\n", id, err)
			}
		}(obsID, ev.Project)
	}

	return nil
}

// shouldCaptureObservation filters out events that have no retrieval or audit
// value beyond what mem_sessions / mem_turns / mem_trust_contracts already
// carry. The pipeline still fires every event so subscribed hooks
// (PredictionRecorder, Voyager extractor, IntentFlow) run as before; only
// the row-write into mem_observations is suppressed.
func shouldCaptureObservation(name EventName) bool {
	switch name {
	case SessionStart, SessionEnd, Stop, Notification, PreCompact,
		SubagentStart, SubagentStop:
		return false
	}
	return true
}

// shouldCompress filters out high-volume, low-value events to keep Haiku
// costs reasonable. Only events that carry user intent or model output go through.
func shouldCompress(name EventName) bool {
	switch name {
	case UserPromptSubmit, TaskCompleted, PostToolUseFailure:
		return true
	default:
		return false
	}
}

func importanceFor(name EventName) int {
	switch name {
	case UserPromptSubmit, TaskCompleted:
		return 7
	case PostToolUseFailure:
		return 8
	case SessionStart, SessionEnd:
		return 4
	default:
		return 5
	}
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func previewPayload(p map[string]any) string {
	if len(p) == 0 {
		return ""
	}
	parts := make([]string, 0, len(p))
	for k, v := range p {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		if len(parts) >= 6 {
			break
		}
	}
	return strings.Join(parts, " ")
}

type dedupCache struct {
	mu     sync.Mutex
	window time.Duration
	seen_  map[string]time.Time
}

func newDedupCache(window time.Duration) *dedupCache {
	return &dedupCache{window: window, seen_: make(map[string]time.Time)}
}

func (c *dedupCache) seen(hash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, t := range c.seen_ {
		if now.Sub(t) > c.window {
			delete(c.seen_, k)
		}
	}
	if t, ok := c.seen_[hash]; ok && now.Sub(t) < c.window {
		return true
	}
	c.seen_[hash] = now
	return false
}
