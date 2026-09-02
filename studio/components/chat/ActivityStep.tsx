"use client";

import * as React from "react";
import { stripMarkdown } from "@/lib/chat/plainText";
import Link from "next/link";
import {
  Ban,
  Bell,
  Blocks,
  BookOpen,
  Bookmark,
  Bot,
  Brain,
  Calendar,
  Check,
  ChevronRight,
  CircleAlert,
  Clock,
  Contact,
  Database,
  DollarSign,
  Download,
  Eye,
  FilePen,
  FilePlus,
  FileSpreadsheet,
  FileText,
  Flag,
  FolderOpen,
  Gauge,
  GitBranch,
  GitCommitHorizontal,
  GitPullRequest,
  Globe,
  Hand,
  History,
  Image as ImageIcon,
  Inbox,
  Layers,
  Link as LinkIcon,
  ListChecks,
  Lock,
  Mail,
  MessageSquare,
  Microscope,
  MonitorPlay,
  MousePointerClick,
  Network,
  NotebookPen,
  Pause,
  Pencil,
  Phone,
  Play,
  Puzzle,
  Radar,
  RadioTower,
  RefreshCw,
  Reply,
  Route,
  Save,
  ScanSearch,
  Search,
  Send,
  Settings,
  ShieldCheck,
  SquareCheckBig,
  Target,
  Terminal,
  Trash2,
  Upload,
  Users,
  Waypoints,
  Wrench,
  X,
  Zap,
  type LucideIcon,
} from "lucide-react";
import { Spinner } from "@/components/ui/spinner";

import { Button } from "@/components/ui/button";
import { CodeChangeView } from "@/components/chat/CodeChange";
import { Inset } from "@/components/ui/inset";
import { StatusDot } from "@/components/ui/list-row";
import { decideTrust } from "@/lib/api";
import {
  coalesce,
  firstSentence,
  formatDuration,
  previewFor,
  splitRefusal,
  type ActivityItem,
  type StepStatus,
} from "@/lib/chat/activity";
import {
  isCodeChangeTool,
  isRepoWriteTool,
  extractToolFilePath,
  extractToolFilePaths,
} from "@/lib/canvas/detection";
import { looksLikeDiff } from "@/lib/diff";
import { useNow } from "@/lib/useNow";
import { cn } from "@/lib/utils";
import type { ChatMessage } from "@/hooks/useChat";

/**
 * ActivityStep — ONE line of the ledger (MAJORDOMO §6).
 *
 * It replaces `ToolCallCard` and `ThinkingBlock`. The row is: an 18px glyph,
 * the verb in the chrome face, the meta quiet and truncating, the elapsed in
 * tabular figures, a chevron. Tapping opens the detail *in place*, inside an
 * `<Inset>` chosen by the item's `kind` — never another bordered box (§1.2).
 *
 * THE DIVISION OF LABOUR. Every word on this row, and the status behind it,
 * is decided in `lib/chat/activity.ts` and arrives on the `ActivityItem`:
 * `label` already resolves present vs past tense, `meta` already picked the
 * input field that matters, `status` already came out of `deriveStatus`. This
 * component derives NONE of that (CLAUDE.md Rule #1b: the mechanic lives in
 * code, in one place, and the renderer stays dumb). What it owns is paint,
 * the open/closed state, and the three interactions the old cards owned:
 * Approve / Deny, the Trust link, and the live code-write preview.
 *
 * COLOUR COMES FROM STATE AND NOTHING ELSE (§1.4):
 *   running  → the brand spinner, and it is the ONLY spinner on screen. The
 *              ledger nominates one row (`spinner`); every other running row
 *              — a member of an opened group, a second in-flight call — gets
 *              the pulsing brand dot instead.
 *   done     → the static glyph in the resting ink. No green check: "it
 *              worked" is the absence of red, not a decoration on every row.
 *   error    → red glyph, opens itself, the explanation in the voice face.
 *   approval → amber lock, opens itself, Approve / Deny inline.
 *   stopped  → the ban glyph, quiet, and it must never read as done.
 */

