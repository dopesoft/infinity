"use client";

import { useCallback, useEffect, useState } from "react";
import { Loader2, Lock, Plus, Shield, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Section } from "@/components/dashboard/Section";
import { GroupLabel, ListRow } from "@/components/ui/list-row";
import { SettingRow } from "@/components/ui/setting-row";
import { getMeta, setMeta } from "@/lib/api";
import { useRealtime } from "@/lib/realtime/provider";
import { deleteWard, fetchWards, putWard, type WardDTO } from "@/lib/api";

// PrivacySection — manages Wards: structural privacy zones the agent must not
// freely read. A 'private' ward is denied outright; a 'sensitive' ward routes
// through the Trust queue. Enforced by proactive.WardGate on the Go side. The
// credential/.env/key defaults ship seeded, so this list is never empty.
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
    <Section title="Privacy" badge={loaded ? wards.length : undefined} noPad>
      <SettingRow
        label="Add a ward"
        description="A path Jarvis must not freely read. Patterns match a file's name, e.g. *.pem or .env."
      >
        <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center">
          <div className="min-w-0 flex-1">
            <Input
              value={glob}
              onChange={(e) => setGlob(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void add();
              }}
              placeholder="path pattern, e.g. *.key or secrets/*"
              inputMode="text"
              aria-label="Ward path pattern"
            />
          </div>
          <NativeSelect
            value={level}
            onValueChange={(v) => setLevel(v as "private" | "sensitive")}
            aria-label="Ward level"
            className="sm:w-[13.5rem]"
          >
            <option value="private">Private · blocks the read</option>
            <option value="sensitive">Sensitive · asks you first</option>
          </NativeSelect>
          <Button size="sm" onClick={add} disabled={busy || !glob.trim()} className="shrink-0">
            {busy ? <Loader2 className="size-3.5 animate-spin" /> : <Plus className="size-3.5" />}
            Add
          </Button>
        </div>
      </SettingRow>

      <GroupLabel label="Wards" count={loaded ? wards.length : undefined} />
      {!loaded ? (
        <p className="flex items-center gap-2 py-2 text-[13.5px] text-quiet">
          <Loader2 className="size-4 animate-spin" /> Loading…
        </p>
      ) : wards.length === 0 ? (
        <p className="py-2 text-[13.5px] text-quiet">
          No wards yet. Anything you add here is off limits until you say otherwise.
        </p>
      ) : (
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
            meta={w.level === "private" ? `Blocked outright${w.note ? ` · ${w.note}` : ""}` : `Asks you first${w.note ? ` · ${w.note}` : ""}`}
            chevron={false}
            trailing={
              <Button
                size="icon"
                variant="ghost"
                className="size-9"
                onClick={() => remove(w)}
                disabled={busy}
                aria-label={`Remove ward ${w.glob}`}
              >
                <Trash2 className="size-4 text-quiet" />
              </Button>
            }
          />
        ))
      )}
    </Section>
  );
}

