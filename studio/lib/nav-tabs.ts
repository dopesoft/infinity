import {
  Activity,
  ClipboardList,
  Clock,
  Brain,
  LayoutPanelLeft,
  type LucideIcon,
  MessageSquare,
  ScrollText,
  ShieldCheck,
  Wrench,
} from "lucide-react";

/**
 * Single source of truth for the primary nav tabs. Imported by both
 * `<TabNav>` (desktop header segmented control) and `<MobileNav>`
 * (bottom-sheet drawer) so the two never drift in shape, label, or icon.
 *
 * Settings is intentionally NOT here — it lives in the desktop header's
 * right cluster as its own action and inside the mobile drawer's
 * settings shortcut, since it's an app-level action rather than a
 * primary tab.
 */
export type NavTab = {
  href: string;
  label: string;
  Icon: LucideIcon;
};

export const NAV_TABS: NavTab[] = [
  { href: "/live", label: "Live", Icon: MessageSquare },
  { href: "/canvas", label: "Canvas", Icon: LayoutPanelLeft },
  { href: "/sessions", label: "Sessions", Icon: ScrollText },
  { href: "/memory", label: "Memory", Icon: Brain },
  { href: "/skills", label: "Skills", Icon: Wrench },
  { href: "/heartbeat", label: "Heartbeat", Icon: Activity },
  { href: "/trust", label: "Trust", Icon: ShieldCheck },
  { href: "/cron", label: "Cron", Icon: Clock },
  { href: "/audit", label: "Audit", Icon: ClipboardList },
];
