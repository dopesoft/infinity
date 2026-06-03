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
  Wrench,
} from "lucide-react";

/**
 * Single source of truth for the primary nav.
 *
 * The boss's mental model (2026-05-16):
 *
 *   Dashboard - info at a glance + agent notifications
 *   Chat (Live) - where the agent works in the open; trust + skill
 *                 proposals approved inline
 *   Memory - the brain (read-only browse)
 *   Skills - capabilities library, auto-authored or manual
 *
 * Everything else lives in the overflow (3-dot) menu. Trust is folded
 * into Settings (audit + bulk pending); real-time approvals still come
 * through Chat as inline ToolCallCard.
 *
 * `NAV_TABS` = primary (top bar on desktop, top of mobile drawer).
 * `NAV_OVERFLOW` = secondary (mobile drawer "More" section, desktop
 * header overflow kebab). Deep-linkable but demoted.
 */
export type NavTab = {
  href: string;
  label: string;
  Icon: LucideIcon;
};

export const NAV_TABS: NavTab[] = [
  { href: "/", label: "Dashboard", Icon: LayoutDashboard },
  { href: "/live", label: "Chat", Icon: MessageSquare },
  { href: "/memory", label: "Memory", Icon: Brain },
  { href: "/skills", label: "Skills", Icon: Wrench },
];

export const NAV_OVERFLOW: NavTab[] = [
  { href: "/lab", label: "Lab", Icon: FlaskConical },
  { href: "/cron", label: "Cron", Icon: Clock },
  { href: "/heartbeat", label: "Heartbeat", Icon: Activity },
  { href: "/logs", label: "Logs", Icon: ScrollText },
  { href: "/settings", label: "Settings", Icon: Settings },
];
