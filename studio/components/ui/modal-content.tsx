"use client";

import * as React from "react";
import DOMPurify from "dompurify";
import { AlertCircle, AlignLeft, Eye } from "lucide-react";
import { Markdown } from "@/components/chat/Markdown";
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

/** Before→after line diff. Mobile-first: lines WRAP (no horizontal scroll),
 *  a +/- gutter carries the meaning, and long runs of unchanged lines collapse
 *  to "… N unchanged …" so the boss sees WHAT CHANGED, not the whole file.
 *  Self-contained LCS — no dependency. When `before` is empty it renders as an
 *  all-added block (a brand-new skill). */
export function ModalDiff({
  before,
  after,
  className,
  context = 2,
}: {
  before: string;
  after: string;
  className?: string;
  /** unchanged lines kept around each change for orientation */
  context?: number;
}) {
  const { rows, added, removed } = React.useMemo(
    () => diffLines(before ?? "", after ?? "", context),
    [before, after, context],
  );
  return (
    <div className={cn("min-w-0 max-w-full", className)}>
      <div className="mb-1.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-[10px]">
        <span className="text-success">+{added} added</span>
        <span className="text-danger">−{removed} removed</span>
        {added === 0 && removed === 0 && (
          <span className="text-muted-foreground">no text change</span>
        )}
      </div>
      <div className="overflow-hidden rounded-md border font-mono text-[11px] leading-relaxed">
        {rows.map((r, i) =>
          r.kind === "gap" ? (
            <div
              key={i}
              className="select-none bg-muted/30 px-2 py-0.5 text-center text-[10px] text-muted-foreground"
            >
              ··· {r.count} unchanged ···
            </div>
          ) : (
            <div
              key={i}
              className={cn(
                "flex min-w-0 gap-1.5 px-2 py-0.5",
                r.kind === "add" && "bg-success/10",
                r.kind === "del" && "bg-danger/10",
              )}
            >
              <span
                className={cn(
                  "shrink-0 select-none",
                  r.kind === "add"
                    ? "text-success"
                    : r.kind === "del"
                      ? "text-danger"
                      : "text-muted-foreground/40",
                )}
                aria-hidden
              >
                {r.kind === "add" ? "+" : r.kind === "del" ? "−" : " "}
              </span>
              <span
                className={cn(
                  "min-w-0 whitespace-pre-wrap break-words",
                  r.kind === "add"
                    ? "text-success"
                    : r.kind === "del"
                      ? "text-danger"
                      : "text-foreground/70",
                )}
              >
                {r.text || " "}
              </span>
            </div>
          ),
        )}
      </div>
    </div>
  );
}

type DiffRow =
  | { kind: "add" | "del" | "ctx"; text: string }
  | { kind: "gap"; count: number };

/** Minimal LCS line diff → rows, collapsing unchanged runs longer than
 *  2*context+1 into a single "gap" marker. Bounded for safety on huge inputs. */
