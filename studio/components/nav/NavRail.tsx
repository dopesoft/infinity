"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Search } from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { AppMark } from "@/components/nav/AppMark";
import { RailStatus } from "@/components/nav/RailStatus";
import { WakeNavButton } from "@/components/WakeNavButton";
import { NAV, isNavActive, type NavEntry } from "@/lib/nav-tabs";
import { useNavBadge } from "@/lib/nav-badges";
import { cn } from "@/lib/utils";

/**
 * NavRail - the desktop navigation, and the only one.
 *
 * A 56px icon rail holding all seven destinations at once, which is what
 * retires both of the controls it replaces: the centre pill strip that only
 * had room for four, and the kebab that hid the other five. Nothing is
 * behind a menu any more, so there is no "where did that page go" state.
 *
 * The rail is `lg:` and up. Below that the same registry renders as
 * <NavDrawer>, a bottom sheet behind the hamburger - deliberately NOT a
 * bottom tab bar, which would cost 52pt of vertical room on every single
 * screen forever.
 *
 * Icons carry a tooltip with the real label so the rail is legible on first
 * use; the badge is a 5px dot rather than a number because at this size a
 * count is unreadable and the count itself lives on the page.
 */
export function NavRail({ onOpenSearch }: { onOpenSearch: () => void }) {
  const pathname = usePathname();
  // Settings sits at the foot, apart from the six, because it is where you go
  // to change the machine rather than to use it.
  const main = NAV.filter((e) => e.href !== "/settings");
  const settings = NAV.find((e) => e.href === "/settings");

  return (
    <TooltipProvider delayDuration={300}>
      <nav
        aria-label="Primary"
        className="hidden w-rail shrink-0 flex-col items-center gap-0.5 border-r border-hairline bg-background pb-safe pt-3 lg:flex"
      >
        <AppMark variant="rail" />

        {main.map((entry) => (
          <RailLink key={entry.href} entry={entry} pathname={pathname} />
        ))}

        <span className="flex-1" aria-hidden />

        <RailButton label="Search" shortcut="⌘K" onClick={onOpenSearch}>
          <Search className="size-4" aria-hidden />
        </RailButton>

        {settings ? <RailLink entry={settings} pathname={pathname} /> : null}

        <WakeNavButton />
        <RailStatus />
      </nav>
    </TooltipProvider>
  );
}

function RailLink({
  entry,
  pathname,
}: {
  entry: NavEntry;
  pathname: string | null;
}) {
  const active = isNavActive(pathname, entry.href);
  const count = useNavBadge(entry.href);
  const Icon = entry.Icon;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Link
          href={entry.href}
          aria-label={entry.label}
          aria-current={active ? "page" : undefined}
          /* Active is INK AND A MARKER, never a filled rectangle. At 36px a
             `bg-accent` block is a solid grey tile sitting in an otherwise
             hairline-and-tone interface — the loudest shape on the screen,
             marking the page you are already looking at. The glyph going to
             full-strength foreground against quiet siblings says it, and the
             2px rule on the rail's edge says it again for a glance. Hover
             still washes, because a wash that comes and goes is feedback. */
          className={cn(
            "relative grid size-9 shrink-0 place-items-center rounded-lg transition-colors",
            active ? "text-foreground" : "text-quiet hover:bg-accent/60 hover:text-foreground",
          )}
        >
          <span
            aria-hidden
            className={cn(
              "absolute inset-y-1.5 -left-[9px] w-[2px] rounded-full transition-colors",
              active ? "bg-foreground" : "bg-transparent",
            )}
          />
          <Icon className="size-4" aria-hidden />
          {count > 0 && (
            <span
              aria-hidden
              className="absolute right-1.5 top-1.5 size-1.5 rounded-full bg-warning"
            />
          )}
          {count > 0 && <span className="sr-only">{count} need you</span>}
        </Link>
      </TooltipTrigger>
      <TooltipContent side="right" sideOffset={8}>
        {entry.label}
      </TooltipContent>
    </Tooltip>
  );
}

function RailButton({
  label,
  shortcut,
  onClick,
  children,
}: {
  label: string;
  shortcut?: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={onClick}
          aria-label={label}
          className="grid size-9 shrink-0 place-items-center rounded-lg text-quiet transition-colors hover:bg-accent/60 hover:text-foreground"
        >
          {children}
        </button>
      </TooltipTrigger>
      <TooltipContent side="right" sideOffset={8}>
        {label}
        {shortcut ? (
          <span className="ml-1.5 font-mono text-[10px] text-quiet">{shortcut}</span>
        ) : null}
      </TooltipContent>
    </Tooltip>
  );
}
