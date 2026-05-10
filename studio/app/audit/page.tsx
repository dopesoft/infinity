"use client";

import { useEffect, useMemo, useState } from "react";
import { IconRefresh, IconSearch } from "@tabler/icons-react";
import { TabFrame } from "@/components/TabFrame";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { fetchAuditLog, type AuditRowDTO } from "@/lib/api";

const OPS = ["all", "create", "update", "delete", "supersede"] as const;

export default function AuditPage() {
  const [rows, setRows] = useState<AuditRowDTO[]>([]);
  const [op, setOp] = useState<(typeof OPS)[number]>("all");
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [expanded, setExpanded] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    const r = await fetchAuditLog(200, op === "all" ? "" : op);
    setRows(r ?? []);
    setLoading(false);
  }
  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [op]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter((r) =>
      `${r.operation} ${r.actor} ${r.target} ${JSON.stringify(r.diff ?? {})}`.toLowerCase().includes(q),
    );
  }, [rows, query]);

  return (
    <TabFrame>
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="space-y-3 border-b px-3 py-3 sm:px-4">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-semibold">Audit log</h2>
            <Badge variant="secondary" className="font-mono">{filtered.length}</Badge>
            <div className="relative ml-2 flex-1">
              <IconSearch
                className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
                aria-hidden
              />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Filter rows…"
                className="pl-9"
                inputMode="search"
              />
            </div>
            <Button size="icon" variant="ghost" onClick={load} aria-label="Refresh" disabled={loading}>
              <IconRefresh className="size-4" />
            </Button>
          </div>
          <div className="flex flex-wrap items-center gap-1 text-xs">
            {OPS.map((o) => (
              <button
                key={o}
                onClick={() => setOp(o)}
                className={`rounded-md border px-2 py-1 font-mono uppercase tracking-wide ${
                  op === o
                    ? "border-info bg-info/10 text-info"
                    : "border-transparent bg-muted text-muted-foreground hover:bg-accent"
                }`}
              >
                {o}
              </button>
            ))}
          </div>
        </div>

        <div className="flex-1 overflow-y-auto px-3 py-3 scroll-touch sm:px-4">
          {filtered.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              {loading ? "Loading…" : "No matching rows."}
            </p>
          ) : (
            <ul className="space-y-1">
              {filtered.map((r) => (
                <li key={r.id} className="rounded-md border bg-card">
                  <button
                    type="button"
                    className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-xs"
                    onClick={() => setExpanded((p) => (p === r.id ? null : r.id))}
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      <Badge variant="outline" className="font-mono uppercase">{r.operation}</Badge>
                      <span className="font-mono text-muted-foreground">{r.actor}</span>
                      <code className="truncate font-mono">{r.target}</code>
                    </div>
                    <time className="font-mono text-muted-foreground" suppressHydrationWarning>
                      {new Date(r.created_at).toLocaleString()}
                    </time>
                  </button>
                  {expanded === r.id && r.diff && (
                    <pre className="border-t bg-muted/40 p-2 font-mono text-[11px] leading-relaxed">
                      {JSON.stringify(r.diff, null, 2)}
                    </pre>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </TabFrame>
  );
}
