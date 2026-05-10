package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type WebSearch struct {
	apiKey string
	client *http.Client
}

func NewWebSearchFromEnv() (*WebSearch, error) {
	key := strings.TrimSpace(os.Getenv("TAVILY_API_KEY"))
	if key == "" {
		return nil, errors.New("TAVILY_API_KEY not set")
	}
	return &WebSearch{
		apiKey: key,
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (w *WebSearch) Name() string        { return "web_search" }
func (w *WebSearch) Description() string { return "Search the web via Tavily. Returns titles, URLs, and short content excerpts." }

func (w *WebSearch) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":       map[string]any{"type": "string", "description": "Natural language search query."},
			"num_results": map[string]any{"type": "integer", "default": 5, "minimum": 1, "maximum": 10},
		},
		"required": []string{"query"},
	}
}

type tavilyRequest struct {
	APIKey   string `json:"api_key"`
	Query    string `json:"query"`
	MaxRes   int    `json:"max_results"`
	Depth    string `json:"search_depth"`
	IncludeA bool   `json:"include_answer"`
}

type tavilyResponse struct {
	Answer  string `json:"answer"`
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
		Score   float64 `json:"score"`
	} `json:"results"`
}

func (w *WebSearch) Execute(ctx context.Context, input map[string]any) (string, error) {
	q, _ := input["query"].(string)
	if q == "" {
		return "", errors.New("query is required")
	}
	num := 5
	if n, ok := input["num_results"].(float64); ok && n > 0 {
		num = int(n)
		if num > 10 {
			num = 10
		}
	}

	body, _ := json.Marshal(tavilyRequest{
		APIKey: w.apiKey, Query: q, MaxRes: num, Depth: "basic", IncludeA: true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("tavily %d: %s", resp.StatusCode, string(raw))
	}

	var out tavilyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}

	var b strings.Builder
	if out.Answer != "" {
		b.WriteString("Answer: ")
		b.WriteString(out.Answer)
		b.WriteString("\n\n")
	}
	for i, r := range out.Results {
		fmt.Fprintf(&b, "[%d] %s\n    %s\n    %s\n\n", i+1, r.Title, r.URL, truncate(r.Content, 320))
	}
	return b.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
