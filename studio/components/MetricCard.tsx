import { cn } from "@/lib/utils";

export function MetricCard({
  label,
  value,
  highlight,
  className,
}: {
  label: string;
  value: number | string;
  highlight?: boolean;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "rounded-xl border bg-secondary/40 px-4 py-3",
        highlight && "border-warning/50 bg-warning/10",
        className,
      )}
    >
      <div className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</div>
      <div
        className={cn(
          "mt-1 font-mono text-2xl font-medium leading-none",
          highlight ? "text-warning-foreground" : "text-foreground",
        )}
      >
        {value}
      </div>
    </div>
  );
}
