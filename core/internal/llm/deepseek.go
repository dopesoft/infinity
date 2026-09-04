package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// DeepSeek ships an OpenAI-compatible Chat Completions API, so it reuses the
// OpenAI provider wholesale with a different base URL, id and normalizer.
// https://api-docs.deepseek.com - "The DeepSeek API uses an API format
// compatible with OpenAI. By modifying the configuration, you can use the
// OpenAI SDK." The /v1 suffix is the documented compatibility path and has
// nothing to do with the model version.
const (
	DeepSeekBaseURL = "https://api.deepseek.com/v1/"
	// DeepSeekDefaultModel is the flagship. Flash is the cheap tier; both
	// carry a 1M context window.
	DeepSeekDefaultModel = "deepseek-v4-pro"
	deepSeekFlashModel   = "deepseek-v4-flash"
)

// NewDeepSeek builds the DeepSeek brain. Model may be empty (falls back to
// the flagship). Registered under id "deepseek" so Settings, the composer
// chip and the cost ledger address it like any other vendor.
func NewDeepSeek(apiKey, model string) *OpenAI {
	if strings.TrimSpace(model) == "" {
		model = DeepSeekDefaultModel
	}
	c := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(DeepSeekBaseURL),
	)
	return &OpenAI{
		client:    c,
		model:     model,
		name:      "deepseek",
		normalize: normalizeDeepSeekModel,
		// DeepSeek accepts the standard Chat Completions body. It has no
		// prompt_cache_key (its caching is automatic and server-side), so
		// that stays off.
		openaiExtras: false,
		// Thinking mode (api-docs.deepseek.com/guides/thinking_mode): the
		// reasoning streams as delta.reasoning_content, `reasoning_effort`
		// takes low | high | max, and in a tool-calling round the reasoning
		// goes back with the assistant message that made the call.
		effortLevel:     deepSeekEffort,
		replayReasoning: true,
		baseURL:         DeepSeekBaseURL,
		apiKey:          apiKey,
	}
}

// deepSeekEffort maps the effort router's five levels onto DeepSeek's three.
// "" (auto) omits the field so the model's default applies.
func deepSeekEffort(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "none", "low":
		return "low"
	case "medium", "high":
		return "high"
	case "xhigh", "max":
		return "max"
	}
	return ""
}

// normalizeDeepSeekModel passes through anything in the DeepSeek namespace
// and maps the tier nicknames the delegate tool and skills use, so an agent
// that learned "haiku for cheap" still lands on the cheap tier here instead
// of shipping a foreign id upstream. Returns "" for an unknown id so the
// caller falls back to the configured default.
func normalizeDeepSeekModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	if strings.HasPrefix(m, "deepseek") {
		return model
	}
	switch m {
	case "haiku", "cheap", "small", "mini", "fast":
		return deepSeekFlashModel
	case "sonnet", "default", "medium", "opus", "premium", "large":
		return DeepSeekDefaultModel
	}
	return ""
}

// Verify proves a pasted credential actually works, by listing models on the
// vendor's own endpoint. This is what turns "saved" into "saved and it
// answers": without it a bad paste looks identical to a good one until the
// next turn dies with a 401 that reads like the brain is broken.
//
// Only implemented for the OpenAI-compatible path, where /models is a cheap,
// side-effect-free GET. Callers treat a missing implementation as
// "unverifiable", never as "failed" (never-hide-errors cuts both ways: an
// absent probe must not read as a proven-bad key).
func (o *OpenAI) Verify(ctx context.Context) error {
	base := o.baseURL
	if base == "" {
		base = "https://api.openai.com/v1/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", o.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
	snippet := strings.TrimSpace(string(body))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: %s turned it down (%d)", ErrKeyRejected, o.name, resp.StatusCode)
	}
	return fmt.Errorf("%s returned %d: %s", o.name, resp.StatusCode, snippet)
}

// Verifier is implemented by providers that can cheaply prove a credential
// before the boss depends on it. The provider-keys API probes through this
// interface, so a new vendor gets verification by implementing one method -
// no per-vendor branch in the HTTP layer.
type Verifier interface {
	Verify(ctx context.Context) error
}

// ErrKeyRejected marks the one verification outcome that is definitely the
// credential's fault: the vendor looked at it and said no. Anything else
// (timeout, 500, DNS) is the network's fault and must NOT be reported as a
// bad key - that distinction is the difference between "fix your paste" and
// "try again in a minute".
var ErrKeyRejected = errors.New("key rejected by the vendor")

// compatCachedPromptTokens reads the prompt-cache hit count from a vendor
// that reports it under its own field name instead of OpenAI's
// prompt_tokens_details.cached_tokens.
//
// DeepSeek returns prompt_cache_hit_tokens / prompt_cache_miss_tokens and no
// prompt_tokens_details at all (vendor API reference, checked 2026-08-30), so
// the SDK's typed field is always zero there. Left unread, a cache hit that
// really happened would report as no cache at all: the meter's "served from
// cache" line disappears and the ledger prices every token at full rate. The
// saving is real either way; this is what makes it visible.
//
// Reads through the SDK's ExtraFields bag, so it costs nothing on OpenAI
// (where the typed field is populated and this never runs) and needs no
// per-vendor branch at the call site.
func compatCachedPromptTokens(u openai.CompletionUsage) int {
	// Note: Valid() is deliberately NOT consulted. The SDK reports every
	// field it has no typed slot for as invalid, which is precisely what an
	// extra field is - gating on it discards the value we came for. Verified
	// against the decoder in deepseek_test.go; a parse failure (absent, null,
	// non-numeric) falls through to zero on its own.
	for _, name := range []string{"prompt_cache_hit_tokens", "cached_tokens"} {
		f, ok := u.JSON.ExtraFields[name]
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(f.Raw()))
		if err == nil && n > 0 {
			return n
		}
	}
	return 0
}
