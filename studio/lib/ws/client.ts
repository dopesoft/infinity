/**
 * Reconnecting WebSocket client.
 *
 * iOS Safari kills WebSocket connections when the tab moves to the background.
 * We listen for `visibilitychange` and `pageshow` to reconnect on foreground.
 */

export type WSEvent =
  | { type: "delta"; session_id: string; text: string }
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
};

export type WSStatus = "connected" | "connecting" | "disconnected";

export type WSClientOptions = {
  url: string;
  onEvent: (ev: WSEvent) => void;
  onStatusChange?: (status: WSStatus) => void;
};

const MIN_BACKOFF = 500;
const MAX_BACKOFF = 15_000;

export class WSClient {
  private url: string;
  private socket: WebSocket | null = null;
  private status: WSStatus = "disconnected";
  private backoff = MIN_BACKOFF;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private closedByUser = false;
  private listeners: WSClientOptions;

  constructor(opts: WSClientOptions) {
    this.url = opts.url;
    this.listeners = opts;
  }

  connect() {
    if (this.socket?.readyState === WebSocket.OPEN || this.socket?.readyState === WebSocket.CONNECTING) {
      return;
    }
    this.closedByUser = false;
    this.setStatus("connecting");
    try {
      this.socket = new WebSocket(this.url);
    } catch {
      this.scheduleReconnect();
      return;
    }

    this.socket.onopen = () => {
      this.backoff = MIN_BACKOFF;
      this.setStatus("connected");
    };
    this.socket.onclose = () => {
      this.setStatus("disconnected");
      if (!this.closedByUser) this.scheduleReconnect();
    };
    this.socket.onerror = () => {
      // onclose follows; reconnect logic lives there
    };
    this.socket.onmessage = (raw) => {
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
    this.socket.send(JSON.stringify(data));
    return true;
  }

  close() {
    this.closedByUser = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.socket?.close();
    this.socket = null;
    this.setStatus("disconnected");
  }

  forceReconnect() {
    this.socket?.close();
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) return;
    const delay = this.backoff + Math.random() * 250;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.backoff = Math.min(this.backoff * 2, MAX_BACKOFF);
      this.connect();
    }, delay);
  }

  private setStatus(status: WSStatus) {
    if (this.status === status) return;
    this.status = status;
    this.listeners.onStatusChange?.(status);
  }
}
