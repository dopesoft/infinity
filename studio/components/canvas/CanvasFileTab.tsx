"use client";

import dynamic from "next/dynamic";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  AlertCircle,
  ArrowUpRightFromSquare,
  Check,
  Files,
  GitCompare,
  Loader2,
  Pencil,
  Save,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { useCanvasStore } from "@/lib/canvas/store";
import { fetchCanvasFSRead, fetchCanvasGitShow, saveCanvasFile } from "@/lib/canvas/api";
import { fetchTrustContracts, type TrustContractDTO } from "@/lib/api";
import { INFINITY_DARK, INFINITY_LIGHT, registerInfinityThemes } from "@/lib/canvas/monaco-theme";
import { cn } from "@/lib/utils";

/**
 * CanvasFileTab - Monaco-backed view of a single file.
 *
 * Mode switch (top-right of the body):
 *   ┌─────────────────────┐
 *   │ [ Diff ] [ Edit ]   │   ← segmented control
 *   └─────────────────────┘
 *
 *   • Diff opens a Monaco DiffEditor with the original (working-tree-on-load)
 *     content on the left and the in-memory buffer (or staged head if no
 *     edits) on the right. Default mode when the file has unsaved changes
 *     this session.
 *   • Edit opens a normal Monaco editor. Cmd/Ctrl+S triggers save → POST
 *     /api/canvas/fs/save → Trust contract → boss approves → file lands
 *     on disk.
 *
 * Persistence rules:
 *   • Buffer is local to this tab. Cloning to a second tab would lose state;
 *     we deliberately don't allow opening the same path twice.
 *   • The status bar polls /api/trust-contracts when a save is pending so
 *     the bottom-right "Pending approval" badge flips to "Saved" without
 *     the user reloading.
 */

const SAVE_POLL_INTERVAL_MS = 2_000;

// Dynamic imports keep Monaco out of the initial chunk - it's ~600KB and
// only loads when a file tab is actually opened. ssr:false because Monaco
// can't render on the server.
const MonacoEditor = dynamic(
  () => import("@monaco-editor/react").then((m) => m.default),
  { ssr: false, loading: () => <MonacoSkeleton /> },
);
const MonacoDiffEditor = dynamic(
  () => import("@monaco-editor/react").then((m) => m.DiffEditor),
  { ssr: false, loading: () => <MonacoSkeleton /> },
);

type Mode = "diff" | "edit";

