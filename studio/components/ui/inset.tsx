"use client";

import * as React from "react";
import { cn } from "@/lib/utils";
import { diffLineClass } from "@/lib/diff";

/**
 * Inset — the ONLY container allowed inside a row (Majordomo §2, §5).
 *
 * A tinted, borderless `bg-muted` block at radius 10. It is what an opened
 * row, a terminal transcript, a diff, a quote, a key/value dump, or a schema
 * looks like. It is never bordered and never nested inside another Inset:
 * one level of tone, always (§1.2).
 *
 * The primitive owns the discipline that consumers kept dropping:
 *  - `min-w-0 max-w-full overflow-hidden` plus `break-words`/`break-all` so a
 *    1,200-character JSON blob or an unbroken URL can never widen the page.
 *  - A capped height with its own `overflow-y-auto scroll-touch` scroller, so
 *    a long output scrolls inside the row instead of burying the page.
 *  - 12px mono for data, the voice face for quotes. Never a bare `<pre>` in a
 *    consumer — that is the smell this replaces.
 *
 * Variants:
 *  - `plain`    arbitrary children, or `text` rendered as wrapped prose/JSON.
 *  - `terminal` `$ command` lines above trimmed output, with "Show all" once
 *               the output exceeds `maxLines` (default 12).
 *  - `diff`     unified diff, tinted per line by the shared `lib/diff.ts`
 *               helper — the exact tinting `ToolCallCard`'s DiffPre uses.
 *  - `quote`    Jarvis's own words: voice face, curly quotes added.
 *  - `kv`       a `<dl>` grid; label column is mono/quiet, value wraps.
 *  - `schema`   a mono table of fields (name · type · note).
 *
 * Everything except `terminal` is deterministic markup; `terminal` owns the
 * one piece of state (expanded) so the consumer never has to.
 */

export type InsetVariant = "plain" | "terminal" | "diff" | "quote" | "kv" | "schema";

export interface InsetKV {
  label: string;
  value: React.ReactNode;
}

export interface InsetField {
  name: string;
  type?: string;
  note?: string;
  required?: boolean;
}

export interface InsetProps {
  variant?: InsetVariant;
  /** Body text for `plain`, `terminal` (output), `diff`, and `quote`. */
  text?: string;
  /** `terminal` only: the command(s) shown as `$ …` above the output. */
  command?: string | string[];
  /** `kv` only. */
  items?: InsetKV[];
  /** `schema` only. */
  fields?: InsetField[];
  /** `terminal` only: lines shown before "Show all" appears. Default 12. */
  maxLines?: number;
  /** `plain` only: render children instead of `text`. */
  children?: React.ReactNode;
  className?: string;
}

/** Shared shell: tinted ground, radius 10, overflow discipline. Never bordered. */
const SHELL = "min-w-0 max-w-full overflow-hidden rounded-[10px] bg-muted";

