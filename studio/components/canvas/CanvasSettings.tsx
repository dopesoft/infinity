"use client";

import { useEffect, useState } from "react";
import { Check, Save } from "lucide-react";
import { SettingsPanel } from "@/components/settings/SettingsPanel";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { SettingRow } from "@/components/ui/setting-row";
import { Switch } from "@/components/ui/switch";

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
  const [savedKey, setSavedKey] = useState<string | null>(null);

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
      setSavedKey(key);
    } catch {
      /* ignore */
    }
  }

  function persistAutoOpen(next: boolean) {
    setAutoOpen(next);
    if (typeof window === "undefined") return;
    try {
      window.localStorage.setItem(AUTO_OPEN_KEY, next ? "1" : "0");
      setSavedKey(AUTO_OPEN_KEY);
    } catch {
      /* ignore */
    }
  }

  return (
    // Every row is a SettingRow and every toggle is the Switch primitive, the
    // same as every other Settings section. This was the last one still using
    // a private `Field` with its own icons and a hand-rolled switch, which is
    // why Workbench looked unlike its neighbours.
    <SettingsPanel>
      <SettingRow
        label="Workspace root"
        description="The folder on your Mac he works in. He cannot read or write outside it."
        htmlFor="canvas-root"
      >
        <div className="flex min-w-0 gap-2">
          <Input
            id="canvas-root"
            value={root}
            onChange={(e) => setRoot(e.target.value)}
            placeholder="/Users/you/Dev/infinity"
            inputMode="text"
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            className="font-mono text-sm"
          />
          <SaveButton onClick={() => persist(ROOT_KEY, root.trim())} saved={savedKey === ROOT_KEY} />
        </div>
      </SettingRow>

      <SettingRow
        label="Preview address"
        description="Where your app is running, so you can watch it while he works. Leave blank to use the default."
        htmlFor="canvas-preview"
      >
        <div className="flex min-w-0 gap-2">
          <Input
            id="canvas-preview"
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
          <SaveButton
            onClick={() => persist(PREVIEW_KEY, previewUrl.trim())}
            saved={savedKey === PREVIEW_KEY}
          />
        </div>
      </SettingRow>

      <SettingRow
        label="Open the workbench on its own"
        description="Jump straight there the moment he starts editing, instead of showing a banner."
        control={<Switch checked={autoOpen} onCheckedChange={persistAutoOpen} aria-label="Open the workbench on its own" />}
      />
    </SettingsPanel>
  );
}

/** Save plus the confirmation it produces, in one place so the two cannot drift. */
function SaveButton({ onClick, saved }: { onClick: () => void; saved: boolean }) {
  return (
    <Button size="sm" onClick={onClick} className="shrink-0 gap-1">
      {saved ? <Check className="size-3.5" aria-hidden /> : <Save className="size-3.5" aria-hidden />}
      {saved ? "Saved" : "Save"}
    </Button>
  );
}
