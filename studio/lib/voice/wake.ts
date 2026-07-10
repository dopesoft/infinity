// wake.ts - on-device "hey Jarvis" wake-word detection via openWakeWord.
//
// Fully self-contained: the three-stage openWakeWord pipeline
// (melspectrogram → speech embedding → wake head, all Apache-2.0 ONNX
// models from github.com/dscripka/openWakeWord) runs in-browser on
// onnxruntime-web. No vendor account, no access key, no network after the
// first asset load - audio never leaves the device. Models + the ORT wasm
// runtime are self-hosted under /wake/ (studio/public/wake/).
//
// Mic capture rides @picovoice/web-voice-processor (Apache-2.0, keyless):
// it owns getUserMedia + resampling and hands us 512-sample Int16 frames
// at 16kHz. We buffer those into the 1280-sample (80ms) chunks the
// melspectrogram model expects.
//
// Pipeline constants (openWakeWord's streaming contract):
//   - mel model:  [1, 1280] float32 PCM → [1,1,T,32] mel frames,
//                 each value rescaled v/10 + 2
//   - embedding:  a sliding window of the last 76 mel frames → [1,96],
//                 recomputed every 8 new mel frames (stride)
//   - wake head:  the last 16 embeddings [1,16,96] → sigmoid score
//   - detection:  score ≥ threshold, then a refractory cooldown so one
//                 utterance can't double-fire
//
// The handoff contract with useVoice: the wake listener is PAUSED (mic
// released) whenever a voice session is active, and resumed when it ends -
// two consumers never fight over the mic (see use-wake-word.ts).

export type WakeStatus = "off" | "loading" | "listening" | "paused" | "error";

type OrtSession = import("onnxruntime-web").InferenceSession;

const WAKE_BASE = "/wake";
const CHUNK_SAMPLES = 1280; // 80ms @ 16kHz
const MEL_FRAME_DIMS = 32;
const EMB_WINDOW = 76; // mel frames per embedding
const EMB_STRIDE = 8; // new mel frames between embeddings
const EMB_DIMS = 96;
const WAKE_WINDOW = 16; // embeddings per wake-head inference
const THRESHOLD = 0.5;
const COOLDOWN_MS = 2500;

export class WakeWordListener {
  private mel: OrtSession | null = null;
  private emb: OrtSession | null = null;
  private head: OrtSession | null = null;
  private ort: typeof import("onnxruntime-web") | null = null;

  // Streaming buffers.
  private pcm: Int16Array = new Int16Array(0);
  private melFrames: Float32Array[] = [];
  private embFrames: Float32Array[] = [];
  private framesSinceEmb = 0;
  private processing = false;
  private cooldownUntil = 0;

  private subscribed = false;
  private stopped = false;
  private onStatus: (status: WakeStatus, error?: string) => void;
  private onWake: () => void;

  // The PvEngine WebVoiceProcessor pushes mic frames into.
  private engine = {
    onmessage: (e: MessageEvent) => {
      const data = e.data as { command?: string; inputFrame?: Int16Array };
      if (data?.command === "process" && data.inputFrame) {
        this.ingest(data.inputFrame);
      }
    },
  };

  constructor(opts: {
    onWake: () => void;
    onStatus: (status: WakeStatus, error?: string) => void;
  }) {
    this.onWake = opts.onWake;
    this.onStatus = opts.onStatus;
  }

  /** Load models and start listening. Safe to call once per instance. */
  async start(): Promise<void> {
    if (this.mel || this.stopped) return;
    this.onStatus("loading");
    try {
      // Load the self-hosted ORT browser bundle natively. The specifier is
      // a VARIABLE on purpose: webpackIgnore keeps webpack's bundler and
      // minifier away from the prebuilt ESM (both choke on it), and the
      // non-literal form keeps TypeScript from treating the URL as a
      // resolvable path. The npm dep stays for types only.
      const ortURL = `${WAKE_BASE}/ort/ort.min.mjs`;
      const ort = (await import(
        /* webpackIgnore: true */ ortURL
      )) as typeof import("onnxruntime-web");
      // Self-hosted runtime; single-threaded because Studio doesn't ship
      // COOP/COEP headers (threaded wasm needs cross-origin isolation).
      ort.env.wasm.wasmPaths = `${WAKE_BASE}/ort/`;
      ort.env.wasm.numThreads = 1;
      const [mel, emb, head] = await Promise.all([
        ort.InferenceSession.create(`${WAKE_BASE}/melspectrogram.onnx`),
        ort.InferenceSession.create(`${WAKE_BASE}/embedding_model.onnx`),
        ort.InferenceSession.create(`${WAKE_BASE}/hey_jarvis_v0.1.onnx`),
      ]);
      if (this.stopped) return; // raced by stop() while loading
      this.ort = ort;
      this.mel = mel;
      this.emb = emb;
      this.head = head;
      const { WebVoiceProcessor } = await import("@picovoice/web-voice-processor");
      await WebVoiceProcessor.subscribe(this.engine);
      this.subscribed = true;
      this.onStatus("listening");
    } catch (err) {
      this.onStatus("error", err instanceof Error ? err.message : String(err));
    }
  }

  /** Release the mic (voice session taking over) but keep models warm. */
  async pause(): Promise<void> {
    if (!this.subscribed) return;
    try {
      const { WebVoiceProcessor } = await import("@picovoice/web-voice-processor");
      await WebVoiceProcessor.unsubscribe(this.engine);
      this.subscribed = false;
      this.resetBuffers();
      this.onStatus("paused");
    } catch {
      // Losing a pause is harmless; the voice session owns the mic anyway.
    }
  }

