"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  AlertCircle,
  Check,
  ChevronDown,
  CircleDashed,
  Link as LinkIcon,
  Pencil,
  Plus,
  Search,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { PageTabs, PageTabsList, PageTabsTrigger } from "@/components/ui/page-tabs";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Drawer, DrawerContent, DrawerDescription, DrawerFooter, DrawerHeader, DrawerTitle } from "@/components/ui/drawer";
import { useMediaQuery } from "@/lib/use-media-query";
import { cn } from "@/lib/utils";
import {
  disconnectComposioAccount,
  fetchComposioAliases,
  fetchComposioConnected,
  fetchComposioToolkits,
  initiateComposioConnect,
  setComposioAlias,
  type ComposioAliasMap,
  type ComposioConnectedAccount,
  type ComposioToolkit,
  type MCPStatus,
} from "@/lib/api";

// ConnectorsSection is the single surface for managing every MCP/integration
// the agent can call. Three sub-tabs:
//
//   Active  → native mcp.yaml servers (claude_code, github, composio, …)
//             merged with Composio toolkits. Multiple connected accounts per
//             toolkit (e.g. four Gmail mailboxes) collapse into a single
//             toolkit group with per-account sub-rows — each row has an
//             editable alias + disconnect, plus an "Add another account"
//             button on the group header.
//   Browse  → searchable Composio catalog (~250 toolkits).
//   Custom  → placeholder until the user_mcp_servers table lands.

type Tab = "active" | "browse" | "custom";

