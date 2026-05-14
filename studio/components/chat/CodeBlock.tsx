"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { cn } from "@/lib/utils";

/* CodeBlock — fenced-code renderer for chat markdown.
 *
 * Renders a header strip (language tag + copy button) above a horizontally
 * scrollable monospace pane. Long lines scroll inside the block instead
 * of forcing the parent bubble to grow — important for mobile, where the
 * conversation column is narrow.
 */
export function CodeBlock({
  language,
  children,
}: {
  language?: string;
  children: string;
}) {
  const [copied, setCopied] = useState(false);

  function doCopy() {
    if (!children) return;
    void navigator.clipboard.writeText(children).then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1200);
      },
      () => undefined,
    );
  }

  return (
    <div className="my-2 overflow-hidden rounded-lg border bg-background/70">
      <div className="flex items-center justify-between border-b bg-muted/40 px-2.5 py-1 text-[10px] uppercase tracking-wide text-muted-foreground">
        <span className="font-mono">{language || "text"}</span>
        <button
          type="button"
          onClick={doCopy}
          aria-label={copied ? "Copied" : "Copy code"}
          title={copied ? "Copied" : "Copy"}
          className="inline-flex size-6 items-center justify-center rounded transition-colors hover:bg-accent hover:text-foreground"
        >
          {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
        </button>
      </div>
      <pre
        className={cn(
          "overflow-x-auto px-3 py-2 text-[12.5px] leading-relaxed",
          "scroll-touch font-mono",
        )}
      >
        <code>{children}</code>
      </pre>
    </div>
  );
}
