"use client";

// use-wake-word.ts - React lifecycle around WakeWordListener.
//
// Owns the enable toggle (persisted per-device in localStorage - a mic
// affordance is inherently per-device) and the mic handoff: `suspended`
// MUST be true while a realtime voice session is active so the wake
// listener releases the mic before useVoice acquires it, and re-arms
// after. Detection is fully on-device (openWakeWord over onnxruntime-web,
// see wake.ts) - no account, no key, nothing to configure.
//
// States surface to the composer as a single chip driven by
// off/loading/listening/paused/error.

import * as React from "react";
import { WakeWordListener, type WakeStatus } from "@/lib/voice/wake";

const ENABLED_KEY = "jarvis.wake.enabled";

export type UseWakeWord = {
  enabled: boolean;
  status: WakeStatus;
  error: string | null;
  toggle: () => void;
};

export function useWakeWord(opts: {
  suspended: boolean;
  onWake: () => void;
}): UseWakeWord {
  const { suspended, onWake } = opts;
  const [enabled, setEnabled] = React.useState(false);
  const [status, setStatus] = React.useState<WakeStatus>("off");
  const [error, setError] = React.useState<string | null>(null);
  const listenerRef = React.useRef<WakeWordListener | null>(null);
  const onWakeRef = React.useRef(onWake);
  onWakeRef.current = onWake;

  // Hydrate the toggle after mount (localStorage is client-only).
  React.useEffect(() => {
    try {
      setEnabled(window.localStorage.getItem(ENABLED_KEY) === "true");
    } catch {
      // Private-mode Safari can throw; default stays off.
    }
  }, []);

  // Listener lifecycle: exists iff enabled.
  React.useEffect(() => {
    if (!enabled) return;
    const listener = new WakeWordListener({
      onWake: () => onWakeRef.current(),
      onStatus: (s, err) => {
        setStatus(s);
        setError(s === "error" ? (err ?? "wake word failed") : null);
      },
    });
    listenerRef.current = listener;
    void listener.start();
    return () => {
      listenerRef.current = null;
      void listener.stop();
    };
  }, [enabled]);

  // Mic handoff with the realtime voice session.
  React.useEffect(() => {
    const listener = listenerRef.current;
    if (!listener) return;
    if (suspended) void listener.pause();
    else void listener.resume();
  }, [suspended]);

  const toggle = React.useCallback(() => {
    setEnabled((prev) => {
      const next = !prev;
      try {
        window.localStorage.setItem(ENABLED_KEY, String(next));
      } catch {
        // Non-persistent storage - the toggle still works for this page.
      }
      return next;
    });
  }, []);

  return { enabled, status, error, toggle };
}
