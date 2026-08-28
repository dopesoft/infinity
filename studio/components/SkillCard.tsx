"use client";

import { Sparkles } from "lucide-react";
import { cn } from "@/lib/utils";
import { ListRow, type RowTone } from "@/components/ui/list-row";
import type { SkillSummaryDTO } from "@/lib/api";

/**
 * SkillCard — one skill, one row (Majordomo §2: surface → sections → rows).
 *
 * Was a `rounded-xl border bg-card` tile with three stacked chip rows inside
 * a bordered column. Now it composes `ListRow`, so the hairline, the 44px
 * touch target, the hover ground, the truncation chain and the chevron all
 * come from the primitive and this file only decides WHAT is said:
 *
 *   title  the plain-English description (the readable name rule) and only
 *          the skill's id when it has no description.
 *   meta   the engine detail on one line: id · version · risk · network.
 *   dot    status (active / candidate / archived), the one bit of colour.
 *
 * The name keeps its export and prop shape, so /skills is the only consumer
 * that had to change.
 */
const statusTone: Record<SkillSummaryDTO["status"], RowTone> = {
  active: "success",
  candidate: "warning",
  archived: "quiet",
};

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

  const meta = [
    skill.name,
    `v${skill.version || "-"}`,
    `${skill.risk_level} risk`,
    networkEgress,
    `importance ${skill.importance ?? 50}`,
  ].join(" · ");

  return (
    <ListRow
      tone={statusTone[skill.status]}
      title={skill.description?.trim() || skill.name}
      meta={meta}
      onClick={onClick}
      trailing={
        <>
          {skill.source === "auto_evolved" ? (
            <span title="Evolved by Voyager" className="inline-flex">
              <Sparkles className="size-3.5 text-info" aria-hidden />
            </span>
          ) : null}
          {skill.success_rate > 0 ? (
            <span className="font-mono text-[12px] tabular-nums text-quiet">
              {Math.round(skill.success_rate * 100)}%
            </span>
          ) : null}
        </>
      }
      className={cn(active && "bg-accent")}
    />
  );
}
