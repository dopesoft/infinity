"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import { clearLiveTool, publishLiveTool } from "@/lib/live-tool";
import type { WSEvent, WSToolEvent } from "@/lib/ws/client";
import { useWebSocket } from "@/lib/ws/provider";
import { isRestoredSession, nextSessionHref } from "@/lib/chat/sessionUrl";
import { fetchSessionMessages, fetchTraces, type TurnRowDTO } from "@/lib/api";
import { attachmentRawPath, uploadAttachments, type UploadResult } from "@/lib/attachments";
import type { AssistantTranscriptEvent } from "@/lib/voice/client";
import { reconcileSteerEcho } from "@/lib/chat/steer";
import { settleNestedOnly } from "@/lib/chat/settle";
import { survivesRefetch } from "@/lib/chat/preserve";
import { toolRowToMessage } from "@/lib/chat/toolRow";
import { useCodingRuns } from "@/lib/runs/useCodingRuns";
import {
  initialLiveness,
  onConnected as livenessOnConnected,
  onFrame as livenessOnFrame,
  onSend as livenessOnSend,
  onSessionSwitch as livenessOnSessionSwitch,
  onStop as livenessOnStop,
  onTick as livenessOnTick,
  type LivenessAction,
  type LivenessState,
} from "@/lib/chat/liveness";

export type ChatRole = "user" | "assistant" | "tool" | "thinking";

export type ChatAttachment = {
  /** mem_attachments id once uploaded to Core. Absent while uploading / on failure. */
  id?: string;
  name: string;
  mimeType?: string;
  sizeBytes?: number;
  text?: string;
  /** Image preview: a local blob: URL while sending, Core's raw route after reload. */
  previewUrl?: string;
  /** Where the file landed on Jarvis's workspace (when the bridge was up). */
  storagePath?: string;
  /** Core raw route for "open" (JWT-protected). */
  url?: string;
  uploading?: boolean;
  error?: string;
  extractStatus?: string;
  pageCount?: number;
  file?: File;
};

export type ChatMessage = {
  id: string;
  role: ChatRole;
  text: string;
  attachments?: ChatAttachment[];
  toolCall?: WSToolEvent;
  toolResult?: WSToolEvent;
  pending?: boolean;
  inputTokens?: number;
  outputTokens?: number;
  latencyMs?: number;
  error?: string;
  createdAt: number;
  runId?: string;
  progress?: number;
  // Enriched background_build_progress fields, consumed by BackgroundProgressCard.
  progressStep?: number;
  progressAction?: string;
  progressDetail?: string;
  progressTask?: string;
  // Only set on `thinking` messages once the agent moves on to text/tool/complete.
  endedAt?: number;
  // turnId is the server-minted id of the turn a live row belongs to. Deltas
  // land in the pending assistant bubble of THEIR turn, never merely "the
  // last pending bubble": a nested coding step or a mid-turn settle between
  // two deltas used to split one reply into two bubbles.
  turnId?: string;
  // steered=true on a user message means it was typed and sent mid-turn -
  // it routes through the WS `steer` channel and gets drained into the
  // running agent loop on the next iteration boundary. ChatBubble surfaces
  // a small "↳ steered" affordance so the transcript reads correctly.
  steered?: boolean;
  // interrupted=true on the final assistant message of a turn means the
  // user pressed Stop mid-stream. The partial text streamed is preserved;
  // the UI surfaces a "↩ interrupted" hint rather than an error state.
  interrupted?: boolean;
  // thinkingTokens: how much a brain has reasoned on this turn, when it
  // reports the AMOUNT instead of the reasoning (Claude Code redacts the
  // text). Shown on the Thinking row as the one live sign it is working.
  thinkingTokens?: number;
  // interim=true on an assistant bubble that streamed BEFORE a tool call in
  // the same turn (the "let me check…" narration). Folded into the turn's
  // work block by ConversationStream; the turn's final reply is never interim.
  interim?: boolean;
  // proactive=true marks an assistant bubble that originated from the
  // heartbeat broadcaster (an unprompted turn), not from a user message.
  // Studio renders a subtle origin badge so the boss can tell the agent
  // spoke first.
  proactive?: boolean;
  proactiveKind?: string;
  // seeded=true marks the dashboard context block injected by
  // Discuss-with-Jarvis. It's a user-role turn to the model, but Studio
  // renders it as a distinct "from dashboard" card (DashboardContextCard)
  // instead of a plain user message. seedKind is the originating
  // dashboard item kind (e.g. "activity") used as the card header.
  seeded?: boolean;
  seedKind?: string;
  voiceResponseId?: string;
  voiceLastSequence?: number;
  voiceTranscriptSource?: AssistantTranscriptEvent["source"];
  // curiosityId links a heartbeat/seeded finding to an open curiosity
  // question. When set, the card renders an "Approve & fix" action that
  // marks the question approved and tells the agent to apply the fix.
  curiosityId?: string;
};

type Usage = { input: number; output: number };

const SESSION_KEY = "infinity:sessionId";
// Optimistic per-session message cache. Core (Postgres-backed) remains the
// source of truth - this cache only exists so a refresh while Core is
// offline doesn't blank out the visible conversation. Whenever Core
// returns rows for the session, those overwrite the cache entirely.
const MESSAGES_KEY_PREFIX = "infinity:messages:";
const MESSAGES_CACHE_LIMIT = 200;
// Last time the boss actually exchanged messages, stamped on every transcript
// change. Used by the stale-session rotation below.
const LAST_ACTIVE_KEY = "infinity:lastActiveAt";
// A session restored from storage (NOT explicitly opened) whose last activity
// is older than this is a finished conversation: rotate to a fresh session so
// the new exchange becomes its own chat with its own auto-generated title.
// Without this, the wake-word/voice flow especially talks into whatever
// weeks-old session localStorage was holding - the 2026-07-10 voice chat
// landed in a 17-day-old Discuss session, so no new titled chat ever appeared
// ("voice chats don't generate a title"). Memory continuity is unaffected:
// cross-session recall is the mem_* substrate's job, not the transcript's.
const STALE_SESSION_MS = 6 * 60 * 60 * 1000;
// How often the liveness machine (lib/chat/liveness.ts) is ticked while a
// turn is in flight. It decides about silence; nothing here does.
const LIVENESS_TICK_MS = 1_000;
// After Stop, how long to wait for the server's word before asking again.
const STOP_ACK_MS = 5_000;

function makeId() {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}

function newSessionId() {
  if (typeof crypto !== "undefined" && crypto.randomUUID) return crypto.randomUUID();
  return `sess_${Date.now()}_${Math.random().toString(16).slice(2)}`;
}

// A server transcript row as returned by GET /api/sessions/{id}/messages.
type ServerRow = {
  role: ChatRole;
  text: string;
  created_at: string;
  steered?: boolean;
  kind?: string;
  seed_kind?: string;
  curiosity_id?: string;
  attachments?: {
    id?: string;
    name?: string;
    mime_type?: string;
    size_bytes?: number;
    text?: string;
    preview_url?: string;
    storage_path?: string;
    extract_status?: string;
    page_count?: number;
  }[];
  // Tool-call reconstruction (role="tool"): rebuilt into a ToolCallCard so it
  // survives navigation/reload. tool_output present = completed.
  tool_call_id?: string;
  interim?: boolean;
  tool_running?: boolean;
  tool_interrupted?: boolean;
  tool_awaiting_approval?: boolean;
  tool_contract_id?: string;
  tool_name?: string;
  tool_input?: Record<string, unknown>;
  tool_output?: string;
  tool_is_error?: boolean;
};

function rowAttachmentsToChat(atts?: ServerRow["attachments"]): ChatAttachment[] | undefined {
  if (!Array.isArray(atts) || atts.length === 0) return undefined;
  const out = atts
    .map((att) => {
      const id = att.id?.trim() || undefined;
      return {
        id,
        name: att.name?.trim() || "attachment",
        mimeType: att.mime_type?.trim() || undefined,
        sizeBytes: typeof att.size_bytes === "number" ? att.size_bytes : undefined,
        text: att.text?.trim() || undefined,
        previewUrl: att.preview_url?.trim() || undefined,
        storagePath: att.storage_path?.trim() || undefined,
        url: id ? attachmentRawPath(id) : undefined,
        extractStatus: att.extract_status?.trim() || undefined,
        pageCount: typeof att.page_count === "number" ? att.page_count : undefined,
      };
    })
    .filter((att) => att.id || att.name || att.previewUrl || att.text);
  return out.length > 0 ? out : undefined;
}

function dedupeAttachments(atts: ChatAttachment[]): ChatAttachment[] {
  const seen = new Set<string>();
  const out: ChatAttachment[] = [];
  for (const att of atts) {
    const key = att.id
      ? `id:${att.id}`
      : [att.name, att.sizeBytes ?? "", att.mimeType ?? "", att.storagePath ?? "", att.previewUrl ?? ""].join("|");
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(att);
  }
  return out;
}

function mergeAttachmentLists(
  local?: ChatAttachment[],
  remote?: ChatAttachment[],
): ChatAttachment[] | undefined {
  const merged = dedupeAttachments([...(local ?? []), ...(remote ?? [])]);
  return merged.length > 0 ? merged : undefined;
}

