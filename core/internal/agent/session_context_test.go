package agent

import (
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
)

func TestPinVolatileRidesNewestUserMessageAndClears(t *testing.T) {
	s := &Session{}
	s.Append(llm.Message{Role: llm.RoleUser, Content: "old"})
	s.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	s.Append(llm.Message{Role: llm.RoleUser, Content: "new"})
	s.pinVolatile("ctx")
	if s.Messages[2].Volatile != "ctx" || s.Messages[0].Volatile != "" {
		t.Fatalf("pin landed wrong: %+v", s.Messages)
	}
	s.Append(llm.Message{Role: llm.RoleTool, Content: "r", ToolCallID: "c"})
	s.clearVolatile()
	for i, m := range s.Messages {
		if m.Volatile != "" {
			t.Fatalf("message %d still pinned", i)
		}
	}
}

func TestInsertWorldStateGoesAheadOfTheRequest(t *testing.T) {
	s := &Session{}
	s.Append(llm.Message{Role: llm.RoleUser, Content: "do it"})
	s.pinVolatile("ctx")
	s.insertWorldState("<world_state>x</world_state>")
	if len(s.Messages) != 2 || s.Messages[0].Meta[MetaWorldState] != true || s.Messages[1].Content != "do it" {
		t.Fatalf("world state must precede the request: %+v", s.Messages)
	}
	if s.Messages[1].Volatile != "ctx" {
		t.Fatal("pin must stay on the request")
	}
}

func TestAppendWindDownRidesTheNewestToolResultOnce(t *testing.T) {
	s := &Session{}
	s.Append(llm.Message{Role: llm.RoleUser, Content: "q"})
	if s.appendWindDown() {
		t.Fatal("no tool result to ride: must report false")
	}
	s.Append(llm.Message{Role: llm.RoleTool, Content: "out", ToolCallID: "c"})
	if !s.appendWindDown() || !s.appendWindDown() {
		t.Fatal("should attach")
	}
	if strings.Count(s.Messages[1].Content, "<budget_notice>") != 1 {
		t.Fatalf("attached more than once:\n%s", s.Messages[1].Content)
	}
}

func TestClearOldToolResultsKeepsNewestAndHonoursMinimum(t *testing.T) {
	s := &Session{}
	s.Append(llm.Message{Role: llm.RoleUser, Content: "q"})
	big := strings.Repeat("x", 30_000)
	for i := 0; i < 5; i++ {
		s.Append(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c", Name: "fs_read"}}})
		s.Append(llm.Message{Role: llm.RoleTool, Content: big, ToolCallID: "c", ToolName: "fs_read"})
	}
	// Two clearable results (5 - 3 kept) = 60K chars; a 100K minimum refuses.
	if freed := s.clearOldToolResults(3, nil, 100_000); freed != 0 {
		t.Fatalf("under the minimum must be a no-op, freed %d", freed)
	}
	freed := s.clearOldToolResults(3, nil, 40_000)
	if freed != 60_000 {
		t.Fatalf("freed %d, want 60000", freed)
	}
	cleared := 0
	for _, m := range s.Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "result cleared to save context") {
			cleared++
		}
	}
	if cleared != 2 {
		t.Fatalf("cleared %d, want 2", cleared)
	}
	// Newest three untouched.
	if len(s.Messages[len(s.Messages)-1].Content) != 30_000 {
		t.Fatal("newest result must stay whole")
	}
	// Idempotent: nothing more to free.
	if freed := s.clearOldToolResults(3, nil, 1); freed != 0 {
		t.Fatalf("second pass freed %d", freed)
	}
}

func TestClearOldToolResultsSkipsExcludedTools(t *testing.T) {
	s := &Session{}
	s.Append(llm.Message{Role: llm.RoleUser, Content: "q"})
	s.Append(llm.Message{Role: llm.RoleTool, Content: strings.Repeat("p", 50_000), ToolCallID: "a", ToolName: "plan_get"})
	s.Append(llm.Message{Role: llm.RoleTool, Content: strings.Repeat("w", 50_000), ToolCallID: "b", ToolName: "web_search"})
	freed := s.clearOldToolResults(0, map[string]bool{"plan_get": true}, 1)
	if freed != 50_000 || !strings.HasPrefix(s.Messages[1].Content, "ppp") {
		t.Fatalf("excluded tool must survive; freed=%d", freed)
	}
}

func TestDegradeOldAttachmentsDropsBytesExceptNewest(t *testing.T) {
	s := &Session{}
	s.Append(llm.Message{Role: llm.RoleUser, Content: "look", Attachments: []llm.Attachment{{Kind: llm.AttachmentImage, Name: "a.png", MIME: "image/png", Data: []byte{1, 2, 3}}}})
	s.Append(llm.Message{Role: llm.RoleAssistant, Content: "seen"})
	s.Append(llm.Message{Role: llm.RoleUser, Content: "again", Attachments: []llm.Attachment{{Kind: llm.AttachmentImage, Name: "b.png", MIME: "image/png", Data: []byte{4}}}})
	if n := s.degradeOldAttachments(); n != 1 {
		t.Fatalf("degraded %d, want 1", n)
	}
	if len(s.Messages[0].Attachments[0].Data) != 0 || !strings.Contains(s.Messages[0].Attachments[0].Note, "shown earlier") {
		t.Fatalf("old attachment should be text-only: %+v", s.Messages[0].Attachments[0])
	}
	if len(s.Messages[2].Attachments[0].Data) != 1 {
		t.Fatal("newest attachment must keep its bytes")
	}
}
