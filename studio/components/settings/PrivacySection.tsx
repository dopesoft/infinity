"use client";

import { useCallback, useEffect, useState } from "react";
import { Lock, Plus, Shield, Trash2 } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { ListRow } from "@/components/ui/list-row";
import { SettingRow } from "@/components/ui/setting-row";
import { useRealtime } from "@/lib/realtime/provider";
import { deleteWard, fetchWards, putWard, type WardDTO } from "@/lib/api";

// The "Off limits" tab of the Vault: files the agent must not freely read. A
// 'private' one is denied outright; a 'sensitive' one routes through the Trust
// queue. Enforced by proactive.WardGate on the Go side. The credential/.env/key
// defaults ship seeded, so this list is never empty.
//
// The word "ward" is gone from every line the boss can read. It is a perfectly
// good name for the concept in Go, and it meant nothing to him on a screen —
// see the naming rule in CLAUDE.md. The prop, the table and the gate keep it;
// the label says what happens.
//
// Majordomo sweep: the duplicated `<h2>` + paragraph became the Section title
// + count; the private-vs-sensitive explanation moved onto the control that
// makes that choice (where it is a decision aid, §1.5) and the segmented
// button strip inside a bordered card — the one nested-border pair left in
// Settings — is now a NativeSelect on a SettingRow. Wards render as ListRows,
// never bordered rows (§1.2).
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
    // No Section heading: the tab that got him here already says "Off limits".
    <div className="min-w-0">
      <SettingRow
        label="Add a file he can't open"
        description="A whole name like .env, or a star to cover a family of them like *.key."
      >
        <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center">
          <div className="min-w-0 flex-1">
            <Input
              value={glob}
              onChange={(e) => setGlob(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void add();
              }}
              placeholder="e.g. *.key, .env, or secrets/*"
              inputMode="text"
              aria-label="File name or pattern"
            />
          </div>
          <NativeSelect
            value={level}
            onValueChange={(v) => setLevel(v as "private" | "sensitive")}
            aria-label="What happens when he tries to open it"
            className="sm:w-[13.5rem]"
          >
            <option value="private">Never, refuse him</option>
            <option value="sensitive">Ask me first</option>
          </NativeSelect>
          <Button size="sm" onClick={add} disabled={busy || !glob.trim()} className="shrink-0">
            {busy ? <Spinner className="size-3.5" /> : <Plus className="size-3.5" />}
            Add
          </Button>
        </div>
      </SettingRow>

      {/* No group label: the add-row above and the list below are the only two
          things here, and naming the list repeats the row that fills it. The
          empty case is the add-row itself. */}
      {!loaded ? (
        <p className="flex items-center gap-2 py-2 text-[13.5px] text-quiet">
          <Spinner className="size-4" /> Loading…
        </p>
      ) : wards.length === 0 ? null : (
        wards.map((w) => (
          <ListRow
            key={w.id ?? w.glob}
            tone={w.level === "private" ? "danger" : "warning"}
            leading={
              w.level === "private" ? (
                <Lock className="size-3.5 text-danger" aria-hidden />
              ) : (
                <Shield className="size-3.5 text-warning" aria-hidden />
              )
            }
            title={<span className="font-mono text-[12.5px]">{w.glob}</span>}
            // The lock/shield icon and the row tone already carry which of
            // the two this is, so the meta line says only what is left: the
            // note, when there is one.
            meta={w.note || undefined}
            chevron={false}
            trailing={
              <Button
                size="icon"
                variant="ghost"
                className="size-9"
                onClick={() => remove(w)}
                disabled={busy}
                aria-label={`Stop protecting ${w.glob}`}
              >
                <Trash2 className="size-4 text-quiet" />
              </Button>
            }
          />
        ))
      )}
    </div>
  );
}