// rowToMessage converts a canonical server transcript row into the local
// ChatMessage shape. Single source of truth for the mapping so the
// session-load, switch-session, and reconnect-merge paths stay in sync -
// notably the `seeded` flag that routes the Discuss-with-Jarvis context
// block to DashboardContextCard instead of a plain user bubble.
function rowToMessage(r: ServerRow): ChatMessage {
  // Reconstruct a tool-call card from a persisted PostToolUse row so the
  // inline ToolCallCard survives navigation/reload (the history endpoint used
  // to omit tool events, so cards vanished on return).
  if (r.role === "tool" && r.tool_call_id) {
    return toolRowToMessage(
      { ...r, tool_call_id: r.tool_call_id },
      makeId(),
      new Date(r.created_at).getTime() || Date.now(),
    );
  }
  // Durable turn-level error (provider/API failure) replayed from mem_turns.
  // Rebuilt into the same red error card the live WS path renders, so it
  // survives reload / a second device instead of vanishing.
  if (r.kind === "error") {
    return {
      id: makeId(),
      role: "assistant",
      text: "",
      error: r.text,
      createdAt: new Date(r.created_at).getTime() || Date.now(),
    };
  }
  return {
    id: makeId(),
    role: r.role,
    text: r.text,
    attachments: rowAttachmentsToChat(r.attachments),
    createdAt: new Date(r.created_at).getTime() || Date.now(),
    steered: r.steered || undefined,
    // Narration that streamed before a tool call stays folded into the
    // ledger on reload, exactly as it was live. Without this the server's
    // copy arrived as a plain reply and split the ledger around it.
    interim: r.interim || undefined,
    seeded: r.kind === "dashboard_seed" || undefined,
    seedKind: r.seed_kind || undefined,
    curiosityId: r.curiosity_id || undefined,
  };
}

function readStoredSessionId(): string {
  if (typeof window === "undefined") return "";
  try {
    return window.localStorage.getItem(SESSION_KEY) || "";
  } catch {
    return "";
  }
}

// rememberSession records WHICH CONVERSATION IS OPEN, in both places that
// answer that question: localStorage (what a refresh falls back to) and the
// `?session=` param (what a refresh reads FIRST).
//
// They used to be allowed to disagree. A chat opened from the dashboard or a
// record sheet arrives as /live?session=<id>; switching chats in the drawer or
// starting a new one moved only React state and localStorage, so that first id
// stayed in the address bar. Refresh read the URL, won, and dropped him into an
// older conversation. Every session change in this hook goes through here, so a
// new writer cannot reintroduce the split.
function rememberSession(id: string) {
  if (typeof window === "undefined") return;
  try {
    if (id) window.localStorage.setItem(SESSION_KEY, id);
    else window.localStorage.removeItem(SESSION_KEY);
  } catch {
    /* private mode / quota */
  }
  // Shallow, and a REPLACE: the back button belongs to the pages he visited,
  // not to every conversation he opened inside this one.
  const href = nextSessionHref(window.location.href, id);
  if (href) {
    try {
      window.history.replaceState(window.history.state, "", href);
    } catch {
      /* ignore - the localStorage half still holds */
    }
  }
}

function readLastActiveAt(): number {
  if (typeof window === "undefined") return 0;
  try {
    return Number(window.localStorage.getItem(LAST_ACTIVE_KEY)) || 0;
  } catch {
    return 0;
  }
}

function writeLastActiveAt(t: number) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(LAST_ACTIVE_KEY, String(t));
  } catch {
    /* private mode / quota */
  }
}

// lastActivityFor estimates when a stored session was last used: the global
// activity stamp or the newest cached message for that session, whichever is
// later. 0 when unknown (fresh device / cleared storage) - callers treat
// unknown as NOT stale so we never rotate away from a session blind.
function lastActivityFor(sessionId: string): number {
  let last = readLastActiveAt();
  for (const m of readCachedMessages(sessionId)) {
    if (m.createdAt > last) last = m.createdAt;
  }
  return last;
}

// Pending / in-flight messages aren't worth caching - they'd hydrate as
// orphaned spinners. The "thinking" placeholder is always dropped (it's
// purely an in-flight affordance). Error messages, however, ARE cached:
// if the boss gets an error and navigates away to fix it, the error
// MUST still be visible when they come back. Errors are durable client
// state; the server never persisted them.
function isCacheable(m: ChatMessage): boolean {
  if (m.role === "thinking") return false;
  if (m.pending && !m.error) return false;
  return true;
}

