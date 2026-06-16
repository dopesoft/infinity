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
