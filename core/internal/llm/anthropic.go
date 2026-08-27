package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/dopesoft/infinity/core/internal/httpx"
)

type Anthropic struct {
	client         anthropic.Client
	model          string
	thinkingBudget int64 // 0 = disabled. ≥1024 enables extended thinking with that token budget.
}

func NewAnthropic(apiKey, model string) *Anthropic {
	if model == "" {
		model = "claude-sonnet-4-5-20250929"
	}
	// The SDK's DefaultClientOptions eagerly builds defaultHTTPClient(), which
	// does http.DefaultTransport.(*http.Transport).Clone() and PANICS once
	// httpx.InstallDefault has wrapped the global transport (a *httpx.roundTripper
	// is not a *http.Transport — that exact panic crash-looped core on boot). Skip
	// it with WithoutEnvironmentDefaults and supply our own instrumented client,
	// so anthropic calls are still recorded into mem_http_failures.
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(httpx.InstrumentedClient("anthropic")),
	}
	// WithoutEnvironmentDefaults also drops the SDK's ANTHROPIC_BASE_URL handling;
	// re-add it so a configured override still applies.
	if base := os.Getenv("ANTHROPIC_BASE_URL"); base != "" {
		opts = append(opts, option.WithBaseURL(base))
	}
	c := anthropic.NewClient(opts...)
	return &Anthropic{client: c, model: model, thinkingBudget: thinkingBudgetFromEnv()}
}

// thinkingBudgetFromEnv reads ANTHROPIC_THINKING_BUDGET. Anthropic requires
// a budget ≥1024 tokens; values below that disable extended thinking entirely
// so the user doesn't accidentally pay for a feature that won't activate.
func thinkingBudgetFromEnv() int64 {
	raw := os.Getenv("ANTHROPIC_THINKING_BUDGET")
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1024 {
		return 0
	}
	return n
}

// effortToBudget maps a steal-C per-turn effort level to an Anthropic extended-
// thinking budget, treating the boss's ANTHROPIC_THINKING_BUDGET (envCeil) as a
// CEILING the ladder scales within - auto-routing never exceeds a budget the
// boss set. When no budget is configured (envCeil<=0) a default ceiling lets the
// feature still work; the floor (none) is always 0 = thinking disabled, so an
// un-escalated turn is unchanged. Respects Anthropic's >=1024 minimum.
func effortToBudget(level Effort, envCeil int64) int64 {
	var frac float64
	switch level {
	case EffortNone:
		return 0
	case EffortLow:
		frac = 0.25
	case EffortMedium:
		frac = 0.5
	case EffortHigh:
		frac = 0.75
	case EffortXHigh:
		frac = 1.0
	default:
		return envCeil // unknown level -> leave the env budget untouched
	}
	ceil := envCeil
	if ceil <= 0 {
		ceil = 32768
	}
	b := int64(float64(ceil) * frac)
	if b > 0 && b < 1024 { // Anthropic minimum
		b = 1024
	}
	if envCeil > 0 && b > envCeil { // never exceed the boss's configured ceiling
		b = envCeil
	}
	return b
}

func (a *Anthropic) Name() string  { return "anthropic" }
func (a *Anthropic) Model() string { return a.model }

// Draft is a one-shot, non-streaming completion used by helper subsystems
// (e.g. the Voyager skill extractor) that need a quick JSON-shaped reply
// without setting up the full streaming loop. Returns the concatenated text
// of the response.
func (a *Anthropic) Draft(ctx context.Context, model, system, userPrompt string, maxTokens int64) (string, error) {
	if model == "" {
		model = a.model
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)),
		},
	}
	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return "", err
	}
	var b []byte
	for _, c := range msg.Content {
		if t, ok := c.AsAny().(anthropic.TextBlock); ok {
			b = append(b, t.Text...)
		}
	}
	return string(b), nil
}

