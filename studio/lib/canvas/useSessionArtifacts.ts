"use client";

// useSessionArtifacts reads the generated documents for a session — the data
// behind the Artifacts/Media repository AND the open-tab rehydration. Like
// useRuns, it's the consumer side of the "Server-tracked progress" rule:
// document_create writes a mem_artifacts row deterministically, so the list
// survives refresh / navigation / device switch (it reads from the DB, not
// in-memory state). Realtime on mem_artifacts keeps it live as new docs land.

import { useCallback, useEffect, useRef, useState } from "react";
import { useRealtime } from "@/lib/realtime/provider";
import { fetchSessionArtifacts, type DocArtifact } from "@/lib/api";

export function useSessionArtifacts(sessionId: string): {
  artifacts: DocArtifact[];
  loading: boolean;
  refresh: () => void;
} {
  const [artifacts, setArtifacts] = useState<DocArtifact[]>([]);
  const [loading, setLoading] = useState(true);
  const sidRef = useRef(sessionId);
  sidRef.current = sessionId;

  const reload = useCallback(async () => {
    const sid = sidRef.current;
    if (!sid) {
      setArtifacts([]);
      setLoading(false);
      return;
    }
    const next = await fetchSessionArtifacts(sid);
    // Guard against a stale response landing after the session changed.
    if (sidRef.current === sid) {
      setArtifacts(next);
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    void reload();
  }, [sessionId, reload]);

  // Any mem_artifacts change re-fetches the filtered snapshot (cheap, indexed).
  useRealtime("mem_artifacts", () => {
    void reload();
  });

  return { artifacts, loading, refresh: reload };
}
