"use client";

import dynamic from "next/dynamic";
import { useCallback, useEffect, useMemo, useState } from "react";
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
import { fetchCanvasFSRead, saveCanvasFile } from "@/lib/canvas/api";
import { fetchTrustContracts, type TrustContractDTO } from "@/lib/api";
import { INFINITY_DARK, INFINITY_LIGHT, registerInfinityThemes } from "@/lib/canvas/monaco-theme";
import { cn } from "@/lib/utils";

/**
 * CanvasFileTab — Monaco-backed view of a single file.
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

// Dynamic imports keep Monaco out of the initial chunk — it's ~600KB and
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
  const isDirtyOnLoad = store.dirtyPaths.has(path);
  const [mode, setMode] = useState<Mode>(isDirtyOnLoad ? "diff" : "edit");
  const [originalContent, setOriginalContent] = useState<string | null>(null);
  const [currentContent, setCurrentContent] = useState<string | null>(null);
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
  const [reloadKey, setReloadKey] = useState(0);
  useEffect(() => {
    if (!isActive && originalContent !== null) return; // Don't fetch background tabs.
    let cancelled = false;
    setLoading(true);
    setLoadError(null);
    fetchCanvasFSRead(path).then((r) => {
      if (cancelled) return;
      setLoading(false);
      if (!r) {
        setLoadError("Could not read file (mac bridge unreachable?)");
        return;
      }
      setOriginalContent(r.content);
      setCurrentContent(r.content);
      setBaseSha(r.sha);
      setLanguage(r.language || "plaintext");
    });
    return () => {
      cancelled = true;
    };
    // Reload on path change or explicit reload trigger.
  }, [path, reloadKey, isActive, originalContent]);

  const isModified = useMemo(() => {
    if (originalContent === null || currentContent === null) return false;
    return currentContent !== originalContent;
  }, [originalContent, currentContent]);

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
      setSaveState({ status: "error", error: res.reason ?? "File changed on disk — reload to see it." });
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
        // After approval, the backend will have written the file. Refresh
        // the buffer's "original" so subsequent edits diff against the
        // newly-saved version, not the pre-save version.
        setOriginalContent(currentContent);
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
  }, [saveState.status, saveState.contractId, currentContent]);

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
        {loading && (
          <div className="absolute inset-0 flex items-center justify-center">
            <Loader2 className="size-5 animate-spin text-muted-foreground" />
          </div>
        )}
        {loadError && !loading && (
          <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-sm">
            <AlertCircle className="size-6 text-danger" />
            <p className="text-muted-foreground">{loadError}</p>
            <Button variant="ghost" size="sm" onClick={() => setReloadKey((k) => k + 1)}>
              Retry
            </Button>
          </div>
        )}
        {!loading && !loadError && originalContent !== null && currentContent !== null && (
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
        <span className="font-mono uppercase">{language}</span>
        <span className="text-muted-foreground/40">·</span>
        {saveState.status === "idle" && isModified && <span className="text-warning">Modified</span>}
        {saveState.status === "idle" && !isModified && <span>Clean</span>}
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
        <span className="ml-auto flex items-center gap-1.5">
          {mode === "edit" && (
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