export function CanvasFileTab({
  path,
  isActive,
  sessionId,
}: {
  path: string;
  isActive: boolean;
  sessionId: string;
}) {
  const store = useCanvasStore();
  const isDirty = store.dirtyPaths.has(path);
  // Live content: what Jarvis is writing RIGHT NOW (streamed token-by-token)
  // or the complete content from a just-finished tool_call. When present we
  // render it live instead of the disk read, so the boss watches the file
  // fill in. Cleared on tool_result (endLiveFile) → falls back to the disk
  // diff/edit flow below, which is authoritative.
  const live = store.liveContent.get(path);
  const isStreaming = store.liveStreaming.has(path);
  const hasLive = live !== undefined;
  const [mode, setMode] = useState<Mode>(isDirty ? "diff" : "edit");
  // originalContent is the LEFT side of the diff editor. For a clean
  // file it equals on-disk (so diff vs your unsaved edits makes sense).
  // For a file Jarvis touched (dirty), it must instead be the version
  // at git HEAD - otherwise both sides are identical (Jarvis already
  // wrote to disk) and the diff renders empty.
  const [originalContent, setOriginalContent] = useState<string | null>(null);
  const [currentContent, setCurrentContent] = useState<string | null>(null);
  // diskBaseline holds the on-disk content captured at load time. We
  // keep this separately from originalContent so the Edit-mode "is
  // modified" calculation always compares your Monaco buffer against
  // what's actually on disk, regardless of what the diff editor uses
  // as its "original" side.
  const [diskBaseline, setDiskBaseline] = useState<string | null>(null);
  const [diffSource, setDiffSource] = useState<"head" | "disk">(isDirty ? "head" : "disk");
  const [baseSha, setBaseSha] = useState<string>("");
  const [language, setLanguage] = useState<string>("plaintext");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [saveState, setSaveState] = useState<{
    status: "idle" | "saving" | "pending" | "saved" | "error";
    contractId?: string;
    error?: string;
  }>({ status: "idle" });
  const editorTheme = useEditorTheme();

  // Load (or reload) the file. Bumping reloadKey forces a fresh fetch.
  // When the file is dirty (Jarvis touched it this session) we ALSO
  // fetch the HEAD version in parallel so the diff editor can show
  // git HEAD vs working tree - the real "what did Jarvis change"
  // comparison. The two fetches race but settle independently so the
  // editor can render the disk version immediately and swap in the
  // HEAD baseline as soon as it arrives.
  const [reloadKey, setReloadKey] = useState(0);
  useEffect(() => {
    // While the live buffer is showing (Jarvis writing, or full content from a
    // just-finished tool_call), don't fetch — the on-disk file is pre-edit and
    // would be stale. When endLiveFile clears the buffer (tool_result, file now
    // written), hasLive flips false and this effect runs, reading the
    // authoritative post-write content for the diff/edit view.
    if (hasLive) return;
    if (!isActive && diskBaseline !== null) return; // Don't fetch background tabs.
    let cancelled = false;
    setLoading(true);
    setLoadError(null);
    fetchCanvasFSRead(path, sessionId).then((r) => {
      if (cancelled) return;
      setLoading(false);
      if (!r) {
        setLoadError("Could not read file (mac bridge unreachable?)");
        return;
      }
      setDiskBaseline(r.content);
      setCurrentContent(r.content);
      setBaseSha(r.sha);
      setLanguage(r.language || "plaintext");
      // For clean files the diff baseline is just the disk content.
      // Dirty files overwrite this once git/show resolves below.
      if (!isDirty) {
        setOriginalContent(r.content);
        setDiffSource("disk");
      }
    });
    if (isDirty) {
      fetchCanvasGitShow(path, undefined, sessionId).then((r) => {
        if (cancelled) return;
        if (!r) return;
        // Use HEAD as the diff baseline regardless of `found` - when
        // not found (untracked / brand-new file Jarvis just added),
        // r.content is "" and the diff renders the whole file as
        // additions, which is exactly correct.
        setOriginalContent(r.content);
        setDiffSource("head");
      });
    }
    return () => {
      cancelled = true;
    };
    // Reload on path change or explicit reload trigger.
  }, [path, reloadKey, isActive, diskBaseline, isDirty, hasLive]);

  // "Modified" means the Monaco buffer diverges from what's on disk
  // (i.e. the boss made unsaved edits). It is NOT the same as
  // dirty-from-Jarvis: a file Jarvis edited is on-disk-current but the
  // boss hasn't typed anything → save button stays disabled.
  const isModified = useMemo(() => {
    if (diskBaseline === null || currentContent === null) return false;
    return currentContent !== diskBaseline;
  }, [diskBaseline, currentContent]);

  const onSave = useCallback(async () => {
    if (currentContent === null) return;
    setSaveState({ status: "saving" });
    const res = await saveCanvasFile({
      path,
      content: currentContent,
      base_sha: baseSha,
      session_id: sessionId,
    });
    if (!res) {
      setSaveState({ status: "error", error: "Save request failed." });
      return;
    }
    if (res.status === "conflict") {
      setSaveState({ status: "error", error: res.reason ?? "File changed on disk - reload to see it." });
      return;
    }
    if (res.contract_id) {
      setSaveState({ status: "pending", contractId: res.contract_id });
      return;
    }
    if (res.status === "denied") {
      setSaveState({ status: "error", error: res.reason ?? "Save denied." });
      return;
    }
    setSaveState({ status: "saved" });
  }, [path, currentContent, baseSha, sessionId]);

  // Poll the trust queue while a save is pending so the status bar flips
  // automatically. Stops as soon as it leaves "pending".
  useEffect(() => {
    if (saveState.status !== "pending" || !saveState.contractId) return;
    let cancelled = false;
    const id = setInterval(async () => {
      const contracts = await fetchTrustContracts("approved");
      if (cancelled || !contracts) return;
      const approved = contracts.find((c) => c.id === saveState.contractId);
      if (approved) {
        setSaveState({ status: "saved" });
        // After approval, the backend has written the file. Reset
        // diskBaseline so isModified clears (Save button disables),
        // and refresh originalContent unless the diff editor is
        // pinned to HEAD (in which case Jarvis's still-uncommitted
        // changes should keep showing as additions on top).
        setDiskBaseline(currentContent);
        if (diffSource !== "head") setOriginalContent(currentContent);
      } else {
        // Also poll 'denied' so a rejection flips the badge.
        const denied = await fetchTrustContracts("denied");
        if (denied?.some((c: TrustContractDTO) => c.id === saveState.contractId)) {
          setSaveState({ status: "error", error: "Save denied by the boss." });
        }
      }
    }, SAVE_POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [saveState.status, saveState.contractId, currentContent, diffSource]);

  // Keyboard: Cmd/Ctrl+S to save. Active only when the tab is in focus.
  useEffect(() => {
    if (!isActive) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "s") {
        e.preventDefault();
        if (mode === "edit" && isModified) void onSave();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [isActive, mode, isModified, onSave]);

  // Beforehand: register custom Monaco themes when the editor mounts.
  const handleEditorWillMount = useCallback((monaco: typeof import("monaco-editor")) => {
    registerInfinityThemes(monaco);
  }, []);

  // Track live editor instances so we can detach their models on unmount.
  // Without this, when the tab unmounts (route change, mode switch, canvas
  // close) Monaco disposes the underlying TextModel before the
  // DiffEditorWidget gets to reset its model, throwing "TextModel got
  // disposed before DiffEditorWidget model got reset".
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const editorRef = useRef<any>(null);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const handleEditorMount = useCallback((editor: any) => {
    editorRef.current = editor;
  }, []);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const handleDiffEditorMount = useCallback((editor: any) => {
    editorRef.current = editor;
  }, []);
  useEffect(() => {
    return () => {
      const ed = editorRef.current;
      if (!ed) return;
      try {
        if (typeof ed.getModel === "function" && typeof ed.setModel === "function") {
          const m = ed.getModel();
          // DiffEditor: getModel returns { original, modified }
          if (m && "original" in m && "modified" in m) {
            ed.setModel(null);
          } else {
            ed.setModel(null);
          }
        }
      } catch {
        // Editor already torn down; ignore.
      }
      editorRef.current = null;
    };
  }, []);

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* Mode bar */}
      <div className="flex h-9 shrink-0 items-center gap-2 border-b bg-muted/20 px-2 dark:bg-zinc-900/40">
        <Files className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground" title={path}>
          {path}
        </span>
        <div className="inline-flex items-center gap-0 rounded-md border bg-background p-0.5">
          <ModeButton active={mode === "diff"} onClick={() => setMode("diff")} icon={<GitCompare className="size-3" />} label="Diff" />
          <ModeButton active={mode === "edit"} onClick={() => setMode("edit")} icon={<Pencil className="size-3" />} label="Edit" />
        </div>
      </div>

      {/* Body */}
      <div className="relative min-h-0 flex-1">
        {hasLive && (
          <LiveStreamView
            content={live ?? ""}
            language={languageFromPath(path)}
            theme={editorTheme}
            beforeMount={handleEditorWillMount}
          />
        )}
        {!hasLive && loading && (
          <div className="absolute inset-0 flex items-center justify-center">
            <Loader2 className="size-5 animate-spin text-muted-foreground" />
          </div>
        )}
        {!hasLive && loadError && !loading && (
          <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-sm">
            <AlertCircle className="size-6 text-danger" />
            <p className="text-muted-foreground">{loadError}</p>
            <Button variant="ghost" size="sm" onClick={() => setReloadKey((k) => k + 1)}>
              Retry
            </Button>
          </div>
        )}
        {!hasLive && !loading && !loadError && originalContent !== null && currentContent !== null && (
          <>
            {mode === "diff" ? (
              <MonacoDiffEditor
                key={`${path}-diff-${reloadKey}`}
                height="100%"
                language={language}
                theme={editorTheme}
                original={originalContent}
                modified={currentContent}
                beforeMount={handleEditorWillMount}
                onMount={handleDiffEditorMount}
                options={{
                  readOnly: true,
                  originalEditable: false,
                  renderSideBySide: true,
                  fontSize: 13,
                  minimap: { enabled: false },
                  scrollBeyondLastLine: false,
                  smoothScrolling: true,
                  wordWrap: "off",
                  automaticLayout: true,
                }}
              />
            ) : (
              <MonacoEditor
                key={`${path}-edit-${reloadKey}`}
                height="100%"
                language={language}
                theme={editorTheme}
                value={currentContent}
                onChange={(v) => setCurrentContent(v ?? "")}
                beforeMount={handleEditorWillMount}
                onMount={handleEditorMount}
                options={{
                  fontSize: 13,
                  minimap: { enabled: false },
                  scrollBeyondLastLine: false,
                  smoothScrolling: true,
                  wordWrap: "on",
                  tabSize: 2,
                  automaticLayout: true,
                }}
              />
            )}
          </>
        )}
      </div>

      {/* Status bar */}
      <div className="flex h-7 shrink-0 items-center gap-2 border-t bg-muted/20 px-2 text-[10px] text-muted-foreground dark:bg-zinc-900/40">
        <span className="font-mono uppercase">{hasLive ? languageFromPath(path) : language}</span>
        <span className="text-muted-foreground/40">·</span>
        {/* Live-writing indicator — what Jarvis is doing to this file right now. */}
        {hasLive && (
          <span className="flex items-center gap-1 text-info">
            {isStreaming ? (
              <>
                <Loader2 className="size-3 animate-spin" /> Jarvis is writing…
              </>
            ) : (
              <>
                <Pencil className="size-3" /> Written
              </>
            )}
          </span>
        )}
        {!hasLive && mode === "diff" && (
          <>
            <span
              className="font-mono"
              title={
                diffSource === "head"
                  ? "Diff is HEAD (left) → working tree (right) - Jarvis's uncommitted edits"
                  : "Diff is on-disk (left) → your unsaved edits (right)"
              }
            >
              vs {diffSource === "head" ? "HEAD" : "disk"}
            </span>
            <span className="text-muted-foreground/40">·</span>
          </>
        )}
        {!hasLive && (
          <>
            {saveState.status === "idle" && isModified && <span className="text-warning">Modified</span>}
            {saveState.status === "idle" && !isModified && isDirty && diffSource === "head" && (
              <span className="text-warning" title="Jarvis edited this file this session - not yet committed">
                Jarvis-edited
              </span>
            )}
            {saveState.status === "idle" && !isModified && !(isDirty && diffSource === "head") && <span>Clean</span>}
            {saveState.status === "saving" && (
              <span className="flex items-center gap-1">
                <Loader2 className="size-3 animate-spin" /> Saving…
              </span>
            )}
            {saveState.status === "pending" && (
              <a
                href="/trust"
                className="inline-flex items-center gap-1 text-warning hover:underline"
                title="Open Trust queue"
              >
                <ArrowUpRightFromSquare className="size-3" /> Pending approval
              </a>
            )}
            {saveState.status === "saved" && (
              <span className="flex items-center gap-1 text-success">
                <Check className="size-3" /> Saved
              </span>
            )}
            {saveState.status === "error" && (
              <span className="flex items-center gap-1 text-danger" title={saveState.error}>
                <AlertCircle className="size-3" /> {saveState.error}
              </span>
            )}
          </>
        )}
        <span className="ml-auto flex items-center gap-1.5">
          {!hasLive && mode === "edit" && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-6 gap-1 px-2 text-[10px]"
              disabled={!isModified || saveState.status === "saving" || saveState.status === "pending"}
              onClick={() => void onSave()}
              title="Cmd/Ctrl+S"
            >
              <Save className="size-3" /> Save
            </Button>
          )}
        </span>
      </div>
    </div>
  );
}

