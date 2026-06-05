"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { reportVoiceError, startVoiceSession } from "@/lib/api";
import { useWebSocket } from "@/lib/ws/provider";
import { AudioQueue } from "./audio-queue";
import { VoiceClient, type VoiceStatus } from "./client";

/**
 * useVoice owns one realtime voice session at a time. After the parity rewrite
 * the realtime/WebRTC session is INPUT ONLY - mic capture, whisper
 * transcription, and VAD barge-in. It does NOT think and does NOT speak.
 *
 * The flow:
 *   1. /api/voice/session   → input-only ephemeral key
 *   2. WebRTC SDP exchange  → mic in + transcription events
 *   3. finalized transcript → onSend(text) → chat.send(text, {voice:true})
 *      → Loop.Run (the SAME brain as text) streams the reply
 *   4. reply text → TTS on Core → `voice_audio` WS frames → AudioQueue plays
 *   5. boss talks over Jarvis → VAD speech_started → AudioQueue.stop()
 *
 * So voice Jarvis is text Jarvis with a mic and a voice bolted on - identical
 * memory, skills, tools, and gate, because it runs the identical code path.
 */
export type UseVoiceState = {
  status: VoiceStatus;
  level: number; // 0..1, mic peak so the orb pulse stays smooth
  muted: boolean;
  error: string | null;
};

const INITIAL: UseVoiceState = {
  status: "idle",
  level: 0,
  muted: false,
  error: null,
};

export type UseVoiceCallbacks = {
  // Send a finalized voice transcript to the real brain. The host wires this
  // to chat.send(text, undefined, { voice: true }) so voice runs the IDENTICAL
  // Loop.Run as text. The spoken reply returns as `voice_audio` frames that
  // this hook plays - the host doesn't deal with audio at all.
  onSend?: (text: string) => void;
};

// After a barge-in we briefly ignore incoming audio frames so the tail of the
// clip Jarvis was mid-way through (already synthesized and in flight from Core)
// doesn't resume after we cut him off. The next response's audio lands well
// after this window. Tune on-device if it ever clips the start of a reply.
const SUPPRESS_AFTER_BARGE_IN_MS = 600;

