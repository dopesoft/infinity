// wake-bus.ts - window-event contract between the global wake-word button
// (WakeNavButton, lives in the header on every page) and the composer's
// voice session (ai-prompt-box.tsx, lives only on /live).
//
// Two signals, both fire-and-forget:
//   jarvis:wake         - the wake word was heard. If a composer is mounted
//                         it starts the realtime session; WakeNavButton also
//                         navigates to /live?voice=1 when on another page.
//   jarvis:voice-active - the composer's voice session took/released the
//                         mic. The wake listener suspends while true so two
//                         consumers never fight over the microphone.
//
// A window CustomEvent bus (not React context) on purpose: the header and
// the composer live in unrelated trees, and the contract is two booleans -
// context plumbing through the layout would be pure ceremony.

const WAKE_EVENT = "jarvis:wake";
const VOICE_ACTIVE_EVENT = "jarvis:voice-active";

export function emitWakeDetected(): void {
  window.dispatchEvent(new CustomEvent(WAKE_EVENT));
}

export function onWakeDetected(fn: () => void): () => void {
  const handler = () => fn();
  window.addEventListener(WAKE_EVENT, handler);
  return () => window.removeEventListener(WAKE_EVENT, handler);
}

export function emitVoiceActive(active: boolean): void {
  window.dispatchEvent(new CustomEvent(VOICE_ACTIVE_EVENT, { detail: active }));
}

export function onVoiceActive(fn: (active: boolean) => void): () => void {
  const handler = (e: Event) => fn(Boolean((e as CustomEvent).detail));
  window.addEventListener(VOICE_ACTIVE_EVENT, handler);
  return () => window.removeEventListener(VOICE_ACTIVE_EVENT, handler);
}
