package hooks

// PipelineAdapter implements agent.HookEmitter without coupling agent to hooks.
type PipelineAdapter struct {
	P *Pipeline
}

func (a *PipelineAdapter) Emit(name, sessionID, project, text string, payload map[string]any) {
	if a == nil || a.P == nil {
		return
	}
	a.P.Emit(Event{
		Name:      EventName(name),
		SessionID: sessionID,
		Project:   project,
		Text:      text,
		Payload:   payload,
	})
}
