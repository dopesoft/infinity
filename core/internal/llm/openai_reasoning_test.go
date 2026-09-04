package llm

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-go"
)

// DeepSeek streams its chain of thought as delta.reasoning_content, which the
// SDK has no typed slot for. It was never read, so a reasoner turn produced no
// frame for minutes and the browser called the agent dead (2026-09-04).
func TestReasoningDelta_ReadsTheVendorExtensionOffTheChunk(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want string
	}{
		"deepseek reasoning_content": {`{"content":"","reasoning_content":"let me think"}`, "let me think"},
		"generic reasoning":          {`{"content":null,"reasoning":"hmm"}`, "hmm"},
		"absent":                     {`{"content":"hi"}`, ""},
		"null":                       {`{"reasoning_content":null}`, ""},
		"empty string":               {`{"reasoning_content":""}`, ""},
		"not a string":               {`{"reasoning_content":{"x":1}}`, ""},
	}
	for name, c := range cases {
		var d openai.ChatCompletionChunkChoiceDelta
		if err := json.Unmarshal([]byte(c.raw), &d); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		if got := reasoningDelta(d); got != c.want {
			t.Fatalf("%s: reasoningDelta = %q, want %q", name, got, c.want)
		}
	}
}

func TestDeepSeekEffort_MapsFiveLevelsOntoThree(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "auto": "", "none": "low", "low": "low", "medium": "high",
		"high": "high", "xhigh": "max", "max": "max",
	} {
		if got := deepSeekEffort(in); got != want {
			t.Fatalf("deepSeekEffort(%q) = %q, want %q", in, got, want)
		}
	}
}
