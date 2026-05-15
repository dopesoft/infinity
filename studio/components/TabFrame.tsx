import Link from "next/link";
import { Infinity as InfinityIcon } from "lucide-react";
import { TabNav } from "@/components/TabNav";
import { ThemeToggle } from "@/components/ThemeToggle";
import { SignOutButton } from "@/components/SignOutButton";
import { StatusPill, type AgentState } from "@/components/StatusPill";
import { AutoStatusPill } from "@/components/AutoStatusPill";
import { FooterStatus } from "@/components/FooterStatus";
import { MobileNav } from "@/components/MobileNav";
import { NavOverflow } from "@/components/NavOverflow";

/* Layout shell. On mobile (<lg) the header shows a logo on the left and a
 * hamburger on the right that opens MobileNav (bottom drawer). On desktop
 * (≥lg) the header shows the logo, a center TabNav, and the ThemeToggle.
 * Tabs scrolling/overflow handling is gone on mobile because the drawer
 * gives unlimited room. */

export function TabFrame({
  children,
  agentState,
}: {
  children: React.ReactNode;
  agentState?: AgentState;
}) {
  return (
    <div className="flex h-app min-h-app flex-col bg-background">
      <header className="sticky top-0 z-30 flex h-14 items-center justify-between gap-2 border-b bg-background/95 px-3 pt-safe backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:px-4 lg:relative lg:h-16">
        <div className="flex min-w-0 items-center gap-2">
          <Link href="/live" className="flex items-center gap-2 text-foreground">
            <InfinityIcon className="size-6 shrink-0" aria-hidden />
            <span className="hidden text-sm font-semibold tracking-tight sm:inline">
              infinity
            </span>
          </Link>
          {agentState ? <StatusPill state={agentState} /> : <AutoStatusPill />}
        </div>

        {/* Desktop: TabNav anchored to the geometric center of the header so
         * side-cluster width changes (e.g. StatusPill resizing) don't push it. */}
        <div className="pointer-events-none absolute inset-y-0 left-1/2 hidden -translate-x-1/2 items-center lg:flex">
          <div className="pointer-events-auto">
            <TabNav />
          </div>
        </div>

        {/* Right cluster: theme + sign out on desktop, hamburger on mobile. */}
        <div className="flex items-center gap-1">
          <span className="hidden items-center gap-1 lg:inline-flex">
            <NavOverflow />
            <ThemeToggle />
            <SignOutButton />
          </span>
          <span className="lg:hidden">
            <MobileNav />
          </span>
        </div>
      </header>

      {/* overflow-x-hidden is the page-level guardrail: no descendant
          (chat column, dashboard card, tool output, runaway flex child)
          can push the page wider than the viewport on mobile, no matter
          how badly its own constraints break. Vertical scroll still
          flows to whichever container actually scrolls. */}
      <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-x-hidden px-safe">
        {children}
      </main>

      <FooterStatus />
    </div>
  );
}
