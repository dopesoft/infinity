"use client";

import { useEffect, useRef, useState } from "react";
import { Code2, Send } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

const MAX_LINES_PX = 112; // ~4 lines (text-base 16px × line-height 1.5 × 4 + py-2)
const CODE_MODE_KEY = "infinity:composer:codeMode";

export function Composer({
  onSend,
  onSlash,
  disabled,
}: {
  onSend: (text: string) => void;
  onSlash?: (cmd: "new" | "clear") => void;
  disabled?: boolean;
}) {
  const [value, setValue] = useState("");
  const [codeMode, setCodeMode] = useState(false);
  const ref = useRef<HTMLTextAreaElement>(null);

  // Hydrate the code-mode toggle from localStorage after mount so SSR matches.
  useEffect(() => {
    if (typeof window === "undefined") return;
    try {
      setCodeMode(window.localStorage.getItem(CODE_MODE_KEY) === "1");
    } catch {
      /* ignore */
    }
  }, []);

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