export function ConnectorsSection({ servers }: { servers: MCPStatus[] }) {
  const [tab, setTab] = useState<Tab>("active");

  const [connected, setConnected] = useState<ComposioConnectedAccount[]>([]);
  const [connectedError, setConnectedError] = useState<string | null>(null);
  const [connectedLoading, setConnectedLoading] = useState(true);
  const [aliases, setAliases] = useState<ComposioAliasMap>({});

  const [catalog, setCatalog] = useState<ComposioToolkit[]>([]);
  const [catalogError, setCatalogError] = useState<string | null>(null);
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [catalogQuery, setCatalogQuery] = useState("");
  const [connecting, setConnecting] = useState<string | null>(null);

  const [activeQuery, setActiveQuery] = useState("");

  // Pre-connect alias prompt. Required field — boss can't initiate OAuth
  // without naming the account. Eliminates the "four indistinguishable
  // Gmails" failure mode common to bare Composio integrations.
  const [pendingConnect, setPendingConnect] = useState<{
    slug: string;
    name: string;
    logo?: string;
    existingAliases: string[];
  } | null>(null);

  const loadConnected = useCallback(async () => {
    setConnectedLoading(true);
    const [accountsRes, aliasMap] = await Promise.all([
      fetchComposioConnected(),
      fetchComposioAliases(),
    ]);
    if ("error" in accountsRes) {
      setConnected([]);
      setConnectedError(accountsRes.error);
    } else {
      setConnected(accountsRes.items ?? []);
      setConnectedError(null);
    }
    setAliases(aliasMap);
    setConnectedLoading(false);
  }, []);

  const loadCatalog = useCallback(
    async (reset = true) => {
      setCatalogLoading(true);
      const r = await fetchComposioToolkits({
        q: catalogQuery || undefined,
        cursor: reset ? undefined : nextCursor ?? undefined,
        limit: 30,
      });
      if ("error" in r) {
        if (reset) setCatalog([]);
        setCatalogError(r.error);
        setNextCursor(null);
      } else {
        setCatalogError(null);
        setCatalog((prev) => (reset ? r.items ?? [] : [...prev, ...(r.items ?? [])]));
        setNextCursor(r.next_cursor ?? null);
      }
      setCatalogLoading(false);
    },
    [catalogQuery, nextCursor],
  );

  useEffect(() => {
    loadConnected();
  }, [loadConnected]);

  useEffect(() => {
    if (tab === "browse" && catalog.length === 0 && !catalogError) {
      loadCatalog(true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  useEffect(() => {
    if (tab !== "browse") return;
    const t = setTimeout(() => loadCatalog(true), 250);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalogQuery]);

  const connectedSlugs = useMemo(() => {
    const s = new Set<string>();
    for (const c of connected) {
      const slug = c.toolkit?.slug ?? c.toolkit?.name ?? "";
      if (slug) s.add(slug.toLowerCase());
    }
    return s;
  }, [connected]);

  // Grouped activated view. Composio toolkits collapse into one row per slug
  // with each connected_account as a sub-row. Native MCP servers from
  // mcp.yaml render as single-account groups. The composio parent MCP
  // entry is hidden when at least one toolkit account is connected — its
  // "connection status" is implicit in the children.
  const activeGroups = useMemo<ActiveGroup[]>(() => {
    const groups: ActiveGroup[] = [];
    const hasComposio = connected.length > 0;

    for (const s of servers) {
      if (hasComposio && s.name === "composio") continue;
      groups.push({
        kind: "native",
        key: `mcp:${s.name}`,
        slug: s.name,
        name: s.name,
        source: nativeSourceLabel(s.name),
        accounts: [
          {
            id: s.name,
            ok: s.connected,
            error: s.error,
            tools: s.tools ?? [],
            statusText: s.connected ? "ACTIVE" : s.error ? "ERROR" : "PENDING",
          },
        ],
      });
    }

    // Composio: group by toolkit slug.
    const byToolkit = new Map<string, ComposioConnectedAccount[]>();
    for (const acc of connected) {
      const slug = (acc.toolkit?.slug ?? "unknown").toLowerCase();
      const arr = byToolkit.get(slug) ?? [];
      arr.push(acc);
      byToolkit.set(slug, arr);
    }
    for (const [slug, accs] of byToolkit.entries()) {
      const first = accs[0];
      groups.push({
        kind: "composio",
        key: `composio:${slug}`,
        slug,
        name: first.toolkit?.name ?? slug,
        source: "via Composio",
        logo: first.toolkit?.logo,
        accounts: accs.map((a) => ({
          id: a.id,
          accountId: a.id,
          ok: ((a.status ?? "").toUpperCase() || "ACTIVE") === "ACTIVE",
          statusText: (a.status ?? "ACTIVE").toUpperCase(),
          alias: aliases[a.id] ?? "",
          identityHint: extractIdentityHint(a),
          userId: a.user_id,
          createdAt: a.created_at,
          tools: [],
        })),
      });
    }

    const q = activeQuery.trim().toLowerCase();
    if (!q) return groups;
    return groups.filter((g) => {
      if (g.name.toLowerCase().includes(q) || g.slug.toLowerCase().includes(q)) return true;
      for (const a of g.accounts) {
        if (a.alias?.toLowerCase().includes(q)) return true;
        if (a.identityHint?.toLowerCase().includes(q)) return true;
        if (a.tools?.some((t) => t.toLowerCase().includes(q))) return true;
      }
      return false;
    });
  }, [servers, connected, aliases, activeQuery]);

  const totalActiveCount = useMemo(
    () => activeGroups.reduce((sum, g) => sum + g.accounts.length, 0),
    [activeGroups],
  );

  // requestConnect opens the alias dialog. Actual OAuth doesn't fire until
  // the dialog submits, so the boss always gives the account a name first.
  // Pass existing aliases for this toolkit so the dialog can warn on
  // duplicates without us re-fetching.
  function requestConnect(slug: string, displayName: string, logo?: string) {
    const existing = connected
      .filter((c) => (c.toolkit?.slug ?? "").toLowerCase() === slug.toLowerCase())
      .map((c) => aliases[c.id] ?? "")
      .filter(Boolean);
    setPendingConnect({ slug, name: displayName, logo, existingAliases: existing });
  }

  async function handleConnect(slug: string, opts: { userId: string; alias: string }) {
    setConnecting(slug);
    const r = await initiateComposioConnect(slug, opts);
    setConnecting(null);
    if (r.error) {
      // eslint-disable-next-line no-alert
      alert(`Couldn't start ${slug} connection: ${r.error}`);
      return;
    }
    if (r.redirect_url) window.open(r.redirect_url, "_blank", "noopener,noreferrer");
    setTimeout(() => loadConnected(), 3000);
  }

  async function handleDisconnect(id: string, label: string) {
    // eslint-disable-next-line no-alert
    if (!confirm(`Disconnect ${label}? Tools that depend on it will stop working.`)) return;
    const ok = await disconnectComposioAccount(id);
    if (!ok) {
      // eslint-disable-next-line no-alert
      alert("Couldn't disconnect. Try again or remove from Composio dashboard.");
      return;
    }
    await loadConnected();
  }

  async function handleAliasSave(accountId: string, alias: string) {
    setAliases((prev) => ({ ...prev, [accountId]: alias }));
    const ok = await setComposioAlias(accountId, alias);
    if (!ok) {
      // eslint-disable-next-line no-alert
      alert("Couldn't save alias — refreshing to recover canonical state.");
      await loadConnected();
    }
  }

  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <h2 className="text-base font-semibold tracking-tight">Connectors</h2>
        <p className="text-xs text-muted-foreground">
          MCP servers and Composio integrations the agent can call. Each connected toolkit
          adds tool schemas to the system prompt — keep the activated set tight to control
          context budget. Connect the same toolkit more than once for multi-account routing
          (e.g. personal + work Gmail).
        </p>
      </div>

      <PageTabs value={tab} onValueChange={(v) => setTab(v as Tab)}>
        <PageTabsList columns={3}>
          <PageTabsTrigger value="active">
            Active{totalActiveCount ? ` (${totalActiveCount})` : ""}
          </PageTabsTrigger>
          <PageTabsTrigger value="browse">Browse</PageTabsTrigger>
          <PageTabsTrigger value="custom">Custom</PageTabsTrigger>
        </PageTabsList>
      </PageTabs>

      {tab === "active" && (
        <ActiveList
          groups={activeGroups}
          query={activeQuery}
          onQueryChange={setActiveQuery}
          loading={connectedLoading}
          composioError={connectedError}
          onDisconnect={handleDisconnect}
          onBrowse={() => setTab("browse")}
          onAliasSave={handleAliasSave}
          onAddAnother={(slug, name, logo) => requestConnect(slug, name, logo)}
          connecting={connecting}
        />
      )}
      {tab === "browse" && (
        <BrowseList
          loading={catalogLoading}
          toolkits={catalog}
          error={catalogError}
          query={catalogQuery}
          onQueryChange={setCatalogQuery}
          hasMore={Boolean(nextCursor)}
          onLoadMore={() => loadCatalog(false)}
          connectedSlugs={connectedSlugs}
          onConnect={(slug, name, logo) => requestConnect(slug, name, logo)}
          connecting={connecting}
        />
      )}
      {tab === "custom" && <CustomComingSoon />}

      {pendingConnect && (
        <NameAccountPrompt
          toolkit={pendingConnect}
          onCancel={() => setPendingConnect(null)}
          onSubmit={async (alias) => {
            const slug = pendingConnect.slug;
            setPendingConnect(null);
            await handleConnect(slug, { userId: alias, alias });
          }}
        />
      )}
    </div>
  );
}

type ActiveAccount = {
  id: string;
  accountId?: string; // composio-only
  ok: boolean;
  error?: string;
  statusText: string;
  alias?: string;
  identityHint?: string;
  userId?: string;
  createdAt?: string;
  tools: string[];
};

type ActiveGroup = {
  kind: "native" | "composio";
  key: string;
  slug: string;
  name: string;
  source: string;
  logo?: string;
  accounts: ActiveAccount[];
};

function ActiveList({
  groups,
  query,
  onQueryChange,
  loading,
  composioError,
  onDisconnect,
  onBrowse,
  onAliasSave,
  onAddAnother,
  connecting,
}: {
  groups: ActiveGroup[];
  query: string;
  onQueryChange: (v: string) => void;
  loading: boolean;
  composioError: string | null;
  onDisconnect: (id: string, label: string) => void;
  onBrowse: () => void;
  onAliasSave: (accountId: string, alias: string) => void;
  onAddAnother: (slug: string, name: string, logo?: string) => void;
  connecting: string | null;
}) {
  if (groups.length === 0 && !loading && !query) {
    return (
      <div className="space-y-3">
        {composioError && <ComposioErrorBanner message={composioError} />}
        <div className="flex flex-col items-center justify-center gap-2 rounded-xl border bg-muted/30 p-6 text-center">
          <LinkIcon className="size-7 text-muted-foreground" aria-hidden />
          <p className="text-sm font-semibold">Nothing activated yet</p>
          <p className="max-w-sm text-xs text-muted-foreground">
            Connect your first SaaS account from the catalog. Each one gives the agent a new
            set of <code className="font-mono">composio__*</code> tools.
          </p>
          <Button onClick={onBrowse} size="sm" className="mt-1">
            Browse catalog
          </Button>
        </div>
      </div>
    );
  }
  return (
    <div className="space-y-2">
      {composioError && <ComposioErrorBanner message={composioError} />}
      <SearchInput value={query} onChange={onQueryChange} placeholder="Search by name, alias, or tool…" />
      {query && (
        <p className="text-[11px] text-muted-foreground">
          {groups.length} group{groups.length === 1 ? "" : "s"} match
        </p>
      )}
      <ul className="space-y-1.5">
        {groups.map((g) => (
          <ActiveGroupCard
            key={g.key}
            group={g}
            highlightTool={query}
            onDisconnect={onDisconnect}
            onAliasSave={onAliasSave}
            onAddAnother={onAddAnother}
            connecting={connecting}
          />
        ))}
      </ul>
    </div>
  );
}

function ActiveGroupCard({
  group,
  highlightTool,
  onDisconnect,
  onAliasSave,
  onAddAnother,
  connecting,
}: {
  group: ActiveGroup;
  highlightTool?: string;
  onDisconnect: (id: string, label: string) => void;
  onAliasSave: (accountId: string, alias: string) => void;
  onAddAnother: (slug: string, name: string, logo?: string) => void;
  connecting: string | null;
}) {
  const matchedTool = Boolean(
    highlightTool &&
      group.accounts.some((a) => a.tools?.some((t) => t.toLowerCase().includes(highlightTool.toLowerCase()))),
  );
  // Multi-account groups open by default so the boss can see all accounts
  // without an extra tap. Single-account groups stay collapsed to keep
  // the list scannable.
  const [open, setOpen] = useState(group.accounts.length > 1);
  const isOpen = open || matchedTool;
  const totalTools = group.accounts.reduce((sum, a) => sum + (a.tools?.length ?? 0), 0);

  return (
    <li className="overflow-hidden rounded-md border bg-background">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-accent/40"
      >
        {group.kind === "composio" && group.logo ? (
          /* eslint-disable-next-line @next/next/no-img-element */
          <img src={group.logo} alt="" className="size-5 shrink-0 rounded object-contain" />
        ) : group.accounts[0].ok ? (
          <Check className="size-3.5 shrink-0 text-success" aria-hidden />
        ) : group.accounts[0].error ? (
          <X className="size-3.5 shrink-0 text-danger" aria-hidden />
        ) : (
          <CircleDashed className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        )}
        <span className="truncate text-xs font-semibold">{group.name}</span>
        <Badge variant="secondary" className="h-4 shrink-0 px-1 font-mono text-[9px] uppercase">
          {group.source}
        </Badge>
        {group.accounts.length > 1 && (
          <Badge className="h-4 shrink-0 bg-info/15 px-1 font-mono text-[9px] text-info">
            {group.accounts.length} accounts
          </Badge>
        )}
        {totalTools > 0 && (
          <Badge variant="secondary" className="h-4 shrink-0 px-1 font-mono text-[9px]">
            {totalTools}
          </Badge>
        )}
        <span className="ml-auto flex items-center gap-1">
          <ChevronDown
            className={cn(
              "size-3.5 shrink-0 text-muted-foreground transition-transform",
              isOpen && "rotate-180",
            )}
            aria-hidden
          />
        </span>
      </button>
      {isOpen && (
        <div className="space-y-2 border-t bg-muted/30 px-3 py-2.5">
          {group.kind === "composio" && (
            <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
              <span>
                Tools register as{" "}
                <code className="font-mono">composio__{group.slug.toUpperCase()}_*</code>. Add another
                account to authorise a second mailbox/workspace/org.
              </span>
              <button
                type="button"
                onClick={() => onAddAnother(group.slug, group.name, group.logo)}
                disabled={connecting === group.slug}
                className="inline-flex h-7 shrink-0 items-center gap-1 rounded border bg-background px-2 text-[11px] font-medium hover:bg-accent"
              >
                <Plus className="size-3" />
                {connecting === group.slug ? "Opening…" : "Add another"}
              </button>
            </div>
          )}
          <ul className="space-y-1.5">
            {group.accounts.map((a) => (
              <AccountSubRow
                key={a.id}
                account={a}
                groupName={group.name}
                kind={group.kind}
                onDisconnect={onDisconnect}
                onAliasSave={onAliasSave}
                highlightTool={highlightTool}
              />
            ))}
          </ul>
        </div>
      )}
    </li>
  );
}

function AccountSubRow({
  account,
  groupName,
  kind,
  onDisconnect,
  onAliasSave,
  highlightTool,
}: {
  account: ActiveAccount;
  groupName: string;
  kind: "native" | "composio";
  onDisconnect: (id: string, label: string) => void;
  onAliasSave: (accountId: string, alias: string) => void;
  highlightTool?: string;
}) {
  const [editingAlias, setEditingAlias] = useState(false);
  const [aliasDraft, setAliasDraft] = useState(account.alias ?? "");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editingAlias) inputRef.current?.focus();
  }, [editingAlias]);

  useEffect(() => {
    setAliasDraft(account.alias ?? "");
  }, [account.alias]);

  function commitAlias() {
    setEditingAlias(false);
    if ((account.alias ?? "") === aliasDraft) return;
    if (account.accountId) onAliasSave(account.accountId, aliasDraft.trim());
  }

  const displayLabel =
    account.alias?.trim() ||
    account.identityHint ||
    (kind === "composio" ? account.accountId?.slice(-8) ?? "account" : account.id);

  return (
    <li className="flex items-start gap-2 rounded-md border border-border/40 bg-background px-2.5 py-2">
      {/* Status icon */}
      {account.ok ? (
        <Check className="mt-0.5 size-3.5 shrink-0 text-success" aria-hidden />
      ) : account.error ? (
        <X className="mt-0.5 size-3.5 shrink-0 text-danger" aria-hidden />
      ) : (
        <CircleDashed className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden />
      )}

      <div className="min-w-0 flex-1 space-y-1">
        {/* Alias / identity row */}
        <div className="flex flex-wrap items-center gap-2">
          {kind === "composio" && editingAlias ? (
            <input
              ref={inputRef}
              value={aliasDraft}
              onChange={(e) => setAliasDraft(e.target.value)}
              onBlur={commitAlias}
              onKeyDown={(e) => {
                if (e.key === "Enter") commitAlias();
                if (e.key === "Escape") {
                  setAliasDraft(account.alias ?? "");
                  setEditingAlias(false);
                }
              }}
              placeholder="alias (e.g. work, personal)"
              className="h-6 max-w-[220px] flex-1 rounded border bg-background px-1.5 text-xs focus:outline-none focus:ring-1 focus:ring-info"
            />
          ) : (
            <button
              type="button"
              onClick={() => kind === "composio" && setEditingAlias(true)}
              className={cn(
                "flex items-center gap-1 text-xs font-medium",
                kind === "composio" && "rounded px-1 hover:bg-accent/50",
              )}
              disabled={kind !== "composio"}
            >
              {displayLabel}
              {kind === "composio" && <Pencil className="size-3 text-muted-foreground" aria-hidden />}
            </button>
          )}
          <Badge
            variant="secondary"
            className={cn(
              "h-4 px-1 font-mono text-[9px] uppercase",
              account.ok ? "bg-success/10 text-success" : "bg-danger/10 text-danger",
            )}
          >
            {account.statusText}
          </Badge>
        </div>

        {/* Secondary line — identity hint (if alias is set, also surface
            email for disambiguation) + account id tail. */}
        {kind === "composio" && (account.identityHint || account.accountId) && (
          <p className="truncate text-[10px] text-muted-foreground">
            {account.identityHint && (
              <span className="mr-2">{account.identityHint}</span>
            )}
            {account.accountId && (
              <code className="font-mono">id={account.accountId}</code>
            )}
          </p>
        )}

        {/* Tools (native only — composio tools live under the gateway entry) */}
        {(account.tools?.length ?? 0) > 0 && (
          <div className="flex flex-wrap gap-1">
            {(account.tools ?? []).map((t) => {
              const m =
                highlightTool && t.toLowerCase().includes(highlightTool.toLowerCase());
              return (
                <Badge
                  key={t}
                  variant="secondary"
                  className={cn(
                    "font-mono text-[10px]",
                    m && "bg-info/15 text-info ring-1 ring-info/40",
                  )}
                >
                  {t}
                </Badge>
              );
            })}
          </div>
        )}

        {account.error && (
          <p className="break-words rounded-sm bg-danger/10 p-1.5 text-[10px] text-danger">
            {account.error}
          </p>
        )}
      </div>

      {kind === "composio" && account.accountId && (
        <button
          type="button"
          onClick={() => onDisconnect(account.accountId!, displayLabel)}
          className="inline-flex h-7 shrink-0 items-center gap-1 rounded px-2 text-[10px] text-muted-foreground hover:bg-danger/10 hover:text-danger"
          aria-label={`Disconnect ${displayLabel}`}
        >
          <X className="size-3" />
        </button>
      )}
      {/* Suppress unused-prop warning */}
      <span className="sr-only">{groupName}</span>
    </li>
  );
}

