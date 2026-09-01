"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useTabParam } from "@/lib/useTabParam";
import { openAuthWindow } from "@/lib/auth-window";
import {
  Check,
  ExternalLink,
  Bell,
  LayoutDashboard,
  Shield,
  LayoutPanelLeft,
  Loader2,
  MessageSquare,
  Plug,
  Server,
  ShieldCheck,
  Sliders,
  Unplug,
  Wrench,
} from "lucide-react";
import { AppShell } from "@/components/AppShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SearchInput } from "@/components/ui/search-input";
import { PageHeader } from "@/components/ui/page-header";
import { GroupLabel, ListRow } from "@/components/ui/list-row";
import { PickListItem } from "@/components/ui/pick-list";
import { Inset, type InsetField } from "@/components/ui/inset";
import { NativeSelect } from "@/components/ui/native-select";
import { SettingRow } from "@/components/ui/setting-row";
import { Switch } from "@/components/ui/switch";
import { Section } from "@/components/dashboard/Section";
import { PageTabs, PageTabsList, PageTabsTrigger } from "@/components/ui/page-tabs";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { CanvasSettings } from "@/components/canvas/CanvasSettings";
import { CompassSection } from "@/components/settings/CompassSection";
import { SettingsPanel } from "@/components/settings/SettingsPanel";
import { VaultSection } from "@/components/settings/VaultSection";
import { ConnectorsSection } from "@/components/settings/ConnectorsSection";
import { DashboardSettings } from "@/components/settings/DashboardSection";
import { NotificationsSection } from "@/components/settings/NotificationsSection";
import { TrustReviewPanel } from "@/components/TrustReviewPanel";
import { cn } from "@/lib/utils";
import {
  deleteProviderKey,
  disconnectOpenAIOAuth,
  exchangeOpenAIOAuth,
  fetchCoreStatus,
  fetchChatSettings,
  fetchMCP,
  fetchClaudeMaxStatus,
  removeClaudeMaxToken,
  saveClaudeMaxToken,
  fetchOpenAIOAuthStatus,
  fetchProviderKeys,
  fetchTools,
  saveProviderKey,
  startOpenAIOAuth,
  saveChatSettings,
  type ChatSettings,
  type CoreStatus,
  type MCPStatus,
  type OpenAIOAuthStartResponse,
  type ClaudeMaxStatus,
  type OpenAIOAuthStatusResponse,
  type ProviderKeyRow,
  type ToolDescriptor,
} from "@/lib/api";
import { standbyLabel, standbyResetClock, useGlobalModel } from "@/lib/use-model";
import { CountBadge } from "@/components/ui/count-badge";
import { useTrustPendingBadge } from "@/lib/nav-badges";
import {
  ConnectorAccountsProvider,
  useConnectorAccounts,
} from "@/lib/connectors/provider";
import {
  buildActiveConnectorGroups,
  countActiveConnectorAccounts,
} from "@/lib/connectors/active";
import {
  VENDORS,
  findVendor,
  type VendorEntry,
} from "@/lib/models-catalog";

type SectionId =
  | "general"
  | "chat"
  | "compass"
  | "dashboard"
  | "notifications"
  | "privacy"
  | "tools"
  | "mcp"
  | "canvas"
  | "trust";

type SectionMeta = {
  id: SectionId;
  label: string;
  /**
   * Kept as the rail row's `title` attribute only. Majordomo §1.5: a rail
   * label that carries a sentence restating itself is furniture, so the
   * description no longer renders — but the copy stays here so nothing is
   * lost and a hover still explains the section.
   */
  description: string;
  icon: typeof Sliders;
};

/**
 * Named for what each one does for the boss, not for the machinery behind
 * it. "Connectors / MCP" meant nothing to a person: to you they are Gmail,
 * Slack and GitHub, so they are Accounts. "Tools" was a list of 112 raw ids;
 * they are the things he can do, so they are Abilities. "Trust" is the
 * mechanism; approving is the thing you actually do.
 *
 * Compass is NOT in this list any more. Your mission is a fact about you, so
 * it lives with the rest of what he knows, at the top of Memory. The editor
 * itself is unchanged — the same <CompassSection/> renders there.
 */
const SECTIONS: SectionMeta[] = [
  { id: "general", label: "Brain", description: "Which model answers, and what it falls back to", icon: Sliders },
  { id: "chat", label: "Chat", description: "How he behaves in a conversation, and what he may spend", icon: MessageSquare },
  { id: "trust", label: "Approvals", description: "What he is asking to do, and what you have already allowed", icon: ShieldCheck },
  { id: "privacy", label: "Vault", description: "Your cards, your personal details, and the files he must never open", icon: Shield },
  { id: "mcp", label: "Accounts", description: "Gmail, Slack, GitHub and the rest, and what he may do with each", icon: Plug },
  { id: "tools", label: "Abilities", description: "Everything he can do, and what each one touches", icon: Wrench },
  { id: "canvas", label: "Workbench", description: "Where he works, what the preview points at", icon: LayoutPanelLeft },
  { id: "notifications", label: "Alerts", description: "When he is allowed to interrupt you, and how", icon: Bell },
  { id: "dashboard", label: "Home layout", description: "Which cards show on your home screen", icon: LayoutDashboard },
];

const SECTION_IDS = SECTIONS.map((s) => s.id) as SectionId[];

export default function SettingsPage() {
  // The connector provider wraps the WHOLE page, not just the Accounts
  // section: the rail has to be able to count accounts on a screen the boss
  // has not opened yet. See lib/connectors/provider.tsx.
  return (
    <ConnectorAccountsProvider>
      <SettingsScreen />
    </ConnectorAccountsProvider>
  );
}

