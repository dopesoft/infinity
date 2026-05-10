"use client";

import { ChevronRight, Globe, Sparkles } from "lucide-react";
import { cn } from "@/lib/utils";
import { RiskBadge } from "@/components/RiskBadge";
import type { SkillSummaryDTO } from "@/lib/api";

const statusDot = {
  active: "bg-success",
  candidate: "bg-warning",
  archived: "bg-muted-foreground/40",
} as const;

export function SkillCard({
  skill,
  active,
  onClick,
}: {
  skill: SkillSummaryDTO;
  active?: boolean;
  onClick?: () => void;
}) {
  const networkEgress =
    !skill.network_egress || skill.network_egress.length === 0
      ? "no network"
      : skill.network_egress.length === 1
        ? skill.network_egress[0]
        : `${skill.network_egress.length} domains`;

  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "w-full rounded-xl border bg-card px-3 py-2 text-left transition-colors",
        "hover:bg-accent",
        active && "border-info ring-1 ring-info",
      )}
    >
      <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className={cn("inline-block size-1.5 rounded-full", statusDot[skill.status])} />
          <code className="truncate font-mono text-foreground">{skill.name}</code>
          <span className="font-mono">v{skill.version || "—"}</span>
        </div>
        {skill.source === "auto_evolved" && (
          <Sparkles className="size-3 text-info" aria-hidden />
        )}
      </div>
      <p className="mt-1 line-clamp-2 break-words text-sm">
        {skill.description || <span className="text-muted-foreground">no description</span>}
      </p>
      <div className="mt-1 flex flex-wrap items-center gap-1.5 text-[10px]">
        <RiskBadge level={skill.risk_level} />
        <span className="inline-flex items-center gap-0.5 rounded-full bg-muted px-1.5 py-0.5 font-mono uppercase text-muted-foreground">
          <Globe className="size-2.5" aria-hidden />
          {networkEgress}
        </span>
        {skill.success_rate > 0 && (
          <span className="ml-auto font-mono text-muted-foreground">
            {Math.round(skill.success_rate * 100)}%
          </span>
        )}
        <ChevronRight className="size-3 text-muted-foreground" aria-hidden />
      </div>
    </button>
  );
}
