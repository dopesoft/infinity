import {
  Activity,
  Clock,
  Brain,
  FlaskConical,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  ScrollText,
  Settings,
  ShieldCheck,
  Wrench,
} from "lucide-react";

/**
 * Single source of truth for the primary nav.
 *
 * Live is the unified workspace (chat + files/git + canvas in a single
 * 3-column desktop layout, 3-mode mobile switcher). The old standalone
 * /canvas page redirects here.
 *
 * `NAV_TABS` = primary tabs (top bar on desktop, top of mobile drawer).
 * `NAV_OVERFLOW` = secondary tabs (mobile drawer "More" section, desktop
 * header overflow kebab). Deep-linkable but demoted.
 *
 * Settings stays out of NAV_TABS, it's an app-level action surfaced as its
 * own header button + mobile-drawer footer link.
 *
 * /lab replaces the old /gym and /code-proposals slots. It's the single
 * surface for "what Jarvis noticed, learned, and taught himself" - one
 * "Fix this" tab with actionable approve-and-fix proposals, one
 * "Lessons" tab showing the training examples that already inject into
 * every turn's prompt, and one "Skills evolved" tab for autonomously
 * promoted skills. The old pages redirect here.
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
  { href: "/lab", label: "Lab", Icon: FlaskConical },
  { href: "/skills", label: "Skills", Icon: Wrench },
];

export const NAV_OVERFLOW: NavTab[] = [
  { href: "/heartbeat", label: "Heartbeat", Icon: Activity },
  { href: "/trust", label: "Trust", Icon: ShieldCheck },
  { href: "/cron", label: "Cron", Icon: Clock },
  { href: "/logs", label: "Logs", Icon: ScrollText },
  { href: "/settings", label: "Settings", Icon: Settings },
];
