"use client";

import { ListRow } from "@/components/ui/list-row";
import { Section } from "./Section";
import { ScrollList } from "./ScrollList";
import { DASHBOARD_LIST_ROWS } from "./listHeight";
import { relTime } from "@/lib/dashboard/format";
import type { DashboardItem, FollowUp } from "@/lib/dashboard/types";

/* Follow-ups - humans (or connector-surfaced systems) waiting on you.
 *
 * Reads a unified `followUps` array sourced from BOTH mem_followups
 * (connector polls) and mem_surface_items with surface='followups' /
 * 'inbox' / 'email' (agent-surfaced triage). Same card, same signals,
 * same dismiss path - Rule #1: one surface concept, one place to
 * render. A triage skill the agent invents drops its output here just
 * by calling surface_item with surface='followups'; no new card is
 * spawned.
 *
 * Row shape (Majordomo): sender as the title, and everything that used to be
 * a bordered chip - the account, the triager's classification, the intent, the
 * recommended mode - as one quiet meta line under it, with the subject and the
 * timestamp. Unread reads as an INFO dot in the leading slot, which is the
 * same 7px glyph every other row in the app uses.
 *
 * WHAT CHANGED, AND WHY: the row was a `TileCard` with a `size-9 rounded-md
 * border` source tile, a bordered chip row, and its own unread dot with a glow
 * shadow - four pieces of chrome to say "an email came in". Chips are boxes
 * inside a box (§1.2), and four of them made a two-line row into a four-line
 * one. Nothing is lost: every chip's TEXT survives in the meta line, so the
 * classification a triage skill attached is still readable, and the source
 * (gmail / slack / imessage) is named in words instead of drawn as a glyph.
 *
 * The dashed empty box is gone too - §1.2 forbids a bordered empty state, and
 * "Inbox zero" deserves quiet text, not a dotted rectangle.
 */

/** gmail → "email", slack → "slack", …. A word, not a glyph. */
const SOURCE_LABEL: Record<string, string> = {
  gmail: "email",
  slack: "slack",
  imessage: "imsg",
  linear: "linear",
  other: "",
};

export function FollowUpsCard({
  followUps,
  onOpen,
  onMeasure,
}: {
  followUps: FollowUp[];
  onOpen: (item: DashboardItem) => void;
  /**
   * Legacy hook: this card used to MEASURE its rendered list and hand the
   * pixel height to its neighbours (see `./listHeight`). The dashboard no
   * longer threads it - every list card clips at `DASHBOARD_LIST_ROWS`
   * instead, so the row aligns on the first paint. Still accepted so a
   * surface that genuinely needs the measurement can ask for it.
   */
  onMeasure?: (px: number | null) => void;
}) {
  return (
    <Section
      title="Email"
      badge={followUps.length}
      action={{ label: "see inbox", href: "/memory" }}
    >
      {followUps.length === 0 ? (
        <p className="py-2 text-[13px] text-quiet">Inbox zero - no one is waiting on you.</p>
      ) : (
        <ScrollList max={DASHBOARD_LIST_ROWS} onMeasure={onMeasure}>
          <div className="flex min-w-0 flex-col">
            {followUps.map((f) => (
              <FollowUpRow
                key={f.id}
                f={f}
                onClick={() => onOpen({ kind: "followup", data: f })}
              />
            ))}
          </div>
        </ScrollList>
      )}
    </Section>
  );
}

// Pull a string-valued signal from the row's metadata. The agent attaches
// {intent, mode, classification, ...} when it triages; this is the reader.
// Returns "" when the key is missing or not a string.
function metaString(m: Record<string, unknown> | undefined, ...keys: string[]): string {
  if (!m) return "";
  for (const k of keys) {
    const v = m[k];
    if (typeof v === "string" && v.trim()) return v.trim();
  }
  return "";
}

function FollowUpRow({ f, onClick }: { f: FollowUp; onClick: () => void }) {
  const source = String(f.source ?? "other");
  const account = (f.account ?? "").trim();
  // classification: triager output - "newsletter" | "personal" | "work" |
  // "promotion" | "transaction" | "notification" | "automated" | "spam"
  const classification = metaString(f.metadata, "classification", "category");
  // intent: "needs reply" | "fyi" | "urgent" | "question" | etc.
  const intent = metaString(f.metadata, "intent");
  // mode: "reply" | "read" | "skim" | "ignore" - recommended action.
  const mode = metaString(f.metadata, "mode", "action");

  // Subject first (it is what the mail is ABOUT), then the triager's read of
  // it, then who it landed with, then when. Falls back to the preview when
  // triage has not run yet, so the row never collapses to "name + time".
  const signals = [classification, intent, mode].filter(Boolean);
  const meta = [
    f.subject || f.preview || "",
    ...signals,
    account || SOURCE_LABEL[source] || source,
    relTime(f.receivedAt),
  ]
    .filter(Boolean)
    .join(" · ");

  // An unread thread is the one waiting on him; a read one is resting.
  return (
    <ListRow
      tone={f.unread ? "info" : "quiet"}
      title={f.from}
      meta={<span suppressHydrationWarning>{meta}</span>}
      onClick={onClick}
    />
  );
}
