package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"
)

// OpenAI is the Chat Completions provider. It also serves every
// OpenAI-COMPATIBLE vendor (DeepSeek today), because the wire format is
// identical and the differences are data, not code: an id, a base URL, a
// model-normalizer, and a flag for the request fields only OpenAI accepts.
// That is the whole reason a second vendor costs a constructor here instead
// of a second 300-line streaming implementation to keep in sync forever.
type OpenAI struct {
	client openai.Client
	model  string
	// name is the registry id this instance answers to ("openai",
	// "deepseek"). Never hardcoded at the Name() call site.
	name string
	// normalize maps a requested model id or tier nickname onto something
	// THIS vendor can serve, returning "" so the caller falls back to the
	// configured default rather than shipping a foreign id upstream.
	normalize func(string) string
	// openaiExtras gates request fields only OpenAI itself accepts
	// (prompt_cache_key). A compatible vendor that has never seen them gets
	// a clean request instead of an unexplained 400.
	openaiExtras bool
	// effortLevel maps the effort router's level (none|low|medium|high|xhigh)
	// onto what THIS vendor's reasoning_effort accepts, or "" to omit the
	// field. nil means the vendor has no such field. OpenAI takes the levels
	// as they are; DeepSeek takes low|high|max.
	effortLevel func(string) string
	// replayReasoning: this vendor wants the model's own reasoning text sent
	// back on assistant messages in a tool-calling round (DeepSeek's
	// documented contract for thinking mode). Read off Message.Meta, where
	// the loop keeps what the stream produced.
	replayReasoning bool
	// nativeAttachments gates the OpenAI-native image_url / file content
	// parts. Only OpenAI itself is known to accept them: DeepSeek's Chat
	// Completions schema wants file_id/file_data directly on the content
	// part, so the SDK's nested {"file":{...}} object comes back as
	// `.messages[1]: file must have a file_id or file_data` (HTTP 400) and
	// the turn dies with the file never reaching the brain. Off by default,
	// so a newly wired compatible vendor degrades to the labelled text
	// rendering (which every Chat Completions vendor accepts) instead of
	// inheriting a shape only OpenAI serves.
	nativeAttachments bool
	// baseURL is empty for OpenAI (SDK default) and set for a compatible
	// vendor. Verify() uses it to probe the credential.
	baseURL string
	// apiKey is retained solely so Verify() can probe the credential; the
	// SDK client does not expose it back. Never logged, never serialized.
	apiKey string
}

func NewOpenAI(apiKey, model string) *OpenAI {
	if model == "" {
		model = "gpt-5"
	}
	c := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAI{
		client:            c,
		model:             model,
		name:              "openai",
		normalize:         normalizeOpenAIModel,
		openaiExtras:      true,
		effortLevel:       func(l string) string { return l },
		nativeAttachments: true,
		apiKey:            apiKey,
	}
}

