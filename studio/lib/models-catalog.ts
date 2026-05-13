/**
 * Single source of truth for the vendor / model / pricing catalog.
 *
 * Drives three surfaces that all need to stay aligned:
 *   1. Settings → General → vendor picker + pricing table.
 *   2. Settings → General → model dropdown for the selected vendor.
 *   3. Live → composer → ModelChip cycles through the models that belong
 *      to whichever vendor Core is actively wired to.
 *
 * Pricing is per 1M tokens, USD. Numbers are best-effort snapshots and
 * tend to drift; the table caveats this in the footer so a stale row
 * doesn't get treated as authoritative.
 */

export type VendorId = "anthropic" | "openai" | "openai_oauth" | "google";

export type ModelEntry = {
  /** Stable id used by Core's settings store + provider Stream() override. */
  id: string;
  /** Friendly label shown in dropdowns and the composer chip. */
  label: string;
  /** Short tag shown in the pricing table (e.g. "most capable", "fastest"). */
  tagline?: string;
  /** Marked as the boot default for the vendor. */
  recommended?: boolean;
  /** Per-1M-token input price in USD. Null for subscription-only providers. */
  input_per_mtok?: number;
  /** Per-1M-token output price in USD. Null for subscription-only providers. */
  output_per_mtok?: number;
  /** Free-form note (e.g. "Included in ChatGPT Plus/Pro"). */
  note?: string;
};

export type VendorEntry = {
  id: VendorId;
  label: string;
  /** Env var the provider reads for credentials (informational). */
  keyEnv?: string;
  /** Auth flow shape — drives Settings UI. */
  auth: "api_key" | "oauth";
  docsHint: string;
  /** Currency/pricing footer text shown beneath the pricing table. */
  pricingNote: string;
  models: ModelEntry[];
};

export const VENDORS: VendorEntry[] = [
  {
    id: "anthropic",
    label: "Anthropic",
    keyEnv: "ANTHROPIC_API_KEY",
    auth: "api_key",
    docsHint: "Get a key at console.anthropic.com → API keys.",
    pricingNote: "USD per 1M tokens. Prices may drift — check console.anthropic.com.",
    models: [
      {
        id: "claude-opus-4-7",
        label: "Claude Opus 4.7",
        tagline: "most capable",
        input_per_mtok: 15,
        output_per_mtok: 75,
      },
      {
        id: "claude-sonnet-4-6",
        label: "Claude Sonnet 4.6",
        tagline: "balanced",
        input_per_mtok: 3,
        output_per_mtok: 15,
      },
      {
        id: "claude-sonnet-4-5-20250929",
        label: "Claude Sonnet 4.5",
        tagline: "default",
        recommended: true,
        input_per_mtok: 3,
        output_per_mtok: 15,
      },
      {
        id: "claude-haiku-4-5-20251001",
        label: "Claude Haiku 4.5",
        tagline: "fastest",
        input_per_mtok: 1,
        output_per_mtok: 5,
      },
    ],
  },
  {
    id: "openai",
    label: "OpenAI (API key)",
    keyEnv: "OPENAI_API_KEY",
    auth: "api_key",
    docsHint: "Get a key at platform.openai.com → API keys.",
    pricingNote: "USD per 1M tokens. Prices may drift — check platform.openai.com.",
    models: [
      {
        id: "gpt-5",
        label: "GPT-5",
        tagline: "default",
        recommended: true,
        input_per_mtok: 5,
        output_per_mtok: 15,
      },
      {
        id: "gpt-4o",
        label: "GPT-4o",
        tagline: "multimodal",
        input_per_mtok: 2.5,
        output_per_mtok: 10,
      },
      {
        id: "gpt-4o-mini",
        label: "GPT-4o mini",
        tagline: "cheapest",
        input_per_mtok: 0.15,
        output_per_mtok: 0.6,
      },
    ],
  },
  {
    id: "openai_oauth",
    label: "OpenAI (ChatGPT subscription)",
    auth: "oauth",
    docsHint:
      "Connect with your ChatGPT account — inference runs against your Plus/Pro subscription quota instead of pay-per-token API credit.",
    pricingNote:
      "No per-token cost. Included in your ChatGPT subscription (Plus ~$20/mo, Pro ~$200/mo).",
    models: [
      {
        id: "gpt-5",
        label: "GPT-5",
        tagline: "default · subscription quota",
        recommended: true,
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5-codex",
        label: "GPT-5 Codex",
        tagline: "coding-tuned",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "o4-mini",
        label: "o4-mini",
        tagline: "reasoning, faster",
        note: "Included in ChatGPT Plus / Pro",
      },
    ],
  },
  {
    id: "google",
    label: "Google",
    keyEnv: "GOOGLE_API_KEY",
    auth: "api_key",
    docsHint: "Get a key at aistudio.google.com → Get API key.",
    pricingNote: "USD per 1M tokens. Prices may drift — check ai.google.dev.",
    models: [
      {
        id: "gemini-2.5-pro",
        label: "Gemini 2.5 Pro",
        tagline: "default",
        recommended: true,
        input_per_mtok: 1.25,
        output_per_mtok: 10,
      },
      {
        id: "gemini-2.5-flash",
        label: "Gemini 2.5 Flash",
        tagline: "fast + cheap",
        input_per_mtok: 0.3,
        output_per_mtok: 2.5,
      },
      {
        id: "gemini-2.0-flash",
        label: "Gemini 2.0 Flash",
        input_per_mtok: 0.1,
        output_per_mtok: 0.4,
      },
    ],
  },
];

export function findVendor(id: string | undefined | null): VendorEntry {
  return VENDORS.find((v) => v.id === id) ?? VENDORS[0];
}

/**
 * Map a raw model id to its (vendor, model) pair. Used by the composer chip
 * to figure out which list to cycle through for the currently active vendor.
 * Returns null when the id doesn't match any cataloged entry.
 */
export function resolveModelEntry(
  modelId: string,
): { vendor: VendorEntry; model: ModelEntry } | null {
  for (const v of VENDORS) {
    const m = v.models.find((mm) => mm.id === modelId);
    if (m) return { vendor: v, model: m };
  }
  return null;
}

/** Default model id for a vendor — the first entry tagged `recommended`. */
export function defaultModelFor(vendor: VendorEntry): string {
  return (vendor.models.find((m) => m.recommended) ?? vendor.models[0]).id;
}
