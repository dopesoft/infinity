"use client";

import * as React from "react";
import { AlertCircle, Eye } from "lucide-react";
import { cn } from "@/lib/utils";

export type ModalSectionTone = "default" | "error" | "warning" | "success";

/* Modal body content primitives - the standardized building blocks every
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

/** Labeled context block - the "card-within-a-modal" surface used for
 *  body content, JSON payloads, diffs, drafts, etc. Replaces the
 *  inlined ContextBlock that lived in ObjectViewer.
 *
 *  Props:
 *    label   - eyebrow text (default "Context"). Pass "Error" / "Schedule"
 *              / "Output" / "Steps" so cards self-describe.
 *    tone    - color hint. default | error | warning | success. Tints
 *              the border + header background so errors visually
 *              stand out without the consumer hand-rolling color
 *              classes. Pairs with a matching icon swap (alert icon
 *              for error/warning, eye for everything else).
 *    icon    - override the leading icon (rare). Defaults follow tone.
 *    meta    - right-aligned eyebrow (timestamp, count, etc.).
 */
export function ModalSection({
  label = "Context",
  tone = "default",
  icon,
  meta,
  children,
  className,
}: {
  label?: string;
  tone?: ModalSectionTone;
  icon?: React.ReactNode;
  meta?: React.ReactNode;
  className?: string;
  children: React.ReactNode;
}) {
  const toneClasses = (() => {
    switch (tone) {
      case "error":
        return {
          frame: "border-danger/40 bg-danger/5",
          header: "border-danger/30 bg-danger/10",
          label: "text-danger",
        };
      case "warning":
        return {
          frame: "border-warning/40 bg-warning/5",
          header: "border-warning/30 bg-warning/10",
          label: "text-warning",
        };
      case "success":
        return {
          frame: "border-success/40 bg-success/5",
          header: "border-success/30 bg-success/10",
          label: "text-success",
        };
      default:
        return {
          frame: "bg-muted/30",
          header: "bg-muted/40",
          label: "text-muted-foreground",
        };
    }
  })();
  const defaultIcon =
    tone === "error" || tone === "warning" ? (
      <AlertCircle className={cn("size-3.5 shrink-0", toneClasses.label)} aria-hidden />
    ) : (
      <Eye className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
    );
  return (
    <div
      className={cn(
        "mt-4 min-w-0 max-w-full overflow-hidden rounded-lg border",
        toneClasses.frame,
        className,
      )}
    >
      <header
        className={cn("flex min-w-0 items-center gap-2 border-b px-3 py-2", toneClasses.header)}
      >
        {icon ?? defaultIcon}
        <span
          className={cn(
            "font-mono text-[10px] uppercase tracking-[0.16em]",
            toneClasses.label,
          )}
        >
          {label}
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

/** Code / diff block - preserves whitespace and line integrity; scrolls
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

/** Bare URL link - always wraps unbroken strings and pins the icon so the
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

/** Key/value metadata grid - replaces hand-rolled `<dl class="grid">`
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

/** Labeled inline row - the Untitled UI "From: <value>" pattern. Stacks
 *  vertically on mobile (label on its own line above the value), shifts
 *  to a two-column grid on sm+ (label left in a fixed column, value
 *  right, wraps freely). Use these to render parsed key/value metadata
 *  inside a modal body instead of crammed `<dl>` rows or pipe-separated
 *  subtitles.
 *
 *  Compose multiple in a column wrapped in `<div className="divide-y divide-border">`
 *  for hairline dividers between rows. The component itself does NOT
 *  draw its own divider so the wrapper can control density (some bodies
 *  want zero dividers + larger gaps).
 *
 *  Props:
 *    label    - small uppercase tracking-wide muted label (e.g. "FROM").
 *    children - the value content. Anything — plain text, a link node,
 *               nested chips. Wraps freely; never overflows the modal. */
export function ModalField({
  label,
  children,
  className,
}: {
  label: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        "grid min-w-0 grid-cols-1 gap-1 py-3 first:pt-0 last:pb-0 sm:grid-cols-[7.5rem_minmax(0,1fr)] sm:gap-4 sm:py-3.5",
        className,
      )}
    >
      <div className="min-w-0 font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground sm:pt-px">
        {label}
      </div>
      <div className="min-w-0 break-words text-[13px] leading-relaxed text-foreground/90">
        {children}
      </div>
    </div>
  );
}

/** Horizontal chip row - the standardized "eyebrow with badges" line that
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
