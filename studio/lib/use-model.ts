"use client";

import { useCallback, useEffect, useState } from "react";
import { MODELS, type ModelKey } from "@/components/ui/ai-prompt-box";
import { authedFetch } from "@/lib/api";

/**
 * Global model selection — single source of truth in Core's settings
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
  /** Provider's boot-default model id — what kicks in when the override is cleared. */
  defaultModel: string;
  /** Provider name (anthropic / openai / google) — read-only from the UI. */
  provider: string;
  /** "user" when the value came from the settings store; "default" when riding the boot env. */
  source: "user" | "default";
};

const SETTING_ENDPOINT = "/api/settings/model";

// Module-scoped cache + subscribers. The cache lets a freshly mounted
// hook render with the last known value instead of a placeholder. The
// subscriber list pushes updates when any caller writes — so the chip
// updates when Settings saves and vice versa.
let cache: ModelSetting | null = null;
const subscribers = new Set<(s: ModelSetting) => void>();

function broadcast(next: ModelSetting) {
  cache = next;
  for (const fn of subscribers) fn(next);
}

async function fetchSetting(signal?: AbortSignal): Promise<ModelSetting | null> {
  try {
    const res = await authedFetch(SETTING_ENDPOINT, { signal });
    if (!res.ok) return null;
    const raw = (await res.json()) as {
      model?: string;
      default_model?: string;
      provider?: string;
      source?: string;
    };
    return {
      model: raw.model ?? "",
      defaultModel: raw.default_model ?? "",
      provider: (raw.provider ?? "").toLowerCase(),
      source: raw.source === "user" ? "user" : "default",
    };
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
      const raw = (await res.json()) as {
        model?: string;
        default_model?: string;
        provider?: string;
        source?: string;
      };
      broadcast({
        model: raw.model ?? "",
        defaultModel: raw.default_model ?? "",
        provider: (raw.provider ?? "").toLowerCase(),
        source: raw.source === "user" ? "user" : "default",
      });
      return true;
    } catch {
      return false;
    } finally {
      setSaving(false);
    }
  }, []);

  return { setting, setModel, saving };
}

/**
 * resolveModelKey maps a raw model id (from /api/settings/model) to the
 * ModelKey the chip uses. Returns the first key when the id is unknown
 * — better to show *something* in the chip than a blank state.
 */
export function resolveModelKey(modelId: string): ModelKey {
  const hit = MODELS.find((m) => m.id === modelId);
  return hit?.key ?? MODELS[0].key;
}