function SettingsScreen() {
  const [status, setStatus] = useState<CoreStatus | null>(null);
  const [tools, setTools] = useState<ToolDescriptor[]>([]);
  const [mcp, setMCP] = useState<MCPStatus[]>([]);
  // setLoading is retained so refresh() can still toggle it for any
  // sub-section that may want it later; the value itself isn't read at
  // this level now that the header refresh button is gone.
  const [, setLoading] = useState(true);
  // Active section lives in ?section=<id> so a refresh, back/forward, and
  // deep-links (the TrustToast's router.push("/settings?section=trust"),
  // dashboard, notifications) all land on the right section instead of
  // snapping back to General. Clicking a section writes the param too, so
  // the state actually survives a reload.
  const [active, setActive] = useTabParam<SectionId>(
    "section",
    "general",
    SECTION_IDS,
  );

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

  // A rail number has to be the number of things the section actually
  // shows, or it is worse than no number. Accounts was counting `mcp.length`
  // - the MCP processes that answered /api/mcp - while the screen itself
  // lists those servers PLUS every connected mailbox and workspace, so it
  // read 2 against a list of nine rows. It now counts the same groups the
  // Accounts screen renders, built by the one shared function.
  //
  // Sections with nothing countable (Brain, Chat, Vault, Workbench, Alerts,
  // Home layout) deliberately carry no number: those are settings you set,
  // not lists that grow.
  const { accounts, aliases, error: accountsError } = useConnectorAccounts();
  const trustPending = useTrustPendingBadge();

  const accountCount = useMemo(() => {
    // An unreachable connector backend is not "zero accounts". Show no
    // number rather than a confident 0 the screen will contradict.
    if (accountsError) return undefined;
    return countActiveConnectorAccounts(
      buildActiveConnectorGroups(mcp, accounts, aliases),
    );
  }, [mcp, accounts, aliases, accountsError]);

  const counts = useMemo<Partial<Record<SectionId, number>>>(
    () => ({ tools: tools.length, mcp: accountCount, trust: trustPending }),
    [tools.length, accountCount, trustPending],
  );

  // The one meta line under the title: counts, never a description (§1.5).
  const meta = useMemo(() => {
    const bits: string[] = [];
    if (tools.length) bits.push(`${tools.length} tools`);
    if (accountCount) bits.push(`${accountCount} accounts`);
    if (status?.version) bits.push(`core ${status.version}`);
    return bits.join(" · ");
  }, [tools.length, accountCount, status?.version]);

  return (
    <AppShell>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="px-4 pt-4 sm:px-6 lg:px-8">
          <PageHeader title="Settings" meta={meta || undefined} />
        </div>

        {/* Mobile: the section rail is a chip row — the house tab-strip
            primitive, not a bespoke pill (CLAUDE.md → "Page tab strips"). */}
        <div className="flex min-h-0 flex-1 flex-col lg:hidden">
          <div className="px-4 sm:px-6">
            <PageTabs value={active} onValueChange={(v) => setActive(v as SectionId)}>
              <PageTabsList scrollable>
                {SECTIONS.map((s) => (
                  <PageTabsTrigger key={s.id} value={s.id} className="gap-1.5">
                    <span>{s.label}</span>
                    <CountBadge count={counts[s.id] ?? 0} active={active === s.id} />
                  </PageTabsTrigger>
                ))}
              </PageTabsList>
            </PageTabs>
          </div>
          {/* px-4 sm:px-6 exactly matches Section tone="band"'s negative
              margins, so a band bleeds to the screen edge without ever
              widening the page. */}
          <div className="min-h-0 flex-1 overflow-y-auto scroll-touch px-4 pb-safe sm:px-6">
            <SectionContent active={active} status={status} tools={tools} mcp={mcp} />
          </div>
        </div>

        {/* Desktop: resizable split - names-only rail + content. */}
        <div className="hidden min-h-0 flex-1 lg:flex">
          <ResizablePanelGroup direction="horizontal" autoSaveId="settings:h">
            <ResizablePanel defaultSize={22} minSize={16} maxSize={36}>
              {/* The rail is a PickListItem list, not ListRow + a local
                  `bg-accent` override. ListRow draws a hairline under every
                  row — correct for a list of records, wrong for a rail of
                  names, where it turns nine words into a nine-row table. And
                  the override was a one-off copy of a selected state that
                  the primitive already owns. */}
              <nav className="flex h-full flex-col gap-0.5 overflow-y-auto px-3 py-2 scroll-touch">
                {SECTIONS.map((s) => {
                  const Icon = s.icon;
                  return (
                    <PickListItem
                      key={s.id}
                      leading={<Icon className="size-4" aria-hidden />}
                      label={s.label}
                      meta={
                        typeof counts[s.id] === "number" && counts[s.id]
                          ? counts[s.id]
                          : undefined
                      }
                      selected={active === s.id}
                      onSelect={() => setActive(s.id)}
                    />
                  );
                })}
              </nav>
            </ResizablePanel>
            <ResizableHandle />
            <ResizablePanel defaultSize={78} minSize={50}>
              <div className="h-full overflow-y-auto px-6 py-2 scroll-touch">
                <div className="mx-auto w-full min-w-0 max-w-3xl">
                  <SectionContent active={active} status={status} tools={tools} mcp={mcp} />
                </div>
              </div>
            </ResizablePanel>
          </ResizablePanelGroup>
        </div>
      </div>
    </AppShell>
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
    case "chat":
      return <ChatSettingsSection />;
    case "compass":
      // Compass lives in Memory now ("About you"). The id and this case stay
      // so an old /settings?section=compass link is not a dead end — it
      // renders the same editor rather than a blank pane.
      return <CompassSection />;
    case "privacy":
      // Three tabs (Cards, Personal info, Off limits) rather than the three
      // stacked blocks this used to be. The id stays "privacy" so existing
      // /settings?section=privacy links still land here.
      return <VaultSection />;
    case "trust":
      return <TrustSection />;
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

function TrustSection() {
  return (
    <SettingsPanel>
      <TrustReviewPanel />
    </SettingsPanel>
  );
}

const DEFAULT_CHAT_SETTINGS: ChatSettings = {
  agent_teams: "auto",
  team_aggressiveness: "full_tilt",
  show_team_activity: "detailed",
  default_team_card_state: "expanded",
  max_agents_per_team: 6,
  max_parallel_teams: 2,
  max_runtime_seconds: 600,
  max_team_tokens: 120000,
  max_tool_calls: 120,
  allow_artifact_agents: true,
  allow_code_agents: true,
  allow_connector_agents: true,
  require_action_approval: true,
  model_policy: "same_as_chat",
  show_token_usage: true,
  show_worker_summaries: true,
  show_artifacts: true,
};

function ChatSettingsSection() {
  const [draft, setDraft] = useState<ChatSettings>(DEFAULT_CHAT_SETTINGS);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [savedAt, setSavedAt] = useState<number | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    fetchChatSettings(ac.signal).then((s) => {
      if (s) setDraft(s);
      setLoading(false);
    });
    return () => ac.abort();
  }, []);

  function patch(next: Partial<ChatSettings>) {
    setDraft((cur) => ({ ...cur, ...next }));
    setSavedAt(null);
  }

  async function save() {
    setSaving(true);
    setErr(null);
    const res = await saveChatSettings(draft);
    setSaving(false);
    if (!res) {
      setErr("Chat settings save failed.");
      return;
    }
    setDraft(res);
    setSavedAt(Date.now());
  }

  return (
    // Same frame as every other section. "Teamwork" and "Chat visibility"
    // below are GROUP headings, not the section's name, so they stay.
    <SettingsPanel>
      <Section title="Teamwork" noPad>
        <SettingRow
          label="Use specialist agents"
          description="Whether Jarvis may split work across specialist agents."
        >
          <NativeSelect
            value={draft.agent_teams}
            onValueChange={(v) => patch({ agent_teams: v as ChatSettings["agent_teams"] })}
            aria-label="Agent teams"
          >
            <option value="off">Off</option>
            <option value="ask">Ask first</option>
            <option value="auto">Auto</option>
          </NativeSelect>
        </SettingRow>
        <SettingRow label="Aggressiveness" description="How readily he reaches for a team instead of doing it himself.">
          <NativeSelect
            value={draft.team_aggressiveness}
            onValueChange={(v) =>
              patch({ team_aggressiveness: v as ChatSettings["team_aggressiveness"] })
            }
            aria-label="Aggressiveness"
          >
            <option value="conservative">Conservative</option>
            <option value="balanced">Balanced</option>
            <option value="full_tilt">Full tilt</option>
          </NativeSelect>
        </SettingRow>
        <NumberSetting
          label="Max agents"
          description="Ceiling on workers inside one team."
          value={draft.max_agents_per_team}
          min={1}
          max={12}
          onChange={(v) => patch({ max_agents_per_team: v })}
        />
        <NumberSetting
          label="Max parallel teams"
          description="How many teams may run at once."
          value={draft.max_parallel_teams}
          min={1}
          max={6}
          onChange={(v) => patch({ max_parallel_teams: v })}
        />
        <NumberSetting
          label="Runtime seconds"
          description="A team is stopped once it passes this."
          value={draft.max_runtime_seconds}
          min={60}
          max={3600}
          onChange={(v) => patch({ max_runtime_seconds: v })}
        />
        <NumberSetting
          label="Team token budget"
          description="Tokens a single team may spend."
          value={draft.max_team_tokens}
          min={1000}
          max={1000000}
          onChange={(v) => patch({ max_team_tokens: v })}
        />
        <NumberSetting
          label="Team tool-call budget"
          description="Tool calls a single team may make."
          value={draft.max_tool_calls}
          min={1}
          max={500}
          onChange={(v) => patch({ max_tool_calls: v })}
        />
        <SettingRow label="Worker model policy" description="Which brain the workers run on.">
          <NativeSelect
            value={draft.model_policy}
            onValueChange={(v) => patch({ model_policy: v })}
            aria-label="Worker model policy"
          >
            <option value="same_as_chat">Same as chat</option>
          </NativeSelect>
        </SettingRow>
        <ToggleSetting
          label="Allow artifact agents"
          description="Workers that produce documents, decks, and images."
          checked={draft.allow_artifact_agents}
          onChange={(v) => patch({ allow_artifact_agents: v })}
        />
        <ToggleSetting
          label="Allow code-writing agents"
          description="Workers that edit and write source."
          checked={draft.allow_code_agents}
          onChange={(v) => patch({ allow_code_agents: v })}
        />
        <ToggleSetting
          label="Allow connector/action agents"
          description="Workers that call your connected accounts."
          checked={draft.allow_connector_agents}
          onChange={(v) => patch({ allow_connector_agents: v })}
        />
        <ToggleSetting
          label="Require approval for external/destructive actions"
          description="A worker must ask before anything leaves the machine."
          checked={draft.require_action_approval}
          onChange={(v) => patch({ require_action_approval: v })}
        />
      </Section>

      <Section title="Chat visibility" tone="band" noPad>
        <SettingRow label="Team activity" description="How much of a team's work shows in the thread.">
          <NativeSelect
            value={draft.show_team_activity}
            onValueChange={(v) =>
              patch({ show_team_activity: v as ChatSettings["show_team_activity"] })
            }
            aria-label="Team activity"
          >
            <option value="off">Off</option>
            <option value="compact">Compact</option>
            <option value="detailed">Detailed</option>
          </NativeSelect>
        </SettingRow>
        <SettingRow label="Default team card" description="Whether a team card arrives open or folded.">
          <NativeSelect
            value={draft.default_team_card_state}
            onValueChange={(v) =>
              patch({ default_team_card_state: v as ChatSettings["default_team_card_state"] })
            }
            aria-label="Default team card"
          >
            <option value="collapsed">Collapsed</option>
            <option value="expanded">Expanded</option>
          </NativeSelect>
        </SettingRow>
        <ToggleSetting
          label="Show token usage"
          description="Per-turn token counts in the thread."
          checked={draft.show_token_usage}
          onChange={(v) => patch({ show_token_usage: v })}
        />
        <ToggleSetting
          label="Show worker summaries"
          description="Each worker's closing summary."
          checked={draft.show_worker_summaries}
          onChange={(v) => patch({ show_worker_summaries: v })}
        />
        <ToggleSetting
          label="Show artifacts"
          description="Inline previews of what a team produced."
          checked={draft.show_artifacts}
          onChange={(v) => patch({ show_artifacts: v })}
        />
      </Section>

      {err && <ErrorNote>{err}</ErrorNote>}
      <div className="flex items-center justify-end gap-2 pt-3">
        {savedAt && <span className="text-[12px] text-quiet">Saved</span>}
        <Button onClick={save} disabled={saving || loading}>
          {saving ? "Saving…" : "Save"}
        </Button>
      </div>
    </SettingsPanel>
  );
}

