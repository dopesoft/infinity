"use client";

import { useEffect, useMemo, useState } from "react";
import {
  Check,
  ChevronDown,
  ExternalLink,
  Bell,
  Info,
  LayoutDashboard,
  LayoutPanelLeft,
  Loader2,
  Plug,
  PlugZap,
  RefreshCw,
  Search,
  Server,
  Sliders,
  Unplug,
  Wrench,
  X,
} from "lucide-react";
import { TabFrame } from "@/components/TabFrame";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { CanvasSettings } from "@/components/canvas/CanvasSettings";
import { ConnectorsSection } from "@/components/settings/ConnectorsSection";
import { DashboardSettings } from "@/components/settings/DashboardSection";
import { NotificationsSection } from "@/components/settings/NotificationsSection";
import { cn } from "@/lib/utils";
import {
  disconnectOpenAIOAuth,
  exchangeOpenAIOAuth,
  fetchCoreStatus,
  fetchMCP,
  fetchOpenAIOAuthStatus,
  fetchTools,
  startOpenAIOAuth,
  type CoreStatus,
  type MCPStatus,
  type OpenAIOAuthStartResponse,
  type OpenAIOAuthStatusResponse,
  type ToolDescriptor,
} from "@/lib/api";
import { useGlobalModel } from "@/lib/use-model";
import {
  VENDORS,
  findVendor,
  type VendorEntry,
  type VendorId,
} from "@/lib/models-catalog";

type SectionId = "general" | "dashboard" | "notifications" | "tools" | "mcp" | "canvas";

type SectionMeta = {
  id: SectionId;
  label: string;
  description: string;
  icon: typeof Sliders;
};

const SECTIONS: SectionMeta[] = [
  { id: "general", label: "General", description: "LLM provider, model, version", icon: Sliders },
  { id: "dashboard", label: "Dashboard", description: "Pick which Dashboard sections show on /", icon: LayoutDashboard },
  { id: "notifications", label: "Notifications", description: "iOS-style push notifications on iPhone + Mac", icon: Bell },
  { id: "mcp", label: "Connectors", description: "MCP servers + Composio integrations the agent can call", icon: Plug },
  { id: "tools", label: "Tools", description: "Native + MCP tools the agent can call", icon: Wrench },
  { id: "canvas", label: "Canvas", description: "Workspace root, preview URL, auto-open", icon: LayoutPanelLeft },
];

export default function SettingsPage() {
  const [status, setStatus] = useState<CoreStatus | null>(null);
  const [tools, setTools] = useState<ToolDescriptor[]>([]);
  const [mcp, setMCP] = useState<MCPStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [active, setActive] = useState<SectionId>("general");

  async function refresh() {
    setLoading(true);
    const [s, t, m] = await Promise.all([fetchCoreStatus(), fetchTools(), fetchMCP()]);
    setStatus(s);
    setTools(t ?? []);
    setMCP(m ?? []);
    setLoading(false);
  }

  useEffect(() => {
    refresh();
  }, []);

  const counts = useMemo<Partial<Record<SectionId, number>>>(
    () => ({ tools: tools.length, mcp: mcp.length }),
    [tools.length, mcp.length],
  );

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="flex items-center justify-between gap-2 border-b px-3 py-2 sm:px-4">
          <h1 className="text-sm font-semibold tracking-tight">Settings</h1>
          <Button size="sm" variant="ghost" onClick={refresh} disabled={loading} className="gap-1.5">
            <RefreshCw className={cn("size-4", loading && "animate-spin")} />
            <span className="hidden sm:inline">{loading ? "loading…" : "refresh"}</span>
          </Button>
        </div>

        {/* Mobile: section rail across the top, content below. */}
        <div className="flex min-h-0 flex-1 flex-col lg:hidden">
          <nav className="no-scrollbar flex gap-1.5 overflow-x-auto scroll-touch border-b px-3 py-2">
            {SECTIONS.map((s) => (
              <SectionPill
                key={s.id}
                meta={s}
                active={active === s.id}
                count={counts[s.id]}
                onClick={() => setActive(s.id)}
              />
            ))}
          </nav>
          <div className="min-h-0 flex-1 overflow-y-auto scroll-touch p-3 pb-safe">
            <SectionContent active={active} status={status} tools={tools} mcp={mcp} />
          </div>
        </div>

        {/* Desktop: resizable split — sidebar list + content. */}
        <div className="hidden min-h-0 flex-1 lg:flex">
          <ResizablePanelGroup direction="horizontal" autoSaveId="settings:h">
            <ResizablePanel defaultSize={22} minSize={16} maxSize={36}>
              <nav className="flex h-full flex-col gap-0.5 overflow-y-auto p-2">
                {SECTIONS.map((s) => (
                  <SectionRow
                    key={s.id}
                    meta={s}
                    active={active === s.id}
                    count={counts[s.id]}
                    onClick={() => setActive(s.id)}
                  />
                ))}
              </nav>
            </ResizablePanel>
            <ResizableHandle />
            <ResizablePanel defaultSize={78} minSize={50}>
              <div className="h-full overflow-y-auto p-4">
                <div className="mx-auto w-full max-w-3xl space-y-3">
                  <SectionContent active={active} status={status} tools={tools} mcp={mcp} />
                </div>
              </div>
            </ResizablePanel>
          </ResizablePanelGroup>
        </div>
      </div>
    </TabFrame>
  );
}

