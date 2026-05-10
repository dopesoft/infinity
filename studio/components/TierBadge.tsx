import { cn } from "@/lib/utils";

type Tier = "working" | "episodic" | "semantic" | "procedural";

const styles: Record<Tier, string> = {
  working: "bg-tier-working/15 text-tier-working",
  episodic: "bg-tier-episodic/15 text-tier-episodic",
  semantic: "bg-tier-semantic/15 text-tier-semantic",
  procedural: "bg-tier-procedural/15 text-tier-procedural",
};

export function TierBadge({
  tier,
  stale,
  className,
}: {
  tier: Tier | string;
  stale?: boolean;
  className?: string;
}) {
  const safeTier = (tier in styles ? tier : "working") as Tier;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[10px] font-mono uppercase tracking-wide",
        styles[safeTier],
        stale && "ring-1 ring-tier-stale",
        className,
      )}
    >
      {safeTier}
      {stale && <span className="text-tier-stale">stale</span>}
    </span>
  );
}
