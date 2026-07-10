# Wake word — "hey Jarvis", hands-free

Three layers, from in-app to across-the-room. Layers 1–2 are shipped; layer 3
is a planned follow-up.

## How it works

The wake word is detected **fully on-device** by
[openWakeWord](https://github.com/dscripka/openWakeWord) (Apache-2.0) running
in-browser on onnxruntime-web. Its three-stage pipeline (melspectrogram →
speech embedding → the pretrained `hey_jarvis` head) scores 80ms audio chunks
locally — **no vendor account, no access key, and audio never leaves the
device**. On hearing "hey Jarvis" it starts the same GPT-Realtime voice
session the mic button does. When a voice session is active the wake listener
releases the mic; it re-arms when the session ends
(`studio/lib/voice/use-wake-word.ts`).

All assets are self-hosted under `studio/public/wake/`: the three ONNX models
plus the onnxruntime WASM runtime (loaded lazily — bosses who never enable
wake word pay zero bundle cost).

## Layer 1 — in-app, hands-free (shipped)

Tap the **ear icon** in the chat composer once (that tap grants the mic) — it
pulses while armed. Say **"hey Jarvis"** and the voice session opens. Phone on
the desk while you work, Mac tab pinned, etc. The toggle is per-device (a mic
affordance should be) and sticks across visits.

**iOS reality check:** browsers cannot keep a mic open when the app is
backgrounded or the screen locks. That's an OS restriction, not a bug —
foreground-only is the ceiling for any web app.

## Layer 2 — lock-screen entry: Siri Shortcut / Action Button (shipped)

For "start talking without touching the app", deep-link into voice mode:
**`https://infinity.dopesoft.io/live?voice=1`** auto-starts listening on
arrival (if iOS demands a fresh gesture after a cold start, the composer
shows its one-tap Retry — worst case is one tap). The PWA app icon also has a
long-press shortcut: **Talk to Jarvis**.

Siri Shortcut setup (once, ~1 minute, on the iPhone):

1. **Shortcuts** app → **+** → name it `Jarvis`.
2. Add action: **Open URLs** → `https://infinity.dopesoft.io/live?voice=1`.
3. Done. Now **"Hey Siri, Jarvis"** opens straight into a listening session.
4. Optional: Settings → **Action Button** → **Shortcut** → `Jarvis` for a
   physical push-to-talk button.

## Layer 3 — ambient, across the room (follow-up, not built)

A tiny always-on listener on the Mac (menubar app or launchd agent running
openWakeWord natively) that hears "hey Jarvis" room-scale and opens a voice
session. Needs: mic entitlement, a Supabase owner JWT to reach Core, and a
decision on speaker/mic routing. Tracked as the natural next step after
layers 1–2 prove out the habit.

## Tuning

Constants live at the top of `studio/lib/voice/wake.ts`: detection
`THRESHOLD` (default 0.5 — raise toward 0.7 if false triggers, lower toward
0.4 if it misses you), and `COOLDOWN_MS` refractory window (2.5s). The
`hey_jarvis_v0.1.onnx` head can be swapped for any other openWakeWord model
(or a custom-trained phrase) by replacing the file.
