"use client";

import { useEffect, useMemo, useState } from "react";
import {
  Check,
  ChevronDown,
  CircleDashed,
  LayoutPanelLeft,
  RefreshCw,
  Search,
  Server,
  Sliders,
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
import { cn } from "@/lib/utils";
import {
  fetchCoreStatus,
  fetchMCP,
  fetchTools,
  type CoreStatus,
  type MCPStatus,
  type ToolDescriptor,
} from "@/lib/api";

type SectionId = "general" | "tools" | "mcp" | "canvas";

type SectionMeta = {
  id: SectionId;
  label: string;
  description: string;
  icon: typeof Sliders;
};

const SECTIONS: SectionMeta[] = [
  { id: "general", label: "General", description: "LLM provider, model, version", icon: Sliders },
  { id: "tools", label: "Tools", description: "Native + MCP tools the agent can call", icon: Wrench },
  { id: "mcp", label: "MCP servers", description: "Connected MCP servers + their tool exports", icon: Server },
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
    case "tools":
      return <ToolsSection tools={tools} />;
    case "mcp":
      return <McpSection servers={mcp} />;
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
  return (
    <div className="space-y-4">
      <SectionHeader title="General" description="LLM provider configuration. Set via env on the Core service." />
      <div className="rounded-md border bg-background">
        <Row label="provider" value={status?.provider || "—"} />
        <Row label="model" value={status?.model || "—"} />
        <Row label="version" value={status?.version || "—"} />
      </div>
      <p className="text-xs text-muted-foreground">
        Set <code className="font-mono">LLM_PROVIDER</code> / <code className="font-mono">LLM_MODEL</code> /{" "}
        <code className="font-mono">ANTHROPIC_API_KEY</code> on the Core service to change.
      </p>
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
        type="search"
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

function McpSection({ servers }: { servers: MCPStatus[] }) {
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();

  const filtered = useMemo(() => {
    if (!q) return servers;
    return servers.filter(
      (s) =>
        s.name.toLowerCase().includes(q) ||
        (s.tools ?? []).some((t) => t.toLowerCase().includes(q)),
    );
  }, [servers, q]);

  return (
    <div className="space-y-3">
      <SectionHeader
        title={`MCP servers (${servers.length})`}
        description="Each entry in core/config/mcp.yaml that was attempted at boot. Tap to see exported tools."
      />
      <SearchBar
        value={query}
        onChange={setQuery}
        placeholder="Search by server or tool name…"
      />
      {q && (
        <p className="text-[11px] text-muted-foreground">
          {filtered.length} match{filtered.length === 1 ? "" : "es"}
        </p>
      )}
      {servers.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No MCP servers configured. Edit <code className="font-mono">core/config/mcp.yaml</code> and restart Core.
        </p>
      ) : filtered.length === 0 ? (
        <p className="text-sm text-muted-foreground">No servers match “{query}”.</p>
      ) : (
        <ul className="space-y-1.5">
          {filtered.map((s) => (
            <McpCard key={s.name} server={s} highlightTool={q} />
          ))}
        </ul>
      )}
    </div>
  );
}

function McpCard({ server, highlightTool }: { server: MCPStatus; highlightTool?: string }) {
  const matchedTool = Boolean(
    highlightTool && (server.tools ?? []).some((t) => t.toLowerCase().includes(highlightTool)),
  );
  const [open, setOpen] = useState(false);
  const isOpen = open || matchedTool;
  const toolCount = server.tools?.length ?? 0;
  return (
    <li className="overflow-hidden rounded-md border bg-background">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-accent/40"
      >
        {server.connected ? (
          <Check className="size-3.5 shrink-0 text-success" aria-hidden />
        ) : server.error ? (
          <X className="size-3.5 shrink-0 text-danger" aria-hidden />
        ) : (
          <CircleDashed className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        )}
        <span className="truncate font-mono text-xs">{server.name}</span>
        {toolCount > 0 && (
          <Badge variant="secondary" className="h-4 shrink-0 px-1 font-mono text-[9px]">
            {toolCount}
          </Badge>
        )}
        <span className="ml-auto flex items-center gap-2">
          <span className="hidden text-[10px] text-muted-foreground sm:inline" suppressHydrationWarning>
            {new Date(server.tested).toLocaleTimeString()}
          </span>
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
          <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
            <span className="font-mono uppercase tracking-wider">last tested</span>
            <span suppressHydrationWarning>{new Date(server.tested).toLocaleString()}</span>
          </div>
          {server.error && (
            <p className="break-words rounded-sm bg-danger/10 p-2 text-[11px] text-danger">{server.error}</p>
          )}
          {toolCount > 0 ? (
            <div className="flex flex-wrap gap-1">
              {(server.tools ?? []).map((t) => {
                const matches = highlightTool && t.toLowerCase().includes(highlightTool);
                return (
                  <Badge
                    key={t}
                    variant="secondary"
                    className={cn(
                      "font-mono text-[10px]",
                      matches && "bg-info/15 text-info ring-1 ring-info/40",
                    )}
                  >
                    {t}
                  </Badge>
                );
              })}
            </div>
          ) : (
            !server.error && <p className="text-[11px] text-muted-foreground">No tools exported.</p>
          )}
        </div>
      )}
    </li>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-2 border-b px-3 py-2 text-sm last:border-0">
      <span className="text-muted-foreground">{label}</span>
      <code className="truncate font-mono text-xs">{value}</code>
    </div>
  );
}
