package agent

import "testing"

// Why: this function decides whether a 14-minute build lives or dies, and it
// is the ONLY thing standing between "how's it going?" and a thrown-away
// afternoon. The false-positive cases matter more than the true ones: a
// missed stop costs one press of the Stop button, a wrong stop costs the work.
func TestIsStopIntent(t *testing.T) {
	stops := []string{
		"stop",
		"STOP",
		"stop.",
		"stop!",
		"ok stop",
		"stop please",
		"please stop",
		"just stop",
		"stop it",
		"stop building",
		"stop the build",
		"stop coding",
		"stop that",
		"stop everything",
		"i said stop",
		"cancel",
		"cancel that",
		"cancel the build",
		"cancel it",
		"abort",
		"halt",
		"nevermind",
		"never mind",
		"forget it",
		"call it off",
		"stand down",
		"kill it",
		"kill the build",
		"don't build",
		"dont build",
		"do not build",
		"don’t build that", // curly apostrophe: how a phone writes it
		"well i just told u not to build anything",
		"don't continue",
		"no more building",
		"scrap it",
		"abandon it",
		"wait stop",
		"whoa stop stop stop",
	}
	for _, s := range stops {
		if !isStopIntent(s) {
			t.Errorf("must read as an explicit stop: %q", s)
		}
	}

	keepGoing := []string{
		"how's it going",
		"hows it going?",
		"how is that going",
		"also add a settings page",
		"also, add X",
		"nice",
		"nice work",
		"cool",
		"thanks",
		"can you also run the tests after",
		"what file are you on",
		"don't stop",
		"dont stop",
		"do not stop",
		"don't stop building",
		"no need to cancel",
		"keep going",
		"keep building",
		"carry on",
		"take your time",
		"no rush",
		"finish it",
		"nonstop",                             // "stop" inside another word
		"stopwatch",                           // ditto
		"stop-and-go traffic is why i'm late", // long prose that merely contains the word
		"cancel the 3pm meeting when you get a sec",
		"can you cancel my dentist appointment tomorrow morning please",
		"i was stopped at a light",
		"",
		"   ",
		"?",
	}
	for _, s := range keepGoing {
		if isStopIntent(s) {
			t.Errorf("must NOT kill the job: %q", s)
		}
	}
}

// Why: the boss often types twice - a question, then the real order. The
// decision is made over EVERY message consumed in one interruption, not just
// the first one that happened to arrive.
func TestAnyStopIntent(t *testing.T) {
	if anyStopIntent([]Steer{{Text: "how's it going"}, {Text: "also add tests"}}) {
		t.Error("two questions must not kill the job")
	}
	if !anyStopIntent([]Steer{{Text: "how's it going"}, {Text: "actually stop"}}) {
		t.Error("a stop anywhere in the batch is a stop")
	}
	if anyStopIntent(nil) {
		t.Error("no messages is not a stop")
	}
}

// Why: Tier B is the risky half (a bare word taken as an order), so its bound
// is pinned: a message is only "the command itself" when every other word is
// filler.
func TestIsBareStopCommand_OnlyWhenTheMessageIsTheOrder(t *testing.T) {
	yes := []string{"stop", "ok stop now please", "cancel it", "just stop the build"}
	for _, s := range yes {
		if !isBareStopCommand(normalizeStopText(s)) {
			t.Errorf("%q is the whole order", s)
		}
	}
	no := []string{
		"cancel the 3pm meeting",
		"stop by the store on the way",
		"nothing to see here",
		"stop the deploy from going out to production tonight please", // prose, over the bound
	}
	for _, s := range no {
		if isBareStopCommand(normalizeStopText(s)) {
			t.Errorf("%q names something else, so it is not a bare stop order", s)
		}
	}
}