function ModeButton({
  active,
  onClick,
  icon,
  label,
}: {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "inline-flex h-6 items-center gap-1 rounded-sm px-2 text-[11px] font-medium transition-colors",
        active ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:text-foreground",
      )}
    >
      {icon}
      {label}
    </button>
  );
}

// LiveStreamView renders the content Jarvis is writing right now — read-only,
// auto-scrolling to the tail so the boss watches it fill in. Separate from the
// disk-backed editor below so it has zero coupling to save/diff state; it just
// mirrors the live buffer. When the tool finishes (endLiveFile) this unmounts
// and the authoritative disk editor takes over.
function LiveStreamView({
  content,
  language,
  theme,
  beforeMount,
}: {
  content: string;
  language: string;
  theme: string;
  beforeMount: (monaco: typeof import("monaco-editor")) => void;
}) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const edRef = useRef<any>(null);

  // Typewriter reveal: catch a `displayed` buffer up to the streamed `content`
  // at a capped rate, so the boss WATCHES the code type in even when the model
  // (esp. gpt-5.x reasoning) dumps its tool-call args in a few big bursts
  // instead of trickling them. The effect re-runs as `displayed` advances and
  // stops once it has caught up to `content`.
  const [displayed, setDisplayed] = useState("");
  useEffect(() => {
    if (displayed === content) return;
    // New stream, or a buffer that diverged (shouldn't happen mid-file) → snap
    // rather than "un-type" what's already shown.
    if (content.length < displayed.length || !content.startsWith(displayed)) {
      setDisplayed(content);
      return;
    }
    const raf = requestAnimationFrame(() => {
      setDisplayed((cur) => {
        if (cur.length >= content.length) return cur;
        // Reveal proportional to the gap (min 4 chars/frame): small streams
        // keep pace, big bursts catch up fast but still visibly flow.
        const step = Math.max(4, Math.ceil((content.length - cur.length) / 20));
        return content.slice(0, cur.length + step);
      });
    });
    return () => cancelAnimationFrame(raf);
  }, [content, displayed]);

  // Auto-scroll to the last line as the reveal advances so the newest tokens
  // stay in view, the way a terminal follows output.
  useEffect(() => {
    const ed = edRef.current;
    if (!ed || typeof ed.getModel !== "function") return;
    try {
      const model = ed.getModel();
      if (model && typeof ed.revealLine === "function") {
        ed.revealLine(model.getLineCount());
      }
    } catch {
      /* editor not ready */
    }
  }, [displayed]);
  return (
    <MonacoEditor
      height="100%"
      language={language}
      theme={theme}
      value={displayed}
      beforeMount={beforeMount}
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      onMount={(editor: any) => {
        edRef.current = editor;
      }}
      options={{
        readOnly: true,
        domReadOnly: true,
        fontSize: 13,
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
        smoothScrolling: true,
        wordWrap: "on",
        automaticLayout: true,
        renderValidationDecorations: "off",
      }}
    />
  );
}