  /** Re-acquire the mic after a voice session ends. */
  async resume(): Promise<void> {
    if (this.subscribed || this.stopped || !this.mel) return;
    try {
      const { WebVoiceProcessor } = await import("@picovoice/web-voice-processor");
      await WebVoiceProcessor.subscribe(this.engine);
      this.subscribed = true;
      this.onStatus("listening");
    } catch (err) {
      this.onStatus("error", err instanceof Error ? err.message : String(err));
    }
  }

  /** Full teardown - mic released, ONNX sessions freed. */
  async stop(): Promise<void> {
    this.stopped = true;
    try {
      if (this.subscribed) {
        const { WebVoiceProcessor } = await import("@picovoice/web-voice-processor");
        await WebVoiceProcessor.unsubscribe(this.engine);
      }
    } catch {
      // Best-effort - session release below reclaims the real resources.
    }
    this.subscribed = false;
    this.resetBuffers();
    const sessions = [this.mel, this.emb, this.head];
    this.mel = this.emb = this.head = null;
    for (const s of sessions) {
      try {
        await s?.release();
      } catch {
        // Already released or never created.
      }
    }
    this.onStatus("off");
  }

  private resetBuffers(): void {
    this.pcm = new Int16Array(0);
    this.melFrames = [];
    this.embFrames = [];
    this.framesSinceEmb = 0;
  }

  /** Accumulate mic frames; drain in 1280-sample chunks, one at a time. */
  private ingest(frame: Int16Array): void {
    const joined = new Int16Array(this.pcm.length + frame.length);
    joined.set(this.pcm);
    joined.set(frame, this.pcm.length);
    this.pcm = joined;
    if (!this.processing) void this.drain();
  }

  private async drain(): Promise<void> {
    this.processing = true;
    try {
      while (this.pcm.length >= CHUNK_SAMPLES && this.mel && !this.stopped) {
        const chunk = this.pcm.slice(0, CHUNK_SAMPLES);
        this.pcm = this.pcm.slice(CHUNK_SAMPLES);
        await this.processChunk(chunk);
      }
    } catch (err) {
      this.onStatus("error", err instanceof Error ? err.message : String(err));
    } finally {
      this.processing = false;
    }
  }

  private async processChunk(chunk: Int16Array): Promise<void> {
    const ort = this.ort;
    const mel = this.mel;
    const emb = this.emb;
    const head = this.head;
    if (!ort || !mel || !emb || !head) return;

    // 1. PCM → mel frames.
    const f32 = new Float32Array(CHUNK_SAMPLES);
    for (let i = 0; i < CHUNK_SAMPLES; i++) f32[i] = chunk[i] / 32768;
    const melOut = await runSingle(ort, mel, new ort.Tensor("float32", f32, [1, CHUNK_SAMPLES]));
    const newFrames = melOut.length / MEL_FRAME_DIMS;
    for (let t = 0; t < newFrames; t++) {
      const frame = new Float32Array(MEL_FRAME_DIMS);
      for (let d = 0; d < MEL_FRAME_DIMS; d++) {
        // openWakeWord's fixed rescale of the mel model's output.
        frame[d] = melOut[t * MEL_FRAME_DIMS + d] / 10 + 2;
      }
      this.melFrames.push(frame);
      this.framesSinceEmb++;
    }
    if (this.melFrames.length > EMB_WINDOW + EMB_STRIDE * 4) {
      this.melFrames.splice(0, this.melFrames.length - (EMB_WINDOW + EMB_STRIDE * 4));
    }

    // 2. Every EMB_STRIDE new mel frames: embed the last 76-frame window.
    while (this.melFrames.length >= EMB_WINDOW && this.framesSinceEmb >= EMB_STRIDE) {
      this.framesSinceEmb -= EMB_STRIDE;
      const window = this.melFrames.slice(-EMB_WINDOW);
      const flat = new Float32Array(EMB_WINDOW * MEL_FRAME_DIMS);
      for (let t = 0; t < EMB_WINDOW; t++) flat.set(window[t], t * MEL_FRAME_DIMS);
      const embOut = await runSingle(
        ort,
        emb,
        new ort.Tensor("float32", flat, [1, EMB_WINDOW, MEL_FRAME_DIMS, 1]),
      );
      this.embFrames.push(Float32Array.from(embOut.slice(0, EMB_DIMS)));
      if (this.embFrames.length > WAKE_WINDOW) {
        this.embFrames.splice(0, this.embFrames.length - WAKE_WINDOW);
      }

      // 3. Wake head over the last 16 embeddings.
      if (this.embFrames.length === WAKE_WINDOW) {
        const wakeIn = new Float32Array(WAKE_WINDOW * EMB_DIMS);
        for (let t = 0; t < WAKE_WINDOW; t++) wakeIn.set(this.embFrames[t], t * EMB_DIMS);
        const score = (
          await runSingle(ort, head, new ort.Tensor("float32", wakeIn, [1, WAKE_WINDOW, EMB_DIMS]))
        )[0];
        const now = Date.now();
        if (score >= THRESHOLD && now >= this.cooldownUntil) {
          this.cooldownUntil = now + COOLDOWN_MS;
          this.onWake();
        }
      }
    }
  }
}

// runSingle feeds a session's sole input and returns its sole output's data,
// resolving input/output names dynamically so model regenerations that
// rename tensors don't break us.
async function runSingle(
  ort: typeof import("onnxruntime-web"),
  session: OrtSession,
  tensor: import("onnxruntime-web").Tensor,
): Promise<Float32Array> {
  const results = await session.run({ [session.inputNames[0]]: tensor });
  const out = results[session.outputNames[0]];
  return out.data as Float32Array;
}