/** Lucide name → component. `activity.ts` stores a NAME so it stays pure. */
const GLYPHS: Record<string, LucideIcon> = {
  Ban, Bell, Blocks, BookOpen, Bookmark, Bot, Brain, Calendar, CircleAlert, Clock,
  Contact, Database, DollarSign, Download, Eye, FilePen, FilePlus, FileSpreadsheet,
  FileText, Flag, FolderOpen, Gauge, GitBranch, GitCommitHorizontal, GitPullRequest,
  Globe, Hand, History, Image: ImageIcon, Inbox, Layers, Link: LinkIcon, ListChecks,
  Lock, Mail, MessageSquare, Microscope, MonitorPlay, MousePointerClick, Network,
  NotebookPen, Pause, Pencil, Phone, Play, Puzzle, Radar, RadioTower, RefreshCw,
  Reply, Route, Save, ScanSearch, Search, Send, Settings, ShieldCheck, SquareCheckBig,
  Target, Terminal, Trash2, Upload, Users, Waypoints, Wrench, Zap,
};

function glyphFor(name: string): LucideIcon {
  return GLYPHS[name] ?? Wrench;
}

/** Ink per state. The ONLY place a ledger row takes colour. */
const GLYPH_TONE: Record<StepStatus, string> = {
  running: "text-brand",
  done: "text-quiet",
  error: "text-danger",
  approval: "text-warning",
  stopped: "text-quiet",
};

/**
 * Sub-second precision matters on a row (a 240ms read vs a 12s one is the
 * difference between "instant" and "he is waiting"), so this is finer-grained
 * than `formatDuration`, which is the right shape for the ledger's headline.
 */
function formatElapsed(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return formatDuration(ms);
}

/** A code/repo write: the boss wants to SEE Jarvis coding, so these open live. */
function isWriteItem(item: ActivityItem): boolean {
  const name = item.messages[0]?.toolCall?.name;
  return isCodeChangeTool(name) || isRepoWriteTool(name);
}

/** `delegate` spawns a sub-agent; the running detail says so, as it always did. */
function isDelegate(item: ActivityItem): boolean {
  const name = item.messages[0]?.toolCall?.name ?? "";
  return name === "delegate" || name === "delegate_parallel";
}

/**
 * The before/after pair behind a write, whichever tool made it.
 *
 * Deliberately keyed on the FIELDS rather than the tool name, so it covers
 * `claude_code__edit` on the Mac, `fs_edit` in the cloud workspace, a nested
 * step forwarded out of a Claude Code run, and any future editor verb — all of
 * which carry an old/new pair or a whole new body under one of these names.
 * The bridge picks which model writes the code; it does not get to pick how
 * much of it the boss can see.
 */
function codeChangeOf(
  input: Record<string, unknown> | undefined,
): { path?: string; before: string; after: string } | null {
  if (!input) return null;
  const str = (...keys: string[]): string => {
    for (const k of keys) {
      const v = input[k];
      if (typeof v === "string" && v !== "") return v;
    }
    return "";
  };
  const before = str("old_string", "old_str", "old_text");
  const after = str("new_string", "new_str", "new_text", "content", "text");
  if (!before && !after) return null;
  return { path: extractToolFilePath(input) || undefined, before, after };
}

export interface ActivityStepProps {
  item: ActivityItem;
  /**
   * This row may render the brand spinner. The ledger nominates exactly one
   * so §6's "the only spinner on screen" is guaranteed structurally rather
   * than by every caller remembering.
   */
  spinner?: boolean;
  /** Rendered inside an opened folded row: no spinner, no bottom hairline. */
  nested?: boolean;
  className?: string;
}