function readCachedMessages(sessionId: string): ChatMessage[] {
  if (typeof window === "undefined" || !sessionId) return [];
  try {
    const raw = window.localStorage.getItem(MESSAGES_KEY_PREFIX + sessionId);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as ChatMessage[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function writeCachedMessages(sessionId: string, messages: ChatMessage[]) {
  if (typeof window === "undefined" || !sessionId) return;
  try {
    const trimmed = messages.filter(isCacheable).slice(-MESSAGES_CACHE_LIMIT);
    if (trimmed.length === 0) {
      window.localStorage.removeItem(MESSAGES_KEY_PREFIX + sessionId);
      return;
    }
    window.localStorage.setItem(
      MESSAGES_KEY_PREFIX + sessionId,
      JSON.stringify(trimmed),
    );
  } catch {
    /* private mode / quota - caller can ignore */
  }
}

// mergeServerRows reconciles the canonical transcript from Core with the
// optimistic local state. We keep any *pending* local messages (thinking
// indicators, drafts in flight) and replace finalized turns with whatever
// Core reports - Core is the source of truth for completed turns.
//
// This is the rehydration path used on WS reconnect: a turn the agent
// completed while we were disconnected is in `rows` but missing locally,
// so we append it.
function mergeServerRows(
  local: ChatMessage[],
  rows: ServerRow[],
): ChatMessage[] {
  // Convert server rows into ChatMessage shape with stable created_at
  // timestamps for ordering.
  const fromServer: ChatMessage[] = rows.map(rowToMessage);
  // PRESERVE local tool messages. The server transcript rows do NOT
  // carry toolCall / toolResult payloads (role+text only), so a naive
  // merge that drops local in favour of server would erase the inline
  // approval card (SkillProposalCard / ToolCallCard awaiting Approve /
  // Deny). Net effect from the boss's POV: "approval dialog disappears
  // when I click away or widen the column" because a navigation /
  // reconcile reloads from server. We keep every local tool message;
  // they sort back into place by created_at below.
  // De-dupe local tool cards by tool_call.id while preserving them. Without
  // this, a single in-flight tool call that got appended more than once (the
  // phantom-duplicate path) would survive every reconcile as N copies. Keep
  // the first occurrence (it carries the live streaming/awaiting state).
  const seenLocalToolIds = new Set<string>();
  const localToolMessages: ChatMessage[] = local.filter((m) => {
    if (m.role !== "tool" || !m.toolCall) return false;
    const id = m.toolCall.id;
    if (!id) return true;
    if (seenLocalToolIds.has(id)) return false;
    seenLocalToolIds.add(id);
    return true;
  });
  // Preserve every in-flight local item, wherever it sits: the streaming
  // tail, but ALSO an interim assistant bubble that streamed BEFORE a tool
  // call in the same turn. The server transcript only holds a turn's FINAL
  // text (TaskCompleted), so a tail-only rule erased that interim text on
  // every reconnect/reconcile — the "it had a whole message, then it
  // disappeared" report (2026-08-26). They sort back into place by time.
  //
  // An ERROR card is preserved on the same grounds and for a sharper reason:
  // it carries no `pending` flag (useChat's "error" frame handler builds it
  // settled), so it matched neither rule and every refetch deleted it. The
  // boss saw a failure flash up and remove itself, which is the UI version of
  // a silent-green failure - worse than never showing it, because now he
  // cannot tell whether it happened. Some errors never reach the server
  // transcript at all: the loop can emit one mid-stream and then recover, and
  // only a turn that CLOSES errored gets a durable row. Local is the only
  // copy, so local keeps it.
  const pendingTail: ChatMessage[] = local.filter(survivesRefetch);
  // De-dupe: drop any pending bubble whose text matches OR is a prefix of
  // a same-role server row. The server's finalized turn always wins.
  //
  // The prefix check matters for voice mode: a streaming assistant bubble
  // may sit at a partial transcript ("Good afternoon, boss. What's on
  // your") while the server has already persisted the completed text
  // ("…What's on your mind today?"). Without the prefix-dedupe we'd
  // render BOTH - the orphaned streaming partial AND the canonical
  // completed turn. The bug looked like the agent "duplicating" itself.
  const sameRoleServer: Map<ChatRole, string[]> = new Map();
  for (const m of fromServer) {
    const list = sameRoleServer.get(m.role) ?? [];
    list.push(m.text.trim());
    sameRoleServer.set(m.role, list);
  }
  // The same fact must not be told twice: once a turn closes errored the
  // server replays a durable error row, and the local card it duplicates goes.
  const serverErrors = new Set(
    fromServer.map((m) => (m.error ?? "").trim()).filter(Boolean),
  );
  const filteredPending = pendingTail.filter((m) => {
    const err = (m.error ?? "").trim();
    if (err) return !serverErrors.has(err);
    const candidates = sameRoleServer.get(m.role);
    if (!candidates) return true;
    const local = m.text.trim();
    if (!local) return true;
    for (const s of candidates) {
      if (s === local) return false;
      if (s.startsWith(local)) return false;
    }
    return true;
  });
  // Dedup tool cards by id. Server now returns RICH tool rows (reconstructed
  // from PostToolUse), so on reload they rebuild the cards. During a live
  // session the local message is freshest (it has the streaming/awaiting
  // state), so when both exist we keep the local one and drop the server copy.
  // On a cold reload there's no local copy → the reconstructed server card is
  // kept. This is what makes tool cards survive navigation.
  const localToolIds = new Set(
    localToolMessages.map((m) => m.toolCall?.id).filter(Boolean) as string[],
  );
  const fromServerSansTools = fromServer.filter((m) => {
    if (m.role !== "tool") return true;
    const id = m.toolCall?.id;
    if (id && localToolIds.has(id)) return false; // prefer the live local card
    return true; // reconstructed-only (reload) — keep it
  });
  // Sort the local tool messages into the timeline by createdAt so a
  // tool that fired mid-session lands in the right slot relative to
  // the server-loaded user/assistant turns around it.
  const merged: ChatMessage[] = [
    ...fromServerSansTools,
    ...localToolMessages,
    ...filteredPending,
  ].sort((a, b) => a.createdAt - b.createdAt);
  return merged;
}

// markTrailingAssistantInterim flags a pending assistant bubble at the tail
// as interim narration (a tool call is about to follow it).
function markTrailingAssistantInterim(messages: ChatMessage[]): ChatMessage[] {
  const last = messages[messages.length - 1];
  if (!last || last.role !== "assistant" || !last.pending || last.interim) return messages;
  const next = [...messages];
  next[next.length - 1] = { ...last, interim: true };
  return next;
}

// settleInFlight closes EVERYTHING the turn left open once it ends (complete,
// error, or interrupted): every pending assistant bubble (an interim "let me
// check the plan…" before a tool call stays pending forever otherwise) and
// every tool card that never got its result frame. A card with no result
// after the turn is over is not "running": its timer must stop and it must
// read as stopped (2026-08-26: code_agent card ticking past 500s after the
// turn had already finished, "I can't stop it").
function settleInFlight(
  messages: ChatMessage[],
  now: number,
  codingLive = true,
): ChatMessage[] {
  return messages.map((m) => {
    if (m.role === "assistant" && m.pending) return { ...m, pending: false };
    // A NESTED step belongs to a coding job, not to this turn. The job runs on
    // past the turn by design — that is the whole point of a detached build —
    // so stamping it "interrupted" while the job is still going would put a
    // stopped row on screen for work that is visibly still editing files.
    //
    // But ONLY while it is still going. The exemption used to be
    // unconditional, and its cost was the boss's exact words: "it looked like
    // 'run a command' was still spinning and time ticking upwards even tho I
    // had no stop button". No stop button means the turn is over; nothing
    // coding means the job is over too; a row still counting up in that state
    // is the UI claiming work that nobody is doing. The job closes its own
    // steps server-side now, but that only helps while the core is alive to
    // send the frames - after a restart there is nobody left to send them, and
    // this is what makes the row settle anyway.
    if (m.toolCall?.nested) return codingLive ? m : { ...m, interrupted: true, endedAt: now };
    if (m.role === "tool" && m.toolCall && !m.toolResult && !m.interrupted) {
      return { ...m, interrupted: true, endedAt: now };
    }
    return m;
  });
}

// Mark the most recent pending `thinking` message as complete. Called whenever
// the agent transitions out of "thinking" - first text delta, first tool call,
// or stream complete. Returns a new array (never mutates).
function closePendingThinking(messages: ChatMessage[]): ChatMessage[] {
  const next = [...messages];
  for (let i = next.length - 1; i >= 0; i--) {
    if (next[i].role === "thinking" && next[i].pending) {
      next[i] = { ...next[i], pending: false, endedAt: Date.now() };
      break;
    }
  }
  return next;
}

function normalizedVoiceText(text: string): string {
  return text.trim().replace(/\s+/g, " ").toLowerCase();
}

function isDuplicateVoiceAssistantText(a: string, b: string): boolean {
  const left = normalizedVoiceText(a);
  const right = normalizedVoiceText(b);
  if (!left || !right) return false;
  return left === right || left.startsWith(right) || right.startsWith(left);
}

function findLatestPendingAssistant(messages: ChatMessage[]): number {
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === "assistant" && messages[i].pending) return i;
  }
  return -1;
}

// SendAttachment is the WS-frame reference: the id Core handed back from the
// upload. Bytes never ride the socket; Core resolves the id into native
// image / PDF blocks for the brain.
type SendAttachment = {
  id: string;
  name: string;
  mime_type?: string;
  size_bytes?: number;
};

function toSendAttachment(att: ChatAttachment): SendAttachment {
  return {
    id: att.id ?? "",
    name: att.name,
    mime_type: att.mimeType,
    size_bytes: att.sizeBytes,
  };
}

function isBlobAttachmentPreview(att: ChatAttachment): boolean {
  return Boolean(att.file && att.previewUrl?.startsWith("blob:"));
}

// fileToLocalAttachment is the optimistic chip shown the instant the boss
// hits send: local preview for images, "Uploading…" state until Core answers.
function fileToLocalAttachment(file: File): ChatAttachment {
  const att: ChatAttachment = {
    name: file.name || "attachment",
    mimeType: file.type || undefined,
    sizeBytes: typeof file.size === "number" ? file.size : undefined,
    file,
    uploading: true,
  };
  if (file.type.startsWith("image/")) att.previewUrl = URL.createObjectURL(file);
  return att;
}

// reconcileUploads stamps Core's answer onto the optimistic chips, in order:
// a stored file gets its id / workspace path / extraction status, a failed
// one keeps its chip with the reason (never silently dropped).
function reconcileUploads(pending: ChatAttachment[], result: UploadResult): ChatAttachment[] {
  const stored = [...result.ok];
  const failed = [...result.failed];
  return pending.map((att) => {
    const okIdx = stored.findIndex((u) => u.name === att.name);
    if (okIdx >= 0) {
      const [u] = stored.splice(okIdx, 1);
      return {
        ...att,
        id: u.id,
        mimeType: u.mime_type || att.mimeType,
        sizeBytes: u.size_bytes ?? att.sizeBytes,
        storagePath: u.storage_path || undefined,
        url: attachmentRawPath(u.id),
        extractStatus: u.extract_status,
        pageCount: u.page_count,
        uploading: false,
      };
    }
    const failIdx = failed.findIndex((f) => f.name === att.name);
    const reason = failIdx >= 0 ? failed.splice(failIdx, 1)[0].error : "upload failed";
    return { ...att, uploading: false, error: `Couldn't upload: ${reason}` };
  });
}

// attachedMarker mirrors Core's turnText: a file-only send still needs a
// non-empty user message, and both sides render the same marker so the
// optimistic bubble matches the persisted row.
function attachedMarker(files: File[]): string {
  return `(attached: ${files.map((f) => f.name || "attachment").join(", ")})`;
}

function revokeMessageAttachmentUrls(messages: ChatMessage[]) {
  for (const message of messages) {
    if (!Array.isArray(message.attachments)) continue;
    for (const att of message.attachments) {
      if (isBlobAttachmentPreview(att)) {
        URL.revokeObjectURL(att.previewUrl!);
      }
    }
  }
}

export function useChat() {
  const ws = useWebSocket();
  const searchParams = useSearchParams();
  const requestedSessionId = searchParams.get("session")?.trim() ?? "";
  // The conversation this hook has opened, and the one function that changes
  // it. Its only extra job over rememberSession is recording what is open, so
  // the mount effect can tell a `?session=` this hook wrote itself from one
  // that is genuinely asking for a different chat.
  const openSessionRef = useRef("");
  const initializedRef = useRef(false);
  const openConversation = useCallback((id: string) => {
    openSessionRef.current = id;
    rememberSession(id);
  }, []);
  // Empty on first server render; assigned client-side in useEffect to avoid
  // hydration mismatches from non-deterministic UUID generation.
  const [sessionId, setSessionId] = useState<string>("");
  const [messages, setMessages] = useState<ChatMessage[]>([]);

  // IS ANYTHING ACTUALLY CODING? Server state, so it survives a reload and a
  // core restart, which is the whole point: a forwarded step whose job died
  // has nobody left to close it, and this is what stops the row pretending
  // otherwise. `loading` counts as live, because the hook starts with an empty
  // array and treating that as "nothing is coding" would settle the steps of a
  // build that is running perfectly well.
  const { runs: liveCodingRuns, loading: codingLoading } = useCodingRuns({ runningOnly: true });
  const codingLive = codingLoading || liveCodingRuns.length > 0;
  const codingLiveRef = useRef(true);
  codingLiveRef.current = codingLive;

  // When the last coding job stops, close whatever it left forwarded but
  // unfinished. Server-side the job closes its own steps; this is the net for
  // when there is no server left to do it.
  //
  // IT TOUCHES NOTHING ELSE, and that is not a detail. The first version
  // reused settleInFlight, which also clears `pending` on the assistant
  // bubble — correct at the END of a turn, catastrophic in the MIDDLE of one.
  // This fires whenever a coding run stops, which routinely happens with the
  // turn still going: the bubble the deltas were streaming into stopped being
  // pending, the reply that arrived after it had nowhere to land, and the boss
  // watched a turn whose answer was already written in the database. "NOTHING
  // IN MY STREAM SAYS ANY OF THAT." A mid-turn effect may only ever touch the
  // rows it is actually about.
  useEffect(() => {
    if (codingLive) return;
    const now = Date.now();
    setMessages((prev) => settleNestedOnly(prev, now));
  }, [codingLive]);
  const [usage, setUsage] = useState<Usage>({ input: 0, output: 0 });
  const [isStreaming, setIsStreaming] = useState(false);
  // steal C: the reasoning-effort level Jarvis chose for the latest turn (from
  // the EventEffort frame). Drives the Composer chip's "Auto · <level>" display.
  // Per-turn, not durable.
  const [appliedEffort, setAppliedEffort] = useState<string>("");

  const turnStartRef = useRef<number | null>(null);
  // THE LIVENESS MACHINE (lib/chat/liveness.ts). The server says whether a
  // turn is alive and what it is doing (heartbeat, turn_status, seq on every
  // frame); this ref folds that in and decides the only three things the
  // client may do on its own: ask for a replay, rebuild the socket, or
  // rebuild from the transcript once the server has said the turn is over.
  // It replaces a 90-second "no frame → the agent went silent" watchdog that
  // painted an error and closed a healthy socket on a turn that was thinking.
  const livenessRef = useRef<LivenessState>(initialLiveness());
  const [liveness, setLivenessState] = useState<LivenessState>(livenessRef.current);
  const applyLiveness = useCallback((next: LivenessState) => {
    if (next === livenessRef.current) return;
    livenessRef.current = next;
    setLivenessState(next);
  }, []);
  // Discuss-with-Jarvis opens a session whose only row is a DashboardSeed
  // context block - no agent reply yet. seededKickRef parks such a session
  // id until the socket is connected enough to fire one `resume` turn so
  // the agent actually responds. kickedSessionsRef dedupes so a remount or
  // reconnect never re-fires the opening turn.
  const seededKickRef = useRef<string>("");
  const kickedSessionsRef = useRef<Set<string>>(new Set());

  // showLiveTurn puts the streaming affordances on screen for a turn the
  // server says is running: the working row, the Stop button, and a pending
  // "Thinking" row whose clock counts from when the turn ACTUALLY started,
  // not from when he came back to the tab.
  const showLiveTurn = useCallback((startedAt: number) => {
    turnStartRef.current = startedAt || Date.now();
    setIsStreaming(true);
    setMessages((prev) => {
      const tail = prev[prev.length - 1];
      // Something of this turn is already live on screen (a streaming
      // bubble, a running card): nothing to add.
      if (prev.some((m) => m.pending)) return prev;
      if (tail && tail.role === "thinking" && tail.pending) return prev;
      return [
        ...prev,
        {
          id: makeId(),
          role: "thinking",
          text: "",
          pending: true,
          createdAt: turnStartRef.current ?? Date.now(),
        } as ChatMessage,
      ];
    });
  }, []);

  // RESUMING A TURN THAT IS STILL GOING, without a server that can be asked.
  //
  // The live path is `attach`: the socket binds to the session and the server
  // answers with turn_status (and replays what was missed). This is the
  // fallback for a core that predates attach: it reads mem_turns over HTTP,
  // the same substrate /logs reads, and puts the affordances up if a turn is
  // in flight. It arms nothing and decides nothing about silence.
  const resumeLiveTurn = useCallback(
    async (id: string, signal?: AbortSignal) => {
      if (!id) return false;
      let live: TurnRowDTO | undefined;
      try {
        const rows = await fetchTraces({ sessionId: id, status: "in_flight", limit: 1 }, signal);
        live = rows?.[0];
      } catch {
        // A failed probe must never leave the page CLAIMING work is happening.
        return false;
      }
      if (!live) return false;
      const startedAt = Date.parse(live.started_at) || Date.now();
      showLiveTurn(startedAt);
      applyLiveness({ ...livenessRef.current, inFlight: true, startedAt, phase: livenessRef.current.phase ?? "thinking" });
      return true;
    },
    [showLiveTurn, applyLiveness],
  );

  // reconcileFromCore merges the canonical transcript into the local view.
  // MERGE ONLY: it never decides that a turn is over. That used to be
  // inferred from "the last row is an assistant row", which is false whenever
  // the tail is the interim narration Core persists before a tool call, so a
  // reconcile mid-turn silently killed the streaming UI. The server's
  // turn_status is the only thing that ends a turn now (settleTurn).
  const reconcileFromCore = useCallback(
    async (signal?: AbortSignal): Promise<boolean> => {
      if (!sessionId) return false;
      const rows = await fetchSessionMessages(sessionId, signal);
      if (!rows || rows.length === 0) return false;
      setMessages((prev) => {
        const next = mergeServerRows(prev, rows);
        const remoteTail = rowToMessage(rows[rows.length - 1]);
        for (let i = next.length - 1; i >= 0; i--) {
          const local = next[i];
          if (local.role !== remoteTail.role) continue;
          if (local.text !== remoteTail.text) continue;
          const mergedAttachments = mergeAttachmentLists(local.attachments, remoteTail.attachments);
          if (mergedAttachments !== local.attachments) {
            next[i] = { ...local, attachments: mergedAttachments };
          }
          break;
        }
        return next;
      });
      return true;
    },
    [sessionId],
  );

  // settleTurn closes everything a turn left open once the SERVER has said
  // the turn is over (turn_status in_flight:false after we were streaming,
  // or a legacy transcript poll). The transcript by then holds the final
  // reply, so live-only tails are dropped in favour of the persisted rows.
  const settleTurn = useCallback(
    async (stopReason?: string) => {
      try {
        await reconcileFromCore();
      } catch {
        /* Core may be waking; the merge is a nicety, the settle is not. */
      }
      const now = Date.now();
      setMessages((prev) => {
        const next = closePendingThinking(prev);
        while (next.length > 0) {
          const last = next[next.length - 1];
          if (last.role === "thinking" && last.pending) {
            next.pop();
            continue;
          }
          if (last.role === "assistant" && last.pending && !last.error && !last.text.trim()) {
            next.pop();
            continue;
          }
          break;
        }
        const settled = settleInFlight(next, now, codingLiveRef.current);
        if (stopReason === "interrupted") {
          for (let i = settled.length - 1; i >= 0; i--) {
            if (settled[i].role === "assistant") {
              settled[i] = { ...settled[i], interrupted: true };
              break;
            }
          }
        }
        return settled;
      });
      clearLiveTool();
      turnStartRef.current = null;
      setIsStreaming(false);
    },
    [reconcileFromCore],
  );

  // dispatchLiveness carries out what the machine decided. Three verbs, no
  // guessing: attach (ask the server what we missed), reconnect (the socket
  // is the problem), reconcile (the server said the turn is over).
  const dispatchLiveness = useCallback(
    (action: LivenessAction) => {
      switch (action.kind) {
        case "attach":
          if (sessionId) ws.send({ type: "attach", session_id: sessionId, since_seq: action.sinceSeq });
          break;
        case "reconnect":
          ws.reconnect();
          break;
        case "reconcile":
          void settleTurn(action.stopReason);
          break;
        default:
          break;
      }
    },
    [ws, sessionId, settleTurn],
  );
  const dispatchRef = useRef(dispatchLiveness);
  dispatchRef.current = dispatchLiveness;

  // The clock the machine runs on, only while a turn is in flight.
  useEffect(() => {
    if (!liveness.inFlight) return;
    const t = setInterval(() => {
      const r = livenessOnTick(livenessRef.current, Date.now());
      applyLiveness(r.state);
      if (r.action.kind !== "none") dispatchRef.current(r.action);
    }, LIVENESS_TICK_MS);
    return () => clearInterval(t);
  }, [liveness.inFlight, applyLiveness]);

  // Fire one `resume` turn for a freshly-seeded session so the agent
  // replies to the dashboard context Discuss-with-Jarvis injected (the
  // context block alone never triggers a turn). No-op once a session has
  // been kicked. If the socket isn't OPEN yet, the id is parked on
  // seededKickRef and the ws.status effect below retries on connect.
  const kickSeeded = useCallback(
    (target: string) => {
      if (!target || kickedSessionsRef.current.has(target)) return;
      const ok = ws.send({ type: "resume", session_id: target });
      if (!ok) {
        seededKickRef.current = target; // retry once connected
        return;
      }
      seededKickRef.current = "";
      kickedSessionsRef.current.add(target);
      turnStartRef.current = Date.now();
      setIsStreaming(true);
      applyLiveness(livenessOnSend(livenessRef.current, Date.now()));
      // Show the "Jarvis is thinking" indicator while the resume turn
      // spins up - same optimistic affordance a normal send() gives.
      setMessages((prev) =>
        prev.some((m) => m.role === "thinking" && m.pending)
          ? prev
          : [
              ...prev,
              { id: makeId(), role: "thinking", text: "", pending: true, createdAt: Date.now() },
            ],
      );
    },
    [ws, applyLiveness],
  );
  // Latest-callback ref so the session-load effect can kick a seeded
  // session without taking kickSeeded as a dependency (which would make
  // it re-fetch the transcript on every ws status change).
  const kickSeededRef = useRef(kickSeeded);
  useEffect(() => {
    kickSeededRef.current = kickSeeded;
  }, [kickSeeded]);

  // Retry a parked seeded-session kick once the socket reconnects.
  useEffect(() => {
    if (ws.status === "connected" && seededKickRef.current) {
      kickSeeded(seededKickRef.current);
    }
  }, [ws.status, kickSeeded]);

  // ATTACH: bind this socket to the conversation and ask what we missed.
  //
  // Sent on every connect (a reload, iOS killing the socket, a network flap,
  // wandering off to another page and back) and on every session switch,
  // with the newest seq we have. The server answers with turn_status - its
  // own word on whether a turn is running, since when, and in what phase -
  // and replays every journaled frame past that seq. So a socket that arrives
  // late is caught up instead of left to guess, and a reload mid-turn shows
  // "Thinking" counting from the turn's real start.
  const attachRef = useRef<(id: string) => void>(() => {});
  attachRef.current = (id: string) => {
    if (!id || ws.status !== "connected") return;
    const action = livenessOnConnected(livenessRef.current);
    ws.send({ type: "attach", session_id: id, since_seq: action.kind === "attach" ? action.sinceSeq : 0 });
  };
  // Legacy: a core that predates attach answers `unknown type: attach`, and
  // the machine flips to polling mem_turns over HTTP instead.
  const resumeRef = useRef(resumeLiveTurn);
  useEffect(() => {
    resumeRef.current = resumeLiveTurn;
  }, [resumeLiveTurn]);
  useEffect(() => {
    if (ws.status !== "connected" || !sessionId || isStreaming) return;
    if (liveness.attachSupported !== false) return;
    const ac = new AbortController();
    void resumeRef.current(sessionId, ac.signal);
    return () => ac.abort();
  }, [ws.status, sessionId, isStreaming, liveness.attachSupported]);

  // Restore session id from localStorage on mount; mint a fresh one if none.
  // Hydrate from the optimistic local cache *immediately* so a refresh -
  // even one while Core is offline - keeps the visible conversation. Then
  // ask Core for the canonical transcript and overwrite the local cache
  // with whatever Core returns. Core wins; local cache only fills the
  // gap when Core is unreachable. Will be replaced by Supabase Realtime
  // once auth is wired so multiple tabs / devices stay in sync.
  useEffect(() => {
    // Syncing the address bar (openConversation) changes the param this effect
    // depends on. Once we are running, a param naming the conversation already
    // open is one this hook wrote itself, not a request to switch - so return
    // rather than re-running the whole mount. Never on the first pass, which is
    // the one that has to honour a deep link.
    if (
      initializedRef.current &&
      requestedSessionId &&
      requestedSessionId === openSessionRef.current
    ) {
      return;
    }
    initializedRef.current = true;
    const stored = readStoredSessionId();
    let id = requestedSessionId || stored || newSessionId();
    // RESTORED means "put me back where I was", as against a deep link asking
    // for a specific conversation. A refresh is now the former WITH a param in
    // the URL (the address bar follows the open chat), and it is told apart by
    // the param agreeing with what was stored. Getting this wrong would have
    // quietly retired the rotation below - it used to key off "no param at
    // all", a condition that can no longer happen.
    const restored = isRestoredSession(requestedSessionId, stored);
    // Stale-session rotation: a restored (never an explicitly requested)
    // session idle past the window is a finished conversation - start fresh
    // so this exchange gets its own chat + title. Synchronous on purpose:
    // it must win the race against the ?voice=1 auto-start, which otherwise
    // pipes a whole voice conversation into the stale session.
    if (restored && stored) {
      const last = lastActivityFor(stored);
      if (last > 0 && Date.now() - last > STALE_SESSION_MS) {
        id = newSessionId();
      }
    }
    setSessionId(id);
    openConversation(id);

    const cached = readCachedMessages(id);
    if (cached.length > 0) {
      setMessages(cached);
    } else {
      setMessages([]);
    }

    const ac = new AbortController();
    fetchSessionMessages(id, ac.signal).then((rows) => {
      if (!rows) return;
      const fromServer: ChatMessage[] = rows.map(rowToMessage);
      setMessages((prev) => {
        // Preserve locally-rendered steered messages not yet reflected in
        // the server transcript. The hook pipeline is async (goroutine →
        // Postgres INSERT), so there is a small window between the client
        // sending a steer and the row landing in mem_observations. If the
        // user navigates away during that window and the mount fetch resolves
        // before the write commits, the message would otherwise vanish.
        // Keep any steered local bubbles whose text isn't in the server rows.
        const serverUserTexts = new Set(
          fromServer.filter((m) => m.role === "user").map((m) => m.text.trim()),
        );
        const serverErrors = new Set(
          fromServer.map((m) => (m.error ?? "").trim()).filter(Boolean),
        );
        // Same rule as the reconcile merge (survivesRefetch), so navigating
        // away and back cannot lose something a reconnect would have kept.
        // This path used to preserve steered messages ONLY, which is how an
        // error card and a mid-turn note both vanished on return.
        const keep = prev.filter((m) => {
          if (!survivesRefetch(m)) return false;
          const err = (m.error ?? "").trim();
          if (err) return !serverErrors.has(err);
          if (m.role === "user") return !serverUserTexts.has(m.text.trim());
          return true;
        });
        if (keep.length === 0) return fromServer;
        return [...fromServer, ...keep].sort((a, b) => a.createdAt - b.createdAt);
      });
      // Seeded session: fire the opening agent turn now that history is loaded.
      if (fromServer.length === 1 && fromServer[0].seeded) {
        kickSeededRef.current(id);
      }
    });
    // Messages are on screen; now bind the socket to this conversation and
    // ask whether a turn is still running (and what we missed of it).
    applyLiveness(livenessOnSessionSwitch(livenessRef.current));
    attachRef.current(id);
    return () => ac.abort();
  }, [requestedSessionId, openConversation, applyLiveness]);

  // Mirror the visible transcript into localStorage whenever it changes.
  // Pending / thinking messages are filtered out by writeCachedMessages
  // so the cache only contains finalized turns. The activity stamp feeds
  // the stale-session rotation.
  useEffect(() => {
    if (!sessionId) return;
    writeCachedMessages(sessionId, messages);
    if (messages.length > 0) writeLastActiveAt(Date.now());
  }, [sessionId, messages]);

  useEffect(() => {
    return () => revokeMessageAttachmentUrls(messages);
  }, [messages]);

  // Browser lifecycle rehydration. Some mobile/PWA resumes keep the WebSocket
  // object in OPEN state even though the stream died while the app was
  // BACKGROUNDED; in that case ws.status never changes and the reconnect-only
  // effect below would not run — so on returning from a real background we force
  // a fresh socket + reconcile against Core's persisted transcript.
  //
  // CRITICAL: only do this when the page actually went HIDDEN. Switching to
  // another desktop app leaves Studio's tab "visible" and its socket alive and
  // streaming — it only fires a window `focus`, not a visibilitychange. Forcing
  // a reconnect + full transcript reconcile on that plain focus is exactly the
  // jarring "it sits there, then a thousand things move the moment I click it"
  // the boss hates: the updates were arriving live the whole time, and the
  // reconcile just re-slams the entire conversation at once. If we never went
  // hidden, do NOTHING — let the live stream keep flowing.
  const wasHiddenRef = useRef(false);
  // Refs for the foreground handler below - it must read the CURRENT
  // transcript/streaming state without re-binding listeners on every render.
  const messagesLenRef = useRef(0);
  messagesLenRef.current = messages.length;
  const isStreamingRef = useRef(false);
  isStreamingRef.current = isStreaming;

  // rotateToFreshSession is the resume-time half of stale-session rotation
  // (the mount path handles reloads). Unlike newSession it does NOT wipe the
  // previous session's cache - that conversation is finished, not discarded.
  const rotateToFreshSession = useCallback(() => {
    applyLiveness(livenessOnSessionSwitch(livenessRef.current));
    const id = newSessionId();
    setSessionId(id);
    openConversation(id);
    setMessages([]);
    turnStartRef.current = null;
    setIsStreaming(false);
    writeLastActiveAt(Date.now()); // the fresh session is "active now"
    attachRef.current(id);
  }, [applyLiveness, openConversation]);

  useEffect(() => {
    if (!sessionId) return;
    const onHide = () => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") {
        wasHiddenRef.current = true;
      }
    };
    const onForeground = () => {
      if (typeof document !== "undefined" && document.visibilityState !== "visible") return;
      if (!wasHiddenRef.current) return; // plain blur/focus — socket stayed live
      wasHiddenRef.current = false;
      // Stale-session rotation on resume: an installed PWA can stay mounted
      // for days, so the mount-time staleness check never re-runs. Returning
      // to a conversation idle past the window starts a fresh chat (with its
      // own title) instead of resuming it - never for explicitly requested
      // sessions, never mid-stream, never when there's nothing to rotate from.
      if (!requestedSessionId && !isStreamingRef.current && messagesLenRef.current > 0) {
        const last = readLastActiveAt();
        if (last > 0 && Date.now() - last > STALE_SESSION_MS) {
          rotateToFreshSession();
          return; // nothing to reconcile - the fresh session is empty
        }
      }
      // The socket itself is the WS provider's business: it reconnects a
      // dead one on this same event, and a live one is left alone (tearing
      // down a healthy socket here used to drop the rest of the turn). We
      // just merge what landed while hidden and re-ask the server where the
      // turn is; the liveness machine rebuilds the socket only if the server
      // stays silent.
      void reconcileFromCore().catch(() => {
        /* Core may still be waking; WS reconnect will retry independently. */
      });
      attachRef.current(sessionId);
    };
    document.addEventListener("visibilitychange", onHide);
    window.addEventListener("pageshow", onForeground);
    window.addEventListener("focus", onForeground);
    document.addEventListener("visibilitychange", onForeground);
    return () => {
      document.removeEventListener("visibilitychange", onHide);
      window.removeEventListener("pageshow", onForeground);
      window.removeEventListener("focus", onForeground);
      document.removeEventListener("visibilitychange", onForeground);
    };
  }, [sessionId, ws, reconcileFromCore, requestedSessionId, rotateToFreshSession]);

  // WS-reconnect rehydration. iOS Safari (and any flaky network) can kill
  // the WebSocket mid-turn. The turn keeps running server-side; on every
  // connect transition we attach (which binds the socket and replays every
  // frame we missed, completion included) and merge whatever the transcript
  // has. Nothing here decides the turn is over; the server's turn_status
  // does, in the frame handler below.
  const lastStatusRef = useRef<typeof ws.status>(ws.status);
  useEffect(() => {
    const prevStatus = lastStatusRef.current;
    lastStatusRef.current = ws.status;
    if (ws.status !== "connected") return;
    if (prevStatus === "connected") return; // initial mount is covered by the session-load effect
    if (!sessionId) return;
    attachRef.current(sessionId);
    const ac = new AbortController();
    void reconcileFromCore(ac.signal).catch(() => {
      /* transient fetch failure; socket retry path remains active */
    });
    return () => ac.abort();
  }, [ws.status, sessionId, reconcileFromCore]);

  useEffect(() => {
    return ws.subscribe((ev: WSEvent) => {
      if ("session_id" in ev && ev.session_id && ev.session_id !== sessionId) return;

      // Every frame goes through the liveness machine first: it resets the
      // silence clock, spots a seq gap, adopts the server's phase and start
      // time, and flags a replayed frame the transcript already holds.
      const lv = livenessOnFrame(livenessRef.current, ev, Date.now());
      applyLiveness(lv.state);
      if (lv.action.kind !== "none") dispatchRef.current(lv.action);
      if (lv.duplicate) return;
      const turnId = "turn_id" in ev && typeof ev.turn_id === "string" ? ev.turn_id : undefined;

      switch (ev.type) {
        case "turn_status":
        case "heartbeat": {
          // The server's own word on the turn. In flight → the streaming
          // affordances go up (idempotent), counting from its real start.
          // Not in flight while we were streaming → the machine already
          // dispatched a reconcile, which settles from the transcript.
          const st = ev.turn_status;
          if (st.in_flight) {
            const startedAt = st.started_at ? Date.parse(st.started_at) : NaN;
            if (!isStreamingRef.current) {
              showLiveTurn(Number.isFinite(startedAt) ? startedAt : Date.now());
            } else if (Number.isFinite(startedAt) && startedAt > 0) {
              turnStartRef.current = startedAt;
            }
            if (typeof st.thinking_tokens === "number" && st.thinking_tokens > 0) {
              const tokens = st.thinking_tokens;
              setMessages((prev) => {
                const i = prev.findIndex((m) => m.role === "thinking" && m.pending);
                if (i === -1 || (prev[i].thinkingTokens ?? 0) >= tokens) return prev;
                const next = [...prev];
                next[i] = { ...next[i], thinkingTokens: tokens };
                return next;
              });
            }
          }
          break;
        }
        case "thinking": {
          setMessages((prev) => {
            const next = [...prev];
            const last = next[next.length - 1];
            if (last && last.role === "thinking" && last.pending) {
              next[next.length - 1] = {
                ...last,
                text: last.text + (ev.text ?? ""),
                turnId: last.turnId ?? turnId,
                // The count only ever grows within a turn; never let a late
                // frame walk it backwards.
                thinkingTokens: Math.max(last.thinkingTokens ?? 0, ev.thinking_tokens ?? 0) || undefined,
              };
            } else {
              next.push({
                id: makeId(),
                role: "thinking",
                text: ev.text ?? "",
                thinkingTokens: ev.thinking_tokens,
                pending: true,
                turnId,
                createdAt: Date.now(),
              });
            }
            return next;
          });
          break;
        }
        case "delta": {
          setMessages((prev) => {
            const next = closePendingThinking(prev);
            // The bubble this delta belongs to is the pending assistant
            // bubble of ITS turn, wherever it sits - a nested coding step
            // or a settled row may have landed after it. Only without a
            // turn id (an older core) does "the tail" have to do.
            let at = -1;
            if (turnId) {
              for (let i = next.length - 1; i >= 0; i--) {
                const m = next[i];
                if (m.role === "assistant" && m.pending && m.turnId === turnId) {
                  at = i;
                  break;
                }
                if (m.role === "user" && !m.steered) break;
              }
            } else {
              const last = next[next.length - 1];
              if (last && last.role === "assistant" && last.pending) at = next.length - 1;
            }
            if (at === -1) {
              next.push({
                id: makeId(),
                role: "assistant",
                text: ev.text,
                pending: true,
                turnId,
                createdAt: Date.now(),
              });
            } else {
              next[at] = { ...next[at], text: next[at].text + ev.text };
            }
            return next;
          });
          break;
        }
        case "tool_call": {
          // A NESTED step is a coding job reporting from inside its own run —
          // Claude Code on the Mac, or the settings model in the cloud
          // workspace. Those keep arriving for as long as the build lasts,
          // which is routinely long after the reply landed; the liveness
          // machine already ignores them for this turn's phase.
          const nested = !!ev.tool_call.nested;
          // Tell the status chrome (bridge pill) what is in flight this
          // second, so it can flash the model doing the coding.
          if (ev.tool_call.id && ev.tool_call.name) {
            publishLiveTool({ id: ev.tool_call.id, name: ev.tool_call.name, startedAt: Date.now() });
          }
          setMessages((prev0) => {
            // The narration streamed just before this call is interim, not
            // the reply: mark it so the stream folds it with the tool cards.
            //
            // NEVER for a nested step. A build reports for another forty
            // minutes after Jarvis has answered, and re-marking on each one
            // would fold his actual reply away into the ledger — the boss
            // would watch his answer vanish while the job kept working.
            const prev = nested ? prev0 : markTrailingAssistantInterim(prev0);
            // Upsert by tool_call.id - NEVER blind-append. The same tool_call
            // frame can reach this handler more than once during a long
            // blocking call (iOS Safari focus/visibility churn -> reconnect ->
            // reconcile interleave). A makeId() push rendered the SAME in-flight
            // call as dozens of identical spinning cards - the "cron_run_now
            // storm" that was actually ONE call (verified: mem_predictions
            // recorded cron_run_now exactly once). Keyed upsert makes phantom
            // duplicates structurally impossible.
            const id = ev.tool_call.id;
            const existing = id
              ? prev.findIndex((m) => m.role === "tool" && m.toolCall?.id === id)
              : -1;
            if (existing !== -1) {
              const next = [...prev];
              const done = !!next[existing].toolResult;
              next[existing] = {
                ...next[existing],
                // A card whose result already landed keeps its call as it
                // was: a REPLAYED call frame (awaiting_approval and all)
                // must not reopen a decision the boss already made.
                toolCall: done ? next[existing].toolCall : ev.tool_call,
                turnId: next[existing].turnId ?? turnId,
                // Keep a resolved card resolved if its result already landed
                // (out-of-order frames); otherwise it's still in flight.
                pending: !done,
              };
              return next;
            }
            return [
              ...closePendingThinking(prev),
              {
                id: makeId(),
                role: "tool",
                text: "",
                toolCall: ev.tool_call,
                pending: true,
                turnId,
                createdAt: Date.now(),
              },
            ];
          });
          break;
        }
        case "tool_result": {
          clearLiveTool(ev.tool_result.id);
          setMessages((prev) => {
            const id = ev.tool_result.id;
            const at = prev.findIndex((m) => m.role === "tool" && m.toolCall?.id === id);
            if (at !== -1) {
              const next = [...prev];
              next[at] = { ...next[at], toolResult: ev.tool_result, pending: false };
              return next;
            }
            // A result whose call frame never reached us (it was shed on a
            // slow socket, or the replay floor moved past it) still renders
            // as a finished card rather than vanishing.
            return [
              ...closePendingThinking(prev),
              {
                id: makeId(),
                role: "tool",
                text: "",
                toolCall: { id, name: ev.tool_result.name, nested: ev.tool_result.nested },
                toolResult: ev.tool_result,
                turnId,
                createdAt: Date.now(),
              },
            ];
          });
          break;
        }
        case "complete": {
          clearLiveTool();
          const inputT = ev.usage?.input ?? 0;
          const outputT = ev.usage?.output ?? 0;
          setUsage((u) => ({ input: u.input + inputT, output: u.output + outputT }));
          const latency = turnStartRef.current ? Date.now() - turnStartRef.current : undefined;
          const interrupted = ev.stop_reason === "interrupted";
          setMessages((prev) => {
            const next = closePendingThinking(prev);
            // Find the most recent pending assistant message and finalize
            // it. When `interrupted`, the partial text already streamed
            // becomes the canonical reply and we mark the bubble so the
            // UI can render a subtle "↩ interrupted" hint. No error state.
            let finalized = false;
            for (let i = next.length - 1; i >= 0; i--) {
              if (next[i].role === "assistant" && next[i].pending) {
                next[i] = {
                  ...next[i],
                  pending: false,
                  inputTokens: inputT,
                  outputTokens: outputT,
                  latencyMs: latency,
                  interrupted: interrupted || undefined,
                };
                finalized = true;
                break;
              }
            }
            // If the user interrupted before any assistant text streamed
            // (stop pressed during pure thinking) there's no pending
            // assistant bubble to finalize. Insert a small marker so the
            // transcript still reflects that the turn ended.
            if (!finalized && interrupted) {
              next.push({
                id: makeId(),
                role: "assistant",
                text: "",
                interrupted: true,
                createdAt: Date.now(),
              });
            }
            // Nothing stays "running" once the turn is over: interim assistant
            // bubbles close and result-less tool cards stop ticking.
            for (let i = 0; i < next.length; i++)
              next[i] = settleInFlight([next[i]], Date.now(), codingLiveRef.current)[0];
            // Silent-turn rescue: when a turn completes with no
            // visible assistant text, surface a clear marker so the
            // chat never just stops mid-air. Phrase the marker
            // honestly based on what we know:
            //  - had tool messages in this turn → tools ran, model
            //    just didn't follow up with prose. Asking "what did
            //    you find?" continues naturally.
            //  - output_tokens === 0 → model emitted literally
            //    nothing. Refusal, transient API issue, or upstream
            //    interrupt. Rephrasing usually unsticks it.
            //  - otherwise → generic, with the usage numbers so the
            //    pattern is debuggable if it recurs.
            if (!interrupted) {
              // Did he get a reply THIS TURN? Walk back to the user message
              // that started it and look for any assistant message carrying
              // text, rather than only checking the very last row.
              //
              // Checking only the last row meant a single tool row arriving
              // after the answer turned a finished turn into "Jarvis didn't
              // follow up with a reply", with the reply sitting directly
              // above it. Whether a step is recorded after the words is a
              // detail of how a brain streams; whether he was answered is
              // not, so it is not decided by ordering.
              const last = next[next.length - 1];
              let hasAssistantText = false;
              for (let i = next.length - 1; i >= 0; i--) {
                const m = next[i];
                if (m.role === "user") break;
                if (m.role === "assistant" && m.text.trim()) {
                  hasAssistantText = true;
                  break;
                }
              }
              if (!hasAssistantText) {
                // Did this turn produce any tool activity? Walk back
                // from the end until we hit the user message that
                // started the turn; any tool entries we cross mean
                // tools did run.
                let toolsRanThisTurn = false;
                for (let i = next.length - 1; i >= 0; i--) {
                  const m = next[i];
                  if (m.role === "user") break;
                  if (m.role === "tool") {
                    toolsRanThisTurn = true;
                    break;
                  }
                }
                let errorText: string;
                if (toolsRanThisTurn) {
                  errorText =
                    "Tools ran above but Jarvis didn't follow up with a reply. Ask \"what did you find?\" to continue.";
                } else if (outputT === 0) {
                  errorText =
                    "Jarvis didn't emit anything this turn (0 output tokens) - likely a transient API hiccup or a soft refusal. Try rephrasing.";
                } else {
                  errorText = `Turn ended without a visible reply (${outputT} output tokens). Ask a follow-up to continue.`;
                }
                const placeholder: ChatMessage = {
                  id: makeId(),
                  role: "assistant",
                  text: "",
                  error: errorText,
                  inputTokens: inputT,
                  outputTokens: outputT,
                  latencyMs: latency,
                  createdAt: Date.now(),
                };
                if (
                  last &&
                  last.role === "assistant" &&
                  !last.text.trim() &&
                  !last.error
                ) {
                  // Reuse the empty finalized bubble in place rather
                  // than stacking a sibling marker.
                  next[next.length - 1] = { ...last, ...placeholder, id: last.id };
                } else {
                  next.push(placeholder);
                }
              }
            }
            return next;
          });
          turnStartRef.current = null;
          setIsStreaming(false);
          break;
        }
        case "steer_received": {
          // Echo for steered input. The originating tab already rendered
          // the bubble optimistically; this exists for multi-tab parity
          // and reconnect cases. Dedup is best-effort: if the most recent
          // user message has the same text and was marked steered, drop.
          // Reconciliation is a MECHANIC, so it lives in a pure tested
          // function rather than inline here: the client and the server can
          // legitimately disagree about whether a send was a message or a
          // steer, and the old inline dedupe only handled the one case that
          // could never happen in that race, so it drew the boss's message
          // twice. See lib/chat/steer.ts.
          setMessages((prev) => reconcileSteerEcho(prev, ev.text, makeId()));
          break;
        }
        case "error": {
          clearLiveTool();
          setMessages((prev) => [
            ...settleInFlight(closePendingThinking(prev), Date.now(), codingLiveRef.current),
            {
              id: makeId(),
              role: "assistant",
              text: "",
              error: ev.message,
              createdAt: Date.now(),
            },
          ]);
          turnStartRef.current = null;
          setIsStreaming(false);
          break;
        }
        case "cleared":
          break;
        case "pong":
          break;
        case "intent": {
          /* IntentFlow classification (incl. the consent stance) is consumed
           * server-side and by the IntentStream panel via its own fetch path.
           * Nothing to render in the transcript. */
          break;
        }
        case "gauge": {
          /* Effort sizing (glance/standard/deep) - like intent, surfaced by
           * the Intent/effort panel via its own DB fetch path, not the chat
           * transcript. Acknowledged for switch exhaustiveness. */
          break;
        }
        case "effort": {
          /* steal C: the per-turn reasoning level Jarvis chose for this turn.
           * Surface it to the Composer chip ("Auto · <level>") so the boss sees
           * how hard it's thinking. */
          // Always reflect THIS turn's level (empty = omit/model-default ->
          // chip shows "Auto"), so a prior turn's level can't go stale.
          setAppliedEffort(ev.effort?.level ?? "");
          break;
        }
        case "proactive_message": {
          /* Unprompted assistant turn pushed by the heartbeat. Render it
           * as a regular assistant bubble so the transcript reads
           * naturally - the `proactive` flag lets the bubble surface a
           * subtle origin badge ("heartbeat: surprise", etc.) without
           * altering the conversation flow.
           *
           * Actionable system findings should not pollute the live chat
           * transcript. They belong in dashboard / heartbeat surfaces
           * unless they are genuinely conversational. */
          if (ev.finding_kind === "surprise" && !ev.curiosity_id) {
            break;
          }
          if (ev.finding_kind === "background_build_progress") {
            // Background progress lives ONLY in the pinned dock
            // (BackgroundJobDock, mem_runs-backed). We deliberately do NOT
            // inject it into the chat transcript — the dock is the single live
            // surface so the boss never has to scroll up through the chat to
            // find a job's status. The "kicking off" message the boss DOES see
            // is the tool's normal return text, not this event.
            break;
          }
          // An unprompted message says nothing about the turn in flight (if
          // any); it must never touch the liveness of one.
          setMessages((prev) => [
            ...closePendingThinking(prev),
            {
              id: makeId(),
              role: "assistant",
              text: ev.text,
              proactive: true,
              proactiveKind: ev.finding_kind,
              curiosityId: ev.curiosity_id,
              runId: ev.run_id,
              progress: ev.progress,
              createdAt: Date.now(),
            },
          ]);
          break;
        }
        default:
          break;
      }
    });
  }, [ws, sessionId, applyLiveness, showLiveTurn]);

  const send = useCallback(
    async (content: string, files?: File[], opts?: { voice?: boolean; effort?: string }) => {
      const trimmed = content.trim();
      const localFiles = files ?? [];
      if ((!trimmed && localFiles.length === 0) || !sessionId) return false;

      // Mid-turn steering. When a turn is already in flight, send the
      // input as `steer` instead of `message` - the server drops it into
      // the running agent loop's steer channel and the loop drains it
      // between iterations. We render the user bubble optimistically
      // with `steered: true` so the transcript distinguishes it. The
      // model in use is resolved server-side from the settings store
      // (not from the WS frame) so there's a single source of truth.
      // Decided up-front (before any upload) so a file dropped mid-turn is
      // routed the way the boss saw it; the server tolerates a stale choice
      // by auto-routing message→steer and steer→message.
      const steering = isStreamingRef.current;
      const bubbleId = makeId();
      const pending = localFiles.map(fileToLocalAttachment);
      const bubbleText = trimmed || attachedMarker(localFiles);
      setMessages((prev) => [
        ...prev,
        {
          id: bubbleId,
          role: "user",
          text: bubbleText,
          attachments: pending.length > 0 ? pending : undefined,
          steered: steering || undefined,
          createdAt: Date.now(),
        },
        // Optimistic "Jarvis is thinking" indicator (fresh turns only). Closes
        // on first delta / tool_call / complete. Hidden if it ends up empty.
        ...(steering
          ? []
          : [{ id: makeId(), role: "thinking" as const, text: "", pending: true, createdAt: Date.now() }]),
      ]);

      // Files go to Core FIRST (multipart); the WS frame only references the
      // ids. Core turns them into native image / PDF blocks for the brain.
      let usableAttachments: ChatAttachment[] = [];
      if (pending.length > 0) {
        const result = await uploadAttachments(sessionId, localFiles);
        const reconciled = reconcileUploads(pending, result);
        setMessages((prev) => prev.map((m) => (m.id === bubbleId ? { ...m, attachments: reconciled } : m)));
        usableAttachments = reconciled.filter((a) => !!a.id);
        if (usableAttachments.length === 0 && !trimmed) {
          const reason = result.failed[0]?.error || "upload failed";
          setMessages((prev) => [
            ...prev.filter((m) => !(m.role === "thinking" && m.pending)),
            {
              id: makeId(),
              role: "assistant",
              text: "",
              error: `I couldn't take that file: ${reason}`,
              createdAt: Date.now(),
            },
          ]);
          return false;
        }
      }

      if (steering) {
        const ok = ws.send({
          type: "steer",
          session_id: sessionId,
          content: trimmed,
          attachments: usableAttachments.map(toSendAttachment),
          // steal C: carry the boss's effort pin on the steer too, so mid-turn
          // input keeps the same thinking level. "auto" omits (let C decide).
          ...(opts?.effort && opts.effort !== "auto" ? { effort: opts.effort } : {}),
        });
        if (!ok) {
          setMessages((prev) => [
            ...prev,
            {
              id: makeId(),
              role: "assistant",
              text: "",
              error: "Steer dropped - connection is reconnecting. Try again.",
              createdAt: Date.now(),
            },
          ]);
        }
        return ok;
      }

      turnStartRef.current = Date.now();
      setIsStreaming(true);
      applyLiveness(livenessOnSend(livenessRef.current, Date.now()));
      const ok = ws.send({
        type: "message",
        session_id: sessionId,
        content: trimmed,
        attachments: usableAttachments.map(toSendAttachment),
        // Voice turns run the SAME Loop.Run as text; the flag just tells Core
        // to also speak the reply (TTS) and add the spoken-delivery overlay.
        ...(opts?.voice ? { voice: true } : {}),
        // steal C: the boss's per-turn effort pin. "auto" / undefined omits so
        // the effort router decides; a level pins it for this turn.
        ...(opts?.effort && opts.effort !== "auto" ? { effort: opts.effort } : {}),
      });
      if (!ok) {
        applyLiveness(livenessOnSessionSwitch(livenessRef.current));
        setIsStreaming(false);
        setMessages((prev) => [
          ...prev,
          {
            id: makeId(),
            role: "assistant",
            text: "",
            error: "Not connected to core. Tap reconnect in the footer.",
            createdAt: Date.now(),
          },
        ]);
      }
      return ok;
    },
    [ws, sessionId, applyLiveness],
  );

  // interrupt asks the server to stop the turn. It ALWAYS answers: with
  // complete{stop_reason:"interrupted"} once the loop unwinds, or with a
  // turn_status saying nothing was running - so Stop can never be pressed
  // into silence, whatever the client thought was going on. The label reads
  // "Stopping…" until that answer lands; if none does within STOP_ACK_MS the
  // machine re-attaches and takes the server's word from there.
  const interrupt = useCallback(() => {
    if (!sessionId) return false;
    applyLiveness(livenessOnStop(livenessRef.current));
    const ok = ws.send({ type: "interrupt", session_id: sessionId });
    setTimeout(() => {
      if (livenessRef.current.stopping) attachRef.current(sessionId);
    }, STOP_ACK_MS);
    return ok;
  }, [ws, sessionId, applyLiveness]);

  const newSession = useCallback(() => {
    applyLiveness(livenessOnSessionSwitch(livenessRef.current));
    // Drop the previous session's cache too - `/new` is a deliberate
    // reset, the user doesn't want it lingering in storage.
    setSessionId((prev) => {
      if (prev) writeCachedMessages(prev, []);
      return prev;
    });
    const id = newSessionId();
    setSessionId(id);
    openConversation(id);
    setMessages([]);
    turnStartRef.current = null;
    setIsStreaming(false);
    attachRef.current(id);
  }, [applyLiveness, openConversation]);

  // switchSession loads an existing session in place - same view, different
  // conversation. Used by the Sessions drawer in the Live header. We hydrate
  // from Core's authoritative transcript and fall back to localStorage only
  // when Core is unreachable.
  const switchSession = useCallback((id: string) => {
    if (!id || id === sessionId) return;
    applyLiveness(livenessOnSessionSwitch(livenessRef.current));
    setSessionId(id);
    openConversation(id);
    setIsStreaming(false);
    turnStartRef.current = null;

    const cached = readCachedMessages(id);
    setMessages(cached);

    const ac = new AbortController();
    fetchSessionMessages(id, ac.signal).then((rows) => {
      if (!rows) return;
      const restored: ChatMessage[] = rows.map(rowToMessage);
      setMessages(restored);
      writeCachedMessages(id, restored);
      // Same seeded-session kick as the mount path: switching into a
      // Discuss-with-Jarvis session that has only the context block and
      // no reply should fire the opening turn.
      if (restored.length === 1 && restored[0].seeded) {
        kickSeededRef.current(id);
      }
    });
    // Messages are on screen; bind the socket to this conversation and ask
    // whether a turn is still running (and what we missed of it).
    attachRef.current(id);
  }, [sessionId, applyLiveness, openConversation]);

  const clear = useCallback(() => {
    applyLiveness(livenessOnSessionSwitch(livenessRef.current));
    ws.send({ type: "clear", session_id: sessionId });
    setMessages([]);
    if (sessionId) writeCachedMessages(sessionId, []);
  }, [ws, sessionId, applyLiveness]);

  // ── Voice integration ──────────────────────────────────────────────────
  //
  // Voice mode (OpenAI Realtime over WebRTC) streams transcripts on the
  // browser side instead of going through the WS turn pipeline. These
  // hooks let the voice client push final user utterances and live
  // assistant deltas straight into the same `messages` array text mode
  // populates - so the conversation reads as one continuous thread,
  // regardless of which modality each turn arrived through.
  //
  // The /api/voice/turn POST still fires on the Core side for memory
  // capture + cross-tab durability; these methods only mirror the same
  // data into the local view for live UX.

  /** Append a finalised user utterance from voice mode. */
  const addVoiceUserMessage = useCallback((text: string) => {
    const trimmed = text.trim();
    if (!trimmed) return;
    const userMessage: ChatMessage = {
      id: makeId(),
      role: "user",
      text: trimmed,
      createdAt: Date.now(),
    };
    setMessages((prev) => {
      const next = [...prev];
      const pendingAssistantIdx = findLatestPendingAssistant(next);
      if (pendingAssistantIdx >= 0) {
        next.splice(pendingAssistantIdx, 0, userMessage);
        return next;
      }
      next.push(userMessage);
      return next;
    });
  }, []);

  /** Stream an assistant delta from voice. Creates a pending assistant
   *  message on the first delta of a response and appends to it on
   *  subsequent deltas. On `isFinal`, the message is committed (loses
   *  its pending flag) so the cache picks it up.
   *
   *  Important: when `isFinal` is true, `delta` is the COMPLETE final
   *  transcript (not an incremental chunk). We replace the bubble's
   *  text wholesale instead of concatenating - otherwise the final
   *  transcript gets appended to the already-accumulated streamed
   *  text, producing duplicated "X X" bubbles. */
  const streamVoiceAssistantDelta = useCallback((event: AssistantTranscriptEvent) => {
    const text = event.text;
    const finalText = text.trim();
    if (!text && !event.isFinal) return;
    setMessages((prev) => {
      const next = [...prev];
      const pendingAssistantIdx = findLatestPendingAssistant(next);
      if (pendingAssistantIdx >= 0) {
        const pending = next[pendingAssistantIdx];
        if (pending.voiceResponseId && pending.voiceResponseId !== event.responseId) {
          next[pendingAssistantIdx] = { ...pending, pending: false };
        } else if ((pending.voiceLastSequence ?? 0) >= event.sequence) {
          return next;
        } else {
          next[pendingAssistantIdx] = {
            ...pending,
            text: event.isFinal ? (finalText || pending.text) : pending.text + text,
            pending: !event.isFinal,
            voiceResponseId: event.responseId,
            voiceLastSequence: event.sequence,
            voiceTranscriptSource: event.source,
          };
          return next;
        }
      }
      for (let i = next.length - 1, seen = 0; i >= 0 && seen < 8; i--) {
        if (next[i].role !== "assistant") continue;
        seen++;
        if (next[i].voiceResponseId === event.responseId) return next;
      }
      if (event.isFinal) {
        for (let i = next.length - 1, seen = 0; i >= 0 && seen < 8; i--) {
          if (next[i].role !== "assistant") continue;
          seen++;
          if (isDuplicateVoiceAssistantText(next[i].text, finalText)) return next;
        }
      }
      // No matching in-flight assistant bubble - start one. For the
      // final-only case (no preceding deltas) this captures the full
      // provider audio transcript in a single committed message.
      next.push({
        id: makeId(),
        role: "assistant",
        text: event.isFinal ? finalText : text,
        pending: !event.isFinal,
        createdAt: Date.now(),
        voiceResponseId: event.responseId,
        voiceLastSequence: event.sequence,
        voiceTranscriptSource: event.source,
      });
      return next;
    });
  }, []);

  const status = ws.status;

  return useMemo(
    () => ({
      sessionId,
      messages,
      usage,
      isStreaming,
      // liveness is what the working row reads: the server-reported phase,
      // the tool in flight, the turn's real start, and whether we are
      // reconnecting or stopping.
      liveness,
      appliedEffort,
      send,
      interrupt,
      newSession,
      switchSession,
      clear,
      status,
      addVoiceUserMessage,
      streamVoiceAssistantDelta,
    }),
    [
      sessionId,
      messages,
      usage,
      isStreaming,
      liveness,
      appliedEffort,
      send,
      interrupt,
      newSession,
      switchSession,
      clear,
      status,
      addVoiceUserMessage,
      streamVoiceAssistantDelta,
    ],
  );
}
