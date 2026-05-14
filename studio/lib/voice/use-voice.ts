"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  recordVoiceTurn,
  runVoiceTool,
  startVoiceSession,
} from "@/lib/api";
import { VoiceClient, type VoiceStatus } from "./client";

/**
 * useVoice owns one realtime voice session at a time. It wires:
 *
 *   1. /api/voice/session   → ephemeral key
 *   2. WebRTC SDP exchange  → audio + data channel
 *   3. Tool-call dispatch   → /api/voice/tool → submit result back
 *   4. Transcript capture   → /api/voice/turn → memory hooks
 *
 * Consumers get a state snapshot and three methods: start(), stop(),
 * setMuted(bool). Status drives the orb color / caption in the composer;
 * captions hold the last user + assistant lines for the strip.
 */
export type UseVoiceState = {
  status: VoiceStatus;
  level: number; // 0..1, peak of mic+out so the orb pulse stays smooth
  muted: boolean;
  toolName: string | null;
  error: string | null;
};

const INITIAL: UseVoiceState = {
  status: "idle",
  level: 0,
  muted: false,
  toolName: null,
  error: null,
};

/** Hooks the chat conversation stream into voice's transcript events so
 *  user utterances and live assistant deltas appear as proper chat
 *  messages in the conversation area — never as ephemeral captions in
 *  the composer. The composer only ever renders the orb + status + Mute
 *  + End controls. */
export type UseVoiceCallbacks = {
  onUserMessage?: (text: string) => void;
  onAssistantDelta?: (delta: string, isFinal: boolean) => void;
};

// playConnectChime plays a short two-note ascending tone via the Web
// Audio API. Fires once when the realtime session reaches "listening"
// for the first time, so the boss has an audible confirmation that
// audio output is working AND the agent is ready to listen. Doubles as
// a sanity check: if you don't hear the chime, you won't hear the
// agent either — fix your output device before recording bug reports.
function playConnectChime(): void {
  try {
    const Ctx =
      window.AudioContext ||
      (window as unknown as { webkitAudioContext: typeof AudioContext })
        .webkitAudioContext;
    if (!Ctx) return;
    const ctx = new Ctx();
    // The mic gesture that started this session already unlocked
    // audio; resume() is a no-op if running, a kick if suspended.
    void ctx.resume();
    const now = ctx.currentTime;

    const tone = (freq: number, start: number, dur: number) => {
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.type = "sine";
      osc.frequency.value = freq;
      // Short attack + decay envelope so the chime sounds intentional
      // and doesn't pop.
      gain.gain.setValueAtTime(0, start);
      gain.gain.linearRampToValueAtTime(0.18, start + 0.015);
      gain.gain.linearRampToValueAtTime(0, start + dur);
      osc.connect(gain);
      gain.connect(ctx.destination);
      osc.start(start);
      osc.stop(start + dur + 0.02);
    };

    // C5 → E5: a small, pleasant rising third.
    tone(523.25, now, 0.16);
    tone(659.25, now + 0.13, 0.22);

    // Close the context after the chime finishes so we don't leak.
    window.setTimeout(() => {
      void ctx.close();
    }, 700);
  } catch {
    // Audio context construction can fail in private modes or on
    // some embedded webviews — silent failure is fine, the chime is
    // a convenience.
  }
}

