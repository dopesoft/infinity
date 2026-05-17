"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { MoreHorizontal } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { NAV_OVERFLOW } from "@/lib/nav-tabs";
import { useNavBadge } from "@/lib/nav-badges";
import { cn } from "@/lib/utils";

/* NavOverflow - desktop header kebab that exposes the demoted routes
 * (Skills / Heartbeat / Trust / Cron / Audit). Primary tabs (Live, Memory)
 * stay in <TabNav>. Settings keeps its own header button. */

export function NavOverflow() {
  const pathname = usePathname();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label="More"
          title="More"
          className="inline-flex size-9 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <MoreHorizontal className="size-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" sideOffset={6} className="w-52">
        {NAV_OVERFLOW.map((t) => {
          const active = pathname === t.href || pathname?.startsWith(t.href + "/");
          const Icon = t.Icon;
          return (
            <DropdownMenuItem key={t.href} asChild>
              <Link
                href={t.href}
                className={cn(
                  "flex w-full cursor-pointer items-center gap-2.5 py-2 text-sm",
                  active && "bg-accent",
                )}
              >
                <Icon className="size-4 text-muted-foreground" aria-hidden />
                <span className="flex-1">{t.label}</span>
                <OverflowBadge href={t.href} />
              </Link>
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function OverflowBadge({ href }: { href: string }) {
  const count = useNavBadge(href);
  if (count <= 0) return null;
  return (
    <span className="inline-flex h-4 min-w-[16px] items-center justify-center rounded-full bg-warning/20 px-1 font-mono text-[10px] font-semibold leading-none text-warning">
      {count > 99 ? "99+" : count}
    </span>
  );
}
