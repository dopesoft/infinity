package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// World state: the overlays that describe the session's WORLD rather than
// this turn's request - which tools exist, which accounts are connected,
// which bridge is live, the boss's compass and goals. They change rarely
// (a connection, a load_tools, the Mac waking up) but were rebuilt into the
// volatile block on every LLM call: measured at ~40K of the 64K-char block,
// re-sent uncached on every call of every turn, and on the Claude brain
// stored in the session transcript once per turn forever.
//
// This is Codex's mechanism: it injects environment context, AGENTS.md and
// the tool listing as user-role items on the FIRST turn, snapshots them, and
// on later turns emits only a merge-patch of the sections that changed
// (codex-rs/core/src/session/mod.rs, context/world_state/). Claude Code does
// the same with reminder messages that stay in the transcript. So: the full
// block goes in once as a message that STAYS in the history (it becomes part
// of the cached prefix), and after that a turn carries at most a short
// update naming what changed.

// worldSectionTags are the XML tags, in render order, that the loop lifts
// out of the memory prefix into world state. Each provider wraps its block in
// its tag; anything untagged stays per-turn.
var worldSectionTags = []string{"tool_catalog", "connected_accounts", "bridge", "installed_cli_tools", "compass", "agent_goals"}

// worldStateCaption frames the first full send. It is addressed to the model
// as reference, so it is never mistaken for a request.
const worldStateCaption = "<world_state>\nThe state of your world for this conversation: the tools that exist, the accounts you can act through, the machine you are on, the boss's compass and goals. Reference material that stays true until an update below says otherwise. Not a request.\n"

// worldUpdateCaption frames a later diff.
const worldUpdateCaption = "<world_state_update>\nWhat changed in your world since the last update. Everything not mentioned is as before.\n"

// worldSnapshot is what a session remembers: per-section content hashes.
type worldSnapshot struct {
	hashes map[string]string
	sent   bool
}

// splitWorldSections lifts every <tag>…</tag> block named in
// worldSectionTags out of text. It returns the remaining text (per-turn
// context) and the lifted sections keyed by tag, in tag order.
func splitWorldSections(text string) (rest string, sections map[string]string, order []string) {
	sections = map[string]string{}
	rest = text
	for _, tag := range worldSectionTags {
		re := worldTagRegexp(tag)
		m := re.FindStringSubmatch(rest)
		if m == nil {
			continue
		}
		sections[tag] = strings.TrimSpace(m[0])
		order = append(order, tag)
		rest = strings.TrimSpace(strings.Replace(rest, m[0], "", 1))
	}
	// Collapse the blank runs the removals leave behind.
	rest = multiBlank.ReplaceAllString(rest, "\n\n")
	return rest, sections, order
}

var (
	tagRegexps = map[string]*regexp.Regexp{}
	multiBlank = regexp.MustCompile(`\n{3,}`)
)

func worldTagRegexp(tag string) *regexp.Regexp {
	if re, ok := tagRegexps[tag]; ok {
		return re
	}
	re := regexp.MustCompile(`(?s)<` + tag + `>.*?</` + tag + `>`)
	tagRegexps[tag] = re
	return re
}

func hashSection(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// worldStateMessage decides what, if anything, the turn must say about the
// world: the full block on the first send of a session, a diff after that,
// "" when nothing changed. It updates the snapshot in place.
func worldStateMessage(snap *worldSnapshot, sections map[string]string) string {
	if snap.hashes == nil {
		snap.hashes = map[string]string{}
	}
	keys := make([]string, 0, len(sections))
	for _, tag := range worldSectionTags {
		if _, ok := sections[tag]; ok {
			keys = append(keys, tag)
		}
	}
	if !snap.sent {
		if len(keys) == 0 {
			return ""
		}
		var b strings.Builder
		b.WriteString(worldStateCaption)
		for _, k := range keys {
			b.WriteString("\n")
			b.WriteString(sections[k])
			b.WriteString("\n")
			snap.hashes[k] = hashSection(sections[k])
		}
		b.WriteString("</world_state>")
		snap.sent = true
		return b.String()
	}
	var changed, removed []string
	for _, k := range keys {
		if snap.hashes[k] != hashSection(sections[k]) {
			changed = append(changed, k)
		}
	}
	for k := range snap.hashes {
		if _, ok := sections[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(removed)
	if len(changed) == 0 && len(removed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(worldUpdateCaption)
	for _, k := range changed {
		b.WriteString("\n")
		b.WriteString(sections[k])
		b.WriteString("\n")
		snap.hashes[k] = hashSection(sections[k])
	}
	for _, k := range removed {
		b.WriteString("\n<" + k + " removed=\"true\"/>\n")
		delete(snap.hashes, k)
	}
	b.WriteString("</world_state_update>")
	return b.String()
}
