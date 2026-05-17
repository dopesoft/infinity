"use client";

import { useEffect, useRef, useState } from "react";
import { Check, Loader2, RefreshCw, X } from "lucide-react";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  fetchBridgeSession,
  fetchBridgeStatus,
  refreshBridgeStatus,
  setBridgePreference,
  type BridgeSessionView,
  type BridgeStatus,
} from "@/lib/canvas/api";

/**
 * BridgePill - persistent indicator + per-session control for the two-bridge
 * router. Shows which workspace (Mac vs Railway Cloud) is currently
 * fulfilling `bridge_*` tool calls for the active session, and lets the
 * boss pin a preference (auto / mac / cloud) via a Dialog (lg+) or Drawer
 * (<lg) modal.
 *
 * Polls the per-session endpoint every 10s to catch router decisions
 * flipping mid-session (e.g. Mac goes to sleep → Auto falls back to
 * Cloud). When `active_kind` changes vs. the previously-rendered value,
 * we surface an in-app toast so the swap isn't silent.
 *
 * When `sessionId` is null the pill renders a muted placeholder and
 * does not poll - there's no session to scope the router decision to.
 */
type Preference = "auto" | "mac" | "cloud";

type Tone = "success" | "info" | "warning" | "danger" | "muted";

const POLL_MS = 10_000;

export function BridgePill({ sessionId }: { sessionId: string | null }) {
  const [status, setStatus] = useState<BridgeStatus | null>(null);
  const [session, setSession] = useState<BridgeSessionView | null>(null);
  const [open, setOpen] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [savingPref, setSavingPref] = useState<Preference | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const previousKindRef = useRef<"mac" | "cloud" | undefined>(undefined);
  const sawFirstLoadRef = useRef(false);

  // Global status - fetched once on mount, refreshable.
  useEffect(() => {
    const ac = new AbortController();
    void (async () => {
      const s = await fetchBridgeStatus(ac.signal);
      if (s) setStatus(s);
    })();
    return () => ac.abort();
  }, []);

  // Per-session view - polled. Resets on session swap.
  useEffect(() => {
    previousKindRef.current = undefined;
    sawFirstLoadRef.current = false;
    setSession(null);
    if (!sessionId) return;
    let alive = true;
    const ac = new AbortController();
    const tick = async () => {
      const next = await fetchBridgeSession(sessionId, ac.signal);
      if (!alive || !next) return;
      // Toast on bridge swap (but never on the first observation).
      if (sawFirstLoadRef.current && previousKindRef.current && next.active_kind) {
        if (previousKindRef.current !== next.active_kind) {
          const human = next.active_kind === "mac" ? "Mac" : "Cloud";
          if (next.active_kind === "cloud" && next.why_active?.toLowerCase().includes("mac")) {
            setToast(`Bridge switched to ${human} - Mac offline, falling back.`);
          } else {
            setToast(`Bridge switched to ${human}`);
          }
        }
      }
      if (next.active_kind) previousKindRef.current = next.active_kind;
      sawFirstLoadRef.current = true;
      setSession(next);
    };
    void tick();
    const id = setInterval(() => void tick(), POLL_MS);
    return () => {
      alive = false;
      clearInterval(id);
      ac.abort();
    };
  }, [sessionId]);

  // Auto-dismiss toast after 4s.
  useEffect(() => {
    if (!toast) return;
    const id = setTimeout(() => setToast(null), 4_000);
    return () => clearTimeout(id);
  }, [toast]);

  const onRecheck = async () => {
    setRefreshing(true);
    try {
      const [s, sv] = await Promise.all([
        refreshBridgeStatus(),
        sessionId ? fetchBridgeSession(sessionId) : Promise.resolve(null),
      ]);
      if (s) setStatus(s);
      if (sv) setSession(sv);
    } finally {
      setRefreshing(false);
    }
  };

  const onChoosePreference = async (next: Preference) => {
    if (!sessionId) return;
    if (session?.preference === next) return;
    setSavingPref(next);
    try {
      const updated = await setBridgePreference(sessionId, next);
      if (updated) setSession(updated);
      // Also re-pull global status so the modal status block reflects the
      // server's freshest probe (the POST forces a re-probe upstream).
      const s = await fetchBridgeStatus();
      if (s) setStatus(s);
    } finally {
      setSavingPref(null);
    }
  };

  const pill = renderPill({ sessionId, status, session });

  const triggerNode = (
    <button
      type="button"
      aria-label="Bridge for this session"
      title={pill.title}
      className={cn(
        // Compact pill - sits in the top bar next to other small status
        // chips. Padding + gap chosen so the longest label ("Connecting…")
        // breathes away from the rounded border instead of kissing it.
        "inline-flex h-7 items-center gap-1 rounded-full border px-2 text-[10px] font-medium transition-colors",
        "hover:bg-accent/60",
        pill.toneClasses.border,
        pill.toneClasses.bg,
        pill.toneClasses.text,
      )}
      disabled={!sessionId}
    >
      <span
        className={cn(
          "inline-block size-1.5 shrink-0 rounded-full",
          pill.toneClasses.dot,
          pill.spin && "animate-pulse",
        )}
        aria-hidden
      />
      <span className="font-mono tracking-tight">{pill.label}</span>
    </button>
  );

  // No session → render the muted placeholder and skip the modal entirely.
  if (!sessionId) {
    return (
      <span
        className="inline-flex h-7 items-center gap-1 rounded-full border border-border/60 px-2 text-[10px] font-medium text-muted-foreground"
        aria-label="No session"
        title="No active session"
      >
        <span className="inline-block size-1.5 rounded-full bg-muted-foreground/40" aria-hidden />
        <span className="font-mono tracking-tight">-</span>
      </span>
    );
  }

  const body = (
    <BridgeModalBody
      status={status}
      session={session}
      savingPref={savingPref}
      refreshing={refreshing}
      onChoose={onChoosePreference}
      onRecheck={onRecheck}
    />
  );

  return (
    <>
      <span onClick={() => setOpen(true)}>{triggerNode}</span>
      <ResponsiveModal
        open={open}
        onOpenChange={setOpen}
        size="sm"
        title="Bridge for this session"
      >
        {body}
      </ResponsiveModal>
      {toast && <BridgeSwapToast text={toast} onDismiss={() => setToast(null)} />}
    </>
  );
}

