"use client";

import * as React from "react";
import DOMPurify from "dompurify";
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

/** Rendered HTML email body.
 *
 *  Renders untrusted email HTML the way a real mail client does: on a clean
 *  white card, inside a SANDBOXED iframe. Two layers of safety:
 *    1. `sandbox="allow-same-origin allow-popups"` - NO `allow-scripts`, so
 *       no JavaScript in the email can ever execute (the hard boundary).
 *    2. DOMPurify sanitization before injection - strips <script>, on*
 *       handlers and javascript: URLs as defense-in-depth + junk removal.
 *  Email CSS is naturally scoped to the iframe, so its styles can never
 *  bleed into the app.
 *
 *  The frame auto-sizes to its content (single scroll - the modal body
 *  scrolls, never a nested scrollbar) and links open in a new tab. */
export function ModalHtml({ html, className }: { html: string; className?: string }) {
  const frameRef = React.useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = React.useState(160);

  // Build the sandboxed document once per html change. Guarded for SSR -
  // DOMPurify needs a DOM, so it only runs client-side (this file is a
  // client component; srcDoc stays "" during the server pass).
  const srcDoc = React.useMemo(() => {
    if (typeof window === "undefined") return "";
    const clean = DOMPurify.sanitize(html ?? "", {
      ADD_ATTR: ["target"],
      WHOLE_DOCUMENT: false,
    });
    return [
      "<!doctype html><html><head><meta charset='utf-8'>",
      "<meta name='viewport' content='width=device-width, initial-scale=1'>",
      "<base target='_blank'>",
      "<style>",
      "html,body{margin:0;padding:16px;background:#ffffff;color:#1a1a1a;",
      "font-family:ui-sans-serif,system-ui,-apple-system,'Segoe UI',Roboto,sans-serif;",
      "font-size:14px;line-height:1.55;-webkit-text-size-adjust:100%;word-break:break-word;overflow-wrap:anywhere;}",
      "img{max-width:100%!important;height:auto;}",
      "table{max-width:100%;}",
      "a{color:#1a56db;}",
      "blockquote{margin:0 0 0 12px;padding-left:12px;border-left:3px solid #e2e2e2;color:#555;}",
      "</style></head><body>",
      clean,
      "</body></html>",
    ].join("");
  }, [html]);

  // Auto-size: measure on load, then keep watching the body so late image
  // loads / reflows grow the frame. allow-same-origin (without allow-scripts)
  // is what lets us read contentDocument here while keeping JS disabled.
  React.useEffect(() => {
    const frame = frameRef.current;
    if (!frame) return;
    let ro: ResizeObserver | undefined;
    const measure = () => {
      const doc = frame.contentDocument;
      if (!doc) return;
      const h = Math.max(
        doc.documentElement?.scrollHeight ?? 0,
        doc.body?.scrollHeight ?? 0,
      );
      if (h > 0) setHeight(h + 4);
    };
    const onLoad = () => {
      measure();
      const doc = frame.contentDocument;
      if (doc?.body && typeof ResizeObserver !== "undefined") {
        ro = new ResizeObserver(measure);
        ro.observe(doc.body);
      }
    };
    frame.addEventListener("load", onLoad);
    return () => {
      frame.removeEventListener("load", onLoad);
      ro?.disconnect();
    };
  }, [srcDoc]);

  return (
    <div
      className={cn(
        "min-w-0 max-w-full overflow-hidden rounded-lg border border-border bg-white",
        className,
      )}
    >
      <iframe
        ref={frameRef}
        srcDoc={srcDoc}
        title="Email message"
        sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox"
        className="block w-full border-0 bg-white"
        style={{ height }}
      />
    </div>
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
