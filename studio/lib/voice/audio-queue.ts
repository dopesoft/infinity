// AudioQueue plays Jarvis's spoken reply, clip by clip, in order. Core
// synthesizes the agent loop's text one sentence at a time and ships each as a
// base64 `voice_audio` WS frame; we decode and play them back-to-back through a
// single AudioContext so it sounds like one continuous voice.
//
// Barge-in is the load-bearing operation: stop() cuts playback instantly and
// drops everything queued. That's what lets the boss talk over Jarvis - the
// realtime mic layer fires the interrupt, we kill the audio here in the same
// tick, and the new utterance goes to the brain.
//
// iOS Safari: an AudioContext must be created/resumed inside a user gesture.
// The mic tap that starts the voice session is that gesture, so construct the
// queue then (resume() is a no-op when already running).
export class AudioQueue {
  private ctx: AudioContext | null = null;
  private pending: ArrayBuffer[] = [];
  private current: AudioBufferSourceNode | null = null;
  private playing = false;
  // Bumped on every stop(); a decode that resolves after a barge-in checks its
  // captured epoch against this and drops itself, so a clip decoded just before
  // the interrupt can't sneak into playback afterward.
  private epoch = 0;
  private closed = false;

  constructor() {
    try {
      const Ctx =
        window.AudioContext ||
        (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
      this.ctx = Ctx ? new Ctx() : null;
      void this.ctx?.resume();
    } catch {
      this.ctx = null;
    }
  }

  /** Queue one base64-encoded audio clip (mp3) for playback. */
  enqueue(base64: string): void {
    if (this.closed || !this.ctx || !base64) return;
    const bytes = base64ToArrayBuffer(base64);
    if (!bytes) return;
    this.pending.push(bytes);
    void this.pump();
  }

  /** Barge-in: stop the current clip immediately and drop the queue. Safe to
   *  call repeatedly. Does NOT close the context - the session is still live
   *  and may speak again on the next turn. */
  stop(): void {
    this.epoch++;
    this.pending = [];
    this.playing = false;
    if (this.current) {
      try {
        this.current.onended = null;
        this.current.stop();
      } catch {
        // already stopped / not started - fine
      }
      this.current = null;
    }
  }

  /** Tear down at end of the voice session. */
  close(): void {
    this.closed = true;
    this.stop();
    try {
      void this.ctx?.close();
    } catch {
      // ignore
    }
    this.ctx = null;
  }

  private async pump(): Promise<void> {
    if (this.playing || this.closed || !this.ctx) return;
    const next = this.pending.shift();
    if (!next) return;
    this.playing = true;
    const epochAtDecode = this.epoch;
    let buffer: AudioBuffer;
    try {
      // decodeAudioData detaches the ArrayBuffer; slice() hands it a private
      // copy so a retry/inspect elsewhere can't hit a detached buffer.
      buffer = await this.ctx.decodeAudioData(next.slice(0));
    } catch {
      // Bad/clipped audio - skip this clip, keep the stream going.
      this.playing = false;
      void this.pump();
      return;
    }
    // A barge-in (or close) happened while we were decoding - discard.
    if (this.closed || !this.ctx || epochAtDecode !== this.epoch) {
      this.playing = false;
      return;
    }
    const src = this.ctx.createBufferSource();
    src.buffer = buffer;
    src.connect(this.ctx.destination);
    this.current = src;
    src.onended = () => {
      if (this.current === src) this.current = null;
      this.playing = false;
      void this.pump();
    };
    try {
      src.start();
    } catch {
      this.playing = false;
      void this.pump();
    }
  }
}

function base64ToArrayBuffer(b64: string): ArrayBuffer | null {
  try {
    const binary = atob(b64);
    const len = binary.length;
    const bytes = new Uint8Array(len);
    for (let i = 0; i < len; i++) bytes[i] = binary.charCodeAt(i);
    return bytes.buffer;
  } catch {
    return null;
  }
}