// --------------------------------------------------------------------------

type PillRender = {
  label: string;
  title: string;
  spin: boolean;
  toneClasses: ReturnType<typeof toneToClasses>;
};

function renderPill({
  sessionId,
  status,
  session,
}: {
  sessionId: string | null;
  status: BridgeStatus | null;
  session: BridgeSessionView | null;
}): PillRender {
  // No session: caller handles this and never reaches here, but be safe.
  if (!sessionId) {
    return {
      label: "-",
      title: "No active session",
      spin: false,
      toneClasses: toneToClasses("muted"),
    };
  }

  // No status fetched yet, or in-flight.
  if (!status || !session) {
    return {
      label: "Connecting…",
      title: "Probing bridges",
      spin: true,
      toneClasses: toneToClasses("warning"),
    };
  }

  if (!status.configured) {
    return {
      label: "No bridge",
      title: "No bridge URLs configured on Core",
      spin: false,
      toneClasses: toneToClasses("danger"),
    };
  }

  // Pinned but the pinned bridge is offline → red.
  const pinnedAndDown =
    (session.preference === "mac" && !status.mac_healthy) ||
    (session.preference === "cloud" && !status.cloud_healthy);

  if (pinnedAndDown) {
    return {
      label: "No bridge",
      title: session.bridge_error || "Pinned bridge offline",
      spin: false,
      toneClasses: toneToClasses("danger"),
    };
  }

  // No active kind chosen by the router (e.g. both offline in auto).
  if (!session.active_kind) {
    const bothDown = !status.mac_healthy && !status.cloud_healthy;
    return {
      label: bothDown ? "No bridge" : "Connecting…",
      title: session.bridge_error || "Waiting for a healthy bridge",
      spin: !bothDown,
      toneClasses: toneToClasses(bothDown ? "danger" : "warning"),
    };
  }

  if (session.active_kind === "mac") {
    return {
      label: "Mac",
      title: session.why_active || "Mac bridge active",
      spin: false,
      toneClasses: toneToClasses("success"),
    };
  }

  return {
    label: "Cloud",
    title: session.why_active || "Cloud bridge active",
    spin: false,
    toneClasses: toneToClasses("info"),
  };
}

function toneToClasses(tone: Tone) {
  switch (tone) {
    case "success":
      return {
        border: "border-success/40",
        bg: "bg-success/10",
        text: "text-success",
        dot: "bg-success",
      };
    case "info":
      return {
        border: "border-info/40",
        bg: "bg-info/10",
        text: "text-info",
        dot: "bg-info",
      };
    case "warning":
      return {
        border: "border-warning/40",
        bg: "bg-warning/10",
        text: "text-warning",
        dot: "bg-warning",
      };
    case "danger":
      return {
        border: "border-danger/40",
        bg: "bg-danger/10",
        text: "text-danger",
        dot: "bg-danger",
      };
    case "muted":
    default:
      return {
        border: "border-border/60",
        bg: "bg-transparent",
        text: "text-muted-foreground",
        dot: "bg-muted-foreground/40",
      };
  }
}

// --------------------------------------------------------------------------