/** A number field on a setting row. One shape, every numeric cap. */
function NumberSetting({
  label,
  description,
  value,
  min,
  max,
  onChange,
}: {
  label: string;
  description?: string;
  value: number;
  min: number;
  max: number;
  onChange: (next: number) => void;
}) {
  return (
    <SettingRow
      label={label}
      description={description}
      control={
        <Input
          type="number"
          inputMode="numeric"
          aria-label={label}
          min={min}
          max={max}
          value={value}
          onChange={(e) => onChange(Number(e.target.value || min))}
          className="w-28 text-right font-mono tabular-nums"
        />
      }
    />
  );
}

/** A toggle on a setting row. Routes through the Switch primitive. */
function ToggleSetting({
  label,
  description,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <SettingRow
      label={label}
      description={description}
      control={
        <Switch
          checked={checked}
          disabled={disabled}
          onCheckedChange={onChange}
          aria-label={label}
        />
      }
    />
  );
}

/** The one failure note shape on this page: tinted, borderless, radius 8. */
function ErrorNote({ children }: { children: React.ReactNode }) {
  return (
    <p className="min-w-0 rounded-[8px] bg-danger/10 px-3 py-2 text-[12px] leading-relaxed text-danger [overflow-wrap:anywhere]">
      {children}
    </p>
  );
}

