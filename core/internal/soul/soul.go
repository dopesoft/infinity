// Package soul carries Jarvis's identity - the system prompt that defines
// who the agent is, how he speaks, and how he operates. The default soul is
// embedded at build time so the distroless container always has it; setting
// INFINITY_SOUL_PATH at runtime swaps in a file from disk for live iteration.
package soul

import (
	_ "embed"
	"os"
	"strings"
)

//go:embed soul.md
var defaultSoul string

// Load returns the system prompt to use. If INFINITY_SOUL_PATH is set and
// readable, that file wins; otherwise the embedded default ships.
//
// Returns the prompt and the source description ("embedded" or the path) for
// startup logging.
func Load() (prompt string, source string) {
	if path := strings.TrimSpace(os.Getenv("INFINITY_SOUL_PATH")); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			s := strings.TrimSpace(string(b))
			if s != "" {
				return s, path
			}
		}
	}
	return strings.TrimSpace(defaultSoul), "embedded"
}
