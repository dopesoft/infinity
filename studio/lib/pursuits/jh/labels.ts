import type { RowTone } from "@/components/ui/list-row";
import type { JHCockpit, JHContact, JHRole } from "./types";

/* Plain English for every constrained Job Hunt value.
 *
 * The LISTS never live here: they come from the server's vocabulary, so a
 * stage added to the schema shows up as a column with no client change. What
 * lives here is the WORDING, as a lookup with a graceful fallback, because
 * `google_jobs` and `positioning_read` are storage keys and a screen must
 * never render one (the plain-English rule in CLAUDE.md). An unknown value is
 * humanised rather than dropped: a stage this file has never heard of still
 * reads as a phrase, and the board still shows every card.
 */

/** Turn a storage key into something sayable: `no_named_manager` reads as
 *  "No named manager". Used for any value with no wording of its own. */
export function humanise(value: string): string {
  const words = value.replace(/[_-]+/g, " ").trim();
  if (!words) return "";
  return words.charAt(0).toUpperCase() + words.slice(1);
}

function labelFrom(map: Record<string, string>, value: string): string {
  return map[value] ?? humanise(value);
}

const ROLE_STAGES: Record<string, string> = {
  discovered: "Found",
  reviewed: "Reviewed",
  tailoring: "Tailoring",
  applied: "Applied",
  outreached: "Reached out",
  responded: "They replied",
  interviewing: "Interviewing",
  offer: "Offer",
  dead: "Closed",
};

const ROLE_SOURCES: Record<string, string> = {
  linkedin: "LinkedIn",
  builtin: "Built In",
  google_jobs: "Google Jobs",
  wellfound: "Wellfound",
  yc: "Y Combinator",
};

const CONTACT_STATUSES: Record<string, string> = {
  identified: "Found them",
  drafted: "Message drafted",
  sent: "Waiting on a reply",
  replied: "They replied",
  dead: "Gone quiet",
};

const ARTIFACT_KINDS: Record<string, string> = {
  resume: "Resume",
  cover_letter: "Cover letter",
  positioning_read: "Positioning read",
};

const ARTIFACT_STATUSES: Record<string, string> = {
  draft: "Draft",
  approved: "Approved",
  sent: "Sent",
};

const CORPUS_SOURCES: Record<string, string> = {
  interview: "From an interview",
  adhoc: "Added on its own",
};

export const stageLabel = (v: string) => labelFrom(ROLE_STAGES, v);
export const sourceLabel = (v: string) => labelFrom(ROLE_SOURCES, v);
export const contactStatusLabel = (v: string) => labelFrom(CONTACT_STATUSES, v);
export const artifactKindLabel = (v: string) => labelFrom(ARTIFACT_KINDS, v);
export const artifactStatusLabel = (v: string) => labelFrom(ARTIFACT_STATUSES, v);
export const corpusSourceLabel = (v: string) => labelFrom(CORPUS_SOURCES, v);

/* ── Money ─────────────────────────────────────────────────────────────── */

/** A band is only a band when the number is a real one. A stored 0 is the
 *  absence of a salary, not a salary of nothing, and rendering it as "$0" is
 *  the exact lie this guard exists to prevent. */
function realAmount(n: number | undefined): number | null {
  return typeof n === "number" && Number.isFinite(n) && n > 0 ? n : null;
}

function money(n: number): string {
  return n >= 1000 ? `$${Math.round(n / 1000)}k` : `$${n.toLocaleString()}`;
}

/** The pay, in the most specific honest form available: the parsed band
 *  first, then the posting's own wording, then a plain admission that the
 *  posting did not say. Never a zero, never an empty string. */
export function compLabel(role: JHRole): string {
  const min = realAmount(role.comp_min);
  const max = realAmount(role.comp_max);
  if (min && max) return min === max ? money(min) : `${money(min)} to ${money(max)}`;
  if (min) return `${money(min)} and up`;
  if (max) return `up to ${money(max)}`;
  const stated = role.comp_text?.trim();
  if (stated) return stated;
  return "Pay not listed";
}

/** Where the job is. Postings write "Remote" into this column, so remote is
 *  simply what the location says. */
export function locationLabel(role: JHRole): string {
  const where = role.location?.trim();
  return where || "Location not listed";
}

/** The fit score as a phrase, or null when nothing has scored it yet. An
 *  unscored role must not read as a role that scored zero. */
export function fitLabel(role: JHRole): string | null {
  return typeof role.fit_score === "number" ? `${role.fit_score}% fit` : null;
}

/* ── Ghost risk ────────────────────────────────────────────────────────── */

/** Whether the posting carries any ghost-listing signal at all. The flags are
 *  the evidence, so their presence is the signal: a score with no flags behind
 *  it is a number nobody can act on. */
export function hasGhostRisk(role: JHRole): boolean {
  return Array.isArray(role.ghost_flags) && role.ghost_flags.length > 0;
}

/** The ghost-risk sentence for the detail view, scored when a score exists. */
export function ghostSentence(role: JHRole): string {
  const count = role.ghost_flags?.length ?? 0;
  const signals = count === 1 ? "1 signal" : `${count} signals`;
  return typeof role.ghost_score === "number"
    ? `This posting may not be a real opening: ${signals}, scored ${role.ghost_score} out of 100.`
    : `This posting may not be a real opening: ${signals}.`;
}

/* ── Rows ──────────────────────────────────────────────────────────────── */

/** A role's tone. Driven by the ghost flags rather than by the stage, so a
 *  stage this file has never seen still gets a correct dot. Colour marks one
 *  thing here: this posting may be wasting your time. */
export function roleTone(role: JHRole): RowTone {
  return hasGhostRisk(role) ? "warning" : "default";
}

const CONTACT_TONES: Record<string, RowTone> = {
  sent: "warning",
  replied: "success",
  dead: "quiet",
};

/** A contact's tone. Amber is reserved for `sent`, which is the one state on
 *  this board carrying an obligation: a message went out and nothing came
 *  back. Everything else rests. */
export function contactTone(contact: JHContact): RowTone {
  return CONTACT_TONES[contact.outreach_status] ?? "default";
}

/* ── The header line ───────────────────────────────────────────────────── */

/** The one-line state of the board, built from the summary the server derived
 *  from the same rows on screen.
 *
 *  An empty board says so in words rather than showing a bare zero, because a
 *  board that failed to load must never be mistakable for one with nothing on
 *  it, and "0 roles" reads like both. */
export function describeBoard(cockpit: JHCockpit): string {
  const { total_roles, contacts_awaiting_reply } = cockpit.summary;
  if (total_roles === 0) return "Nothing filed yet";
  const parts = [`${total_roles} ${total_roles === 1 ? "role" : "roles"}`];
  if (contacts_awaiting_reply > 0) {
    parts.push(`${contacts_awaiting_reply} waiting on a reply`);
  }
  return parts.join(" · ");
}

/** A date the boss can read, or a plain admission when there isn't one. A
 *  posting with no stated date must say so rather than render a placeholder
 *  glyph he has to interpret.
 *
 *  Every caller renders it under `suppressHydrationWarning` because the server
 *  formats in UTC and the browser formats in his locale. */
export function shortDate(value: string | null | undefined): string {
  if (!value) return "Not stated";
  const d = new Date(value);
  return Number.isNaN(d.getTime())
    ? "Not stated"
    : d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
}