export function ActivityStep({ item, spinner = true, nested, className }: ActivityStepProps) {
  const write = isWriteItem(item);
  // A thinking row with no trace behind it. Claude Code redacts its reasoning
  // (every `thinking_delta` on the wire carries an empty string), so this is
  // not an edge case for that brain, it is the norm: the row auto-opened onto
  // a blank panel and read as a hang. A control that cannot show anything is
  // not shown - no chevron, no body, just the word and the clock.
  const traceless =
    item.kind === "thought" &&
    item.count === 1 &&
    !(item.messages[0]?.text ?? "").trim();
  // Opens itself when the boss must see something: what broke, what he has to
  // approve, the code being written right now, the live thinking trace.
  const shouldOpen =
    item.status === "error" ||
    item.status === "approval" ||
    (item.status === "running" && (write || (item.kind === "thought" && !traceless)));
  const [open, setOpen] = React.useState<boolean>(shouldOpen);
  const [touched, setTouched] = React.useState(false);
  const [raw, setRaw] = React.useState(false);

  // Follow the self-opening rule until the boss overrides it — then his choice
  // wins for the rest of the row's life.
  //
  // The rule may only ever OPEN a row. It used to run both ways: a write
  // opened itself while running, and `shouldOpen` went false the instant the
  // next step landed, so the diff slammed shut the moment it finished - he
  // never got to read what was written and lived in the Changes tab instead
  // (2026-09-02). A finished diff is the one thing in a build worth looking
  // at, so a write that opened itself stays open until he closes it. Errors
  // and approvals already stay. Only a thinking trace folds itself away once
  // it settles, as the old ThinkingBlock always did: the ledger's summary
  // line owns "he thought", and a settled trace is not what he came to read.
  React.useEffect(() => {
    if (touched) return;
    if (shouldOpen) {
      setOpen(true);
      return;
    }
    if (item.kind === "thought") setOpen(false);
  }, [shouldOpen, touched, item.kind]);

  const inFlight = item.status === "running" || (item.status === "approval" && item.awaiting);
  const tick = useNow(inFlight);
  // A stopped or settled row has an end stamp, so its timer is already frozen;
  // only a live one reads the clock. 0 = "no tick yet", fall back to render time.
  const end = item.endedAt ?? (inFlight ? tick || Date.now() : item.startedAt);
  const elapsed = formatElapsed(end - item.startedAt);

  // A stopped step wears the ban glyph, not the glyph of the work it was
  // doing (§6). Nobody knows whether it ran, so it must not look like a
  // finished read or a finished write.
  const Glyph = item.status === "stopped" ? Ban : glyphFor(item.glyph);
  const showSpinner = spinner && !nested && item.status === "running";

  // The row's own face, so the expandable and the flat form cannot drift: one
  // definition, two frames.
  const face = (
    <>
      <span className="flex size-[18px] shrink-0 items-center justify-center">
        {showSpinner ? (
          <Spinner className="size-[18px] text-brand" aria-hidden />
        ) : item.status === "running" ? (
          <StatusDot tone="brand" pulse />
        ) : (
          <Glyph className={cn("size-[18px]", GLYPH_TONE[item.status])} aria-hidden />
        )}
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-1">
        <span
          className={cn(
            "min-w-0 truncate font-sans text-[13.5px] font-medium transition-colors",
            item.status === "stopped" ? "text-quiet" : "text-foreground",
            "group-hover:text-foreground",
          )}
        >
          {item.label}
        </span>
        {item.meta ? (
          <span className="min-w-0 truncate font-mono text-[12px] tabular-nums text-quiet">
            {item.meta}
          </span>
        ) : null}
      </span>
      {elapsed ? (
        <span
          className="shrink-0 font-mono text-[12px] tabular-nums text-quiet"
          suppressHydrationWarning
        >
          {elapsed}
        </span>
      ) : null}
    </>
  );

  const ROW = "group flex min-h-12 w-full min-w-0 max-w-full items-center gap-3.5 py-2.5 text-left";

  return (
    <div
      className={cn(
        "min-w-0 max-w-full",
        !nested && "border-b border-hairline last:border-b-0",
        className,
      )}
    >
      {traceless ? (
        <div className={ROW}>{face}</div>
      ) : (
        <button
          type="button"
          onClick={() => {
            setTouched(true);
            setOpen((v) => !v);
          }}
          aria-expanded={open}
          // No row-wide background on hover or press. A full-bleed grey block
          // behind a ledger line reads as a chunky list item, which is exactly
          // the vibe-coded look this replaces - the affordance is the text and
          // the chevron warming up, nothing moving and nothing filling in.
          // The focus ring stays: keyboard users need to see where they are.
          className={cn(
            ROW,
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
          )}
        >
          {face}
          <ChevronRight
            className={cn(
              "size-4 shrink-0 text-quiet transition-all duration-150 group-hover:text-foreground",
              open && "rotate-90",
            )}
            aria-hidden
          />
        </button>
      )}

      {open && !traceless ? (
        <div className="min-w-0 max-w-full space-y-2.5 pb-4 pl-[32px]">
          {item.count > 1 ? (
            <GroupDetail item={item} />
          ) : (
            <StepDetail item={item} raw={raw} onRaw={() => setRaw((v) => !v)} />
          )}
        </div>
      ) : null}
    </div>
  );
}