export function useVoice(
  sessionId: string | undefined,
  callbacks?: UseVoiceCallbacks,
) {
  // Keep callbacks fresh across renders without re-binding the voice
  // client's event handlers (which were created when start() was called).
  const cbRef = useRef<UseVoiceCallbacks>(callbacks ?? {});
  cbRef.current = callbacks ?? {};

  const [state, setState] = useState<UseVoiceState>(INITIAL);
  const clientRef = useRef<VoiceClient | null>(null);
  // Latest mic + out levels feed a smoothed peak into state so the orb
  // pulse doesn't flicker between two RAFs. Refs avoid re-renders for
  // every audio frame.
  const micLevelRef = useRef(0);
  const outLevelRef = useRef(0);
  // Buffer assistant deltas so we can post the final string to
  // /api/voice/turn (memory + transcript). We post on the final event
  // and clear here.
  const assistantTextRef = useRef("");
  // Track whether we've already chimed for this session so we don't
  // play the connect tone every time we cycle back through "listening"
  // (e.g. after each tool call completes).
  const chimedRef = useRef(false);

  // Smooth-peak ticker — runs as long as a session exists.
  useEffect(() => {
    let raf = 0;
    let last = 0;
    const tick = (t: number) => {
      if (t - last >= 60) {
        last = t;
        const peak = Math.max(micLevelRef.current, outLevelRef.current);
        setState((s) => (Math.abs(s.level - peak) > 0.02 ? { ...s, level: peak } : s));
      }
      raf = window.requestAnimationFrame(tick);
    };
    raf = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(raf);
  }, []);

  const stop = useCallback(() => {
    clientRef.current?.stop();
    clientRef.current = null;
    micLevelRef.current = 0;
    outLevelRef.current = 0;
    assistantTextRef.current = "";
    chimedRef.current = false;
    setState(INITIAL);
  }, []);

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

    const minted = await startVoiceSession(sessionId);
    if ("error" in minted) {
      setState((s) => ({ ...s, status: "error", error: minted.error }));
      return;
    }

    const client = new VoiceClient({
      sdpUrl: minted.sdp_url,
      clientSecret: minted.client_secret,
      callbacks: {
        onStatus: (status, detail) => {
          setState((s) => ({
            ...s,
            status,
            toolName: status === "tool-running" ? detail ?? null : null,
          }));
          // Audible "connected" cue on the FIRST transition into
          // listening. Subsequent listening states (after a tool
          // call, after a user turn) don't re-chime.
          if (status === "listening" && !chimedRef.current) {
            chimedRef.current = true;
            playConnectChime();
          }
        },
        onError: (msg) => {
          setState((s) => ({ ...s, error: msg }));
        },
        onLevel: (kind, level01) => {
          if (kind === "mic") micLevelRef.current = level01;
          else outLevelRef.current = level01;
        },
        onUserTranscript: (text, isFinal) => {
          if (!isFinal) return;
          // Push the finalised user utterance into the conversation
          // stream as a real chat message AND mirror to Core for
          // memory capture. Belt + suspenders: the live UI shows it
          // immediately; the next session reload would rebuild it
          // from mem_observations either way.
          cbRef.current.onUserMessage?.(text);
          void recordVoiceTurn({ sessionId, role: "user", text });
        },
        onAssistantTranscript: (delta, isFinal) => {
          if (!isFinal) {
            assistantTextRef.current += delta;
            // Stream the delta into the assistant message bubble.
            // useChat creates / extends a pending bubble exactly like
            // text-mode deltas do — the conversation stream is the
            // single source of truth for "what did the agent say".
            cbRef.current.onAssistantDelta?.(delta, false);
            return;
          }
          const finalText = delta && delta.length > assistantTextRef.current.length
            ? delta
            : assistantTextRef.current;
          assistantTextRef.current = "";
          if (finalText.trim()) {
            cbRef.current.onAssistantDelta?.(finalText, true);
            void recordVoiceTurn({ sessionId, role: "assistant", text: finalText });
          }
        },
        onToolCall: async (call) => {
          let input: Record<string, unknown> = {};
          try {
            input = JSON.parse(call.arguments || "{}") as Record<string, unknown>;
          } catch (err) {
            // Don't silently run with empty input — the boss should see
            // this in the console + the model gets a real signal back.
            console.warn("voice: failed to parse tool arguments", {
              tool: call.name,
              raw: call.arguments,
              err,
            });
            clientRef.current?.submitToolResult(
              call.callId,
              `tool ${call.name} call failed: invalid JSON arguments — ${
                err instanceof Error ? err.message : String(err)
              }`,
            );
            return;
          }
          const result = await runVoiceTool({
            sessionId,
            callId: call.callId,
            name: call.name,
            input,
          });
          // Guard against the user ending voice while the tool was
          // running — submitting on a closed data channel is a no-op
          // inside the client, but bailing early saves a render.
          if (!clientRef.current) return;
          // If load_tools / unload_tools / tool_search mutated the
          // active set, push the new tool list to OpenAI BEFORE
          // submitting the result so the model's next response is
          // aware of the new schemas it can call.
          if (!("error" in result) && result.updated_tools && result.updated_tools.length > 0) {
            clientRef.current.updateTools(result.updated_tools);
          }
          const output = "error" in result
            ? `tool ${call.name} failed: ${result.error}`
            : result.output;
          clientRef.current.submitToolResult(call.callId, output);
        },
      },
    });
    clientRef.current = client;
    try {
      await client.start();
    } catch {
      // start() surfaces details via onError + onStatus("error"); we
      // just clean up the ref so a subsequent retry can build a fresh
      // client.
      clientRef.current = null;
    }
  }, [sessionId]);

  // Auto-cleanup on unmount or when the session id changes underneath us.
  useEffect(() => {
    return () => {
      clientRef.current?.stop();
      clientRef.current = null;
    };
  }, []);
  useEffect(() => {
    // Session swap mid-voice ends the current call rather than crossing
    // sessions silently. The composer can re-tap mic if they want to
    // resume on the new session.
    if (state.status !== "idle" && state.status !== "closed") stop();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  const active =
    state.status !== "idle" &&
    state.status !== "closed" &&
    state.status !== "error";

  return { ...state, active, start, stop, setMuted };
}
