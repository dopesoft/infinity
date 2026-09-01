"use client";

import { useEffect, useRef, useState } from "react";
import "@xterm/xterm/css/xterm.css";
import { RotateCcw } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { ptyStart, ptyInput, ptyResize, ptyClose, ptyStreamResponse } from "@/lib/canvas/api";
import { Button } from "@/components/ui/button";

// xterm themes aligned with Studio's tokens (true-black dark, white light) so
// the terminal follows the global light/dark toggle instead of always-black.
const XTERM_DARK = {
  background: "#000000",
  foreground: "#f5f5f5",
  cursor: "#f5f5f5",
  cursorAccent: "#000000",
  selectionBackground: "#2a2a2a",
};
const XTERM_LIGHT = {
  background: "#ffffff",
  foreground: "#141414",
  cursor: "#141414",
  cursorAccent: "#ffffff",
  selectionBackground: "#d8d8d8",
};

// useIsDark tracks the `.dark` class Studio toggles on <html>, mirroring the
// Monaco editor's theme detection so the terminal flips with the global toggle.
function useIsDark(): boolean {
  const [isDark, setIsDark] = useState(true);
  useEffect(() => {
    if (typeof document === "undefined") return;
    const root = document.documentElement;
    setIsDark(root.classList.contains("dark"));
    const mo = new MutationObserver(() => setIsDark(root.classList.contains("dark")));
    mo.observe(root, { attributes: true, attributeFilter: ["class"] });
    return () => mo.disconnect();
  }, []);
  return isDark;
}

/**
 * CanvasTerminal - a REAL interactive shell (xterm.js) backed by a PTY on the
 * cloud workspace (Jarvis's always-on computer, where installed CLIs and their
 * saved credentials live). Unlike the old one-shot command→result terminal,
 * this can run programs that wait for input: a device `auth login`, `ssh`, a
 * REPL, `vim`. Keystrokes stream to the PTY; output streams back over SSE.
 *
 * The PTY lives on the server, so the shell + scrollback survive navigation and
 * refresh - reconnecting replays recent output. Cleaned up (PTY killed) on
 * unmount.
 */
export function CanvasTerminal({ sessionId }: { sessionId: string }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<"connecting" | "ready" | "error">("connecting");
  const [generation, setGeneration] = useState(0); // bump to restart the shell
  const ptyIdRef = useRef<string>("");
  const termRef = useRef<import("@xterm/xterm").Terminal | null>(null);
  const isDark = useIsDark();
  const isDarkRef = useRef(isDark);
  isDarkRef.current = isDark;

  // Flip the live terminal's palette when the global theme toggles — no need to
  // tear down the shell.
  useEffect(() => {
    if (termRef.current) termRef.current.options.theme = isDark ? XTERM_DARK : XTERM_LIGHT;
  }, [isDark]);

  useEffect(() => {
    let disposed = false;
    const abort = new AbortController();
    let term: import("@xterm/xterm").Terminal | null = null;
    let fit: import("@xterm/addon-fit").FitAddon | null = null;
    let ro: ResizeObserver | null = null;
    let resizeTimer: ReturnType<typeof setTimeout> | null = null;

    (async () => {
      const host = hostRef.current;
      if (!host) return;
      setStatus("connecting");

      // Dynamic import keeps xterm out of the SSR bundle (it touches window).
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);
      if (disposed) return;

      term = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
        theme: isDarkRef.current ? XTERM_DARK : XTERM_LIGHT,
        convertEol: false,
        scrollback: 5000,
      });
      termRef.current = term;
      fit = new FitAddon();
      term.loadAddon(fit);
      term.open(host);
      try {
        fit.fit();
      } catch {
        /* host not laid out yet */
      }

      const id = await ptyStart(term.cols || 80, term.rows || 24);
      if (disposed) return;
      if (!id) {
        setStatus("error");
        term.writeln("\x1b[31mCouldn't start a shell — the cloud workspace isn't reachable.\x1b[0m");
        return;
      }
      ptyIdRef.current = id;
      setStatus("ready");

      // Keystrokes → PTY stdin.
      term.onData((d) => void ptyInput(id, d));

      // Window/pane resize → refit + tell the PTY (debounced).
      ro = new ResizeObserver(() => {
        if (resizeTimer) clearTimeout(resizeTimer);
        resizeTimer = setTimeout(() => {
          if (!fit || !term) return;
          try {
            fit.fit();
            void ptyResize(id, term.cols, term.rows);
          } catch {
            /* mid-layout */
          }
        }, 150);
      });
      ro.observe(host);

      // Output stream (SSE: `data: <base64>` lines). authedFetch carries the
      // bearer; we read the ReadableStream and decode chunks into the terminal.
      const res = await ptyStreamResponse(id, abort.signal);
      if (disposed || !res?.body) return;
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      try {
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          let nl: number;
          while ((nl = buf.indexOf("\n")) >= 0) {
            const line = buf.slice(0, nl);
            buf = buf.slice(nl + 1);
            if (line.startsWith("data: ")) {
              const b64 = line.slice(6);
              try {
                const bin = atob(b64);
                const bytes = new Uint8Array(bin.length);
                for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
                term?.write(bytes);
              } catch {
                /* skip malformed frame */
              }
            }
          }
        }
      } catch {
        /* stream ended / aborted */
      }
    })();

    return () => {
      disposed = true;
      abort.abort();
      if (resizeTimer) clearTimeout(resizeTimer);
      ro?.disconnect();
      if (ptyIdRef.current) void ptyClose(ptyIdRef.current);
      ptyIdRef.current = "";
      term?.dispose();
      termRef.current = null;
    };
    // generation restarts the shell on demand; a new session gets a fresh shell.
  }, [generation, sessionId]);

  return (
    <div className="relative flex h-full min-h-0 flex-col bg-background">
      <div className="flex h-8 shrink-0 items-center justify-between border-b border-border px-3 text-[11px] text-muted-foreground">
        <span className="inline-flex items-center gap-1.5">
          {status === "connecting" ? (
            <>
              <Spinner className="size-3" /> starting shell…
            </>
          ) : status === "error" ? (
            <span className="text-danger">workspace unreachable</span>
          ) : (
            <>
              <span className="size-1.5 rounded-full bg-success" /> cloud workspace
            </>
          )}
        </span>
        <Button
          size="sm"
          variant="ghost"
          className="h-6 gap-1 px-2 text-[11px]"
          onClick={() => setGeneration((g) => g + 1)}
          title="Restart the shell"
        >
          <RotateCcw className="size-3" />
          Restart
        </Button>
      </div>
      <div ref={hostRef} className="min-h-0 flex-1 overflow-hidden px-2 py-1" />
    </div>
  );
}
