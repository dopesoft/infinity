package llm

import "strings"

// How big the window actually is, in one place.
//
// Two things need this number and they must never disagree: the meter that
// shows the boss how full his context is, and the compaction that decides
// when to start summarising the thread away. A second copy of this table in
// Studio drifted and reported a 1M window as 200K; a threshold that ignored
// it compacted a 1M brain at 12% full. So it lives here, next to the models,
// and everything reads it.

// ContextWindow returns the model's EFFECTIVE input context window in
// tokens - the size our client actually gets, which is what the meter must
// divide by. Numbers verified against vendor model cards (last checked
// 2026-06-19); update in lock step with studio/lib/models-catalog.ts when a
// card changes. Order matters: most specific patterns first.
func ContextWindow(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	// Some paths carry a "vendor:model" form (e.g. "openai_oauth:gpt-5.4");
	// match on the bare model id so a prefix can't silently drop the lookup
	// to the default window.
	if i := strings.LastIndex(m, ":"); i >= 0 {
		m = m[i+1:]
	}

	// Claude Code takes TIER ALIASES as model ids ("opus", "opus[1m]",
	// "sonnet"), and an alias is what Settings stores when the boss picks the
	// plan brain - the running turns log model="opus[1m]". Without this the
	// lookup fell through every branch to the 200K default and told him his
	// 1M window was a fifth of its real size. Haiku is the one alias that
	// really is 200K.
	switch strings.SplitN(m, "[", 2)[0] {
	case "opus", "sonnet", "fable":
		return 1_000_000 // Claude 5 family: 1M as standard
	case "haiku":
		return 200_000 // Haiku 4.5
	}

	// Anthropic - effective 200K. Opus 4.6+/Sonnet 4.6 advertise a 1M window
	// but it requires the context-1m beta header, which anthropic.go does NOT
	// send, so >200K input is rejected -> effective 200K. The boss opts into
	// 1M by picking a model id carrying the "1m" marker (then we'd add the
	// header). Showing 1M here while the client caps at 200K would UNDER-report
	// fill (dangerous), so 200K is the honest default.
	if strings.HasPrefix(m, "claude-") {
		if strings.Contains(m, "1m") {
			return 1_000_000
		}
		// Claude 5 family (Opus 5, Sonnet 5, Fable 5) ships a 1M window as
		// standard, no beta header (model pages, checked 2026-08-26). Matched
		// on the family boundary so "claude-sonnet-4-5-…" stays 200K.
		if isClaude5Family(m) {
			return 1_000_000
		}
		return 200_000
	}

	// OpenAI gpt-5.x - window differs by MINOR version AND tier. Verified vs
	// OpenAI cards (all 128K max output):
	//   gpt-5.4, gpt-5.4-pro, gpt-5.5, gpt-5.5-pro, gpt-5.6(+sol/terra/luna): 1,050,000
	//   gpt-5.4-mini/-nano, gpt-5.2, gpt-5.1, gpt-5(+pro/mini/nano): 400,000
	if strings.HasPrefix(m, "gpt-5") {
		// mini / nano stay at 400K on every minor version (gpt-5.4-mini is
		// 400K even though gpt-5.4 is 1.05M) - exclude them first.
		if strings.Contains(m, "-mini") || strings.Contains(m, "-nano") {
			return 400_000
		}
		if strings.HasPrefix(m, "gpt-5.4") || strings.HasPrefix(m, "gpt-5.5") || strings.HasPrefix(m, "gpt-5.6") {
			return 1_050_000
		}
		return 400_000
	}
	// o-series reasoning: o1/o3/o4 are 200K; o1-mini is the 128K exception.
	if strings.HasPrefix(m, "o1-mini") {
		return 128_000
	}
	if strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") {
		return 200_000
	}
	// gpt-4.1 is the long-context one at ~1M (1,047,576); gpt-4o / gpt-4 at 128K.
	if strings.HasPrefix(m, "gpt-4.1") {
		return 1_000_000
	}
	if strings.HasPrefix(m, "gpt-4") {
		return 128_000
	}

	// DeepSeek - V4 flash/pro/flash-vision all ship a 1M window (vendor
	// pricing page, checked 2026-08-30). Wired: the vendor is in the Studio
	// catalog and the provider registry.
	if strings.HasPrefix(m, "deepseek") {
		return 1_000_000
	}

	// Google Gemini (verified vs Google's cards):
	//   Gemini 3 Flash: 200K (the small one) - check before the 3-family default
	//   Gemini 3 Pro / Deep Think: 1M
	//   Gemini 2.5 Pro / 2.5 Flash / 2.5 Flash-Lite / 2.0 Flash: 1M
	// (2.5 Pro is 1M, NOT 2M - that was the old Gemini 1.5 Pro.)
	if strings.HasPrefix(m, "gemini-3-flash") {
		return 200_000
	}
	if strings.HasPrefix(m, "gemini-3") {
		return 1_000_000
	}
	if strings.HasPrefix(m, "gemini-2.5") || strings.HasPrefix(m, "gemini-2.0") {
		return 1_000_000
	}

	return 200_000
}

// isClaude5Family reports a Claude 5 model id: "claude-<family>-5" followed
// by the end of the id, a date/variant suffix ("-…") or the 1M marker ("[…").
func isClaude5Family(m string) bool {
	for _, fam := range []string{"opus", "sonnet", "haiku", "fable"} {
		p := "claude-" + fam + "-5"
		if !strings.HasPrefix(m, p) {
			continue
		}
		rest := m[len(p):]
		if rest == "" || rest[0] == '-' || rest[0] == '[' || rest[0] == '.' {
			return true
		}
	}
	return strings.Contains(m, "mythos")
}
