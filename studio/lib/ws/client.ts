/**
 * Reconnecting WebSocket client.
 *
 * iOS Safari kills WebSocket connections when the tab moves to the background.
 * We listen for `visibilitychange` and `pageshow` to reconnect on foreground.
 *
 * We also run an application-level ping/pong heartbeat: every PING_INTERVAL_MS
 * we send `{type:"ping"}` (the core replies `{type:"pong"}`). If no message
 * arrives for STALE_TIMEOUT_MS, we treat the socket as dead and force-reconnect
 * even though `readyState` may still claim OPEN - this is the half-dead socket
 * pattern (mobile sleep, NAT timeout, captive proxy) that silently breaks chat.
 */

export type WSEvent =
  | { type: "delta"; session_id: string; text: string }
  | { type: "thinking"; session_id: string; text: string }
  | { type: "tool_call"; session_id: string; tool_call: WSToolEvent }
  // tool_input_delta streams the model writing a tool call's arguments live —
  // e.g. the file content for an edit — BEFORE the tool runs. Studio opens the
  // file in the canvas and types it in as deltas arrive. id/name correlate to
  // the eventual tool_call. Model-agnostic: providers that can't stream tool
  // args never send these and the canvas falls back to the full tool_call.
  | { type: "tool_input_delta"; session_id: string; tool_input_delta: WSToolInputDelta }
  | { type: "tool_result"; session_id: string; tool_result: WSToolEvent }
  | {
      type: "complete";
      session_id: string;
      usage?: { input?: number; output?: number };
      // stop_reason="interrupted" signals a user-cancelled turn. The
      // partial assistant text already streamed is preserved; the UI
      // should treat this as a clean turn end (no error state).
      stop_reason?: string;
    }
  | { type: "error"; session_id: string; message: string }
  // voice_audio carries one synthesized speech clip (a sentence of Jarvis's
  // spoken reply) for a voice turn. data is base64 audio of mime_type; seq
  // orders clips within the turn. useVoice decodes + plays them; useChat
  // ignores them (the text already renders via the normal `delta` frames).
  | { type: "voice_audio"; session_id: string; audio: { seq: number; mime_type: string; data: string } }
  | { type: "cleared"; session_id: string }
  | { type: "pong"; session_id: string }
  // steer_received echoes a mid-turn steer back to all connected tabs so
  // the input that was injected into a running turn is visible everywhere.
  // The originating tab already rendered it optimistically and ignores
  // the echo via a duplicate-id check.
  | { type: "steer_received"; session_id: string; text: string }
  // intent carries the per-turn IntentFlow classification. Emitted async
  // after the WS handler kicks off classification - arrives mid-turn or
  // after `complete` depending on Haiku latency. The IntentStream panel
  // consumes it; the chat transcript ignores it.
  | { type: "intent"; session_id: string; intent: WSIntent }
  // gauge is the per-turn effort sizing (glance/standard/deep). Emitted async
  // like intent; the Intent/effort panel reads it via its own DB fetch path,
  // and the chat transcript ignores it.
  | { type: "gauge"; session_id: string; gauge: WSGauge }
  // effort is the per-turn reasoning level steal C chose (none|low|medium|high|
  // xhigh). The Composer chip renders it as "Auto · <level>" so the boss sees
  // how hard Jarvis is thinking. Per-turn, not persisted in the transcript.
  | { type: "effort"; session_id: string; effort: WSEffort }
  // proactive_message is an unprompted assistant turn - broadcast by the
  // heartbeat when a finding crosses the surface threshold (surprise,
  // curiosity, security, or any pre-approved finding). useChat renders
  // these as regular assistant bubbles with a subtle origin badge.
  // curiosity_id is set when the finding is backed by a curiosity
  // question - it lets the chat card offer an "Approve & fix" action.
  | {
      type: "proactive_message";
      session_id: string;
      text: string;
      finding_kind?: string;
      curiosity_id?: string;
      run_id?: string;
      progress?: number;
      // Enriched background_build_progress fields — step number, the verb
      // (edit/write/bash…), the current file/command, and the originating task.
      progress_step?: number;
      progress_action?: string;
      progress_detail?: string;
      progress_task?: string;
    }
  // browser_frame is one live CDP screencast frame from the cloud browser,
  // routed to this session's tab so the boss watches Jarvis drive in real
  // time. The CanvasBrowser component renders frame into an <img>. These
  // ride the per-session broadcaster (not the turn stream), so they flow
  // continuously for the whole browser session.
  | { type: "browser_frame"; session_id: string; browser_frame: WSBrowserFrame }
  // browser_control announces who is driving the live browser — "agent" or
  // "human" (takeover). Flips when the agent requests a hand (captcha/login),
  // when the boss's first manual input claims control, and when he hands back.
  | { type: "browser_control"; session_id: string; browser_control: WSBrowserControl }
  // phone_live streams a call in flight: one event per transcript line,
  // then a final done event carrying the outcome summary. Broadcast to all
  // tabs (the Phone card lives on the dashboard, not in a chat session).
  | { type: "phone_live"; session_id: string; phone_live: WSPhoneLive }
  // document_created fires when document_create produces a file. Studio opens
  // a NEW tab: rendered markdown inline for reports, a download card for
  // binaries. Cloud-first — markdown rides the event, binaries fetch via the
  // cloud-direct /api/workspace/download proxy (works on any device).
  | { type: "document_created"; session_id: string; document_created: WSDocumentCreated }
  // project_changed fires when the session's active project switches (agent
  // called project_open/create/clone, or the boss switched it on another
  // device). Studio re-scopes the canvas to project_path instantly instead of
  // waiting on the 1.5s session poll. project_path "" = back to Jarvis's own code.
  | { type: "project_changed"; session_id: string; project_changed: { project_path: string } };

