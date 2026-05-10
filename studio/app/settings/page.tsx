"use client";

import { useEffect, useState } from "react";
import {
  IconCheck,
  IconCircleDashed,
  IconRefresh,
  IconTool,
  IconX,
} from "@tabler/icons-react";
import { TabFrame } from "@/components/TabFrame";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  fetchCoreStatus,
  fetchMCP,
  fetchTools,
  type CoreStatus,
  type MCPStatus,
  type ToolDescriptor,
} from "@/lib/api";

export default function SettingsPage() {
  const [status, setStatus] = useState<CoreStatus | null>(null);
  const [tools, setTools] = useState<ToolDescriptor[]>([]);
  const [mcp, setMCP] = useState<MCPStatus[]>([]);
  const [loading, setLoading] = useState(true);

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

  return (
    <TabFrame>
      <div className="mx-auto w-full max-w-3xl flex-1 space-y-4 overflow-y-auto p-3 scroll-touch sm:p-4">
        <div className="flex items-center justify-between gap-2">
          <h1 className="text-lg font-semibold tracking-tight">Settings</h1>
          <Button size="sm" variant="ghost" onClick={refresh} disabled={loading}>
            <IconRefresh className="size-4" />
            {loading ? "loading…" : "refresh"}
          </Button>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>LLM provider</CardTitle>
            <CardDescription>Configured via env on the Core service.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            <Row label="provider" value={status?.provider || "—"} />
            <Row label="model" value={status?.model || "—"} />
            <Row label="version" value={status?.version || "—"} />
            <p className="pt-2 text-xs text-muted-foreground">
              Set <code className="font-mono">LLM_PROVIDER</code> /{" "}
              <code className="font-mono">LLM_MODEL</code> /{" "}
              <code className="font-mono">ANTHROPIC_API_KEY</code> on the Core service to change.
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Tools ({tools.length})</CardTitle>
            <CardDescription>Native + MCP tools available to the agent right now.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {tools.length === 0 ? (
              <p className="text-sm text-muted-foreground">No tools registered.</p>
            ) : (
              tools.map((t) => (
                <div key={t.name} className="flex items-start gap-2 rounded-md border bg-background p-3">
                  <IconTool className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <code className="font-mono text-sm">{t.name}</code>
                      {t.name.includes(".") && <Badge variant="outline">mcp</Badge>}
                    </div>
                    <p className="mt-0.5 text-xs text-muted-foreground">{t.description || "—"}</p>
                  </div>
                </div>
              ))
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>MCP servers ({mcp.length})</CardTitle>
            <CardDescription>Each entry in core/config/mcp.yaml that was attempted at boot.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {mcp.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No MCP servers configured. Edit{" "}
                <code className="font-mono">core/config/mcp.yaml</code> and restart Core.
              </p>
            ) : (
              mcp.map((s) => (
                <div key={s.name} className="rounded-md border bg-background p-3">
                  <div className="flex items-center gap-2">
                    {s.connected ? (
                      <IconCheck className="size-4 text-success" aria-hidden />
                    ) : s.error ? (
                      <IconX className="size-4 text-danger" aria-hidden />
                    ) : (
                      <IconCircleDashed className="size-4 text-muted-foreground" aria-hidden />
                    )}
                    <span className="font-mono text-sm">{s.name}</span>
                    <span className="ml-auto text-[11px] text-muted-foreground" suppressHydrationWarning>
                      {new Date(s.tested).toLocaleString()}
                    </span>
                  </div>
                  {s.error && (
                    <p className="mt-1 text-xs text-danger break-words">{s.error}</p>
                  )}
                  {s.tools.length > 0 && (
                    <div className="mt-1 flex flex-wrap gap-1">
                      {s.tools.map((t) => (
                        <Badge key={t} variant="secondary">
                          {t}
                        </Badge>
                      ))}
                    </div>
                  )}
                </div>
              ))
            )}
          </CardContent>
        </Card>
      </div>
    </TabFrame>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-2 border-b py-1.5 text-sm last:border-0">
      <span className="text-muted-foreground">{label}</span>
      <code className="font-mono">{value}</code>
    </div>
  );
}
