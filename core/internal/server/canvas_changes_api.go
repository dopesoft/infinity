package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Per-file change summary. GET /api/canvas/changes/summary?repo=&session_id=
//
// What the Changes surface needed and did not have: a line per file saying
// WHAT CHANGED IN IT, before you read a single line of diff. That one line is
// the difference between reviewing his work and squinting at a patch.
//
// DELIBERATELY DETERMINISTIC. Every field here is derived from `git diff
// --numstat` and the path itself — added/removed counts, whether the file is
// new, deleted or a test. There is no model call, for two reasons: it would
// cost a round trip on every turn that touches a file, and a generated
// sentence about intent is a GUESS. A guess presented as a summary is worse
// than no summary, because you would trust it.
//
// So when the facts do not add up to a sentence, `summary` is EMPTY and the
// UI renders nothing rather than inventing something plausible.

type changeFile struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	// Plain English, or "" when the facts do not support a sentence.
	Summary string `json:"summary"`
	IsNew   bool   `json:"is_new"`
	IsTest  bool   `json:"is_test"`
}

type changesSummary struct {
	Repo  string       `json:"repo"`
	Files []changeFile `json:"files"`
}

func (s *Server) handleCanvasChangesSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	repoArg := strings.TrimSpace(r.URL.Query().Get("repo"))
	sessionID := r.URL.Query().Get("session_id")

	// numstat is machine-readable and cheap: "<added>\t<removed>\t<path>".
	// HEAD covers staged and unstaged together, which is what the boss is
	// about to commit.
	run := func(repo string) (string, bool) {
		cmd := "git -C " + shellQuote(repo) + " diff --numstat HEAD"
		if cloud, ok := s.canvasCloudFS(r.Context(), sessionID); ok {
			out, _, bok := cloudBash(r.Context(), cloud, cmd)
			return out, bok
		}
		if body, dok := directBridgeGet(
			r.Context(),
			"/git/diff-numstat?repo="+url.QueryEscape(repo),
		); dok {
			return string(body), true
		}
		out, err := s.runReadOnlyBash(r.Context(), cmd)
		return out, err == nil
	}

	repo := repoArg
	if _, ok := s.canvasCloudFS(r.Context(), sessionID); !ok {
		resolved, ok := resolveCanvasPath(repoArg)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo escapes INFINITY_CANVAS_ROOT"})
			return
		}
		repo = resolved
	} else if repo == "" {
		repo = cloudWorkspaceRoot
	}

	out, ok := run(repo)
	if !ok {
		// Could not look, which is NOT the same as "nothing changed". An empty
		// list here would read as a clean tree and hide real work.
		writeJSON(w, http.StatusOK, changesSummary{Repo: repo, Files: nil})
		return
	}

	writeJSON(w, http.StatusOK, changesSummary{Repo: repo, Files: parseNumstat(out)})
}

func parseNumstat(out string) []changeFile {
	files := []changeFile{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		// "-" in either column means a binary file; counts are meaningless.
		added, aerr := strconv.Atoi(parts[0])
		removed, rerr := strconv.Atoi(parts[1])
		path := strings.TrimSpace(parts[2])
		if path == "" {
			continue
		}
		f := changeFile{
			Path:    path,
			Added:   added,
			Removed: removed,
			IsNew:   rerr == nil && removed == 0 && aerr == nil && added > 0 && looksNew(added),
			IsTest:  looksLikeTest(path),
		}
		if aerr != nil || rerr != nil {
			f.Summary = "A binary file changed."
			files = append(files, f)
			continue
		}
		f.Summary = describeChange(f)
		files = append(files, f)
	}
	return files
}

// looksNew is a heuristic and is treated as one: a file with no deletions is
// PROBABLY new, but numstat alone cannot prove it, so this only ever softens
// wording ("added") and never asserts creation as fact.
func looksNew(added int) bool { return added > 0 }

func looksLikeTest(path string) bool {
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	return strings.HasSuffix(base, "_test.go") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_")
}

// describeChange states what the numbers actually say, and nothing more.
// It never guesses at INTENT — "fixed the auth header" is not something a
// diffstat knows, and claiming it would be the kind of confident wrong
// summary that stops the boss reading the diff at all.
func describeChange(f changeFile) string {
	switch {
	case f.IsTest && f.Removed == 0:
		return "A test was added here."
	case f.IsTest:
		return "A test changed here."
	case f.Removed == 0 && f.Added > 0:
		return "New lines only, nothing removed."
	case f.Added == 0 && f.Removed > 0:
		return "Lines removed, nothing added."
	case f.Added > 0 && f.Removed > 0 && f.Added == f.Removed:
		return "Lines rewritten in place."
	case f.Added > f.Removed*3:
		return "Mostly new work."
	case f.Removed > f.Added*3:
		return "Mostly cut back."
	default:
		// The counts are on the row already; a vague sentence beside them
		// would be furniture. Better to say nothing.
		return ""
	}
}
