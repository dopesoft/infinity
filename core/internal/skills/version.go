// version.go - canonical skill version format helpers.
//
// Format: v<MAJOR>.<MINOR>-<M>-<D>-<YYYY>
//
//	v1.0-5-19-2026        first version, May 19 2026
//	v1.1-5-20-2026        next evolution, May 20 2026
//
// One shape across every producer (loader fallback, Voyager
// auto-evolution, GEPA optimizer, manually-authored frontmatter).
// Lexically NOT sortable by intent - "last edited" is read from
// mem_skills.updated_at, not parsed from the version string. The date
// suffix is for at-a-glance "when was this version created?" without
// the boss having to open the row.

package skills

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// versionRE captures (major, minor, m, d, yyyy).
var versionRE = regexp.MustCompile(`^v(\d+)\.(\d+)-(\d+)-(\d+)-(\d+)$`)

// InitialVersion returns the canonical "first version" label for a skill
// being created today. e.g. v1.0-5-19-2026 on May 19, 2026.
func InitialVersion() string {
	return formatVersion(1, 0, time.Now().UTC())
}

// BumpVersion produces the next version when an evolution lands. The
// minor part increments; the date is rewritten to today. Examples:
//
//	BumpVersion("v1.0-5-19-2026") → "v1.1-5-19-2026" (same day evolve)
//	BumpVersion("v1.4-3-12-2026") → "v1.5-5-19-2026" (today, whatever today is)
//
// Unparseable parents (legacy / hand-typed weirdness) fall back to a
// fresh v1.0 so we never propagate garbage forward.
func BumpVersion(parent string) string {
	major, minor, ok := parseMajorMinor(parent)
	if !ok {
		return InitialVersion()
	}
	return formatVersion(major, minor+1, time.Now().UTC())
}

// IsCanonical reports whether s is already in the canonical shape. The
// loader uses this to decide whether to leave a frontmatter-supplied
// version alone or rewrite to InitialVersion().
func IsCanonical(s string) bool {
	return versionRE.MatchString(strings.TrimSpace(s))
}

func formatVersion(major, minor int, t time.Time) string {
	// Month/day intentionally NOT zero-padded - the boss's example
	// "v1.0-5-19-2026" reads cleaner than "v1.0-05-19-2026".
	return fmt.Sprintf("v%d.%d-%d-%d-%d", major, minor, int(t.Month()), t.Day(), t.Year())
}

func parseMajorMinor(s string) (int, int, bool) {
	m := versionRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return major, minor, true
}