// Stream satisfies the base Provider interface. It is a thin shim over
// StreamCached treating the whole system string as the (cacheable) stable
// segment - correct for the one-shot helper callers that have no per-turn
// volatile content.
func (a *Anthropic) Stream(
	ctx context.Context,
	model string,
	system string,
	messages []Message,
	tools []ToolDef,
	out chan<- StreamEvent,
) (Response, error) {
	return a.StreamCached(ctx, model, SystemPrompt{Stable: system}, messages, tools, out)
}

// StreamCached is the caching-aware path. It places Anthropic cache_control
// breakpoints exactly the way Claude Code does: one on the stable system
// block, one on the last tool definition (so tools+system cache together),
// and a walking breakpoint on the last message so the growing transcript
// caches incrementally. Up to 4 breakpoints are allowed; we use 3.
func (a *Anthropic) StreamCached(
	ctx context.Context,
	model string,
	sys SystemPrompt,
	messages []Message,
	tools []ToolDef,
	out chan<- StreamEvent,
) (Response, error) {
	// Per-call model override (studio model chip). Falls back to the
	// boot-time default when unset. We don't validate the string here -
	// the Anthropic API surfaces a 404 / invalid-model error if the
	// client requests something unrecognized, which the WS error path
	// already plumbs back to the user.
	effectiveModel := a.model
	if model != "" {
		if normalized := normalizeAnthropicModel(model); normalized != "" {
			effectiveModel = normalized
		}
		// Unrecognized names (e.g. "gpt-5", "o3") silently fall back to
		// the boot-time default instead of erroring at the API. The agent
		// passing the wrong nickname should never tank the whole turn.
	}
	apiMessages := make([]anthropic.MessageParam, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleUser:
			apiMessages = append(apiMessages, anthropic.NewUserMessage(anthropicUserBlocks(m)...))
		case RoleAssistant:
			blocks := []anthropic.ContentBlockParamUnion{}
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				input, _ := json.Marshal(tc.Input)
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, json.RawMessage(input), tc.Name))
			}
			if len(blocks) > 0 {
				apiMessages = append(apiMessages, anthropic.NewAssistantMessage(blocks...))
			}
		case RoleTool:
			apiMessages = append(apiMessages, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false),
			))
		}
	}

	// Walking message breakpoint: cache_control on the last content block of
	// the last message caches the growing transcript incrementally, so each
	// turn only pays to write the newest turn's tokens and reads everything
	// prior at 0.1x. GetCacheControl returns a writable pointer into the
	// active union member; nil for block kinds that can't be cached.
	if n := len(apiMessages); n > 0 {
		blocks := apiMessages[n-1].Content
		if b := len(blocks); b > 0 {
			if cc := blocks[b-1].GetCacheControl(); cc != nil {
				*cc = anthropic.NewCacheControlEphemeralParam()
			}
		}
	}

	apiTools := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		raw, _ := json.Marshal(t.Schema)
		var schema anthropic.ToolInputSchemaParam
		_ = json.Unmarshal(raw, &schema)
		apiTools = append(apiTools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: schema,
			},
		})
	}
	// Cache breakpoint on the LAST tool def: the prefix hash is cumulative
	// tools -> system -> messages, so this caches the whole tool array (and,
	// with the system breakpoint below, tools+system as one stable prefix).
	if n := len(apiTools); n > 0 && apiTools[n-1].OfTool != nil {
		apiTools[n-1].OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}

	// steal C: a per-turn effort hint overrides the thinking budget for THIS call
	// only, scaled WITHIN the boss's configured ANTHROPIC_THINKING_BUDGET as a
	// ceiling (auto-routing never exceeds a budget the boss set). ctx unset ->
	// the env budget, exactly as before.
	budget := a.thinkingBudget
	if lvl := EffortFromContext(ctx); lvl != "" {
		budget = effortToBudget(lvl, a.thinkingBudget)
	}
	maxTokens := int64(4096)
	if budget > 0 && budget+1024 > maxTokens {
		maxTokens = budget + 1024
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(effectiveModel),
		MaxTokens: maxTokens,
		Messages:  apiMessages,
	}
	// Stable system block carries the cache breakpoint; the volatile block
	// (RRF retrieval, current_time, tool catalog, ...) follows AFTER it so it
	// never invalidates the cached prefix.
	if sys.Stable != "" {
		params.System = []anthropic.TextBlockParam{{
			Text:         sys.Stable,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}}
		if sys.Volatile != "" {
			params.System = append(params.System, anthropic.TextBlockParam{Text: sys.Volatile})
		}
	} else if sys.Volatile != "" {
		params.System = []anthropic.TextBlockParam{{Text: sys.Volatile}}
	}
	if len(apiTools) > 0 {
		params.Tools = apiTools
	}
	if budget > 0 {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
	}

	stream := a.client.Messages.NewStreaming(ctx, params)

	var msg anthropic.Message
	var resp Response

	// Correlate streamed input_json_delta chunks back to the tool block they
	// belong to. content_block_start announces a tool_use block (id + name) at
	// an index; subsequent input_json_delta events carry that index. We forward
	// each chunk as a StreamToolInputDelta so Studio can open the file in the
	// canvas and type it in live as the model writes the tool arguments.
	type toolBlockMeta struct{ id, name string }
	toolBlocks := map[int64]toolBlockMeta{}

	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
			return resp, err
		}

		switch ev := event.AsAny().(type) {
		case anthropic.ContentBlockStartEvent:
			if tub, ok := ev.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
				toolBlocks[ev.Index] = toolBlockMeta{id: tub.ID, name: tub.Name}
			}
		case anthropic.ContentBlockDeltaEvent:
			switch d := ev.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				if d.Text != "" {
					emit(out, StreamEvent{Kind: StreamText, TextDelta: d.Text})
				}
			case anthropic.ThinkingDelta:
				if d.Thinking != "" {
					emit(out, StreamEvent{Kind: StreamThinking, ThinkingDelta: d.Thinking})
				}
			case anthropic.InputJSONDelta:
				if d.PartialJSON != "" {
					meta := toolBlocks[ev.Index]
					emit(out, StreamEvent{
						Kind:       StreamToolInputDelta,
						ToolCallID: meta.id,
						ToolName:   meta.name,
						InputDelta: d.PartialJSON,
					})
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		if q := quotaFromAnthropicErr(a.Name(), err); q != nil {
			emit(out, StreamEvent{Kind: StreamError, Err: q.Error()})
			return resp, q
		}
		emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
		return resp, err
	}

	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			resp.Text += b.Text
		case anthropic.ToolUseBlock:
			var input map[string]any
			if len(b.Input) > 0 {
				if err := json.Unmarshal(b.Input, &input); err != nil {
					return resp, fmt.Errorf("decode tool input: %w", err)
				}
			}
			tc := ToolCall{ID: b.ID, Name: b.Name, Input: input}
			resp.ToolCalls = append(resp.ToolCalls, tc)
			emit(out, StreamEvent{Kind: StreamToolCall, ToolCall: &tc})
		}
	}

	// Anthropic reports input_tokens NET of cache (the uncached remainder),
	// with cache reads/writes broken out separately - so Input maps straight
	// across with no subtraction. The cost ledger applies the 0.1x/1.25x
	// multipliers; the context meter re-sums all three as window fill.
	resp.Usage = TokenUsage{
		Input:      int(msg.Usage.InputTokens),
		Output:     int(msg.Usage.OutputTokens),
		CacheRead:  int(msg.Usage.CacheReadInputTokens),
		CacheWrite: int(msg.Usage.CacheCreationInputTokens),
	}
	resp.StopReason = string(msg.StopReason)
	emit(out, StreamEvent{Kind: StreamComplete, StopReason: resp.StopReason, Usage: &resp.Usage})

	return resp, nil
}

