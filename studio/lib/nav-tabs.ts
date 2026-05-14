import {
  Activity,
  Clock,
  Brain,
  Dumbbell,
  FileCode,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  Settings,
  ShieldCheck,
  Wrench,
} from "lucide-react";

/**
 * Single source of truth for the primary nav.
 *
 * Live is now the unified workspace (chat + files/git + canvas in a single
 * 3-column desktop layout, 3-mode mobile switcher). The old standalone
 * /canvas page redirects here.
 *
 * `NAV_TABS` = primary tabs (top bar on desktop, top of mobile drawer).
 * `NAV_OVERFLOW` = secondary tabs (mobile drawer "More" section, desktop
 * header overflow kebab). They're deep-linkable but demoted because their
 * day-to-day duty (heartbeat, trust history, skill registry, audit) is read
 * mostly through the workspace's Info modal.
 *
 * Settings stays out of NAV_TABS — it's an app-level action surfaced as its
 * own header button + mobile-drawer footer link.
 */
export type NavTab = {
  href: string;
  label: string;
  Icon: LucideIcon;
};

export const NAV_TABS: NavTab[] = [
  { href: "/", label: "Dashboard", Icon: LayoutDashboard },
  { href: "/live", label: "Live", Icon: MessageSquare },
  { href: "/memory", label: "Memory", Icon: Brain },
  { href: "/gym", label: "Gym", Icon: Dumbbell },
  { href: "/skills", label: "Skills", Icon: Wrench },
];

export const NAV_OVERFLOW: NavTab[] = [
  { href: "/heartbeat", label: "Heartbeat", Icon: Activity },
  { href: "/trust", label: "Trust", Icon: ShieldCheck },
  { href: "/cron", label: "Cron", Icon: Clock },
  { href: "/code-proposals", label: "Code", Icon: FileCode },
  { href: "/settings", label: "Settings", Icon: Settings },
];
