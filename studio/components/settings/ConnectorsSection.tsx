"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  AlertCircle,
  Info,
  Link as LinkIcon,
  Pencil,
  Plus,
  RefreshCcw,
  Search,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageTabs, PageTabsList, PageTabsTrigger } from "@/components/ui/page-tabs";
import { SettingsPanel } from "@/components/settings/SettingsPanel";
import { GroupLabel, ListRow } from "@/components/ui/list-row";
import { Inset } from "@/components/ui/inset";
import { EmptyState } from "@/components/EmptyState";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { CustomExtensions } from "@/components/settings/CustomExtensions";
import { useTabParam } from "@/lib/useTabParam";
import { openAuthWindow } from "@/lib/auth-window";
import { cn } from "@/lib/utils";
import {
  disconnectComposioAccount,
  fetchComposioToolkits,
  initiateComposioConnect,
  refreshComposioAccount,
  setComposioAlias,
  type ComposioToolkit,
  type MCPStatus,
} from "@/lib/api";
import { CountBadge } from "@/components/ui/count-badge";
import { useConnectorAccounts } from "@/lib/connectors/provider";
import {
  buildActiveConnectorGroups,
  countActiveConnectorAccounts,
  filterActiveConnectorGroups,
  isReconnectableAccount,
  type ActiveAccount,
  type ActiveGroup,
} from "@/lib/connectors/active";

// ConnectorsSection is the single surface for managing every MCP/integration
// the agent can call. Three sub-tabs:
//
//   Active  → native mcp.yaml servers (claude_code, github, composio, …)
//             merged with Composio toolkits. Multiple connected accounts per
//             toolkit (e.g. four Gmail mailboxes) collapse into a single
//             toolkit group with per-account sub-rows - each row has an
//             editable alias + disconnect, plus an "Add another account"
//             button on the group header.
//   Browse  → searchable Composio catalog (~250 toolkits).
//   Custom  → placeholder until the user_mcp_servers table lands.

type Tab = "active" | "browse" | "custom";

