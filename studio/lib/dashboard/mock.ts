/* Mock data for the Dashboard tab.
 *
 * Until the backend tables (mem_tasks / mem_pursuits / mem_followups /
 * mem_saved / mem_sessions.seeded_from) are wired up, the Dashboard renders
 * from this static fixture. The shapes match `types.ts` 1:1 so swapping to
 * live data is a matter of replacing the imports of each `mockX` with a
 * fetcher hook — the components themselves stay unchanged.
 *
 * Times are computed at module load so the dashboard always feels "today"
 * even when reloaded later in the day. Don't read this file as a snapshot
 * of state — it's a generator for plausible state.
 */

import type {
  ActivityEvent,
  Approval,
  CalendarEvent,
  FollowUp,
  MemoryStats,
  Pursuit,
  Reflection,
  Saved,
  Todo,
  WorkItem,
} from "./types";

const NOW = new Date();
const SOD = (() => {
  const d = new Date(NOW);
  d.setHours(0, 0, 0, 0);
  return d;
})();

function isoOffset(minutes: number): string {
  return new Date(NOW.getTime() + minutes * 60_000).toISOString();
}
function isoToday(h: number, m = 0): string {
  const d = new Date(SOD);
  d.setHours(h, m, 0, 0);
  return d.toISOString();
}
function isoFuture(daysAhead: number, h: number, m = 0): string {
  const d = new Date(SOD);
  d.setDate(d.getDate() + daysAhead);
  d.setHours(h, m, 0, 0);
  return d.toISOString();
}

// ── PURSUITS ────────────────────────────────────────────────────────────────
export const mockPursuits: Pursuit[] = [
  {
    id: "p-workout",
    title: "Workout",
    cadence: "daily",
    doneToday: true,
    doneAt: isoToday(6, 42),
    streakDays: 4,
  },
  {
    id: "p-morning-pages",
    title: "Morning pages",
    cadence: "daily",
    doneToday: false,
    streakDays: 0,
  },
  {
    id: "p-read",
    title: "Read 20 min",
    cadence: "daily",
    doneToday: false,
    streakDays: 12,
  },
  {
    id: "p-meditate",
    title: "Meditate",
    cadence: "daily",
    doneToday: true,
    doneAt: isoToday(7, 5),
    streakDays: 18,
  },
  {
    id: "p-books",
    title: "Read 24 books this year",
    cadence: "goal",
    progress: { current: 8, target: 24, unit: "books" },
    dueAt: new Date(NOW.getFullYear(), 11, 31).toISOString(),
    status: "on_track",
  },
  {
    id: "p-phase9",
    title: "Ship Phase 9",
    cadence: "goal",
    progress: { current: 32, target: 100, unit: "%" },
    dueAt: new Date(NOW.getFullYear(), 6, 1).toISOString(),
    status: "on_track",
  },
  {
    id: "p-weight",
    title: "Lose 10 lbs",
    cadence: "quarterly",
    progress: { current: 2, target: 10, unit: "lbs" },
    status: "slow",
  },
  {
    id: "p-lift",
    title: "Lift 3x / week",
    cadence: "weekly",
    progress: { current: 2, target: 3, unit: "sessions" },
    status: "on_track",
  },
];

// ── TODOS ───────────────────────────────────────────────────────────────────
export const mockTodos: Todo[] = [
  {
    id: "t-insurance",
    title: "Call insurance about claim",
    done: false,
    dueAt: isoToday(17, 0),
    priority: "high",
    source: "agent",
  },
  {
    id: "t-gepa-pr",
    title: "Review GEPA frontier PR",
    done: false,
    dueAt: isoToday(18, 0),
    priority: "med",
    source: "manual",
  },
  {
    id: "t-adam-deck",
    title: "Send Adam the Tuesday deck",
    done: false,
    dueAt: isoFuture(1, 12, 0),
    priority: "med",
    source: "agent",
  },
  {
    id: "t-rent",
    title: "Pay rent",
    done: false,
    dueAt: isoFuture(2, 23, 59),
    priority: "high",
    source: "cron",
  },
  {
    id: "t-grocery",
    title: "Grocery order before Sunday",
    done: false,
    dueAt: isoFuture(3, 18, 0),
    source: "manual",
  },
];

