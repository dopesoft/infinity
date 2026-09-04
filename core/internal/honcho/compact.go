package honcho

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// RepMaxChars caps the peer representation folded into the system prompt.
//
// Measured on real prompts (2026-09-04): Honcho's "## Explicit Observations"
// section alone was ~10.9K chars / 100 lines, and most of those lines were the
// SAME fact captured day after day from a recurring cron prompt ("boss
// instructed that no messages should be sent directly and that drafts only
// should be created" with a fresh timestamp each morning). At ~4 chars a token
// that is ~2.7K tokens per turn, on every brain, for one repeated sentence.
// The block is deduplicated by stem first (see normaliseStem), then cut here,
// newest first, with a trailer naming how many observations were dropped.
const RepMaxChars = 3000

// repTruncatedTrailer is appended when observations were dropped by the cap.
// %d is the number of observations that did not fit.
const repTruncatedTrailer = "(+%d more, ask recall for detail)"

// obsTimeLayout is how Honcho renders created_at inside the leading bracket
// (microseconds and timezone stripped by its _strip_microseconds_and_timezone).
const obsTimeLayout = "2006-01-02 15:04:05"

var (
	// obsLeadTimestamp matches the "[2026-05-28 06:00:02] " prefix Honcho puts
	// on every timestamped observation line.
	obsLeadTimestamp = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\]\s*`)
	// obsLeadID matches Honcho's optional "[id:<uuid>] " prefix
	// (format_as_markdown with include_ids=True).
	obsLeadID = regexp.MustCompile(`^\[id:[^\]]*\]\s*`)
	// obsLeadNumber matches a "1. " enumerator (Representation.__str__).
	obsLeadNumber = regexp.MustCompile(`^\d+\.\s+`)
	// obsLeadBullet matches a markdown bullet.
	obsLeadBullet = regexp.MustCompile(`^[-*•]\s+`)
	// stemLeadIn strips the reporting frame so "boss instructed that X",
	// "the boss said X" and "boss asked X" all collapse to "x". Kept to the
	// attribution verbs Honcho actually emits for a single peer; the subject
	// is optional so a bare "instructed that X" also folds.
	stemLeadIn = regexp.MustCompile(`^(?:the\s+)?(?:boss|user)?\s*(?:said|says|stated|states|instructed|instructs|told|tells|set|sets|asked|asks|requested|requests|mentioned|mentions|wants|wanted)\s+(?:that\s+)?`)
	// stemSpaces collapses any whitespace run to one space.
	stemSpaces = regexp.MustCompile(`\s+`)
)

// normaliseStem reduces one observation line to the key used to detect
// restatements of the same fact. Rules, applied in order:
//
//  1. strip a leading "[id:…] ", "N. " enumerator and "- " bullet;
//  2. strip the leading "[YYYY-MM-DD HH:MM:SS] " timestamp;
//  3. lowercase;
//  4. strip the attribution lead-in: optional "the", optional "boss"/"user",
//     one of said/stated/instructed/told/set/asked/requested/mentioned/wants
//     (present or past), optional "that";
//  5. collapse whitespace runs to a single space;
//  6. drop trailing punctuation (. , ; : ! ?) and surrounding space.
//
// Pure; the only thing it knows about Honcho is the bracket prefix shapes.
func normaliseStem(line string) string {
	s := strings.TrimSpace(line)
	s = obsLeadID.ReplaceAllString(s, "")
	s = obsLeadNumber.ReplaceAllString(s, "")
	s = obsLeadBullet.ReplaceAllString(s, "")
	s = obsLeadTimestamp.ReplaceAllString(s, "")
	s = strings.ToLower(s)
	s = stemLeadIn.ReplaceAllString(s, "")
	s = stemSpaces.ReplaceAllString(s, " ")
	s = strings.TrimRight(s, " .,;:!?")
	return strings.TrimSpace(s)
}

// observationAt extracts the timestamp from an observation's first line.
// Zero when the line carries none (inductive / contradiction rows).
func observationAt(line string) time.Time {
	s := strings.TrimSpace(line)
	s = obsLeadID.ReplaceAllString(s, "")
	s = obsLeadNumber.ReplaceAllString(s, "")
	s = obsLeadBullet.ReplaceAllString(s, "")
	m := obsLeadTimestamp.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}
	}
	t, err := time.Parse(obsTimeLayout, m[1])
	if err != nil {
		return time.Time{}
	}
	return t
}

type repObservation struct {
	at    time.Time
	lines []string // first line + indented continuation (premises, sources)
	stem  string
	seq   int // arrival order, for a stable tiebreak
}

type repSection struct {
	heading string // "" for prose before the first heading
	prose   []string
	obs     []repObservation
}

// isObservationStart reports whether a (trimmed, prefix-stripped) line opens
// a new observation: Honcho starts every one with a "[timestamp]" bracket, or
// with a bold marker for the un-timestamped inductive / contradiction rows.
func isObservationStart(trimmed string) bool {
	s := obsLeadID.ReplaceAllString(trimmed, "")
	s = obsLeadNumber.ReplaceAllString(s, "")
	s = obsLeadBullet.ReplaceAllString(s, "")
	return strings.HasPrefix(s, "[") || strings.HasPrefix(s, "**")
}

// isContinuation reports whether an indented line belongs to the observation
// above it: Honcho indents premises / sources as "   - …" bullets under an
// "   Premises:"-style label.
func isContinuation(raw, trimmed string) bool {
	if raw == trimmed { // no leading whitespace
		return false
	}
	return strings.HasPrefix(trimmed, "-") || strings.HasSuffix(trimmed, ":")
}

func parseRepresentation(rep string) []repSection {
	var (
		sections []repSection
		cur      = repSection{}
		seq      int
	)
	flush := func() {
		if cur.heading != "" || len(cur.prose) > 0 || len(cur.obs) > 0 {
			sections = append(sections, cur)
		}
	}
	for _, raw := range strings.Split(rep, "\n") {
		trimmed := strings.TrimSpace(raw)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "## "):
			flush()
			cur = repSection{heading: trimmed}
		case len(cur.obs) > 0 && isContinuation(raw, trimmed):
			last := &cur.obs[len(cur.obs)-1]
			last.lines = append(last.lines, raw)
		case isObservationStart(trimmed):
			seq++
			cur.obs = append(cur.obs, repObservation{
				at:    observationAt(trimmed),
				lines: []string{trimmed},
				stem:  normaliseStem(trimmed),
				seq:   seq,
			})
		default:
			cur.prose = append(cur.prose, trimmed)
		}
	}
	flush()
	return sections
}

// CompactRepresentation dedupes Honcho's peer representation by stem (newest
// instance of each fact wins, wherever it sits), orders every section newest
// first, and caps the whole block at RepMaxChars with a "(+N more…)" trailer.
// Returns the input untouched in shape (headings preserved, blank line
// between sections) when nothing needed folding.
func CompactRepresentation(rep string) string {
	return compactRepresentation(rep, RepMaxChars)
}

func compactRepresentation(rep string, maxChars int) string {
	sections := parseRepresentation(rep)
	if len(sections) == 0 {
		return ""
	}

	// Newest instance of each stem wins, across sections.
	newest := map[string]repObservation{}
	for _, sec := range sections {
		for _, o := range sec.obs {
			if o.stem == "" {
				continue
			}
			prev, seen := newest[o.stem]
			if !seen || o.at.After(prev.at) {
				newest[o.stem] = o
			}
		}
	}
	for si := range sections {
		kept := sections[si].obs[:0]
		for _, o := range sections[si].obs {
			if o.stem != "" && newest[o.stem].seq != o.seq {
				continue
			}
			kept = append(kept, o)
		}
		sort.SliceStable(kept, func(i, j int) bool {
			// Newest first; undated rows sink below dated ones.
			if kept[i].at.Equal(kept[j].at) {
				return kept[i].seq > kept[j].seq
			}
			return kept[i].at.After(kept[j].at)
		})
		sections[si].obs = kept
	}

	// Render under the cap. The trailer is reserved up front so the budget
	// never lies: when we drop anything the trailer always fits.
	var b strings.Builder
	trailerReserve := len(repTruncatedTrailer) + 8
	budget := maxChars - trailerReserve
	if budget < 0 {
		budget = 0
	}
	dropped := 0
	wrote := false
	over := false
	write := func(s string) bool {
		if over || b.Len()+len(s) > budget {
			over = true
			return false
		}
		b.WriteString(s)
		wrote = true
		return true
	}
	for _, sec := range sections {
		if len(sec.prose) == 0 && len(sec.obs) == 0 {
			continue
		}
		var chunk strings.Builder
		if wrote {
			chunk.WriteString("\n")
		}
		if sec.heading != "" {
			chunk.WriteString(sec.heading)
			chunk.WriteString("\n")
		}
		for _, p := range sec.prose {
			chunk.WriteString(p)
			chunk.WriteString("\n")
		}
		if !write(chunk.String()) {
			dropped += len(sec.obs)
			continue
		}
		for _, o := range sec.obs {
			if over || !write(strings.Join(o.lines, "\n")+"\n") {
				dropped++
			}
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if dropped > 0 {
		out += "\n" + fmt.Sprintf(repTruncatedTrailer, dropped)
	}
	return out
}
