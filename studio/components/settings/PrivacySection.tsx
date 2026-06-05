"use client";

import { useCallback, useEffect, useState } from "react";
import { Loader2, Lock, Plus, Shield, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { useRealtime } from "@/lib/realtime/provider";
import { deleteWard, fetchWards, putWard, type WardDTO } from "@/lib/api";

// PrivacySection — manages Wards: structural privacy zones the agent must not
// freely read. A 'private' ward is denied outright; a 'sensitive' ward routes
// through the Trust queue. Enforced by proactive.WardGate on the Go side. The
// credential/.env/key defaults ship seeded, so this list is never empty.
export function PrivacySection() {
  const [wards, setWards] = useState<WardDTO[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [glob, setGlob] = useState("");
  const [level, setLevel] = useState<"private" | "sensitive">("private");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const res = await fetchWards();
    if (res) setWards(res);
    setLoaded(true);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useRealtime("mem_wards", load);

  async function add() {
    const g = glob.trim();
    if (!g) return;
    setBusy(true);
    const ok = await putWard({ glob: g, level });
    setBusy(false);
    if (ok) {
      setGlob("");
      void load();
    }
  }

  async function remove(w: WardDTO) {
    setBusy(true);
    await deleteWard(w.id ? { id: w.id } : { glob: w.glob });
    setBusy(false);
    void load();
  }

  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <h2 className="flex items-center gap-2 text-base font-semibold tracking-tight">
          <Shield className="size-4" /> Privacy
        </h2>
        <p className="text-xs text-muted-foreground">
          Wards are paths Jarvis must not freely read. <b>Private</b> blocks the read outright;{" "}
          <b>Sensitive</b> asks you to approve it first. Patterns match a file&apos;s name (e.g.{" "}
          <code className="text-[11px]">*.pem</code>, <code className="text-[11px]">.env</code>).
        </p>
      </div>

      {/* Add a ward */}
      <div className="flex flex-col gap-2 rounded-xl border border-border bg-card/40 p-3 sm:flex-row sm:items-center">
        <Input
          value={glob}
          onChange={(e) => setGlob(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void add();
          }}
          placeholder="path pattern, e.g. *.key or secrets/*"
          inputMode="text"
          className="flex-1"
        />
        <div className="flex items-center gap-2">
          <div className="flex overflow-hidden rounded-md border border-border">
            {(["private", "sensitive"] as const).map((l) => (
              <button
                key={l}
                type="button"
                onClick={() => setLevel(l)}
                className={cn(
                  "px-3 py-2 text-xs font-medium capitalize transition-colors",
                  level === l
                    ? "bg-foreground text-background"
                    : "bg-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                {l}
              </button>
            ))}
          </div>
          <Button size="sm" onClick={add} disabled={busy || !glob.trim()}>
            {busy ? <Loader2 className="size-3.5 animate-spin" /> : <Plus className="size-3.5" />}
            <span className="ml-1">Add</span>
          </Button>
        </div>
      </div>

      {/* Ward list */}
      {!loaded ? (
        <div className="flex items-center gap-2 py-8 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> Loading…
        </div>
      ) : (
        <div className="space-y-2">
          {wards.map((w) => (
            <div
              key={w.id ?? w.glob}
              className="flex items-center justify-between gap-3 rounded-xl border border-border bg-card/40 p-3 min-w-0"
            >
              <div className="flex min-w-0 items-center gap-2">
                {w.level === "private" ? (
                  <Lock className="size-4 shrink-0 text-rose-400" />
                ) : (
                  <Shield className="size-4 shrink-0 text-amber-400" />
                )}
                <code className="truncate text-sm">{w.glob}</code>
                {w.note ? (
                  <span className="hidden truncate text-xs text-muted-foreground sm:inline">
                    — {w.note}
                  </span>
                ) : null}
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <span
                  className={cn(
                    "rounded-full px-2 py-0.5 text-[11px] font-medium capitalize",
                    w.level === "private"
                      ? "bg-rose-500/10 text-rose-400"
                      : "bg-amber-500/10 text-amber-400",
                  )}
                >
                  {w.level}
                </span>
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8"
                  onClick={() => remove(w)}
                  disabled={busy}
                  aria-label={`Remove ward ${w.glob}`}
                >
                  <Trash2 className="size-4 text-muted-foreground" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
