"use client";

import { useCallback, useEffect, useState } from "react";
import { authedFetch } from "@/lib/api";
import { findVendor } from "@/lib/models-catalog";

/**
 * Global model selection - single source of truth in Core's settings
 * store (infinity_meta row at key `setting.model`). Persists across
 * sessions, devices, and Core restarts.
 *
 * Why a hook + module-scoped pub/sub instead of React context:
 *   - The chip in the chat input and the Settings page need to stay
 *     synchronized when either one mutates the value. Both render in
 *     totally different subtrees, so a single context provider high
 *     in the tree would work but adds a wrapper to the app root for
 *     what's effectively one string.
 *   - A module-scoped subscriber list lets any component opt in with
 *     `useGlobalModel()` and get push updates from any other component
 *     that calls `setModel`. The shape mirrors what context would give
 *     us without the provider boilerplate.
 *
 * The hook caches the last fetched value at module scope so a new
 * subscriber mounted after the initial fetch renders with the right
 * value immediately, no flash of "loading".
 */

export type ModelSetting = {
  /** Effective model id Core is using (override if set, default otherwise). */
  model: string;
  /** Provider's boot-default model id - what kicks in when the override is cleared. */
  defaultModel: string;
  /** Active provider id (anthropic / openai / openai_oauth / google). */
  provider: string;
  /** "user" when the model came from the settings store; "default" when riding the boot env. */
  source: "user" | "default";
  /** "user" when the provider came from the settings store; "default" when riding LLM_PROVIDER. */
  providerSource: "user" | "default";
  /** Providers the runtime knows how to swap to (creds wired). */
  availableProviders: string[];
  /** Set while the chosen brain's plan is spent and a standby is answering
   *  (Core's llm.EffectiveBrain). null when the chosen brain is healthy. */
  standby: StandbyBrain | null;
};

export type StandbyBrain = {
  /** Provider actually answering right now (e.g. "anthropic"). */
  provider: string;
  /** Model actually answering right now. */
  model: string;
  /** ISO time the chosen brain's plan resets. */
  until: string;
  /** Plain-English reason from Core ("the ChatGPT plus plan's usage allowance is spent"). */
  reason: string;
};

const SETTING_ENDPOINT = "/api/settings/model";
const PROVIDER_ENDPOINT = "/api/settings/provider";

// Module-scoped cache + subscribers. The cache lets a freshly mounted
// hook render with the last known value instead of a placeholder. The
// subscriber list pushes updates when any caller writes - so the chip
// updates when Settings saves and vice versa.
let cache: ModelSetting | null = null;
const subscribers = new Set<(s: ModelSetting) => void>();

function broadcast(next: ModelSetting) {
  cache = next;
  for (const fn of subscribers) fn(next);
}

type WireResp = {
  model?: string;
  default_model?: string;
  provider?: string;
  source?: string;
  provider_source?: string;
  available_providers?: string[];
  effective_provider?: string;
  effective_model?: string;
  standby_until?: string;
  standby_reason?: string;
};

function decode(raw: WireResp): ModelSetting {
  const provider = (raw.provider ?? "").toLowerCase();
  const effectiveProvider = (raw.effective_provider ?? provider).toLowerCase();
  const onStandby = Boolean(raw.standby_until) && effectiveProvider !== "" && effectiveProvider !== provider;
  return {
    model: raw.model ?? "",
    defaultModel: raw.default_model ?? "",
    provider,
    source: raw.source === "user" ? "user" : "default",
    providerSource: raw.provider_source === "user" ? "user" : "default",
    availableProviders: raw.available_providers ?? [],
    standby: onStandby
      ? {
          provider: effectiveProvider,
          model: raw.effective_model ?? "",
          until: raw.standby_until ?? "",
          reason: raw.standby_reason ?? "",
        }
      : null,
  };
}

async function fetchSetting(signal?: AbortSignal): Promise<ModelSetting | null> {
  try {
    const res = await authedFetch(SETTING_ENDPOINT, { signal });
    if (!res.ok) return null;
    return decode((await res.json()) as WireResp);
  } catch {
    return null;
  }
}