/* PhoneVaultCard - the two call-borne secrets, stored as infinity_meta keys
 * the agent can never read (they're released server-side only: the card is
 * attached to a brief by the phone_call tool when an errand authorizes
 * payment; the passphrase is checked in Go against inbound-call transcripts
 * and scrubbed from every stored line). Rendered by the Privacy section in
 * settings/page.tsx below the wards list.
 *
 * Majordomo sweep: the bordered card became a `band` Section and every field
 * is a SettingRow. The two paragraphs explaining what each secret is for are
 * kept verbatim — they are the reason the boss is willing to type a card
 * number into a box, which is exactly the description §1.5 protects.
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
    <Section title="Phone vault" tone="band" noPad>
      <p className="pb-1 pt-2 text-[12.5px] leading-relaxed text-muted-foreground">
        <span className="font-medium text-foreground">Passphrase</span>, say it when you call
        Jarvis&apos;s line and your spoken instructions execute for real (verified in code, from any
        phone, and scrubbed from transcripts).{" "}
        <span className="font-medium text-foreground">Payment card</span>, released only into a call
        you commissioned that requires prepayment; Jarvis&apos;s brain never sees the number and
        transcripts scrub it. Format it how you&apos;d read it to a merchant (number, expiry, CVC,
        zip).
      </p>

      <SettingRow label="Spoken passphrase" htmlFor="vault-passphrase">
        <Input
          id="vault-passphrase"
          type="password"
          autoComplete="off"
          placeholder="e.g. blue falcon 22"
          value={passphrase}
          onChange={(e) => setPassphrase(e.target.value)}
          disabled={!loaded}
        />
      </SettingRow>

      <SettingRow label="Card number" htmlFor="vault-card-number">
        <Input
          id="vault-card-number"
          type="password"
          inputMode="numeric"
          autoComplete="off"
          placeholder="4242 4242 4242 4242"
          value={card.number}
          onChange={(e) => setCard({ ...card, number: e.target.value })}
          disabled={!loaded}
        />
      </SettingRow>

      <SettingRow label="Name on card" htmlFor="vault-card-name">
        <Input
          id="vault-card-name"
          type="text"
          autoComplete="off"
          placeholder="Khaya Malabie"
          value={card.name}
          onChange={(e) => setCard({ ...card, name: e.target.value })}
          disabled={!loaded}
        />
      </SettingRow>

      <SettingRow label="Expiry, CVC, zip">
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          <Input
            type="text"
            inputMode="numeric"
            autoComplete="off"
            placeholder="12/28"
            aria-label="Card expiry"
            value={card.exp}
            onChange={(e) => setCard({ ...card, exp: e.target.value })}
            disabled={!loaded}
          />
          <Input
            type="password"
            inputMode="numeric"
            autoComplete="off"
            placeholder="CVC"
            aria-label="Card CVC"
            value={card.cvc}
            onChange={(e) => setCard({ ...card, cvc: e.target.value })}
            disabled={!loaded}
          />
          <Input
            type="text"
            inputMode="numeric"
            autoComplete="off"
            placeholder="Billing zip"
            aria-label="Card billing zip"
            value={card.zip}
            onChange={(e) => setCard({ ...card, zip: e.target.value })}
            disabled={!loaded}
          />
        </div>
      </SettingRow>

      <SettingRow
        label="Your cell"
        description="Used for patch-in and callbacks."
        htmlFor="vault-boss-cell"
      >
        <Input
          id="vault-boss-cell"
          type="tel"
          inputMode="tel"
          autoComplete="off"
          placeholder="+16095551234"
          value={bossCell}
          onChange={(e) => setBossCell(e.target.value)}
          disabled={!loaded}
        />
      </SettingRow>

      <GroupLabel label="Identity" />
      <p className="pb-1 text-[12.5px] leading-relaxed text-muted-foreground">
        Released only into a call that must verify you (a bank, a utility) and only the specific
        detail they ask for. Same server-side-only guarantee as the card; never in Jarvis&apos;s
        context, scrubbed from transcripts.
      </p>

      <SettingRow label="Date of birth" htmlFor="vault-dob">
        <Input
          id="vault-dob"
          type="password"
          autoComplete="off"
          placeholder="MM/DD/YYYY"
          value={identity.dob}
          onChange={(e) => setIdentity({ ...identity, dob: e.target.value })}
          disabled={!loaded}
        />
      </SettingRow>
      <SettingRow label="Account number" htmlFor="vault-account">
        <Input
          id="vault-account"
          type="password"
          autoComplete="off"
          placeholder="optional"
          value={identity.account}
          onChange={(e) => setIdentity({ ...identity, account: e.target.value })}
          disabled={!loaded}
        />
      </SettingRow>
      <SettingRow label="Last 4 (SSN)" htmlFor="vault-last4">
        <Input
          id="vault-last4"
          type="password"
          inputMode="numeric"
          autoComplete="off"
          placeholder="optional"
          value={identity.last4}
          onChange={(e) => setIdentity({ ...identity, last4: e.target.value })}
          disabled={!loaded}
        />
      </SettingRow>
      <SettingRow label="Billing zip" htmlFor="vault-identity-zip">
        <Input
          id="vault-identity-zip"
          type="text"
          inputMode="numeric"
          autoComplete="off"
          placeholder="optional"
          value={identity.zip}
          onChange={(e) => setIdentity({ ...identity, zip: e.target.value })}
          disabled={!loaded}
        />
      </SettingRow>

      {err && (
        <p className="mt-2 rounded-[8px] bg-danger/10 px-3 py-2 text-[12px] text-danger">{err}</p>
      )}
      <div className="flex items-center justify-end gap-2 pt-3">
        {savedAt && <span className="text-[12px] text-quiet">Saved</span>}
        <Button onClick={save} disabled={saving || !loaded}>
          {saving ? "Saving…" : "Save vault"}
        </Button>
      </div>
    </Section>
  );
}
