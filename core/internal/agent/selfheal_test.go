package agent

import "testing"

// These tests pin the BEHAVIOR the boss asked for: when Jarvis hands back an
// unresolved failure he could fix himself, the loop must trigger a self-heal
// pass; when he succeeds (or is merely asking a question), it must NOT — so the
// reflex never turns a normal turn into a wasteful grind.
func TestShouldSelfHeal(t *testing.T) {
	t.Setenv("INFINITY_SELF_HEAL", "") // ensure enabled

	cases := []struct {
		name    string
		reply   string
		toolErr bool
		want    bool
	}{
		// The failures the boss is tired of seeing handed back:
		{"couldnt complete", "I couldn't complete the higgsfield login.", false, true},
		{"didnt work", "That didn't work — the page wouldn't load.", false, true},
		{"errored tool", "Hit an error reaching the API.", true, true},
		{"blocked", "I'm blocked on the build failing.", false, true},
		{"empty after tool error", "", true, true},

		// The exact punt the boss keeps catching — confident, no failure words,
		// but tells him to open/sign in himself. Must still self-heal:
		{"browse to punt", "In the preview browser, open https://higgsfield.ai/auth/login and sign in there.", false, true},
		{"navigate punt", "Navigate to the login page and log in, then tell me done.", false, true},
		{"go to and sign in", "Go to higgsfield.ai/auth/login and sign in, then say done.", false, true},

		// Must NOT fire — these would just waste a pass:
		{"clean success", "Done. Deployed and confirmed the endpoint returns 200.", false, false},
		{"resolved past failure", "The build failed at first but I fixed it and it works now.", false, false},
		{"plain question", "Which Gmail account should I send from, work or personal?", false, false},
		{"normal answer", "Your next meeting is at 3pm with the design team.", false, false},
		{"empty no error", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldSelfHeal(c.reply, c.toolErr); got != c.want {
				t.Fatalf("shouldSelfHeal(%q, toolErr=%v) = %v, want %v", c.reply, c.toolErr, got, c.want)
			}
		})
	}
}

func TestSelfHealDisabledOff(t *testing.T) {
	t.Setenv("INFINITY_SELF_HEAL", "off")
	if shouldSelfHeal("I couldn't do it, it failed.", true) {
		t.Fatal("self-heal must be off when INFINITY_SELF_HEAL=off")
	}
}

// The two replies Jarvis actually sent on 2026-07-09, copied byte-for-byte out
// of mem_observations. Both end the turn on an unresolved problem. Neither
// triggered a heal pass, because every contraction he writes uses a typographic
// apostrophe (U+2019) and every pattern here spelled it ASCII. The boss asked
// for a YouTube transcript and got a shrug, while yt-dlp sat installed and
// active on the cloud workspace.
func TestSelfHealFiresOnTypographicApostrophes(t *testing.T) {
	turn1 := "I can do this, boss, but I need the transcript or at least the key points from the video first. " +
		"I can’t reliably break down a YouTube link from the ID alone, and I’m not going to bluff my way " +
		"through your money wiring, that would be sloppy."
	turn2 := "The bridge is the snag, boss, not the idea. I tried to pull the transcript with the YouTube " +
		"tooling, but the Mac bridge rejected it because that shell can’t see `/workspace`, so it never " +
		"actually ran. I need to use the cloud shell tools for this."

	if !shouldSelfHeal(turn1, false) {
		t.Error(`turn 1 ("I can’t reliably break down...") must trigger a self-heal pass`)
	}
	if !shouldSelfHeal(turn2, false) {
		t.Error(`turn 2 ("that shell can’t see /workspace") must trigger a self-heal pass`)
	}
}

// Every curly contraction the model actually emits must read as its ASCII twin.
func TestSelfHealApostropheVariants(t *testing.T) {
	for _, reply := range []string{
		"I couldn’t reach the API.",
		"That didn’t work.",
		"It isn’t working.",
		"I wasn’t able to load the page.",
		"I can`t see the file.",
		"I couldn´t sign in.",
	} {
		if !shouldSelfHeal(reply, false) {
			t.Errorf("must self-heal on %q", reply)
		}
	}
}

// The normalisation must not turn confident, resolved replies into heal loops.
func TestSelfHealStillSilentOnSuccess(t *testing.T) {
	for _, reply := range []string{
		"Pulled the transcript and broke it down below.",
		"Fixed it — the deploy is green and I’ve verified the endpoint returns 200.",
		"Here’s the summary you asked for.",
	} {
		if shouldSelfHeal(reply, false) {
			t.Errorf("must NOT self-heal on %q", reply)
		}
	}
}
