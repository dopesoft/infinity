"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { KeyRound, ExternalLink, RefreshCcw, CheckCircle2, X } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { ModalUrl } from "@/components/ui/modal-content";
import { useRealtime } from "@/lib/realtime/provider";
import { fetchExtensions, checkExtension, activateExtension, type Extension } from "@/lib/api";
import { cn } from "@/lib/utils";

/**
 * CanvasAuthCard - the self-contained "sign in to <tool>" surface that takes
 * over the Preview pane when a cli extension is in `pending_auth`. It replaces
 * the old behaviour where a tool needing device-login left the boss staring at
 * a raw 404 with no way to act.
 *
 * Why a card and not the provider's page embedded: device-login pages (Google,
 * Higgsfield, …) send X-Frame-Options: DENY, so they can't live in an iframe.
 * The card surfaces the URL + instructions, opens the real page in a new tab,
 * and polls the extension's check_cmd so it flips to "Connected" on its own the
 * moment the boss finishes - no tab-switch to Settings, no manual refresh.
 *
 * Isolation: a pending sign-in is scoped to the ONE session that started it
 * (ext.auth_session_id). The card renders only in that conversation, so a
 * globally-pending extension (e.g. higgsfield, which can't finish its
 * browser-redirect sign-in headlessly and sat pending forever) no longer
 * hijacks the Preview pane of an unrelated conversation. A pending row with no
 * originating session surfaces in Settings → Extensions, never seizes a Canvas.
 */

const POLL_MS = 7000;

// usePendingAuthExtension returns the cli/mcp extension awaiting sign-in that
// belongs to THIS session (matched on auth_session_id), or null. Refreshes on
// the mem_extensions realtime channel so the card appears the instant the gate
// parks one in this session and vanishes the instant it goes active. A pending
// extension with no originating session — or one tied to a different session —
// is intentionally ignored here so it can't take over an unrelated Preview.
export function usePendingAuthExtension(sessionId?: string): {
  pending: Extension | null;
  refresh: () => void;
} {
  const [pending, setPending] = useState<Extension | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      const res = await fetchExtensions(signal);
      if (Array.isArray(res)) {
        const sid = sessionId?.trim();
        setPending(
          sid
            ? res.find(
                (e) => e.status === "pending_auth" && e.auth_session_id === sid,
              ) ?? null
            : null,
        );
      }
    },
    [sessionId],
  );

  useEffect(() => {
    const ctl = new AbortController();
    void load(ctl.signal);
    return () => ctl.abort();
  }, [load]);

  useRealtime("mem_extensions", () => void load());

  return { pending, refresh: () => void load() };
}

