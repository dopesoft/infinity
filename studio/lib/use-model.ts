"use client";

import { useEffect, useState, useCallback } from "react";
import { MODELS, type ModelKey } from "@/components/ui/ai-prompt-box";

/**
 * Per-session model selection. Persists to localStorage keyed by session id.
 * The agent loop's server-side default is honored until the user clicks the
 * chip to override it — at which point we round-trip the choice through the
 * `/api/sessions/{id}/model` endpoint so the next turn uses the new model.
 *
 * Storage key: `infinity:session:<id>:model` → ModelKey.
 */
const DEFAULT_MODEL: ModelKey = "sonnet-4-5";

function storageKey(sessionId: string | null | undefined): string | null {
  if (!sessionId) return null;
  return `infinity:session:${sessionId}:model`;
}

export function useSessionModel(sessionId: string | null | undefined) {
  const [model, setModelState] = useState<ModelKey>(DEFAULT_MODEL);
  const [hydrated, setHydrated] = useState(false);

  // Hydrate from localStorage when the session changes.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const key = storageKey(sessionId);
    if (!key) {
      setModelState(DEFAULT_MODEL);
      setHydrated(true);
      return;
    }
    try {
      const stored = window.localStorage.getItem(key) as ModelKey | null;
      if (stored && MODELS.some((m) => m.key === stored)) {
        setModelState(stored);
      } else {
        setModelState(DEFAULT_MODEL);
      }
    } catch {
      setModelState(DEFAULT_MODEL);
    }
    setHydrated(true);
  }, [sessionId]);

  const setModel = useCallback(
    (next: ModelKey) => {
      setModelState(next);
      if (typeof window === "undefined") return;
      const key = storageKey(sessionId);
      if (!key) return;
      try {
        window.localStorage.setItem(key, next);
      } catch {
        /* ignore */
      }
    },
    [sessionId],
  );

  return { model, setModel, hydrated };
}