function SectionPill({
  meta,
  active,
  count,
  onClick,
}: {
  meta: SectionMeta;
  active: boolean;
  count?: number;
  onClick: () => void;
}) {
  const Icon = meta.icon;
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "inline-flex h-9 shrink-0 items-center gap-1.5 rounded-full border px-3 font-mono text-[11px] uppercase tracking-wider transition-colors",
        active
          ? "border-foreground bg-foreground text-background"
          : "border-border bg-muted text-muted-foreground hover:bg-accent",
      )}
    >
      <Icon className="size-3.5" aria-hidden />
      <span>{meta.label}</span>
      {typeof count === "number" && (
        <span
          className={cn(
            "ml-0.5 inline-flex h-4 min-w-[1rem] items-center justify-center rounded-full px-1 text-[10px]",
            active ? "bg-background/20 text-background" : "bg-background text-muted-foreground",
          )}
        >
          {count}
        </span>
      )}
    </button>
  );
}

function SectionRow({
  meta,
  active,
  count,
  onClick,
}: {
  meta: SectionMeta;
  active: boolean;
  count?: number;
  onClick: () => void;
}) {
  const Icon = meta.icon;
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex min-h-11 w-full items-center gap-3 rounded-md px-3 py-2 text-left transition-colors",
        active ? "bg-accent text-foreground" : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
      )}
    >
      <Icon className="size-4 shrink-0" aria-hidden />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium">{meta.label}</span>
          {typeof count === "number" && (
            <Badge variant="secondary" className="h-4 min-w-[1.1rem] justify-center px-1 font-mono text-[10px]">
              {count}
            </Badge>
          )}
        </div>
        <p className="truncate text-[11px] text-muted-foreground">{meta.description}</p>
      </div>
    </button>
  );
}

function SectionContent({
  active,
  status,
  tools,
  mcp,
}: {
  active: SectionId;
  status: CoreStatus | null;
  tools: ToolDescriptor[];
  mcp: MCPStatus[];
}) {
  switch (active) {
    case "general":
      return <GeneralSection status={status} />;
    case "dashboard":
      return <DashboardSettings />;
    case "notifications":
      return <NotificationsSection />;
    case "tools":
      return <ToolsSection tools={tools} />;
    case "mcp":
      return <ConnectorsSection servers={mcp} />;
    case "canvas":
      return <CanvasSettings />;
  }
}

function SectionHeader({ title, description }: { title: string; description: string }) {
  return (
    <div className="space-y-1">
      <h2 className="text-base font-semibold tracking-tight">{title}</h2>
      <p className="text-xs text-muted-foreground">{description}</p>
    </div>
  );
}