// reasoningDelta reads the vendor-extension reasoning text off a streamed
// chunk. DeepSeek streams its chain of thought as `delta.reasoning_content`
// (api-docs.deepseek.com, thinking mode); other compatibles use `reasoning`.
// The SDK has no typed slot for either, so they land in JSON.ExtraFields.
//
// This was simply never read (2026-09-04): a DeepSeek turn that reasoned for
// two and a half minutes produced NO frame for the whole of it, the browser's
// watchdog declared the agent dead, and the boss was shown an error over a
// turn that was fine. Now the reasoning streams into the Thinking row the way
// Anthropic's does.
func reasoningDelta(d openai.ChatCompletionChunkChoiceDelta) string {
	for _, key := range []string{"reasoning_content", "reasoning"} {
		f, ok := d.JSON.ExtraFields[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal([]byte(f.Raw()), &s); err == nil && s != "" {
			return s
		}
	}
	return ""
}

func (o *OpenAI) Name() string  { return o.name }
func (o *OpenAI) Model() string { return o.model }

// normalizeModel resolves a per-call model override through this vendor's
// normalizer, nil-safe so a zero-value struct can't panic a turn.
func (o *OpenAI) normalizeModel(model string) string {
	if o.normalize == nil {
		return normalizeOpenAIModel(model)
	}
	return o.normalize(model)
}

func (o *OpenAI) Stream(
	ctx context.Context,
	model string,
	system string,
	messages []Message,
	tools []ToolDef,
	out chan<- StreamEvent,
) (Response, error) {
	return o.StreamCached(ctx, model, SystemPrompt{Stable: system}, messages, tools, out)
}

// StreamCached renders the system stable-first so OpenAI's automatic prompt
// caching (exact-prefix match on the leading tokens) hits across turns, and
// pins routing with prompt_cache_key. OpenAI has no explicit cache_control;
// the win is entirely from keeping the stable bytes contiguous at the front.
func (o *OpenAI) StreamCached(
	ctx context.Context,
	model string,
	sys SystemPrompt,
	messages []Message,
	tools []ToolDef,
	out chan<- StreamEvent,
) (Response, error) {
	system := sys.Render()
	effectiveModel := o.model
	if model != "" {
		if normalized := o.normalizeModel(model); normalized != "" {
			effectiveModel = normalized
		}
		// Unknown nickname (e.g. "haiku"/"sonnet") silently falls back to
		// the configured default so an upstream bad guess can't break the
		// turn.
	}
	apiMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
	if system != "" {
		apiMessages = append(apiMessages, openai.SystemMessage(system))
	}
	for _, m := range messages {
		switch m.Role {
		case RoleUser:
			apiMessages = append(apiMessages, o.userMessage(m))
		case RoleAssistant:
			am := openai.ChatCompletionAssistantMessageParam{}
			if m.Content != "" {
				am.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(m.Content),
				}
			}
			for _, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Input)
				am.ToolCalls = append(am.ToolCalls, openai.ChatCompletionMessageToolCallParam{
					ID:   tc.ID,
					Type: "function",
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      tc.Name,
						Arguments: string(args),
					},
				})
			}
			if o.replayReasoning && len(tools) > 0 {
				// DeepSeek's thinking-mode contract: in a tool-calling round
				// the reasoning that led to each call goes back with it.
				if rc, _ := m.Meta[MetaReasoningContent].(string); rc != "" {
					am.SetExtraFields(map[string]any{"reasoning_content": rc})
				}
			}
			apiMessages = append(apiMessages, openai.ChatCompletionMessageParamUnion{OfAssistant: &am})
		case RoleTool:
			apiMessages = append(apiMessages, openai.ToolMessage(m.Content, m.ToolCallID))
		}
	}

	apiTools := make([]openai.ChatCompletionToolParam, 0, len(tools))
	for _, t := range tools {
		apiTools = append(apiTools, openai.ChatCompletionToolParam{
			Type: "function",
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  toFunctionParameters(t.Schema),
			},
		})
	}

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(effectiveModel),
		Messages: apiMessages,
	}
	if len(apiTools) > 0 {
		params.Tools = apiTools
	}
	// Ask for token usage on the stream. Without this OpenAI sends NO usage
	// object on a streamed response at all, so resp.Usage comes back as
	// zeros - which is not "this turn was free", it is "I never looked".
	// Those zeros flow straight into Session.RecordUsage, so the context
	// meter reads 0% while the window is genuinely filling, and the cost
	// ledger books nothing. An empty reading that is indistinguishable from
	// a real one is the exact failure the never-hide-errors rule exists to
	// prevent. DeepSeek returns usage on the final chunk either way and
	// accepts this flag, so one line covers both vendors.
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
		IncludeUsage: openai.Bool(true),
	}
	// Pin the cache shard to the session so all of a session's turns share a
	// route, improving the auto-cache hit rate on the stable prefix. OpenAI
	// only: a compatible vendor caches on its own prefix rules and has no
	// such field, so sending it risks a 400 for zero benefit.
	if o.openaiExtras {
		if key := CacheKeyFromContext(ctx); key != "" {
			params.PromptCacheKey = openai.String(key)
		}
	}
	// steal C: per-turn reasoning effort (ctx hint > env fallback > omit). Only
	// on reasoning-capable models; "" omits the field (model default), so an
	// un-escalated turn is unchanged.
	if o.effortLevel != nil && modelSupportsReasoning(effectiveModel) {
		lvl := string(EffortFromContext(ctx))
		if lvl == "" {
			lvl = strings.TrimSpace(os.Getenv("INFINITY_OPENAI_REASONING_EFFORT"))
		}
		if lvl = o.effortLevel(lvl); lvl != "" {
			params.ReasoningEffort = shared.ReasoningEffort(lvl)
		}
	}

	stream := o.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var (
		acc       openai.ChatCompletionAccumulator
		resp      Response
		streamErr error
	)

	// Correlate streamed function-argument chunks back to a tool call. The
	// first chunk for a given index carries id + name; later chunks carry only
	// the argument delta. We forward each as a StreamToolInputDelta so Studio
	// can open the file in the canvas and type it in live.
	type toolDeltaMeta struct{ id, name string }
	toolDeltas := map[int64]toolDeltaMeta{}
	var reasoning strings.Builder

	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)
		for _, choice := range chunk.Choices {
			if r := reasoningDelta(choice.Delta); r != "" {
				reasoning.WriteString(r)
				emit(out, StreamEvent{Kind: StreamThinking, ThinkingDelta: r})
			}
			if choice.Delta.Content != "" {
				emit(out, StreamEvent{Kind: StreamText, TextDelta: choice.Delta.Content})
			}
			for _, tc := range choice.Delta.ToolCalls {
				meta := toolDeltas[tc.Index]
				if tc.ID != "" {
					meta.id = tc.ID
				}
				if tc.Function.Name != "" {
					meta.name = tc.Function.Name
				}
				toolDeltas[tc.Index] = meta
				if tc.Function.Arguments != "" {
					emit(out, StreamEvent{
						Kind:       StreamToolInputDelta,
						ToolCallID: meta.id,
						ToolName:   meta.name,
						InputDelta: tc.Function.Arguments,
					})
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		streamErr = err
		emit(out, StreamEvent{Kind: StreamError, Err: err.Error()})
	}

	if len(acc.Choices) > 0 {
		msg := acc.Choices[0].Message
		resp.Text = msg.Content
		for _, tc := range msg.ToolCalls {
			var input map[string]any
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
					return resp, fmt.Errorf("decode openai tool arguments: %w", err)
				}
			}
			call := ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: input}
			resp.ToolCalls = append(resp.ToolCalls, call)
			emit(out, StreamEvent{Kind: StreamToolCall, ToolCall: &call})
		}
		// OpenAI's prompt_tokens INCLUDES cached tokens, so subtract them to
		// get the full-priced uncached input the ledger needs; CacheRead
		// carries the discounted portion. (No separate cache-write charge.)
		cached := int(acc.Usage.PromptTokensDetails.CachedTokens)
		if cached == 0 {
			cached = compatCachedPromptTokens(acc.Usage)
		}
		uncached := int(acc.Usage.PromptTokens) - cached
		if uncached < 0 {
			uncached = 0
		}
		resp.Usage = TokenUsage{Input: uncached, Output: int(acc.Usage.CompletionTokens), CacheRead: cached}
		resp.StopReason = string(acc.Choices[0].FinishReason)
	}
	if reasoning.Len() > 0 {
		// Kept on the response so the loop can hand it back on the next
		// request of a tool-calling round (replayReasoning).
		resp.Meta = map[string]any{MetaReasoningContent: reasoning.String()}
	}

	emit(out, StreamEvent{Kind: StreamComplete, StopReason: resp.StopReason, Usage: &resp.Usage})
	return resp, streamErr
}

