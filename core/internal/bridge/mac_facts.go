package bridge

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Two facts the agent kept GUESSING, and now never has to.
//
// `code_agent` demands an absolute repo path, and nothing in the system ever
// told the model what the Mac's home directory is. So it inferred one from the
// boss's email address and produced `/Users/kai/Dev/infinity` — on 2026-08-26,
// again on 2026-08-27, and again this week, each time costing a round trip and
// a correction from the boss, who is fairly sick of it. `macPath` collapses the
// wrong home so the call still lands, but the boss still watches it type the
// wrong thing and has to say something.
//
// A guess is what you do when nobody told you. So we tell it: the real home,
// and the real list of repos, read off the Mac once and cached. Deterministic
// fact, injected every turn — not prose asking it to remember (Rule #1b).
//
// Same reasoning covers the repo names: "is it ELMAGO or elmago?" is another
// thing it was inventing, and one `ls` answers it forever.

// macFacts is what one probe learned about the Mac.
type macFacts struct {
	Home  string
	Repos []string
}

// factsTTL is long: a home directory does not move, and a new repo appearing
// within ten minutes is not worth a probe on every single turn.
const factsTTL = 10 * time.Minute

// macFactsScript is read-only and cheap: the home, then the directory names
// under ~/Dev that are actually git worktrees.
const macFactsScript = `echo "HOME:$HOME"
for d in "$HOME"/Dev/*/; do [ -d "$d.git" ] && echo "REPO:$(basename "$d")"; done
exit 0`

type factsCache struct {
	mu   sync.Mutex
	val  macFacts
	exp  time.Time
	last time.Time
}

// facts returns the cached Mac facts, probing at most once per TTL. On a failed
// probe it returns what it has (possibly nothing) and does not retry-storm: an
// unreachable Mac is already reported elsewhere in the overlay, and a missing
// home line simply means the overlay says nothing about it rather than
// asserting something false.
func (c *factsCache) facts(ctx context.Context, b Bridge) macFacts {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.exp) {
		return c.val
	}
	// Back off between failed probes so a sleeping Mac isn't hit every turn.
	if time.Since(c.last) < time.Minute {
		return c.val
	}
	c.last = time.Now()
	if b == nil {
		return c.val
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, status, ok := b.Post(pctx, "/bash", map[string]any{"cmd": macFactsScript, "timeout_sec": 8})
	if !ok || status >= 300 {
		return c.val
	}
	out, _ := BashOutput(body)
	got := parseMacFacts(out)
	if got.Home == "" {
		return c.val
	}
	c.val = got
	c.exp = time.Now().Add(factsTTL)
	return c.val
}

func parseMacFacts(out string) macFacts {
	var f macFacts
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "HOME:"):
			f.Home = strings.TrimSpace(strings.TrimPrefix(line, "HOME:"))
		case strings.HasPrefix(line, "REPO:"):
			if name := strings.TrimSpace(strings.TrimPrefix(line, "REPO:")); name != "" {
				f.Repos = append(f.Repos, name)
			}
		}
	}
	return f
}

// line renders the facts for the system prompt, or "" when nothing is known —
// silence beats asserting a home we did not read.
func (f macFacts) line() string {
	if f.Home == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Paths: the Mac's home directory is `" + f.Home + "` and his code lives in `" + f.Home + "/Dev/<repo>`. " +
		"That is read off the machine, so it is the truth — NEVER infer a home directory from his email address or his name " +
		"(`/Users/kai` is the wrong one you keep inventing). `~/Dev/<repo>` is always accepted too, and is the safer spelling.")
	if len(f.Repos) > 0 {
		const max = 24
		names := f.Repos
		more := 0
		if len(names) > max {
			more = len(names) - max
			names = names[:max]
		}
		b.WriteString(" Repos there right now: " + strings.Join(names, ", "))
		if more > 0 {
			b.WriteString(", and " + itoa(more) + " more")
		}
		b.WriteString(". Use these names exactly as spelled - don't guess the capitalisation.")
	}
	b.WriteString("\n")
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