function BrowseList({
  loading,
  toolkits,
  error,
  query,
  onQueryChange,
  hasMore,
  onLoadMore,
  connectedSlugs,
  onConnect,
  connecting,
}: {
  loading: boolean;
  toolkits: ComposioToolkit[];
  error: string | null;
  query: string;
  onQueryChange: (q: string) => void;
  hasMore: boolean;
  onLoadMore: () => void;
  connectedSlugs: Set<string>;
  onConnect: (slug: string, name: string, logo?: string) => void;
  connecting: string | null;
}) {
  return (
    <div className="space-y-3">
      <SearchInput
        value={query}
        onChange={onQueryChange}
        placeholder="Search 250+ integrations…"
      />
      {error ? (
        <ComposioErrorBanner
          message={error}
          hint="If the error mentions undeployed routes, push core. If it mentions 401/invalid key, set COMPOSIO_ADMIN_API_KEY (workspace admin tier — separate from the MCP consumer key)."
        />
      ) : toolkits.length === 0 && loading ? (
        <p className="text-sm text-muted-foreground">Loading catalog…</p>
      ) : toolkits.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {query ? `No integrations match "${query}".` : "No integrations returned."}
        </p>
      ) : (
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {toolkits.map((t) => {
            const isConnected = connectedSlugs.has((t.slug ?? "").toLowerCase());
            const busy = connecting === t.slug;
            return (
              <article
                key={t.slug}
                className="flex h-full flex-col gap-2 rounded-xl border bg-card p-3"
              >
                <div className="flex items-center gap-2">
                  <ToolkitLogo logo={t.meta?.logo} slug={t.slug} className="size-7 shrink-0" />
                  <p className="min-w-0 flex-1 truncate text-sm font-semibold">
                    {t.name ?? t.slug}
                  </p>
                  {isConnected && (
                    <Badge className="bg-success/15 text-success">
                      <Check className="mr-0.5 size-3" />
                      Active
                    </Badge>
                  )}
                </div>
                {t.meta?.description ? (
                  <p className="line-clamp-2 text-xs text-muted-foreground">
                    {t.meta.description}
                  </p>
                ) : (
                  <p className="text-xs italic text-muted-foreground/70">No description.</p>
                )}
                <div className="mt-auto flex gap-2">
                  <Button
                    size="sm"
                    variant={isConnected ? "ghost" : "default"}
                    className="h-9 flex-1"
                    disabled={busy}
                    onClick={() => onConnect(t.slug, t.name ?? t.slug, t.meta?.logo)}
                  >
                    {busy ? "Opening…" : isConnected ? "Add another" : "Connect"}
                  </Button>
                </div>
              </article>
            );
          })}
        </div>
      )}
      {hasMore && !error && (
        <div className="flex justify-center pt-2">
          <Button variant="ghost" size="sm" onClick={onLoadMore} disabled={loading}>
            {loading ? "Loading…" : "Load more"}
          </Button>
        </div>
      )}
    </div>
  );
}