// ── UPCOMING CALENDAR ───────────────────────────────────────────────────────
export const mockEvents: CalendarEvent[] = [
  {
    id: "e-standup",
    title: "Engineering stand-up",
    startsAt: isoToday(9, 0),
    endsAt: isoToday(9, 15),
    classification: "meeting",
    prep: [],
  },
  {
    id: "e-dentist",
    title: "Dentist — cleaning",
    startsAt: isoToday(11, 0),
    endsAt: isoToday(12, 0),
    location: "Dr. Sarah Lin · 145 W 57th St",
    classification: "appointment",
    prep: [
      {
        id: "pr-d1",
        label: "Bring insurance card",
        done: false,
        rationale: "Standard for first cleaning of the year.",
      },
      {
        id: "pr-d2",
        label: "Don't eat after 10:30am",
        done: false,
        rationale: "You usually grab coffee at 10:45 — set a 10:25 cutoff?",
      },
    ],
  },
  {
    id: "e-adam-call",
    title: "Adam Foster — call (?)",
    startsAt: isoToday(14, 0),
    endsAt: isoToday(14, 30),
    classification: "meeting",
    prep: [
      {
        id: "pr-a1",
        label: "Confirm time (Adam hasn't proposed yet)",
        done: false,
        rationale: "His email asked vaguely 'got a sec to chat?' — needs a concrete slot.",
      },
    ],
  },
  {
    id: "e-gym",
    title: "Gym — pull session",
    startsAt: isoToday(17, 30),
    endsAt: isoToday(18, 30),
    location: "Equinox Bryant Park",
    classification: "personal",
    prep: [],
  },
  {
    id: "e-design-review",
    title: "Design review · Studio v2",
    startsAt: isoFuture(1, 10, 0),
    endsAt: isoFuture(1, 11, 0),
    classification: "meeting",
    prep: [],
  },
  {
    id: "e-msg-phoebe",
    title: "Phoebe Bridgers @ Madison Square Garden",
    startsAt: isoFuture(2, 20, 0),
    endsAt: isoFuture(2, 23, 0),
    location: "MSG · 4 Pennsylvania Plaza",
    classification: "concert",
    prep: [
      {
        id: "pr-ph1",
        label: "Tickets — confirmed in Gmail, sec 110 row 5",
        done: true,
      },
      {
        id: "pr-ph2",
        label: "Book parking — SP+ on 33rd has spots ($48)",
        done: false,
        rationale: "MSG-area lots typically full by 6pm on event nights.",
      },
      {
        id: "pr-ph3",
        label: "Pre-show dinner — Atoboy 5:30pm",
        done: false,
        rationale:
          "You usually eat ~2h before shows. Atoboy is a 6-min walk, opens 5pm, fits your tasting-menu pattern.",
      },
    ],
  },
  {
    id: "e-flight",
    title: "Flight JFK → SFO",
    startsAt: isoFuture(4, 6, 30),
    endsAt: isoFuture(4, 9, 50),
    classification: "flight",
    prep: [
      { id: "pr-f1", label: "Online check-in (24h prior)", done: false },
      { id: "pr-f2", label: "Pack carry-on", done: false },
      {
        id: "pr-f3",
        label: "Car to JFK — book Sunday 4:00am",
        done: false,
        rationale: "Average JFK trip from your block: 38 min. 2h pre-flight buffer = 4:30am.",
      },
      { id: "pr-f4", label: "Out-of-office on Gmail", done: false },
      { id: "pr-f5", label: "Hotel confirm — Hotel Drisco", done: true },
    ],
  },
  {
    id: "e-dinner-atomix",
    title: "Dinner · Atomix",
    startsAt: isoFuture(4, 19, 30),
    endsAt: isoFuture(4, 22, 0),
    location: "Atomix · 104 E 30th St (NYC)",
    classification: "dinner",
    prep: [
      { id: "pr-at1", label: "Reservation confirmed via Resy", done: false, rationale: "Need to confirm — Atomix releases 8 weeks out, you may not have grabbed one." },
    ],
  },
  {
    id: "e-wedding",
    title: "Mike & Lila — wedding",
    startsAt: isoFuture(11, 16, 0),
    endsAt: isoFuture(11, 23, 30),
    location: "Hudson Valley · Bear Mountain Inn",
    classification: "social",
    prep: [
      { id: "pr-w1", label: "RSVP — confirmed", done: true },
      { id: "pr-w2", label: "Suit picked up from dry cleaner", done: false },
      { id: "pr-w3", label: "Gift — registry at Crate & Barrel", done: false },
      { id: "pr-w4", label: "Car rental for the weekend", done: false },
      { id: "pr-w5", label: "Hotel near venue (or stay at inn)", done: false },
      { id: "pr-w6", label: "Speech if asked (best man wasn't named yet)", done: false },
      { id: "pr-w7", label: "Pet-sitter for Tuesday-thru-Sunday", done: false },
      { id: "pr-w8", label: "Out-of-office Friday + Monday", done: false },
    ],
  },
  {
    id: "e-reunion",
    title: "Family reunion · upstate",
    startsAt: isoFuture(60, 12, 0),
    endsAt: isoFuture(62, 18, 0),
    location: "Adirondacks · Lake Placid",
    classification: "travel",
    prep: [
      { id: "pr-r1", label: "Confirm dates with Mom", done: false },
      { id: "pr-r2", label: "Car rental — 3 days", done: false },
      { id: "pr-r3", label: "Cabin / Airbnb", done: false },
      { id: "pr-r4", label: "Out-of-office across reunion days", done: false },
    ],
  },
  {
    id: "e-q3-board",
    title: "Q3 board meeting",
    startsAt: isoFuture(95, 14, 0),
    endsAt: isoFuture(95, 16, 0),
    classification: "meeting",
    prep: [
      { id: "pr-b1", label: "Q2 retrospective deck", done: false },
      { id: "pr-b2", label: "Q3 plan + budget", done: false },
    ],
  },
];

