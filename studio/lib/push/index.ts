"use client";

import { authedFetch } from "@/lib/api";

/* Push subscription client helpers.
 *
 * The flow:
 *   1. registerServiceWorker() — called once on app mount (PWARegister).
 *   2. requestPermission()    — user-initiated tap in Settings, prompts
 *                                the browser for notification permission.
 *   3. subscribe()             — once permission is granted, registers a
 *                                PushSubscription with the user's browser
 *                                push service (FCM / Apple) using our
 *                                VAPID public key, then POSTs the resulting
 *                                endpoint+keys to Core.
 *   4. unsubscribe()           — local + server cleanup when the boss
 *                                disables push for the current device.
 *
 * VAPID public key flows from the server via /api/push/vapid (set as
 * NEXT_PUBLIC_VAPID_PUBLIC_KEY at build time OR fetched at runtime — we
 * support both). Until the key is provisioned, the UI shows a clear
 * "not configured" state instead of attempting a doomed subscribe.
 */

export type PushPermission = "default" | "granted" | "denied";

export type PushStatus = {
  supported: boolean;
  permission: PushPermission;
  subscribed: boolean;
  endpoint?: string;
  // Reason a subscribe attempt would fail right now — used to render
  // a helpful empty state in Settings instead of a generic error.
  blocker?:
    | null
    | "no-service-worker"
    | "no-permission"
    | "no-vapid-key"
    | "ios-needs-install";
};

export function isPushSupported(): boolean {
  if (typeof window === "undefined") return false;
  return (
    "serviceWorker" in navigator &&
    "PushManager" in window &&
    "Notification" in window
  );
}

/* iOS Safari only allows Web Push when the page is launched from the
 * home screen (i.e. installed as a PWA). Without that, PushManager exists
 * but subscribe() fails silently. We detect the standalone display-mode
 * to gate the UI. */
export function isIosStandalone(): boolean {
  if (typeof window === "undefined") return false;
  const standalone = window.matchMedia("(display-mode: standalone)").matches;
  const iosLegacy = (window.navigator as unknown as { standalone?: boolean }).standalone === true;
  return standalone || iosLegacy;
}

export function isIos(): boolean {
  if (typeof window === "undefined") return false;
  const ua = window.navigator.userAgent;
  return /iPad|iPhone|iPod/.test(ua) && !(window as unknown as { MSStream?: unknown }).MSStream;
}

export async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!isPushSupported()) return null;
  try {
    return await navigator.serviceWorker.register("/sw.js", { scope: "/" });
  } catch {
    return null;
  }
}

/* fetchVapidPublicKey hits Core's /api/push/vapid endpoint so Studio can
 * subscribe even when NEXT_PUBLIC_VAPID_PUBLIC_KEY isn't baked in at
 * build time. Returns "" when Core has no key configured (or is
 * unreachable) so callers can render "not configured" gracefully. */
export async function fetchVapidPublicKey(): Promise<string> {
  try {
    const res = await authedFetch("/api/push/vapid");
    if (!res.ok) return "";
    const body = (await res.json()) as { publicKey?: string };
    return body.publicKey ?? "";
  } catch {
    return "";
  }
}

export type Device = {
  id: string;
  endpoint: string;
  label: string;
  userAgent: string;
  revoked: boolean;
  lastSeenAt?: string;
  createdAt: string;
};

export async function fetchDevices(): Promise<Device[]> {
  try {
    const res = await authedFetch("/api/push/devices");
    if (!res.ok) return [];
    const body = (await res.json()) as { devices?: Device[] };
    return body.devices ?? [];
  } catch {
    return [];
  }
}

export async function sendTestPush(opts?: {
  title?: string;
  body?: string;
  url?: string;
}): Promise<{ sent: number; results: Array<{ endpoint: string; status?: number; error?: string }> } | null> {
  try {
    const res = await authedFetch("/api/push/test", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(opts ?? {}),
    });
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

export async function getStatus(vapidPublicKey?: string): Promise<PushStatus> {
  if (!isPushSupported()) {
    return { supported: false, permission: "default", subscribed: false, blocker: "no-service-worker" };
  }
  const permission = Notification.permission as PushPermission;
  const reg = await navigator.serviceWorker.getRegistration("/");
  if (!reg) {
    return { supported: true, permission, subscribed: false, blocker: "no-service-worker" };
  }
  const sub = await reg.pushManager.getSubscription();
  let blocker: PushStatus["blocker"] = null;
  if (isIos() && !isIosStandalone()) blocker = "ios-needs-install";
  else if (!vapidPublicKey) blocker = "no-vapid-key";
  else if (permission !== "granted") blocker = "no-permission";
  return {
    supported: true,
    permission,
    subscribed: !!sub,
    endpoint: sub?.endpoint,
    blocker,
  };
}

export async function requestPermission(): Promise<PushPermission> {
  if (!("Notification" in window)) return "denied";
  if (Notification.permission === "granted") return "granted";
  const result = await Notification.requestPermission();
  return result as PushPermission;
}

export async function subscribe(vapidPublicKey: string): Promise<PushSubscription | null> {
  const reg = await navigator.serviceWorker.ready;
  const sub = await reg.pushManager.subscribe({
    userVisibleOnly: true,
    // Cast: modern TS narrows Uint8Array<ArrayBufferLike> which is wider
    // than the strict BufferSource the lib.d.ts requires here. Runtime
    // contract is identical.
    applicationServerKey: urlBase64ToUint8Array(vapidPublicKey) as unknown as BufferSource,
  });
  // Persist to the server. The Core push package writes a row to
  // mem_push_subscriptions and starts targeting this device for any
  // future notification fires.
  try {
    await authedFetch("/api/push/subscribe", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        endpoint: sub.endpoint,
        keys: extractKeys(sub),
        userAgent: navigator.userAgent,
      }),
    });
  } catch {
    // If the server roundtrip fails we still return the subscription —
    // Settings will show "subscribed locally, server sync pending" so
    // the user can retry sync without re-prompting browser permission.
  }
  return sub;
}

export async function unsubscribe(): Promise<boolean> {
  const reg = await navigator.serviceWorker.getRegistration("/");
  if (!reg) return false;
  const sub = await reg.pushManager.getSubscription();
  if (!sub) return false;
  // Tell the server first so it stops sending to a dead endpoint, then
  // tear down locally. If the server roundtrip fails, we still kill
  // the local subscription — better to silence notifications than to
  // half-disable.
  try {
    await authedFetch("/api/push/unsubscribe", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ endpoint: sub.endpoint }),
    });
  } catch {
    // ignore — local unsubscribe still proceeds
  }
  return sub.unsubscribe();
}

function extractKeys(sub: PushSubscription): { p256dh: string; auth: string } {
  // PushSubscription.toJSON() returns the same keys shape browsers expect.
  const j = sub.toJSON() as { keys?: { p256dh?: string; auth?: string } };
  return {
    p256dh: j.keys?.p256dh ?? "",
    auth: j.keys?.auth ?? "",
  };
}

/* Web push VAPID keys arrive as a URL-safe base64 string from Core.
 * The PushManager.subscribe contract wants a Uint8Array — convert. */
function urlBase64ToUint8Array(base64: string): Uint8Array {
  const padding = "=".repeat((4 - (base64.length % 4)) % 4);
  const padded = (base64 + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(padded);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; ++i) out[i] = raw.charCodeAt(i);
  return out;
}