export function ConnectorsSection({ servers }: { servers: MCPStatus[] }) {
  // Sub-tab persists in ?connectors=<id> (distinct from settings' ?section=)
  // so a refresh on /settings?section=mcp keeps active/browse/custom.
  const [tab, setTab] = useTabParam<Tab>("connectors", "active", ["active", "browse", "custom"]);

  // Connected accounts + aliases come from the shared provider, not a local
  // fetch: the Settings rail needs the same array to count and this screen
  // is not always mounted. See lib/connectors/provider.tsx.
  const {
    accounts: connected,
    aliases,
    error: connectedError,
    loading: connectedLoading,
    reload: loadConnected,
    setAliasLocal,
  } = useConnectorAccounts();

  const [catalog, setCatalog] = useState<ComposioToolkit[]>([]);
  const [catalogError, setCatalogError] = useState<string | null>(null);
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [catalogQuery, setCatalogQuery] = useState("");
  const [connecting, setConnecting] = useState<string | null>(null);

  const [activeQuery, setActiveQuery] = useState("");

  // Pre-connect alias prompt. Required field - boss can't initiate OAuth
  // without naming the account. Eliminates the "four indistinguishable
  // Gmails" failure mode common to bare Composio integrations.
  const [pendingConnect, setPendingConnect] = useState<{
    slug: string;
    name: string;
    logo?: string;
    existingAliases: string[];
  } | null>(null);

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

  // Grouping is a pure function in lib/connectors/active.ts so the Settings
  // rail counts exactly what this list renders. Search filters the RENDERED
  // groups only - a count that shrinks while you type is not a count.
  const allActiveGroups = useMemo<ActiveGroup[]>(
    () => buildActiveConnectorGroups(servers, connected, aliases),
    [servers, connected, aliases],
  );

  const activeGroups = useMemo(
    () => filterActiveConnectorGroups(allActiveGroups, activeQuery),
    [allActiveGroups, activeQuery],
  );

  const totalActiveCount = useMemo(
    () => countActiveConnectorAccounts(allActiveGroups),
    [allActiveGroups],
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
    // Claim the OAuth window BEFORE the await - iOS Safari (and the
    // installed PWA) only allows window.open inside the synchronous tap
    // gesture; opened after the API round-trip it is silently blocked.
    const authWindow = openAuthWindow();
    setConnecting(connectKey(slug));
    const r = await initiateComposioConnect(slug, opts);
    setConnecting(null);
    if (r.error || !r.redirect_url) {
      authWindow.close();
      // eslint-disable-next-line no-alert
      alert(`Couldn't start ${slug} connection: ${r.error ?? "no sign-in link came back"}`);
      return;
    }
    authWindow.navigate(r.redirect_url);
    setTimeout(() => loadConnected(true), 3000);
  }

  async function handleReconnect(
    account: ActiveAccount,
  ) {
    if (!account.accountId) return;
    // Same gesture rule as handleConnect: claim the window before the await
    // or iOS never opens it.
    const authWindow = openAuthWindow();
    setConnecting(reconnectKey(account.accountId));
    const r = await refreshComposioAccount(account.accountId);
    setConnecting(null);
    if (r.error || !r.redirect_url) {
      authWindow.close();
      // eslint-disable-next-line no-alert
      alert(`Couldn't start reconnect: ${r.error ?? "no sign-in link came back"}`);
      return;
    }
    authWindow.navigate(r.redirect_url);
    setTimeout(() => loadConnected(true), 3000);
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
    setAliasLocal(accountId, alias);
    const ok = await setComposioAlias(accountId, alias);
    if (!ok) {
      // eslint-disable-next-line no-alert
      alert("Couldn't save alias - refreshing to recover canonical state.");
      await loadConnected();
    }
  }

  return (
    <SettingsPanel
      tabs={
        <PageTabs value={tab} onValueChange={(v) => setTab(v as Tab)}>
          <PageTabsList level="sub">
            <PageTabsTrigger value="active" className="gap-1.5">
              <span>Active</span>
              <CountBadge
                count={totalActiveCount}
                active={tab === "active"}
                noun={totalActiveCount === 1 ? "account" : "accounts"}
              />
            </PageTabsTrigger>
            <PageTabsTrigger value="browse">Browse</PageTabsTrigger>
            <PageTabsTrigger value="custom">Custom</PageTabsTrigger>
          </PageTabsList>
        </PageTabs>
      }
    >
      <div className="min-w-0 space-y-3">

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
          onReconnect={handleReconnect}
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
      {tab === "custom" && <CustomExtensions />}

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
    </SettingsPanel>
  );
}

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
  onReconnect,
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
  onReconnect: (account: ActiveAccount) => void;
  connecting: string | null;
}) {
  if (groups.length === 0 && !loading && !query) {
    // Majordomo §1.2: never a bordered empty state. The sentence says what
    // will fill it, which is the description §1.5 keeps.
    return (
      <div className="min-w-0 space-y-3">
        {composioError && <ComposioErrorBanner message={composioError} />}
        <EmptyState
          icon={LinkIcon}
          align="top"
          className="pt-8"
          title="Nothing activated yet"
          // "composio__* tools" is our plumbing, not something he asked for.
          description="Connect an account and he can act inside it."
          action={
            <Button onClick={onBrowse} size="sm">
              Browse catalog
            </Button>
          }
        />
      </div>
    );
  }
  return (
    <div className="min-w-0 space-y-2">
      {composioError && <ComposioErrorBanner message={composioError} />}
      <SearchInput value={query} onChange={onQueryChange} placeholder="Search by name, alias, or tool…" />
      {query && (
        <p className="text-[12px] text-quiet">
          {groups.length} group{groups.length === 1 ? "" : "s"} match
        </p>
      )}
      {groups.map((g) => (
        <ActiveGroupCard
          key={g.key}
          group={g}
          highlightTool={query}
          onDisconnect={onDisconnect}
          onAliasSave={onAliasSave}
          onAddAnother={onAddAnother}
          onReconnect={onReconnect}
          connecting={connecting}
        />
      ))}
    </div>
  );
}

