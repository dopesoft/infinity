package agent

import "testing"

// Verbatim from the boss's job-hunt thread on 2026-09-01. Both of these are
// good answers he read and then lost: each tripped the failure detector, each
// triggered a heal pass, and each pass's "Nothing broke, boss" note became the
// turn's final message and replaced the answer in his transcript. Neither turn
// ran a single tool. Nothing failed.
//
// They are stored verbatim because that is the only way this stays fixed: the
// detector reads English, so the test has to be the English that broke it.
const (
	realAnswerAboutJobBoards = `Built In is a solid add, boss. It's a curated tech job board (Chicago/Austin/DFW/Boston/NYC/LA are their strongest markets), lots of VP and Head of Product roles, and comp bands are usually posted right on the listing which makes the $200k+ filter reliable. DFW is one of their real markets so you'd get local hits too. Worth including from day one.

Google for Jobs is trickier. It's not really a board, it's an aggregator that scrapes every ATS and job site into one search surface, so it's the widest net you can cast. The catch is Google shut down their public jobs API back in 2024, so I can't pull it cleanly like I can with a normal board. I'd have to drive the search page in my cloud browser and scrape results. Doable, and honestly probably worth it for the coverage, but it's noisier: same job posted three places, listings without comp bands, occasional ghost posts. I'd treat it as a wide-net sweep that feeds into a dedupe step against the cleaner sources.

My read: Built In goes in the core rotation next to LinkedIn and Wellfound. Google for Jobs I'd run as a secondary sweep, maybe less often (2-3x a week vs nightly), specifically to catch the ones the curated boards miss. Sound right, or do you want Google in the nightly too?`

	realAnswerAboutArtifacts = `Fair pushback on both, boss. Let me take them separately.

On the artifact: you're right, Loom is only for companies with a public product I can actually poke. That was lazy shorthand from me. The stronger, universal version is what I'd call a "positioning read." I don't touch their product; I read everything public around it: recent news and funding, the CEO's posts and podcasts, their careers page, engineering blog, competitive landscape, review sites. From that I write a one-pager: where I think they are in their arc, what the next 6-12 months probably looks like from a product lens, and where I'd focus in the first 90 days if I got the role. Every company on earth has this surface area, even stealth ones (their team's tweets, the investor's thesis, the space itself). It lands harder than a Loom actually, because it shows you can reason from thin signals. When a company DOES have a public product, I add the Loom on top. So: positioning read as the default artifact, product teardown as a bonus when it applies.

On the shape: yes, this is a Pursuit, and honestly you just named the reason it should be. Same substrate we used for Psycho-Cybernetics: identity + long-running objective + a cadence between us + an immersive UX + the tools already in Jarvis. What I picture:

**Identity/objective:** "The AI-native product operator who ships. Land a remote Head of / VP / Sr Dir Product role at $200k+."

**The immersive view** (like the PC coaching mode): a Pipeline card showing every role I've surfaced and where it stands (Discovered / Reviewed / Applied / Outreached / Responded / Interview / Offer / Dead), a Corpus card that grows every week with stories and metrics you and I bank together, an Outreach card tracking each hiring manager I've identified and whether they've replied, and an Artifacts card holding every tailored resume, cover letter, and positioning read I've produced.

**The cadence between us:**
- Nightly: the sweep runs; morning dashboard shows top 5-8 fresh roles with fit scores, and I flag the 1-2 worth going high-touch on
- Per role you greenlight: I generate the tailored resume + cover letter + positioning read + a draft LinkedIn message to a named hiring manager (all in the canvas, you approve before anything goes out)
- Weekly (30 min): the immersive interview session, coach-led, like PC Day 1. I ask, you talk, I write it up into the Corpus, and future tailored resumes automatically get sharper.

**Tools it plugs into:** cloud browser for the sweep and for finding hiring managers on LinkedIn, ` + "`" + `document_create` + "`" + ` for the tailored docs, ` + "`" + `entity_upsert` + "`" + ` to build a world model of companies + contacts + roles, the world model then feeds future outreach so I stop cold-starting on companies you've already considered.

Two greenlights and I'll start building tonight:

1. Should I scaffold this as a Pursuit called "Land a Product Leader Role" and stand up the schema (pipeline states, corpus format, outreach tracking) so the immersive view has something to render?
2. And when do you want the first weekly interview session? I'd suggest Wednesday evening (say 7pm CT), 30 min, I run it in the immersive coach flow. If Wed doesn't work, pick another slot.`
)

func TestSelfHealDoesNotFireOnAnAnswerThatMerelyMentionsTrouble(t *testing.T) {
	if shouldSelfHeal(realAnswerAboutJobBoards, false) {
		t.Error(`"Google shut down their public jobs API ... so I can't pull it cleanly" is a fact about a vendor, not a failed turn`)
	}
	if shouldSelfHeal(realAnswerAboutArtifacts, false) {
		t.Error(`"If Wed doesn't work, pick another slot" is about his diary, not about this turn`)
	}
}

// And the reflex still has to catch what it was built for.
func TestSelfHealStillCatchesARealPunt(t *testing.T) {
	cases := []struct {
		name     string
		reply    string
		toolErr  bool
		wantHeal bool
	}{
		{"a thin shrug", "I couldn't get that to work.", false, true},
		{"a shrug with a tool error behind it", "That didn't work.", true, true},
		{"a long reply with a REAL failure and a tool error", realAnswerAboutArtifacts + " I ran into an error saving this and stopped.", true, true},
		{"handing him the browser", "Open the LinkedIn page in the preview and sign in, then tell me when you're done.", false, true},
		{"a clean answer", "Done. Wednesday at 7pm CT is on your calendar.", false, false},
		{"already fixed it", "The sync failed twice, so I patched the header and confirmed it works now.", false, false},
	}
	for _, c := range cases {
		if got := shouldSelfHeal(c.reply, c.toolErr); got != c.wantHeal {
			t.Errorf("%s: shouldSelfHeal = %v, want %v", c.name, got, c.wantHeal)
		}
	}
}
