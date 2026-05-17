"use client";

import { useEffect, useRef, useState } from "react";
import { Code2, Send } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

const MAX_LINES_PX = 112; // ~4 lines (text-base 16px × line-height 1.5 × 4 + py-2)
const CODE_MODE_KEY = "infinity:composer:codeMode";
// Single global draft slot — what the boss typed survives navigating away,
// switching apps on iOS Safari, or any remount of the Live page. Restored
// on mount and cleared on send. Single-user product, so one slot is right.
const DRAFT_KEY = "infinity:composer:draft";

function readStoredDraft(): string {
  if (typeof window === "undefined") return "";
  try {
    return window.localStorage.getItem(DRAFT_KEY) ?? "";
  } catch {
    return "";
  }
}

function writeStoredDraft(value: string) {
  if (typeof window === "undefined") return;
  try {
    if (value) window.localStorage.setItem(DRAFT_KEY, value);
    else window.localStorage.removeItem(DRAFT_KEY);
  } catch {
    /* private mode / quota — best-effort only */
  }
}

export function Composer({
  onSend,
  onSlash,
  disabled,
}: {
  onSend: (text: string) => void;
  onSlash?: (cmd: "new" | "clear") => void;
  disabled?: boolean;
}) {
  // Empty on first server render; hydrated from localStorage in the
  // mount effect below so SSR markup stays deterministic.
  const [value, setValue] = useState("");
  const [codeMode, setCodeMode] = useState(false);
  const ref = useRef<HTMLTextAreaElement>(null);

  // Hydrate the code-mode toggle AND the in-progress draft from
  // localStorage after mount so SSR matches and a remount (navigation,
  // tab switch, iOS Safari backgrounding+restore) restores what the
  // boss was typing.
  useEffect(() => {
    if (typeof window === "undefined") return;
    try {
      setCodeMode(window.localStorage.getItem(CODE_MODE_KEY) === "1");
    } catch {
      /* ignore */
    }
    const stored = readStoredDraft();
    if (stored) setValue(stored);
  }, []);

  // Mirror every keystroke into localStorage. Cheap (single key, small
  // string) and the iOS Safari kill-tab path doesn't fire beforeunload
  // reliably, so we can't batch on unload. Best to keep storage in sync
  // with every change.
  useEffect(() => {
    writeStoredDraft(value);
  }, [value]);

  function persistCodeMode(next: boolean) {
    setCodeMode(next);
    if (typeof window === "undefined") return;
    try {
      window.localStorage.setItem(CODE_MODE_KEY, next ? "1" : "0");
    } catch {
      /* ignore */
    }
  }

  // Auto-resize textarea (Safari < 17.4 lacks field-sizing: content)
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, MAX_LINES_PX)}px`;
  }, [value]);

  function submit() {
    const t = value.trim();
    if (!t) return;

    if (t.toLowerCase() === "/new") {
      setValue("");
      onSlash?.("new");
      return;
    }
    if (t.toLowerCase() === "/clear") {
      setValue("");
      onSlash?.("clear");
      return;
    }

    onSend(t);
    setValue("");
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
      className="sticky bottom-0 z-10 border-t bg-background/95 px-3 pt-2 backdrop-blur keyboard-safe-bottom supports-[backdrop-filter]:bg-background/80 sm:px-4"
    >
      <div className={cn("flex items-end gap-2", disabled && "opacity-60")}>
        <Button
          type="button"
          size="icon"
          variant={codeMode ? "default" : "ghost"}
          onClick={() => persistCodeMode(!codeMode)}
          aria-label={codeMode ? "Disable code mode" : "Enable code mode"}
          aria-pressed={codeMode}
          title="Code mode disables iOS autocorrect, autocapitalize and spellcheck"
          className="h-11 w-11 shrink-0"
        >
          <Code2 className="size-5" />
        </Button>
        <Textarea
          ref={ref}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder={codeMode ? "code mode: paths, commands, code..." : "ask me anything..."}
          rows={1}
          inputMode="text"
          autoCapitalize={codeMode ? "none" : "sentences"}
          autoCorrect={codeMode ? "off" : "on"}
          autoComplete={codeMode ? "off" : undefined}
          spellCheck={!codeMode}
          aria-label="Compose message"
          className={cn(
            "min-h-11 flex-1 py-2.5",
            codeMode && "font-mono",
          )}
        />
        <Button
          type="submit"
          size="icon"
          disabled={!value.trim() || disabled}
          aria-label="Send"
          className="h-11 w-11 shrink-0"
        >
          <Send className="size-5" />
        </Button>
      </div>
    </form>
  );
}
