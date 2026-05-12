import Link from "next/link";
import { Infinity as InfinityIcon } from "lucide-react";
import { TabNav } from "@/components/TabNav";
import { ThemeToggle } from "@/components/ThemeToggle";
import { SignOutButton } from "@/components/SignOutButton";
import { StatusPill, type AgentState } from "@/components/StatusPill";
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
  agentState = "listening",
}: {
  children: React.ReactNode;
  agentState?: AgentState;
}) {
  return (
    <div className="flex h-app min-h-app flex-col bg-background">
      <header className="sticky top-0 z-30 flex h-14 items-center justify-between gap-2 border-b bg-background/95 px-3 pt-safe backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:px-4 lg:h-16">
        <div className="flex min-w-0 items-center gap-2">
          <Link href="/live" className="flex items-center gap-2 text-foreground">
            <InfinityIcon className="size-6 shrink-0" aria-hidden />
            <span className="hidden text-sm font-semibold tracking-tight sm:inline">
              infinity
            </span>
          </Link>
          <StatusPill state={agentState} />
        </div>

        {/* Desktop: full TabNav, centered. */}
        <div className="hidden flex-1 lg:flex lg:justify-center">
          <TabNav />
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

      <main className="flex min-h-0 flex-1 flex-col px-safe">{children}</main>

      <FooterStatus />
    </div>
  );
}