export function CanvasAuthCard({
  ext,
  sessionId,
  onResolved,
  onDismiss,
}: {
  ext: Extension;
  sessionId?: string;
  onResolved: () => void;
  onDismiss?: () => void;
}) {
  const [checking, setChecking] = useState(false);
  const [ready, setReady] = useState(false);
  const [note, setNote] = useState<string>("");
  const [preparing, setPreparing] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const activatedRef = useRef(false);

  // A cli extension parked at pending_auth with no captured login URL (e.g.
  // freshly corrected in the DB) needs its install + auth flow driven so the
  // device URL appears. Kick it once; the mem_extensions realtime channel
  // re-renders this card with auth_url the moment it lands.
  useEffect(() => {
    if (ext.kind !== "cli" || ext.auth_url || activatedRef.current) return;
    activatedRef.current = true;
    setPreparing(true);
    void activateExtension(ext.name, sessionId);
  }, [ext.kind, ext.auth_url, ext.name, sessionId]);

  // Once a URL arrives, we're no longer preparing.
  useEffect(() => {
    if (ext.auth_url) setPreparing(false);
  }, [ext.auth_url]);

  // Some CLIs (e.g. higgsfield) authenticate by opening a real browser rather
  // than printing a device link, so the headless cloud activate can never
  // capture a URL and "Preparing…" would spin forever. After a grace period
  // with no URL, stop spinning and tell the boss the real path instead.
  const [prepTimedOut, setPrepTimedOut] = useState(false);
  useEffect(() => {
    if (!preparing || ext.auth_url) {
      setPrepTimedOut(false);
      return;
    }
    const t = setTimeout(() => setPrepTimedOut(true), 30_000);
    return () => clearTimeout(t);
  }, [preparing, ext.auth_url]);

  const probe = useCallback(
    async (manual: boolean) => {
      if (checking) return;
      setChecking(true);
      if (manual) setNote("");
      try {
        const res = await checkExtension(ext.name);
        if (res.ready || res.extension?.status === "active") {
          setReady(true);
          // Let the boss see "Connected" briefly, then drop the card.
          setTimeout(onResolved, 900);
        } else if (manual) {
          setNote("Not signed in yet — finish in the browser tab, then check again.");
        }
      } finally {
        setChecking(false);
      }
    },
    [ext.name, checking, onResolved],
  );

  // Auto-poll so the card flips to Connected on its own once the boss finishes,
  // exactly like the OAuth "you can close this tab" handoff.
  useEffect(() => {
    pollRef.current = setInterval(() => void probe(false), POLL_MS);
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [probe]);

  const prettyName = ext.name.replace(/[-_]+/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());

  return (
    <div className="relative flex h-full min-h-0 flex-col overflow-auto bg-gradient-to-br from-zinc-200/60 to-zinc-300/40 px-6 py-10 dark:from-zinc-900/40 dark:to-black">
      {onDismiss ? (
        <button
          type="button"
          onClick={onDismiss}
          aria-label="Dismiss sign-in"
          className="absolute right-3 top-3 inline-flex size-8 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <X className="size-4" aria-hidden />
        </button>
      ) : null}
      <div className="mx-auto flex w-full max-w-md flex-col items-center gap-5 text-center">
        <span
          className={cn(
            "inline-flex size-12 items-center justify-center rounded-full transition-colors",
            ready
              ? "bg-success/15 text-success"
              : "bg-brand/10 text-brand",
          )}
        >
          {ready ? <CheckCircle2 className="size-6" aria-hidden /> : <KeyRound className="size-6" aria-hidden />}
        </span>

        <div className="space-y-1.5">
          <h3 className="text-base font-semibold text-foreground">
            {ready ? `${prettyName} connected` : `Sign in to ${prettyName}`}
          </h3>
          <p className="text-[13px] leading-relaxed text-muted-foreground">
            {ready
              ? "You're authenticated — Jarvis can use it now."
              : ext.auth_instructions ||
                `${prettyName} needs you to sign in once. Open the page below, authorize, and it'll connect automatically.`}
          </p>
        </div>

        {!ready && !ext.auth_url && preparing && !prepTimedOut ? (
          <p className="inline-flex items-center gap-1.5 text-[12px] text-muted-foreground">
            <Spinner className="size-3.5" aria-hidden />
            Preparing your sign-in link…
          </p>
        ) : null}

        {!ready && !ext.auth_url && prepTimedOut ? (
          <p className="max-w-xs text-[12px] leading-relaxed text-muted-foreground">
            Couldn&apos;t get an automatic link — {prettyName} signs in through a
            browser, which the cloud workspace can&apos;t open on its own. Open the{" "}
            <span className="font-medium text-foreground">Terminal</span> tab and run{" "}
            <code className="rounded bg-muted px-1 font-mono text-[11px]">{ext.name} auth login</code>
            , or sign in on your Mac and I&apos;ll sync the credentials. You can dismiss this with ✕.
          </p>
        ) : null}

        {!ready && ext.auth_url ? (
          <div className="w-full space-y-3">
            <a href={ext.auth_url} target="_blank" rel="noreferrer noopener" className="block">
              <Button className="w-full gap-1.5">
                <ExternalLink className="size-4" aria-hidden />
                Open sign-in page
              </Button>
            </a>
            <div className="min-w-0 text-left">
              <ModalUrl href={ext.auth_url} icon={<ExternalLink className="size-3.5" aria-hidden />}>
                {ext.auth_url}
              </ModalUrl>
            </div>
          </div>
        ) : null}

        {!ready ? (
          <div className="flex flex-col items-center gap-2">
            <Button
              variant="secondary"
              size="sm"
              disabled={checking}
              onClick={() => void probe(true)}
              className="gap-1.5"
            >
              {checking ? (
                <Spinner className="size-3.5" aria-hidden />
              ) : (
                <RefreshCcw className="size-3.5" aria-hidden />
              )}
              {checking ? "Checking…" : "I've signed in"}
            </Button>
            <p className="inline-flex items-center gap-1.5 text-[11px] text-muted-foreground">
              <Spinner className="size-3" aria-hidden />
              Watching for you to finish…
            </p>
            {note ? <p className="text-[11px] text-warning">{note}</p> : null}
          </div>
        ) : null}

        {ext.last_error && !ready ? (
          <p className="max-w-full break-words text-[11px] text-muted-foreground">{ext.last_error}</p>
        ) : null}
      </div>
    </div>
  );
}
