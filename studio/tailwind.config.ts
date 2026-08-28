import type { Config } from "tailwindcss";
import animate from "tailwindcss-animate";

const config: Config = {
  darkMode: ["class"],
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}", "./lib/**/*.{ts,tsx}"],
  theme: {
    container: {
      center: true,
      padding: "1rem",
      screens: { "2xl": "1400px" },
    },
    extend: {
      fontFamily: {
        // Majordomo type (docs/studio/MAJORDOMO.md §4). One family, three
        // registers:
        //   sans  - chrome: labels, nav, buttons, meta (13.5 / medium)
        //   voice - Jarvis's words + page/section titles (15.5 / 26)
        //   mono  - data: commands, diffs, ids, schemas, group labels
        //
        // `voice` is intentionally the same family as `sans` today. The
        // utility exists so the voice register can be retuned in ONE place
        // later without touching every consumer.
        //
        // `mono` is REAL mono now (Geist Mono). It used to alias the sans
        // family, which is why data columns never lined up; use it with
        // `tabular-nums` for numeric alignment.
        sans: ["var(--font-geist-sans)", "ui-sans-serif", "system-ui"],
        voice: ["var(--font-geist-sans)", "ui-sans-serif", "system-ui"],
        mono: ["var(--font-geist-mono)", "ui-monospace", "SFMono-Regular", "monospace"],
      },
      height: { dvh: "100dvh", svh: "100svh", lvh: "100lvh" },
      minHeight: { dvh: "100dvh", svh: "100svh" },
      colors: {
        border: "hsl(var(--border))",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
        background: "hsl(var(--background))",
        foreground: "hsl(var(--foreground))",
        // Majordomo §3. `hairline` is the fainter rule between rows -
        // `border-hairline`. `quiet` is the third ink level below
        // muted-foreground (meta, timestamps, resting glyphs) - `text-quiet`,
        // `bg-quiet`, and `border-quiet` all resolve from the one token.
        hairline: "hsl(var(--hairline))",
        quiet: "hsl(var(--foreground-quiet))",
        primary: {
          DEFAULT: "hsl(var(--primary))",
          foreground: "hsl(var(--primary-foreground))",
        },
        secondary: {
          DEFAULT: "hsl(var(--secondary))",
          foreground: "hsl(var(--secondary-foreground))",
        },
        destructive: {
          DEFAULT: "hsl(var(--destructive))",
          foreground: "hsl(var(--destructive-foreground))",
        },
        muted: {
          DEFAULT: "hsl(var(--muted))",
          foreground: "hsl(var(--muted-foreground))",
        },
        accent: {
          DEFAULT: "hsl(var(--accent))",
          foreground: "hsl(var(--accent-foreground))",
        },
        popover: {
          DEFAULT: "hsl(var(--popover))",
          foreground: "hsl(var(--popover-foreground))",
        },
        card: {
          DEFAULT: "hsl(var(--card))",
          foreground: "hsl(var(--card-foreground))",
        },
        info: { DEFAULT: "hsl(var(--info))", foreground: "hsl(var(--info-foreground))" },
        success: { DEFAULT: "hsl(var(--success))", foreground: "hsl(var(--success-foreground))" },
        warning: { DEFAULT: "hsl(var(--warning))", foreground: "hsl(var(--warning-foreground))" },
        danger: { DEFAULT: "hsl(var(--danger))", foreground: "hsl(var(--danger-foreground))" },
        brand: { DEFAULT: "hsl(var(--brand))", foreground: "hsl(var(--brand-foreground))" },
        tier: {
          working: "hsl(var(--tier-working))",
          episodic: "hsl(var(--tier-episodic))",
          semantic: "hsl(var(--tier-semantic))",
          procedural: "hsl(var(--tier-procedural))",
          stale: "hsl(var(--tier-stale))",
        },
      },
      borderRadius: {
        lg: "var(--radius)",
        md: "calc(var(--radius) - 2px)",
        sm: "calc(var(--radius) - 4px)",
      },
      keyframes: {
        "highlight-flash": {
          "0%": { backgroundColor: "hsl(var(--info) / 0.18)" },
          "100%": { backgroundColor: "transparent" },
        },
        "pulse-soft": {
          "0%, 100%": { opacity: "1" },
          "50%": { opacity: "0.55" },
        },
      },
      animation: {
        "highlight-flash": "highlight-flash 300ms ease-out",
        "pulse-soft": "pulse-soft 1.8s ease-in-out infinite",
      },
    },
  },
  plugins: [animate],
};

export default config;