// ── REFLECTION OF THE DAY ───────────────────────────────────────────────────
export const mockReflection: Reflection = {
  id: "r-insurance-delay",
  title: "You keep delaying the insurance call",
  body:
    "I noticed you set the insurance call as a todo four days ago and have re-snoozed it three times. Looking back across the last six months, the pattern is clear: every time an admin task slips more than three days, it takes ~four weeks before it actually gets done. Two of those — the dental claim and the renter's policy update — eventually escalated. Worth knocking it out today rather than letting it ride.",
  capturedAt: isoOffset(-65),
  evidenceCount: 7,
};

// ── APPROVALS ───────────────────────────────────────────────────────────────
export const mockApprovals: Approval[] = [
  {
    id: "a-bash-rebase",
    kind: "trust_bash",
    title: "Run: git rebase main",
    subtitle: "claude_code__Bash",
    createdAt: isoOffset(-1),
    toolCall: {
      name: "claude_code__Bash",
      args: {
        command: "git rebase main",
        description: "Rebase current branch onto main before merging the dashboard PR",
        cwd: "/Users/n0m4d/Dev/infinity",
      },
    },
    rationale:
      "Local branch dashboard-v1 is 4 commits ahead of main; need a clean rebase before opening the PR.",
    riskLevel: "low",
  },
  {
    id: "a-code-loop",
    kind: "code_proposal",
    title: "Refactor: split loop.go responsibilities",
    subtitle: "core/internal/agent/loop.go · 412 → 3 files",
    createdAt: isoOffset(-12),
    filePath: "core/internal/agent/loop.go",
    riskLevel: "medium",
    rationale:
      "You've edited loop.go 14 times in the last 3 weeks and it's grown to 412 lines mixing IO, gating, and capture concerns. Proposing a split into loop.go (orchestration), gating.go (tool gates), capture.go (hook fan-out).",
    diff: `--- core/internal/agent/loop.go
+++ core/internal/agent/loop.go
@@ -38,82 +38,12 @@
-func (l *Loop) Run(ctx context.Context, t *Turn) (*Result, error) {
-    // ... 80 lines of mixed orchestration + gating + capture ...
-}
+// Run delegates to the per-concern packages.
+func (l *Loop) Run(ctx context.Context, t *Turn) (*Result, error) {
+    gated, err := l.gates.Check(ctx, t)
+    if err != nil { return nil, err }
+    res, err := l.orchestrate(ctx, gated)
+    l.capture.Fan(ctx, res)
+    return res, err
+}`,
  },
  {
    id: "a-curiosity-adam",
    kind: "curiosity",
    title: "Which Gmail thread did you mean?",
    subtitle: "two recent Adam threads",
    createdAt: isoOffset(-60),
    question:
      "You mentioned 'the Adam thread' in our last chat, but I'm tracking two open ones from him: the deck-feedback thread from Tuesday and today's 'got a sec to chat?' email. Which one were you talking about?",
    context:
      "Both threads are unanswered. Deck thread has 4 messages, today's thread has 1 message.",
  },
  {
    id: "a-bash-pnpm",
    kind: "trust_bash",
    title: "Run: pnpm install",
    subtitle: "claude_code__Bash",
    createdAt: isoOffset(-180),
    toolCall: {
      name: "claude_code__Bash",
      args: {
        command: "pnpm install",
        description: "Install new dependency added during dashboard build",
      },
    },
    riskLevel: "low",
  },
];

