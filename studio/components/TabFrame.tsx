import Link from "next/link";
import { IconInfinity } from "@tabler/icons-react";
import { TabNav } from "@/components/TabNav";
import { ThemeToggle } from "@/components/ThemeToggle";
import { StatusPill, type AgentState } from "@/components/StatusPill";
import { FooterStatus } from "@/components/FooterStatus";
import { MobileNav } from "@/components/MobileNav";

/* Layout shell. On mobile (<lg) the header shows a logo on the left and a
 * hamburger on the right that opens MobileNav (bottom drawer). On desktop
 * (≥lg) the header shows the logo, a center TabNav, and the ThemeToggle.
 * Tabs scrolling/overflow handling is gone on mobile because the drawer
 * gives unlimited room. */

export function TabFrame({
  children,
  agentState = "listening",
}: {
  children: React.ReactNode;
  agentState?: AgentState;
}) {
  return (
    <div className="flex h-app min-h-app flex-col bg-background">
      <header className="sticky top-0 z-30 flex h-14 items-center justify-between gap-2 border-b bg-background/95 px-3 pt-safe backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:px-4">
        <div className="flex min-w-0 items-center gap-2">
          <Link href="/live" className="flex items-center gap-2 text-foreground">
            <IconInfinity className="size-6 shrink-0" aria-hidden />
            <span className="hidden text-sm font-semibold tracking-tight sm:inline">
              infinity
            </span>
          </Link>
          <span className="hidden md:inline">
            <StatusPill state={agentState} />
          </span>
        </div>

        {/* Desktop: full TabNav, centered. */}
        <div className="hidden flex-1 lg:flex lg:justify-center">
          <TabNav />
        </div>

        {/* Right cluster: theme toggle on desktop, hamburger on mobile. */}
        <div className="flex items-center gap-1">
          <span className="hidden lg:inline">
            <ThemeToggle />
          </span>
          <span className="lg:hidden">
            <MobileNav />
          </span>
        </div>
      </header>

      <main className="flex min-h-0 flex-1 flex-col px-safe">{children}</main>

      <FooterStatus />
    </div>
  );
}
