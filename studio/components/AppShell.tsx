"use client";

import { Suspense } from "react";
import Link from "next/link";
import { Infinity as InfinityIcon, Search } from "lucide-react";
import { NavRail } from "@/components/nav/NavRail";
import { NavDrawer } from "@/components/nav/NavDrawer";
import { RailStatus } from "@/components/nav/RailStatus";
import {
  CommandPalette,
  useCommandPalette,
} from "@/components/nav/CommandPalette";
import { FocusSheet } from "@/components/search/FocusSheet";
import { WakeNavButton } from "@/components/WakeNavButton";

/**
 * AppShell - the frame every route renders inside. Replaces TabFrame.
 *
 * WHAT CHANGED, AND WHY
 *
 * TabFrame carried a 72px header holding a logo, a status pill, a centre tab
 * strip for four routes, a kebab hiding five more, a wake button, a theme
 * toggle and a sign-out button, plus a 40px footer along the bottom. On the
 * chat page a second 40px session bar sat under it. That is 152px of chrome
 * before a single pixel of content, split across two nav controls that
 * disagreed between phone and desktop.
 *
 * Now: a 56px rail on the left holding all seven destinations at once, and
 * nothing along the top at all. Each page owns its own bar, so the page title
 * IS the top of the page. On a phone the rail becomes a hamburger and a
 * bottom sheet - deliberately not a bottom tab bar, which would tax every
 * screen 52pt forever.
 *
 * The footer's four facts (connection, tools, uptime, which model is
 * answering) moved into <RailStatus> at the foot of the rail, along with the
 * theme toggle and sign out. Nothing was dropped.
 *
 * MOBILE: `h-app` is dvh-based (never `vh`, which iOS Safari lies about),
 * `pt-safe`/`px-safe` keep the bar clear of the notch, and the
 * `overflow-x-hidden` on <main> is the page-level guard that stops any
 * descendant with runaway intrinsic width from pushing the whole page
 * sideways.
 */
export function AppShell({
  children,
  /** Page-owned bar rendered above the content on every breakpoint. */
  bar,
}: {
  children: React.ReactNode;
  bar?: React.ReactNode;
}) {
  const palette = useCommandPalette();

  return (
    <div className="flex h-app min-h-app bg-background">
      <NavRail onOpenSearch={() => palette.setOpen(true)} />

      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {/* Mobile-only top bar. Desktop has no global bar at all: the page's
            own bar is the top of the page, which is what gives the chat page
            back the 112px it used to spend on two stacked headers. */}
        <header className="flex h-14 shrink-0 items-center gap-1 border-b border-hairline bg-background px-2 pt-safe lg:hidden">
          <Link
            href="/"
            aria-label="Infinity home"
            className="grid size-9 shrink-0 place-items-center rounded-lg text-foreground"
          >
            <InfinityIcon className="size-5" aria-hidden />
          </Link>
          <span className="flex-1" />
          <button
            type="button"
            onClick={() => palette.setOpen(true)}
            aria-label="Search"
            className="grid size-11 shrink-0 place-items-center rounded-lg text-quiet transition-colors active:bg-accent/60"
          >
            <Search className="size-[18px]" aria-hidden />
          </button>
          <WakeNavButton />
          <RailStatus compact />
          <NavDrawer />
        </header>

        {bar}

        <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-x-hidden px-safe">
          {children}
        </main>
      </div>

      <CommandPalette open={palette.open} onOpenChange={palette.setOpen} />

      {/* Every `?focus=<id>&kind=<kind>` link in the app, made to open
          something. Mounted once here rather than per page — see the file. */}
      <Suspense fallback={null}>
        <FocusSheet />
      </Suspense>
    </div>
  );
}
