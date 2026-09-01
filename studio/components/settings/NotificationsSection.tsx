"use client";

import { useCallback, useEffect, useState } from "react";
import { AlertCircle, BellOff, CheckCircle2, Send, Smartphone, Sparkles, Trash2 } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { SettingsPanel } from "@/components/settings/SettingsPanel";
import { GroupLabel, ListRow } from "@/components/ui/list-row";
import { Inset } from "@/components/ui/inset";
import { SettingRow } from "@/components/ui/setting-row";
import {
  fetchDevices,
  fetchPushPrefs,
  fetchVapidPublicKey,
  getStatus,
  isIos,
  isIosStandalone,
  isPushSupported,
  requestPermission,
  savePushPrefs,
  sendTestPush,
  subscribe,
  unsubscribe,
  type Device,
  type PushKindMeta,
  type PushPrefs,
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
    // Majordomo §1.3: the rail and the page title already said
    // "Notifications", so the duplicated heading + paragraph collapse into
    // the Section title and its count. The per-device and per-kind
    // descriptions below are decision aids and stay (§1.5).
    <SettingsPanel>
      <div className="min-w-0 space-y-3">
      <CapabilityBlock status={status} />

      <ActionBlock
        status={status}
        vapidConfigured={Boolean(vapid)}
        busy={busy}
        onSubscribe={onSubscribe}
        onUnsubscribe={onUnsubscribe}
      />

      {status?.subscribed && Boolean(vapid) && (
        <SettingRow
          label="Send a test banner"
          description="Fires one push to every registered device so you can confirm delivery."
          control={
            <Button variant="ghost" onClick={onTest} disabled={busy !== null} className="gap-1.5">
              {busy === "test" ? <Spinner className="size-3.5" /> : <Send className="size-3.5" />}
              Send test
            </Button>
          }
        />
      )}

      {testResult && (
        <p className="rounded-[8px] bg-success/10 px-3 py-2 text-[12px] text-success">{testResult}</p>
      )}
      {err && (
        <p className="rounded-[8px] bg-danger/10 px-3 py-2 text-[12px] text-danger">{err}</p>
      )}

      <DeviceList devices={devices} onRemove={refresh} />

      <PrefsBlock subscribed={Boolean(status?.subscribed)} />

      <WhyBlock />
      </div>
    </SettingsPanel>
  );
}

function PrefsBlock({ subscribed }: { subscribed: boolean }) {
  const [prefs, setPrefs] = useState<PushPrefs | null>(null);
  const [kinds, setKinds] = useState<PushKindMeta[]>([]);
  const [saving, setSaving] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      const res = await fetchPushPrefs();
      if (res) {
        setPrefs(res.prefs);
        setKinds(res.kinds);
      }
    })();
  }, []);

  async function onToggle(kind: string, next: boolean) {
    setSaving(kind);
    setErr(null);
    // Optimistic — the server merges so a quick double-tap stays sane.
    setPrefs((p) => ({ ...(p ?? {}), [kind]: next }));
    const res = await savePushPrefs({ [kind]: next });
    if (!res) {
      setErr("Couldn't save preference. Try again.");
      setPrefs((p) => ({ ...(p ?? {}), [kind]: !next }));
    } else {
      setPrefs(res.prefs);
    }
    setSaving(null);
  }

  if (kinds.length === 0 && !prefs) return null;

  return (
    <div className="min-w-0">
      <GroupLabel
        label="Notify me about"
        count={kinds.length}
        trailing={
          !subscribed ? (
            <span className="text-[11px] text-quiet">subscribe this device first</span>
          ) : undefined
        }
      />
      {kinds.map((k) => (
        <SettingRow
          key={k.kind}
          label={k.label}
          description={k.description}
          control={
            <Switch
              checked={prefs?.[k.kind] ?? false}
              disabled={saving === k.kind}
              onCheckedChange={(v) => void onToggle(k.kind, v)}
              aria-label={k.label}
            />
          }
        />
      ))}
      {err && (
        <p className="mt-2 rounded-[8px] bg-danger/10 px-3 py-2 text-[12px] text-danger">{err}</p>
      )}
    </div>
  );
}

