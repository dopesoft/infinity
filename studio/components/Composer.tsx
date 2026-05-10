"use client";

import { useEffect, useRef, useState } from "react";
import { IconSend } from "@tabler/icons-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

const MAX_LINES_PX = 112; // ~4 lines (text-base 16px × line-height 1.5 × 4 + py-2)

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
  const ref = useRef<HTMLTextAreaElement>(null);

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
      className="sticky bottom-0 z-10 border-t px-3 pt-2 keyboard-safe-bottom sm:px-4"
    >
      <div className={cn("flex items-end gap-2", disabled && "opacity-60")}>
        <Textarea
          ref={ref}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder="ask me anything..."
          rows={1}
          inputMode="text"
          autoCapitalize="sentences"
          autoCorrect="on"
          spellCheck
          aria-label="Compose message"
          className="min-h-11 flex-1 py-2.5"
        />
        <Button
          type="submit"
          size="icon"
          disabled={!value.trim() || disabled}
          aria-label="Send"
          className="h-11 w-11 shrink-0"
        >
          <IconSend className="size-5" />
        </Button>
      </div>
    </form>
  );
}
