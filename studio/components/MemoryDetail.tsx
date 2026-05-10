"use client";

import { Calendar, Hash, History, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { TierBadge } from "@/components/TierBadge";
import { ProvenanceChain } from "@/components/ProvenanceChain";
import type { MemoryDTO, ObservationDTO, SearchResult } from "@/lib/api";

type ItemKind = "memory" | "observation" | "search";

type Item = {
  kind: ItemKind;
  id: string;
  title?: string;
  hookName?: string;
  text: string;
  createdAt: string;
  sessionId?: string;
  score?: number;
  streams?: string[];
  importance?: number;
  tier?: string;
  status?: string;
  version?: number;
  project?: string;
};

function fromAny(s: SearchResult | ObservationDTO | MemoryDTO): Item {
  if ("observation_id" in s) {
    return {
      kind: "search",
      id: s.observation_id,
      hookName: s.hook_name,
      text: s.raw_text,
      createdAt: s.created_at,
      sessionId: s.session_id,
      score: s.score,
      streams: s.streams,
    };
  }
  if ("hook_name" in s) {
    return {
      kind: "observation",
      id: s.id,
      hookName: s.hook_name,
      text: s.raw_text,
      createdAt: s.created_at,
      sessionId: s.session_id,
      importance: s.importance,
    };
  }
  return {
    kind: "memory",
    id: s.id,
    title: s.title,
    text: s.content,
    createdAt: s.created_at,
    tier: s.tier,
    status: s.status,
    version: s.version,
    importance: s.importance,
    project: s.project,
  };
}

export function MemoryDetail({
  source,
  onClose,
}: {
  source: SearchResult | ObservationDTO | MemoryDTO | null;
  onClose: () => void;
}) {
  if (!source) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Select an item to inspect it.
      </div>
    );
  }
  const item = fromAny(source);
  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-start justify-between gap-2 border-b px-4 py-3">
        <div className="min-w-0">
          <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <Hash className="size-3" aria-hidden />
            <code className="truncate font-mono">{item.id}</code>
          </div>
          {item.title && <h3 className="mt-1 truncate text-sm font-semibold">{item.title}</h3>}
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            {item.tier && <TierBadge tier={item.tier} stale={item.status === "superseded"} />}
            {item.hookName && (
              <Badge variant="outline" className="font-mono">
                {item.hookName}
              </Badge>
            )}
            {item.streams?.map((s) => (
              <Badge key={s} variant="info" className="font-mono uppercase">
                {s}
              </Badge>
            ))}
            {typeof item.importance === "number" && (
              <Badge variant="secondary" className="font-mono">
                importance {item.importance}
              </Badge>
            )}
            {typeof item.version === "number" && (
              <Badge variant="outline" className="font-mono">v{item.version}</Badge>
            )}
            {item.project && (
              <Badge variant="outline" className="font-mono">{item.project}</Badge>
            )}
          </div>
        </div>
        <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close detail">
          <X className="size-4" />
        </Button>
      </header>

      <div className="flex-1 space-y-4 overflow-y-auto px-4 py-3 scroll-touch">
        <Section title={item.kind === "memory" ? "Memory" : "Observation"}>
          <pre className="whitespace-pre-wrap break-words rounded-md border bg-muted/60 p-3 font-mono text-xs leading-relaxed scroll-touch">
            {item.text || "—"}
          </pre>
        </Section>

        <Section title="Metadata">
          <Row icon={<Calendar className="size-3.5" aria-hidden />} label="created">
            <time className="font-mono" suppressHydrationWarning>
              {new Date(item.createdAt).toLocaleString()}
            </time>
          </Row>
          {item.sessionId && (
            <Row icon={<History className="size-3.5" aria-hidden />} label="session">
              <code className="truncate font-mono">{item.sessionId}</code>
            </Row>
          )}
          {typeof item.score === "number" && (
            <Row label="score">
              <code className="font-mono">{item.score.toFixed(4)}</code>
            </Row>
          )}
        </Section>

        {item.kind === "memory" && (
          <Section title="Provenance chain">
            <ProvenanceChain memoryId={item.id} />
          </Section>
        )}
      </div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </div>
      <div className="space-y-1">{children}</div>
    </section>
  );
}

function Row({
  icon,
  label,
  children,
}: {
  icon?: React.ReactNode;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-2 border-b py-1 text-xs last:border-0">
      <span className="flex items-center gap-1 text-muted-foreground">
        {icon}
        {label}
      </span>
      <span className="min-w-0 truncate text-foreground">{children}</span>
    </div>
  );
}
