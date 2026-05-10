package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CaptureHook implements the privacy-first capture pipeline from the spec
// (PDF p.20):
//
//  1. SHA-256 dedup with 5-minute window
//  2. privacy.StripSecrets (regex + <private> tags)
//  3. Insert raw observation
//  4. (LLM-compress + JSON validate happens in a Phase 4 background worker —
//     for now we record the raw observation and let consolidate.go promote
//     it later.)
//  5. Generate vector embedding (best-effort)
//  6. Audit
type CaptureHook struct {
	pool     *pgxpool.Pool
	store    *memory.Store
	embedder embed.Embedder
	auditor  *memory.Auditor
	dedup    *dedupCache
}

func NewCaptureHook(pool *pgxpool.Pool, store *memory.Store, embedder embed.Embedder) *CaptureHook {
	return &CaptureHook{
		pool:     pool,
		store:    store,
		embedder: embedder,
		auditor:  memory.NewAuditor(pool),
		dedup:    newDedupCache(5 * time.Minute),
	}
}

func (c *CaptureHook) Name() string { return "capture" }

func (c *CaptureHook) Fire(ctx context.Context, ev Event) error {
	if c.pool == nil || c.store == nil {
		return nil // memory disabled — silently skip
	}

	// Build raw text from text + payload preview
	raw := strings.TrimSpace(ev.Text)
	if raw == "" {
		raw = previewPayload(ev.Payload)
	}
	if raw == "" {
		return nil
	}

	// 1. Dedup
	hash := sha256Hex(string(ev.Name) + "|" + ev.SessionID + "|" + raw)
	if c.dedup.seen(hash) {
		return nil
	}

	// 2. Privacy
	cleaned, _ := memory.StripSecrets(raw)

	// 3. Ensure session
	sessionID := ev.SessionID
	if sessionID == "" {
		return errors.New("capture: session id required")
	}
	if _, err := c.store.EnsureSession(ctx, sessionID, ev.Project); err != nil {
		return err
	}

	// 5. Embedding (best-effort)
	var emb []float32
	if c.embedder != nil {
		emb, _ = c.embedder.Embed(ctx, cleaned)
	}

	// 4. Insert observation
	obsID, err := c.store.InsertObservation(ctx, memory.ObservationInput{
		SessionID:  sessionID,
		HookName:   string(ev.Name),
		Payload:    ev.Payload,
		RawText:    cleaned,
		Embedding:  emb,
		Importance: importanceFor(ev.Name),
	})
	if err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}

	// 6. Audit
	_ = c.auditor.Log(ctx, "create", "mem_observations", obsID, "hook:"+string(ev.Name),
		map[string]any{"hook": ev.Name, "session": sessionID})
	return nil
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