function BridgeModalBody({
  status,
  session,
  savingPref,
  refreshing,
  onChoose,
  onRecheck,
}: {
  status: BridgeStatus | null;
  session: BridgeSessionView | null;
  savingPref: Preference | null;
  refreshing: boolean;
  onChoose: (p: Preference) => void;
  onRecheck: () => void;
}) {
  const currentPref: Preference = session?.preference ?? "auto";

  return (
    <div className="flex flex-col gap-3">
      <p className="text-[11px] leading-relaxed text-muted-foreground">
        Decide where this session&apos;s file edits and shell commands run.
        Switching takes effect on the next tool call.
      </p>

      <div className="flex flex-col gap-1.5">
        <PreferenceRow
          value="auto"
          current={currentPref}
          saving={savingPref}
          title="Auto"
          description="Use Mac when online, fall back to Cloud automatically. Recommended."
          onChoose={onChoose}
        />
        <PreferenceRow
          value="mac"
          current={currentPref}
          saving={savingPref}
          title="Mac"
          description="Pin to Mac. Heavy edits use Anthropic Max-billed Claude Code (claude_code__* tools)."
          onChoose={onChoose}
        />
        <PreferenceRow
          value="cloud"
          current={currentPref}
          saving={savingPref}
          title="Cloud"
          description="Pin to Railway workspace. Jarvis edits files directly via ChatGPT subscription, no metered API."
          onChoose={onChoose}
        />
      </div>

      <div className="mt-1 rounded-md border bg-muted/30 p-3 text-[11px]">
        <div className="mb-1.5 flex items-center justify-between">
          <span className="font-semibold uppercase tracking-wide text-muted-foreground">
            Status
          </span>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={onRecheck}
            disabled={refreshing}
            className="h-7 gap-1 px-2 text-[11px]"
          >
            <RefreshCw
              className={cn("size-3", refreshing && "animate-spin")}
              aria-hidden
            />
            <span>Re-check now</span>
          </Button>
        </div>
        <div className="grid grid-cols-2 gap-2">
          <StatusLine
            label="Mac"
            healthy={!!status?.mac_healthy}
            configured={!!status?.mac_url}
          />
          <StatusLine
            label="Cloud"
            healthy={!!status?.cloud_healthy}
            configured={!!status?.cloud_url}
          />
        </div>
        {status?.checked_at && (
          <div className="mt-2 font-mono text-[10px] text-muted-foreground" suppressHydrationWarning>
            Last checked {formatCheckedAt(status.checked_at)}
          </div>
        )}
        {session?.why_active && (
          <div className="mt-2 text-[11px] text-muted-foreground">
            <span className="font-semibold text-foreground">Active:</span>{" "}
            {session.why_active}
          </div>
        )}
        {session?.bridge_error && (
          <div className="mt-1 text-[11px] text-danger">{session.bridge_error}</div>
        )}
      </div>
    </div>
  );
}

function PreferenceRow({
  value,
  current,
  saving,
  title,
  description,
  onChoose,
}: {
  value: Preference;
  current: Preference;
  saving: Preference | null;
  title: string;
  description: string;
  onChoose: (p: Preference) => void;
}) {
  const active = current === value;
  const isSaving = saving === value;
  return (
    <button
      type="button"
      onClick={() => onChoose(value)}
      disabled={isSaving}
      aria-pressed={active}
      className={cn(
        "flex w-full items-start gap-3 rounded-md border px-3 py-2.5 text-left transition-colors",
        active
          ? "border-info bg-info/10"
          : "border-border hover:bg-accent/60",
        isSaving && "opacity-60",
      )}
    >
      <span
        className={cn(
          "mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border",
          active ? "border-info bg-info text-info-foreground" : "border-border bg-background",
        )}
        aria-hidden
      >
        {active ? <Check className="size-3" /> : null}
        {isSaving && <Loader2 className="size-3 animate-spin" />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-sm font-semibold text-foreground">{title}</span>
        <span className="mt-0.5 block text-[11px] leading-relaxed text-muted-foreground">
          {description}
        </span>
      </span>
    </button>
  );
}

function StatusLine({
  label,
  healthy,
  configured,
}: {
  label: string;
  healthy: boolean;
  configured: boolean;
}) {
  const tone: Tone = !configured ? "muted" : healthy ? "success" : "danger";
  const cls = toneToClasses(tone);
  const text = !configured ? "not configured" : healthy ? "up" : "down";
  return (
    <div className="flex items-center gap-1.5">
      <span className={cn("inline-block size-2 rounded-full", cls.dot)} aria-hidden />
      <span className="font-semibold text-foreground">{label}</span>
      <span className={cn("font-mono text-[10px]", cls.text)}>{text}</span>
    </div>
  );
}

function formatCheckedAt(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString(undefined, {
      hour: "numeric",
      minute: "2-digit",
      second: "2-digit",
    });
  } catch {
    return iso;
  }
}

// --------------------------------------------------------------------------

function BridgeSwapToast({
  text,
  onDismiss,
}: {
  text: string;
  onDismiss: () => void;
}) {
  return (
    <div className="pointer-events-none fixed inset-x-0 top-0 z-[60] flex justify-center pt-safe">
      <div
        role="status"
        aria-live="polite"
        className={cn(
          "pointer-events-auto mx-3 mt-2 flex w-full max-w-md items-center gap-2",
          "rounded-2xl border border-info/40 bg-popover/95 px-3 py-2 shadow-lg backdrop-blur",
        )}
      >
        <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-info/15">
          <RefreshCw className="size-4 text-info" aria-hidden />
        </span>
        <span className="min-w-0 flex-1 text-left text-sm text-foreground">{text}</span>
        <button
          type="button"
          aria-label="Dismiss"
          onClick={onDismiss}
          className="-mr-1 flex size-8 shrink-0 items-center justify-center rounded-full text-muted-foreground hover:text-foreground"
        >
          <X className="size-4" />
        </button>
      </div>
    </div>
  );
}
