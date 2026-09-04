package agent

// ActiveToolNames returns the names in the session's active tool set, or nil
// when the session is unknown to this loop. The context meter uses it to
// size the tool schemas the way the loop actually sends them
// (DefinitionsFor(active)); sizing the whole registry instead over-reported
// tools by an order of magnitude and clamped the messages slice to zero.
func (l *Loop) ActiveToolNames(sessionID string) []string {
	if l == nil || sessionID == "" {
		return nil
	}
	l.mu.Lock()
	s, ok := l.sessions[sessionID]
	l.mu.Unlock()
	if !ok || s == nil || s.Active == nil {
		return nil
	}
	return s.Active.Names()
}
