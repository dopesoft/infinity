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
 *
 * DeepSeek bills two rates by the clock (peak 01:00-04:00 and 06:00-10:00
 * UTC Mon-Fri, off-peak the rest). The table carries the PEAK number so the
 * figure is never an understatement, with the discount noted per row.
 */

export type VendorId = "anthropic" | "claude_max" | "openai" | "openai_oauth" | "google" | "deepseek";

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
  /** Auth flow shape - drives Settings UI. */
  /**
   * "subscription" is the Mac's own Claude sign-in: nothing to paste and no
   * flow to run, so Settings shows a live connection state instead of a
   * credential field.
   */
  auth: "api_key" | "oauth" | "subscription";
  models: ModelEntry[];
};

export const VENDORS: VendorEntry[] = [
  {
    id: "anthropic",
    label: "Claude (API Key)",
    keyEnv: "ANTHROPIC_API_KEY",
    auth: "api_key",
    models: [
      {
        id: "claude-fable-5-1",
        label: "Fable 5.1",
        tagline: "newest, and the hardest work",
      },
      {
        id: "claude-fable-5",
        label: "Fable 5",
        tagline: "most capable, longest-horizon",
        input_per_mtok: 10,
        output_per_mtok: 50,
      },
      {
        id: "claude-opus-5",
        label: "Opus 5",
        tagline: "frontier",
        input_per_mtok: 10,
        output_per_mtok: 50,
      },
      {
        id: "claude-sonnet-5",
        label: "Sonnet 5",
        tagline: "best value",
        recommended: true,
        input_per_mtok: 2,
        output_per_mtok: 10,
      },
      {
        id: "claude-opus-4-8",
        label: "Opus 4.8",
        tagline: "prior flagship",
        input_per_mtok: 5,
        output_per_mtok: 25,
      },
      {
        id: "claude-opus-4-7",
        label: "Opus 4.7",
        tagline: "previous frontier",
        input_per_mtok: 5,
        output_per_mtok: 25,
      },
      {
        id: "claude-opus-4-6",
        label: "Opus 4.6",
        tagline: "frontier",
        input_per_mtok: 5,
        output_per_mtok: 25,
      },
      {
        id: "claude-opus-4-5",
        label: "Opus 4.5",
        tagline: "frontier",
        input_per_mtok: 5,
        output_per_mtok: 25,
      },
      {
        id: "claude-opus-4-1",
        label: "Opus 4.1",
        tagline: "legacy flagship",
        input_per_mtok: 15,
        output_per_mtok: 75,
      },
      {
        id: "claude-opus-4",
        label: "Opus 4",
        tagline: "legacy flagship",
        input_per_mtok: 15,
        output_per_mtok: 75,
      },
      {
        id: "claude-sonnet-4-6",
        label: "Sonnet 4.6",
        tagline: "balanced",
        input_per_mtok: 3,
        output_per_mtok: 15,
      },
      {
        id: "claude-sonnet-4-5-20250929",
        label: "Sonnet 4.5",
        tagline: "balanced",
        input_per_mtok: 3,
        output_per_mtok: 15,
      },
      {
        id: "claude-sonnet-4",
        label: "Sonnet 4",
        tagline: "balanced",
        input_per_mtok: 3,
        output_per_mtok: 15,
      },
      {
        id: "claude-haiku-4-5-20251001",
        label: "Haiku 4.5",
        tagline: "fastest, cheapest",
        input_per_mtok: 1,
        output_per_mtok: 5,
      },
      {
        id: "claude-haiku-3-5",
        label: "Haiku 3.5",
        tagline: "legacy · cheap",
        input_per_mtok: 0.8,
        output_per_mtok: 4,
      },
    ],
  },
  {
    id: "claude_max",
    label: "Claude Max Plan",
    auth: "subscription",
    // What Claude Code actually serves on a subscription, which is NOT the
    // same list as the API-key vendor above. The 4.x line (Opus 4.6/4.7/4.8)
    // is API-only; through Claude Code a plan gets the 5-series and Haiku 4.5.
    //
    // Ordered best to worst, and that is the whole ordering rule.
    //
    // Two entries are deliberately gone. "Best available" resolved to whatever
    // was newest, which means the model could change under him without him
    // touching anything - a picker should say what he will get. And "Opus 5 ·
    // 1M" was a distinction without a difference: the Claude 5 family ships a
    // million-token window as standard, so plain Opus 5 IS the 1M one.
    //
    // No aliases at all: four models, named, in order. Anything that resolves
    // to "whatever is newest" or splits a turn across two models is a thing he
    // has to reason about before he can pick, and he asked for a list he can
    // read top to bottom.
    models: [
      // Verified against the plan before being listed: `claude -p --model
      // claude-fable-5-1` answers in 2.6s on the Mac bridge. It needed Claude
      // Code 2.1.251+, and the bridge was on 2.1.139 with the auto-updater
      // switched off, so listing it before updating would have 400'd every
      // turn. A picker entry that cannot run is a landmine; check first.
      {
        id: "claude-fable-5-1",
        label: "Fable 5.1",
        tagline: "the hardest and longest-running work",
      },
      {
        id: "claude-opus-5",
        label: "Opus 5",
        tagline: "best all-round, and what Max runs by default",
        recommended: true,
      },
      {
        id: "claude-sonnet-5",
        label: "Sonnet 5",
        tagline: "fast, and strong enough for most of it",
      },
      {
        id: "claude-haiku-4-5",
        label: "Haiku 4.5",
        tagline: "cheapest and quickest",
      },
    ],
  },
  {
    id: "openai",
    label: "ChatGPT (API Key)",
    keyEnv: "OPENAI_API_KEY",
    auth: "api_key",
    models: [
      {
        id: "gpt-5.6-sol",
        label: "GPT-5.6 Sol",
        tagline: "flagship",
        recommended: true,
        input_per_mtok: 5,
        output_per_mtok: 30,
      },
      {
        id: "gpt-5.6",
        label: "GPT-5.6",
        tagline: "alias → Sol",
        input_per_mtok: 5,
        output_per_mtok: 30,
      },
      {
        id: "gpt-5.6-terra",
        label: "GPT-5.6 Terra",
        tagline: "balanced",
        input_per_mtok: 2.5,
        output_per_mtok: 15,
      },
      {
        id: "gpt-5.6-luna",
        label: "GPT-5.6 Luna",
        tagline: "fast, cheapest 5.6",
        input_per_mtok: 1,
        output_per_mtok: 6,
      },
      {
        id: "gpt-5.5",
        label: "GPT-5.5",
        tagline: "prior flagship",
        input_per_mtok: 5,
        output_per_mtok: 30,
      },
      {
        id: "gpt-5.5-pro",
        label: "GPT-5.5 Pro",
        tagline: "premium reasoning",
        input_per_mtok: 30,
        output_per_mtok: 180,
      },
      {
        id: "gpt-5.4",
        label: "GPT-5.4",
        tagline: "flagship",
        input_per_mtok: 2.5,
        output_per_mtok: 15,
      },
      {
        id: "gpt-5.4-mini",
        label: "GPT-5.4 mini",
        tagline: "fast",
        input_per_mtok: 0.75,
        output_per_mtok: 4.5,
      },
      {
        id: "gpt-5.4-nano",
        label: "GPT-5.4 nano",
        tagline: "cheapest current-gen",
        input_per_mtok: 0.2,
        output_per_mtok: 1.25,
      },
      {
        id: "gpt-5.4-pro",
        label: "GPT-5.4 Pro",
        tagline: "premium reasoning",
        input_per_mtok: 30,
        output_per_mtok: 180,
      },
      {
        id: "gpt-5.2",
        label: "GPT-5.2",
        tagline: "prior flagship",
        input_per_mtok: 1.75,
        output_per_mtok: 14,
      },
      {
        id: "gpt-5.2-pro",
        label: "GPT-5.2 Pro",
        tagline: "premium reasoning",
        input_per_mtok: 21,
        output_per_mtok: 168,
      },
      {
        id: "gpt-5.1",
        label: "GPT-5.1",
        tagline: "conversational",
        input_per_mtok: 1.25,
        output_per_mtok: 10,
      },
      {
        id: "gpt-5",
        label: "GPT-5",
        tagline: "original flagship",
        input_per_mtok: 1.25,
        output_per_mtok: 10,
      },
      {
        id: "gpt-5-pro",
        label: "GPT-5 Pro",
        tagline: "premium reasoning",
        input_per_mtok: 15,
        output_per_mtok: 120,
      },
      {
        id: "gpt-5-mini",
        label: "GPT-5 mini",
        tagline: "fast",
        input_per_mtok: 0.25,
        output_per_mtok: 2,
      },
      {
        id: "gpt-5-nano",
        label: "GPT-5 nano",
        tagline: "cheapest",
        input_per_mtok: 0.05,
        output_per_mtok: 0.4,
      },
      {
        id: "gpt-4.1",
        label: "GPT-4.1",
        tagline: "long context",
        input_per_mtok: 2,
        output_per_mtok: 8,
      },
      {
        id: "gpt-4.1-mini",
        label: "GPT-4.1 mini",
        tagline: "fast",
        input_per_mtok: 0.4,
        output_per_mtok: 1.6,
      },
      {
        id: "gpt-4.1-nano",
        label: "GPT-4.1 nano",
        tagline: "cheapest",
        input_per_mtok: 0.1,
        output_per_mtok: 0.4,
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
        tagline: "cheap",
        input_per_mtok: 0.15,
        output_per_mtok: 0.6,
      },
      {
        id: "o4-mini",
        label: "o4-mini",
        tagline: "reasoning",
        input_per_mtok: 1.1,
        output_per_mtok: 4.4,
      },
      {
        id: "o3",
        label: "o3",
        tagline: "reasoning",
        input_per_mtok: 2,
        output_per_mtok: 8,
      },
      {
        id: "o3-mini",
        label: "o3-mini",
        tagline: "reasoning, faster",
        input_per_mtok: 1.1,
        output_per_mtok: 4.4,
      },
      {
        id: "o3-pro",
        label: "o3-pro",
        tagline: "reasoning, deepest",
        input_per_mtok: 20,
        output_per_mtok: 80,
      },
      {
        id: "o1",
        label: "o1",
        tagline: "legacy reasoning",
        input_per_mtok: 15,
        output_per_mtok: 60,
      },
      {
        id: "o1-mini",
        label: "o1-mini",
        tagline: "legacy reasoning",
        input_per_mtok: 1.1,
        output_per_mtok: 4.4,
      },
      {
        id: "o1-pro",
        label: "o1-pro",
        tagline: "legacy reasoning, premium",
        input_per_mtok: 150,
        output_per_mtok: 600,
      },
    ],
  },
  {
    id: "openai_oauth",
    label: "ChatGPT (Plan)",
    auth: "oauth",
    models: [
      {
        id: "gpt-5.6-sol",
        label: "GPT-5.6 Sol",
        tagline: "flagship",
        recommended: true,
        input_per_mtok: 5,
        output_per_mtok: 30,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5.6",
        label: "GPT-5.6",
        tagline: "alias → Sol",
        input_per_mtok: 5,
        output_per_mtok: 30,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5.6-terra",
        label: "GPT-5.6 Terra",
        tagline: "balanced",
        input_per_mtok: 2.5,
        output_per_mtok: 15,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5.6-luna",
        label: "GPT-5.6 Luna",
        tagline: "fast, cheapest 5.6",
        input_per_mtok: 1,
        output_per_mtok: 6,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5.5",
        label: "GPT-5.5",
        tagline: "prior flagship",
        input_per_mtok: 5,
        output_per_mtok: 30,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5.5-pro",
        label: "GPT-5.5 Pro",
        tagline: "premium reasoning",
        input_per_mtok: 30,
        output_per_mtok: 180,
        note: "Pro only",
      },
      {
        id: "gpt-5.4",
        label: "GPT-5.4",
        tagline: "flagship",
        input_per_mtok: 2.5,
        output_per_mtok: 15,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5.4-mini",
        label: "GPT-5.4 mini",
        tagline: "fast",
        input_per_mtok: 0.75,
        output_per_mtok: 4.5,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5.4-nano",
        label: "GPT-5.4 nano",
        tagline: "cheapest",
        input_per_mtok: 0.2,
        output_per_mtok: 1.25,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5.4-pro",
        label: "GPT-5.4 Pro",
        tagline: "premium reasoning",
        input_per_mtok: 30,
        output_per_mtok: 180,
        note: "Pro only",
      },
      {
        id: "gpt-5.2",
        label: "GPT-5.2",
        tagline: "prior flagship",
        input_per_mtok: 1.75,
        output_per_mtok: 14,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5.2-pro",
        label: "GPT-5.2 Pro",
        tagline: "premium reasoning",
        input_per_mtok: 21,
        output_per_mtok: 168,
        note: "Pro only",
      },
      {
        id: "gpt-5.1",
        label: "GPT-5.1",
        tagline: "conversational",
        input_per_mtok: 1.25,
        output_per_mtok: 10,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5",
        label: "GPT-5",
        tagline: "original flagship",
        input_per_mtok: 1.25,
        output_per_mtok: 10,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5-pro",
        label: "GPT-5 Pro",
        tagline: "premium reasoning",
        input_per_mtok: 15,
        output_per_mtok: 120,
        note: "Pro only",
      },
      {
        id: "gpt-5-mini",
        label: "GPT-5 mini",
        tagline: "fast",
        input_per_mtok: 0.25,
        output_per_mtok: 2,
        note: "Plus / Pro",
      },
      {
        id: "gpt-5-nano",
        label: "GPT-5 nano",
        tagline: "cheapest",
        input_per_mtok: 0.05,
        output_per_mtok: 0.4,
        note: "Plus / Pro",
      },
      {
        id: "gpt-4.1",
        label: "GPT-4.1",
        tagline: "long context",
        input_per_mtok: 2,
        output_per_mtok: 8,
        note: "Plus / Pro",
      },
      {
        id: "gpt-4.1-mini",
        label: "GPT-4.1 mini",
        tagline: "fast",
        input_per_mtok: 0.4,
        output_per_mtok: 1.6,
        note: "Plus / Pro",
      },
      {
        id: "gpt-4o",
        label: "GPT-4o",
        tagline: "multimodal",
        input_per_mtok: 2.5,
        output_per_mtok: 10,
        note: "Plus / Pro",
      },
      {
        id: "gpt-4o-mini",
        label: "GPT-4o mini",
        tagline: "cheap",
        input_per_mtok: 0.15,
        output_per_mtok: 0.6,
        note: "Plus / Pro",
      },
      {
        id: "o4-mini",
        label: "o4-mini",
        tagline: "reasoning",
        input_per_mtok: 1.1,
        output_per_mtok: 4.4,
        note: "Plus / Pro",
      },
      {
        id: "o3",
        label: "o3",
        tagline: "reasoning",
        input_per_mtok: 2,
        output_per_mtok: 8,
        note: "Plus / Pro",
      },
      {
        id: "o3-pro",
        label: "o3-pro",
        tagline: "reasoning, deepest",
        input_per_mtok: 20,
        output_per_mtok: 80,
        note: "Pro only",
      },
    ],
  },
  {
    id: "google",
    label: "Gemini (API Key)",
    keyEnv: "GOOGLE_API_KEY",
    auth: "api_key",
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
  {
    id: "deepseek",
    label: "DeepSeek (API Key)",
    keyEnv: "DEEPSEEK_API_KEY",
    auth: "api_key",
    models: [
      {
        id: "deepseek-v4-pro",
        label: "DeepSeek V4 Pro",
        tagline: "flagship · 1M context",
        recommended: true,
        input_per_mtok: 1.32,
        output_per_mtok: 3.96,
        note: "peak rate; off-peak is half",
      },
      {
        id: "deepseek-v4-flash",
        label: "DeepSeek V4 Flash",
        tagline: "cheap · 1M context",
        input_per_mtok: 0.44,
        output_per_mtok: 1.32,
        note: "peak rate; off-peak is half",
      },
      {
        id: "deepseek-v4-flash-vision-exp",
        label: "DeepSeek V4 Flash Vision",
        tagline: "experimental · accepts images",
        input_per_mtok: 0.44,
        output_per_mtok: 1.32,
        note: "peak rate; off-peak is half",
      },
    ],
  },
];

/**
 * Look up a vendor. Returns null when the id is empty or unknown - it does NOT
 * fall back to a default.
 *
 * It used to return VENDORS[0], which is the Anthropic API-KEY vendor. So any
 * caller with no provider set silently rendered that vendor's model list:
 * Opus 4.8 and the rest of the 4.x line, none of which the Max plan serves,
 * all of which would bill an API key the boss has forbidden outright. A
 * picker that can offer a model he must never run is a landmine, and it was
 * armed by a `?? VENDORS[0]` written for convenience. Every caller now decides
 * what "I do not know the vendor" means for itself, out loud.
 */
export function findVendor(id: string | undefined | null): VendorEntry | null {
  return VENDORS.find((v) => v.id === id) ?? null;
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

/** Default model id for a vendor - the first entry tagged `recommended`. */
export function defaultModelFor(vendor: VendorEntry): string {
  return (vendor.models.find((m) => m.recommended) ?? vendor.models[0]).id;
}
