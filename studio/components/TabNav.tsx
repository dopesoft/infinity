"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { cn } from "@/lib/utils";

const tabs = [
  { href: "/live", label: "Live" },
  { href: "/sessions", label: "Sessions" },
  { href: "/memory", label: "Memory" },
  { href: "/skills", label: "Skills" },
  { href: "/heartbeat", label: "Heartbeat" },
  { href: "/trust", label: "Trust" },
  { href: "/cron", label: "Cron" },
  { href: "/audit", label: "Audit" },
];

export function TabNav() {
  const pathname = usePathname();

  return (
    <nav
      role="tablist"
      aria-label="Primary"
      className="flex items-center gap-1 rounded-lg bg-muted p-1 text-muted-foreground"
    >
      {tabs.map((tab) => {
        const active = pathname === tab.href || pathname?.startsWith(tab.href + "/");
        return (
          <Link
            key={tab.href}
            href={tab.href}
            role="tab"
            aria-selected={active}
            className={cn(
              "min-h-9 rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
              active
                ? "bg-background text-foreground shadow-sm"
                : "hover:text-foreground",
            )}
          >
            {tab.label}
          </Link>
        );
      })}
    </nav>
  );
}