/**
 * A folded row ("Ran 3 commands") opens to show the run it counted, one row
 * per call. Each member is a normal step — same glyph, same detail, same
 * Raw link — so nothing is reachable in the group that is not reachable on
 * its own. `nested` keeps the spinner unique and drops the second hairline.
 */
function GroupDetail({ item }: { item: ActivityItem }) {
  const members = React.useMemo(
    () => item.messages.map((m) => coalesce([m])[0]).filter(Boolean),
    [item.messages],
  );
  return (
    <div className="flex min-w-0 max-w-full flex-col">
      {members.map((m) => (
        <ActivityStep key={m.id} item={m} nested spinner={false} />
      ))}
    </div>
  );
}

function StepDetail({
  item,
  raw,
  onRaw,
}: {
  item: ActivityItem;
  raw: boolean;
  onRaw: () => void;
}) {
  const message = item.messages[0];
  const call = message?.toolCall;
  const result = message?.toolResult;
  const output = result?.output ?? "";
  const preview = previewFor(call);
  const write = isWriteItem(item);
  const paths = extractToolFilePaths(call?.input);
  const change = codeChangeOf(call?.input);

  return (
    <>
      {/* The live code write. It is the reason a running write opens itself:
          the boss wants to watch the file being written, not tap for it — and
          now he sees WHAT changed, not just that something was written. */}
      {write && change ? (
        <CodeChangeView
          path={change.path}
          before={change.before}
          after={change.after}
          moreFiles={Math.max(0, paths.length - 1)}
        />
      ) : write && preview ? (
        <>
          <Inset variant={looksLikeDiff(preview) ? "diff" : "plain"} text={preview} />
          {paths.length > 1 ? (
            <p className="font-sans text-[12px] text-quiet">
              + {paths.length - 1} more file{paths.length - 1 === 1 ? "" : "s"} in this call
            </p>
          ) : null}
        </>
      ) : null}

      {item.status === "stopped" ? (
        <p className="font-voice text-[15.5px] leading-[1.55] text-muted-foreground">
          The turn ended before this came back, so I can&apos;t tell you whether it ran.
        </p>
      ) : null}

      {item.status === "running" && isDelegate(item) ? (
        <p className="font-voice text-[15.5px] leading-[1.55] text-muted-foreground">
          Sub-agent working…
        </p>
      ) : null}

      {item.status === "approval" ? <ApprovalDetail item={item} /> : null}

      {item.status === "error" ? <FailureDetail output={output} /> : null}

      {item.status !== "error" && item.status !== "approval" ? (
        // `write` suppresses the key/value grid: the preview above IS the
        // call's payload, and showing `new_string` twice is noise.
        <Body item={item} output={output} inputs={!write || !preview} />
      ) : null}

      {/* Raw is LAST, and it is a link, not a panel: the JSON is never the
          first thing the boss sees (§6). */}
      {call ? (
        <div className="flex min-w-0 items-center gap-3 pt-0.5">
          <button
            type="button"
            onClick={onRaw}
            aria-expanded={raw}
            className="inline-flex min-h-11 items-center font-sans text-[12.5px] font-medium text-quiet transition-colors hover:text-foreground"
          >
            {raw ? "Hide raw" : "Raw"}
          </button>
        </div>
      ) : null}
      {raw && call ? (
        <Inset
          variant="plain"
          text={JSON.stringify(
            { tool: call.name, input: call.input ?? {}, output: output || undefined },
            null,
            2,
          )}
        />
      ) : null}
    </>
  );
}

/**
 * A failure, explained in the voice face and then shown in full (§6).
 *
 * The full dump only renders when it has more to say than the gist already
 * did — a one-line error would otherwise appear twice, once as Jarvis
 * speaking and once as data. Nothing is ever hidden: if there is more, it is
 * all here (CLAUDE.md: a failure must never read as a clean result).
 */
