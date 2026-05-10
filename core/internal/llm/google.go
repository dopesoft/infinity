package llm

import "context"

type Google struct {
	apiKey string
	model  string
}

func NewGoogle(apiKey, model string) *Google {
	if model == "" {
		model = "gemini-2.5-pro"
	}
	return &Google{apiKey: apiKey, model: model}
}

func (g *Google) Name() string  { return "google" }
func (g *Google) Model() string { return g.model }

func (g *Google) Stream(_ context.Context, _ string, _ []Message, _ []ToolDef, out chan<- StreamEvent) (Response, error) {
	emit(out, StreamEvent{Kind: StreamError, Err: "google provider: " + ErrNotImplemented.Error()})
	return Response{}, ErrNotImplemented
}