function CustomComingSoon() {
  return (
    <div className="space-y-3 rounded-xl border bg-muted/30 p-4">
      <div className="flex items-center gap-2">
        <Plus className="size-4 text-muted-foreground" aria-hidden />
        <h3 className="text-sm font-semibold">Bring your own MCP</h3>
        <Badge variant="secondary" className="font-mono text-[9px] uppercase">
          soon
        </Badge>
      </div>
      <p className="text-xs text-muted-foreground">
        For now, MCPs that aren't on Composio go through{" "}
        <code className="font-mono">core/config/mcp.yaml</code> — add a server entry, set any
        required env vars on Railway, redeploy. See the existing{" "}
        <code className="font-mono">claude_code</code> and <code className="font-mono">composio</code>{" "}
        entries as a reference.
      </p>
      <p className="text-xs text-muted-foreground">
        The in-app "add custom MCP" form will land when we add a{" "}
        <code className="font-mono">user_mcp_servers</code> table — it needs persistence so
        servers survive deploys without a rebuild.
      </p>
    </div>
  );
}

function SearchInput({
  value,
  onChange,
  placeholder,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
}) {
  return (
    <div className="relative">
      <Search
        className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
        aria-hidden
      />
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        inputMode="search"
        className="pl-9"
      />
    </div>
  );
}

