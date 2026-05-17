import type { ComponentType, ReactNode, SVGProps } from "react";
import { cn } from "@/lib/utils";

type IconProps = SVGProps<SVGSVGElement>;

/**
 * EmptyState - shared placeholder shape used across the workspace columns
 * and detail panels. Mirrors the Live-tab "fresh session" treatment:
 * lucide icon inside a muted circle, semibold title, soft sub-copy.
 *
 * Use `align="center"` for detail panels that own their column; the default
 * `align="top"` mirrors the column-top anchor used in chat / files / preview
 * so the empty states line up horizontally across the workspace.
 */
export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  align = "center",
  className,
}: {
  icon: ComponentType<IconProps>;
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  align?: "top" | "center";
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex h-full flex-col items-center gap-3 p-6 text-center",
        align === "center" ? "justify-center" : "justify-start pt-24",
        className,
      )}
    >
      <span className="inline-flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Icon className="size-5" aria-hidden />
      </span>
      <div className="max-w-md space-y-1">
        <h3 className="text-sm font-semibold">{title}</h3>
        {description && (
          <p className="text-xs leading-relaxed text-muted-foreground">{description}</p>
        )}
      </div>
      {action && <div className="pt-1">{action}</div>}
    </div>
  );
}
