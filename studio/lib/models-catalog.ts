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
        tagline: "frontier · 1M context",
        input_per_mtok: 15,
        output_per_mtok: 75,
      },
      {
        id: "claude-opus-4-5",
        label: "Claude Opus 4.5",
        tagline: "deep reasoning",
        input_per_mtok: 15,
        output_per_mtok: 75,
      },
      {
        id: "claude-sonnet-4-6",
        label: "Claude Sonnet 4.6",
        tagline: "balanced workhorse",
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
        tagline: "fastest, cheapest",
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
        id: "gpt-5.5",
        label: "GPT-5.5",
        tagline: "latest · flagship",
        recommended: true,
        input_per_mtok: 1.5,
        output_per_mtok: 12,
      },
      {
        id: "gpt-5.5-mini",
        label: "GPT-5.5 mini",
        tagline: "latest · fast",
        input_per_mtok: 0.3,
        output_per_mtok: 2.4,
      },
      {
        id: "gpt-5.4",
        label: "GPT-5.4",
        tagline: "flagship",
        input_per_mtok: 1.5,
        output_per_mtok: 12,
      },
      {
        id: "gpt-5.4-mini",
        label: "GPT-5.4 mini",
        tagline: "fast",
        input_per_mtok: 0.3,
        output_per_mtok: 2.4,
      },
      {
        id: "gpt-5.2",
        label: "GPT-5.2",
        tagline: "flagship",
        input_per_mtok: 1.4,
        output_per_mtok: 11,
      },
      {
        id: "gpt-5.2-mini",
        label: "GPT-5.2 mini",
        tagline: "fast",
        input_per_mtok: 0.3,
        output_per_mtok: 2.2,
      },
      {
        id: "gpt-5.1",
        label: "GPT-5.1",
        tagline: "warmer · conversational",
        input_per_mtok: 1.25,
        output_per_mtok: 10,
      },
      {
        id: "gpt-5.1-mini",
        label: "GPT-5.1 mini",
        tagline: "fast",
        input_per_mtok: 0.25,
        output_per_mtok: 2,
      },
      {
        id: "gpt-5.1-codex",
        label: "GPT-5.1 Codex",
        tagline: "coding-tuned",
        input_per_mtok: 1.25,
        output_per_mtok: 10,
      },
      {
        id: "gpt-5-pro",
        label: "GPT-5 Pro",
        tagline: "deepest reasoning",
        input_per_mtok: 15,
        output_per_mtok: 60,
      },
      {
        id: "gpt-5",
        label: "GPT-5",
        tagline: "original flagship",
        input_per_mtok: 1.25,
        output_per_mtok: 10,
      },
      {
        id: "gpt-5-codex",
        label: "GPT-5 Codex",
        tagline: "coding-tuned",
        input_per_mtok: 1.25,
        output_per_mtok: 10,
      },
      {
        id: "gpt-5-mini",
        label: "GPT-5 mini",
        tagline: "fast + balanced",
        input_per_mtok: 0.25,
        output_per_mtok: 2,
      },
      {
        id: "gpt-5-nano",
        label: "GPT-5 nano",
        tagline: "cheapest, fastest",
        input_per_mtok: 0.05,
        output_per_mtok: 0.4,
      },
      {
        id: "o4",
        label: "o4",
        tagline: "high-reasoning",
        input_per_mtok: 15,
        output_per_mtok: 60,
      },
      {
        id: "o4-mini",
        label: "o4-mini",
        tagline: "reasoning, faster",
        input_per_mtok: 1.1,
        output_per_mtok: 4.4,
      },
      {
        id: "gpt-4.1",
        label: "GPT-4.1",
        tagline: "legacy · long context",
        input_per_mtok: 2,
        output_per_mtok: 8,
      },
      {
        id: "gpt-4.1-mini",
        label: "GPT-4.1 mini",
        tagline: "legacy · cheap",
        input_per_mtok: 0.4,
        output_per_mtok: 1.6,
      },
      {
        id: "gpt-4o",
        label: "GPT-4o",
        tagline: "legacy · multimodal",
        input_per_mtok: 2.5,
        output_per_mtok: 10,
      },
      {
        id: "gpt-4o-mini",
        label: "GPT-4o mini",
        tagline: "legacy · cheap",
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
      "No per-token cost. Included in your ChatGPT subscription (Plus ~$20/mo, Pro ~$200/mo). Rate limits per ChatGPT plan apply.",
    models: [
      {
        id: "gpt-5.5",
        label: "GPT-5.5",
        tagline: "latest · default",
        recommended: true,
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5.5-mini",
        label: "GPT-5.5 mini",
        tagline: "latest · fast",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5.5-thinking",
        label: "GPT-5.5 Thinking",
        tagline: "latest · extended reasoning",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5.4",
        label: "GPT-5.4",
        tagline: "flagship",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5.4-mini",
        label: "GPT-5.4 mini",
        tagline: "fast",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5.2",
        label: "GPT-5.2",
        tagline: "flagship",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5.2-mini",
        label: "GPT-5.2 mini",
        tagline: "fast",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5.1",
        label: "GPT-5.1",
        tagline: "warmer · conversational",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5.1-mini",
        label: "GPT-5.1 mini",
        tagline: "fast",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5.1-codex",
        label: "GPT-5.1 Codex",
        tagline: "coding-tuned",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5",
        label: "GPT-5",
        tagline: "original flagship",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5-pro",
        label: "GPT-5 Pro",
        tagline: "deepest reasoning",
        note: "ChatGPT Pro only",
      },
      {
        id: "gpt-5-thinking",
        label: "GPT-5 Thinking",
        tagline: "extended reasoning",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5-codex",
        label: "GPT-5 Codex",
        tagline: "coding-tuned",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5-mini",
        label: "GPT-5 mini",
        tagline: "fast",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-5-nano",
        label: "GPT-5 nano",
        tagline: "cheapest, fastest",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "o4",
        label: "o4",
        tagline: "high reasoning",
        note: "ChatGPT Pro only",
      },
      {
        id: "o4-mini",
        label: "o4-mini",
        tagline: "reasoning, faster",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "o4-mini-high",
        label: "o4-mini-high",
        tagline: "reasoning, deeper",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-4.1",
        label: "GPT-4.1",
        tagline: "legacy",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-4.1-mini",
        label: "GPT-4.1 mini",
        tagline: "legacy · cheap",
        note: "Included in ChatGPT Plus / Pro",
      },
      {
        id: "gpt-4o-mini",
        label: "GPT-4o mini",
        tagline: "legacy · cheap",
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
        id: "gemini-3-pro",
        label: "Gemini 3 Pro",
        tagline: "frontier · multimodal",
        input_per_mtok: 2,
        output_per_mtok: 12,
      },
      {
        id: "gemini-3-flash",
        label: "Gemini 3 Flash",
        tagline: "fast",
        input_per_mtok: 0.35,
        output_per_mtok: 2.5,
      },
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
        id: "gemini-2.5-flash-lite",
        label: "Gemini 2.5 Flash Lite",
        tagline: "cheapest",
        input_per_mtok: 0.1,
        output_per_mtok: 0.4,
      },
      {
        id: "gemini-2.0-flash",
        label: "Gemini 2.0 Flash",
        tagline: "legacy",
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