// ── FOLLOW-UPS ──────────────────────────────────────────────────────────────
export const mockFollowUps: FollowUp[] = [
  {
    id: "f-adam",
    source: "gmail",
    account: "khaya@malabieindustries.com",
    from: "Adam Foster",
    subject: "got a sec to chat?",
    preview: "hey man — got a sec to hop on a call this week? nothing urgent…",
    body:
      "hey man — got a sec to hop on a call this week? nothing urgent but want to run something by you. let me know what works.\n\n— Adam",
    receivedAt: isoOffset(-3 * 60),
    unread: true,
  },
  {
    id: "f-stripe",
    source: "gmail",
    account: "khaya@malabieindustries.com",
    from: "Stripe",
    subject: "Your annual review is ready",
    preview: "Your 2025 platform summary is now available. Total processed: $1.24M…",
    body:
      "Hi Khaya,\n\nYour 2025 Stripe platform summary is now available in the Dashboard. Highlights:\n\n• Total processed: $1.24M (up 42% YoY)\n• Avg transaction: $128\n• Top countries: US, CA, UK\n• Dispute rate: 0.21% (well below 0.5% threshold)\n\nReview your year here: https://dashboard.stripe.com/year-in-review\n\n— The Stripe team",
    receivedAt: isoOffset(-24 * 60),
  },
  {
    id: "f-infra",
    source: "slack",
    from: "#infra",
    subject: "Marc Wilson · ping",
    preview: "anyone know why the staging GH actions cache cleared overnight?",
    body:
      "Marc Wilson · 2h ago in #infra\n\n@khaya anyone know why the staging GH actions cache cleared overnight? builds went from 4m to 18m. checking if you have context.",
    receivedAt: isoOffset(-2 * 60),
  },
  {
    id: "f-mom",
    source: "imessage",
    from: "Mom",
    preview: "honey are you still coming the weekend of the 24th?",
    body:
      "honey are you still coming the weekend of the 24th? dad wants to know whether to bring out the grill for sunday.",
    receivedAt: isoOffset(-5 * 60),
  },
  {
    id: "f-linear-design",
    source: "linear",
    from: "Linear · DSGN-241",
    subject: "Needs your sign-off",
    preview: "Sara updated the dashboard mocks — final approval needed before sprint planning.",
    receivedAt: isoOffset(-6 * 60),
  },
  {
    id: "f-investor",
    source: "gmail",
    account: "khaya@dopesoft.io",
    from: "Priya Nadkarni",
    subject: "intro to the Sequoia partner you mentioned",
    preview: "Hey — happy to make the intro. Want me to cc you on the email today?",
    receivedAt: isoOffset(-30 * 60),
    unread: true,
  },
];

