// Package embed exposes the Embedder interface used by the memory subsystem.
//
// Three implementations:
//   - Stub: deterministic 384-dim hash embedding for dev/test (no model load)
//   - HTTP: posts to a sidecar Python FastAPI service that returns 384-dim
//   - ONNX: github.com/clems4ever/all-minilm-l6-v2-go (build-tag gated; the
//     stub package compiles without it on dev machines that don't have
//     libonnxruntime installed). Wire under build tag `onnx` if needed.
//
// Picked at runtime via INFINITY_EMBED env:
//   - "stub"   (default; deterministic hash, dev-only)
//   - "http"   (sidecar; INFINITY_EMBED_URL points at docker/embed.Dockerfile)
//   - "openai" (OpenAI text-embedding-3-small with dim=384; needs OPENAI_API_KEY)
//   - "onnx"   (in-process MiniLM; build-tag gated)
package embed

import (
	"context"
	"errors"
	"os"
	"strings"
)

const Dim = 384

type Embedder interface {
	Name() string
	Dim() int
	Embed(ctx context.Context, text string) ([]float32, error)
}

func FromEnv() Embedder {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("INFINITY_EMBED")))
	switch mode {
	case "http":
		url := strings.TrimSpace(os.Getenv("INFINITY_EMBED_URL"))
		if url == "" {
			return NewStub()
		}
		return NewHTTP(url)
	case "openai":
		if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
			return NewStub()
		}
		return NewOpenAI()
	case "stub", "":
		return NewStub()
	default:
		// onnx etc. would go here under build tags; fall back to stub.
		return NewStub()
	}
}

var ErrEmptyText = errors.New("embed: empty text")
