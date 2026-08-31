import { cn } from "@/lib/utils";

type Tier = "working" | "episodic" | "semantic" | "procedural";

/**
 * What each tier is called ON SCREEN.
 *
 * `working`, `episodic`, `semantic` and `procedural` are the storage tiers
 * (CoALA), and they are the right names in Go and in the database. Printed on
 * every row of the Memory tab they are four words a person has to be taught.
 * These are what he would say instead. The tier itself is unchanged and the
 * colour still carries it, so nothing downstream moves.
 */
const words: Record<Tier, string> = {
  working: "fresh",
  episodic: "something that happened",
  semantic: "a fact",
  procedural: "a rule he follows",
};

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
        "inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[10px] tracking-wide",
        styles[safeTier],
        stale && "ring-1 ring-tier-stale",
        className,
      )}
    >
      {words[safeTier]}
      {stale && <span className="text-tier-stale">out of date</span>}
    </span>
  );
}
