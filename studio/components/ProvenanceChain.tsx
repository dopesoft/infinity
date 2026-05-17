"use client";

import { useEffect, useState } from "react";
import { Clock, Hash, Link as LinkIcon } from "lucide-react";
import { fetchProvenance, type ProvenanceChain as Chain } from "@/lib/api";

export function ProvenanceChain({ memoryId }: { memoryId: string }) {
  const [chain, setChain] = useState<Chain | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setLoading(true);
    setError(null);
    const ctrl = new AbortController();
    fetchProvenance(memoryId, ctrl.signal)
      .then((c) => {
        setChain(c);
        if (!c) setError("not found");
      })
      .finally(() => setLoading(false));
    return () => ctrl.abort();
  }, [memoryId]);

  if (loading) {
    return <p className="text-xs text-muted-foreground">Loading provenance…</p>;
  }
  if (error) {
    return <p className="text-xs text-muted-foreground">No provenance available.</p>;
  }
  if (!chain) {
    return null;
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <p className="text-xs text-muted-foreground">
          {chain.sources.length} source{chain.sources.length === 1 ? "" : "s"}
        </p>
        <span className="font-mono text-[11px] text-muted-foreground">
          confidence {chain.confidence.toFixed(2)}
        </span>
      </div>
      {chain.sources.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          No source observations linked. This memory was created directly (e.g. by the{" "}
          <code className="font-mono">remember</code> tool).
        </p>
      ) : (
        <ol className="space-y-2">
          {chain.sources.map((src) => (
            <li
              key={src.observation_id}
              className="rounded-md border bg-background p-2 text-xs"
            >
              <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                <span className="flex items-center gap-1">
                  <Clock className="size-3" aria-hidden />
                  <time suppressHydrationWarning>{new Date(src.created_at).toLocaleString()}</time>
                </span>
                <span className="flex items-center gap-1">
                  <Hash className="size-3" aria-hidden />
                  <code className="font-mono">{src.observation_id.slice(0, 8)}</code>
                </span>
              </div>
              <p className="mt-1 break-words">{src.excerpt || "-"}</p>
              {src.session_id && (
                <p className="mt-1 flex items-center gap-1 text-[10px] text-muted-foreground">
                  <LinkIcon className="size-2.5" aria-hidden />
                  <code className="font-mono">{src.session_id}</code>
                </p>
              )}
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
