"use client";

import * as React from "react";
import { Eye } from "lucide-react";
import { cn } from "@/lib/utils";

/* Modal body content primitives — the standardized building blocks every
 * <ResponsiveModal> body should compose from. They exist because freehand
 * <pre>/<dl>/<a> blocks inside modal bodies kept regressing the mobile
 * overflow chain (a missing `min-w-0`, a missing `break-all`, a long URL
 * pushing the whole page wider than the viewport). Using these
 * components makes the disciplined version the path of least resistance.
 *
 * RULE: inside a <ResponsiveModalBody> never reach for a bare <pre>,
 * <code>, <a href={url}>, or <dl> for tabular metadata. Use these. If
 * the primitive doesn't fit, extend it here so the rest of the app gets
 * the same baseline. */

/** Labeled context block — the "card-within-a-modal" surface used for
 *  body content, JSON payloads, diffs, drafts, etc. Replaces the
 *  inlined ContextBlock that lived in ObjectViewer. */
export function ModalSection({
  meta,
  children,
  className,
}: {
  /** Right-aligned label/eyebrow in the header. Strings get truncated. */
  meta?: React.ReactNode;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        "mt-4 min-w-0 max-w-full overflow-hidden rounded-lg border bg-muted/30",
        className,
      )}
    >
      <header className="flex min-w-0 items-center gap-2 border-b bg-muted/40 px-3 py-2">
        <Eye className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
          Context
        </span>
        {meta ? (
          <span className="ml-auto min-w-0 truncate text-[11px] text-muted-foreground">
            {meta}
          </span>
        ) : null}
      </header>
      <div className="min-w-0 max-w-full p-3 text-[13px] leading-relaxed">{children}</div>
    </div>
  );
}

/** Prose / JSON / wrapping preformatted text. Always wraps, breaks long
 *  unbroken strings (URLs, tokens, IDs) so nothing escapes the modal. */
export function ModalPre({
  children,
  className,
  /** Use `serif: false` for monospace JSON-style. Default is serif/sans
   *  for email-style body prose (matches FollowUp body block). */
  mono = false,
}: {
  children: React.ReactNode;
  className?: string;
  mono?: boolean;
}) {
  return (
    <pre
      className={cn(
        "min-w-0 max-w-full whitespace-pre-wrap break-words leading-relaxed text-foreground/90",
        mono ? "font-mono text-[12px]" : "font-sans text-[13px]",
        className,
      )}
    >
      {children}
    </pre>
  );
}

/** Code / diff block — preserves whitespace and line integrity; scrolls
 *  internally instead of escaping the modal. Use ModalPre for prose. */
export function ModalCode({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <pre
      className={cn(
        "min-w-0 max-w-full overflow-x-auto whitespace-pre font-mono text-[11px] leading-relaxed",
        className,
      )}
    >
      {children}
    </pre>
  );
}

/** Bare URL link — always wraps unbroken strings and pins the icon so the
 *  link never escapes the modal frame on mobile. */
export function ModalUrl({
  href,
  children,
  icon,
  className,
  external = true,
}: {
  href: string;
  children?: React.ReactNode;
  /** Optional leading icon node. Should be a Lucide icon at `size-3.5`. */
  icon?: React.ReactNode;
  className?: string;
  external?: boolean;
}) {
  return (
    <a
      href={href}
      target={external ? "_blank" : undefined}
      rel={external ? "noreferrer" : undefined}
      className={cn(
        "inline-flex max-w-full items-center gap-1 break-all text-[12px] text-info hover:underline",
        className,
      )}
    >
      {icon ? <span className="shrink-0">{icon}</span> : null}
      <span className="min-w-0 break-all">{children ?? href}</span>
    </a>
  );
}

/** Key/value metadata grid — replaces hand-rolled `<dl class="grid">`
 *  blocks. Each row is `key (mono, truncates) · value (breaks)`. */
export function ModalDl({
  entries,
  className,
}: {
  entries: { k: string; v: React.ReactNode }[];
  className?: string;
}) {
  if (entries.length === 0) return null;
  return (
    <dl
      className={cn(
        "grid min-w-0 grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-3 gap-y-1 text-[12px]",
        className,
      )}
    >
      {entries.map((e) => (
        <React.Fragment key={e.k}>
          <dt className="min-w-0 truncate font-mono text-muted-foreground">{e.k}</dt>
          <dd className="min-w-0 break-all text-foreground/85">{e.v}</dd>
        </React.Fragment>
      ))}
    </dl>
  );
}

/** Horizontal chip row — the standardized "eyebrow with badges" line that
 *  sits above the body content (kind, time, risk, etc.). Wraps on mobile. */
export function ModalChips({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-wrap items-center gap-2 text-[11px] text-muted-foreground",
        className,
      )}
    >
      {children}
    </div>
  );
}