function FailureDetail({ output }: { output: string }) {
  const text = output.trim();
  const gist = firstSentence(text);
  // The voice face is for JARVIS's words. A stack trace, a `--- FAIL:` line,
  // a `panic:` — those are data, and setting them 15.5px in ink would be a
  // category error that makes the machine sound like him. When the first
  // sentence reads like prose it IS the explanation; when it reads like a log
  // line, he says the plain-English thing and the log goes in the inset below.
  const prose = /^[A-Za-z]/.test(gist) && !/[\t]/.test(gist) && gist.length <= 140;
  const spoken = prose ? gist : "That one failed. Here's exactly what came back.";
  return (
    <>
      <p className="font-voice text-[15.5px] leading-[1.55] text-foreground">
        {text ? spoken : "That one failed and gave me nothing to go on."}
      </p>
      {text && (!prose || text.length > gist.length) ? (
        <Inset variant="plain" text={text} />
      ) : null}
    </>
  );
}

/** The detail body for a settled, non-failing step, chosen by `kind` (§6). */
function Body({
  item,
  output,
  inputs,
}: {
  item: ActivityItem;
  output: string;
  inputs: boolean;
}) {
  const message = item.messages[0];
  const call = message?.toolCall;

  if (item.kind === "thought") return <ThoughtTrace message={message} />;

  if (item.kind === "terminal") {
    const input = (call?.input ?? {}) as Record<string, unknown>;
    const command =
      typeof input.command === "string"
        ? input.command
        : typeof input.cmd === "string"
          ? input.cmd
          : undefined;
    if (command || output) {
      return <Inset variant="terminal" command={command} text={output} />;
    }
  }

  if (item.kind === "note" && message && message.role !== "tool") {
    // Jarvis's own narration: his words, in his face, in quotes.
    return message.text?.trim() ? <Inset variant="quote" text={message.text.trim()} /> : null;
  }

  if (output && looksLikeDiff(output)) return <Inset variant="diff" text={output} />;

  const items = inputs ? kvItems(call?.input) : [];
  return (
    <>
      {items.length > 0 ? <Inset variant="kv" items={items} /> : null}
      {output ? <Inset variant="plain" text={output} /> : null}
    </>
  );
}

/**
 * The call's inputs as a key/value grid rather than a JSON dump — the same
 * information, readable on a phone. Raw JSON stays behind the Raw link.
 */
function kvItems(input: Record<string, unknown> | undefined): { label: string; value: string }[] {
  if (!input) return [];
  return Object.entries(input)
    .filter(([, v]) => v !== undefined && v !== null && v !== "")
    .slice(0, 8)
    .map(([k, v]) => ({
      label: k,
      value: typeof v === "string" ? v : JSON.stringify(v),
    }))
    .map((kv) => ({
      ...kv,
      value: kv.value.length > 600 ? `${kv.value.slice(0, 600)}…` : kv.value,
    }));
}

/**
 * The thinking trace. Preserved wholesale from `ThinkingBlock`: it streams,
 * it stays pinned to the bottom so the newest line is the one you see, and it
 * wears the fade mask while live. The row above already says "Thinking" /
 * "Thought it through" and carries the elapsed, which is the collapsed pill
 * the old block became.
 */
function ThoughtTrace({ message }: { message: ChatMessage | undefined }) {
  const ref = React.useRef<HTMLDivElement>(null);
  const pending = !!message?.pending;
  const text = message?.text ?? "";

  React.useEffect(() => {
    if (!pending) return;
    const el = ref.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
  }, [pending, text]);

  // A thinking trace is prose the model wrote, and it writes prose in
  // markdown - so this used to render `**Planning agent termination**` with
  // the asterisks showing, in a monospace block, as if it were output from a
  // program. It is neither code nor output: strip the syntax and set it in the
  // reading face.
  const prose = stripMarkdown(text);
  if (!prose) return null;
  return (
    <Inset>
      <div
        ref={ref}
        className={cn(
          // [overflow-wrap:anywhere] because a thinking trace is full of the
          // exact tokens that don't break: paths, URLs, hashes.
          "max-h-40 min-w-0 max-w-full overflow-y-auto whitespace-pre-wrap break-words font-sans text-[13px] leading-[1.6] text-muted-foreground scroll-touch [overflow-wrap:anywhere]",
          pending && "thinking-fade-mask",
        )}
      >
        {prose}
      </div>
    </Inset>
  );
}

