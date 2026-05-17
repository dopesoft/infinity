package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// OpenAI calls the OpenAI Embeddings API. We use text-embedding-3-small with
// the `dimensions: 384` parameter so the returned vector matches Infinity's
// hardcoded schema dim - no migrations, drop-in upgrade from the stub.
//
// Cost (Nov 2025 pricing): ~$0.02 per 1M tokens. A typical observation is
// 50–500 tokens, so a busy month is well under a dollar.
//
// Set OPENAI_API_KEY + INFINITY_EMBED=openai to enable. Optional:
//
//	OPENAI_EMBED_MODEL  (default: text-embedding-3-small)
//	OPENAI_API_BASE     (default: https://api.openai.com/v1)
type OpenAI struct {
	apiKey string
	model  string
	base   string
	client *http.Client
}

func NewOpenAI() *OpenAI {
	model := strings.TrimSpace(os.Getenv("OPENAI_EMBED_MODEL"))
	if model == "" {
		model = "text-embedding-3-small"
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("OPENAI_API_BASE")), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &OpenAI{
		apiKey: strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		model:  model,
		base:   base,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (o *OpenAI) Name() string { return "openai" }
func (o *OpenAI) Dim() int     { return Dim }

type openAIEmbedReq struct {
	Input          string `json:"input"`
	Model          string `json:"model"`
	Dimensions     int    `json:"dimensions"`
	EncodingFormat string `json:"encoding_format"`
}

type openAIEmbedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (o *OpenAI) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, ErrEmptyText
	}
	if o.apiKey == "" {
		return nil, fmt.Errorf("openai embedder: OPENAI_API_KEY not set")
	}

	body, _ := json.Marshal(openAIEmbedReq{
		Input:          text,
		Model:          o.model,
		Dimensions:     Dim,
		EncodingFormat: "float",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("openai embed %d: %s", resp.StatusCode, string(raw))
	}

	var out openAIEmbedResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("openai embed: %s", out.Error.Message)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) != Dim {
		return nil, fmt.Errorf("openai embed: expected %d dims, got %d", Dim, lenSafe(out.Data))
	}
	return out.Data[0].Embedding, nil
}

func lenSafe(d []struct {
	Embedding []float32 `json:"embedding"`
}) int {
	if len(d) == 0 {
		return 0
	}
	return len(d[0].Embedding)
}