function GeneralSection({ status }: { status: CoreStatus | null }) {
  // The Brain section applies INSTANTLY. There is no draft and no Save.
  //
  // It used to hold a draft vendor + draft model behind a footer Save, next
  // to a Reset, next to the key box's own two buttons: four controls in one
  // card, and the only one that actually changed the brain was the least
  // prominent. The boss pasted a DeepSeek key, saw "connected", never saw the
  // Save, and kept running on ChatGPT while believing he had switched. A
  // control that must be found is a control that will be missed, so picking a
  // vendor now IS switching to it, and choosing a model IS setting it - the
  // same instant-apply shape the rest of Settings uses.
  const { setting, setModel, setProvider, refresh } = useGlobalModel();
  const liveProvider = ((setting?.provider ?? status?.provider ?? "") as string).toLowerCase();
  const effectiveModel = setting?.model ?? status?.model ?? "";
  const defaultModel = setting?.defaultModel ?? "";
  const availableProviders = setting?.availableProviders ?? [];

  // The vendor whose credential box is open. Normally the live one; it moves
  // when the boss taps a vendor he cannot switch to yet, so its key box is
  // reachable. That was the other half of the same bug: gating SELECTION on
  // being configured made the key box unreachable for exactly the vendors
  // that needed a key.
  const [focusVendor, setFocusVendor] = useState<string>(liveProvider || VENDORS[0].id);
  const [busyVendor, setBusyVendor] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [keyRows, setKeyRows] = useState<ProviderKeyRow[] | null>(null);

  useEffect(() => {
    if (liveProvider) setFocusVendor(liveProvider);
  }, [liveProvider]);

  useEffect(() => {
    const ac = new AbortController();
    // Settle to [] on failure rather than staying null: a permanent "still
    // loading" would hide the key box forever, which is the same dead end as
    // greying the vendor out.
    fetchProviderKeys(ac.signal).then((res) => setKeyRows(res?.providers ?? []));
    return () => ac.abort();
  }, []);

  const selectedVendor = findVendor(focusVendor);
  const liveVendor = findVendor(liveProvider);
  const keyRow = keyRows?.find((r) => r.provider === focusVendor) ?? null;

  function stateOf(id: string): {
    label: string;
    tone: "success" | "quiet" | "warning";
    ready: boolean;
  } {
    const row = keyRows?.find((r) => r.provider === id) ?? null;
    const ready = availableProviders.length === 0 || availableProviders.includes(id);
    if (id === liveProvider) return { label: "Answering", tone: "success", ready: true };
    if (row?.implemented === false) return { label: "No client yet", tone: "warning", ready: false };
    if (!ready) return { label: "Needs a key", tone: "warning", ready: false };
    return { label: "Ready", tone: "quiet", ready: true };
  }

  // Tapping a ready vendor switches Jarvis onto it, now. Tapping one that
  // needs a key just opens its key box - nothing is claimed that did not
  // happen.
  async function pickVendor(id: string) {
    setErr(null);
    setFocusVendor(id);
    if (id === liveProvider) return;
    if (!stateOf(id).ready) return;
    setBusyVendor(id);
    try {
      const res = await setProvider(id);
      if (!res.ok) setErr(res.error ?? "Could not switch to that vendor.");
    } finally {
      setBusyVendor(null);
    }
  }

  async function pickModel(id: string) {
    setErr(null);
    setBusyVendor(liveProvider);
    try {
      if (!(await setModel(id))) setErr("Could not save that model.");
    } finally {
      setBusyVendor(null);
    }
  }

  // The key box saved a credential and stopped there, so a working key still
  // left the boss on the old brain. Storing a key for the vendor on screen is
  // the instruction to run on it.
  async function onKeySaved(
    rows: ProviderKeyRow[],
    available: string[],
    activate = true,
  ): Promise<string> {
    setKeyRows(rows);
    await refresh();
    if (!activate || focusVendor === liveProvider) return "";
    if (!available.includes(focusVendor)) {
      return `Saved, but ${selectedVendor.label} is not answering yet, so I have left you on ${liveVendor.label}.`;
    }
    const res = await setProvider(focusVendor);
    if (!res.ok) return `Saved, but the switch failed: ${res.error ?? "unknown error"}`;
    return `Jarvis is on ${selectedVendor.label} now.`;
  }

  // Models belong to the LIVE vendor: this row sets what is answering, so
  // showing another vendor's catalog here would be an invitation to pick
  // something that cannot run.
  const modelOptions = liveVendor.models.some((m) => m.id === effectiveModel)
    ? liveVendor.models
    : effectiveModel
      ? [{ id: effectiveModel, label: `${effectiveModel} (custom)` }, ...liveVendor.models]
      : liveVendor.models;

  return (
    <div className="min-w-0 space-y-1">
      <SettingsPanel>
        <SettingRow
          label="Vendor"
          description="Tap one to switch. Jarvis moves the moment you do."
        >
          <div className="min-w-0">
            {VENDORS.map((v) => {
              const st = stateOf(v.id);
              return (
                <ListRow
                  key={v.id}
                  title={v.label}
                  tone={v.id === liveProvider ? "success" : st.tone}
                  onClick={() => pickVendor(v.id)}
                  chevron={false}
                  trailing={
                    <span
                      className={cn(
                        "text-[12px]",
                        v.id === liveProvider ? "text-success" : "text-quiet",
                      )}
                    >
                      {busyVendor === v.id ? "Switching…" : st.label}
                    </span>
                  }
                />
              );
            })}
          </div>
        </SettingRow>

        <SettingRow
          label="Model"
          description={`Which ${liveVendor.label.replace(" (API Key)", "").replace(" (Plan)", "")} model answers.`}
          control={
            setting?.source === "user" && defaultModel && effectiveModel !== defaultModel ? (
              <Button variant="ghost" onClick={() => pickModel("")}>
                Use default
              </Button>
            ) : null
          }
        >
          <NativeSelect
            value={effectiveModel}
            onValueChange={pickModel}
            aria-label="Model"
          >
            {modelOptions.map((m) => (
              <option key={m.id} value={m.id}>
                {m.id === defaultModel ? `${m.label} · default` : m.label}
              </option>
            ))}
          </NativeSelect>
        </SettingRow>

        {setting?.standby && (
          <p className="min-w-0 rounded-[8px] bg-warning/10 px-3 py-2 text-[12px] leading-relaxed text-foreground/90">
            {liveVendor.label} is out of usage
            {standbyResetClock(setting.standby) ? (
              <>
                {" "}until{" "}
                <span suppressHydrationWarning>{standbyResetClock(setting.standby)}</span>
              </>
            ) : null}
            . Jarvis is answering on {standbyLabel(setting.standby)} (your configured standby) until then, and switches back on its own.
          </p>
        )}

        {selectedVendor.auth === "subscription" ? (
          <SubscriptionConnectBlock />
        ) : selectedVendor.auth === "oauth" ? (
          <OAuthConnectBlock />
        ) : (
          <ApiKeyBlock
            vendor={selectedVendor}
            row={keyRow}
            loading={keyRows === null}
            onSaved={onKeySaved}
          />
        )}

        {err && <ErrorNote>{err}</ErrorNote>}
      </SettingsPanel>

      <PricingTable vendor={selectedVendor} />
    </div>
  );
}

