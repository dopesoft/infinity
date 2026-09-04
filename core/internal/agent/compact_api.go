package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/memory"
)

// CompactSession is the manual compaction entry point (the Compact action on
// the context meter). Same path the automatic triggers use: tool results are
// cleared first, then the history is summarised against the session's own
// cached prefix, and a brain that holds the conversation itself drops its
// session so the next turn starts from the compacted transcript.
func (l *Loop) CompactSession(ctx context.Context, sessionID string) (memory.CompactionResult, error) {
	if l == nil {
		return memory.CompactionResult{}, errors.New("no agent loop")
	}
	l.mu.Lock()
	s, ok := l.sessions[strings.TrimSpace(sessionID)]
	l.mu.Unlock()
	if !ok || s == nil {
		return memory.CompactionResult{}, errors.New("this conversation is not loaded in memory, so there is nothing to compact yet")
	}
	l.compactorMu.RLock()
	c := l.compactor
	l.compactorMu.RUnlock()
	if c == nil {
		return memory.CompactionResult{}, errors.New("compaction is not wired on this server")
	}
	provider := l.Provider()
	stable := l.systemPrompt
	if o := strings.TrimSpace(s.SystemPromptOverride); o != "" {
		stable = o
	}
	if freed := s.clearOldToolResults(keepToolResults, clearExcludedTools, 1); freed > 0 {
		infoLog.Printf("compact(manual): session=%s cleared older tool results (%d chars freed)", s.ID, freed)
	}
	cfg := &memory.CompactionConfig{Force: true, StableSystem: stable, Tools: l.tools.DefinitionsFor(s.Active.Names())}
	newMsgs, res, err := c.Compact(ctx, s.ID, s.Snapshot(), cfg)
	if err != nil {
		return res, err
	}
	if res.CompactedTurns == 0 {
		return res, nil
	}
	s.ReplaceMessages(newMsgs)
	s.InvalidateUsage()
	if llm.ForgetSessionIfSupported(ctx, provider, s.ID) {
		infoLog.Printf("compact(manual): session=%s brain session dropped", s.ID)
	}
	infoLog.Printf("compact(manual): session=%s compacted %d turns, kept %d, %d observations promoted", s.ID, res.CompactedTurns, res.KeptTurns, len(res.ObservationIDs))
	return res, nil
}
