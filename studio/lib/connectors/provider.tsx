"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  fetchComposioAliases,
  fetchComposioConnected,
  type ComposioAliasMap,
  type ComposioConnectedAccount,
} from "@/lib/api";
import { isPendingConnectorStatus } from "@/lib/connectors/active";

/**
 * One owner of "which accounts are connected".
 *
 * The Accounts screen used to fetch this itself, which meant the Settings
 * rail — which renders whether or not that screen is mounted — had no way to
 * see it and fell back to counting MCP processes instead. The fetch, the
 * refresh-on-return and the mid-handshake polling all live here now, so the
 * rail's number and the screen's list come from the same array.
 */

type ConnectorAccountsValue = {
  accounts: ComposioConnectedAccount[];
  aliases: ComposioAliasMap;
  /** Set when the connector backend could not be reached or answered badly. */
  error: string | null;
  loading: boolean;
  /** Refetch. `background` keeps the list on screen instead of flashing a spinner. */
  reload: (background?: boolean) => Promise<void>;
  /** Optimistic local alias write; the caller persists and reloads on failure. */
  setAliasLocal: (accountId: string, alias: string) => void;
};

const Ctx = createContext<ConnectorAccountsValue | null>(null);

export function ConnectorAccountsProvider({ children }: { children: ReactNode }) {
  const [accounts, setAccounts] = useState<ComposioConnectedAccount[]>([]);
  const [aliases, setAliases] = useState<ComposioAliasMap>({});
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const reload = useCallback(async (background = false) => {
    if (!background) setLoading(true);
    const [accountsRes, aliasMap] = await Promise.all([
      fetchComposioConnected(),
      fetchComposioAliases(),
    ]);
    if ("error" in accountsRes) {
      // An unreachable backend is NOT "no accounts". Keep the error so the
      // screen says so rather than rendering a confident empty list, and so
      // the rail shows no number rather than a confident zero.
      setAccounts([]);
      setError(accountsRes.error);
    } else {
      setAccounts(accountsRes.items ?? []);
      setError(null);
    }
    setAliases(aliasMap);
    if (!background) setLoading(false);
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  // The OAuth round-trip happens in another tab (or, on the iPhone PWA, an
  // in-app browser sheet), and iOS freezes this page's timers the moment it
  // goes to the background - so a "reload in 3s" never fires there. The
  // status refresh has to key off COMING BACK: refetch whenever the app
  // regains visibility/focus, same listeners the ws provider uses.
  useEffect(() => {
    const refresh = () => {
      if (document.visibilityState === "visible") void reload(true);
    };
    window.addEventListener("focus", refresh);
    window.addEventListener("pageshow", refresh);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      window.removeEventListener("focus", refresh);
      window.removeEventListener("pageshow", refresh);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, [reload]);

  // While any account is mid-handshake (INITIATED and friends), poll until
  // Composio flips it to ACTIVE - the flip happens on their side after the
  // provider redirects, so nothing pushes it to us.
  const hasPendingAccount = useMemo(
    () => accounts.some((a) => isPendingConnectorStatus(a.status ?? "")),
    [accounts],
  );

  useEffect(() => {
    if (!hasPendingAccount) return;
    const t = setInterval(() => {
      if (document.visibilityState === "visible") void reload(true);
    }, 5000);
    return () => clearInterval(t);
  }, [hasPendingAccount, reload]);

  const setAliasLocal = useCallback((accountId: string, alias: string) => {
    setAliases((prev) => ({ ...prev, [accountId]: alias }));
  }, []);

  const value = useMemo<ConnectorAccountsValue>(
    () => ({ accounts, aliases, error, loading, reload, setAliasLocal }),
    [accounts, aliases, error, loading, reload, setAliasLocal],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useConnectorAccounts(): ConnectorAccountsValue {
  const v = useContext(Ctx);
  if (!v) {
    throw new Error("useConnectorAccounts must be used inside <ConnectorAccountsProvider>");
  }
  return v;
}
