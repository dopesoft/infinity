"use client";

import { useCallback, useEffect, useState } from "react";
import { CreditCard, Loader2, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { addWalletCard, fetchWalletCards, revokeWalletCard, type WalletCardDTO } from "@/lib/api";
import { cn } from "@/lib/utils";

/**
 * WalletCard — the boss's stored cards.
 *
 * WHAT THIS DELIBERATELY CANNOT DO
 *
 * Show a card number. Not masked, not partially, not once. The number goes
 * straight from this form to the vault endpoint, is sealed with AES-256-GCM
 * before it is written, and the only thing that ever comes back is a label, a
 * brand and the last four. Jarvis gets the same view: he picks a card by its
 * id and never learns what is on it, and the number is decrypted in exactly
 * one place, inside the boundary that types it into a checkout.
 *
 * That is also why the number is entered HERE and never in chat. A card number
 * typed into a message is a card number in the transcript, in the memory
 * graph, and in whatever the embedder saw.
 */
export function WalletCard() {
  const [cards, setCards] = useState<WalletCardDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [locked, setLocked] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    const res = await fetchWalletCards();
    if (res.locked) setLocked(res.error ?? "The card vault is locked.");
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
    <section className="min-w-0 space-y-3">
      <header className="space-y-1">
        <h3 className="flex items-center gap-2 text-sm font-medium">
          <CreditCard className="size-4 text-muted-foreground" aria-hidden />
          Wallet
        </h3>
        <p className="text-xs text-muted-foreground">
          Cards Jarvis can pay with. He sees the label and the last four, never the number.
          Every purchase still stops for your approval, showing the merchant and the exact total.
        </p>
      </header>

      {locked ? (
        <p className="rounded-lg border border-warning/40 bg-warning/5 p-3 text-xs text-warning">
          {locked}
        </p>
      ) : loading ? (
        <p className="text-xs text-muted-foreground">Loading…</p>
      ) : (
        <>
          {cards.length === 0 ? (
            <p className="text-xs text-muted-foreground">No cards yet.</p>
          ) : (
            <ul className="space-y-1.5">
              {cards.map((c) => (
                <li
                  key={c.id}
                  className="flex min-w-0 items-center gap-3 rounded-lg border border-hairline px-3 py-2"
                >
                  <span className="min-w-0 flex-1 truncate text-sm">{c.label}</span>
                  <span className="shrink-0 font-mono text-xs text-muted-foreground">
                    {c.brand} ···· {c.last4}
                  </span>
                  <button
                    type="button"
                    aria-label={`Remove ${c.label}`}
                    onClick={async () => {
                      if (await revokeWalletCard(c.id)) void load();
                    }}
                    className="grid size-7 shrink-0 place-items-center rounded text-quiet transition-colors hover:bg-destructive/10 hover:text-destructive"
                  >
                    <Trash2 className="size-3.5" aria-hidden />
                  </button>
                </li>
              ))}
            </ul>
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
            <Button variant="outline" size="sm" onClick={() => setAdding(true)} className="gap-1.5">
              <Plus className="size-3.5" aria-hidden />
              Add a card
            </Button>
          )}
        </>
      )}
    </section>
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
    // Clear the number out of component state the moment it is stored. It is
    // no longer needed here and there is no reason for it to sit in memory.
    setNumber("");
    setCvc("");
    onDone();
  };

  return (
    <div className="space-y-2 rounded-lg border border-hairline p-3">
      <Input
        value={label}
        onChange={(e) => setLabel(e.target.value)}
        placeholder="What to call it (personal Amex)"
        inputMode="text"
        aria-label="Card label"
      />
      <Input
        value={number}
        onChange={(e) => setNumber(e.target.value)}
        placeholder="Card number"
        inputMode="numeric"
        autoComplete="cc-number"
        aria-label="Card number"
        className="font-mono"
      />
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <Input
          value={exp}
          onChange={(e) => setExp(e.target.value)}
          placeholder="MM/YY"
          inputMode="numeric"
          autoComplete="cc-exp"
          aria-label="Expiry"
        />
        <Input
          value={cvc}
          onChange={(e) => setCvc(e.target.value)}
          placeholder="Security code"
          inputMode="numeric"
          autoComplete="cc-csc"
          aria-label="Security code"
        />
      </div>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Name on card"
          inputMode="text"
          autoComplete="cc-name"
          aria-label="Name on card"
        />
        <Input
          value={zip}
          onChange={(e) => setZip(e.target.value)}
          placeholder="Billing ZIP"
          inputMode="numeric"
          autoComplete="postal-code"
          aria-label="Billing ZIP"
        />
      </div>

      {error ? <p className="text-xs text-destructive">{error}</p> : null}

      <p className="text-[11px] text-muted-foreground">
        This goes straight to the encrypted vault. It is never shown again, and Jarvis never sees it.
      </p>

      <div className="flex gap-2">
        <Button size="sm" onClick={() => void submit()} disabled={saving || !number.trim()}>
          {saving ? <Loader2 className={cn("size-3.5 animate-spin")} aria-hidden /> : null}
          Save card
        </Button>
        <Button size="sm" variant="ghost" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
      </div>
    </div>
  );
}
