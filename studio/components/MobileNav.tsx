"use client";

import { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Menu, X } from "lucide-react";
import {
  Drawer,
  DrawerContent,
  DrawerTitle,
  DrawerTrigger,
} from "@/components/ui/drawer";
import { ThemeToggle } from "@/components/ThemeToggle";
import { cn } from "@/lib/utils";

/* MobileNav is the right-hand hamburger that opens a draggable
 * bottom-sheet drawer on phones. Drawer contains the full nav list +
 * theme toggle. Auto-dismisses when a nav link is tapped. */

const tabs = [
  { href: "/live", label: "Live", emoji: "💬" },
  { href: "/sessions", label: "Sessions", emoji: "📜" },
  { href: "/memory", label: "Memory", emoji: "🧠" },
  { href: "/skills", label: "Skills", emoji: "🛠" },
  { href: "/heartbeat", label: "Heartbeat", emoji: "💓" },
  { href: "/trust", label: "Trust", emoji: "🤝" },
  { href: "/cron", label: "Cron", emoji: "⏰" },
  { href: "/audit", label: "Audit", emoji: "🧾" },
  { href: "/settings", label: "Settings", emoji: "⚙️" },
];

export function MobileNav() {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);

  return (
    <Drawer open={open} onOpenChange={setOpen}>
      <DrawerTrigger asChild>
        <button
          type="button"
          aria-label="Open navigation"
          className="inline-flex size-11 items-center justify-center rounded-md text-foreground hover:bg-accent active:scale-95"
        >
          {open ? <X className="size-5" /> : <Menu className="size-5" />}
        </button>
      </DrawerTrigger>
      <DrawerContent>
        <DrawerTitle className="sr-only">Navigation</DrawerTitle>
        <nav className="flex flex-col gap-1 px-3 pb-4 pt-2">
          {tabs.map((t) => {
            const active = pathname === t.href || pathname?.startsWith(t.href + "/");
            return (
              <Link
                key={t.href}
                href={t.href}
                onClick={() => setOpen(false)}
                className={cn(
                  "flex items-center gap-3 rounded-lg px-3 py-3 text-base transition-colors",
                  "min-h-12",
                  active
                    ? "bg-accent text-accent-foreground"
                    : "hover:bg-accent/60",
                )}
              >
                <span className="text-lg" aria-hidden>
                  {t.emoji}
                </span>
                <span className="flex-1 font-medium">{t.label}</span>
                {active && (
                  <span className="size-1.5 rounded-full bg-foreground" aria-hidden />
                )}
              </Link>
            );
          })}

          <div className="mt-2 flex items-center justify-between gap-2 border-t pt-3">
            <span className="text-[11px] uppercase tracking-wide text-muted-foreground">
              theme
            </span>
            <ThemeToggle />
          </div>
        </nav>
      </DrawerContent>
    </Drawer>
  );
}
