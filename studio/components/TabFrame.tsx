import Link from "next/link";
import { IconInfinity } from "@tabler/icons-react";
import { TabNav } from "@/components/TabNav";
import { ThemeToggle } from "@/components/ThemeToggle";
import { StatusPill } from "@/components/StatusPill";
import { FooterStatus } from "@/components/FooterStatus";

export function TabFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-app min-h-app flex-col bg-background">
      <header className="sticky top-0 z-30 flex h-14 items-center justify-between gap-2 border-b bg-background/95 px-3 pt-safe backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:px-4">
        <div className="flex min-w-0 items-center gap-2">
          <Link href="/live" className="flex items-center gap-2 text-foreground">
            <IconInfinity className="size-6 shrink-0" aria-hidden />
            <span className="hidden text-sm font-semibold tracking-tight sm:inline">infinity</span>
          </Link>
          <span className="hidden md:inline">
            <StatusPill state="idle" />
          </span>
        </div>
        <div className="flex-1 overflow-x-auto scroll-touch [scrollbar-width:none] [-ms-overflow-style:none] [&::-webkit-scrollbar]:hidden">
          <div className="flex justify-center">
            <TabNav />
          </div>
        </div>
        <ThemeToggle />
      </header>

      <main className="flex min-h-0 flex-1 flex-col px-safe">{children}</main>

      <FooterStatus />
    </div>
  );
}
