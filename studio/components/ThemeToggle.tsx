"use client";

import { useEffect, useState } from "react";
import { Monitor, Moon, Sun } from "lucide-react";
import { cn } from "@/lib/utils";

type Theme = "light" | "dark" | "system";
type Variant = "dropdown" | "cycle-row";

function applyTheme(theme: Theme) {
  const root = document.documentElement;
  if (theme === "system") {
    const prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    root.classList.toggle("dark", prefersDark);
  } else {
    root.classList.toggle("dark", theme === "dark");
  }
}

const OPTIONS: { value: Theme; label: string; Icon: typeof Sun }[] = [
  { value: "light", label: "Light Mode", Icon: Sun },
  { value: "dark", label: "Dark Mode", Icon: Moon },
  { value: "system", label: "Auto", Icon: Monitor },
];

// Two render modes:
//   - "dropdown" (desktop header): icon-only trigger with a radio dropdown.
//   - "cycle-row" (mobile drawer): full-width row, icon + label, tap to
//     advance to the next theme. Matches the nav row styling above it.
export function ThemeToggle({
  variant = "dropdown",
  className,
}: {
  variant?: Variant;
  className?: string;
}) {
  const [theme, setTheme] = useState<Theme>("system");
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    const stored = (localStorage.getItem("theme") as Theme | null) ?? "system";
    setTheme(stored);
    applyTheme(stored);
    setMounted(true);

    if (stored === "system") {
      const mq = window.matchMedia("(prefers-color-scheme: dark)");
      const handler = () => applyTheme("system");
      mq.addEventListener("change", handler);
      return () => mq.removeEventListener("change", handler);
    }
  }, []);

  function set(next: Theme) {
    setTheme(next);
    applyTheme(next);
    localStorage.setItem("theme", next);
  }

  // Default to the system icon during SSR / pre-mount so hydration matches
  // before localStorage is read.
  const activeOption = mounted
    ? OPTIONS.find((o) => o.value === theme) ?? OPTIONS[2]
    : OPTIONS[2];
  const ActiveIcon = activeOption.Icon;

  if (variant === "cycle-row") {
    function cycle() {
      const idx = OPTIONS.findIndex((o) => o.value === theme);
      const next = OPTIONS[(idx + 1) % OPTIONS.length];
      set(next.value);
    }
    return (
      <button
        type="button"
        onClick={cycle}
        aria-label={`Theme: ${activeOption.label}. Tap to change.`}
        className={cn(
          "flex min-h-12 w-full items-center justify-center gap-2 rounded-lg px-3 py-3 text-base text-muted-foreground transition-colors hover:bg-accent/60 hover:text-foreground",
          className,
        )}
      >
        <ActiveIcon className="size-5 shrink-0" aria-hidden />
        <span className="font-medium">{activeOption.label}</span>
      </button>
    );
  }

  // Desktop variant: same cycle behaviour as the mobile drawer row - one
  // tap advances Light → Dark → Auto → Light. No dropdown, no menu state
  // to hydrate. The aria-label always reflects the *current* theme so
  // screen readers + tooltips read the active state, not the action.
  function cycle() {
    const idx = OPTIONS.findIndex((o) => o.value === theme);
    const next = OPTIONS[(idx + 1) % OPTIONS.length];
    set(next.value);
  }
  return (
    <button
      type="button"
      onClick={cycle}
      aria-label={`Theme: ${activeOption.label}. Click to cycle.`}
      title={`Theme: ${activeOption.label}`}
      className={cn(
        "inline-flex size-9 items-center justify-center rounded-md text-foreground hover:bg-accent active:scale-95 lg:size-10",
        className,
      )}
    >
      <ActiveIcon className="size-4" aria-hidden />
    </button>
  );
}
