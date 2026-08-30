"use client";

import { useEffect, useMemo, useState } from "react";
import { useTabParam } from "@/lib/useTabParam";
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
import { PrivacySection, PhoneVaultCard } from "@/components/settings/PrivacySection";
import { ConnectorsSection } from "@/components/settings/ConnectorsSection";
import { DashboardSettings } from "@/components/settings/DashboardSection";
import { NotificationsSection } from "@/components/settings/NotificationsSection";
import { TrustReviewPanel } from "@/components/TrustReviewPanel";
import { cn } from "@/lib/utils";
import {
  disconnectOpenAIOAuth,
  exchangeOpenAIOAuth,
  fetchCoreStatus,
  fetchChatSettings,
  fetchMCP,
  fetchOpenAIOAuthStatus,
  fetchTools,
  startOpenAIOAuth,
  saveChatSettings,
  type ChatSettings,
  type CoreStatus,
  type MCPStatus,
  type OpenAIOAuthStartResponse,
  type OpenAIOAuthStatusResponse,
  type ToolDescriptor,
} from "@/lib/api";
import { standbyLabel, standbyResetClock, useGlobalModel } from "@/lib/use-model";
import {
  VENDORS,
  findVendor,
  type VendorEntry,
  type VendorId,
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
  { id: "privacy", label: "Privacy", description: "Paths he must never read freely", icon: Shield },
  { id: "mcp", label: "Accounts", description: "Gmail, Slack, GitHub and the rest, and what he may do with each", icon: Plug },
  { id: "tools", label: "Abilities", description: "Everything he can do, and what each one touches", icon: Wrench },
  { id: "canvas", label: "Workbench", description: "Where he works, what the preview points at", icon: LayoutPanelLeft },
  { id: "notifications", label: "Alerts", description: "When he is allowed to interrupt you, and how", icon: Bell },
  { id: "dashboard", label: "Home layout", description: "Which cards show on your home screen", icon: LayoutDashboard },
];

const SECTION_IDS = SECTIONS.map((s) => s.id) as SectionId[];