/**
 * One toolkit = a GroupLabel + one row per connected account.
 *
 * Was: a bordered card whose header bar you tapped to reveal a tinted panel
 * of bordered sub-rows — three containers deep for what is a list of accounts
 * (Majordomo §2). The group label carries the source and the "Add another"
 * action; each account is a `ListRow` with its own status dot, alias editing,
 * reconnect and disconnect. Multi-account routing is untouched: every account
 * still gets its own row, its own alias, and its own controls.
 */
function ActiveGroupCard({
  group,
  highlightTool,
  onDisconnect,
  onAliasSave,
  onAddAnother,
  onReconnect,
  connecting,
}: {
  group: ActiveGroup;
  highlightTool?: string;
  onDisconnect: (id: string, label: string) => void;
  onAliasSave: (accountId: string, alias: string) => void;
  onAddAnother: (slug: string, name: string, logo?: string) => void;
  onReconnect: (account: ActiveAccount) => void;
  connecting: string | null;
}) {
  const totalTools = group.accounts.reduce((sum, a) => sum + (a.tools?.length ?? 0), 0);
  const adding = connecting === connectKey(group.slug);

  return (
    <div className="min-w-0">
      <GroupLabel
        label={group.name}
        count={group.accounts.length > 1 ? group.accounts.length : undefined}
        trailing={
          <span className="flex items-center gap-2">
            <span className="font-mono text-[11px] uppercase tracking-[0.06em] text-quiet">
              {group.source}
              {totalTools > 0 ? ` · ${totalTools} tools` : ""}
            </span>
            {group.kind === "composio" && (
              <Button
                size="sm"
                variant="ghost"
                className="h-8 gap-1"
                onClick={() => onAddAnother(group.slug, group.name, group.logo)}
                disabled={adding}
                title={`Authorise another ${group.name} account`}
              >
                <Plus className="size-3.5" aria-hidden />
                {adding ? "Opening…" : "Add another"}
              </Button>
            )}
          </span>
        }
      />
      {group.accounts.map((a) => (
        <AccountSubRow
          key={a.id}
          account={a}
          groupName={group.name}
          kind={group.kind}
          onDisconnect={onDisconnect}
          onAliasSave={onAliasSave}
          onReconnect={() => onReconnect(a)}
          reconnecting={Boolean(a.accountId && connecting === reconnectKey(a.accountId))}
          highlightTool={highlightTool}
        />
      ))}
    </div>
  );
}

/**
 * One connected account = one row. The alias is edited in place (tap the
 * name), the status is the row's tone dot, and reconnect/disconnect live in
 * the trailing slot. Native MCP servers reuse the same row and open their
 * tool list in an `Inset` — the only container allowed inside a row.
 */
