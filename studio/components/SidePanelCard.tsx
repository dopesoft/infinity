import { cn } from "@/lib/utils";

export function SidePanelCard({
  label,
  action,
  className,
  children,
}: {
  label: string;
  action?: React.ReactNode;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section
      className={cn(
        "rounded-xl border bg-card/60 backdrop-blur-sm",
        className,
      )}
    >
      <header className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <span className="text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
          {label}
        </span>
        {action ? <div className="shrink-0">{action}</div> : null}
      </header>
      <div className="p-3">{children}</div>
    </section>
  );
}
