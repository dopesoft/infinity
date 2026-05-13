/*
 * Infinity service worker.
 *
 * Scope:
 *   - Receive Web Push payloads from Core and show them as system
 *     notifications (banner on macOS/iOS, lockscreen on iOS, badge on
 *     dock/home).
 *   - On tap, focus an existing Studio window OR open Studio at the
 *     deep-link encoded in the payload (e.g. /trust/:id, /code-proposals,
 *     a saved item, etc).
 *   - Skip-waiting + claim on install so a fresh push routing pipeline
 *     activates immediately without the user closing all tabs.
 *
 * Non-goals (deliberate):
 *   - We do NOT cache app shell. Studio talks to Core through dynamic
 *     APIs and a stale shell would mask deploys. Offline-mode is a
 *     future, opt-in layer.
 *   - We do NOT intercept fetches. The agent's real-time tools depend
 *     on always-fresh requests; caching here would be a footgun.
 */

const VERSION = "infinity-sw-1";

self.addEventListener("install", (event) => {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("push", (event) => {
  // Push payloads are JSON sent by Core's webpush sender. Each payload
  // describes ONE notification — no batching here (Core batches before
  // it sends if needed).
  //
  //   {
  //     title:   "Approve: git rebase main",
  //     body:    "claude_code__Bash — needs your call",
  //     tag:     "trust:a-bash-rebase",  // dedupes repeat notifs
  //     url:     "/trust/a-bash-rebase",  // deep link on tap
  //     icon:    "/icon.svg",
  //     badge:   "/icon.svg",
  //     data:    { kind: "trust", id: "..." }
  //   }
  let payload = {};
  try {
    payload = event.data ? event.data.json() : {};
  } catch {
    payload = { title: "Infinity", body: event.data ? event.data.text() : "" };
  }

  const title = payload.title || "Infinity";
  const options = {
    body: payload.body || "",
    tag: payload.tag,
    icon: payload.icon || "/icon.svg",
    badge: payload.badge || "/icon.svg",
    data: { url: payload.url || "/", kind: payload.kind, id: payload.id, ...(payload.data || {}) },
    // renotify=true means a repeat notification with the same tag still
    // alerts (sound + vibrate) rather than silently replacing.
    renotify: Boolean(payload.renotify),
    requireInteraction: Boolean(payload.requireInteraction),
    silent: Boolean(payload.silent),
  };

  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || "/";

  event.waitUntil(
    (async () => {
      // If a Studio window is already open, focus it and route there.
      const all = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      for (const c of all) {
        // Try to find a window already on this URL or on the Studio origin.
        if (c.url.includes(url)) {
          await c.focus();
          return;
        }
      }
      // No matching window — focus any Studio window and navigate, or
      // open a fresh one.
      for (const c of all) {
        await c.focus();
        if ("navigate" in c) {
          try {
            await c.navigate(url);
            return;
          } catch {
            // navigate() can reject for cross-origin; fall through.
          }
        }
      }
      await self.clients.openWindow(url);
    })(),
  );
});
