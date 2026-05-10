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

// CodeExec posts code to a sandboxed Python sidecar. Core never executes user
// code itself — see docker/codeexec.Dockerfile for the sidecar image.
type CodeExec struct {
	sidecarURL string
	client     *http.Client
}

func NewCodeExecFromEnv() (*CodeExec, error) {
	url := strings.TrimSpace(os.Getenv("CODEEXEC_URL"))
	if url == "" {
		return nil, errors.New("CODEEXEC_URL not set")
	}
	return &CodeExec{
		sidecarURL: strings.TrimRight(url, "/"),
		client:     &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *CodeExec) Name() string        { return "code_exec" }
func (c *CodeExec) Description() string { return "Execute Python in a sandboxed sidecar. CPU-time and memory limits enforced. No network." }

func (c *CodeExec) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"language": map[string]any{"type": "string", "enum": []string{"python"}, "default": "python"},
			"code":     map[string]any{"type": "string", "description": "Source code to execute."},
		},
		"required": []string{"code"},
	}
}

type sidecarReq struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

type sidecarResp struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int    `json:"duration_ms"`
}

func (c *CodeExec) Execute(ctx context.Context, input map[string]any) (string, error) {
	code, _ := input["code"].(string)
	if code == "" {
		return "", errors.New("code is required")
	}
	lang, _ := input["language"].(string)
	if lang == "" {
		lang = "python"
	}

	body, _ := json.Marshal(sidecarReq{Language: lang, Code: code})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.sidecarURL+"/run", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("codeexec sidecar %d: %s", resp.StatusCode, string(raw))
	}

	var out sidecarResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "exit=%d duration=%dms\n", out.ExitCode, out.DurationMS)
	if out.Stdout != "" {
		b.WriteString("--- stdout ---\n")
		b.WriteString(out.Stdout)
		if !strings.HasSuffix(out.Stdout, "\n") {
			b.WriteString("\n")
		}
	}
	if out.Stderr != "" {
		b.WriteString("--- stderr ---\n")
		b.WriteString(out.Stderr)
	}
	return b.String(), nil
}