function ToolkitLogo({
  logo,
  slug,
  className,
}: {
  logo?: string;
  slug?: string;
  className?: string;
}) {
  const [failed, setFailed] = useState(false);
  const initial = (slug ?? "?").charAt(0).toUpperCase();
  if (!logo || failed) {
    return (
      <div
        className={cn(
          "flex items-center justify-center rounded-md bg-muted font-mono text-sm font-semibold text-muted-foreground",
          className,
        )}
      >
        {initial}
      </div>
    );
  }
  return (
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src={logo}
      alt=""
      onError={() => setFailed(true)}
      className={cn("rounded-md object-contain", className)}
    />
  );
}

function ComposioErrorBanner({ message, hint }: { message: string; hint?: string }) {
  return (
    <div className="rounded-xl border border-danger/30 bg-danger/5 p-3">
      <div className="flex items-start gap-2">
        <AlertCircle className="mt-0.5 size-4 shrink-0 text-danger" aria-hidden />
        <div className="min-w-0">
          <p className="text-sm font-semibold text-danger">Composio request failed</p>
          <p className="mt-1 break-words text-xs text-danger/80">{message}</p>
          {hint && (
            <p className="mt-2 break-words text-[11px] text-muted-foreground">{hint}</p>
          )}
        </div>
      </div>
    </div>
  );
}