function GeneralSection({ status }: { status: CoreStatus | null }) {
  // Vendor picker hot-swaps Core's active provider via /api/settings/provider.
  // The change is synchronous — the next turn (and the Live composer's chip)
  // sees the new vendor immediately. Stored OAuth credentials persist across
  // vendor flips, so switching back to ChatGPT later doesn't require re-auth.
  // Model edits flow through /api/settings/model as before.
  const { setting, setModel, setProvider } = useGlobalModel();
  const liveProvider = ((setting?.provider ?? status?.provider ?? "") as string).toLowerCase();
  const effectiveModel = setting?.model ?? status?.model ?? "";
  const defaultModel = setting?.defaultModel ?? "";
  const availableProviders = setting?.availableProviders ?? [];

  // Vendor + model are both *drafts* until Save fires — selecting from
  // either dropdown mutates local state only. Save is the deterministic
  // commit; matches the BossProfilePanel pattern in this codebase.
  const [draftVendor, setDraftVendor] = useState<string>(liveProvider || VENDORS[0].id);
  const [draftModel, setDraftModel] = useState<string>(effectiveModel);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Sync drafts with whatever Core broadcasts (composer chip cycle,
  // first load, etc.). The model effect runs on every effectiveModel
  // change — but only resets the draft when the user hasn't started
  // editing locally yet (draft still equals the last broadcast).
  useEffect(() => {
    if (liveProvider) setDraftVendor(liveProvider);
  }, [liveProvider]);
  useEffect(() => {
    setDraftModel(effectiveModel);
  }, [effectiveModel]);

  const selectedVendor = findVendor(draftVendor);
  const isOAuthVendor = selectedVendor.auth === "oauth";

  // Auto-reset the model dropdown when the current draft isn't in the
  // active vendor's catalog at all (e.g. Anthropic's claude-haiku surviving
  // a flip to openai_oauth). We check the *active vendor's catalog* rather
  // than asking resolveModelEntry which vendor "owns" the id — that's
  // wrong when an id is shared across multiple catalogs (gpt-5.4 lives in
  // both `openai` and `openai_oauth`), because the lookup grabs the first
  // match and would snap subscription picks back to the API vendor's
  // default.
  useEffect(() => {
    if (!draftModel) return;
    const inActiveVendor = selectedVendor.models.some((m) => m.id === draftModel);
    if (!inActiveVendor) {
      const fallback =
        selectedVendor.models.find((m) => m.recommended) ?? selectedVendor.models[0];
      if (fallback) setDraftModel(fallback.id);
    }
  }, [draftVendor, draftModel, selectedVendor]);

  const knownModelIds = new Set(selectedVendor.models.map((m) => m.id));
  const dropdownOptions = knownModelIds.has(draftModel)
    ? selectedVendor.models
    : draftModel
      ? [{ id: draftModel, label: `${draftModel} (custom)` }, ...selectedVendor.models]
      : selectedVendor.models;

  const dirty =
    draftVendor !== liveProvider || draftModel !== effectiveModel;

  async function save() {
    setBusy(true);
    setErr(null);
    try {
      if (draftVendor !== liveProvider) {
        const res = await setProvider(draftVendor);
        if (!res.ok) {
          setErr(res.error ?? "provider swap failed");
          return;
        }
      }
      if (draftModel !== effectiveModel) {
        const ok = await setModel(draftModel);
        if (!ok) {
          setErr("model save failed");
        }
      }
    } finally {
      setBusy(false);
    }
  }

  async function clearOverride() {
    setBusy(true);
    try {
      await setModel("");
      if (defaultModel) setDraftModel(defaultModel);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      <SectionHeader
        title="General"
        description="Pick a vendor to inspect its pricing and connect credentials. The active provider is wired via LLM_PROVIDER on Core; switch the env to flip which one runs the chat."
      />

      <div className="space-y-3 rounded-md border bg-background p-3">
        <FieldLabel label="Vendor">
          <NativeSelect value={draftVendor} onChange={setDraftVendor}>
            {VENDORS.map((v) => {
              const available =
                availableProviders.length === 0 ||
                availableProviders.includes(v.id);
              return (
                <option key={v.id} value={v.id} disabled={!available}>
                  {v.label}
                  {v.id === (liveProvider as VendorId) ? " · active" : ""}
                  {!available ? " · not configured" : ""}
                </option>
              );
            })}
          </NativeSelect>
        </FieldLabel>

        <FieldLabel label="Model">
          <NativeSelect value={draftModel} onChange={setDraftModel}>
            {dropdownOptions.map((m) => (
              <option key={m.id} value={m.id}>
                {m.id === defaultModel ? `${m.label} · default` : m.label}
              </option>
            ))}
          </NativeSelect>
        </FieldLabel>

        {isOAuthVendor && <OAuthConnectBlock />}

        {err && (
          <p className="rounded-sm bg-danger/10 p-2 text-[11px] text-danger">{err}</p>
        )}

        <div className="flex items-center justify-end gap-2">
          {setting?.source === "user" && defaultModel && draftModel !== defaultModel && (
            <Button variant="ghost" onClick={clearOverride} disabled={busy}>
              Reset to default
            </Button>
          )}
          <Button onClick={save} disabled={!dirty || busy}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </div>
      </div>

      <PricingTable vendor={selectedVendor} />
    </div>
  );
}

// ── Pricing table ─────────────────────────────────────────────────────────
// Static snapshot of the most popular models for the selected vendor.
// Prices are USD per 1M tokens; subscription-billed vendors (openai_oauth)
// surface their plan note instead. Anyone updating prices in
// `models-catalog.ts` automatically updates this table.
function PricingTable({ vendor }: { vendor: VendorEntry }) {
  // Sortable column state. Default is catalog order — the boss has it
  // arranged with the recommended model on top, so first-render shouldn't
  // jump them around. Click a column header to toggle asc/desc.
  type SortKey = "default" | "input" | "output";
  const [sortKey, setSortKey] = useState<SortKey>("default");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");

  function toggle(next: SortKey) {
    if (next === sortKey) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(next);
      setSortDir("asc");
    }
  }

  const sorted = useMemo(() => {
    if (sortKey === "default") return vendor.models;
    const arr = [...vendor.models];
    arr.sort((a, b) => {
      const av = (sortKey === "input" ? a.input_per_mtok : a.output_per_mtok) ?? Infinity;
      const bv = (sortKey === "input" ? b.input_per_mtok : b.output_per_mtok) ?? Infinity;
      return sortDir === "asc" ? av - bv : bv - av;
    });
    return arr;
  }, [vendor.models, sortKey, sortDir]);

  return (
    <div className="space-y-2 rounded-md border bg-background p-3">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold tracking-tight">
          {vendor.label} pricing
        </h3>
        <Badge variant="secondary" className="font-mono text-[10px]">
          per 1M tokens
        </Badge>
      </div>
      <div className="overflow-x-auto scroll-touch">
        <table className="w-full text-left text-[12px]">
          <thead>
            <tr className="border-b text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
              <th className="px-2 py-1.5 font-normal">Model</th>
              <SortHeader label="Input" active={sortKey === "input"} dir={sortDir} onClick={() => toggle("input")} />
              <SortHeader label="Output" active={sortKey === "output"} dir={sortDir} onClick={() => toggle("output")} />
            </tr>
          </thead>
          <tbody>
            {sorted.map((m) => (
              <tr key={m.id} className="border-b last:border-b-0">
                <td className="px-2 py-2">
                  <div className="flex flex-col">
                    <span className="font-medium">{m.label}</span>
                    {m.tagline && (
                      <span className="text-[10px] text-muted-foreground">
                        {m.tagline}
                        {m.note ? ` · ${m.note}` : ""}
                      </span>
                    )}
                  </div>
                </td>
                <td className="px-2 py-2 text-right font-mono">
                  {m.input_per_mtok != null ? `$${m.input_per_mtok.toFixed(2)}` : "—"}
                </td>
                <td className="px-2 py-2 text-right font-mono">
                  {m.output_per_mtok != null ? `$${m.output_per_mtok.toFixed(2)}` : "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SortHeader({
  label,
  active,
  dir,
  onClick,
}: {
  label: string;
  active: boolean;
  dir: "asc" | "desc";
  onClick: () => void;
}) {
  return (
    <th className="px-2 py-1.5 text-right font-normal">
      <button
        type="button"
        onClick={onClick}
        className={cn(
          "inline-flex items-center gap-1 transition-colors hover:text-foreground",
          active && "text-foreground",
        )}
      >
        {label}
        <span className="text-[8px]" aria-hidden>
          {active ? (dir === "asc" ? "▲" : "▼") : "↕"}
        </span>
      </button>
    </th>
  );
}

// ── OAuth Connect block (openai_oauth only) ────────────────────────────────
// Three states:
//   • disconnected — "Connect ChatGPT" button kicks off /api/auth/openai/start,
//     opens the authorize URL in a new tab, reveals the paste box.
//   • paste-pending — user has clicked through, needs to paste the callback
//     URL (or code+state). Pressing "Connect" calls /exchange.
//   • connected — shows account email, last refresh, expiry, with a
//     Disconnect button. Reconnect is a one-click flow that re-enters the
//     paste-pending state without dropping the existing token until success.
function OAuthConnectBlock() {
  const [status, setStatus] = useState<OpenAIOAuthStatusResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState<OpenAIOAuthStartResponse | null>(null);
  const [paste, setPaste] = useState("");
  const [busy, setBusy] = useState<"start" | "exchange" | "disconnect" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [successAt, setSuccessAt] = useState<number | null>(null);

  async function refresh() {
    setLoading(true);
    try {
      const s = await fetchOpenAIOAuthStatus();
      setStatus(s);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  async function connect() {
    setBusy("start");
    setError(null);
    try {
      const next = await startOpenAIOAuth();
      if (!next) {
        setError("Could not start the connect flow — check Core logs.");
        return;
      }
      setPending(next);
      // Open in a new tab so the user can leave Studio open and paste
      // back without losing the dialog state.
      if (typeof window !== "undefined") {
        window.open(next.authorize_url, "_blank", "noopener,noreferrer");
      }
    } finally {
      setBusy(null);
    }
  }

  async function exchange() {
    if (!pending) return;
    setBusy("exchange");
    setError(null);
    try {
      const trimmed = paste.trim();
      const looksLikeURL = /^https?:\/\//i.test(trimmed) || trimmed.startsWith("/");
      const body = looksLikeURL
        ? { callback_url: trimmed, state: pending.state }
        : { code: trimmed, state: pending.state };
      const res = await exchangeOpenAIOAuth(body);
      if ("error" in res) {
        setError(res.error);
        return;
      }
      setStatus(res);
      setPending(null);
      setPaste("");
      setSuccessAt(Date.now());
    } finally {
      setBusy(null);
    }
  }

  async function disconnect() {
    setBusy("disconnect");
    setError(null);
    try {
      const ok = await disconnectOpenAIOAuth();
      if (ok) {
        setStatus({ connected: false });
        setPending(null);
        setPaste("");
      } else {
        setError("Disconnect failed — check Core logs.");
      }
    } finally {
      setBusy(null);
    }
  }

  const connected = !!status?.connected;
  const expiresAt = status?.expires_at ? new Date(status.expires_at) : null;
  const refreshedAt = status?.last_refreshed ? new Date(status.last_refreshed) : null;

  return (
    <div className="space-y-2 rounded-md border border-dashed bg-muted/30 p-3">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <PlugZap className="size-3.5 text-muted-foreground" aria-hidden />
          <span className="text-xs font-semibold tracking-tight">
            ChatGPT (Plan) connect
          </span>
        </div>
        {loading ? (
          <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
        ) : connected ? (
          <Badge variant="secondary" className="gap-1 text-[10px]">
            <Check className="size-3 text-success" />
            connected
          </Badge>
        ) : (
          <Badge variant="secondary" className="gap-1 text-[10px]">
            <Unplug className="size-3 text-muted-foreground" />
            not connected
          </Badge>
        )}
      </div>

      {connected && (
        <dl className="space-y-1 text-[11px] text-muted-foreground">
          {status?.account_email && (
            <div className="flex items-center justify-between gap-2">
              <dt className="font-mono uppercase tracking-wider">account</dt>
              <dd className="truncate font-mono">{status.account_email}</dd>
            </div>
          )}
          {refreshedAt && (
            <div className="flex items-center justify-between gap-2">
              <dt className="font-mono uppercase tracking-wider">refreshed</dt>
              <dd className="font-mono" suppressHydrationWarning>
                {refreshedAt.toLocaleString()}
              </dd>
            </div>
          )}
          {expiresAt && (
            <div className="flex items-center justify-between gap-2">
              <dt className="font-mono uppercase tracking-wider">expires</dt>
              <dd className="font-mono" suppressHydrationWarning>
                {expiresAt.toLocaleString()}
              </dd>
            </div>
          )}
        </dl>
      )}

      {!connected && !pending && (
        <div className="space-y-2 rounded-md border bg-background p-2.5">
          <div className="flex items-start gap-2 rounded-sm bg-info/10 p-2 text-[11px] text-info">
            <Info className="mt-0.5 size-3.5 shrink-0" aria-hidden />
            <p className="leading-relaxed">
              <span className="font-semibold">Heads up:</span> after you log in,
              your browser will show a &quot;can&apos;t reach{" "}
              <code className="font-mono">localhost:1455</code>&quot; page.
              That&apos;s expected — OpenAI&apos;s OAuth client only redirects
              to localhost, and Studio lives in the cloud. Just copy the URL
              from the address bar back here.
            </p>
          </div>
        </div>
      )}

      {pending && (
        <div className="space-y-2 rounded-md border bg-background p-2.5">
          <p className="text-[11px] text-muted-foreground">
            Logged in? Copy the full address-bar URL from the &quot;can&apos;t
            reach&quot; page (or just the{" "}
            <code className="font-mono">code=…</code> value) and paste it below.
          </p>
          <a
            href={pending.authorize_url}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-[11px] text-info hover:underline"
          >
            <ExternalLink className="size-3" />
            re-open authorize URL
          </a>
          <Input
            value={paste}
            onChange={(e) => setPaste(e.target.value)}
            placeholder="paste callback URL or code…"
            inputMode="text"
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            className="h-9 font-mono text-[11px]"
          />
          <div className="flex flex-wrap items-center justify-end gap-1.5">
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setPending(null);
                setPaste("");
                setError(null);
              }}
            >
              cancel
            </Button>
            <Button
              size="sm"
              onClick={exchange}
              disabled={!paste.trim() || busy === "exchange"}
            >
              {busy === "exchange" ? (
                <Loader2 className="animate-spin" />
              ) : (
                <Check />
              )}
              connect
            </Button>
          </div>
        </div>
      )}

      {error && (
        <p className="rounded-sm bg-danger/10 p-2 text-[11px] text-danger">{error}</p>
      )}
      {successAt && Date.now() - successAt < 4000 && !error && (
        <p className="rounded-sm bg-success/10 p-2 text-[11px] text-success">
          Connected — Core will use this token on the next openai_oauth turn.
        </p>
      )}

      <div className="flex flex-wrap items-center justify-end gap-2 pt-1">
        <div className="flex items-center gap-1.5">
          {connected && (
            <Button
              size="sm"
              variant="ghost"
              onClick={disconnect}
              disabled={busy === "disconnect"}
            >
              {busy === "disconnect" ? <Loader2 className="animate-spin" /> : <Unplug />}
              disconnect
            </Button>
          )}
          {!pending && (
            <Button
              size="sm"
              variant={connected ? "ghost" : "default"}
              onClick={connect}
              disabled={busy === "start"}
            >
              {busy === "start" ? <Loader2 className="animate-spin" /> : <Plug />}
              {connected ? "reconnect" : "open ChatGPT login"}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

function FieldLabel({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-1.5">
      <span className="block font-mono text-[11px] uppercase tracking-wider text-muted-foreground">
        {label}
      </span>
      {children}
    </label>
  );
}

function NativeSelect({
  value,
  onChange,
  children,
  disabled,
}: {
  value: string;
  onChange: (next: string) => void;
  children: React.ReactNode;
  disabled?: boolean;
}) {
  return (
    <div className="relative">
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className={cn(
          "h-11 w-full appearance-none rounded-md border border-input bg-background pl-3 pr-9 text-sm",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 ring-offset-background",
          "[&>option]:bg-popover [&>option]:text-popover-foreground",
          "disabled:cursor-not-allowed disabled:opacity-60",
        )}
      >
        {children}
      </select>
      <ChevronDown
        className="pointer-events-none absolute right-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
        aria-hidden
      />
    </div>
  );
}

function splitToolName(name: string): { group: string; leaf: string } {
  const dunder = name.indexOf("__");
  if (dunder > 0) return { group: name.slice(0, dunder), leaf: name.slice(dunder + 2) };
  const dot = name.indexOf(".");
  if (dot > 0) return { group: name.slice(0, dot), leaf: name.slice(dot + 1) };
  return { group: "native", leaf: name };
}

function ToolsSection({ tools }: { tools: ToolDescriptor[] }) {
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();

  const groups = useMemo(() => {
    const filtered = q
      ? tools.filter(
          (t) =>
            t.name.toLowerCase().includes(q) ||
            (t.description ?? "").toLowerCase().includes(q),
        )
      : tools;
    const map = new Map<string, ToolDescriptor[]>();
    for (const t of filtered) {
      const { group } = splitToolName(t.name);
      const arr = map.get(group) ?? [];
      arr.push(t);
      map.set(group, arr);
    }
    return Array.from(map.entries())
      .map(([name, items]) => ({
        name,
        items: items.sort((a, b) => a.name.localeCompare(b.name)),
      }))
      .sort((a, b) => {
        if (a.name === "native") return -1;
        if (b.name === "native") return 1;
        return a.name.localeCompare(b.name);
      });
  }, [tools, q]);

  const filteredCount = groups.reduce((sum, g) => sum + g.items.length, 0);

  return (
    <div className="space-y-3">
      <SectionHeader
        title={`Tools (${tools.length})`}
        description="Native + MCP tools available to the agent right now. Grouped by source — tap a group to expand, then tap a tool to inspect its schema."
      />
      <SearchBar
        value={query}
        onChange={setQuery}
        placeholder="Search tools by name or description…"
      />
      {q && (
        <p className="text-[11px] text-muted-foreground">
          {filteredCount} match{filteredCount === 1 ? "" : "es"} across {groups.length} group{groups.length === 1 ? "" : "s"}
        </p>
      )}
      {tools.length === 0 ? (
        <p className="text-sm text-muted-foreground">No tools registered.</p>
      ) : groups.length === 0 ? (
        <p className="text-sm text-muted-foreground">No tools match “{query}”.</p>
      ) : (
        <ul className="space-y-2">
          {groups.map((g) => (
            <ToolGroup key={g.name} name={g.name} items={g.items} forceOpen={Boolean(q)} />
          ))}
        </ul>
      )}
    </div>
  );
}

function ToolGroup({
  name,
  items,
  forceOpen,
}: {
  name: string;
  items: ToolDescriptor[];
  forceOpen: boolean;
}) {
  // Default collapsed for big groups, open for small ones.
  const [open, setOpen] = useState(items.length <= 6);
  const isOpen = forceOpen || open;
  const isNative = name === "native";
  return (
    <li className="overflow-hidden rounded-md border bg-muted/20">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-accent/40"
      >
        {isNative ? (
          <Wrench className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        ) : (
          <Server className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        )}
        <span className="truncate font-mono text-[11px] font-semibold uppercase tracking-wider">
          {name}
        </span>
        <Badge variant="secondary" className="h-4 min-w-[1.1rem] shrink-0 justify-center px-1 font-mono text-[10px]">
          {items.length}
        </Badge>
        <ChevronDown
          className={cn(
            "ml-auto size-3.5 shrink-0 text-muted-foreground transition-transform",
            isOpen && "rotate-180",
          )}
          aria-hidden
        />
      </button>
      {isOpen && (
        <ul className="space-y-1 border-t bg-background p-1.5">
          {items.map((t) => (
            <ToolCard key={t.name} tool={t} groupName={name} />
          ))}
        </ul>
      )}
    </li>
  );
}

function ToolCard({ tool, groupName }: { tool: ToolDescriptor; groupName?: string }) {
  const [open, setOpen] = useState(false);
  const hasSchema = tool.schema && Object.keys(tool.schema).length > 0;
  const { leaf } = splitToolName(tool.name);
  // Inside a group we show just the leaf to avoid duplicating the prefix.
  const display = groupName && groupName !== "native" ? leaf : tool.name;
  return (
    <li className="overflow-hidden rounded-md border bg-background">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left transition-colors hover:bg-accent/40"
      >
        <Wrench className="size-3 shrink-0 text-muted-foreground" aria-hidden />
        <code className="truncate font-mono text-xs">{display}</code>
        <ChevronDown
          className={cn(
            "ml-auto size-3 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-180",
          )}
          aria-hidden
        />
      </button>
      {open && (
        <div className="space-y-2 border-t bg-muted/30 px-3 py-2.5">
          <p className="text-xs leading-relaxed text-muted-foreground">{tool.description || "No description."}</p>
          {hasSchema && (
            <details className="text-[11px]">
              <summary className="cursor-pointer font-mono uppercase tracking-wider text-muted-foreground">
                input schema
              </summary>
              <pre className="mt-1.5 overflow-x-auto rounded-sm bg-background p-2 font-mono text-[10px] leading-relaxed text-foreground/90">
                {JSON.stringify(tool.schema, null, 2)}
              </pre>
            </details>
          )}
        </div>
      )}
    </li>
  );
}

function SearchBar({
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
      <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden />
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        inputMode="search"
        type="text"
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        className="h-9 pl-8 pr-8 text-sm"
      />
      {value && (
        <button
          type="button"
          onClick={() => onChange("")}
          aria-label="Clear search"
          className="absolute right-1 top-1/2 inline-flex size-7 -translate-y-1/2 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <X className="size-3.5" />
        </button>
      )}
    </div>
  );
}