func toFunctionParameters(schema map[string]any) shared.FunctionParameters {
	if schema == nil {
		return shared.FunctionParameters{"type": "object"}
	}
	if !strings.Contains(fmt.Sprint(schema), "type") {
		schema["type"] = "object"
	}
	out := shared.FunctionParameters{}
	for k, v := range schema {
		out[k] = v
	}
	return out
}

// normalizeOpenAIModel maps full ids + nicknames onto canonical OpenAI
// model strings. Returns "" if the input doesn't look like something
// this provider can serve, so the caller can fall back to its own
// default. Mirrors normalizeAnthropicModel on the other side so the
// delegate tool can pass either tier shorthand or a full id without
// caring which provider is wired up.
func normalizeOpenAIModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	// Pass-through for any full id in the OpenAI namespace.
	if strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") ||
		strings.HasPrefix(m, "chatgpt-") {
		return model
	}
	// Map Anthropic tier nicknames onto the closest OpenAI tier so an
	// agent that learned "haiku for cheap" doesn't tank when the loop is
	// on OpenAI. Adjust these in lock step with the OpenAI lineup.
	switch m {
	case "haiku", "cheap", "small", "mini":
		return "gpt-5-mini"
	case "sonnet", "default", "medium":
		return "gpt-5"
	case "opus", "premium", "large":
		return "gpt-5"
	}
	return ""
}

