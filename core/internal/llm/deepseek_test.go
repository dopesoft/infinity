package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
)

// A foreign model id must never reach DeepSeek. The whole point of the
// per-vendor normalizer is that the delegate tool and skills pass around tier
// nicknames and ids from whichever brain they learned on; shipping
// "gpt-5.6-sol" upstream would 400 the turn instead of falling back.
func TestNormalizeDeepSeekModel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"deepseek-v4-pro", "deepseek-v4-pro"},
		{"deepseek-v4-flash", "deepseek-v4-flash"},
		{"DeepSeek-V4-Pro", "DeepSeek-V4-Pro"}, // passthrough preserves case
		{"cheap", deepSeekFlashModel},
		{"haiku", deepSeekFlashModel},
		{"opus", DeepSeekDefaultModel},
		{"gpt-5.6-sol", ""}, // foreign id → caller falls back to the default
		{"claude-opus-5", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeDeepSeekModel(c.in); got != c.want {
			t.Errorf("normalizeDeepSeekModel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The provider must answer to its own registry id, not OpenAI's, or Settings
// would swap to "openai" the moment the boss picked DeepSeek.
func TestDeepSeekIdentity(t *testing.T) {
	p := NewDeepSeek("sk-test", "")
	if p.Name() != "deepseek" {
		t.Errorf("Name() = %q, want deepseek", p.Name())
	}
	if p.Model() != DeepSeekDefaultModel {
		t.Errorf("Model() = %q, want %q", p.Model(), DeepSeekDefaultModel)
	}
	// OpenAI-only request fields must stay off: DeepSeek has never seen
	// prompt_cache_key or reasoning.effort.
	if p.openaiExtras {
		t.Error("openaiExtras must be false for an OpenAI-compatible vendor")
	}
	if custom := NewDeepSeek("sk-test", "deepseek-v4-flash"); custom.Model() != "deepseek-v4-flash" {
		t.Errorf("explicit model not honoured: %q", custom.Model())
	}
}

func TestModelFamilyMatchesDeepSeek(t *testing.T) {
	if !ModelFamilyMatches("deepseek", "deepseek-v4-pro") {
		t.Error("deepseek-v4-pro should belong to the deepseek family")
	}
	if ModelFamilyMatches("deepseek", "gpt-5") {
		t.Error("gpt-5 must not be treated as a deepseek model")
	}
	if ModelFamilyMatches("openai", "deepseek-v4-pro") {
		t.Error("a deepseek id must not be handed to openai")
	}
}

// Vendor lookup is what keeps the HTTP layer free of per-vendor branches.
func TestFindKeyableVendor(t *testing.T) {
	v, ok := FindKeyableVendor("DeepSeek")
	if !ok || v.ID != "deepseek" || v.Env != "DEEPSEEK_API_KEY" {
		t.Fatalf("FindKeyableVendor(deepseek) = %+v, ok=%v", v, ok)
	}
	if _, ok := FindKeyableVendor("openai_oauth"); ok {
		t.Error("openai_oauth is a subscription, not a pasteable API key")
	}
	if _, ok := FindKeyableVendor("nope"); ok {
		t.Error("unknown vendor must not resolve")
	}
}

// Precedence is the contract: a key pasted in the UI is the boss's most
// recent explicit instruction and outranks the deploy-time env var. With a
// nil store (no DB) the env var still works, so the CLI paths keep running.
func TestResolveKeyFallsBackToEnv(t *testing.T) {
	v, _ := FindKeyableVendor("deepseek")
	t.Setenv(v.Env, "env-key")
	key, source, err := ResolveKey(context.Background(), nil, v)
	if err != nil {
		t.Fatalf("ResolveKey: %v", err)
	}
	if key != "env-key" || source != "env" {
		t.Errorf("got (%q, %q), want (env-key, env)", key, source)
	}
}

// A masked hint must identify a key without revealing it.
func TestMaskKey(t *testing.T) {
	if got := MaskKey("sk-abcdefghijklmnop"); got != "****mnop" {
		t.Errorf("MaskKey = %q, want ****mnop", got)
	}
	if got := MaskKey("short"); got != "****" {
		t.Errorf("short key leaked: %q", got)
	}
	if got := MaskKey(""); got != "" {
		t.Errorf("empty key = %q, want empty", got)
	}
}

// DeepSeek reports its cache split under prompt_cache_hit_tokens with no
// prompt_tokens_details at all. If that is not read, a cache hit that really
// happened reports as no cache: the meter's "served from cache" line vanishes
// and every token is priced at full rate. This decodes a real DeepSeek-shaped
// usage payload through the SDK to prove the extra field survives decoding.
func TestCompatCachedPromptTokensReadsDeepSeekShape(t *testing.T) {
	var u openai.CompletionUsage
	raw := `{"prompt_tokens":1200,"completion_tokens":300,"total_tokens":1500,` +
		`"prompt_cache_hit_tokens":960,"prompt_cache_miss_tokens":240}`
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if u.PromptTokensDetails.CachedTokens != 0 {
		t.Fatalf("precondition: DeepSeek sends no prompt_tokens_details, got %d",
			u.PromptTokensDetails.CachedTokens)
	}
	if got := compatCachedPromptTokens(u); got != 960 {
		t.Errorf("compatCachedPromptTokens = %d, want 960", got)
	}
}

// OpenAI's own shape must not be double-counted or disturbed: the typed field
// is populated there, so the compat path never runs.
func TestCompatCachedPromptTokensIgnoresOpenAIShape(t *testing.T) {
	var u openai.CompletionUsage
	raw := `{"prompt_tokens":1200,"completion_tokens":300,"total_tokens":1500,` +
		`"prompt_tokens_details":{"cached_tokens":800}}`
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if u.PromptTokensDetails.CachedTokens != 800 {
		t.Errorf("typed cached_tokens = %d, want 800", u.PromptTokensDetails.CachedTokens)
	}
	if got := compatCachedPromptTokens(u); got != 0 {
		t.Errorf("compat path fired on an OpenAI payload: %d", got)
	}
}

// A stub provider must never enter the registry. Everything downstream reads
// registry membership as "this brain can answer": the vendor picker enables
// the row, Settings allows the swap, failover may route a spent plan to it.
// Google is a stub whose every Stream call returns ErrNotImplemented, so
// registering it would make all three of those statements false at once.
func TestRegistryRefusesStubProvider(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewGoogle("key", ""))
	if _, ok := reg.Get("google"); ok {
		t.Error("a stub provider was registered and is now selectable")
	}
	reg.Register(NewDeepSeek("key", ""))
	if _, ok := reg.Get("deepseek"); !ok {
		t.Error("a working provider was refused")
	}
}

// Implemented must see through the wrappers every registered provider wears,
// or the sanitizer alone would hide a stub from the guard.
func TestImplementedUnwrapsDecorators(t *testing.T) {
	if Implemented(WrapNoDashes(NewGoogle("key", ""))) {
		t.Error("a wrapped stub reported itself as implemented")
	}
	if !Implemented(WrapNoDashes(NewDeepSeek("key", ""))) {
		t.Error("a wrapped working provider reported itself as a stub")
	}
}

// FirstHealthy is what keeps housekeeping (session titles) alive when one
// plan is spent. Session naming went dark on 2026-08-30 because it was pinned
// to a single provider whose plan ran out; the preference list must skip a
// spent brain rather than fail on it.
func TestFirstHealthySkipsSpentProvider(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewDeepSeek("k", ""))
	reg.Register(NewOpenAI("k", ""))
	t.Cleanup(func() {
		ClearExhausted("deepseek")
		ClearExhausted("openai")
	})

	p, ok := reg.FirstHealthy("deepseek", "openai")
	if !ok || p.Name() != "deepseek" {
		t.Fatalf("preferred brain not chosen: %v %v", p, ok)
	}

	MarkExhausted("deepseek", time.Now().Add(time.Hour), "spent")
	p, ok = reg.FirstHealthy("deepseek", "openai")
	if !ok || p.Name() != "openai" {
		t.Fatalf("a spent brain was handed back instead of the next one: %v %v", p, ok)
	}

	// Everything spent: report honestly rather than returning a brain that
	// will fail on the next call.
	MarkExhausted("openai", time.Now().Add(time.Hour), "spent")
	if p, ok := reg.FirstHealthy("deepseek", "openai"); ok || p != nil {
		t.Errorf("expected no healthy brain, got %v", p)
	}
}

// A PDF attached to DeepSeek must arrive as extracted TEXT, never as an
// OpenAI-native file part.
//
// The regression this pins: production turn 63524f02-e2f7-438a-8ae0-3bfd331a9bc7
// died with HTTP 400 `.messages[1]: file must have a file_id or file_data`
// after a 3-page PDF was attached. Every OpenAI-compatible vendor shared one
// renderer, so DeepSeek was handed the SDK's nested {"file":{...}} content
// part, which its schema does not accept (it wants file_id/file_data on the
// part itself), and deepseek-v4-pro is not the vision model anyway. The boss's
// file never reached the brain and the turn failed outright.
func TestDeepSeekSendsAttachmentsAsTextNotFileParts(t *testing.T) {
	m := Message{
		Role:    RoleUser,
		Content: "summarize this resume",
		Attachments: []Attachment{{
			Name:      "resume.pdf",
			MIME:      "application/pdf",
			Kind:      AttachmentDocument,
			PageCount: 3,
			Data:      []byte("%PDF-1.7 binary"),
			Text:      "Kai Malabie - Founder, DopeSoft",
		}},
	}

	msg := NewDeepSeek("k", "").userMessage(m)

	// (1) The content the boss actually attached, plus what he typed, must
	// both be in the request. A vendor-safe shape that drops the file would
	// be the older, quieter version of the same bug.
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal deepseek user message: %v", err)
	}
	body := string(raw)
	for _, want := range []string{"Kai Malabie - Founder, DopeSoft", "summarize this resume", `name=\"resume.pdf\"`, `pages=\"3\"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("deepseek request is missing %q:\n%s", want, body)
		}
	}

	// (2) Nothing in the marshaled message may carry the shape that produced
	// the 400: a file part, an image part, or the raw PDF bytes.
	for _, banned := range []string{`"type":"file"`, `"file":{`, `"type":"image_url"`, `"image_url"`, "base64,"} {
		if strings.Contains(body, banned) {
			t.Fatalf("deepseek request contains %q, which is what 400s:\n%s", banned, body)
		}
	}
	if msg.OfUser == nil {
		t.Fatal("expected a user message")
	}
	if parts := msg.OfUser.Content.OfArrayOfContentParts; len(parts) > 0 {
		t.Fatalf("deepseek must send a plain string, got %d content parts", len(parts))
	}
	if !msg.OfUser.Content.OfString.Valid() {
		t.Fatal("deepseek user content must be a plain string")
	}
}

// The fix is scoped to the compatible vendors: OpenAI itself still gets the
// native image / file parts, because those are the shape its own PDF and
// vision guides document.
func TestOpenAIStillSendsNativeAttachmentParts(t *testing.T) {
	m := Message{
		Role:    RoleUser,
		Content: "summarize this resume",
		Attachments: []Attachment{
			{Name: "resume.pdf", MIME: "application/pdf", Kind: AttachmentDocument, PageCount: 3, Data: []byte("%PDF"), Text: "Kai Malabie"},
			{Name: "photo.png", MIME: "image/png", Kind: AttachmentImage, Data: []byte{1, 2, 3}},
		},
	}

	msg := NewOpenAI("k", "").userMessage(m)
	if msg.OfUser == nil {
		t.Fatal("expected a user message")
	}
	parts := msg.OfUser.Content.OfArrayOfContentParts
	// caption + file, caption + image, typed text
	if len(parts) != 5 {
		t.Fatalf("openai parts = %d, want caption+file+caption+image+text", len(parts))
	}
	if parts[1].OfFile == nil {
		t.Fatal("openai must keep the native PDF file part")
	}
	if parts[3].OfImageURL == nil {
		t.Fatal("openai must keep the native image part")
	}
	if parts[4].OfText == nil || parts[4].OfText.Text != "summarize this resume" {
		t.Fatalf("typed text must come last, got %+v", parts[4])
	}
}

// A message with no attachments is untouched on either vendor: this fix must
// not change the shape of an ordinary turn.
func TestUserMessageWithoutAttachmentsIsPlainStringEverywhere(t *testing.T) {
	m := Message{Role: RoleUser, Content: "hello"}
	for _, p := range []*OpenAI{NewDeepSeek("k", ""), NewOpenAI("k", "")} {
		msg := p.userMessage(m)
		if msg.OfUser == nil || msg.OfUser.Content.OfString.Or("") != "hello" {
			t.Fatalf("%s: plain turn must stay a plain string, got %+v", p.Name(), msg.OfUser)
		}
	}
}