export type WSBrowserFrame = {
  seq: number;
  frame: string; // data:image/jpeg;base64,...
  url?: string;
  browser_session_id?: string;
};

export type WSBrowserControl = {
  browser_session_id: string;
  controller: "agent" | "human";
  reason?: string;
};

export type WSPhoneLive = {
  call_id: string;
  direction: "inbound" | "outbound";
  number?: string;
  speaker?: string;
  text?: string;
  done?: boolean;
  summary?: string;
  status?: string;
};

export type WSDocumentCreated = {
  format: string; // xlsx | docx | pptx | pdf | md
  filename: string;
  path: string; // cloud workspace path (for download)
  bytes?: number;
  markdown?: string; // rendered inline for md/report formats
  pdf_path?: string; // sibling PDF for preview, when also_pdf
  html_path?: string; // side-scrollable HTML preview (spreadsheets)
};

export type WSIntent = {
  token: "silent" | "fast_intervention" | "full_assistance";
  confidence: number;
  reason?: string;
  suggested_action?: string;
};

export type WSGauge = {
  tier: "glance" | "standard" | "deep";
  reason?: string;
};

export type WSEffort = {
  level: string; // none | low | medium | high | xhigh ("" = model default)
  source?: string; // audit reason: boss_pinned | coding_floor | gauge_deep | ...
};

export type WSToolInputDelta = {
  id: string; // matches the eventual tool_call.id
  name: string; // tool name, e.g. fs_edit / claude_code__write
  delta: string; // raw partial-JSON chunk of the tool arguments
};

export type WSToolEvent = {
  id: string;
  name: string;
  input?: Record<string, unknown>;
  output?: string;
  is_error?: boolean;
  started_at?: string;
  ended_at?: string;
  // Set on tool_call events when the gate parked the call on a Trust
  // contract. Studio uses these to render inline Approve / Deny buttons
  // in the tool card - no tab switch needed. The agent loop is blocked
  // on the gate; the next decideTrust() call unblocks it and the real
  // tool result will arrive as a follow-up tool_result event.
  awaiting_approval?: boolean;
  contract_id?: string;
  preview?: string;
};

export type WSStatus = "connected" | "connecting" | "disconnected";

export type WSClientOptions = {
  url: string;
  // tokenProvider is awaited on every connect attempt so a refreshed JWT is
  // always sent to the server. Returning null aborts the connect (caller
  // hasn't authenticated yet) - the client retries via scheduleReconnect.
  tokenProvider?: () => Promise<string | null>;
  onEvent: (ev: WSEvent) => void;
  onStatusChange?: (status: WSStatus) => void;
};