// normalizeAnthropicModel maps known nicknames + full ids onto canonical
// Anthropic model strings. Returns "" if the input doesn't look like
// anything this provider can serve, so the caller can fall back to its
// own default. Keeps the delegate tool resilient to whatever shorthand
// the agent decides to pass on a given turn.
func normalizeAnthropicModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	// Pass-through for any full id already in the Claude namespace.
	if strings.HasPrefix(m, "claude-") {
		return model
	}
	// Tier nicknames map to the current generation. Update these in lock
	// step with the model knowledge cutoff section of CLAUDE.md.
	switch m {
	case "haiku":
		return "claude-haiku-4-5-20251001"
	case "sonnet":
		return "claude-sonnet-4-6"
	case "opus":
		return "claude-opus-4-7"
	}
	return ""
}

// quotaFromAnthropicErr recognises a spent Anthropic API account ("Your credit
// balance is too low", a 400 invalid_request_error) as plan exhaustion. A 429
// from Anthropic is a per-minute rate limit and stays transient.
func quotaFromAnthropicErr(provider string, err error) *QuotaError {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if apiErr.StatusCode == 400 && strings.Contains(msg, "credit balance") {
		return &QuotaError{Provider: provider, Detail: "the Anthropic API account has no credit left"}
	}
	return nil
}

