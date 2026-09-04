package agent

// Exported seams over the generic tool-result trim, for the one caller that
// hands tool results to a brain WITHOUT going through the agent loop: the
// Infinity MCP server (internal/server/mcp_server.go), which the Claude Max
// brain calls tools through. Without this the trim only protected chat
// turns; a 200KB `go build` handed to Claude Code over MCP went into its
// transcript whole and was re-read on every one of its own calls.
//
// Both wrap the unexported originals in tool_result_trim.go so the loop and
// the MCP path can never disagree about the budget or the digest shape.

// TrimToolResult returns output unchanged when it is within budget, otherwise
// a head + tail + error-lines digest with an elision marker.
func TrimToolResult(name, output string) string { return trimToolResult(name, output) }

// ToolResultMaxChars is the char budget above which a result is trimmed; 0
// means trimming is disabled.
func ToolResultMaxChars() int { return toolResultMaxChars() }