// extractIdentityHint pulls a recognisable label from Composio's per-
// account meta/data blobs. The exact field name varies per toolkit
// (email for Gmail, login for GitHub, team_name for Slack) so we walk
// a list of common identity keys. Best-effort — returns "" when the
// upstream response doesn't surface anything usable.
function extractIdentityHint(acc: ComposioConnectedAccount): string {
  const candidates = ["email", "username", "user_email", "login", "display_name", "name", "team_name", "workspace_name"];
  const pools: Array<Record<string, unknown> | undefined> = [acc.meta, acc.data];
  for (const pool of pools) {
    if (!pool) continue;
    for (const key of candidates) {
      const v = pool[key];
      if (typeof v === "string" && v.trim() !== "") return v.trim();
    }
    for (const nestedKey of ["user", "profile", "account", "authed_user"]) {
      const nested = pool[nestedKey];
      if (nested && typeof nested === "object") {
        for (const key of candidates) {
          const v = (nested as Record<string, unknown>)[key];
          if (typeof v === "string" && v.trim() !== "") return v.trim();
        }
      }
    }
  }
  return "";
}

function nativeSourceLabel(name: string): string {
  switch (name) {
    case "claude_code":
      return "Mac bridge";
    case "github":
      return "Direct API";
    case "composio":
      return "Gateway";
    default:
      return "Direct MCP";
  }
}

