import {
  Activity,
  Brain,
  Clock,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  Settings,
  Sparkles,
} from "lucide-react";

/**
 * NAV - the single registry of every destination in Studio.
 *
 * Seven entries, all visible at once in the rail on desktop and in the
 * drawer on mobile. There is no primary/overflow split any more: the old
 * shape put four routes in a centre pill strip and hid five behind a kebab,
 * which meant half the app lived behind a control that read as an
 * afterthought AND the phone flattened both into one list, so the hierarchy
 * you learned at the desk was wrong in your hand.
 *
 * The folds that got us from nine destinations to seven:
 *
 *   /lab        -> Skills      a candidate skill, a broken skill and a
 *                              learned lesson are all skills
 *   /heartbeat  -> Activity    "what he noticed"
 *   /logs       -> Activity    "what he did" - never two questions
 *   /cron       -> Automations schedules and watchers are both "fires
 *                              without me"
 *
 * Every folded route still resolves; they are permanent redirects, the same
 * way /sessions has been since the Live drawer absorbed it.
 *
 * `label` is what a person would say out loud. The engine's word for a thing
 * (heartbeat, cron, sentinel, observation) lives in the detail sheet, in
 * mono, where it is useful for debugging and nowhere else.
 */
export type NavEntry = {
  href: string;
  label: string;
  Icon: LucideIcon;
  /** One line, used only by the drawer where there is room for it. */
  hint?: string;
};

export const NAV: NavEntry[] = [
  { href: "/", label: "Home", Icon: LayoutDashboard, hint: "What needs you, what he is doing" },
  { href: "/live", label: "Chat", Icon: MessageSquare, hint: "The conversation and the workbench" },
  { href: "/memory", label: "Memory", Icon: Brain, hint: "Everything he knows about you" },
  { href: "/skills", label: "Skills", Icon: Sparkles, hint: "What he knows how to do" },
  { href: "/automations", label: "Automations", Icon: Clock, hint: "What he does without being asked" },
  { href: "/activity", label: "Activity", Icon: Activity, hint: "What he did and what broke" },
  { href: "/settings", label: "Settings", Icon: Settings, hint: "How he behaves and what he can reach" },
];

/** True when `pathname` is inside `href`. Root only matches itself. */
export function isNavActive(pathname: string | null, href: string): boolean {
  if (!pathname) return false;
  if (href === "/") return pathname === "/";
  return pathname === href || pathname.startsWith(href + "/");
}

/**
 * Retired route -> where it lives now. Used by the redirect stubs and by the
 * command palette so an old bookmark or an old link in chat history still
 * lands somewhere real.
 */
export const NAV_REDIRECTS: Record<string, string> = {
  "/lab": "/skills",
  "/gym": "/skills",
  "/code-proposals": "/skills",
  "/heartbeat": "/activity",
  "/logs": "/activity",
  "/audit": "/activity",
  "/cron": "/automations",
  "/sessions": "/live",
  "/trust": "/settings?section=approvals",
};
