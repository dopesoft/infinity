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
  /** The session the current `artifacts` snapshot belongs to. "" while a new
   *  session's fetch is still in flight. Consumers that key behaviour off the
   *  session (open-tab rehydration) MUST compare this against their own
   *  sessionId rather than trusting `artifacts` — otherwise they act on the
   *  previous conversation's documents during the changeover render. */
  forSession: string;
  refresh: () => void;
} {
  const [artifacts, setArtifacts] = useState<DocArtifact[]>([]);
  const [forSession, setForSession] = useState("");
  const [loading, setLoading] = useState(true);
  const sidRef = useRef(sessionId);
  sidRef.current = sessionId;

  const reload = useCallback(async () => {
    const sid = sidRef.current;
    if (!sid) {
      setArtifacts([]);
      setForSession("");
      setLoading(false);
      return;
    }
    const next = await fetchSessionArtifacts(sid);
    // Guard against a stale response landing after the session changed.
    if (sidRef.current === sid) {
      setArtifacts(next);
      setForSession(sid);
      setLoading(false);
    }
  }, []);

  // A session change invalidates the snapshot immediately — documents belong to
  // the conversation that produced them, so the gallery must never render the
  // previous session's artifacts while the new fetch is in flight.
  useEffect(() => {
    setArtifacts([]);
    setForSession("");
    setLoading(true);
    void reload();
  }, [sessionId, reload]);

  // Any mem_artifacts change re-fetches the filtered snapshot (cheap, indexed).
  useRealtime("mem_artifacts", () => {
    void reload();
  });

  return { artifacts, loading, forSession, refresh: reload };
}
