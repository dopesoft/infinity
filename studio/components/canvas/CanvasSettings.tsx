"use client";

import { useEffect, useState } from "react";
import { Check, FolderOpen, LayoutPanelLeft, MonitorPlay, Save, Zap } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * CanvasSettings - workspace root, preview URL override, auto-open toggle.
 *
 * Note: this is a Settings-page card, not part of the Canvas surface itself.
 * Everything edits localStorage keys the CanvasStoreProvider reads on mount,
 * so changes take effect the next time the boss opens Canvas (or refreshes
 * if Canvas is already open).
 */

const ROOT_KEY = "infinity:canvas:root";
const PREVIEW_KEY = "infinity:canvas:previewUrl";
const AUTO_OPEN_KEY = "infinity:canvas:autoOpen";

export function CanvasSettings() {
  const [root, setRoot] = useState("");
  const [previewUrl, setPreviewUrl] = useState("");
  const [autoOpen, setAutoOpen] = useState(false);
  const [savedAt, setSavedAt] = useState<string | null>(null);

  useEffect(() => {
    if (typeof window === "undefined") return;
    setRoot(window.localStorage.getItem(ROOT_KEY) ?? "");
    setPreviewUrl(window.localStorage.getItem(PREVIEW_KEY) ?? "");
    setAutoOpen(window.localStorage.getItem(AUTO_OPEN_KEY) === "1");
  }, []);

  function persist(key: string, value: string) {
    if (typeof window === "undefined") return;
    try {
      if (value) window.localStorage.setItem(key, value);
      else window.localStorage.removeItem(key);
      setSavedAt(new Date().toLocaleTimeString());
    } catch {
      /* ignore */
    }
  }

  function persistAutoOpen(next: boolean) {
    setAutoOpen(next);
    if (typeof window === "undefined") return;
    try {
      window.localStorage.setItem(AUTO_OPEN_KEY, next ? "1" : "0");
      setSavedAt(new Date().toLocaleTimeString());
    } catch {
      /* ignore */
    }
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <LayoutPanelLeft className="size-4 text-muted-foreground" aria-hidden />
            <CardTitle>Canvas</CardTitle>
          </div>
          {savedAt && (
            <span className="flex items-center gap-1 text-[10px] text-success">
              <Check className="size-3" /> saved {savedAt}
            </span>
          )}
        </div>
        <CardDescription>
          The IDE-style surface where you watch the agent code: file tree, git, live preview, Monaco editor tabs.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        <Field
          icon={<FolderOpen className="size-4 text-muted-foreground" />}
          label="Workspace root"
          hint="Absolute path on your Mac. Canvas restricts every read and write to this directory."
        >
          <div className="flex gap-2">
            <Input
              value={root}
              onChange={(e) => setRoot(e.target.value)}
              placeholder="/Users/you/Dev/infinity"
              inputMode="text"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              className="font-mono text-sm"
            />
            <Button size="sm" onClick={() => persist(ROOT_KEY, root.trim())} className="gap-1">
              <Save className="size-3.5" /> Save
            </Button>
          </div>
        </Field>

        <Field
          icon={<MonitorPlay className="size-4 text-muted-foreground" />}
          label="Preview URL"
          hint="HTTP(S) URL of your dev server. Lovable-style iframe lives here. Leave blank to use the NEXT_PUBLIC_PREVIEW_URL env."
        >
          <div className="flex gap-2">
            <Input
              value={previewUrl}
              onChange={(e) => setPreviewUrl(e.target.value)}
              placeholder="https://preview.dopesoft.io"
              inputMode="url"
              type="url"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              className="font-mono text-sm"
            />
            <Button size="sm" onClick={() => persist(PREVIEW_KEY, previewUrl.trim())} className="gap-1">
              <Save className="size-3.5" /> Save
            </Button>
          </div>
        </Field>

        <Field
          icon={<Zap className="size-4 text-muted-foreground" />}
          label="Auto-open Canvas"
          hint="When the agent invokes its first edit/write tool in Live, jump to Canvas automatically instead of showing a banner."
        >
          <button
            type="button"
            role="switch"
            aria-checked={autoOpen}
            onClick={() => persistAutoOpen(!autoOpen)}
            className={cn(
              "relative inline-flex h-7 w-12 shrink-0 items-center rounded-full border transition-colors",
              autoOpen ? "border-foreground bg-foreground" : "border-border bg-muted",
            )}
          >
            <span
              className={cn(
                "inline-block size-5 rounded-full bg-background shadow transition-transform",
                autoOpen ? "translate-x-6" : "translate-x-1",
              )}
            />
          </button>
        </Field>
      </CardContent>
    </Card>
  );
}

function Field({
  icon,
  label,
  hint,
  children,
}: {
  icon: React.ReactNode;
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-2">
        {icon}
        <label className="text-sm font-medium">{label}</label>
      </div>
      {hint && <p className="text-xs leading-relaxed text-muted-foreground">{hint}</p>}
      <div className="pt-1">{children}</div>
    </div>
  );
}