/**
 * The approval detail — the one interaction that must never regress.
 *
 * Two paths reach it and both are amber:
 *   • `awaiting` — the gate parked the call on a contract and the agent loop
 *     is blocked inside WaitForDecision. There is no result and there will
 *     not be one until the boss decides. Approve / Deny post straight to
 *     `decideTrust`, the same call the Trust page and the approvals dock make.
 *   • `gated` — the call came back as the gate's synthesised `BLOCKED:` text.
 *     That path keeps its one-tap link to `/trust?focus=<contract>`.
 */
function ApprovalDetail({ item }: { item: ActivityItem }) {
  const message = item.messages[0];
  const call = message?.toolCall;
  const output = message?.toolResult?.output ?? "";
  const [decision, setDecision] = React.useState<"approved" | "denied" | null>(null);
  const [sending, setSending] = React.useState(false);
  const contractId = item.contractId;

  async function decide(next: "approved" | "denied") {
    if (!contractId || sending) return;
    setSending(true);
    const ok = await decideTrust(contractId, next);
    setSending(false);
    if (ok) setDecision(next);
  }

  // The gate's own copy, said rather than dumped: the sentence that matters in
  // the voice face, the rest behind the inset. A refusal used to render as one
  // giant raw monospace slab with a contract uuid in the middle of it.
  const refusal = splitRefusal(output);

  return (
    <>
      <p className="font-voice text-[15.5px] leading-[1.55] text-foreground">
        {item.awaiting
          ? "I've paused on this one until you say so. Approve it and I carry on from here, you don't have to message me back."
          : refusal.lead || "This needs your approval before I can run it."}
      </p>

      {call?.preview ? <Inset variant="plain" text={call.preview} /> : null}
      {item.awaiting && refusal.lead ? (
        <p className="font-voice text-[15.5px] leading-[1.55] text-muted-foreground">
          {refusal.lead}
        </p>
      ) : null}
      {refusal.rest ? <Inset variant="plain" text={refusal.rest} /> : null}

      {contractId ? (
        decision ? (
          <p className="flex items-center gap-1.5 font-sans text-[13px] font-medium text-quiet">
            {decision === "approved" ? (
              <Check className="size-4 text-brand" aria-hidden />
            ) : (
              <X className="size-4 text-danger" aria-hidden />
            )}
            {decision === "approved" ? "Approved. Carrying on." : "Denied."}
          </p>
        ) : (
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="brand"
              className="h-11"
              disabled={sending}
              onClick={() => void decide("approved")}
            >
              {sending ? <Spinner aria-hidden /> : <Check aria-hidden />}
              Approve
            </Button>
            <Button
              type="button"
              variant="outline"
              className="h-11"
              disabled={sending}
              onClick={() => void decide("denied")}
            >
              <X aria-hidden />
              Deny
            </Button>
          </div>
        )
      ) : null}

      {item.gated ? (
        <Link
          href={contractId ? `/settings?section=trust&focus=${contractId}` : "/settings?section=trust"}
          className="inline-flex min-h-11 items-center gap-1.5 font-sans text-[13px] font-medium text-quiet transition-colors hover:text-foreground"
        >
          <ShieldCheck className="size-4" aria-hidden />
          Approve in Trust tab
        </Link>
      ) : null}
    </>
  );
}

/**
 * ActivityStepFor — render ONE `ChatMessage` as a ledger row.
 *
 * The bridge for the two places a step appears outside a ledger: a lone
 * thinking block in the transcript, and `PlanProposalCard`'s fallback. It
 * runs the same `coalesce` the ledger does, so a message rendered alone and
 * the same message rendered in a run say exactly the same words.
 *
 * Returns null when there is nothing worth a row — which is how a thinking
 * block with no content still hides itself entirely.
 */
export function ActivityStepFor({
  message,
  className,
}: {
  message: ChatMessage;
  className?: string;
}) {
  const item = React.useMemo(() => coalesce([message])[0], [message]);
  if (!item) return null;
  // An empty, settled thought has nothing to say and never had a row.
  if (item.kind === "thought" && item.status !== "running" && !message.text.trim()) return null;
  return <ActivityStep item={item} className={className} />;
}