// ── API key block (every api_key vendor) ──────────────────────────────────
// The paste-a-key half of the Brain section, mirroring OAuthConnectBlock for
// the subscription vendors. Before this, a vendor credential was an
// environment variable read once at boot, so adding a brain meant a Railway
// variable and a redeploy before the picker stopped saying "not configured".
//
// AT REST IT SHOWS ONE CONTROL. A stored key needs no form: it shows what is
// stored and a single Replace. Input, Save and Remove only exist once you
// have said you want to change something. The version that kept all three on
// screen permanently is what buried the button that actually mattered.
//
// Generic on purpose: it renders for whichever api_key vendor is in focus,
// reading the row Core returns for that id. Adding a vendor to the catalog
// gets this block for free.
function ApiKeyBlock({
  vendor,
  row,
  loading,
  onSaved,
}: {
  vendor: VendorEntry;
  /** True until Core has answered. Keeps the paste form from flashing over a
   *  key that is already stored. */
  loading: boolean;
  /** This vendor's credential state, owned by the Brain section so the
   *  vendor list and this box always agree. */
  row: ProviderKeyRow | null;
  /** Called after every store/remove with the refreshed rows and the
   *  provider ids Core will now answer on. Returns a line to append to the
   *  notice (e.g. the outcome of switching onto the vendor). */
  onSaved: (
    rows: ProviderKeyRow[],
    available: string[],
    activate?: boolean,
  ) => Promise<string>;
}) {
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState<"save" | "remove" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const stored = row?.source === "ui";
  const fromEnv = row?.source === "env";
  const unavailable = row?.implemented === false;
  const editable = row?.editable !== false;
  // No key yet is the one case where the form IS the resting state: there is
  // nothing to show and exactly one thing to do.
  const open = !loading && (editing || !row?.configured);

  // Reset when the focused vendor changes, so a message about DeepSeek can
  // never linger over the Gemini row.
  useEffect(() => {
    setDraft("");
    setEditing(false);
    setError(null);
    setNotice(null);
  }, [vendor.id]);

  async function save() {
    const key = draft.trim();
    if (!key) return;
    setBusy("save");
    setError(null);
    setNotice(null);
    try {
      const res = await saveProviderKey({ provider: vendor.id, api_key: key });
      if ("error" in res) {
        setError(res.error);
        return;
      }
      setDraft("");
      setEditing(false);
      const base =
        res.verified === "ok"
          ? `Saved and checked. ${vendor.label} answered.`
          : (res.note ??
            "Saved. I could not check it against the vendor, so the first turn will be the proof.");
      const switched = await onSaved(res.providers, res.available_providers ?? []);
      setNotice(switched ? `${base} ${switched}` : base);
    } finally {
      setBusy(null);
    }
  }

  async function remove() {
    setBusy("remove");
    setError(null);
    setNotice(null);
    try {
      const res = await deleteProviderKey(vendor.id);
      if ("error" in res) {
        setError(res.error);
        return;
      }
      setEditing(false);
      setNotice("Key removed.");
      await onSaved(res.providers, res.available_providers ?? [], false);
    } finally {
      setBusy(null);
    }
  }

  if (unavailable) {
    return (
      <p className="min-w-0 rounded-[8px] bg-warning/10 px-3 py-2 text-[12px] leading-relaxed text-foreground/90">
        I have no working client for {vendor.label.replace(" (API Key)", "")} yet, so a
        key here would not get you a brain. The models and prices are listed for
        reference; pick another vendor to actually run on.
      </p>
    );
  }

  const description = loading
    ? "Checking what is stored…"
    : stored
    ? `Stored here, ending ${row?.hint ?? "****"}.`
    : fromEnv
      ? `Coming from ${row?.env_var ?? vendor.keyEnv ?? ""} on the server. A key saved here takes precedence.`
      : `Paste your ${vendor.label.replace(" (API Key)", "")} key. It saves to Core and never comes back out of the browser.`;

  return (
    <div className="min-w-0 space-y-2">
      <SettingRow
        label="API key"
        description={description}
        control={
          !open && !loading && editable ? (
            <Button variant="ghost" onClick={() => setEditing(true)}>
              Replace
            </Button>
          ) : null
        }
      >
        {open && editable ? (
          <div className="min-w-0 space-y-2">
            <div className="flex min-w-0 flex-col gap-2 sm:flex-row">
              <Input
                type="password"
                inputMode="text"
                autoComplete="off"
                spellCheck={false}
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                placeholder={stored ? "Paste a replacement key" : "Paste key"}
                aria-label={`${vendor.label} API key`}
                className="min-w-0 flex-1"
              />
              <Button onClick={save} disabled={!draft.trim() || busy !== null}>
                {busy === "save" ? "Checking…" : "Save key"}
              </Button>
            </div>
            {stored && (
              <div className="flex items-center justify-between gap-2">
                <Button variant="ghost" onClick={() => setEditing(false)} disabled={busy !== null}>
                  Cancel
                </Button>
                <Button variant="ghost" onClick={remove} disabled={busy !== null}>
                  {busy === "remove" ? "Removing…" : "Remove key"}
                </Button>
              </div>
            )}
          </div>
        ) : null}
      </SettingRow>

      {!editable && (
        <p className="min-w-0 rounded-[8px] bg-warning/10 px-3 py-2 text-[12px] leading-relaxed text-foreground/90">
          This deployment has no database attached, so I cannot store a key here.
          Set {row?.env_var ?? vendor.keyEnv ?? "the vendor key"} on the server instead.
        </p>
      )}
      {notice && (
        <p className="min-w-0 rounded-[8px] bg-success/10 px-3 py-2 text-[12px] leading-relaxed text-foreground/90 [overflow-wrap:anywhere]">
          {notice}
        </p>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
    </div>
  );
}

// ── Pricing table ─────────────────────────────────────────────────────────
// Static snapshot of the most popular models for the selected vendor.
// Prices are USD per 1M tokens; subscription-billed vendors (openai_oauth)
// surface their plan note instead. Anyone updating prices in
// `models-catalog.ts` automatically updates this table.
function PricingTable({ vendor }: { vendor: VendorEntry }) {
  // Sortable column state. Default is catalog order - the boss has it
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

  // A subscription vendor has no per-token price, and a table of dashes is
  // worse than no table: it implies a cost that does not exist. The models'
  // own "Included in Max" note already says how it is paid for.
  if (!vendor.models.some((m) => m.input_per_mtok != null || m.output_per_mtok != null)) {
    return null;
  }

  return (
    <Section title={`${vendor.label} pricing`} badge="per 1M tokens" tone="band" noPad>
      <div className="min-w-0 overflow-x-auto pt-1 scroll-touch">
        <table className="w-full text-left text-[12.5px]">
          <thead>
            <tr className="border-b border-hairline font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
              <th className="py-1.5 pr-2 font-normal">Model</th>
              <SortHeader label="Input" active={sortKey === "input"} dir={sortDir} onClick={() => toggle("input")} />
              <SortHeader label="Output" active={sortKey === "output"} dir={sortDir} onClick={() => toggle("output")} />
            </tr>
          </thead>
          <tbody>
            {sorted.map((m) => (
              <tr key={m.id} className="border-b border-hairline last:border-b-0">
                <td className="py-2 pr-2">
                  <div className="flex min-w-0 flex-col">
                    <span className="font-medium text-foreground">{m.label}</span>
                    {m.tagline && (
                      <span className="text-[11px] text-quiet">
                        {m.tagline}
                        {m.note ? ` · ${m.note}` : ""}
                      </span>
                    )}
                  </div>
                </td>
                <td className="py-2 pl-2 text-right font-mono tabular-nums">
                  {m.input_per_mtok != null ? `$${m.input_per_mtok.toFixed(2)}` : "-"}
                </td>
                <td className="py-2 pl-2 text-right font-mono tabular-nums">
                  {m.output_per_mtok != null ? `$${m.output_per_mtok.toFixed(2)}` : "-"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Section>
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
    <th className="py-1.5 pl-2 text-right font-normal">
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

// ── Subscription block (claude_max) ────────────────────────────────────────
// The third credential shape, and the only one with nothing to fill in.
//
// Claude Max runs through Claude Code's own sign-in on the Mac, so there is no
// key to paste and no flow to click through: the honest UI is a live readout
// of whether that sign-in is there. Core probes the SAME thing the launcher
// checks before it starts a run, so this can never show connected over a brain
// that would then refuse.
//
// One control, and it always does something: Check again. There is no Connect
// button, because there is nothing here for it to do.
function SubscriptionConnectBlock() {
  const [status, setStatus] = useState<ClaudeMaxStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState(false);
  const [token, setToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setBusy(true);
    try {
      setStatus(await fetchClaudeMaxStatus());
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  async function save() {
    setSaving(true);
    setError(null);
    try {
      const err = await saveClaudeMaxToken(token.trim());
      if (err) {
        setError(err);
        return;
      }
      setToken("");
      setAdding(false);
      await refresh();
    } finally {
      setSaving(false);
    }
  }

  async function remove() {
    setSaving(true);
    try {
      await removeClaudeMaxToken();
      await refresh();
    } finally {
      setSaving(false);
    }
  }

  // Nothing renders until Core answers. An empty shell that fills in a second
  // later reads as "you have nothing" when it means "I haven't looked".
  if (status === null) {
    return (
      <p className="text-[13px] text-muted-foreground">
        {busy ? "Checking…" : "I couldn't reach Core to check this."}
      </p>
    );
  }

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex min-w-0 items-start gap-2.5">
        <span
          aria-hidden
          className={cn(
            "mt-[6px] size-2 shrink-0 rounded-full",
            status.connected ? "bg-success" : "bg-warning",
          )}
        />
        <div className="min-w-0 flex-1">
          {status.connected && status.account ? (
            <p className="min-w-0 break-words text-[14px] font-medium text-foreground">
              {status.account}
              {status.plan ? (
                <span className="text-muted-foreground"> · {status.plan} plan</span>
              ) : null}
            </p>
          ) : null}
          <p className="mt-0.5 min-w-0 break-words text-[13px] leading-relaxed text-muted-foreground">
            {status.detail}
          </p>
        </div>
      </div>

      {/* The cloud half. It only earns space when there is something to do or
          something to undo, so a fully set-up boss sees neither a form nor a
          dead button. */}
      {status.cloud_ready ? (
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="text-[13px] text-muted-foreground">
            Token saved, so this keeps working with your laptop shut.
          </span>
          <Button variant="ghost" size="sm" onClick={remove} disabled={saving}>
            Remove
          </Button>
        </div>
      ) : adding ? (
        <div className="flex min-w-0 flex-col gap-2">
          <Input
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="Paste the token here"
            inputMode="text"
            autoComplete="off"
            spellCheck={false}
            autoFocus
          />
          <div className="flex flex-wrap gap-2">
            <Button size="sm" onClick={save} disabled={saving || token.trim() === ""}>
              {saving ? "Saving…" : "Save"}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setAdding(false);
                setToken("");
                setError(null);
              }}
            >
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex min-w-0 flex-col gap-2">
          <p className="text-[13px] leading-relaxed text-muted-foreground">
            Run <code className="font-mono text-[12.5px]">claude setup-token</code> on your Mac and
            paste what it prints. That lets the cloud machine sign in as you, so this brain still
            answers when the Mac is asleep.
          </p>
          <div>
            <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
              Add token
            </Button>
          </div>
        </div>
      )}

      {error && <ErrorNote>{error}</ErrorNote>}

      <div>
        <Button variant="ghost" size="sm" onClick={refresh} disabled={busy}>
          {busy ? "Checking…" : "Check again"}
        </Button>
      </div>
    </div>
  );
}

// ── OAuth Connect block (openai_oauth only) ────────────────────────────────
// Three states:
//   • disconnected - "Connect ChatGPT" button kicks off /api/auth/openai/start,
//     opens the authorize URL in a new tab, reveals the paste box.
//   • paste-pending - user has clicked through, needs to paste the callback
//     URL (or code+state). Pressing "Connect" calls /exchange.
//   • connected - shows account email, last refresh, expiry, with a
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
    // Claim the sign-in window synchronously in the tap - opening it after
    // the await is silently blocked on iOS Safari / the installed PWA.
    // A new tab (not this one) so Studio stays open for the paste-back.
    const authWindow = openAuthWindow();
    setBusy("start");
    setError(null);
    try {
      const next = await startOpenAIOAuth();
      if (!next) {
        authWindow.close();
        setError("Could not start the connect flow - check Core logs.");
        return;
      }
      setPending(next);
      authWindow.navigate(next.authorize_url);
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
        setError("Disconnect failed - check Core logs.");
      }
    } finally {
      setBusy(null);
    }
  }

  const connected = !!status?.connected;
  const expiresAt = status?.expires_at ? new Date(status.expires_at) : null;
  const refreshedAt = status?.last_refreshed ? new Date(status.last_refreshed) : null;

  return (
    <div className="min-w-0 space-y-2 pt-1">
      <GroupLabel
        label="ChatGPT plan"
        trailing={
          loading ? (
            <Loader2 className="size-3.5 animate-spin text-quiet" aria-hidden />
          ) : (
            <span
              className={cn(
                "inline-flex items-center gap-1 font-mono text-[11px] uppercase tracking-[0.06em]",
                connected ? "text-brand" : "text-quiet",
              )}
            >
              {connected ? <Check className="size-3" aria-hidden /> : <Unplug className="size-3" aria-hidden />}
              {connected ? "connected" : "not connected"}
            </span>
          )
        }
      />

      {connected && (
        <Inset
          variant="kv"
          items={[
            ...(status?.account_email ? [{ label: "account", value: status.account_email }] : []),
            ...(refreshedAt
              ? [
                  {
                    label: "refreshed",
                    value: <span suppressHydrationWarning>{refreshedAt.toLocaleString()}</span>,
                  },
                ]
              : []),
            ...(expiresAt
              ? [
                  {
                    label: "expires",
                    value: <span suppressHydrationWarning>{expiresAt.toLocaleString()}</span>,
                  },
                ]
              : []),
          ]}
        />
      )}

      {!connected && !pending && (
        <Inset>
          <span className="font-medium text-foreground">Heads up:</span> after you log in, your
          browser will show a &quot;can&apos;t reach{" "}
          <code className="font-mono text-[12px]">localhost:1455</code>&quot; page. That&apos;s
          expected - OpenAI&apos;s OAuth client only redirects to localhost, and Studio lives in
          the cloud. Just copy the URL from the address bar back here.
        </Inset>
      )}

      {pending && (
        <div className="min-w-0 space-y-2">
          <p className="text-[12px] leading-relaxed text-quiet">
            Logged in? Copy the full address-bar URL from the &quot;can&apos;t reach&quot; page (or
            just the <code className="font-mono">code=…</code> value) and paste it below.
          </p>
          <a
            href={pending.authorize_url}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-[12px] text-info hover:underline"
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
            className="font-mono text-[12px]"
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

      {error && <ErrorNote>{error}</ErrorNote>}
      {successAt && Date.now() - successAt < 4000 && !error && (
        <p className="rounded-[8px] bg-success/10 px-3 py-2 text-[12px] text-success">
          Connected - Core will use this token on the next openai_oauth turn.
        </p>
      )}

      <div className="flex flex-wrap items-center justify-end gap-1.5">
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
  );
}

function splitToolName(name: string): { group: string; leaf: string } {
  const dunder = name.indexOf("__");
  if (dunder > 0) return { group: name.slice(0, dunder), leaf: name.slice(dunder + 2) };
  const dot = name.indexOf(".");
  if (dot > 0) return { group: name.slice(0, dot), leaf: name.slice(dot + 1) };
  return { group: "native", leaf: name };
}

/**
 * schemaFields flattens a JSON-Schema `properties` bag into the field list
 * `<Inset variant="schema">` renders. Anything the schema doesn't describe
 * (oneOf, nested objects) still reaches the boss through "Raw schema".
 */
function schemaFields(schema: Record<string, unknown> | undefined): InsetField[] {
  if (!schema) return [];
  const props = schema.properties;
  if (!props || typeof props !== "object") return [];
  const required = new Set(
    Array.isArray(schema.required) ? (schema.required as unknown[]).map(String) : [],
  );
  return Object.entries(props as Record<string, unknown>).map(([name, raw]) => {
    const v = (raw ?? {}) as Record<string, unknown>;
    return {
      name,
      type: typeof v.type === "string" ? v.type : undefined,
      note: typeof v.description === "string" ? v.description : undefined,
      required: required.has(name),
    };
  });
}

function ToolsSection({ tools }: { tools: ToolDescriptor[] }) {
  const [query, setQuery] = useState("");
  const [openTool, setOpenTool] = useState<string | null>(null);
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

  return (
    <SettingsPanel>
      <div className="min-w-0 space-y-3">
        <SearchInput
          value={query}
          onValueChange={setQuery}
          placeholder={`Search ${tools.length} things he can do…`}
        />
        {tools.length === 0 ? (
          <p className="py-2 text-[13.5px] text-quiet">
            No tools registered — Core is either offline or has an empty registry.
          </p>
        ) : groups.length === 0 ? (
          <p className="py-2 text-[13.5px] text-quiet">No tools match “{query}”.</p>
        ) : (
          <div className="min-w-0">
            {groups.map((g) => (
              <div key={g.name} className="min-w-0">
                <GroupLabel
                  label={g.name}
                  count={g.items.length}
                  trailing={
                    g.name === "native" ? (
                      <Wrench className="size-3.5 text-quiet" aria-hidden />
                    ) : (
                      <Server className="size-3.5 text-quiet" aria-hidden />
                    )
                  }
                />
                {g.items.map((t) => (
                  <ToolRow
                    key={t.name}
                    tool={t}
                    groupName={g.name}
                    open={openTool === t.name}
                    onToggle={() =>
                      setOpenTool((cur) => (cur === t.name ? null : t.name))
                    }
                  />
                ))}
              </div>
            ))}
          </div>
        )}
      </div>
    </SettingsPanel>
  );
}

/**
 * One tool = one row. Tapping opens ONE Inset in place (the field list),
 * with "Raw schema" revealing the JSON as the last link — never a bordered
 * card inside a bordered group inside a tinted panel inside a `<details>`,
 * which is what this replaces (five containers deep).
 */
function ToolRow({
  tool,
  groupName,
  open,
  onToggle,
}: {
  tool: ToolDescriptor;
  groupName: string;
  open: boolean;
  onToggle: () => void;
}) {
  const [raw, setRaw] = useState(false);
  const { leaf } = splitToolName(tool.name);
  const display = groupName && groupName !== "native" ? leaf : tool.name;
  const fields = schemaFields(tool.schema);
  const hasSchema = tool.schema && Object.keys(tool.schema).length > 0;

  return (
    <ListRow
      leading={<Wrench className="size-3.5" aria-hidden />}
      title={<span className="font-mono text-[12.5px]">{display}</span>}
      meta={open ? undefined : tool.description || undefined}
      onClick={onToggle}
      chevron={false}
    >
      {open ? (
        <div className="min-w-0 space-y-2">
          <p className="text-[13.5px] leading-relaxed text-muted-foreground [overflow-wrap:anywhere]">
            {tool.description || "No description."}
          </p>
          {fields.length > 0 ? <Inset variant="schema" fields={fields} /> : null}
          {hasSchema ? (
            <>
              {raw ? <Inset text={JSON.stringify(tool.schema, null, 2)} /> : null}
              <button
                type="button"
                onClick={() => setRaw((v) => !v)}
                className="text-[12px] font-medium text-quiet transition-colors hover:text-foreground"
              >
                {raw ? "Hide raw schema" : "Raw schema"}
              </button>
            </>
          ) : (
            <p className="text-[12px] text-quiet">No input schema.</p>
          )}
        </div>
      ) : null}
    </ListRow>
  );
}