func emit(ch chan<- StreamEvent, ev StreamEvent) {
	if ch == nil {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}

// anthropicUserBlocks renders a user message as content blocks. Attachments
// come FIRST (Anthropic's docs: documents and images before the question
// perform best), then the typed text. Images and PDFs ride natively when
// inside the documented limits (image ≤ 10 MB base64, jpeg/png/gif/webp;
// PDF ≤ 32 MB request, ≤ 100 pages); anything else, and every Note, is the
// labelled text rendering so nothing is ever silently dropped.
func anthropicUserBlocks(m Message) []anthropic.ContentBlockParamUnion {
	if len(m.Attachments) == 0 {
		return []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(m.Content)}
	}
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Attachments)*3+1)
	for _, a := range m.Attachments {
		switch {
		case a.InlineImageOK():
			blocks = append(blocks, anthropic.NewTextBlock(attachmentCaption(a)))
			blocks = append(blocks, anthropic.NewImageBlockBase64(strings.ToLower(a.MIME), base64.StdEncoding.EncodeToString(a.Data)))
		case a.InlinePDFOK():
			doc := anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{Data: base64.StdEncoding.EncodeToString(a.Data)})
			doc.OfDocument.Title = param.NewOpt(a.Name)
			blocks = append(blocks, anthropic.NewTextBlock(attachmentCaption(a)))
			blocks = append(blocks, doc)
		default:
			blocks = append(blocks, anthropic.NewTextBlock(a.TextBlock()))
			for i := range a.Pages {
				pm := a.PageMIME
				if pm == "" {
					pm = "image/jpeg"
				}
				blocks = append(blocks, anthropic.NewImageBlockBase64(pm, base64.StdEncoding.EncodeToString(a.Pages[i])))
			}
		}
	}
	if strings.TrimSpace(m.Content) != "" {
		blocks = append(blocks, anthropic.NewTextBlock(m.Content))
	}
	return blocks
}

// attachmentCaption is the one-line label that precedes a native image /
// document block: name, where it lives on the workspace, and any caveat.
func attachmentCaption(a Attachment) string {
	var b strings.Builder
	b.WriteString("Attachment: ")
	b.WriteString(a.Name)
	if a.PageCount > 0 {
		fmt.Fprintf(&b, " (%d pages)", a.PageCount)
	}
	if a.Path != "" {
		b.WriteString(" — on the workspace at ")
		b.WriteString(a.Path)
	}
	if a.Note != "" {
		b.WriteString(" [note: ")
		b.WriteString(strings.TrimSpace(a.Note))
		b.WriteString("]")
	}
	return b.String()
}
