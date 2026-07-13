package phone

import (
	"testing"
	"time"
)

// The bug this guards: the notes we inject into a live call QUOTE the function
// name ("...say a proper goodbye and use hangup_call..."). The old detector
// substring-matched the raw event, so the agent's own briefing, echoed back by
// the server as conversation.item.created, could end the call.
func TestFunctionCalledIgnoresTextThatMerelyMentionsTheName(t *testing.T) {
	echo := []byte(`{
	  "type": "conversation.item.created",
	  "item": {
	    "id": "item_1",
	    "type": "message",
	    "role": "system",
	    "content": [{"type": "input_text", "text": "When he has finished, say a proper goodbye and use hangup_call: your hands go to work the moment you do. You may also use patch_in_boss."}]
	  }
	}`)
	if calledFunction(echo, "hangup_call") {
		t.Fatal("a system note that MENTIONS hangup_call must never be read as CALLING it")
	}
	if calledFunction(echo, "patch_in_boss") {
		t.Fatal("a system note that MENTIONS patch_in_boss must never be read as CALLING it")
	}
}

func TestFunctionCalledDetectsRealInvocations(t *testing.T) {
	cases := map[string][]byte{
		"response.output_item.done": []byte(`{"type":"response.output_item.done","item":{"type":"function_call","name":"hangup_call","call_id":"c1","arguments":"{}"}}`),
		"response.done":             []byte(`{"type":"response.done","response":{"output":[{"type":"function_call","name":"hangup_call","call_id":"c1"}]}}`),
		"conversation.item.created": []byte(`{"type":"conversation.item.created","item":{"type":"function_call","name":"hangup_call","call_id":"c1"}}`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if !calledFunction(raw, "hangup_call") {
				t.Fatalf("a real function_call in %s was missed", name)
			}
		})
	}
	// An audio transcript is not a function call, whatever words it contains.
	speech := []byte(`{"type":"response.output_audio_transcript.done","transcript":"I shall hangup_call now"}`)
	if calledFunction(speech, "hangup_call") {
		t.Fatal("spoken words must never be read as a function call")
	}
}

func TestHangupAllowed(t *testing.T) {
	now := time.Date(2026, 7, 13, 22, 8, 30, 0, time.UTC)
	justSpoke := now.Add(-3 * time.Second)
	longGone := now.Add(-2 * time.Minute)

	cases := []struct {
		name       string
		humanText  string
		agentText  string
		humanAt    time.Time
		wantAllow  bool
		wantReason string
	}{
		{
			// The Jul 13 call, exactly: the boss had just given his ask and the
			// agent tried to leave to go and write the poem.
			name:       "mid-conversation hangup is refused",
			humanText:  "Our number is 929-310-0906.",
			agentText:  "Lovely request, let me take a moment to shape something tender for your wife.",
			humanAt:    justSpoke,
			wantAllow:  false,
			wantReason: "he was still on the line giving instructions",
		},
		{
			// "Cheers" is thanks, not goodbye, and the agent said it one second
			// before cutting a live call on Jul 13.
			name:      "a British thank-you is not a farewell",
			humanText: "Hey, so this is your boss.",
			agentText: "Cheers, let me acknowledge that and then we can get started with your request.",
			humanAt:   justSpoke,
			wantAllow: false,
		},
		{
			name:      "the human says goodbye",
			humanText: "That's all, thanks. Goodbye.",
			agentText: "Consider it done, sir.",
			humanAt:   justSpoke,
			wantAllow: true,
		},
		{
			name:      "the agent says goodbye",
			humanText: "Nope, that's everything.",
			agentText: "Very good, sir. Goodbye, and do drive safely.",
			humanAt:   justSpoke,
			wantAllow: true,
		},
		{
			name:      "dead air: the caller has gone",
			humanText: "Hold on a second",
			agentText: "Of course.",
			humanAt:   longGone,
			wantAllow: true,
		},
		{
			name:      "nobody ever spoke",
			humanText: "",
			agentText: "Good afternoon, Mr. Kai's office, this is Jarvis.",
			humanAt:   time.Time{},
			wantAllow: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hangupAllowed(tc.humanText, tc.agentText, tc.humanAt, now)
			if got != tc.wantAllow {
				t.Fatalf("hangupAllowed = %v, want %v (%s)", got, tc.wantAllow, tc.wantReason)
			}
		})
	}
}

// Barge-in: the boss cut Jarvis off to give the passphrase and it arrived as two
// separate transcript items. Neither half can match on its own.
func TestPassphraseSurvivesBargeInFragmentation(t *testing.T) {
	const phrase = "blue falcon"

	// Each fragment alone: no match, which is what happened on Jul 13.
	if _, v, _ := matchPassphrase("Blue", phrase); v {
		t.Fatal("half a passphrase must not verify on its own")
	}
	if _, v, _ := matchPassphrase("Falcon.", phrase); v {
		t.Fatal("half a passphrase must not verify on its own")
	}

	// Joined across the caller window: verified, the way monitor.go now does it.
	joined := "It's Blue" + " " + "Falcon."
	if _, v, _ := matchPassphrase(joined, phrase); !v {
		t.Fatal("an interrupted passphrase must verify once the window is joined")
	}

	// And every fragment is scrubbed out of the stored transcript.
	if got := redactFragments("It's Blue", phrase); got != "It's [passphrase]" {
		t.Fatalf("fragment redaction = %q, want %q", got, "It's [passphrase]")
	}
	if got := redactFragments("Falcon.", phrase); got != "[passphrase]." {
		t.Fatalf("fragment redaction = %q, want %q", got, "[passphrase].")
	}
}