// playConnectChime plays a short two-note ascending tone so the boss has an
// audible "connected and listening" cue. If you don't hear it, you won't hear
// Jarvis either - check your output device.
function playConnectChime(): void {
  try {
    const Ctx =
      window.AudioContext ||
      (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
    if (!Ctx) return;
    const ctx = new Ctx();
    void ctx.resume();
    const now = ctx.currentTime;
    const tone = (freq: number, start: number, dur: number) => {
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.type = "sine";
      osc.frequency.value = freq;
      gain.gain.setValueAtTime(0, start);
      gain.gain.linearRampToValueAtTime(0.18, start + 0.015);
      gain.gain.linearRampToValueAtTime(0, start + dur);
      osc.connect(gain);
      gain.connect(ctx.destination);
      osc.start(start);
      osc.stop(start + dur + 0.02);
    };
    tone(523.25, now, 0.16);
    tone(659.25, now + 0.13, 0.22);
    window.setTimeout(() => {
      void ctx.close();
    }, 700);
  } catch {
    // Private mode / embedded webviews can refuse an AudioContext - the chime
    // is a convenience, fail silently.
  }
}

export function useVoice(sessionId: string | undefined, callbacks?: UseVoiceCallbacks) {
  const cbRef = useRef<UseVoiceCallbacks>(callbacks ?? {});
  cbRef.current = callbacks ?? {};

  const ws = useWebSocket();

  const [state, setState] = useState<UseVoiceState>(INITIAL);
  const clientRef = useRef<VoiceClient | null>(null);
  const micLevelRef = useRef(0);

  // Spoken-reply playback + its WS subscription, scoped to the live session.
  const audioRef = useRef<AudioQueue | null>(null);
  const unsubRef = useRef<(() => void) | null>(null);
  const suppressUntilRef = useRef(0);

  const chimedRef = useRef(false);
  const lastVoiceErrorRef = useRef<string>("");
  const reportedErrorRef = useRef(false);

  // Smooth-peak ticker for the orb pulse - runs as long as a session exists.
  useEffect(() => {
    let raf = 0;
    let last = 0;
    const tick = (t: number) => {
      if (t - last >= 60) {
        last = t;
        const peak = micLevelRef.current;
        setState((s) => (Math.abs(s.level - peak) > 0.02 ? { ...s, level: peak } : s));
      }
      raf = window.requestAnimationFrame(tick);
    };
    raf = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(raf);
  }, []);

  const teardownAudio = useCallback(() => {
    unsubRef.current?.();
    unsubRef.current = null;
    audioRef.current?.close();
    audioRef.current = null;
  }, []);

  const stop = useCallback(() => {
    clientRef.current?.stop();
    clientRef.current = null;
    micLevelRef.current = 0;
    chimedRef.current = false;
    teardownAudio();
    setState(INITIAL);
  }, [teardownAudio]);

  const setMuted = useCallback((muted: boolean) => {
    clientRef.current?.setMuted(muted);
    setState((s) => ({ ...s, muted }));
  }, []);

  const start = useCallback(async () => {
    if (!sessionId) {
      setState((s) => ({ ...s, error: "no active session", status: "error" }));
      return;
    }
    if (clientRef.current) return;

    setState((s) => ({ ...s, status: "connecting", error: null }));
    lastVoiceErrorRef.current = "";
    reportedErrorRef.current = false;

    const minted = await startVoiceSession(sessionId);
    if ("error" in minted) {
      setState((s) => ({ ...s, status: "error", error: minted.error }));
      return;
    }

    // Spoken-reply playback. Construct now - inside the mic-tap gesture - so
    // iOS Safari lets the AudioContext start. Subscribe to this session's
    // voice_audio frames and feed them in.
    const audio = new AudioQueue();
    audioRef.current = audio;
    unsubRef.current = ws.subscribe((ev) => {
      const e = ev as { type?: string; session_id?: string; audio?: { data?: string } };
      if (e.type !== "voice_audio") return;
      if (e.session_id && e.session_id !== sessionId) return;
      if (Date.now() < suppressUntilRef.current) return; // swallow stale post-barge-in tail
      if (e.audio?.data) audio.enqueue(e.audio.data);
    });

    const client = new VoiceClient({
      sdpUrl: minted.sdp_url,
      clientSecret: minted.client_secret,
      callbacks: {
        onStatus: (status, detail) => {
          setState((s) => ({ ...s, status }));
          if (status === "error" && !reportedErrorRef.current) {
            reportedErrorRef.current = true;
            void reportVoiceError({
              sessionId,
              kind: detail ?? "unknown",
              message: lastVoiceErrorRef.current || detail || "voice connection failed",
            });
          }
          if (status === "listening" && !chimedRef.current) {
            chimedRef.current = true;
            playConnectChime();
          }
        },
        onError: (msg) => {
          lastVoiceErrorRef.current = msg;
          setState((s) => ({ ...s, error: msg }));
        },
        onLevel: (kind, level01) => {
          if (kind === "mic") micLevelRef.current = level01;
        },
        // Finalized utterance → the REAL brain. Not the realtime model.
        onUserTranscript: (text, isFinal) => {
          if (!isFinal) return;
          const clean = text.trim();
          if (clean) cbRef.current.onSend?.(clean);
        },
        // Barge-in: the boss is talking over Jarvis. Cut playback instantly
        // and briefly ignore in-flight clips so his current sentence's tail
        // doesn't resume. The new utterance steers/starts a turn on its own.
        onBargeIn: () => {
          suppressUntilRef.current = Date.now() + SUPPRESS_AFTER_BARGE_IN_MS;
          audioRef.current?.stop();
        },
      },
    });
    clientRef.current = client;
    try {
      await client.start();
    } catch {
      // start() surfaces details via onError + onStatus("error"); just clear
      // the refs so a retry builds a fresh client + audio queue.
      clientRef.current = null;
      teardownAudio();
    }
  }, [sessionId, ws, teardownAudio]);

  // Auto-cleanup on unmount or when the session id changes underneath us.
  useEffect(() => {
    return () => {
      clientRef.current?.stop();
      clientRef.current = null;
      teardownAudio();
    };
  }, [teardownAudio]);

  useEffect(() => {
    if (state.status !== "idle" && state.status !== "closed") stop();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  const active =
    state.status !== "idle" && state.status !== "closed" && state.status !== "error";

  return { ...state, active, start, stop, setMuted };
}
