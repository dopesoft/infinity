"use client";

import { createContext, useContext, useEffect, useRef, useState } from "react";
import { fetchSessions, canvasProjectActivate, canvasProjectStatus, type SessionDTO, type ProjectDTO } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { useWebSocket } from "@/lib/ws/provider";

const SESSION_KEY = "infinity:sessionId";

/**
 * useCurrentProject - Canvas's read of the active session's project.
 *
 * The session is the project. When the boss is on the Live tab and switches
 * to a different conversation, the Canvas surface inherits the new
 * session's project_path/template/dev_port from mem_sessions. This hook
 * wires that flow:
 *
 *   1. Read infinity:sessionId from localStorage (set by useChat).
 *   2. Fetch the session row + find project_path.
 *   3. If present, ping the bridge supervisor to activate that project
 *      (kicks off the dev server cold-start if it isn't already warm).
 *   4. Poll /api/canvas/project/status while the project is booting so
 *      Canvas can show "warming up the dev server…" → "ready" → "crashed".
 *
 * Returns whatever it knows; consumers handle the null states.
 */
export type CurrentProject = {
  sessionId: string;
  session: SessionDTO | null;
  project: ProjectDTO | null;
  loading: boolean;
  error: string | null;
};

export function useCurrentProject(): CurrentProject {
  const [sessionId, setSessionId] = useState<string>("");
  const [session, setSession] = useState<SessionDTO | null>(null);
  const [project, setProject] = useState<ProjectDTO | null>(null);
  const [loading, setLoading] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);
  const lastActivatedPathRef = useRef<string>("");

  // Track the active session id from localStorage. useChat keeps this in sync
  // across the app; we just observe.
  useEffect(() => {
    if (typeof window === "undefined") return;
    function refreshSessionId() {
      const id = window.localStorage.getItem(SESSION_KEY) || "";
      setSessionId((prev) => (prev === id ? prev : id));
    }
    refreshSessionId();
    // localStorage's `storage` event fires across tabs, but not in the same
    // tab - Live mutates it via writeStoredSessionId. Re-check on focus and
    // when the sessions realtime channel fires to catch in-tab swaps.
    window.addEventListener("focus", refreshSessionId);
    window.addEventListener("pageshow", refreshSessionId);
    const id = setInterval(refreshSessionId, 1500);
    return () => {
      window.removeEventListener("focus", refreshSessionId);
      window.removeEventListener("pageshow", refreshSessionId);
      clearInterval(id);
    };
  }, []);

  // Fetch the session row whenever the id changes. `background` refreshes
  // (realtime ticks, project_changed) DON'T toggle `loading` - the agent writes
  // mem_sessions on every turn, so flipping loading true→false each time caused
  // re-render churn that flickered the canvas/preview. Only the initial load for
  // a new session id shows the loading state.
  async function loadSession(background = false) {
    if (!sessionId) {
      setSession(null);
      setLoading(false);
      return;
    }
    if (!background) setLoading(true);
    setError(null);
    const rows = await fetchSessions();
    const me = rows?.find((r) => r.id === sessionId) ?? null;
    setSession(me);
    if (!background) setLoading(false);
  }

  useEffect(() => {
    loadSession();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  useRealtime("mem_sessions", () => {
    if (sessionId) loadSession(true);
  });

  // Deterministic refresh: project_create/clone/open fire a `project_changed`
  // WS event the instant they set mem_sessions.project_path. Supabase realtime
  // SHOULD also catch that UPDATE, but it can lag or drop across a reconnect -
  // which left the canvas stuck on "no project" after a build, so the preview
  // never activated. Listening to the WS event guarantees the new project_path
  // lands the moment the agent sets it, without a page reload.
  const ws = useWebSocket();
  useEffect(() => {
    return ws.subscribe((ev) => {
      if (ev.type !== "project_changed") return;
      if (ev.session_id && sessionId && ev.session_id !== sessionId) return;
      if (sessionId) loadSession(true);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ws, sessionId]);

  // When the session has a project_path, activate it on the bridge and start
  // polling its status. Re-activate only when the path actually changes.
  useEffect(() => {
    const path = session?.project_path?.trim();
    if (!path) {
      setProject(null);
      return;
    }
    if (path === lastActivatedPathRef.current) return;
    lastActivatedPathRef.current = path;

    let cancelled = false;
    (async () => {
      const result = await canvasProjectActivate({
        project_path: path,
        template: session?.project_template,
        session_id: sessionId,
      });
      if (cancelled) return;
      if (!result) {
        setError("bridge unreachable - the Mac supervisor isn't responding");
        return;
      }
      setError(null);
      setProject(result);
    })();
    return () => {
      cancelled = true;
    };
  }, [session?.project_path, session?.project_template, sessionId]);

  // Status polling while the project is booting/crashed. Stops once running.
  useEffect(() => {
    const path = session?.project_path?.trim();
    if (!path) return;
    if (project?.status === "running") return;
    const id = setInterval(async () => {
      const result = await canvasProjectStatus(path);
      if (!result) return;
      if ("project_path" in result) {
        setProject(result);
      }
    }, 2000);
    return () => clearInterval(id);
  }, [project?.status, session?.project_path]);

  return { sessionId, session, project, loading, error };
}

const CurrentProjectContext = createContext<CurrentProject | null>(null);

export function CurrentProjectProvider({
  value,
  children,
}: {
  value: CurrentProject;
  children: React.ReactNode;
}) {
  return <CurrentProjectContext.Provider value={value}>{children}</CurrentProjectContext.Provider>;
}

/**
 * useProjectContext - read the current-project snapshot from anywhere
 * inside the Canvas tree. Returns null when called outside the provider
 * so consumers can degrade gracefully (treat as "no project").
 */
export function useProjectContext(): CurrentProject | null {
  return useContext(CurrentProjectContext);
}
