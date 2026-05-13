// VoiceClient wraps the OpenAI Realtime WebRTC handshake + data-channel
// event loop. The browser owns the audio pipes (mic capture → out, model
// audio → speakers); Core only mints the ephemeral key and runs tool
// calls. Audio never touches our server.
//
// Lifecycle:
//
//   const client = new VoiceClient({ sdpUrl, clientSecret, callbacks });
//   await client.start();           // permissions → peer connection → connected
//   client.setMuted(true);          // suspend mic track without dropping the session
//   client.stop();                  // close peer connection + release mic
//
// Callbacks fire on the data channel as the model talks, the user is
// transcribed, tools are requested, or barge-in starts. They run on the
// main thread; keep them cheap and let React state batching do the rest.

export type VoiceStatus =
  | "idle"
  | "requesting-permission"
  | "connecting"
  | "listening"
  | "user-speaking"
  | "assistant-speaking"
  | "tool-running"
  | "error"
  | "closed";

export type VoiceToolCall = {
  callId: string;
  name: string;
  arguments: string; // raw JSON-as-string from OpenAI
};

export type VoiceCallbacks = {
  onStatus?: (s: VoiceStatus, detail?: string) => void;
  onUserTranscript?: (text: string, isFinal: boolean) => void;
  onAssistantTranscript?: (delta: string, isFinal: boolean) => void;
  onToolCall?: (call: VoiceToolCall) => void;
  onLevel?: (kind: "mic" | "out", level01: number) => void;
  onError?: (msg: string) => void;
};

export type VoiceClientArgs = {
  sdpUrl: string;
  clientSecret: string;
  callbacks?: VoiceCallbacks;
};

export class VoiceClient {
  private pc: RTCPeerConnection | null = null;
  private dc: RTCDataChannel | null = null;
  private localStream: MediaStream | null = null;
  private audioEl: HTMLAudioElement | null = null;
  private micLevelTimer: number | null = null;
  private outLevelTimer: number | null = null;
  private micAnalyser: AnalyserNode | null = null;
  private outAnalyser: AnalyserNode | null = null;
  private audioCtx: AudioContext | null = null;
  // Buffer assistant transcript deltas per response so we can fire
  // onAssistantTranscript({ isFinal: true }) with the full text on
  // response.done. Keyed by response_id.
  private assistantBuf: Map<string, string> = new Map();

  constructor(private args: VoiceClientArgs) {}

