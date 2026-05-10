"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { Session, User } from "@supabase/supabase-js";
import { getSupabaseBrowserClient } from "@/lib/supabase/client";

type AuthState = {
  user: User | null;
  session: Session | null;
  loading: boolean;
  // accessToken is a snapshot — for live JWT use getAccessToken() which
  // forces a refresh if the cached token is within 30s of expiry.
  accessToken: string | null;
  getAccessToken: () => Promise<string | null>;
  signOut: () => Promise<void>;
};

const AuthContext = createContext<AuthState | null>(null);

const REFRESH_BUFFER_SECONDS = 30;

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);

  // Mirror the latest session in a ref so getAccessToken() stays stable
  // across renders (no stale closures inside long-lived WS handlers).
  const sessionRef = useRef<Session | null>(null);
  sessionRef.current = session;

  useEffect(() => {
    const supabase = getSupabaseBrowserClient();
    let mounted = true;

    supabase.auth.getSession().then(({ data }) => {
      if (!mounted) return;
      setSession(data.session ?? null);
      setUser(data.session?.user ?? null);
      setLoading(false);
    });

    const {
      data: { subscription },
    } = supabase.auth.onAuthStateChange((_event, sess) => {
      setSession(sess ?? null);
      setUser(sess?.user ?? null);
      setLoading(false);
    });

    return () => {
      mounted = false;
      subscription.unsubscribe();
    };
  }, []);

  const getAccessToken = useCallback(async () => {
    const supabase = getSupabaseBrowserClient();
    const current = sessionRef.current;
    if (current && current.expires_at) {
      const expiresInSec = current.expires_at - Math.floor(Date.now() / 1000);
      if (expiresInSec > REFRESH_BUFFER_SECONDS) {
        return current.access_token;
      }
    }
    // No cached session, or it's about to expire — force a refresh.
    const { data, error } = await supabase.auth.getSession();
    if (error || !data.session) return null;
    return data.session.access_token;
  }, []);

  const signOut = useCallback(async () => {
    await getSupabaseBrowserClient().auth.signOut();
  }, []);

  // Publish the accessor so non-React code paths (api.ts, WS client) can
  // read the JWT without prop-drilling through the provider tree.
  useEffect(() => {
    publishAccessTokenAccessor(getAccessToken);
  }, [getAccessToken]);

  const value = useMemo<AuthState>(
    () => ({
      user,
      session,
      loading,
      accessToken: session?.access_token ?? null,
      getAccessToken,
      signOut,
    }),
    [user, session, loading, getAccessToken, signOut],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}

// Token holder pattern: lib/api.ts and lib/ws/client.ts run outside the
// React tree, so they can't call useAuth(). Instead the AuthProvider
// publishes the latest accessor here on every render and the modules
// read it via getCurrentAccessTokenAccessor().
let currentAccessor: (() => Promise<string | null>) | null = null;

export function publishAccessTokenAccessor(fn: () => Promise<string | null>) {
  currentAccessor = fn;
}

export async function getAccessToken(): Promise<string | null> {
  if (!currentAccessor) return null;
  return currentAccessor();
}
