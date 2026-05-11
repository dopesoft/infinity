/**
 * Reconnecting WebSocket client.
 *
 * iOS Safari kills WebSocket connections when the tab moves to the background.
 * We listen for `visibilitychange` and `pageshow` to reconnect on foreground.
 *
 * We also run an application-level ping/pong heartbeat: every PING_INTERVAL_MS
 * we send `{type:"ping"}` (the core replies `{type:"pong"}`). If no message
 * arrives for STALE_TIMEOUT_MS, we treat the socket as dead and force-reconnect
 * even though `readyState` may still claim OPEN — this is the half-dead socket
 * pattern (mobile sleep, NAT timeout, captive proxy) that silently breaks chat.
 */

export type WSEvent =
  | { type: "delta"; session_id: string; text: string }
  | { type: "thinking"; session_id: string; text: string }
  | { type: "tool_call"; session_id: string; tool_call: WSToolEvent }
  | { type: "tool_result"; session_id: string; tool_result: WSToolEvent }
  | {
      type: "complete";
      session_id: string;
      usage?: { input?: number; output?: number };
      stop_reason?: string;
    }
  | { type: "error"; session_id: string; message: string }
  | { type: "cleared"; session_id: string }
  | { type: "pong"; session_id: string };

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
  // in the tool card — no tab switch needed. The agent loop is blocked
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
  // hasn't authenticated yet) — the client retries via scheduleReconnect.
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
        // Not authenticated yet — back off and try again.
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
