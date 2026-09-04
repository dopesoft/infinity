/**
 * transcript.ts - merging what the browser is showing with what the server
 * has written down, BY IDENTITY.
 *
 * PURE, and in `lib/chat` rather than inside `useChat`, for the same reason
 * `liveness.ts`, `settle.ts` and `preserve.ts` are: no React, no timers, so
 * the rules can be tested in plain node instead of asserted in a comment.
 *
 * WHAT THIS REPLACES. The old merge compared TEXT: a pending bubble was
 * dropped when a server row "started with" it, a steered message was kept
 * when no server row had the same words, a reply was matched to its row by
 * being the last row of its role. Every one of those was a guess, and each
 * produced a bug the boss saw: a reply that vanished because an earlier row
 * happened to start the same way, the same message drawn twice, a live
 * bubble eaten by a reconcile mid-stream.
 *
 * Now every row has a name, and the server hands the same name back:
 *
 *   • a tool card is `tool:<tool_call_id>`;
 *   • an assistant message is `reply:<turn_id>:<message_index>` - the loop
 *     numbers the messages of a turn, every delta carries the number of the
 *     message it belongs to, and the persisted row carries the same number;
 *   • a user message is `user:<client_id>` - the browser mints the id, it
 *     rides the `message`/`steer` frame, and the transcript hands it back;
 *   • anything else the server holds is `row:<observation id>`.
 *
 * A local row with a name is matched to the server row with the same name,
 * or kept as it is until the server has it. A local row with NO name (an old
 * cache, a core that predates the ids) is the one place the old text rule
 * still applies, and only as an exact match: it can no longer eat a reply.
 */

import type { ChatAttachment, ChatMessage, ChatRole } from "@/hooks/useChat";
import type { SessionMessageDTO } from "@/lib/api";
import { attachmentRawPath } from "../attachmentPath";
import { survivesRefetch } from "./preserve";
import { toolRowToMessage } from "./toolRow";

/** The server's transcript row, as GET /api/sessions/{id}/messages returns it. */
export type TranscriptRow = SessionMessageDTO;

/**
 * messageKey names a message so the same message on both sides is matched
 * by identity. null when the row has no identity the server could share.
 */
export function messageKey(m: ChatMessage): string | null {
  if (m.role === "tool") return m.toolCall?.id ? `tool:${m.toolCall.id}` : null;
  if (m.role === "assistant" && m.turnId && typeof m.msgIndex === "number") {
    return `reply:${m.turnId}:${m.msgIndex}`;
  }
  if (m.role === "user" && m.clientId) return `user:${m.clientId}`;
  if (m.serverId) return `row:${m.serverId}`;
  return null;
}