export function Inset({
  variant = "plain",
  text,
  command,
  items,
  fields,
  maxLines = 12,
  children,
  className,
}: InsetProps) {
  if (variant === "terminal") {
    return (
      <TerminalInset
        command={command}
        text={text ?? ""}
        maxLines={maxLines}
        className={className}
      />
    );
  }

  if (variant === "diff") {
    const lines = (text ?? "").split("\n");
    return (
      <div className={cn(SHELL, className)}>
        <pre className="max-h-72 overflow-y-auto overflow-x-hidden p-2 font-mono text-[12px] leading-snug scroll-touch">
          {lines.map((line, i) => (
            <div
              key={i}
              className={cn("whitespace-pre-wrap break-all px-1", diffLineClass(line))}
            >
              {line || " "}
            </div>
          ))}
        </pre>
      </div>
    );
  }

  if (variant === "quote") {
    return (
      <blockquote
        className={cn(
          SHELL,
          "px-3 py-2.5 font-voice text-[15.5px] leading-[1.55] text-foreground [overflow-wrap:anywhere]",
          className,
        )}
      >
        {text ? `“${text}”` : children}
      </blockquote>
    );
  }

  if (variant === "kv") {
    return (
      <dl
        className={cn(
          SHELL,
          "grid grid-cols-1 gap-x-3 gap-y-1.5 px-3 py-2.5 sm:grid-cols-[minmax(0,92px)_minmax(0,1fr)]",
          className,
        )}
      >
        {(items ?? []).map((kv) => (
          <React.Fragment key={kv.label}>
            <dt className="min-w-0 truncate font-mono text-[11px] uppercase tracking-[0.06em] text-quiet">
              {kv.label}
            </dt>
            <dd className="min-w-0 text-[13.5px] leading-relaxed text-foreground [overflow-wrap:anywhere]">
              {kv.value}
            </dd>
          </React.Fragment>
        ))}
      </dl>
    );
  }

  if (variant === "schema") {
    return (
      <div className={cn(SHELL, "px-3 py-2.5", className)}>
        <ul className="flex min-w-0 flex-col gap-1.5">
          {(fields ?? []).map((f) => (
            <li
              key={f.name}
              className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5 font-mono text-[12px] leading-snug"
            >
              <span className="min-w-0 truncate text-foreground">{f.name}</span>
              {f.type ? <span className="shrink-0 text-info">{f.type}</span> : null}
              {f.required ? (
                <span className="shrink-0 text-[10.5px] uppercase tracking-[0.08em] text-warning">
                  required
                </span>
              ) : null}
              {f.note ? (
                <span className="min-w-0 text-quiet [overflow-wrap:anywhere]">{f.note}</span>
              ) : null}
            </li>
          ))}
        </ul>
      </div>
    );
  }

  // plain
  return (
    <div className={cn(SHELL, className)}>
      {text !== undefined ? (
        <pre className="max-h-72 overflow-y-auto overflow-x-hidden whitespace-pre-wrap break-words px-3 py-2.5 font-mono text-[12px] leading-relaxed text-foreground scroll-touch [overflow-wrap:anywhere]">
          {text}
        </pre>
      ) : (
        <div className="min-w-0 max-w-full px-3 py-2.5 text-[13.5px] leading-relaxed text-foreground [overflow-wrap:anywhere]">
          {children}
        </div>
      )}
    </div>
  );
}

/**
 * Terminal transcript: the commands that ran, then their output trimmed to
 * `maxLines` with a "Show all" toggle. Separate component because it is the
 * one variant that holds state.
 */
function TerminalInset({
  command,
  text,
  maxLines,
  className,
}: {
  command?: string | string[];
  text: string;
  maxLines: number;
  className?: string;
}) {
  const [expanded, setExpanded] = React.useState(false);
  const commands = React.useMemo(
    () => (command === undefined ? [] : Array.isArray(command) ? command : [command]),
    [command],
  );
  const lines = React.useMemo(() => (text ? text.split("\n") : []), [text]);
  const overflowing = lines.length > maxLines;
  const shown = expanded || !overflowing ? lines : lines.slice(0, maxLines);

  return (
    <div className={cn(SHELL, "px-3 py-2.5", className)}>
      {commands.map((c, i) => (
        <div
          key={i}
          className="flex min-w-0 gap-2 font-mono text-[12px] leading-snug text-foreground"
        >
          <span className="shrink-0 select-none text-quiet" aria-hidden>
            $
          </span>
          <span className="min-w-0 whitespace-pre-wrap break-all">{c}</span>
        </div>
      ))}
      {shown.length ? (
        <pre
          className={cn(
            "mt-2 max-h-72 overflow-y-auto overflow-x-hidden whitespace-pre-wrap break-all font-mono text-[12px] leading-snug text-muted-foreground scroll-touch",
            commands.length === 0 && "mt-0",
          )}
        >
          {shown.join("\n")}
        </pre>
      ) : null}
      {overflowing ? (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="mt-2 inline-flex min-h-11 items-center font-sans text-[12.5px] font-medium text-quiet transition-colors hover:text-foreground"
        >
          {expanded ? "Show less" : `Show all ${lines.length} lines`}
        </button>
      ) : null}
    </div>
  );
}
