package llm

import "context"

type OpenAI struct {
	apiKey string
	model  string
}

func NewOpenAI(apiKey, model string) *OpenAI {
	if model == "" {
		model = "gpt-5"
	}
	return &OpenAI{apiKey: apiKey, model: model}
}

func (o *OpenAI) Name() string  { return "openai" }
func (o *OpenAI) Model() string { return o.model }

// Stream is a stub for Phase 1. Wired in Phase 4 polish.
func (o *OpenAI) Stream(_ context.Context, _ string, _ []Message, _ []ToolDef, out chan<- StreamEvent) (Response, error) {
	emit(out, StreamEvent{Kind: StreamError, Err: "openai provider: " + ErrNotImplemented.Error()})
	return Response{}, ErrNotImplemented
}
