"use client";

import { useCallback, useEffect, useState } from "react";
import {
  AlertCircle,
  Bell,
  BellOff,
  CheckCircle2,
  Loader2,
  Send,
  Smartphone,
  Sparkles,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  fetchDevices,
  fetchVapidPublicKey,
  getStatus,
  isIos,
  isIosStandalone,
  isPushSupported,
  requestPermission,
  sendTestPush,
  subscribe,
  unsubscribe,
  type Device,
  type PushStatus,
} from "@/lib/push";

/* Notifications settings.
 *
 * Renders three blocks:
 *   1. Capability - what the current device can do (browser support,
 *      permission state, PWA install state on iOS).
 *   2. Subscribe / unsubscribe - primary action for this device.
 *   3. Why bother - small explainer of what Jarvis pushes for, so the
 *      boss can decide whether to grant permission.
 *
 * VAPID public key flows in via NEXT_PUBLIC_VAPID_PUBLIC_KEY at build
 * time. When it's missing we still render the section so the boss can
 * see the install state, but the subscribe button explains why it's
 * disabled instead of throwing.
 */

const BUILD_VAPID = process.env.NEXT_PUBLIC_VAPID_PUBLIC_KEY ?? "";

export function NotificationsSection() {
  const [status, setStatus] = useState<PushStatus | null>(null);
  const [vapid, setVapid] = useState<string>(BUILD_VAPID);
  const [devices, setDevices] = useState<Device[]>([]);
  const [busy, setBusy] = useState<"subscribe" | "unsubscribe" | "permission" | "test" | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<string | null>(null);

  // Resolve VAPID: build-time first (fastest), runtime fetch as fallback
  // so the key can be provisioned in Core without a Studio rebuild.
  useEffect(() => {
    if (BUILD_VAPID) return;
    void (async () => {
      const key = await fetchVapidPublicKey();
      if (key) setVapid(key);
    })();
  }, []);

  const refresh = useCallback(async () => {
    const [s, d] = await Promise.all([
      getStatus(vapid || undefined),
      fetchDevices(),
    ]);
    setStatus(s);
    setDevices(d);
  }, [vapid]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function onSubscribe() {
    setBusy("subscribe");
    setErr(null);
    try {
      if (Notification.permission !== "granted") {
        const perm = await requestPermission();
        if (perm !== "granted") {
          setErr("Notification permission was denied. Enable it in browser settings to continue.");
          return;
        }
      }
      if (!vapid) {
        setErr("Push isn't configured on Core yet - VAPID public key is missing.");
        return;
      }
      const sub = await subscribe(vapid);
      if (!sub) setErr("Subscribe failed. Check your browser's notification settings.");
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  async function onTest() {
    setBusy("test");
    setTestResult(null);
    setErr(null);
    try {
      const res = await sendTestPush({
        title: "Hello from Jarvis",
        body: "Push notifications are working end-to-end.",
        url: "/",
      });
      if (!res) {
        setErr("Test push failed - Core may be unreachable or not configured.");
      } else if (res.sent === 0) {
        setTestResult("No devices to deliver to. Subscribe at least one device first.");
      } else {
        const ok = res.results.filter((r) => !r.error).length;
        const failed = res.results.length - ok;
        setTestResult(
          failed === 0
            ? `Sent to ${ok} device${ok === 1 ? "" : "s"} - check your banners.`
            : `Sent to ${ok}, failed on ${failed}.`,
        );
      }
      await refresh();
    } finally {
      setBusy(null);
    }
  }

  async function onUnsubscribe() {
    setBusy("unsubscribe");
    setErr(null);
    try {
      const ok = await unsubscribe();
      if (!ok) setErr("Couldn't unsubscribe - try again, or reset permissions in browser.");
      await refresh();
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="space-y-4">
      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <Bell className="size-4 text-muted-foreground" aria-hidden />
          <h2 className="text-base font-semibold tracking-tight">Notifications</h2>
        </div>
        <p className="text-xs text-muted-foreground">
          Receive iOS-style banners on your iPhone and Mac when Jarvis needs
          you. Each device is subscribed independently - enable on every
          device you want pushes on.
        </p>
      </div>

      <CapabilityBlock status={status} />

      <ActionBlock
        status={status}
        vapidConfigured={Boolean(vapid)}
        busy={busy}
        onSubscribe={onSubscribe}
        onUnsubscribe={onUnsubscribe}
      />

      {status?.subscribed && Boolean(vapid) && (
        <div className="flex flex-wrap items-center justify-end gap-2 rounded-md border bg-background p-3">
          <p className="mr-auto text-[12px] text-muted-foreground">
            Send a test banner to every registered device.
          </p>
          <Button variant="ghost" onClick={onTest} disabled={busy !== null} className="gap-1.5">
            {busy === "test" ? <Loader2 className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}
            Send test
          </Button>
        </div>
      )}

      {testResult && (
        <p className="rounded-sm bg-success/10 p-2 text-[11px] text-success">{testResult}</p>
      )}
      {err && (
        <p className="rounded-sm bg-danger/10 p-2 text-[11px] text-danger">{err}</p>
      )}

      <DeviceList devices={devices} onRemove={refresh} />

      <WhyBlock />
    </div>
  );
}

function DeviceList({ devices, onRemove }: { devices: Device[]; onRemove: () => void }) {
  if (devices.length === 0) return null;
  return (
    <div className="space-y-2">
      <h3 className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
        Devices · {devices.length}
      </h3>
      <ul className="space-y-1.5">
        {devices.map((d) => (
          <li
            key={d.id}
            className={cn(
              "flex items-center gap-2 rounded-md border bg-background px-3 py-2 text-[12px]",
              d.revoked && "opacity-60",
            )}
          >
            <Smartphone className="size-3.5 text-muted-foreground" aria-hidden />
            <div className="min-w-0 flex-1">
              <p className="truncate text-foreground">{d.label || "Browser"}</p>
              <p className="font-mono text-[10px] text-muted-foreground">
                {d.revoked ? "revoked" : d.lastSeenAt ? "active" : "not yet delivered"}
              </p>
            </div>
            <button
              type="button"
              onClick={async () => {
                // For revoked rows the endpoint is truncated; we can still
                // unsubscribe-by-endpoint locally if this is the current
                // device. For other devices, surfacing a remote remove
                // would need an admin endpoint - for now, just hide the
                // local row + re-fetch.
                onRemove();
              }}
              className="text-muted-foreground hover:text-foreground"
              aria-label="Refresh device list"
              title="Refresh"
            >
              <Trash2 className="size-3.5" aria-hidden />
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function CapabilityBlock({ status }: { status: PushStatus | null }) {
  if (!status) {
    return (
      <div className="rounded-md border bg-background p-3 text-xs text-muted-foreground">
        Checking browser capability…
      </div>
    );
  }
  const iosNotInstalled = isIos() && !isIosStandalone();
  return (
    <ul className="space-y-1.5 rounded-md border bg-background p-3 text-[12px]">
      <CapRow
        label="Browser support"
        ok={status.supported}
        detail={status.supported ? "Push API + service worker available." : "This browser doesn't support Web Push."}
      />
      {isIos() && (
        <CapRow
          label="Installed as PWA"
          ok={isIosStandalone()}
          detail={
            isIosStandalone()
              ? "Running standalone - iOS Web Push works on this device."
              : "On iPhone you must add Studio to the Home Screen (Share → Add to Home Screen), then open it from the icon. Web Push only works inside that installed mode."
          }
        />
      )}
      <CapRow
        label="Permission"
        ok={status.permission === "granted"}
        warn={status.permission === "default"}
        detail={
          status.permission === "granted"
            ? "Granted - banners will appear."
            : status.permission === "denied"
              ? "Denied. Reset in browser settings."
              : "Not asked yet. Tap Enable below."
        }
      />
      <CapRow
        label="Subscribed on this device"
        ok={status.subscribed}
        detail={
          status.subscribed
            ? "This device is receiving pushes."
            : iosNotInstalled
              ? "Install as PWA first."
              : "Not yet subscribed."
        }
      />
    </ul>
  );
}

function CapRow({
  label,
  ok,
  warn,
  detail,
}: {
  label: string;
  ok: boolean;
  warn?: boolean;
  detail: string;
}) {
  return (
    <li className="flex items-start gap-2">
      <span className="mt-0.5 inline-flex size-4 shrink-0 items-center justify-center">
        {ok ? (
          <CheckCircle2 className="size-4 text-success" aria-hidden />
        ) : warn ? (
          <AlertCircle className="size-4 text-rose-400" aria-hidden />
        ) : (
          <BellOff className="size-4 text-muted-foreground" aria-hidden />
        )}
      </span>
      <div className="min-w-0 flex-1">
        <p className="text-[12px] font-medium text-foreground">{label}</p>
        <p className="text-[11px] leading-relaxed text-muted-foreground">{detail}</p>
      </div>
    </li>
  );
}

function ActionBlock({
  status,
  vapidConfigured,
  busy,
  onSubscribe,
  onUnsubscribe,
}: {
  status: PushStatus | null;
  vapidConfigured: boolean;
  busy: "subscribe" | "unsubscribe" | "permission" | "test" | null;
  onSubscribe: () => void;
  onUnsubscribe: () => void;
}) {
  if (!status?.supported) {
    return (
      <div className="rounded-md border border-dashed bg-muted/20 p-3 text-[12px] text-muted-foreground">
        This browser doesn&apos;t support Web Push. Try Chrome/Edge on desktop
        or Safari on iOS (after installing as PWA).
      </div>
    );
  }
  if (status.blocker === "ios-needs-install") {
    return (
      <div className="space-y-2 rounded-md border border-dashed bg-muted/20 p-3">
        <p className="flex items-center gap-1.5 text-[12px] font-medium text-foreground">
          <Smartphone className="size-3.5" aria-hidden />
          Install Studio on the Home Screen first
        </p>
        <ol className="ml-1 list-decimal space-y-1 pl-4 text-[11px] leading-relaxed text-muted-foreground">
          <li>Open this page in Safari (Chrome on iPhone can&apos;t do this).</li>
          <li>Tap the Share button.</li>
          <li>Choose <span className="font-semibold">Add to Home Screen</span>.</li>
          <li>Open Infinity from the Home Screen icon and come back here.</li>
        </ol>
      </div>
    );
  }
  if (!vapidConfigured) {
    return (
      <div className="rounded-md border border-dashed bg-muted/20 p-3 text-[12px] text-muted-foreground">
        Push delivery isn&apos;t configured on Core yet - the VAPID public
        key is missing. Once it&apos;s set (
        <code className="font-mono">NEXT_PUBLIC_VAPID_PUBLIC_KEY</code>),
        this button activates.
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center justify-end gap-2 rounded-md border bg-background p-3">
      {status.subscribed ? (
        <>
          <p className="mr-auto text-[12px] text-muted-foreground">
            Subscribed on this device. Banners arrive when Jarvis needs you.
          </p>
          <Button
            variant="ghost"
            onClick={onUnsubscribe}
            disabled={busy !== null}
            className="gap-1.5"
          >
            {busy === "unsubscribe" ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <BellOff className="size-3.5" />
            )}
            Disable on this device
          </Button>
        </>
      ) : (
        <>
          <p className="mr-auto text-[12px] text-muted-foreground">
            Enable to get iOS-style banners on this device.
          </p>
          <Button onClick={onSubscribe} disabled={busy !== null} className="gap-1.5">
            {busy === "subscribe" ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <Sparkles className="size-3.5" />
            )}
            Enable notifications
          </Button>
        </>
      )}
    </div>
  );
}

function WhyBlock() {
  return (
    <details className="rounded-md border bg-muted/20 p-3 text-[12px]">
      <summary className="cursor-pointer font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
        what triggers a notification?
      </summary>
      <ul className="mt-2 space-y-1.5 text-foreground/80">
        <Bullet>
          <span className="font-semibold">Trust requests</span> - Jarvis wants
          to run a bash command, edit, or write that needs your approval.
        </Bullet>
        <Bullet>
          <span className="font-semibold">Curiosity questions</span> - Jarvis
          can&apos;t make a call without clarification.
        </Bullet>
        <Bullet>
          <span className="font-semibold">Code proposals</span> - Voyager
          drafted a refactor based on file-fight detection.
        </Bullet>
        <Bullet>
          <span className="font-semibold">Sentinel fires</span> - a watcher
          tripped (GitHub Actions red, log error spike, etc).
        </Bullet>
        <Bullet>
          <span className="font-semibold">Cron output worth seeing</span> -
          morning brief delivered, GH digest ready.
        </Bullet>
      </ul>
      <p className="mt-2 text-[11px] italic text-muted-foreground">
        Tap a notification to deep-link to the matching record in Studio.
      </p>
    </details>
  );
}

function Bullet({ children }: { children: React.ReactNode }) {
  return (
    <li className="flex gap-2 leading-relaxed">
      <span className="text-muted-foreground">·</span>
      <span className="flex-1">{children}</span>
    </li>
  );
}

// Re-export so the bundle keeps tree-shake awareness of these helpers.
void isPushSupported;
