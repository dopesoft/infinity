"use client";

import { Shield } from "lucide-react";
import { cn } from "@/lib/utils";
import type { SkillRiskLevel } from "@/lib/api";

const styles: Record<SkillRiskLevel, string> = {
  low: "border-success/40 bg-success/10 text-success",
  medium: "border-warning/40 bg-warning/10 text-warning",
  high: "border-orange-500/40 bg-orange-500/10 text-orange-500 dark:text-orange-400",
  critical: "border-danger/40 bg-danger/10 text-danger",
};

export function RiskBadge({ level, className }: { level: SkillRiskLevel; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-[10px] uppercase tracking-wide",
        styles[level],
        className,
      )}
      title={`risk: ${level}`}
    >
      <Shield className="size-3" aria-hidden />
      {level}
    </span>
  );
}
