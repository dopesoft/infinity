package agent

import (
	"sort"
	"strings"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// catalogBlockHeader is the opening line of the dormant-tool catalog the
// loop prepends to the system prompt. The text is intentionally directive:
// the model must learn that a name appearing here is real and reachable
// via tool_search → load_tools, not a phantom.
const catalogBlockHeader = `<tool_catalog>
The following tools exist but their JSON schemas are NOT in your current
toolset to save context. They are real and callable.

Primitives (pinned, always available):
  - system_map() - runtime topology. Lists every backing table, its
    list_tools, mutate_tools, registered actions, live open_count, AND
    the gaps where a tool is missing. Use this FIRST for any task about
    user-facing data ("dismiss my X", "what's on my dashboard", "act on
    the queue"). No prompt-level memorisation.
  - mem_list({table}) / mem_act({table, action, ids}) - GENERIC reader
    and bounded-action mutator over ANY mem_* table. Pair these with
    system_map for one-shot dismissals on any surface, even ones that
    have no bespoke list/mutate tool yet. The action vocabulary is
    bounded by mem_action_schemas (set_status / set_timestamp /
    set_null / set_bool) - no raw SQL.
  - action_register({table, action, op, column, value?}) - extend
    mem_act's vocabulary at runtime. New table, new action → one
    register call, no deploy.
  - domain_hint_add({table, tool_prefix}) - extend system_map's
    topology when a new mem_X has irregular tool naming.
  - tool_search("query") - search the dormant catalog for non-surface
    capabilities (web_search, delegate, claude_code__*). NOT
    claude_code__ToolSearch - that indexes a different registry.

Bring dormant tools online with load_tools(["name1","name2"]).
NEVER ask the boss for record ids; ids aren't shown in the UI -
system_map + mem_list give you everything you need.

Self-extension flow when you hit a gap:
  1. system_map() - see the missing list_tool or mutate_tool.
  2. mem_list({table}) - read works on ANY mem_* table out of the box.
  3. If you need a new action verb, action_register(...) - persisted.
  4. mem_act({table, action, ids}) - apply.

Format: name - short description.
`

// buildToolCatalogBlock renders the dormant catalog into a system-prompt
// block. Returns "" when there's nothing dormant (e.g. small registries
// where everything is active) so the prompt stays clean.
//
// We collapse Composio's per-toolkit explosion into a one-row-per-toolkit
// summary because rendering all 250 verbs would defeat the purpose. Each
// composio__TOOLKIT_* family shows as a single entry pointing the model
// at tool_search for the specific verb. Other dormant tools render
// individually since they're long-tail not catalog-tail.
func buildToolCatalogBlock(reg *tools.Registry, active *tools.ActiveSet, hidden map[string]struct{}) string {
	if reg == nil || active == nil {
		return ""
	}
	dormant := reg.DormantCatalog(active.Names())
	if len(dormant) == 0 {
		return ""
	}

	// Group composio__ entries by toolkit (first underscore-segment after
	// the prefix) so the catalog stays scannable instead of dumping 60
	// gmail verbs into the prompt.
	composioToolkits := map[string]int{}
	regular := make([]tools.CatalogEntry, 0, len(dormant))
	for _, d := range dormant {
		// Honor the per-session hidden set so tools we're filtering out
		// of schemas (e.g. claude_code__* on Cloud) also disappear from
		// the catalog - otherwise the model wastes turns on
		// tool_search/load_tools for things it can't use.
		if _, drop := hidden[d.Name]; drop {
			continue
		}
		if strings.HasPrefix(d.Name, "composio__") {
			tail := strings.TrimPrefix(d.Name, "composio__")
			toolkit := tail
			if i := strings.Index(tail, "_"); i > 0 {
				toolkit = tail[:i]
			}
			composioToolkits[toolkit]++
			continue
		}
		regular = append(regular, d)
	}

	var b strings.Builder
	b.WriteString(catalogBlockHeader)
	b.WriteString("\n")

	// Regular dormant tools first.
	for _, d := range regular {
		desc := strings.TrimSpace(d.Description)
		if len(desc) > 140 {
			desc = desc[:137] + "…"
		}
		b.WriteString("- ")
		b.WriteString(d.Name)
		b.WriteString(" - ")
		b.WriteString(desc)
		b.WriteString("\n")
	}

	// Composio toolkit summaries (alphabetised).
	if len(composioToolkits) > 0 {
		keys := make([]string, 0, len(composioToolkits))
		for k := range composioToolkits {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\nComposio toolkits (use tool_search with the toolkit name to find verbs):\n")
		for _, k := range keys {
			count := composioToolkits[k]
			b.WriteString("- composio__")
			b.WriteString(k)
			b.WriteString("_* ")
			b.WriteString("(")
			if count == 1 {
				b.WriteString("1 verb")
			} else {
				b.WriteString(itoa(count))
				b.WriteString(" verbs")
			}
			b.WriteString(")\n")
		}
	}
	b.WriteString("</tool_catalog>")
	return b.String()
}

// itoa is a tiny stdlib-free int-to-string for the catalog count - keeps
// this file dependency-light.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
