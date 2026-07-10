"use client";

import { useCallback, useEffect, useState } from "react";
import { Loader2, Lock, Plus, Shield, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { getMeta, setMeta } from "@/lib/api";
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
          , {w.note}
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

/* PhoneVaultCard - the two call-borne secrets, stored as infinity_meta keys
 * the agent can never read (they're released server-side only: the card is
 * attached to a brief by the phone_call tool when an errand authorizes
 * payment; the passphrase is checked in Go against inbound-call transcripts
 * and scrubbed from every stored line). Rendered by the Privacy section in
 * settings/page.tsx below the wards list.
 */
export function PhoneVaultCard() {
  const [passphrase, setPassphrase] = useState("");
  const [card, setCard] = useState({ name: "", number: "", exp: "", cvc: "", zip: "" });
  const [bossCell, setBossCell] = useState("");
  const [identity, setIdentity] = useState({ dob: "", account: "", last4: "", zip: "" });
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savedAt, setSavedAt] = useState<number | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    void Promise.all([
      getMeta("vault.phone_passphrase"),
      getMeta("vault.payment_card"),
      getMeta("vault.boss_cell"),
      getMeta("vault.identity"),
    ]).then(([p, c, bc, idn]) => {
        setPassphrase(p ?? "");
        setBossCell(bc ?? "");
        try {
          const pid = JSON.parse(idn ?? "");
          setIdentity({ dob: pid.dob ?? "", account: pid.account ?? "", last4: pid.last4 ?? "", zip: pid.zip ?? "" });
        } catch {
          // unset
        }
        try {
          const parsed = JSON.parse(c ?? "");
          setCard({ name: parsed.name ?? "", number: parsed.number ?? "", exp: parsed.exp ?? "", cvc: parsed.cvc ?? "", zip: parsed.zip ?? "" });
        } catch {
          // Legacy single-string value (or unset) - start fresh fields.
        }
        setLoaded(true);
      },
    );
  }, []);

  async function save() {
    setSaving(true);
    setErr(null);
    const ok =
      (await setMeta("vault.phone_passphrase", passphrase.trim())) &&
      (await setMeta(
        "vault.payment_card",
        card.number.trim() ? JSON.stringify(card) : "",
      )) &&
      (await setMeta("vault.boss_cell", bossCell.trim())) &&
      (await setMeta(
        "vault.identity",
        identity.dob.trim() || identity.account.trim() || identity.last4.trim()
          ? JSON.stringify(identity)
          : "",
      ));
    setSaving(false);
    if (!ok) {
      setErr("Save failed.");
      return;
    }
    setSavedAt(Date.now());
  }

  return (
    <div className="mt-4 space-y-3 rounded-md border bg-background p-3">
      <div className="flex items-center gap-2">
        <Lock className="size-4 text-muted-foreground" aria-hidden />
        <h3 className="text-sm font-semibold tracking-tight">Phone vault</h3>
      </div>
      <p className="text-xs text-muted-foreground">
    <span className="font-medium text-foreground">Passphrase</span>, say it when you
        call Jarvis&apos;s line and your spoken instructions execute for real (verified in
        code, from any phone, and scrubbed from transcripts).{" "}
    <span className="font-medium text-foreground">Payment card</span>, released only
        into a call you commissioned that requires prepayment; Jarvis&apos;s brain never
        sees the number and transcripts scrub it. Format it how you&apos;d read it to a
        merchant (number, expiry, CVC, zip).
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="space-y-1.5 text-xs font-medium text-muted-foreground">
          Spoken passphrase
          <Input
            type="password"
            autoComplete="off"
            placeholder="e.g. blue falcon 22"
            value={passphrase}
            onChange={(e) => setPassphrase(e.target.value)}
            disabled={!loaded}
          />
        </label>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="space-y-1.5 text-xs font-medium text-muted-foreground sm:col-span-2">
          Card number
          <Input type="password" inputMode="numeric" autoComplete="off" placeholder="4242 4242 4242 4242"
            value={card.number} onChange={(e) => setCard({ ...card, number: e.target.value })} disabled={!loaded} />
        </label>
        <label className="space-y-1.5 text-xs font-medium text-muted-foreground">
          Name on card
          <Input type="text" autoComplete="off" placeholder="Khaya Malabie"
            value={card.name} onChange={(e) => setCard({ ...card, name: e.target.value })} disabled={!loaded} />
        </label>
        <div className="grid grid-cols-3 gap-2">
          <label className="space-y-1.5 text-xs font-medium text-muted-foreground">
            Expiry
            <Input type="text" inputMode="numeric" autoComplete="off" placeholder="12/28"
              value={card.exp} onChange={(e) => setCard({ ...card, exp: e.target.value })} disabled={!loaded} />
          </label>
          <label className="space-y-1.5 text-xs font-medium text-muted-foreground">
            CVC
            <Input type="password" inputMode="numeric" autoComplete="off" placeholder="123"
              value={card.cvc} onChange={(e) => setCard({ ...card, cvc: e.target.value })} disabled={!loaded} />
          </label>
          <label className="space-y-1.5 text-xs font-medium text-muted-foreground">
            Zip
            <Input type="text" inputMode="numeric" autoComplete="off" placeholder="75034"
              value={card.zip} onChange={(e) => setCard({ ...card, zip: e.target.value })} disabled={!loaded} />
          </label>
        </div>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="space-y-1.5 text-xs font-medium text-muted-foreground">
          Your cell (for patch-in and callbacks)
          <Input type="tel" inputMode="tel" autoComplete="off" placeholder="+16095551234"
            value={bossCell} onChange={(e) => setBossCell(e.target.value)} disabled={!loaded} />
        </label>
      </div>
      <p className="text-xs text-muted-foreground">
        <span className="font-medium text-foreground">Identity</span>, released only into a call
        that must verify you (a bank, a utility) and only the specific detail they ask for. Same
        server-side-only guarantee as the card; never in Jarvis&apos;s context, scrubbed from transcripts.
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="space-y-1.5 text-xs font-medium text-muted-foreground">
          Date of birth
          <Input type="password" autoComplete="off" placeholder="MM/DD/YYYY"
            value={identity.dob} onChange={(e) => setIdentity({ ...identity, dob: e.target.value })} disabled={!loaded} />
        </label>
        <label className="space-y-1.5 text-xs font-medium text-muted-foreground">
          Account number
          <Input type="password" autoComplete="off" placeholder="optional"
            value={identity.account} onChange={(e) => setIdentity({ ...identity, account: e.target.value })} disabled={!loaded} />
        </label>
        <label className="space-y-1.5 text-xs font-medium text-muted-foreground">
          Last 4 (SSN)
          <Input type="password" inputMode="numeric" autoComplete="off" placeholder="optional"
            value={identity.last4} onChange={(e) => setIdentity({ ...identity, last4: e.target.value })} disabled={!loaded} />
        </label>
        <label className="space-y-1.5 text-xs font-medium text-muted-foreground">
          Billing zip
          <Input type="text" inputMode="numeric" autoComplete="off" placeholder="optional"
            value={identity.zip} onChange={(e) => setIdentity({ ...identity, zip: e.target.value })} disabled={!loaded} />
        </label>
      </div>
      {err && <p className="rounded-sm bg-danger/10 p-2 text-[11px] text-danger">{err}</p>}
      <div className="flex items-center justify-end gap-2">
        {savedAt && <span className="text-xs text-muted-foreground">Saved</span>}
        <Button onClick={save} disabled={saving || !loaded}>
          {saving ? "Saving…" : "Save vault"}
        </Button>
      </div>
    </div>
  );
}
