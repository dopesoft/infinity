"use client";

import { Wrench } from "lucide-react";
import { cn } from "@/lib/utils";

// ToolIcon renders the vendor logo for an MCP-namespaced tool name. The
// tool registry uses `<server>__<verb>` (see core/internal/tools/mcp.go),
// so we switch on the prefix. Mono marks use `currentColor` and inherit
// the brand tint via `className`; multi-color marks ignore className
// colour utilities and keep their own palette.
//
// Adding a new brand:
//   1. Define a small inline SVG below (24×24 viewBox, no fill="white").
//   2. Add a case in the switch.
//   3. Done - every existing tool-card / status pill picks it up.

type ToolIconProps = {
  /** Full MCP tool name, e.g. `claude_code__edit` or `github__create_pull_request`. */
  name?: string;
  className?: string;
};

export function ToolIcon({ name, className }: ToolIconProps) {
  const raw = name ?? "";
  const [prefix, rest] = raw.split("__", 2);
  const p = (prefix ?? "").toLowerCase();

  // Composio is a gateway - the interesting brand is the underlying toolkit
  // (the part before the first underscore in `rest`). composio__GITHUB_CREATE_ISSUE
  // → GitHub mark. Falls back to Composio's own glyph when toolkit is unknown.
  if (p === "composio") {
    const toolkit = ((rest ?? "").split("_", 1)[0] ?? "").toUpperCase();
    return composioToolkitIcon(toolkit, className);
  }

  switch (p) {
    case "claude_code":
      return (
        <AnthropicMark
          className={cn("text-[#cc785c]", className)}
          aria-label="Claude Code"
        />
      );
    case "github":
      return (
        <GitHubMark
          className={cn("text-foreground", className)}
          aria-label="GitHub"
        />
      );
    default:
      return (
        <Wrench
          className={cn("text-muted-foreground", className)}
          aria-hidden
        />
      );
  }
}

// composioToolkitIcon maps a Composio toolkit slug (the part of the tool
// name before the first underscore in the verb) to a vendor mark. Add a
// case here when you connect a new toolkit; otherwise the generic Composio
// chain glyph renders so the call is still attributable.
function composioToolkitIcon(toolkit: string, className?: string) {
  switch (toolkit) {
    case "GITHUB":
      return <GitHubMark className={cn("text-foreground", className)} aria-label="GitHub via Composio" />;
    default:
      return (
        <ComposioMark
          className={cn("text-[#6b5cff]", className)}
          aria-label={toolkit ? `${toolkit} via Composio` : "Composio"}
        />
      );
  }
}

// GitHubMark - Octocat mark from github/octicons (MIT-licensed). Single
// path, mono fill via currentColor so it inherits the foreground colour.
// Lucide-react dropped `Github` as of v0.46 (trademark concerns); inlining
// the open-sourced octicon keeps the brand recognisable without a dep.
function GitHubMark({
  className,
  ...rest
}: React.SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      className={cn("size-4 shrink-0", className)}
      role="img"
      {...rest}
    >
      <path d="M12 .5C5.65.5.5 5.65.5 12c0 5.08 3.29 9.39 7.86 10.91.58.11.79-.25.79-.55v-1.94c-3.2.7-3.87-1.54-3.87-1.54-.52-1.34-1.28-1.69-1.28-1.69-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.7 1.26 3.36.96.1-.75.4-1.26.73-1.55-2.55-.29-5.24-1.28-5.24-5.69 0-1.26.45-2.29 1.18-3.1-.12-.29-.51-1.46.11-3.04 0 0 .97-.31 3.18 1.18.92-.26 1.91-.39 2.89-.39.98 0 1.97.13 2.89.39 2.21-1.49 3.18-1.18 3.18-1.18.63 1.58.23 2.75.11 3.04.74.81 1.18 1.84 1.18 3.1 0 4.42-2.69 5.4-5.25 5.68.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.55C20.21 21.39 23.5 17.07 23.5 12 23.5 5.65 18.35.5 12 .5z" />
    </svg>
  );
}

// ComposioMark - three interlinked chain nodes evoking Composio's role as
// a connector/gateway between many SaaS surfaces. Mono fill, brand-tinted
// via `text-[#6b5cff]` (Composio's signature indigo) at the call site.
function ComposioMark({
  className,
  ...rest
}: React.SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={cn("size-4 shrink-0", className)}
      role="img"
      {...rest}
    >
      <circle cx="6" cy="12" r="2.5" />
      <circle cx="18" cy="6" r="2.5" />
      <circle cx="18" cy="18" r="2.5" />
      <path d="M8.1 10.7 15.9 7.3" />
      <path d="M8.1 13.3 15.9 16.7" />
    </svg>
  );
}

// AnthropicMark - a 4-point spark glyph (four elongated diamond petals
// arranged at 0/90/180/270°). Evokes Anthropic's signature spark without
// reproducing the registered mark literally. Mono fill via currentColor
// so a className like `text-[#cc785c]` (Anthropic's signature kraft tone)
// drops in cleanly.
function AnthropicMark({
  className,
  ...rest
}: React.SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      className={cn("size-4 shrink-0", className)}
      role="img"
      {...rest}
    >
      <path d="M12 1.5l1.6 8.9 8.9 1.6-8.9 1.6-1.6 8.9-1.6-8.9L1.5 12l8.9-1.6L12 1.5z" />
    </svg>
  );
}