function AccountSubRow({
  account,
  groupName,
  kind,
  onDisconnect,
  onAliasSave,
  onReconnect,
  reconnecting,
  highlightTool,
}: {
  account: ActiveAccount;
  groupName: string;
  kind: "native" | "composio";
  onDisconnect: (id: string, label: string) => void;
  onAliasSave: (accountId: string, alias: string) => void;
  onReconnect: () => void;
  reconnecting: boolean;
  highlightTool?: string;
}) {
  const [editingAlias, setEditingAlias] = useState(false);
  const [aliasDraft, setAliasDraft] = useState(account.alias ?? "");
  const [showTools, setShowTools] = useState(false);
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
  const reconnectable = kind === "composio" && isReconnectableAccount(account);
  const tools = account.tools ?? [];
  const matchedTool = Boolean(
    highlightTool && tools.some((t) => t.toLowerCase().includes(highlightTool.toLowerCase())),
  );
  const toolsOpen = showTools || matchedTool;

  const meta = [
    account.statusText,
    kind === "composio" && account.identityHint && account.alias?.trim()
      ? account.identityHint
      : "",
    tools.length > 0 ? `${tools.length} tools` : "",
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <ListRow
      tone={account.ok ? "success" : account.error ? "danger" : "quiet"}
      title={
        kind === "composio" && editingAlias ? (
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
            aria-label={`Alias for ${displayLabel}`}
            className="h-8 w-full max-w-[240px] rounded-[8px] bg-muted px-2 text-[13.5px] focus:outline-none focus:ring-2 focus:ring-ring/60"
          />
        ) : (
          <button
            type="button"
            onClick={() => {
              if (kind === "composio") setEditingAlias(true);
              else if (tools.length) setShowTools((v) => !v);
            }}
            disabled={kind !== "composio" && tools.length === 0}
            className="flex min-w-0 items-center gap-1.5 py-1 text-left"
            title={kind === "composio" ? "Rename this account" : "Show tools"}
          >
            <span className="min-w-0 truncate">{displayLabel}</span>
            {kind === "composio" && <Pencil className="size-3 shrink-0 text-quiet" aria-hidden />}
          </button>
        )
      }
      meta={meta || undefined}
      chevron={false}
      trailing={
        kind === "composio" && account.accountId ? (
          <>
            {reconnectable && (
              <Button
                type="button"
                size="icon"
                variant="ghost"
                onClick={onReconnect}
                disabled={reconnecting}
                className="size-9 text-info hover:bg-info/10 hover:text-info"
                aria-label={`Reconnect ${displayLabel}`}
                title={`Reconnect ${displayLabel}`}
              >
                <RefreshCcw className={cn("size-4", reconnecting && "animate-spin")} aria-hidden />
              </Button>
            )}
            <Button
              type="button"
              size="icon"
              variant="ghost"
              onClick={() => onDisconnect(account.accountId!, displayLabel)}
              className="size-9 text-quiet hover:bg-danger/10 hover:text-danger"
              aria-label={`Disconnect ${displayLabel}`}
              title={`Disconnect ${displayLabel}`}
            >
              <X className="size-4" aria-hidden />
            </Button>
          </>
        ) : tools.length > 0 ? (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="h-8"
            onClick={() => setShowTools((v) => !v)}
            aria-expanded={toolsOpen}
          >
            {toolsOpen ? "Hide tools" : "Tools"}
          </Button>
        ) : undefined
      }
    >
      {account.error ? (
        <p className="min-w-0 whitespace-pre-wrap text-[12px] text-danger [overflow-wrap:anywhere]">
          {account.error}
        </p>
      ) : null}
      {toolsOpen && tools.length > 0 ? (
        <Inset>
          <span className="flex min-w-0 flex-wrap gap-x-3 gap-y-1 font-mono text-[12px]">
            {tools.map((t) => (
              <span
                key={t}
                className={cn(
                  "min-w-0 [overflow-wrap:anywhere]",
                  highlightTool &&
                    t.toLowerCase().includes(highlightTool.toLowerCase()) &&
                    "text-info",
                )}
              >
                {t}
              </span>
            ))}
          </span>
        </Inset>
      ) : null}
      {/* groupName is kept in the props for callers and screen-reader context. */}
      <span className="sr-only">{groupName}</span>
    </ListRow>
  );
}

function connectKey(slug: string) {
  return `connect:${slug}`;
}