function rowAttachmentsToChat(atts?: TranscriptRow["attachments"]): ChatAttachment[] | undefined {
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

/** Both sides' attachment chips, without drawing one twice. */
export function mergeAttachmentLists(
  local?: ChatAttachment[],
  remote?: ChatAttachment[],
): ChatAttachment[] | undefined {
  const merged = dedupeAttachments([...(local ?? []), ...(remote ?? [])]);
  return merged.length > 0 ? merged : undefined;
}

/**
 * rowToMessage converts a server transcript row into the local ChatMessage
 * shape. The one mapping for the mount fetch, the switch-session fetch and
 * the reconnect merge, so they cannot disagree about a row.
 */
export function rowToMessage(r: TranscriptRow, makeId: () => string): ChatMessage {
  const createdAt = new Date(r.created_at).getTime() || Date.now();
  const identity = {
    serverId: r.id || undefined,
    turnId: r.turn_id || undefined,
    msgIndex: typeof r.message_index === "number" ? r.message_index : undefined,
    clientId: r.client_id || undefined,
  };
  // A persisted tool card, in the state it was in live (running / stopped /
  // done) - see toolRow.ts.
  if (r.role === "tool" && r.tool_call_id) {
    return { ...toolRowToMessage({ ...r, tool_call_id: r.tool_call_id }, makeId(), createdAt), ...identity };
  }
  // Durable turn-level error, rebuilt into the same red card the live path
  // renders so it survives reload and a second device.
  if (r.kind === "error") {
    return { id: makeId(), role: "assistant", text: "", error: r.text, createdAt, ...identity };
  }
  return {
    id: makeId(),
    role: r.role as ChatRole,
    text: r.text,
    attachments: rowAttachmentsToChat(r.attachments),
    createdAt,
    steered: r.steered || undefined,
    // Narration that streamed before a tool call stays folded into the
    // ledger on reload, exactly as it was live.
    interim: r.interim || undefined,
    seeded: r.kind === "dashboard_seed" || undefined,
    seedKind: r.seed_kind || undefined,
    curiosityId: r.curiosity_id || undefined,
    ...identity,
  };
}

/**
 * reconcileRow folds the server's copy of a row into the local one.
 *
 * The server is the authority on CONTENT (its text is the persisted one,
 * which for a reply can differ from what streamed - a verify pass appends
 * its caveat). The local row keeps its React id, its live-only bookkeeping
 * (token counts, latency, the interrupted mark) and anything the server does
 * not hold (a blob preview of an attachment still uploading).
 */
function reconcileRow(local: ChatMessage, server: ChatMessage): ChatMessage {
  if (local.role === "tool") {
    // A running card is freshest locally (it has the streaming state); once
    // either side holds a result, the side that has it wins.
    if (local.toolResult || (local.pending && server.pending)) {
      return { ...local, serverId: server.serverId ?? local.serverId, turnId: local.turnId ?? server.turnId };
    }
    return {
      ...server,
      id: local.id,
      createdAt: local.createdAt,
      toolCall: { ...server.toolCall!, input: server.toolCall?.input ?? local.toolCall?.input },
    };
  }
  return {
    ...server,
    id: local.id,
    attachments: mergeAttachmentLists(local.attachments, server.attachments),
    pending: false,
    interrupted: local.interrupted || server.interrupted || undefined,
    interim: server.interim || local.interim || undefined,
    steered: server.steered || local.steered || undefined,
    inputTokens: local.inputTokens,
    outputTokens: local.outputTokens,
    latencyMs: local.latencyMs,
    proactive: local.proactive,
    proactiveKind: local.proactiveKind,
    runId: local.runId,
    clientId: local.clientId ?? server.clientId,
    turnId: server.turnId ?? local.turnId,
    msgIndex: server.msgIndex ?? local.msgIndex,
  };
}

/**
 * mergeTranscript reconciles the local transcript with the server's rows.
 *
 * Rules, in order:
 *  1. A server row replaces the local row with the same name (reconcileRow).
 *  2. A local row with a name the server does not have yet is KEPT. Hooks
 *     persist asynchronously, so the row can legitimately be a moment behind
 *     the socket; it will match on the next fetch.
 *  3. A local row with no name survives only if it is live (pending,
 *     thinking, an error, a steer), and never when a same-role server row has
 *     exactly its text. Nothing is ever dropped for being a PREFIX.
 *
 * Merge only: it never decides that a turn is over. The server's turn_status
 * does that (see liveness.ts).
 */
export function mergeTranscript(
  local: ChatMessage[],
  rows: TranscriptRow[],
  makeId: () => string,
): ChatMessage[] {
  const server = rows.map((r) => rowToMessage(r, makeId));
  const localByKey = new Map<string, ChatMessage>();
  for (const m of local) {
    const k = messageKey(m);
    if (k && !localByKey.has(k)) localByKey.set(k, m);
  }
  const used = new Set<string>();
  const serverKeys = new Set<string>();
  const serverTextByRole = new Map<ChatRole, Set<string>>();
  const serverErrors = new Set<string>();
  const out: ChatMessage[] = [];
  for (const s of server) {
    const k = messageKey(s);
    if (k) serverKeys.add(k);
    const err = (s.error ?? "").trim();
    if (err) serverErrors.add(err);
    const set = serverTextByRole.get(s.role) ?? new Set<string>();
    set.add(s.text.trim());
    serverTextByRole.set(s.role, set);
    const l = k ? localByKey.get(k) : undefined;
    if (l) {
      out.push(reconcileRow(l, s));
      used.add(l.id);
    } else {
      out.push(s);
    }
  }
  for (const m of local) {
    if (used.has(m.id)) continue;
    const k = messageKey(m);
    if (k) {
      // A second local copy of a row the server holds (a tool card appended
      // twice by a replayed frame) is a duplicate, not something to keep.
      if (serverKeys.has(k)) continue;
      out.push(m);
      continue;
    }
    if (!survivesRefetch(m)) continue;
    const err = (m.error ?? "").trim();
    if (err) {
      if (serverErrors.has(err)) continue;
    } else if (m.text.trim() && serverTextByRole.get(m.role)?.has(m.text.trim())) {
      continue;
    }
    out.push(m);
  }
  return out.sort((a, b) => a.createdAt - b.createdAt);
}

/** One `delta` frame, as applyDelta needs it. */
export type DeltaFrame = {
  text: string;
  turnId?: string;
  msgIndex?: number;
};

/**
 * applyDelta lands one chunk of streamed text in the bubble it belongs to.
 *
 * With a turn id and a message index the target is exact: the assistant
 * bubble of THAT message of THAT turn, wherever it sits, created if it does
 * not exist yet. Two indices are two bubbles, which is how the narration
 * before a tool call and the reply after it stay apart. With only a turn id
 * (a core between versions) it is the pending bubble of that turn; with
 * neither (an old core) it is the tail, the rule the ids replace.
 */
export function applyDelta(
  messages: ChatMessage[],
  delta: DeltaFrame,
  makeId: () => string,
  now: number,
): ChatMessage[] {
  const { turnId, msgIndex, text } = delta;
  let at = -1;
  if (turnId && typeof msgIndex === "number") {
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i];
      if (m.role === "assistant" && m.turnId === turnId && m.msgIndex === msgIndex) {
        at = i;
        break;
      }
    }
  } else if (turnId) {
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i];
      if (m.role === "assistant" && m.pending && m.turnId === turnId) {
        at = i;
        break;
      }
      if (m.role === "user" && !m.steered) break;
    }
  } else {
    const last = messages[messages.length - 1];
    if (last && last.role === "assistant" && last.pending) at = messages.length - 1;
  }
  const next = [...messages];
  if (at === -1) {
    next.push({ id: makeId(), role: "assistant", text, pending: true, turnId, msgIndex, createdAt: now });
  } else {
    next[at] = { ...next[at], text: next[at].text + text };
  }
  return next;
}