// NameAccountPrompt is the mandatory pre-connect gate. The OAuth flow does
// not start until the boss provides a label — this is what eliminates the
// "four indistinguishable Gmails" failure mode. The same alias is sent to
// Composio as the `user_id` (so their dashboard also shows the label) AND
// stored locally as the human-readable alias. Two-purpose, one input.
//
// Validation: non-empty, no duplicate within the same toolkit, ≤ 32 chars,
// reasonable charset (alphanumeric + space/hyphen/underscore). Slack-style
// channel names rather than free-form to keep the agent's prompt overlay
// scannable.
function NameAccountPrompt({
  toolkit,
  onCancel,
  onSubmit,
}: {
  toolkit: { slug: string; name: string; logo?: string; existingAliases: string[] };
  onCancel: () => void;
  onSubmit: (alias: string) => void;
}) {
  const isDesktop = useMediaQuery("(min-width: 640px)");
  const [alias, setAlias] = useState("");
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    // Autofocus once the dialog mounts so the boss can start typing
    // immediately. Slight delay because Radix Dialog animates in.
    const t = setTimeout(() => inputRef.current?.focus(), 60);
    return () => clearTimeout(t);
  }, []);

  function validate(v: string): string | null {
    const trimmed = v.trim();
    if (trimmed === "") return "Required — name this account so you can route to it later.";
    if (trimmed.length > 32) return "Keep it short (≤ 32 chars).";
    if (!/^[a-zA-Z0-9 _-]+$/.test(trimmed)) {
      return "Use letters, numbers, spaces, hyphens, or underscores only.";
    }
    if (toolkit.existingAliases.some((a) => a.toLowerCase() === trimmed.toLowerCase())) {
      return `"${trimmed}" is already used for another ${toolkit.name} account.`;
    }
    return null;
  }

  function tryCommit() {
    const err = validate(alias);
    if (err) {
      setError(err);
      return;
    }
    onSubmit(alias.trim());
  }

  const body = (
    <div className="space-y-3 px-1">
      <p className="text-xs text-muted-foreground">
        Pick a short label for this {toolkit.name} account — &ldquo;personal&rdquo;,
        &ldquo;work&rdquo;, &ldquo;newsletters&rdquo;. The agent uses this name to route
        ("send from work account"). You can change it later from the activated list.
      </p>
      {toolkit.existingAliases.length > 0 && (
        <p className="text-[11px] text-muted-foreground">
          Already used:{" "}
          {toolkit.existingAliases.map((a, i) => (
            <span key={a}>
              <code className="font-mono">{a}</code>
              {i < toolkit.existingAliases.length - 1 ? ", " : ""}
            </span>
          ))}
        </p>
      )}
      <div className="space-y-1">
        <label htmlFor="connector-alias" className="block text-[11px] font-medium text-muted-foreground">
          Account label
        </label>
        <Input
          id="connector-alias"
          ref={inputRef}
          value={alias}
          onChange={(e) => {
            setAlias(e.target.value);
            if (error) setError(null);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") tryCommit();
          }}
          placeholder={defaultAliasPlaceholder(toolkit.slug)}
          inputMode="text"
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
        />
        {error && <p className="text-[11px] text-danger">{error}</p>}
      </div>
    </div>
  );

  const header = (
    <div className="flex items-center gap-2">
      {toolkit.logo ? (
        /* eslint-disable-next-line @next/next/no-img-element */
        <img src={toolkit.logo} alt="" className="size-6 rounded object-contain" />
      ) : (
        <div className="flex size-6 items-center justify-center rounded bg-muted font-mono text-xs font-semibold">
          {toolkit.name.charAt(0).toUpperCase()}
        </div>
      )}
      <span>Connect {toolkit.name}</span>
    </div>
  );

  const description =
    "Naming the account up-front means the agent can always tell which one you mean — even after you connect a second or third.";

  if (isDesktop) {
    return (
      <Dialog open onOpenChange={(o) => !o && onCancel()}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{header}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          {body}
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={onCancel}>
              Cancel
            </Button>
            <Button size="sm" onClick={tryCommit}>
              Continue to OAuth
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Drawer open onOpenChange={(o) => !o && onCancel()}>
      <DrawerContent>
        <DrawerHeader>
          <DrawerTitle>{header}</DrawerTitle>
          <DrawerDescription>{description}</DrawerDescription>
        </DrawerHeader>
        <div className="px-4 pb-2">{body}</div>
        <DrawerFooter className="flex-row justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button size="sm" onClick={tryCommit}>
            Continue to OAuth
          </Button>
        </DrawerFooter>
      </DrawerContent>
    </Drawer>
  );
}

// defaultAliasPlaceholder hints at sensible labels per toolkit so the boss
// has a starting point. Falls back to a generic placeholder for the long
// tail.
function defaultAliasPlaceholder(slug: string): string {
  switch (slug.toLowerCase()) {
    case "gmail":
    case "googlecalendar":
    case "googledrive":
    case "googledocs":
    case "googlesheets":
      return "personal, work, …";
    case "slack":
      return "team workspace name";
    case "github":
    case "gitlab":
      return "personal, work-org, …";
    case "notion":
      return "personal, team-wiki, …";
    case "linear":
      return "team name";
    case "hubspot":
    case "salesforce":
      return "sandbox, prod";
    case "stripe":
      return "personal-acct, business";
    default:
      return "personal, work, …";
  }
}
