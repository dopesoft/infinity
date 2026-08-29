package finish

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
)

// BridgeEvidence reads a repo's real state off whichever bridge serves the
// session, through the router's normal failover — so a Mac that has gone to
// sleep since the job ran doesn't mean "no evidence", it means the cloud
// answers instead.
//
// This is the first evidence-based input the system has for deciding what to
// do about work in progress. Every other classifier reads a RECEIPT — what the
// worker said about itself — which is exactly what is missing when a job is
// killed before it can write one. Six files with uncommitted changes is a fact
// no interruption can erase.
//
// Read-only by construction: four `git` queries, no mutation, nothing that
// could touch the boss's tree.
type BridgeEvidence struct {
	router *bridge.Router
	prefs  PreferenceFor
}

// PreferenceFor resolves a session's bridge preference (auto / mac / cloud).
// Supplied by the caller so this package doesn't reach into settings.
type PreferenceFor func(ctx context.Context, sessionID string) bridge.Preference

// NewBridgeEvidence returns a gatherer, or nil without a router (the poller
// then continues jobs while saying plainly that it could not look).
func NewBridgeEvidence(router *bridge.Router, prefs PreferenceFor) *BridgeEvidence {
	if router == nil {
		return nil
	}
	return &BridgeEvidence{router: router, prefs: prefs}
}

// evidenceScript asks four questions in one round trip. Caps are on the
// output, not the query, so a wildly dirty tree can't blow the bridge's
// response limit and lose the whole report.
const evidenceScript = `echo "===BRANCH==="; git rev-parse --abbrev-ref HEAD 2>/dev/null
echo "===HEAD==="; git log -1 --format='%h %s' 2>/dev/null
echo "===DIRTY==="; git status --porcelain 2>/dev/null | head -80
echo "===STAT==="; git diff --stat HEAD 2>/dev/null | tail -40
exit 0`

// Gather runs the read-only probe and returns what it found. A failure is
// reported in Report.Err and never as an empty-but-successful report: "I could
// not look" and "there was nothing there" lead to opposite decisions.
func (e *BridgeEvidence) Gather(ctx context.Context, sessionID, repo string) Report {
	rep := Report{Repo: repo}
	if e == nil || e.router == nil {
		rep.Err = "no bridge router is configured"
		return rep
	}
	if strings.TrimSpace(repo) == "" {
		rep.Err = "that run never recorded which repo it was working in"
		return rep
	}
	pref := bridge.PrefAuto
	if e.prefs != nil {
		pref = e.prefs(ctx, sessionID)
	}
	gctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, body, status, _, err := e.router.Call(gctx, pref, func(b bridge.Bridge) ([]byte, int, bool) {
		// The path may be spelled for the other bridge (a Mac job's repo read
		// from the cloud after a failover), so it is normalized for whichever
		// one actually answers.
		return b.Post(gctx, "/bash", map[string]any{
			"cmd":         evidenceScript,
			"cwd":         bridge.NormalizePath(b, repo),
			"timeout_sec": 20,
		})
	})
	if err != nil {
		rep.Err = fmt.Sprintf("the bridge could not be reached (%v)", err)
		return rep
	}
	if status >= 300 {
		rep.Err = fmt.Sprintf("the bridge answered %d for %s", status, repo)
		return rep
	}
	out, _ := bridge.BashOutput(body)
	parseEvidence(out, &rep)
	if rep.Branch == "" && rep.Head == "" && len(rep.Dirty) == 0 && rep.DiffStat == "" {
		// Every probe came back blank. That is not a clean repo — a clean repo
		// still reports a branch and a HEAD — it is a probe that did not run.
		rep.Err = "the probe ran but returned nothing, so that path may not be a git repo any more"
	}
	return rep
}

// parseEvidence splits the four sections and reads them into the report.
func parseEvidence(out string, rep *Report) {
	sections := map[string]string{}
	current := ""
	var buf []string
	flush := func() {
		if current != "" {
			sections[current] = strings.TrimSpace(strings.Join(buf, "\n"))
		}
		buf = buf[:0]
	}
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "===") && strings.HasSuffix(t, "===") {
			flush()
			current = strings.Trim(t, "=")
			continue
		}
		if current != "" {
			buf = append(buf, line)
		}
	}
	flush()

	rep.Branch = firstLine(sections["BRANCH"])
	rep.Head = firstLine(sections["HEAD"])
	rep.DiffStat = sections["STAT"]
	rep.Dirty = porcelainPaths(sections["DIRTY"])
}

// porcelainPaths pulls the file paths out of `git status --porcelain` lines.
// Each is `XY <path>`, or `XY <old> -> <new>` for a rename — the new name is
// the one that exists on disk, so that is the one reported.
func porcelainPaths(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[2:])
		if i := strings.Index(path, " -> "); i >= 0 {
			path = strings.TrimSpace(path[i+4:])
		}
		if path = strings.Trim(path, `"`); path != "" {
			out = append(out, path)
		}
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