function DeviceList({ devices, onRemove }: { devices: Device[]; onRemove: () => void }) {
  if (devices.length === 0) return null;
  return (
    <div className="min-w-0">
      <GroupLabel label="Devices" count={devices.length} />
      {devices.map((d) => (
        <ListRow
          key={d.id}
          tone={d.revoked ? "quiet" : d.lastSeenAt ? "success" : "warning"}
          leading={<Smartphone className="size-3.5" aria-hidden />}
          title={d.label || "Browser"}
          meta={d.revoked ? "revoked" : d.lastSeenAt ? "active" : "not yet delivered"}
          disabled={d.revoked}
          chevron={false}
          trailing={
            <Button
              size="icon"
              variant="ghost"
              className="size-9"
              onClick={() => {
                // For revoked rows the endpoint is truncated; we can still
                // unsubscribe-by-endpoint locally if this is the current
                // device. For other devices, surfacing a remote remove
                // would need an admin endpoint - for now, just hide the
                // local row + re-fetch.
                onRemove();
              }}
              aria-label="Refresh device list"
              title="Refresh"
            >
              <Trash2 className="size-4 text-quiet" aria-hidden />
            </Button>
          }
        />
      ))}
    </div>
  );
}

function CapabilityBlock({ status }: { status: PushStatus | null }) {
  if (!status) {
    return <p className="py-2 text-[13.5px] text-quiet">Checking browser capability…</p>;
  }
  const iosNotInstalled = isIos() && !isIosStandalone();
  return (
    <div className="min-w-0">
      <GroupLabel label="This device" />
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
    </div>
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
    <ListRow
      tone={ok ? "success" : warn ? "warning" : "quiet"}
      leading={
        ok ? (
          <CheckCircle2 className="size-4 text-success" aria-hidden />
        ) : warn ? (
          <AlertCircle className="size-4 text-warning" aria-hidden />
        ) : (
          <BellOff className="size-4 text-quiet" aria-hidden />
        )
      }
      title={label}
      meta={detail}
      chevron={false}
    />
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
      <Inset>
        This browser doesn&apos;t support Web Push. Try Chrome/Edge on desktop or Safari on iOS
        (after installing as PWA).
      </Inset>
    );
  }
  if (status.blocker === "ios-needs-install") {
    return (
      <Inset>
        <p className="flex items-center gap-1.5 text-[13.5px] font-medium text-foreground">
          <Smartphone className="size-3.5" aria-hidden />
          Install Studio on the Home Screen first
        </p>
        <ol className="ml-1 list-decimal space-y-1 pl-4 text-[11px] leading-relaxed text-muted-foreground">
          <li>Open this page in Safari (Chrome on iPhone can&apos;t do this).</li>
          <li>Tap the Share button.</li>
          <li>Choose <span className="font-semibold">Add to Home Screen</span>.</li>
          <li>Open Infinity from the Home Screen icon and come back here.</li>
        </ol>
      </Inset>
    );
  }
  if (!vapidConfigured) {
    return (
      <Inset>
        Push delivery isn&apos;t configured on Core yet - the VAPID public key is missing. Once
        it&apos;s set (<code className="font-mono text-[12px]">NEXT_PUBLIC_VAPID_PUBLIC_KEY</code>),
        this button activates.
      </Inset>
    );
  }
  return status.subscribed ? (
    <SettingRow
      label="Push on this device"
      description="Subscribed. Banners arrive when Jarvis needs you."
      control={
        <Button
          variant="ghost"
          onClick={onUnsubscribe}
          disabled={busy !== null}
          className="gap-1.5"
        >
          {busy === "unsubscribe" ? (
            <Spinner className="size-3.5" />
          ) : (
            <BellOff className="size-3.5" />
          )}
          Disable on this device
        </Button>
      }
    />
  ) : (
    <SettingRow
      label="Push on this device"
      description="Enable to get iOS-style banners on this device."
      control={
        <Button onClick={onSubscribe} disabled={busy !== null} className="gap-1.5">
          {busy === "subscribe" ? (
            <Spinner className="size-3.5" />
          ) : (
            <Sparkles className="size-3.5" />
          )}
          Enable notifications
        </Button>
      }
    />
  );
}

function WhyBlock() {
  return (
    <details className="min-w-0 py-2 text-[12.5px]">
      <summary className="cursor-pointer font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
        when does he interrupt me?
      </summary>
      {/* The bullet list that used to sit here restated every toggle above,
          which render a label AND a description straight from the server. It
          even ended by saying "each kind has its own toggle above", which is
          the tell. What is left is the two things the toggles cannot say. */}
      <p className="mt-2 text-[12.5px] text-muted-foreground">
        He stays quiet by default. New emails and run updates stay off until you switch
        them on.
      </p>
      <p className="mt-2 text-[12.5px] text-muted-foreground">
        Tap one and it opens the thing it is about.
      </p>
    </details>
  );
}


// Re-export so the bundle keeps tree-shake awareness of these helpers.
void isPushSupported;