var _ ssestream.Stream[openai.ChatCompletionChunk] // keep import for clarity

// userMessage renders one user turn for THIS vendor. It is the single place
// the native-vs-text attachment decision is made, so a test can prove what
// the loop actually sends instead of asserting against a second copy of the
// rule that could drift from it.
func (o *OpenAI) userMessage(m Message) openai.ChatCompletionMessageParamUnion {
	switch {
	case len(m.Attachments) == 0:
		return openai.UserMessage(m.Content)
	case o.nativeAttachments:
		return openai.UserMessage(openaiUserParts(m))
	default:
		return openai.UserMessage(openaiAttachmentText(m))
	}
}

// openaiUserParts renders a user message with attachments as Chat Completions
// content parts: images as image_url data URLs, PDFs as `file` parts
// (file_data + filename, per the OpenAI PDF guide), everything else as the
// labelled text rendering. Attachments precede the typed text.
func openaiUserParts(m Message) []openai.ChatCompletionContentPartUnionParam {
	parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(m.Attachments)*3+1)
	for _, a := range m.Attachments {
		switch {
		case a.InlineImageOK():
			parts = append(parts, openai.TextContentPart(attachmentCaption(a)))
			parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: a.DataURL()}))
		case a.InlinePDFOK():
			parts = append(parts, openai.TextContentPart(attachmentCaption(a)))
			parts = append(parts, openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
				FileData: param.NewOpt(a.DataURL()),
				Filename: param.NewOpt(a.Name),
			}))
		default:
			parts = append(parts, openai.TextContentPart(a.TextBlock()))
			for i := range a.Pages {
				parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: a.PageDataURL(i)}))
			}
		}
	}
	if strings.TrimSpace(m.Content) != "" || len(parts) == 0 {
		parts = append(parts, openai.TextContentPart(m.Content))
	}
	return parts
}

// openaiAttachmentText renders a user message with attachments as ONE plain
// string: each attachment's labelled TextBlock (extracted text, page-count,
// path and any Note) followed by the typed text. This is the rendering for a
// vendor that speaks Chat Completions but does not accept OpenAI's native
// image_url / file parts.
//
// It is deliberately a string rather than a text content-part array: the
// failure being fixed was a content part the vendor rejected, so the safest
// shape is the one with no content parts at all. The file's CONTENT still
// reaches the brain, which is the invariant that matters - the attachment
// path's original bug was metadata-only prompt stuffing, and this is not a
// return to it.
func openaiAttachmentText(m Message) string {
	var b strings.Builder
	for _, a := range m.Attachments {
		// An IMAGE on a vendor that takes no image parts is the one case where
		// this rendering carries no content at all - TextBlock can only say
		// "[image]". Left there, the model reads a filename and answers as if
		// it had looked, which is the confident-answer-about-nothing failure
		// this codebase refuses everywhere else. So it is told plainly, and
		// told to say so.
		if a.Kind == AttachmentImage && len(a.Pages) == 0 {
			a.Note = joinNote(a.Note, "this brain cannot be shown pictures, so you have NOT seen this image — tell the boss that and ask him to switch brains or describe it, never guess at what it shows")
		}
		b.WriteString(a.TextBlock())
		b.WriteString("\n\n")
	}
	b.WriteString(m.Content)
	return strings.TrimRight(b.String(), "\n")
}