/**
 * useGlobalModel returns the current setting + a setter that PUTs the
 * new value to Core and broadcasts to every other subscriber so the
 * chip and the Settings page stay aligned without prop drilling.
 *
 * Initial render uses the module cache (or null when nothing's been
 * fetched yet); a one-shot effect refreshes from Core on mount so
 * we don't trust a stale cache across reloads / tab switches.
 */
export function useGlobalModel() {
  const [setting, setSetting] = useState<ModelSetting | null>(cache);
  const [saving, setSaving] = useState(false);

  // Subscribe to module-scope broadcasts.
  useEffect(() => {
    const fn = (s: ModelSetting) => setSetting(s);
    subscribers.add(fn);
    return () => {
      subscribers.delete(fn);
    };
  }, []);

  // Refresh from Core on mount. AbortController so a fast unmount
  // doesn't write into a torn-down component.
  useEffect(() => {
    const ac = new AbortController();
    fetchSetting(ac.signal).then((next) => {
      if (next) broadcast(next);
    });
    return () => ac.abort();
  }, []);

  const setModel = useCallback(async (modelId: string) => {
    setSaving(true);
    try {
      const res = await authedFetch(SETTING_ENDPOINT, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ model: modelId }),
      });
      if (!res.ok) return false;
      broadcast(decode((await res.json()) as WireResp));
      return true;
    } catch {
      return false;
    } finally {
      setSaving(false);
    }
  }, []);

  // setProvider hot-swaps the active LLM provider on Core. Stored OAuth
  // credentials (mem_provider_tokens) are NOT touched, so flipping
  // openai_oauth → anthropic → openai_oauth doesn't require re-auth.
  // Pass "" to clear the override and revert to LLM_PROVIDER env (takes
  // effect at next restart; runtime stays on whatever's active until then).
  const setProvider = useCallback(async (providerId: string) => {
    setSaving(true);
    try {
      const res = await authedFetch(PROVIDER_ENDPOINT, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ provider: providerId }),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null;
        return { ok: false, error: body?.error ?? `HTTP ${res.status}` };
      }
      broadcast(decode((await res.json()) as WireResp));
      return { ok: true as const };
    } catch (e) {
      return { ok: false, error: String(e) };
    } finally {
      setSaving(false);
    }
  }, []);

  // refresh re-reads Core's answer. Needed whenever something OUTSIDE this
  // hook changes what the picker may offer - pasting a vendor API key
  // registers a provider, and the vendor row has to stop saying "not
  // configured" without a page reload.
  const refresh = useCallback(async () => {
    const next = await fetchSetting();
    if (next) broadcast(next);
  }, []);

  return { setting, setModel, setProvider, saving, refresh };
}

/** Human label for a standby brain, from the shared model catalog. */
export function standbyLabel(standby: StandbyBrain | null | undefined): string | null {
  if (!standby) return null;
  const vendor = findVendor(standby.provider);
  const model = vendor.models.find((m) => m.id === standby.model);
  return `${vendor.label} · ${model?.label ?? (standby.model || vendor.label)}`;
}

/** "10:13pm" style local clock for the reset time; empty when unknown. */
export function standbyResetClock(standby: StandbyBrain | null | undefined): string {
  if (!standby?.until) return "";
  const d = new Date(standby.until);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }).toLowerCase();
}

/** Label of the model ANSWERING right now (standby when the chosen plan is
 *  spent, else the chosen model), e.g. "GPT-5.6 Sol" / "Sonnet 5". */
export function effectiveModelLabel(setting: ModelSetting | null | undefined): string | null {
  if (!setting) return null;
  const provider = setting.standby?.provider ?? setting.provider;
  const modelId = setting.standby?.model || setting.model || setting.defaultModel;
  const vendor = findVendor(provider);
  const model = vendor.models.find((m) => m.id === modelId);
  return model?.label ?? (modelId || null);
}