// languageFromPath guesses the Monaco language id from a file extension. Used
// for the live stream view before the disk read (which carries the server's
// detected language) has run. Falls back to plaintext.
function languageFromPath(path: string): string {
  const ext = path.slice(path.lastIndexOf(".") + 1).toLowerCase();
  const map: Record<string, string> = {
    ts: "typescript", tsx: "typescript", js: "javascript", jsx: "javascript",
    mjs: "javascript", cjs: "javascript", go: "go", py: "python", rb: "ruby",
    rs: "rust", java: "java", kt: "kotlin", swift: "swift", c: "c", h: "c",
    cc: "cpp", cpp: "cpp", hpp: "cpp", cs: "csharp", php: "php", sh: "shell",
    bash: "shell", zsh: "shell", sql: "sql", json: "json", jsonc: "json",
    yaml: "yaml", yml: "yaml", toml: "ini", ini: "ini", md: "markdown",
    mdx: "markdown", html: "html", htm: "html", css: "css", scss: "scss",
    less: "less", xml: "xml", svg: "xml", dockerfile: "dockerfile",
  };
  return map[ext] ?? "plaintext";
}

function MonacoSkeleton() {
  return (
    <div className="flex h-full items-center justify-center">
      <Loader2 className="size-5 animate-spin text-muted-foreground" />
    </div>
  );
}

// Pick the Monaco theme that matches the active Studio theme. Studio
// toggles `.dark` on documentElement; we observe it via MutationObserver
// so the editor flips when the boss flips the global theme.
function useEditorTheme() {
  const [isDark, setIsDark] = useState(true);
  useEffect(() => {
    if (typeof document === "undefined") return;
    const root = document.documentElement;
    setIsDark(root.classList.contains("dark"));
    const mo = new MutationObserver(() => {
      setIsDark(root.classList.contains("dark"));
    });
    mo.observe(root, { attributes: true, attributeFilter: ["class"] });
    return () => mo.disconnect();
  }, []);
  return isDark ? INFINITY_DARK : INFINITY_LIGHT;
}