  /** Acquire mic, build peer connection, do SDP exchange. Idempotent: a
   * second call while already connected is a no-op. */
  async start(): Promise<void> {
    if (this.pc) return;
    const cb = this.args.callbacks ?? {};

    try {
      cb.onStatus?.("requesting-permission");
      this.localStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
        video: false,
      });
    } catch (err) {
      cb.onError?.(err instanceof Error ? err.message : "Mic permission denied");
      cb.onStatus?.("error", "mic-permission");
      throw err;
    }

    cb.onStatus?.("connecting");

    const pc = new RTCPeerConnection({
      iceServers: [{ urls: "stun:stun.l.google.com:19302" }],
    });
    this.pc = pc;

    // Outbound audio from OpenAI lands on this <audio> element.
    //
    // iOS Safari quirks worth knowing about here:
    //   - autoplay-blocking is bypassed by the user gesture that started
    //     the session (the mic tap), but only if the element is in the
    //     DOM. A detached `new Audio()` plays no sound. Append it,
    //     hidden but live.
    //   - `playsInline` is required to keep audio in-page on iPhone
    //     instead of opening a fullscreen player.
    const audioEl = document.createElement("audio");
    audioEl.autoplay = true;
    // `playsInline` lives on the DOM element only on TS lib >= dom.iterable
    // with a recent target; set via attribute for ironclad iOS coverage.
    audioEl.setAttribute("playsinline", "true");
    audioEl.setAttribute("aria-hidden", "true");
    audioEl.style.position = "fixed";
    audioEl.style.width = "0";
    audioEl.style.height = "0";
    audioEl.style.opacity = "0";
    audioEl.style.pointerEvents = "none";
    document.body.appendChild(audioEl);
    this.audioEl = audioEl;
    pc.ontrack = (e) => {
      const stream = e.streams[0];
      audioEl.srcObject = stream;
      // Some browsers race the metadata; call play() explicitly after
      // the gesture. Swallow the "interrupted by new load" rejection.
      audioEl.play().catch(() => undefined);
      this.attachOutLevelMeter(stream);
    };

    // Surface connection failures so the UI doesn't sit on "connecting"
    // forever. NAT-restricted networks without TURN will land here.
    pc.oniceconnectionstatechange = () => {
      const st = pc.iceConnectionState;
      if (st === "failed" || st === "disconnected") {
        this.args.callbacks?.onError?.(`ICE ${st} — network blocked WebRTC`);
        this.args.callbacks?.onStatus?.("error", `ice-${st}`);
      }
    };
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === "failed") {
        this.args.callbacks?.onError?.("Peer connection failed");
        this.args.callbacks?.onStatus?.("error", "pc-failed");
      }
    };

    // Send local mic.
    for (const track of this.localStream.getAudioTracks()) {
      pc.addTrack(track, this.localStream);
    }
    this.attachMicLevelMeter(this.localStream);

    // Data channel for events (function_call, transcription, etc.).
    const dc = pc.createDataChannel("oai-events");
    this.dc = dc;
    dc.onopen = () => cb.onStatus?.("listening");
    dc.onmessage = (e) => this.handleEvent(e.data);

    // SDP exchange.
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);

    let answerSDP: string;
    try {
      const resp = await fetch(this.args.sdpUrl, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${this.args.clientSecret}`,
          "Content-Type": "application/sdp",
        },
        body: offer.sdp,
      });
      if (!resp.ok) {
        const body = await resp.text().catch(() => "");
        throw new Error(`SDP exchange ${resp.status}: ${body.slice(0, 300)}`);
      }
      answerSDP = await resp.text();
    } catch (err) {
      cb.onError?.(err instanceof Error ? err.message : "SDP exchange failed");
      cb.onStatus?.("error", "sdp");
      this.stop();
      throw err;
    }

    await pc.setRemoteDescription({ type: "answer", sdp: answerSDP });
  }

  /** Mute/unmute the mic track. Keeps the peer connection alive so
   * unmuting doesn't require a new SDP exchange. */
  setMuted(muted: boolean): void {
    if (!this.localStream) return;
    for (const t of this.localStream.getAudioTracks()) t.enabled = !muted;
  }

  /** Submit a tool result back into the conversation. Caller is whoever
   * dispatched the tool call to Core (see /api/voice/tool). */
  submitToolResult(callId: string, output: string): void {
    if (!this.dc || this.dc.readyState !== "open") return;
    this.dc.send(
      JSON.stringify({
        type: "conversation.item.create",
        item: {
          type: "function_call_output",
          call_id: callId,
          output,
        },
      }),
    );
    // Nudge the model to continue. Without this prompt the model will
    // sit on the tool result instead of speaking back.
    this.dc.send(JSON.stringify({ type: "response.create" }));
  }

  /** Replace the realtime session's tools list. Used after a tool call
   * (load_tools / unload_tools / tool_search) mutates Core's per-session
   * ActiveSet — the diffed tool defs come back on the /api/voice/tool
   * response and we push them here so the next turn sees the new
   * schemas. No-op when the data channel isn't open. */
  updateTools(tools: Array<Record<string, unknown>>): void {
    if (!this.dc || this.dc.readyState !== "open") return;
    this.dc.send(
      JSON.stringify({
        type: "session.update",
        session: { tools },
      }),
    );
  }

  /** Tear everything down. Safe to call multiple times. */
  stop(): void {
    const cb = this.args.callbacks ?? {};
    try {
      this.dc?.close();
    } catch {
      // intentionally swallow — close races are routine
    }
    try {
      this.pc?.close();
    } catch {
      // intentionally swallow
    }
    if (this.localStream) {
      for (const t of this.localStream.getTracks()) t.stop();
    }
    if (this.audioEl) {
      try {
        this.audioEl.pause();
        this.audioEl.srcObject = null;
        if (this.audioEl.parentNode) this.audioEl.parentNode.removeChild(this.audioEl);
      } catch {
        // intentionally swallow — DOM detach races on close are routine
      }
    }
    if (this.micLevelTimer) {
      window.clearInterval(this.micLevelTimer);
      this.micLevelTimer = null;
    }
    if (this.outLevelTimer) {
      window.clearInterval(this.outLevelTimer);
      this.outLevelTimer = null;
    }
    if (this.audioCtx) {
      try {
        this.audioCtx.close();
      } catch {
        // intentionally swallow
      }
      this.audioCtx = null;
    }
    this.pc = null;
    this.dc = null;
    this.localStream = null;
    this.audioEl = null;
    this.micAnalyser = null;
    this.outAnalyser = null;
    cb.onStatus?.("closed");
  }

  // ── Internals ──────────────────────────────────────────────────────────

  // Per-call accumulators for streamed function-call arguments. GA emits
  // `response.function_call_arguments.delta` but the canonical "done"
  // signal arrives via `response.done` (with output items carrying the
  // assembled function_call). Keying by call_id keeps us correct when
  // multiple tools are requested in a single response.
  private toolArgsBuf: Map<string, { name: string; args: string }> = new Map();
  // Track which calls we've already dispatched so a redundant signal
  // from either path doesn't fire the tool twice.
  private dispatchedCalls: Set<string> = new Set();

  private handleEvent(raw: unknown): void {
    if (typeof raw !== "string") return;
    let evt: { type?: string; [k: string]: unknown };
    try {
      evt = JSON.parse(raw);
    } catch {
      return;
    }
    const cb = this.args.callbacks ?? {};

    switch (evt.type) {
      // Barge-in: user starts talking. Pause the audio element so the
      // model's last delta is silenced immediately. Server-side VAD
      // also tells the model to truncate.
      case "input_audio_buffer.speech_started": {
        if (this.audioEl) {
          try { this.audioEl.pause(); } catch { /* close races */ }
        }
        cb.onStatus?.("user-speaking");
        break;
      }
      case "input_audio_buffer.speech_stopped": {
        cb.onStatus?.("listening");
        break;
      }

      // User transcription. GA emits deltas + completed; we only act
      // on `completed` for caption + memory capture since deltas
      // would replace the caption text mid-utterance and feel jittery.
      case "conversation.item.input_audio_transcription.completed": {
        const text = String((evt as { transcript?: string }).transcript ?? "").trim();
        if (text) cb.onUserTranscript?.(text, true);
        break;
      }

      // Assistant audio transcript. GA event names are
      // response.output_audio_transcript.{delta,done}. The beta
      // surface used response.audio_transcript.* — do NOT revive that.
      case "response.output_audio_transcript.delta": {
        const delta = String((evt as { delta?: string }).delta ?? "");
        if (!delta) break;
        const respId = String((evt as { response_id?: string }).response_id ?? "");
        if (respId) {
          this.assistantBuf.set(respId, (this.assistantBuf.get(respId) ?? "") + delta);
        }
        cb.onAssistantTranscript?.(delta, false);
        cb.onStatus?.("assistant-speaking");
        break;
      }
      case "response.output_audio_transcript.done": {
        const respId = String((evt as { response_id?: string }).response_id ?? "");
        const text = (evt as { transcript?: string }).transcript
          ?? (respId ? this.assistantBuf.get(respId) : undefined)
          ?? "";
        const final = String(text).trim();
        if (final) cb.onAssistantTranscript?.(final, true);
        if (respId) this.assistantBuf.delete(respId);
        break;
      }

      // Function call arguments arrive incrementally on .delta. GA
      // doesn't always fire a .done sibling; the assembled call lands
      // in response.done's output items instead. Accumulate by call_id
      // here and let the response.done handler dispatch.
      case "response.function_call_arguments.delta": {
        const callId = String((evt as { call_id?: string }).call_id ?? "");
        if (!callId) break;
        const name = String((evt as { name?: string }).name ?? "");
        const delta = String((evt as { delta?: string }).delta ?? "");
        const cur = this.toolArgsBuf.get(callId) ?? { name: "", args: "" };
        if (name) cur.name = name;
        cur.args += delta;
        this.toolArgsBuf.set(callId, cur);
        cb.onStatus?.("tool-running", cur.name);
        break;
      }

      // response.done is the canonical "response is fully assembled"
      // signal in GA. Its `response.output` array contains items —
      // any with type === "function_call" carry { call_id, name,
      // arguments } as a complete payload. Dispatch here.
      case "response.done": {
        const response = (evt as { response?: { output?: Array<Record<string, unknown>> } }).response;
        const output = response?.output ?? [];
        let dispatched = 0;
        for (const item of output) {
          if (item?.type !== "function_call") continue;
          const callId = String(item.call_id ?? "");
          if (!callId || this.dispatchedCalls.has(callId)) continue;
          const name = String(item.name ?? this.toolArgsBuf.get(callId)?.name ?? "");
          const argsStr = String(
            item.arguments ?? this.toolArgsBuf.get(callId)?.args ?? "{}",
          );
          if (!name) continue;
          this.dispatchedCalls.add(callId);
          this.toolArgsBuf.delete(callId);
          cb.onStatus?.("tool-running", name);
          cb.onToolCall?.({ callId, name, arguments: argsStr });
          dispatched++;
        }
        if (dispatched === 0) cb.onStatus?.("listening");
        break;
      }

      // Legacy/defensive: some surfaces still emit a `.done` sibling
      // event for function calls. Honour it if it shows up so we
      // don't sit on accumulated args waiting for response.done.
      case "response.function_call_arguments.done": {
        const callId = String((evt as { call_id?: string }).call_id ?? "");
        if (!callId || this.dispatchedCalls.has(callId)) break;
        const name = String(
          (evt as { name?: string }).name ?? this.toolArgsBuf.get(callId)?.name ?? "",
        );
        const args = String(
          (evt as { arguments?: string }).arguments
            ?? this.toolArgsBuf.get(callId)?.args
            ?? "{}",
        );
        if (!name) break;
        this.dispatchedCalls.add(callId);
        this.toolArgsBuf.delete(callId);
        cb.onStatus?.("tool-running", name);
        cb.onToolCall?.({ callId, name, arguments: args });
        break;
      }

      case "error": {
        const err = (evt as { error?: { message?: string } }).error;
        cb.onError?.(err?.message ?? "realtime error");
        cb.onStatus?.("error", err?.message);
        break;
      }

      default:
        // Quiet — there are dozens of event types and we only care
        // about the ones that drive UI.
        break;
    }
  }

  private attachMicLevelMeter(stream: MediaStream): void {
    try {
      const ctx = this.audioCtx ?? new AudioContext();
      this.audioCtx = ctx;
      const src = ctx.createMediaStreamSource(stream);
      const an = ctx.createAnalyser();
      an.fftSize = 256;
      src.connect(an);
      this.micAnalyser = an;
      const buf = new Uint8Array(an.frequencyBinCount);
      const cb = this.args.callbacks ?? {};
      this.micLevelTimer = window.setInterval(() => {
        an.getByteFrequencyData(buf);
        let sum = 0;
        for (let i = 0; i < buf.length; i++) sum += buf[i];
        const level = Math.min(1, sum / (buf.length * 255) * 2.5);
        cb.onLevel?.("mic", level);
      }, 90);
    } catch {
      // Audio context creation can fail on some browsers — surface as
      // missing level meter, not as a session-killing error.
    }
  }

  private attachOutLevelMeter(stream: MediaStream): void {
    try {
      const ctx = this.audioCtx ?? new AudioContext();
      this.audioCtx = ctx;
      const src = ctx.createMediaStreamSource(stream);
      const an = ctx.createAnalyser();
      an.fftSize = 256;
      src.connect(an);
      this.outAnalyser = an;
      const buf = new Uint8Array(an.frequencyBinCount);
      const cb = this.args.callbacks ?? {};
      this.outLevelTimer = window.setInterval(() => {
        an.getByteFrequencyData(buf);
        let sum = 0;
        for (let i = 0; i < buf.length; i++) sum += buf[i];
        const level = Math.min(1, sum / (buf.length * 255) * 2.5);
        cb.onLevel?.("out", level);
      }, 90);
    } catch {
      // intentionally swallow
    }
  }
}
