"use client";

import { useCallback, useEffect, useState } from "react";
import { CreditCard, Plus, Trash2 } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Section } from "@/components/dashboard/Section";
import { ListRow } from "@/components/ui/list-row";
import { SettingRow } from "@/components/ui/setting-row";
import { Switch } from "@/components/ui/switch";
import { PageTabs, PageTabsList, PageTabsTrigger } from "@/components/ui/page-tabs";
import { PrivacySection } from "@/components/settings/PrivacySection";
import { SettingsPanel } from "@/components/settings/SettingsPanel";
import { useTabParam } from "@/lib/useTabParam";
import {
  addWalletCard,
  fetchVaultDetails,
  fetchWalletCards,
  revokeWalletCard,
  saveVaultDetails,
  type VaultDetail,
  type VaultDetailsResponse,
  type WalletCardDTO,
} from "@/lib/api";

/**
 * The Vault: the three things Jarvis is trusted with, or forbidden from.
 *
 * WHAT THIS REPLACED, AND WHY
 *
 * One page that scrolled through a wallet, a list of file patterns and a
 * "Phone vault" form, with rows in it reading `secret:vault.phone_passphrase`.
 * That is a database label, printed at a person. Nobody outside this repo
 * knows what a `vault.` prefix is, and worse, two of those rows were not cards
 * at all — a spoken password and a date of birth were sitting in a list
 * captioned "cards Jarvis can pay with".
 *
 * The rule this now follows, and the one the project CLAUDE.md now carries:
 * a screen names things the way the boss would say them out loud. Cards.
 * Personal info. Off limits. The key, the table and the tool name are
 * implementation, and implementation belongs in a detail view or nowhere.
 *
 * THREE TABS RATHER THAN ONE SCROLL, because they are three different KINDS
 * of thing (ui/scoped-tabs.tsx states the test: switching a tab changes the
 * shape of a row and what you can do to it). A card is spent. A detail is
 * read out on a call. A path is refused. Nothing you can do to one makes sense
 * done to another.
 */

type VaultTab = "personal" | "cards" | "offlimits";

/**
 * Order is who-you-are, then what-you-spend, then what-he-cannot-touch.
 *
 * Personal info leads because it is the part that is about the boss rather
 * than about a transaction: his name, his date of birth, the password that
 * proves it is him on the phone. It is also the tab that is most often
 * incomplete, and a tab you have to go looking for is a tab that stays empty.
 * Cards are a thing you add when you want him buying; off limits is a rule you
 * set once and rarely revisit, so it sits last.
 */
const TABS: VaultTab[] = ["personal", "cards", "offlimits"];

export function VaultSection() {
  const [tab, setTab] = useTabParam<VaultTab>("vault", "personal", TABS);

  return (
    <SettingsPanel
      tabs={
        <PageTabs value={tab} onValueChange={(v) => setTab(v as VaultTab)}>
          <PageTabsList level="sub">
            <PageTabsTrigger value="personal">Personal info</PageTabsTrigger>
            <PageTabsTrigger value="cards">Cards</PageTabsTrigger>
            <PageTabsTrigger value="offlimits">Off limits</PageTabsTrigger>
          </PageTabsList>
        </PageTabs>
      }
    >
      {tab === "personal" ? <PersonalInfoTab /> : null}
      {tab === "cards" ? <CardsTab /> : null}
      {tab === "offlimits" ? <PrivacySection /> : null}
    </SettingsPanel>
  );
}

/* ── Cards ──────────────────────────────────────────────────────────────── */

/**
 * One list of cards, used for both buying online and paying over the phone.
 * There used to be two separate places to type a card number and neither knew
 * about the other; the phone now reads this same list (vault.SecretStore
 * PhoneCard), so adding a card here is the whole job.
 *
 * A card number is never shown back, not masked and not partially. Nothing on
 * the server can return one: it is decrypted inside the boundary that types it
 * into a checkout, and nowhere else.
 */