export default function SettingsPage() {
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

  const counts = useMemo<Partial<Record<SectionId, number>>>(
    () => ({ tools: tools.length, mcp: mcp.length }),
    [tools.length, mcp.length],
  );

  // The one meta line under the title: counts, never a description (§1.5).
  const meta = useMemo(() => {
    const bits: string[] = [];
    if (tools.length) bits.push(`${tools.length} tools`);
    if (mcp.length) bits.push(`${mcp.length} connectors`);
    if (status?.version) bits.push(`core ${status.version}`);
    return bits.join(" · ");
  }, [tools.length, mcp.length, status?.version]);

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
                    {typeof counts[s.id] === "number" && counts[s.id] ? (
                      <span
                        className={cn(
                          "inline-flex h-4 min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-[10px] leading-none",
                          active === s.id
                            ? "bg-background/20 text-background"
                            : "bg-muted-foreground/15 text-muted-foreground",
                        )}
                      >
                        {counts[s.id]}
                      </span>
                    ) : null}
                  </PageTabsTrigger>
                ))}
              </PageTabsList>
            </PageTabs>
          </div>
          {/* px-4 sm:px-6 exactly matches Section tone="band"'s negative
              margins, so a band bleeds to the screen edge without ever
              widening the page. */}
          <div className="min-h-0 flex-1 overflow-y-auto scroll-touch px-4 pb-safe pt-2 sm:px-6">
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
      return (
        <>
          <PrivacySection />
          <PhoneVaultCard />
        </>
      );
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
    <Section
      title="Trust"
      // A decision aid, not a restatement: it says which approvals land here
      // versus inline in Chat, which is what the boss needs to know before
      // he starts clearing them.
      headerExtra={
        <span className="hidden text-[12px] text-quiet sm:inline">batched approvals</span>
      }
      noPad
    >
      <div className="pt-3">
        <TrustReviewPanel />
      </div>
    </Section>
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
    <div className="min-w-0 space-y-1">
      <Section
        title="Agent teams"
        badge={loading ? "loading" : draft.team_aggressiveness.replace("_", " ")}
        noPad
      >
        <SettingRow label="Agent teams" description="Whether Jarvis may split work across specialist agents.">
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
    </div>
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
  // Vendor picker hot-swaps Core's active provider via /api/settings/provider.
  // The change is synchronous - the next turn (and the Live composer's chip)
  // sees the new vendor immediately. Stored OAuth credentials persist across
  // vendor flips, so switching back to ChatGPT later doesn't require re-auth.
  // Model edits flow through /api/settings/model as before.
  const { setting, setModel, setProvider } = useGlobalModel();
  const liveProvider = ((setting?.provider ?? status?.provider ?? "") as string).toLowerCase();
  const effectiveModel = setting?.model ?? status?.model ?? "";
  const defaultModel = setting?.defaultModel ?? "";
  const availableProviders = setting?.availableProviders ?? [];

  // Vendor + model are both *drafts* until Save fires - selecting from
  // either dropdown mutates local state only. Save is the deterministic
  // commit; matches the BossProfilePanel pattern in this codebase.
  const [draftVendor, setDraftVendor] = useState<string>(liveProvider || VENDORS[0].id);
  const [draftModel, setDraftModel] = useState<string>(effectiveModel);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Sync drafts with whatever Core broadcasts (composer chip cycle,
  // first load, etc.). The model effect runs on every effectiveModel
  // change - but only resets the draft when the user hasn't started
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
  // than asking resolveModelEntry which vendor "owns" the id - that's
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
    <div className="min-w-0 space-y-1">
      <Section
        title="Brain"
        badge={findVendor(liveProvider).label}
        noPad
      >
        <SettingRow
          label="Vendor"
          description="Who answers. Switching keeps stored credentials, so you can flip back without re-auth."
        >
          <NativeSelect value={draftVendor} onValueChange={setDraftVendor} aria-label="Vendor">
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
        </SettingRow>

        <SettingRow label="Model" description="The exact model Jarvis runs on for chat.">
          <NativeSelect value={draftModel} onValueChange={setDraftModel} aria-label="Model">
            {dropdownOptions.map((m) => (
              <option key={m.id} value={m.id}>
                {m.id === defaultModel ? `${m.label} · default` : m.label}
              </option>
            ))}
          </NativeSelect>
        </SettingRow>

        {setting?.standby && (
          <p className="min-w-0 rounded-[8px] bg-warning/10 px-3 py-2 text-[12px] leading-relaxed text-foreground/90">
            {findVendor(liveProvider).label} is out of usage
            {standbyResetClock(setting.standby) ? (
              <>
                {" "}until{" "}
                <span suppressHydrationWarning>{standbyResetClock(setting.standby)}</span>
              </>
            ) : null}
            . Jarvis is answering on {standbyLabel(setting.standby)} (your configured standby) until then, and switches back on its own.
          </p>
        )}

        {isOAuthVendor && <OAuthConnectBlock />}

        {err && <ErrorNote>{err}</ErrorNote>}

        <div className="flex items-center justify-end gap-2 pt-3">
          {setting?.source === "user" && defaultModel && draftModel !== defaultModel && (
            <Button variant="ghost" onClick={clearOverride} disabled={busy}>
              Reset to default
            </Button>
          )}
          <Button onClick={save} disabled={!dirty || busy}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </div>
      </Section>

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
    setBusy("start");
    setError(null);
    try {
      const next = await startOpenAIOAuth();
      if (!next) {
        setError("Could not start the connect flow - check Core logs.");
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

  const filteredCount = groups.reduce((sum, g) => sum + g.items.length, 0);

  return (
    <Section
      title="Tools"
      badge={q ? `${filteredCount} of ${tools.length}` : `${tools.length} available`}
      noPad
    >
      <div className="min-w-0 space-y-3 pt-3">
        <SearchInput
          value={query}
          onValueChange={setQuery}
          placeholder="Search tools by name or description…"
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
    </Section>
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