// ── AGENT WORK BOARD ────────────────────────────────────────────────────────
export const mockWorkItems: WorkItem[] = [
  // QUEUED
  {
    id: "w-standup",
    kind: "cron_run",
    title: "Stand-up summary",
    subtitle: "scheduled · 9:00am",
    column: "queued",
    scheduledFor: isoToday(9, 0),
    detailHref: "/cron",
  },
  {
    id: "w-voyager-research",
    kind: "voyager_opt",
    title: "Voyager: optimize research-mode skill",
    subtitle: "GEPA · queued",
    column: "queued",
    detailHref: "/skills",
  },
  {
    id: "w-skill-draft",
    kind: "skill_run",
    title: "Verify: draft-email skill",
    subtitle: "verifier queued",
    column: "queued",
    detailHref: "/skills",
  },
  {
    id: "w-cron-digest",
    kind: "cron_run",
    title: "Weekly memory digest",
    subtitle: "scheduled · Friday 6pm",
    column: "queued",
    scheduledFor: isoFuture(3, 18, 0),
    detailHref: "/cron",
  },
  // RUNNING
  {
    id: "w-morning-brief",
    kind: "cron_run",
    title: "Morning brief",
    subtitle: "running · 6:00am",
    column: "running",
    startedAt: isoToday(6, 0),
    detailHref: "/cron",
  },
  {
    id: "w-sentinel-gh",
    kind: "sentinel",
    title: "Sentinel: GH Actions watcher",
    subtitle: "checking every 5m",
    column: "running",
    detailHref: "/cron",
  },
  // AWAITING YOU
  {
    id: "w-trust-bash",
    kind: "trust",
    title: "Trust: git rebase main",
    subtitle: "needs approval",
    column: "awaiting",
    detailHref: "/trust",
  },
  {
    id: "w-code-loop",
    kind: "code_proposal",
    title: "Code proposal: loop.go",
    subtitle: "split into 3 files",
    column: "awaiting",
    detailHref: "/code-proposals",
  },
  {
    id: "w-curiosity-adam",
    kind: "curiosity",
    title: "Curiosity: which Adam thread?",
    subtitle: "waiting on you",
    column: "awaiting",
    detailHref: "/memory",
  },
  // DONE (today)
  {
    id: "w-done-digest",
    kind: "cron_run",
    title: "GitHub digest delivered",
    subtitle: "completed · 6:14am",
    column: "done",
    finishedAt: isoOffset(-28),
    durationMs: 14_200,
    detailHref: "/cron",
  },
  {
    id: "w-done-compress",
    kind: "memory_op",
    title: "Compressed 14 obs → 3 memories",
    subtitle: "completed · 8:35am",
    column: "done",
    finishedAt: isoOffset(-12),
    durationMs: 4_100,
    detailHref: "/memory",
  },
  {
    id: "w-done-reflection",
    kind: "reflection",
    title: "Reflection saved",
    subtitle: "MAR critic · 7:47am",
    column: "done",
    finishedAt: isoOffset(-60),
    durationMs: 2_200,
    detailHref: "/memory",
  },
  {
    id: "w-done-sentinel",
    kind: "sentinel",
    title: "Sentinel: GH Actions red",
    subtitle: "fired · 8:06am",
    column: "done",
    finishedAt: isoOffset(-41),
    detailHref: "/cron",
  },
  {
    id: "w-done-voyager",
    kind: "voyager_opt",
    title: "Voyager: discovered 3 triplets",
    subtitle: "completed · 5:33am",
    column: "done",
    finishedAt: isoOffset(-200),
    detailHref: "/skills",
  },
];

