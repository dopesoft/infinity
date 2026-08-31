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

// Implemented reports false: this provider is a stub. Every Stream call
// returns ErrNotImplemented, so a registry that registered it would put a
// brain in the vendor picker that cannot answer a single turn - a credential
// accepted, a vendor shown as ready, and every conversation dead on arrival.
// Declaring it here lets the registry and the Settings API tell the truth
// without either of them knowing which vendor is the stub.
func (g *Google) Implemented() bool { return false }

func (g *Google) Stream(_ context.Context, _ string, _ string, _ []Message, _ []ToolDef, out chan<- StreamEvent) (Response, error) {
	emit(out, StreamEvent{Kind: StreamError, Err: "google provider: " + ErrNotImplemented.Error()})
	return Response{}, ErrNotImplemented
}