const MIN_BACKOFF = 500;
const MAX_BACKOFF = 15_000;
const PING_INTERVAL_MS = 25_000;
const STALE_TIMEOUT_MS = 60_000;

export class WSClient {
  private url: string;
  private socket: WebSocket | null = null;
  private status: WSStatus = "disconnected";
  private backoff = MIN_BACKOFF;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private lastActivityAt = 0;
  private closedByUser = false;
  private listeners: WSClientOptions;

  constructor(opts: WSClientOptions) {
    this.url = opts.url;
    this.listeners = opts;
  }

  async connect() {
    if (this.socket?.readyState === WebSocket.OPEN || this.socket?.readyState === WebSocket.CONNECTING) {
      return;
    }
    this.closedByUser = false;
    this.setStatus("connecting");

    let url = this.url;
    if (this.listeners.tokenProvider) {
      const token = await this.listeners.tokenProvider();
      if (!token) {
        // Not authenticated yet - back off and try again.
        this.setStatus("disconnected");
        this.scheduleReconnect();
        return;
      }
      // Browsers can't set headers on WebSocket; pass JWT as query param.
      // Core auth middleware accepts ?token= as a fallback to Authorization.
      const sep = url.includes("?") ? "&" : "?";
      url = `${url}${sep}token=${encodeURIComponent(token)}`;
    }

    try {
      this.socket = new WebSocket(url);
    } catch {
      this.scheduleReconnect();
      return;
    }

    this.socket.onopen = () => {
      this.backoff = MIN_BACKOFF;
      this.lastActivityAt = Date.now();
      this.setStatus("connected");
      this.startHeartbeat();
    };
    this.socket.onclose = () => {
      this.stopHeartbeat();
      this.setStatus("disconnected");
      if (!this.closedByUser) this.scheduleReconnect();
    };
    this.socket.onerror = () => {
      // onclose follows; reconnect logic lives there
    };
    this.socket.onmessage = (raw) => {
      this.lastActivityAt = Date.now();
      try {
        const ev = JSON.parse(raw.data) as WSEvent;
        this.listeners.onEvent(ev);
      } catch {
        /* ignore malformed */
      }
    };
  }

  send(data: Record<string, unknown>) {
    if (this.socket?.readyState !== WebSocket.OPEN) return false;
    try {
      this.socket.send(JSON.stringify(data));
      return true;
    } catch {
      // Broken pipe / half-dead socket. Force reconnect so the next attempt
      // gets a fresh connection.
      this.forceReconnect();
      return false;
    }
  }

  close() {
    this.closedByUser = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.stopHeartbeat();
    this.socket?.close();
    this.socket = null;
    this.setStatus("disconnected");
  }

  forceReconnect() {
    this.stopHeartbeat();
    try {
      this.socket?.close();
    } catch {
      /* ignore */
    }
    this.socket = null;
    // Reset backoff so the user-initiated reconnect tries immediately.
    this.backoff = MIN_BACKOFF;
    void this.connect();
  }

  private startHeartbeat() {
    this.stopHeartbeat();
    this.heartbeatTimer = setInterval(() => {
      if (!this.socket || this.socket.readyState !== WebSocket.OPEN) return;
      // Stale detection: if we haven't heard anything in STALE_TIMEOUT_MS
      // even though readyState says OPEN, treat the socket as dead. This
      // catches the half-dead pattern mobile networks and proxies create.
      if (Date.now() - this.lastActivityAt > STALE_TIMEOUT_MS) {
        this.forceReconnect();
        return;
      }
      try {
        this.socket.send(JSON.stringify({ type: "ping", session_id: "" }));
      } catch {
        this.forceReconnect();
      }
    }, PING_INTERVAL_MS);
  }

  private stopHeartbeat() {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) return;
    const delay = this.backoff + Math.random() * 250;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.backoff = Math.min(this.backoff * 2, MAX_BACKOFF);
      void this.connect();
    }, delay);
  }

  private setStatus(status: WSStatus) {
    if (this.status === status) return;
    this.status = status;
    this.listeners.onStatusChange?.(status);
  }
}