// ── SAVED ───────────────────────────────────────────────────────────────────
export const mockSaved: Saved[] = [
  {
    id: "s-deepmind",
    kind: "article",
    title: "How DeepMind trained Gemini with synthetic data",
    body:
      "DeepMind's June 2026 paper describes a two-stage pipeline: a teacher model generates millions of synthetic problems with hidden gold-standard chains-of-thought, then a student model trains on a curriculum that prioritizes problems where the teacher's CoT diverges from its final answer. The result was a 14-point lift on hard reasoning benchmarks…",
    url: "https://deepmind.google/research/posts/gemini-synthetic-data",
    source: "deepmind.google",
    readingMinutes: 12,
    savedAt: isoOffset(-2 * 24 * 60),
  },
  {
    id: "s-adam-thought",
    kind: "note",
    title: "Ask Adam about the hiring approach",
    body:
      "When Adam was at Stripe, they did this thing where every senior hire spent the first 30 days NOT writing code — just shadowing on-call rotations and reading post-mortems. I want to ask him whether that scales below 50 engineers, or if it only works at Stripe scale.",
    savedAt: isoOffset(-4 * 60),
  },
  {
    id: "s-msg-parking",
    kind: "link",
    title: "SP+ pre-book parking near MSG",
    url: "https://spplus.com/parking/msg-event",
    source: "spplus.com",
    savedAt: isoOffset(-7 * 24 * 60),
  },
  {
    id: "s-agi-essay",
    kind: "article",
    title: "AGI is closer than the consensus admits",
    body:
      "Three trends — synthetic data, longer-horizon RL, and per-token compute scaling — compound. The skeptic case treats them as independent; the realist case treats them as multiplicative. The 2026 frontier models are the first to show the multiplicative regime…",
    source: "essay · gwern.net",
    readingMinutes: 8,
    savedAt: isoOffset(-5 * 24 * 60),
  },
  {
    id: "s-memory-quote",
    kind: "quote",
    body:
      "Memory is not what we have. Memory is what we are. The substrate of cognition is not computation — it's the compression and retrieval of relevant past. Build the memory and the agent emerges.",
    title: "On memory as substrate",
    source: "— Marcus Du, AGI Frontiers 2026",
    savedAt: isoOffset(-14 * 24 * 60),
  },
  {
    id: "s-claude-best",
    kind: "article",
    title: "Anthropic's Claude 4.7 capabilities benchmark",
    body:
      "Claude Opus 4.7 (1M context) showed a 22% improvement on long-horizon agentic tasks over 4.6, primarily attributable to better cache-aware planning and the new compaction substrate…",
    url: "https://anthropic.com/research/claude-4-7",
    source: "anthropic.com",
    readingMinutes: 6,
    savedAt: isoOffset(-1 * 24 * 60),
  },
];

// ── ACTIVITY FEED ───────────────────────────────────────────────────────────
export const mockActivity: ActivityEvent[] = [
  {
    id: "act-future-standup",
    kind: "scheduled",
    title: "Stand-up summary",
    detail: "cron · 9:00am",
    at: isoToday(9, 0),
    future: true,
  },
  {
    id: "act-future-digest",
    kind: "scheduled",
    title: "End-of-day summary",
    detail: "cron · 6:00pm",
    at: isoToday(18, 0),
    future: true,
  },
  {
    id: "act-mem-compress",
    kind: "memory",
    title: "Compressed 14 obs → 3 memories",
    detail: "tier: semantic · provenance: 14 sources",
    at: isoOffset(-12),
  },
  {
    id: "act-gh-digest",
    kind: "completed",
    title: "GH digest delivered",
    detail: "5 PRs reviewed · 2 merges queued",
    at: isoOffset(-28),
  },
  {
    id: "act-sentinel",
    kind: "alert",
    title: "Sentinel: GitHub Actions red",
    detail: "build-test failed on infinity#1284",
    at: isoOffset(-41),
  },
  {
    id: "act-reflection",
    kind: "reflection",
    title: "Reflection saved: 'fights loop.go often'",
    detail: "14 edits in 3 weeks → suggests refactor",
    at: isoOffset(-60),
  },
  {
    id: "act-mem-promote",
    kind: "memory",
    title: "Promoted 'Adam works at Stripe (was)' → semantic",
    detail: "confidence 0.94",
    at: isoOffset(-90),
  },
  {
    id: "act-curiosity",
    kind: "memory",
    title: "Curiosity: which Adam thread?",
    detail: "two open threads detected",
    at: isoOffset(-180),
  },
  {
    id: "act-skill-discover",
    kind: "completed",
    title: "Voyager: 3 skill triplets discovered",
    detail: "ready for verification",
    at: isoOffset(-200),
  },
];

// ── MEMORY STATS ────────────────────────────────────────────────────────────
export const mockMemoryStats: MemoryStats = {
  newToday: 8,
  promotedToday: 12,
  procedural: 3,
  streakDays: 14,
};
