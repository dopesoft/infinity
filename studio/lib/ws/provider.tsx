"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useAuth } from "@/lib/auth/session";
import { WSClient, type WSEvent, type WSStatus } from "@/lib/ws/client";

type Subscriber = (ev: WSEvent) => void;

type WSContextValue = {
  status: WSStatus;
  send: (msg: Record<string, unknown>) => boolean;
  subscribe: (handler: Subscriber) => () => void;
  reconnect: () => void;
};

const WSContext = createContext<WSContextValue | null>(null);

function getDefaultURL(): string {
  if (typeof window === "undefined") return "";
  const explicit = process.env.NEXT_PUBLIC_CORE_WS_URL;
  if (explicit) return explicit;
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/ws`;
}

export function WebSocketProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<WSStatus>("disconnected");
  const subscribers = useRef<Set<Subscriber>>(new Set());
  const clientRef = useRef<WSClient | null>(null);
  const { user, getAccessToken } = useAuth();
  const userId = user?.id ?? null;

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (!userId) {
      // Not signed in yet — tear down any prior client (e.g. after sign-out)
      // and skip connect. The next auth state change re-runs this effect.
      clientRef.current?.close();
      clientRef.current = null;
      setStatus("disconnected");
      return;
    }
    const url = getDefaultURL();
    if (!url) return;

    const client = new WSClient({
      url,
      tokenProvider: getAccessToken,
      onStatusChange: setStatus,
      onEvent: (ev) => {
        for (const fn of subscribers.current) fn(ev);
      },
    });
    clientRef.current = client;
    void client.connect();

    const onVisibilityOrShow = () => {
      if (typeof document !== "undefined" && document.visibilityState === "visible") {
        void client.connect();
      }
    };

    window.addEventListener("pageshow", onVisibilityOrShow);
    window.addEventListener("focus", onVisibilityOrShow);
    document.addEventListener("visibilitychange", onVisibilityOrShow);

    return () => {
      window.removeEventListener("pageshow", onVisibilityOrShow);
      window.removeEventListener("focus", onVisibilityOrShow);
      document.removeEventListener("visibilitychange", onVisibilityOrShow);
      client.close();
      clientRef.current = null;
    };
    // Intentional: getAccessToken is module-level and always returns the
    // freshest token, so re-binding the WS on its identity is unnecessary.
    // userId is the only thing that should rebuild the socket.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userId]);

  const send = useCallback((msg: Record<string, unknown>) => {
    return clientRef.current?.send(msg) ?? false;
  }, []);

  const subscribe = useCallback((handler: Subscriber) => {
    subscribers.current.add(handler);
    return () => {
      subscribers.current.delete(handler);
    };
  }, []);

  const reconnect = useCallback(() => {
    clientRef.current?.forceReconnect();
  }, []);

  const value = useMemo(() => ({ status, send, subscribe, reconnect }), [status, send, subscribe, reconnect]);
  return <WSContext.Provider value={value}>{children}</WSContext.Provider>;
}

export function useWebSocket() {
  const ctx = useContext(WSContext);
  if (!ctx) throw new Error("useWebSocket must be used within WebSocketProvider");
  return ctx;
}