function reconnectKey(accountId: string) {
  return `reconnect:${accountId}`;
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
          hint="If the error mentions undeployed routes, push core. If it mentions 401/invalid key, set COMPOSIO_API_KEY to the Composio Project API Key."
        />
      ) : toolkits.length === 0 && loading ? (
        <p className="py-2 text-[13.5px] text-quiet">Loading catalog…</p>
      ) : toolkits.length === 0 ? (
        <p className="py-2 text-[13.5px] text-quiet">
          {query ? `No integrations match “${query}”.` : "No integrations returned."}
        </p>
      ) : (
        // A catalog is a list, not 250 bordered cards (Majordomo §2): logo,
        // name, what it does, and the one action, on a hairline row.
        <div className="min-w-0">
          {toolkits.map((t) => {
            const isConnected = connectedSlugs.has((t.slug ?? "").toLowerCase());
            const busy = connecting === connectKey(t.slug);
            return (
              <ListRow
                key={t.slug}
                tone={isConnected ? "success" : "quiet"}
                leading={<ToolkitLogo logo={t.meta?.logo} slug={t.slug} className="size-5" />}
                title={t.name ?? t.slug}
                meta={t.meta?.description || "No description."}
                chevron={false}
                trailing={
                  <Button
                    size="sm"
                    variant={isConnected ? "ghost" : "default"}
                    disabled={busy}
                    onClick={() => onConnect(t.slug, t.name ?? t.slug, t.meta?.logo)}
                  >
                    {busy ? "Opening…" : isConnected ? "Add another" : "Connect"}
                  </Button>
                }
              />
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
  // A real failure, said plainly and never as an empty list (CLAUDE.md →
  // "never hide errors"). Tinted, borderless, radius 10 — the Inset ground.
  return (
    <div className="flex min-w-0 items-start gap-2 rounded-[10px] bg-danger/10 px-3 py-2.5">
      <AlertCircle className="mt-0.5 size-4 shrink-0 text-danger" aria-hidden />
      <div className="min-w-0">
        <p className="text-[13.5px] font-medium text-danger">Composio request failed</p>
        <p className="mt-1 break-words text-[12px] text-danger/90">{message}</p>
        {hint && <p className="mt-2 break-words text-[12px] text-quiet">{hint}</p>}
      </div>
    </div>
  );
}

// NameAccountPrompt is the mandatory pre-connect gate. The OAuth flow does
// not start until the boss provides a label - this is what eliminates the
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
    if (trimmed === "") return "Required - name this account so you can route to it later.";
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

  const kind = toolkitKind(toolkit.slug);
  const instructions = toolkitInstructions(toolkit.slug, toolkit.name);

  // Header - logo block + bold title + small kind subtitle. Replaces
  // the previous "blah blah explanation" paragraph: the kind label
  // tells the boss what this thing IS in two words; everything else
  // collapses into the info block below.
  const header = (
    <div className="flex items-start gap-3">
      {toolkit.logo ? (
        /* eslint-disable-next-line @next/next/no-img-element */
        <img src={toolkit.logo} alt="" className="size-10 shrink-0 rounded-lg object-contain" />
      ) : (
        <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted font-mono text-base font-semibold">
          {toolkit.name.charAt(0).toUpperCase()}
        </div>
      )}
      <div className="min-w-0 flex-1">
        <div className="text-base font-semibold leading-tight">Connect {toolkit.name}</div>
        <div className="mt-0.5 text-[12px] text-muted-foreground">{kind}</div>
      </div>
    </div>
  );

  // Body - info card with the one-sentence instruction, then the
  // labeled input, then validation feedback. No more paragraphs of
  // dev-console prose; the placeholder + info card carry the meaning.
  const body = (
    <div className="space-y-4">
      <div className="flex gap-2.5 rounded-lg bg-muted/60 px-3 py-2.5 text-[12.5px] text-foreground/80">
        <Info className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        <p className="leading-snug">{instructions}</p>
      </div>
      <div className="space-y-1.5">
        <label
          htmlFor="connector-alias"
          className="block text-[11px] font-medium uppercase tracking-wide text-muted-foreground"
        >
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
        {toolkit.existingAliases.length > 0 && !error && (
          <p className="text-[11px] text-muted-foreground">
            In use:{" "}
            {toolkit.existingAliases.map((a, i) => (
              <span key={a}>
                <code className="font-mono">{a}</code>
                {i < toolkit.existingAliases.length - 1 ? ", " : ""}
              </span>
            ))}
          </p>
        )}
        {error && <p className="text-[11px] text-danger">{error}</p>}
      </div>
    </div>
  );

  // Routes through ResponsiveModal (the canonical modal primitive) so the
  // Dialog-vs-Drawer split, a11y title/description, overflow chain, pinned
  // footer, and pb-safe are all owned by the primitive - never hand-rolled
  // per consumer (reuse-first rule). The custom `header` keeps the logo +
  // kind subtitle; the body sits inside the disciplined scroll container.
  return (
    <ResponsiveModal
      open
      onOpenChange={(o) => !o && onCancel()}
      title={`Connect ${toolkit.name}`}
      description={`Connect ${toolkit.name}`}
      size="md"
      header={
        <header className="flex shrink-0 items-start gap-3 border-b px-4 pb-3 pt-4 sm:px-5">
          {header}
        </header>
      }
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button size="sm" onClick={tryCommit}>
            Continue to OAuth
          </Button>
        </>
      }
    >
      {body}
    </ResponsiveModal>
  );
}

// toolkitKind maps a Composio toolkit slug to a two-word category
// label rendered as the modal's subtitle ("Email service", "Note
// application"). The boss asked for this - first read in the dialog
// should be "what kind of thing am I connecting," not "what's a
// connector alias."
function toolkitKind(slug: string): string {
  switch (slug.toLowerCase()) {
    case "gmail":
      return "Email service";
    case "googlecalendar":
    case "outlook":
    case "calendly":
      return "Calendar";
    case "googledrive":
    case "dropbox":
    case "box":
    case "onedrive":
      return "File storage";
    case "googledocs":
    case "googlesheets":
      return "Document editor";
    case "notion":
      return "Note application";
    case "slack":
    case "discord":
    case "telegram":
    case "whatsapp":
      return "Chat workspace";
    case "github":
    case "gitlab":
    case "bitbucket":
      return "Code repository";
    case "linear":
    case "jira":
    case "asana":
    case "trello":
    case "clickup":
      return "Issue tracker";
    case "hubspot":
    case "salesforce":
    case "pipedrive":
      return "Customer CRM";
    case "stripe":
    case "paypal":
    case "square":
      return "Payments";
    case "shopify":
    case "woocommerce":
      return "E-commerce";
    case "intercom":
    case "zendesk":
    case "freshdesk":
      return "Customer support";
    case "twilio":
      return "SMS & voice";
    case "sendgrid":
    case "mailchimp":
    case "postmark":
      return "Transactional email";
    case "airtable":
      return "Database";
    case "figma":
      return "Design tool";
    case "loom":
      return "Video recording";
    case "youtube":
    case "vimeo":
      return "Video platform";
    case "twitter":
    case "x":
    case "linkedin":
    case "facebook":
    case "instagram":
    case "tiktok":
      return "Social network";
    default:
      return "External service";
  }
}

// toolkitInstructions returns the one-sentence context that lives in
// the light-grey info card. Most toolkits share the generic "name it
// so the agent can route between accounts" line; a few have toolkit-
// specific hints (workspace name for Slack, org name for GitHub).
function toolkitInstructions(slug: string, name: string): string {
  switch (slug.toLowerCase()) {
    case "slack":
    case "discord":
      return `Use the workspace name so the agent can route messages to the right ${name} space.`;
    case "github":
    case "gitlab":
    case "bitbucket":
      return `Use the org name so the agent picks the right ${name} repos when you ask.`;
    case "stripe":
    case "paypal":
      return `Pick a label like "business" or "personal" - the agent uses it to pick which ${name} account to charge against.`;
    default:
      return `Pick a short label like "personal" or "work" so the agent knows which ${name} account to use when you have more than one.`;
  }
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