function CardsTab() {
  const [cards, setCards] = useState<WalletCardDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [locked, setLocked] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    const res = await fetchWalletCards();
    if (res.locked) setLocked(res.error ?? "The vault is locked.");
    else {
      setLocked(null);
      setCards(res.cards);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    // No Section heading: the tab already says Cards, and repeating it is the
    // exact thing the "say it once" rule forbids. The description keeps only
    // what the labels and the button cannot tell him — that Jarvis never sees
    // the number, and that a purchase still stops for him.
    <div className="min-w-0">
      <p className="pb-3 text-[12.5px] leading-relaxed text-muted-foreground">
        He sees the name you give a card and the last four digits, never the number. Every
        purchase stops and asks you first, showing the shop and the exact total.
      </p>

      {locked ? (
        <VaultLocked message={locked} />
      ) : loading ? (
        <Loading />
      ) : (
        <>
          {/* The empty state is the button. A sentence saying "no cards yet"
              beside a button saying "Add a card" is the count and the action
              told twice. */}
          {cards.length === 0 ? null : (
            cards.map((c) => (
              <ListRow
                key={c.id}
                leading={<CreditCard className="size-3.5 text-quiet" aria-hidden />}
                title={c.label}
                meta={[
                  `${c.brand} ending ${c.last4}`,
                  c.exp_month && c.exp_year
                    ? `expires ${String(c.exp_month).padStart(2, "0")}/${String(c.exp_year).slice(-2)}`
                    : null,
                  c.last_used_at ? "used before" : "never used",
                ]
                  .filter(Boolean)
                  .join(" · ")}
                chevron={false}
                trailing={
                  <Button
                    size="icon"
                    variant="ghost"
                    className="size-9"
                    aria-label={`Remove ${c.label}`}
                    onClick={async () => {
                      if (await revokeWalletCard(c.id)) void load();
                    }}
                  >
                    <Trash2 className="size-4 text-quiet" />
                  </Button>
                }
              />
            ))
          )}

          {adding ? (
            <AddCardForm
              onDone={() => {
                setAdding(false);
                void load();
              }}
              onCancel={() => setAdding(false)}
            />
          ) : (
            <div className={cards.length ? "pt-3" : ""}>
              <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
                <Plus className="size-3.5" aria-hidden />
                Add a card
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function AddCardForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [label, setLabel] = useState("");
  const [number, setNumber] = useState("");
  const [exp, setExp] = useState("");
  const [cvc, setCvc] = useState("");
  const [name, setName] = useState("");
  const [zip, setZip] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    setSaving(true);
    setError(null);
    const [mm, yy] = exp.split("/").map((s) => s.trim());
    const res = await addWalletCard({
      label,
      number,
      cvc,
      name,
      exp_month: Number(mm) || 0,
      exp_year: yy ? (yy.length === 2 ? 2000 + Number(yy) : Number(yy)) : 0,
      billing: { exp: exp.trim(), zip: zip.trim() },
    });
    setSaving(false);
    if (!res.ok) {
      setError(res.error ?? "That did not save.");
      return;
    }
    // Out of component state the moment it is stored. There is no reason for a
    // card number to sit in a browser after the request that carried it.
    setNumber("");
    setCvc("");
    onDone();
  };

  return (
    <div className="mt-3 min-w-0 space-y-3 rounded-[12px] bg-band p-3">
      <SettingRow label="What to call it" htmlFor="card-label">
        <Input
          id="card-label"
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          placeholder="Personal Amex"
          inputMode="text"
        />
      </SettingRow>
      <SettingRow label="Card number" htmlFor="card-number">
        <Input
          id="card-number"
          value={number}
          onChange={(e) => setNumber(e.target.value)}
          placeholder="4242 4242 4242 4242"
          inputMode="numeric"
          autoComplete="cc-number"
          className="font-mono"
        />
      </SettingRow>
      <SettingRow label="Expiry and security code">
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          <Input
            value={exp}
            onChange={(e) => setExp(e.target.value)}
            placeholder="12/28"
            inputMode="numeric"
            autoComplete="cc-exp"
            aria-label="Expiry date"
          />
          <Input
            value={cvc}
            onChange={(e) => setCvc(e.target.value)}
            placeholder="Three digits on the back"
            inputMode="numeric"
            autoComplete="cc-csc"
            aria-label="Security code"
          />
        </div>
      </SettingRow>
      <SettingRow label="Name on the card and billing zip">
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Khaya Malabie"
            inputMode="text"
            autoComplete="cc-name"
            aria-label="Name on the card"
          />
          <Input
            value={zip}
            onChange={(e) => setZip(e.target.value)}
            placeholder="Billing zip"
            inputMode="numeric"
            autoComplete="postal-code"
            aria-label="Billing zip"
          />
        </div>
      </SettingRow>

      {error ? (
        <p className="rounded-[8px] bg-danger/10 px-3 py-2 text-[12px] text-danger">{error}</p>
      ) : null}

      <p className="text-[12px] leading-relaxed text-quiet">
        This is encrypted the moment it arrives. It is never shown again, on this screen or
        anywhere else, and Jarvis never sees the number.
      </p>

      <div className="flex flex-col gap-2 sm:flex-row sm:justify-end">
        <Button variant="ghost" onClick={onCancel} disabled={saving} className="sm:order-1">
          Cancel
        </Button>
        <Button onClick={() => void submit()} disabled={saving || !number.trim()} className="sm:order-2">
          {saving ? <Spinner className="size-3.5" aria-hidden /> : null}
          Save card
        </Button>
      </div>
    </div>
  );
}

/* ── Personal info ──────────────────────────────────────────────────────── */

/**
 * Who the boss is: his name, where things go, and what proves he is him — with
 * a switch on each one saying whether Jarvis may hand it over.
 *
 * RENDERED FROM THE SERVER'S CATALOG, NOT FROM FIELDS TYPED HERE
 *
 * Every row comes from vault.Catalog in Go, which is the SAME list the checkout
 * filler matches against. So a detail he can type is a detail a checkout can
 * fill, and adding one later is one line in Go rather than a form change here
 * plus a filler change there plus the bug where they disagree.
 *
 * WHY SOME BOXES ARE EMPTY WHEN SOMETHING IS SAVED
 *
 * A name and an address are shown back, because a form you cannot proofread is
 * a form that stays wrong. A date of birth, an account number and the spoken
 * password are encrypted, and opening one to fill a box would put it back in a
 * browser, which is what encrypting them stopped. Those say "Saved" and let him
 * replace, never re-read.
 */

/**
 * Titles only. Each one already says what its fields are, so a blurb under it
 * would be the heading again in a longer form — the thing the "say it once"
 * rule in CLAUDE.md forbids. The two facts the titles genuinely cannot carry
 * (what the switches do, and that the last group is write-only) are said ONCE
 * each, at the top of the tab and on that group.
 */
const GROUPS: { id: VaultDetail["group"]; title: string; note?: string }[] = [
  { id: "about", title: "Who you are" },
  { id: "shipping", title: "Where things go" },
  { id: "billing", title: "Billing address" },
  { id: "verify", title: "Proving it's you", note: "Encrypted. You can replace one, never read it back." },
];

function PersonalInfoTab() {
  const [data, setData] = useState<VaultDetailsResponse | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [savedAt, setSavedAt] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const res = await fetchVaultDetails();
    setData(res);
    // Seed the boxes we are allowed to show. A sealed detail never has a value
    // to seed, so its box stays empty and its row says whether one is stored.
    setDrafts((prev) => {
      const next = { ...prev };
      for (const d of res.details) {
        if (!d.sealed && next[d.key] === undefined) next[d.key] = d.value ?? "";
      }
      return next;
    });
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!data) return <Loading />;
  // Could not reach it: say that once and stop. Rendering empty field groups,
  // a second warning about the encrypted half and a Save button underneath
  // would be three tellings of one problem, two of which we cannot actually
  // vouch for — we do not know the vault is locked, we know we could not ask.
  if (data.error) return <VaultLocked message={data.error} />;

  const dirty = data.details.filter((d) => {
    const draft = drafts[d.key];
    if (draft === undefined) return false;
    return d.sealed ? draft.trim() !== "" : draft !== (d.value ?? "");
  });

  async function save() {
    setSaving(true);
    setError(null);
    const values: Record<string, string> = {};
    for (const d of dirty) values[d.key] = drafts[d.key] ?? "";
    const res = await saveVaultDetails({ values });
    setSaving(false);
    if (!res.ok) {
      setError(res.error ?? "That did not save.");
      return;
    }
    // A sealed box is cleared after saving, because there is nothing to show
    // back and leaving the text sitting there would imply otherwise.
    setDrafts((prev) => {
      const next = { ...prev };
      for (const d of dirty) if (d.sealed) next[d.key] = "";
      return next;
    });
    setSavedAt(Date.now());
    void load();
  }

  async function toggleRelease(key: string, on: boolean) {
    // Optimistic, then reconciled by the reload: a switch that lags behind the
    // finger reads as broken.
    setData((prev) =>
      prev
        ? { ...prev, details: prev.details.map((d) => (d.key === key ? { ...d, releasable: on } : d)) }
        : prev,
    );
    const res = await saveVaultDetails({ release: { [key]: on } });
    if (!res.ok) setError(res.error ?? "That switch did not save.");
    void load();
  }

  async function toggleBillingSame(on: boolean) {
    setData((prev) => (prev ? { ...prev, billing_same_as_shipping: on } : prev));
    await saveVaultDetails({ billing_same_as_shipping: on });
    void load();
  }

  return (
    <div className="min-w-0 space-y-4">
      <p className="text-[12.5px] leading-relaxed text-muted-foreground">
        Switch off anything you would rather he never passed on to a shop or a company.
      </p>

      {!data.sealed_available ? (
        <VaultLocked message="The encrypted half of the vault is locked, so your date of birth, account number and spoken password cannot be saved yet. Your name and address still work." />
      ) : null}

      {GROUPS.map((group) => {
        const rows = data!.details.filter((d) => d.group === group.id);
        if (!rows.length) return null;
        const billingHidden = group.id === "billing" && data!.billing_same_as_shipping;

        return (
          <Section
            key={group.id}
            title={group.title}
            tone={group.id === "verify" ? "band" : "plain"}
            noPad
          >
            {group.note ? (
              <p className="pb-1 pt-1 text-[12.5px] leading-relaxed text-muted-foreground">
                {group.note}
              </p>
            ) : null}

            {group.id === "billing" ? (
              <SettingRow
                label="Same as where things ship"
                control={
                  <Switch
                    checked={data!.billing_same_as_shipping}
                    onCheckedChange={(v) => void toggleBillingSame(v)}
                    aria-label="Billing address is the same as shipping"
                  />
                }
              />
            ) : null}

            {billingHidden
              ? null
              : rows.map((d) => (
                  <SettingRow
                    key={d.key}
                    label={d.label}
                    // Only sealed rows carry a word here, because only they
                    // have something the empty box cannot show.
                    description={d.sealed ? (d.saved ? "Saved" : undefined) : undefined}
                    htmlFor={`detail-${d.key}`}
                    // The switch goes in the row's control slot, beside the
                    // label, exactly like every other toggle in Settings. It
                    // used to sit UNDER the input, which made this the one
                    // screen where a toggle was somewhere else. What it means
                    // is said once at the top of the tab, so the control is
                    // bare and the aria-label carries it for a screen reader.
                    control={
                      d.can_toggle ? (
                        <Switch
                          checked={d.releasable}
                          onCheckedChange={(v) => void toggleRelease(d.key, v)}
                          aria-label={`Let Jarvis pass on your ${d.label.toLowerCase()}`}
                        />
                      ) : (
                        <span className="text-[12px] text-quiet">Never passed on</span>
                      )
                    }
                  >
                    <Input
                      id={`detail-${d.key}`}
                      type={d.sealed ? "password" : "text"}
                      autoComplete="off"
                      inputMode={
                        d.key.endsWith("postal") || d.key === "ssn_last4" ? "numeric" : "text"
                      }
                      placeholder={d.placeholder ?? ""}
                      value={drafts[d.key] ?? ""}
                      onChange={(e) => setDrafts({ ...drafts, [d.key]: e.target.value })}
                      disabled={d.sealed && !data!.sealed_available}
                    />
                  </SettingRow>
                ))}
          </Section>
        );
      })}

      <Section title="Paying by phone" noPad>
        <SettingRow
          label="Card he uses on a call"
          description={
            data.card_count > 0 ? undefined : "Without one he arranges to pay on arrival."
          }
        >
          <span className="text-[12.5px] text-quiet">
            {data.card_count > 0
              ? `Your first saved card`
              : "None saved"}
          </span>
        </SettingRow>
      </Section>

      {error ? (
        <p className="rounded-[8px] bg-danger/10 px-3 py-2 text-[12px] text-danger">{error}</p>
      ) : null}

      {/* No greyed-out Save sitting under an untouched form. A disabled
          control that nothing on screen can enable is furniture: it occupies
          the spot where a real action goes and teaches him the page is
          broken. It appears when there is something to save and goes away
          again once there is not. */}
      {dirty.length || saving ? (
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-end">
          <Button onClick={() => void save()} disabled={saving}>
            {saving ? <Spinner className="size-3.5" aria-hidden /> : null}
            Save {dirty.length} change{dirty.length === 1 ? "" : "s"}
          </Button>
        </div>
      ) : savedAt ? (
        <p className="text-right text-[12px] text-quiet">Saved</p>
      ) : null}
    </div>
  );
}

/* ── shared ─────────────────────────────────────────────────────────────── */

function Loading() {
  return (
    <p className="flex items-center gap-2 py-3 text-[13.5px] text-quiet">
      <Spinner className="size-4" aria-hidden /> Loading…
    </p>
  );
}

/** A locked vault is said out loud, never rendered as "nothing saved". The
 *  difference between "there is nothing here" and "I cannot look" is the
 *  difference this whole product turns on. */
function VaultLocked({ message }: { message: string }) {
  return (
    <p className="rounded-[8px] bg-warning/10 px-3 py-2 text-[12.5px] leading-relaxed text-warning">
      {message}
    </p>
  );
}
