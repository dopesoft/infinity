package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// collectSSE runs readResponsesSSE against a canned SSE body and returns the
// parsed response, the terminal error, and every event emitted to `out`.
func collectSSE(t *testing.T, sse string) (Response, error, []StreamEvent) {
	t.Helper()
	out := make(chan StreamEvent, 64)
	var events []StreamEvent
	done := make(chan struct{})
	go func() {
		for ev := range out {
			events = append(events, ev)
		}
		close(done)
	}()
	resp, err := readResponsesSSE(strings.NewReader(sse), out)
	close(out)
	<-done
	return resp, err, events
}

func hasKind(events []StreamEvent, k StreamEventKind) bool {
	for _, e := range events {
		if e.Kind == k {
			return true
		}
	}
	return false
}

// The exact bug: a transient `server_error` arriving on the first event (before
// any content) must come back as retryable, and readResponsesSSE must NOT emit
// terminal events (Stream owns the retry-or-give-up decision). This is what
// turns the inbox-triage cron failure into an automatic retry.
func TestReadResponsesSSE_TransientErrorOnFirstEvent_IsRetryable(t *testing.T) {
	sse := `data: {"type":"error","error":{"type":"server_error","code":"server_error","message":"An error occurred while processing your request."},"sequence_number":1}` + "\n"
	resp, err, events := collectSSE(t, sse)

	var rt *retryableStreamError
	if !errors.As(err, &rt) {
		t.Fatalf("expected *retryableStreamError, got %T: %v", err, err)
	}
	if resp.Text != "" {
		t.Errorf("no content should have been parsed, got %q", resp.Text)
	}
	// Terminal events must be deferred to Stream — none emitted here.
	if hasKind(events, StreamError) || hasKind(events, StreamComplete) {
		t.Errorf("retryable path must not emit terminal events; got %+v", events)
	}
}

// If content already streamed, a later error CANNOT be retried (a retry would
// duplicate the output the caller already saw). It must surface as a plain
// error WITH terminal events emitted.
func TestReadResponsesSSE_ErrorAfterContent_IsNotRetryable(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial answer"}`,
		`data: {"type":"error","error":{"type":"server_error","message":"boom"},"sequence_number":2}`,
	}, "\n") + "\n"
	resp, err, events := collectSSE(t, sse)

	var rt *retryableStreamError
	if errors.As(err, &rt) {
		t.Fatal("error after emitted content must NOT be retryable (would duplicate output)")
	}
	if err == nil {
		t.Fatal("expected a terminal error")
	}
	if resp.Text != "partial answer" {
		t.Errorf("expected the streamed text to be preserved, got %q", resp.Text)
	}
	if !hasKind(events, StreamError) {
		t.Error("non-retryable error must emit a StreamError to the caller")
	}
}

// A non-transient error (e.g. a malformed request) must never be retried — that
// would just hammer the provider with the same bad request.
func TestReadResponsesSSE_NonTransientError_IsNotRetryable(t *testing.T) {
	sse := `data: {"type":"error","error":{"type":"invalid_request_error","message":"unknown parameter foo"},"sequence_number":1}` + "\n"
	_, err, _ := collectSSE(t, sse)

	var rt *retryableStreamError
	if errors.As(err, &rt) {
		t.Fatal("a non-transient (client) error must not be retryable")
	}
	if err == nil {
		t.Fatal("expected a terminal error")
	}
}

func TestIsTransientStatus(t *testing.T) {
	transient := []int{429, 500, 502, 503, 504, 529, 408, 425}
	for _, c := range transient {
		if !isTransientStatus(c) {
			t.Errorf("status %d should be transient", c)
		}
	}
	terminal := []int{400, 401, 403, 404, 422}
	for _, c := range terminal {
		if isTransientStatus(c) {
			t.Errorf("status %d should NOT be transient (client error)", c)
		}
	}
}

func TestIsTransientResponsesError(t *testing.T) {
	yes := []string{
		`{"error":{"type":"server_error"}}`,
		`overloaded_error`,
		`rate_limit exceeded`,
		`please try again later`,
		`status 503`,
	}
	for _, s := range yes {
		if !isTransientResponsesError(s) {
			t.Errorf("%q should be transient", s)
		}
	}
	no := []string{
		`{"error":{"type":"invalid_request_error"}}`,
		`model_not_found`,
		`content policy violation`,
	}
	for _, s := range no {
		if isTransientResponsesError(s) {
			t.Errorf("%q should NOT be transient", s)
		}
	}
}

// Caller cancellation is the loop's own budget, never a provider blip — it must
// not trigger a retry. Connection-level blips should.
func TestIsTransientNetErr(t *testing.T) {
	if isTransientNetErr(context.Canceled) {
		t.Error("context.Canceled must not be retryable")
	}
	if isTransientNetErr(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded must not be retryable")
	}
	if !isTransientNetErr(errors.New("read tcp: connection reset by peer")) {
		t.Error("connection reset should be retryable")
	}
	if !isTransientNetErr(errors.New("net/http: TLS handshake timeout")) {
		t.Error("TLS handshake timeout should be retryable")
	}
}

// The boss's 2026-08-25 report: the raw SSE line landed in his chat window
// verbatim, help-centre URL and request id included. What reaches a person must
// be the provider's own message, never the transport envelope.
func TestProviderErrorMessage_StripsEnvelopeAndBoilerplate(t *testing.T) {
	raw := `{"type":"error","error":{"type":"server_error","code":"server_error","message":"An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists. Please include the request ID 6e6adfff-3356-43cf-8f0b-f4bd09adceb4 in your message.","param":null},"sequence_number":22}`
	got := providerErrorMessage(raw)

	for _, leak := range []string{"sequence_number", `{"type"`, "help.openai.com", "request ID"} {
		if strings.Contains(got, leak) {
			t.Errorf("provider envelope leaked to the boss (%q) in: %s", leak, got)
		}
	}
	if !strings.Contains(got, "An error occurred while processing your request.") {
		t.Errorf("the provider's actual message must survive, got: %s", got)
	}
}

// A payload we can't parse must keep its raw text rather than become a vague
// sentence: hiding what broke is worse than showing something ugly.
func TestProviderErrorMessage_UnparseableKeepsRaw(t *testing.T) {
	got := providerErrorMessage("not json at all: upstream exploded")
	if !strings.Contains(got, "upstream exploded") {
		t.Errorf("unparseable payloads must keep their detail, got: %s", got)
	}
}

// Reasoning is a scratchpad, not an answer. A transient failure after only
// thinking has streamed must STILL be retryable — otherwise a provider hiccup
// mid-reasoning kills a turn that a retry would have completed.
func TestReadResponsesSSE_ErrorAfterThinkingOnly_IsRetryable(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"weighing the options"}`,
		`data: {"type":"error","error":{"type":"server_error","message":"An error occurred while processing your request."},"sequence_number":22}`,
	}, "\n") + "\n"
	_, err, events := collectSSE(t, sse)

	var rt *retryableStreamError
	if !errors.As(err, &rt) {
		t.Fatalf("thinking-only output must stay retryable, got %T: %v", err, err)
	}
	if hasKind(events, StreamError) || hasKind(events, StreamComplete) {
		t.Error("retryable path must leave terminal events to Stream")
	}
}
