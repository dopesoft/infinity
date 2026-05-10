"use client";

import { IconCalendar, IconHash, IconHistory, IconX } from "@tabler/icons-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import type { SearchResult, ObservationDTO } from "@/lib/api";

type Item = {
  id: string;
  hookName: string;
  text: string;
  createdAt: string;
  sessionId?: string;
  score?: number;
  streams?: string[];
  importance?: number;
};

function fromAny(s: SearchResult | ObservationDTO): Item {
  if ("observation_id" in s) {
    return {
      id: s.observation_id,
      hookName: s.hook_name,
      text: s.raw_text,
      createdAt: s.created_at,
      sessionId: s.session_id,
      score: s.score,
      streams: s.streams,
    };
  }
  return {
    id: s.id,
    hookName: s.hook_name,
    text: s.raw_text,
    createdAt: s.created_at,
    sessionId: s.session_id,
    importance: s.importance,
  };
}

export function MemoryDetail({
  source,
  onClose,
}: {
  source: SearchResult | ObservationDTO | null;
  onClose: () => void;
}) {
  if (!source) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Select an observation to inspect it.
      </div>
    );
  }
  const item = fromAny(source);
  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-start justify-between gap-2 border-b px-4 py-3">
        <div className="min-w-0">
          <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <IconHash className="size-3" aria-hidden />
            <code className="truncate font-mono">{item.id}</code>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <Badge variant="outline" className="font-mono">
              {item.hookName}
            </Badge>
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
          </div>
        </div>
        <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close detail">
          <IconX className="size-4" />
        </Button>
      </header>

      <div className="flex-1 space-y-4 overflow-y-auto px-4 py-3 scroll-touch">
        <Section title="Observation">
          <pre className="whitespace-pre-wrap break-words rounded-md border bg-muted/60 p-3 font-mono text-xs leading-relaxed scroll-touch">
            {item.text || "—"}
          </pre>
        </Section>

        <Section title="Metadata">
          <Row icon={<IconCalendar className="size-3.5" aria-hidden />} label="created">
            <time className="font-mono">{new Date(item.createdAt).toLocaleString()}</time>
          </Row>
          {item.sessionId && (
            <Row icon={<IconHistory className="size-3.5" aria-hidden />} label="session">
              <code className="truncate font-mono">{item.sessionId}</code>
            </Row>
          )}
          {typeof item.score === "number" && (
            <Row label="score">
              <code className="font-mono">{item.score.toFixed(4)}</code>
            </Row>
          )}
        </Section>

        <Section title="Provenance chain">
          <p className="text-xs text-muted-foreground">
            JIT verification chain renders here once the memory subsystem promotes this observation
            to a long-term memory record.
          </p>
        </Section>
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
