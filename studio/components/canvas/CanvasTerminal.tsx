"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useWebSocket } from "@/lib/ws/provider";
import { runTerminalCommand } from "@/lib/canvas/api";
import { cn } from "@/lib/utils";

/**
 * CanvasTerminal - the Terminal tab (column 3, after Preview). One shell view
 * that shows BOTH:
 *
 *   • Commands the boss types here - executed directly on the session's active
 *     bridge (cloud /workspace or the Mac) via /api/canvas/terminal/exec. No
 *     Trust prompt: the boss typed it, same model as the git buttons.
 *   • Commands JARVIS runs - mirrored live from the WS tool stream (bash_run on
 *     cloud, claude_code__Bash on Mac), so the boss watches what the agent does
 *     in the shell in the same place.
 *
 * Works for cloud and Mac transparently - the backend routes by the session's
 * bridge, and Jarvis's commands carry whichever tool name the active bridge
 * uses. Output isn't a live PTY (no interactive programs); it's command →
 * result, which covers installs, git, builds, inspection.
 */

const BASH_TOOLS = new Set(["bash_run", "claude_code__bash"]);

type Line = {
  id: string;
  source: "you" | "jarvis";
  command: string;
  output: string;
  exitCode?: number;
  error?: string;
  running?: boolean;
};

export function CanvasTerminal({ sessionId }: { sessionId: string }) {
  const ws = useWebSocket();
  const [lines, setLines] = useState<Line[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const pendingRef = useRef<Set<string>>(new Set()); // jarvis tool_call ids awaiting result

  // Keep the latest session id without re-subscribing the WS handler.
  const sidRef = useRef(sessionId);
  sidRef.current = sessionId;

  // Auto-scroll to the newest line.
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines]);

  // Mirror Jarvis's shell commands from the WS tool stream.
  useEffect(() => {
    return ws.subscribe((ev) => {
      const sid = sidRef.current;
      if ("session_id" in ev && ev.session_id && sid && ev.session_id !== sid) return;
      if (ev.type === "tool_call") {
        const name = (ev.tool_call.name || "").toLowerCase();
        if (!BASH_TOOLS.has(name)) return;
        const inp = ev.tool_call.input || {};
        const cmd = String(inp.cmd ?? inp.command ?? "").trim();
        if (!cmd) return;
        pendingRef.current.add(ev.tool_call.id);
        setLines((prev) => [
          ...prev,
          { id: ev.tool_call.id, source: "jarvis", command: cmd, output: "", running: true },
        ]);
        return;
      }
      if (ev.type === "tool_result") {
        const id = ev.tool_result.id;
        if (!pendingRef.current.has(id)) return;
        pendingRef.current.delete(id);
        const out = unwrapOutput(ev.tool_result.output ?? "");
        setLines((prev) =>
          prev.map((l) => (l.id === id ? { ...l, output: out, running: false } : l)),
        );
        return;
      }
    });
  }, [ws]);

  const submit = useCallback(async () => {
    const cmd = input.trim();
    if (!cmd || busy || !sessionId) return;
    const id = `you-${Date.now()}`;
    setLines((prev) => [...prev, { id, source: "you", command: cmd, output: "", running: true }]);
    setInput("");
    setBusy(true);
    const res = await runTerminalCommand(cmd, sessionId);
    setBusy(false);
    setLines((prev) =>
      prev.map((l) =>
        l.id === id
          ? {
              ...l,
              output: res ? res.output : "",
              exitCode: res?.exit_code,
              error: res ? res.error : "could not reach the bridge",
              running: false,
            }
          : l,
      ),
    );
  }, [input, busy, sessionId]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-[#0a0a0a] font-mono text-[12px] leading-relaxed text-zinc-200">
      <div ref={scrollRef} className="min-h-0 flex-1 space-y-2 overflow-y-auto scroll-touch p-3">
        {lines.length === 0 && (
          <div className="text-zinc-500">
            Terminal — runs on this session&rsquo;s bridge (cloud <span className="text-zinc-400">/workspace</span> or
            your Mac). Type a command, or watch Jarvis&rsquo;s commands stream in here.
          </div>
        )}
        {lines.map((l) => (
          <div key={l.id}>
            <div className="flex items-start gap-1.5">
              <span
                className={cn(
                  "shrink-0 select-none",
                  l.source === "you" ? "text-info" : "text-tier-procedural",
                )}
              >
                {l.source} $
              </span>
              <span className="whitespace-pre-wrap break-all text-zinc-100">{l.command}</span>
            </div>
            {l.running ? (
              <div className="animate-pulse pl-3 text-zinc-500">running…</div>
            ) : (
              <>
                {l.output && (
                  <pre className="mt-0.5 whitespace-pre-wrap break-all pl-3 text-zinc-400">{l.output}</pre>
                )}
                {l.error && <div className="pl-3 text-danger">{l.error}</div>}
                {typeof l.exitCode === "number" && l.exitCode !== 0 && (
                  <div className="pl-3 text-warning">exit {l.exitCode}</div>
                )}
              </>
            )}
          </div>
        ))}
      </div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
        className="flex shrink-0 items-center gap-2 border-t border-zinc-800 px-3 py-2 pb-safe"
      >
        <span className="shrink-0 select-none text-info">$</span>
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder={busy ? "running…" : sessionId ? "type a command" : "start a session first"}
          disabled={busy || !sessionId}
          inputMode="text"
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          className="min-w-0 flex-1 bg-transparent font-mono text-[12px] text-zinc-100 placeholder:text-zinc-600 focus:outline-none disabled:opacity-50"
          aria-label="Terminal command"
        />
      </form>
    </div>
  );
}

// unwrapOutput flattens whatever the bridge returned into plain text. bash_run
// returns the raw output already; claude_code__Bash wraps it as
// {"stdout":"…","stderr":"…"}. Falls back to the raw string.
function unwrapOutput(raw: string): string {
  const s = (raw || "").trim();
  if (!s.startsWith("{")) return s;
  try {
    const obj = JSON.parse(s) as Record<string, unknown>;
    const parts: string[] = [];
    if (typeof obj.stdout === "string" && obj.stdout) parts.push(obj.stdout);
    if (typeof obj.stderr === "string" && obj.stderr) parts.push(obj.stderr);
    if (parts.length) return parts.join("\n").trim();
    if (typeof obj.output === "string") return obj.output;
    return s;
  } catch {
    return s;
  }
}
