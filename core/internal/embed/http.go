package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTP posts to a sidecar service at $INFINITY_EMBED_URL/embed expecting:
//
//	POST /embed  {"text":"..."}  →  {"embedding":[float32; 384]}
//
// Run e.g. the Python sidecar in docker/embed.Dockerfile.
type HTTP struct {
	url    string
	client *http.Client
}

func NewHTTP(url string) *HTTP {
	return &HTTP{
		url:    strings.TrimRight(url, "/"),
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (h *HTTP) Name() string { return "http" }
func (h *HTTP) Dim() int     { return Dim }

type embedReq struct {
	Text string `json:"text"`
}

type embedResp struct {
	Embedding []float32 `json:"embedding"`
}

func (h *HTTP) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, ErrEmptyText
	}
	body, _ := json.Marshal(embedReq{Text: text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("embed sidecar %d: %s", resp.StatusCode, string(raw))
	}

	var out embedResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embedding) != Dim {
		return nil, fmt.Errorf("embed sidecar returned %d dims, want %d", len(out.Embedding), Dim)
	}
	return out.Embedding, nil
}