function diffLines(
  before: string,
  after: string,
  context: number,
): { rows: DiffRow[]; added: number; removed: number } {
  const a = before.length ? before.split("\n") : [];
  const b = after.split("\n");
  // Guardrail: LCS is O(n*m); cap to keep the modal snappy. Beyond the cap we
  // fall back to "all removed then all added" which is still readable.
  const CAP = 600;
  if (a.length > CAP || b.length > CAP) {
    const rows: DiffRow[] = [
      ...a.map((t): DiffRow => ({ kind: "del", text: t })),
      ...b.map((t): DiffRow => ({ kind: "add", text: t })),
    ];
    return { rows, added: b.length, removed: a.length };
  }
  const m = a.length;
  const n = b.length;
  const dp: number[][] = Array.from({ length: m + 1 }, () =>
    new Array(n + 1).fill(0),
  );
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const raw: DiffRow[] = [];
  let added = 0;
  let removed = 0;
  let i = 0;
  let j = 0;
  while (i < m && j < n) {
    if (a[i] === b[j]) {
      raw.push({ kind: "ctx", text: a[i] });
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      raw.push({ kind: "del", text: a[i] });
      removed++;
      i++;
    } else {
      raw.push({ kind: "add", text: b[j] });
      added++;
      j++;
    }
  }
  while (i < m) {
    raw.push({ kind: "del", text: a[i++] });
    removed++;
  }
  while (j < n) {
    raw.push({ kind: "add", text: b[j++] });
    added++;
  }
  // Collapse long unchanged runs: keep `context` lines either side of a change,
  // replace the hidden middle with a single "gap" marker.
  const rows: DiffRow[] = [];
  const ctxBuf: DiffRow[] = [];
  const emitCtx = (nextIsChange: boolean) => {
    if (ctxBuf.length === 0) return;
    const head = rows.length > 0 ? ctxBuf.slice(0, context) : [];
    const tail = nextIsChange
      ? ctxBuf.slice(Math.max(head.length, ctxBuf.length - context))
      : [];
    const hidden = ctxBuf.length - head.length - tail.length;
    if (hidden > 1) {
      rows.push(...head, { kind: "gap", count: hidden }, ...tail);
    } else {
      rows.push(...ctxBuf);
    }
    ctxBuf.length = 0;
  };
  for (const r of raw) {
    if (r.kind === "ctx") {
      ctxBuf.push(r);
    } else {
      emitCtx(true);
      rows.push(r);
    }
  }
  emitCtx(false);
  return { rows, added, removed };
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

/* ── Payload rendering ──────────────────────────────────────────────────────
 *
 * A tool-call payload is arbitrary nested JSON, and somewhere inside it is
 * often the thing the boss actually needs to read: a blog post, an email
 * body, a commit message, a document. `JSON.stringify` turns that into an
 * escaped one-liner with literal \n between every paragraph, rendered in
 * 11px mono. It's present but unreadable — which for an approval surface
 * means the boss can't review what he's approving.
 *
 * The fix is one generic shape, driven by the DATA not the source: walk the
 * payload, and any string long enough to be a document (see
 * ARTIFACT_MIN_CHARS) is lifted out and rendered as markdown with real
 * typography. Everything else stays JSON. No per-tool, per-vendor, or
 * per-kind branch exists or should ever be added here — a Gmail draft body,
 * a GitHub file's `input.content`, and a tool nobody has written yet all get
 * the same treatment for the same reason.
 */

/** Minimum string length for a payload value to count as a readable artifact
 *  rather than a scalar. ~400 chars is comfortably longer than any id, URL,
 *  path, SHA, or one-line message, and comfortably shorter than the shortest
 *  thing worth reading as prose. */
export const ARTIFACT_MIN_CHARS = 400;

/** Depth cap for the payload walk. Real payloads nest 2-3 deep; this only
 *  exists so a pathological/cyclic object can't hang the render. */
const ARTIFACT_MAX_DEPTH = 8;

export type TextArtifact = { path: string; text: string };

/** Deterministic thousands separator. Avoids `toLocaleString`, which renders
 *  differently on the server (UTC/en-US) than in the client's locale and so
 *  trips hydration warnings. */
function countLabel(n: number): string {
  return n.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

/** Walks arbitrary nested JSON and splits it in two:
 *
 *    artifacts - every string ≥ minChars, tagged with its key path
 *                (`input.content`, `files[0].body`, …) so the boss can see
 *                exactly which field he's reading.
 *    rest      - the same structure with those strings swapped for a short
 *                placeholder pointing at the artifact block.
 *
 *  Nothing is dropped: every byte is still on screen, just in the form that
 *  makes it legible. Exported for testing/reuse; `ModalPayload` is the
 *  component you normally want. */
export function extractTextArtifacts(
  value: unknown,
  minChars: number = ARTIFACT_MIN_CHARS,
): { artifacts: TextArtifact[]; rest: unknown } {
  const artifacts: TextArtifact[] = [];

  const walk = (node: unknown, path: string, depth: number): unknown => {
    if (typeof node === "string") {
      if (node.length < minChars) return node;
      // Unkeyed root string still deserves a label.
      const key = path || "value";
      artifacts.push({ path: key, text: node });
      return `↑ shown above as “${key}” (${countLabel(node.length)} chars)`;
    }
    if (node === null || typeof node !== "object") return node;
    if (depth >= ARTIFACT_MAX_DEPTH) return node;
    if (Array.isArray(node)) {
      return node.map((v, i) => walk(v, `${path}[${i}]`, depth + 1));
    }
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(node as Record<string, unknown>)) {
      out[k] = walk(v, path ? `${path}.${k}` : k, depth + 1);
    }
    return out;
  };

  const rest = walk(value, "", 0);
  return { artifacts, rest };
}

/** A long text artifact rendered in its NATIVE form — markdown with real
 *  typography — instead of an escaped JSON string in a mono block.
 *
 *  Scrolls internally (capped in `dvh` so it works on a phone drawer as well
 *  as a desktop dialog) so a 10k-char document can never blow out the modal
 *  or bury the approve/deny buttons below a mile of prose. */
export function ModalArtifact({
  path,
  text,
  className,
}: {
  path: string;
  text: string;
  className?: string;
}) {
  return (
    <ModalSection
      label="Artifact"
      icon={<AlignLeft className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />}
      meta={`${path} · ${countLabel(text.length)} chars`}
      className={className}
    >
      <div className="scroll-touch max-h-[45dvh] min-w-0 max-w-full overflow-y-auto overscroll-contain">
        <Markdown text={text} />
      </div>
    </ModalSection>
  );
}

/** An arbitrary JSON payload, rendered so a human can actually review it.
 *
 *  Long strings surface first as readable `ModalArtifact` blocks (labeled
 *  with their key path); the remaining structure follows as the JSON block,
 *  with each lifted string replaced by a pointer to its block. Use this
 *  anywhere a raw `JSON.stringify` of a tool payload would otherwise go. */
export function ModalPayload({
  value,
  meta,
  label,
  minChars = ARTIFACT_MIN_CHARS,
}: {
  value: unknown;
  /** right-aligned eyebrow on the JSON block (typically the tool name) */
  meta?: React.ReactNode;
  label?: string;
  minChars?: number;
}) {
  const { artifacts, rest } = React.useMemo(
    () => extractTextArtifacts(value, minChars),
    [value, minChars],
  );
  return (
    <>
      {artifacts.map((a) => (
        <ModalArtifact key={a.path} path={a.path} text={a.text} />
      ))}
      <ModalSection label={label} meta={meta}>
        <ModalPre mono>{JSON.stringify(rest, null, 2)}</ModalPre>
      </ModalSection>
    </>
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

/** Media preview - an image or video shown full-width inside a modal body,
 *  contained so it never escapes the frame on mobile. Use for generated
 *  assets (the Media tab tap-through) instead of a bare <img>/<video> that
 *  would miss the overflow + rounding discipline. `kind` picks the element;
 *  videos get native controls. */
export function ModalMedia({
  src,
  kind,
  alt,
  className,
}: {
  src: string;
  kind: "image" | "video";
  alt?: string;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "mt-4 flex min-w-0 max-w-full items-center justify-center overflow-hidden rounded-lg border bg-black/40",
        className,
      )}
    >
      {kind === "video" ? (
        <video
          src={src}
          controls
          playsInline
          className="max-h-[70dvh] w-full max-w-full object-contain"
        />
      ) : (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={src}
          alt={alt ?? "Generated media"}
          className="max-h-[70dvh] w-full max-w-full object-contain"
        />
      )}
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
