"use client";

import * as React from "react";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { ArrowUp, Paperclip, Square, X, Mic, MicOff, AlertCircle, RotateCcw, FileText, ChevronDown, ChevronRight, Check } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubTrigger,
  DropdownMenuSubContent,
} from "@/components/ui/dropdown-menu";
import { motion, AnimatePresence } from "framer-motion";
import { cn } from "@/lib/utils";
import { ContextMeter } from "@/components/ContextMeter";
import { VoiceOrb } from "@/components/VoiceOrb";
import { useVoice } from "@/lib/voice/use-voice";
import { emitVoiceActive, onWakeDetected } from "@/lib/voice/wake-bus";

/**
 * AI prompt box - adapted from 21st.dev/r/easemize/ai-prompt-box.
 *
 * Diverges from the source in three ways:
 *   1. Themed against Infinity tokens (--popover / --border / --foreground /
 *      --muted-foreground / --primary). Works in both light and dark, no
 *      hardcoded #1F2023.
 *   2. SSR-safe - removed the module-scope `document.createElement('style')`
 *      that broke Next.js hydration. Scrollbar styling lives in the className
 *      block instead.
 *   3. Replaced the Search / Think / Canvas toggles with a single
 *      <ModelChip> - click cycles through Sonnet 4.5 → Opus 4.7 → Haiku 4.5.
 *      Voice + image-paste UX stays (the agent loop accepts text only today;
 *      attachments are visual-only until we wire multimodal).
 */

// ── Textarea ──────────────────────────────────────────────────────────────
const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => (
  <textarea
    ref={ref}
    rows={1}
    // cols=1 cancels the HTML default of 20-character intrinsic min-width,
    // which on iOS Safari is what pushes the textarea (and therefore the
    // composer + viewport) wider than the parent when a long unbroken
    // token sits in the buffer. `w-full max-w-full` then constrains it.
    cols={1}
    wrap="soft"
    className={cn(
      // text-sm on desktop, the globals.css min-16px rule still kicks in on
      // pointer:coarse devices to prevent iOS Safari zoom-on-focus.
      // `min-w-0 max-w-full` together guarantee the textarea can never
      // exceed its parent's width even when its content has no break
      // opportunities (long URLs, hashes, paths).
      "flex w-full min-w-0 max-w-full resize-none rounded-md border-none bg-transparent px-3 py-2.5 text-sm text-foreground placeholder:text-muted-foreground [overflow-wrap:anywhere]",
      "focus-visible:outline-none focus-visible:ring-0 disabled:cursor-not-allowed disabled:opacity-50",
      "min-h-[44px] [&::-webkit-scrollbar]:w-1.5 [&::-webkit-scrollbar-track]:bg-transparent",
      "[&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-border",
      "hover:[&::-webkit-scrollbar-thumb]:bg-muted-foreground/50",
      className,
    )}
    {...props}
  />
));
Textarea.displayName = "PromptTextarea";

// ── Tooltip ────────────────────────────────────────────────────────────────
const TooltipProvider = TooltipPrimitive.Provider;
const Tooltip = TooltipPrimitive.Root;
const TooltipTrigger = TooltipPrimitive.Trigger;
const TooltipContent = React.forwardRef<
  React.ElementRef<typeof TooltipPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TooltipPrimitive.Content>
>(({ className, sideOffset = 4, ...props }, ref) => (
  <TooltipPrimitive.Content
    ref={ref}
    sideOffset={sideOffset}
    className={cn(
      "z-50 overflow-hidden rounded-md border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md",
      "data-[state=closed]:opacity-0 data-[state=open]:opacity-100 transition-opacity duration-100",
      className,
    )}
    {...props}
  />
));
TooltipContent.displayName = TooltipPrimitive.Content.displayName;

// ── Image preview dialog ──────────────────────────────────────────────────
const ImageDialog = DialogPrimitive.Root;
const ImageDialogPortal = DialogPrimitive.Portal;
const ImageDialogOverlay = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn(
      "fixed inset-0 z-50 bg-black/70 backdrop-blur-sm transition-opacity duration-100",
      "data-[state=closed]:opacity-0 data-[state=open]:opacity-100",
      className,
    )}
    {...props}
  />
));
ImageDialogOverlay.displayName = DialogPrimitive.Overlay.displayName;

const ImageDialogContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>
>(({ className, children, ...props }, ref) => (
  <ImageDialogPortal>
    <ImageDialogOverlay />
    <DialogPrimitive.Content
      ref={ref}
      className={cn(
        "fixed left-1/2 top-1/2 z-50 grid w-full max-w-[90vw] md:max-w-3xl -translate-x-1/2 -translate-y-1/2",
        "rounded-2xl border bg-popover p-0 shadow-xl transition-opacity duration-100",
        "data-[state=closed]:opacity-0 data-[state=open]:opacity-100",
        className,
      )}
      {...props}
    >
      {children}
      <DialogPrimitive.Close className="absolute right-3 top-3 z-10 rounded-full bg-muted/80 p-2 text-muted-foreground hover:bg-muted hover:text-foreground transition-colors">
        <X className="h-4 w-4" />
        <span className="sr-only">Close</span>
      </DialogPrimitive.Close>
    </DialogPrimitive.Content>
  </ImageDialogPortal>
));
ImageDialogContent.displayName = DialogPrimitive.Content.displayName;

// ── Model cycle ───────────────────────────────────────────────────────────
//
// The chip cycles through whichever vendor is wired (Anthropic, OpenAI,
// OpenAI-OAuth/ChatGPT, Google). Callers feed it the live model id; the
// chip looks up the id in the shared catalog, figures out the vendor, and
// cycles within that vendor's model list. When the id is unknown (e.g. a
// freshly entered custom model) the cycle starts at the vendor's default.
import {
  VENDORS,
  defaultModelFor,
  findVendor,
  resolveModelEntry,
  type VendorId,
} from "@/lib/models-catalog";

// Effort levels (steal C). Mirror the GPT reasoning.effort enum plus an "auto"
// option (let Jarvis size each turn). The chip shows the active level beside the
// model name ("Opus 4.8  High") and the dropdown's Effort submenu changes it.
const EFFORT_LEVELS = ["auto", "none", "low", "medium", "high", "xhigh"] as const;
const EFFORT_LABELS: Record<string, string> = {
  auto: "Auto",
  none: "None",
  low: "Low",
  medium: "Medium",
  high: "High",
  xhigh: "X-High",
};
const EFFORT_DESCRIPTIONS: Record<string, string> = {
  auto: "Jarvis sizes each turn",
  none: "Fastest — no extra thinking",
  low: "A little reasoning",
  medium: "Balanced reasoning",
  high: "Deep reasoning",
  xhigh: "Maximum reasoning",
};

// effortDisplay is the short label on the chip. A pinned level shows verbatim;
// on Auto it shows the level Jarvis actually chose for the in-flight turn (from
// the EventEffort frame), falling back to "Auto" before the first turn resolves.
function effortDisplay(effort?: string, applied?: string): string {
  const pin = (effort || "auto").toLowerCase();
  if (pin !== "auto") return EFFORT_LABELS[pin] ?? pin;
  if (applied) return EFFORT_LABELS[applied.toLowerCase()] ?? applied;
  return "Auto";
}

function ModelChip({
  modelId,
  vendorId,
  onSelect,
  effort,
  appliedEffort,
  onEffortChange,
}: {
  modelId: string;
  vendorId?: string;
  onSelect: (nextModelId: string) => void;
  effort?: string;
  appliedEffort?: string;
  onEffortChange?: (level: string) => void;
}) {
  // The active vendor wins over whatever vendor the model id happens to
  // belong to in the global catalog. Otherwise a stale model override
  // from a previous provider (e.g. "claude-haiku-…" carried over after
  // switching to openai_oauth) would display under the wrong vendor.
  const vendor = vendorId
    ? findVendor(vendorId)
    : (resolveModelEntry(modelId)?.vendor ?? findVendor(null));
  const current =
    vendor.models.find((m) => m.id === modelId) ??
    vendor.models.find((m) => m.id === defaultModelFor(vendor)) ??
    vendor.models[0];
  const pin = (effort || "auto").toLowerCase();
  const showEffort = Boolean(onEffortChange);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          title={`${vendor.label} · model & effort`}
          className={cn(
            "inline-flex h-8 items-center gap-1.5 rounded-full border px-3 text-xs font-medium",
            "border-border bg-muted/50 text-foreground/90 transition-colors",
            "hover:bg-muted hover:text-foreground active:scale-[0.98]",
          )}
        >
          <span className="h-1.5 w-1.5 rounded-full bg-success" aria-hidden />
          <span>{current.label}</span>
          {showEffort ? (
            <span className="text-muted-foreground">{effortDisplay(effort, appliedEffort)}</span>
          ) : null}
          <ChevronDown className="size-3 opacity-60" aria-hidden />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        {vendor.models.map((m) => (
          <DropdownMenuItem
            key={m.id}
            onSelect={() => onSelect(m.id)}
            className="flex-col items-start gap-0.5 py-2"
          >
            <span className="flex w-full items-center justify-between gap-2">
              <span className="font-medium">{m.label}</span>
              {m.id === current.id ? <Check className="size-4 shrink-0 text-info" /> : null}
            </span>
            {m.tagline ? (
              <span className="text-xs text-muted-foreground">{m.tagline}</span>
            ) : null}
          </DropdownMenuItem>
        ))}
        {showEffort ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <span>Effort</span>
                <span className="ml-auto text-muted-foreground">
                  {effortDisplay(effort, appliedEffort)}
                </span>
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className="w-56">
                <DropdownMenuRadioGroup value={pin} onValueChange={(v) => onEffortChange?.(v)}>
                  {EFFORT_LEVELS.map((lvl) => (
                    <DropdownMenuRadioItem
                      key={lvl}
                      value={lvl}
                      className="flex-col items-start gap-0.5 py-2 pl-7"
                    >
                      <span className="font-medium">{EFFORT_LABELS[lvl]}</span>
                      <span className="text-xs text-muted-foreground">{EFFORT_DESCRIPTIONS[lvl]}</span>
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          </>
        ) : null}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={() => {
            if (typeof window !== "undefined") window.location.href = "/settings";
          }}
        >
          <span>More models</span>
          <ChevronRight className="ml-auto size-4 opacity-60" />
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// Re-export for legacy callers (Settings page imports the catalog directly).
export { VENDORS as MODEL_VENDORS };
export type { VendorId };

// Single global draft slot — what the boss typed survives navigating away,
// switching tabs, iOS Safari backgrounding, or any remount of /live. Restored
// on mount and cleared on send. Single-user product, one slot is right.
// Mirrors the pattern in components/Composer.tsx.
const DRAFT_KEY = "infinity:prompt-box:draft";

function readStoredDraft(): string {
  if (typeof window === "undefined") return "";
  try {
    return window.localStorage.getItem(DRAFT_KEY) ?? "";
  } catch {
    return "";
  }
}

function writeStoredDraft(value: string) {
  if (typeof window === "undefined") return;
  try {
    if (value) window.localStorage.setItem(DRAFT_KEY, value);
    else window.localStorage.removeItem(DRAFT_KEY);
  } catch {
    /* private mode / quota — best-effort only */
  }
}

// voiceCaptionLabel maps the voice state machine to the small uppercase
// label that sits above the rolling caption text. Kept here (rather than
// inside the hook) so the same label surfaces consistently anywhere the
// composer is rendered.
function voiceCaptionLabel(v: {
  status: import("@/lib/voice/client").VoiceStatus;
  muted: boolean;
  error: string | null;
}): string {
  if (v.error) return `Voice error · ${v.error}`;
  if (v.muted) return "Muted";
  switch (v.status) {
    case "requesting-permission":
      return "Mic permission…";
    case "connecting":
      return "Connecting…";
    case "user-speaking":
      return "Listening…";
    // assistant-speaking / tool-running come from the demoted realtime model
    // and no longer fire (cognition + speech moved off it); keep the cases so
    // the union stays exhaustive without a tool name.
    case "assistant-speaking":
      return "Speaking…";
    case "tool-running":
      return "Working…";
    case "listening":
      return "Listening";
    case "error":
      return "Voice error";
    default:
      return "Voice";
  }
}

type ComposerFile = {
  file: File;
  previewUrl?: string;
};

function isImageFile(file: File): boolean {
  return file.type.startsWith("image/");
}

function fileKey(file: File): string {
  return `${file.name}:${file.size}:${file.lastModified}`;
}

function trimFiles(files: File[]): File[] {
  const seen = new Set<string>();
  const out: File[] = [];
  for (const file of files) {
    const key = fileKey(file);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(file);
  }
  return out.slice(0, 8);
}

// ── PromptInputBox ────────────────────────────────────────────────────────
export interface PromptInputBoxProps {
  onSend: (message: string, files?: File[]) => void;
  isLoading?: boolean;
  disabled?: boolean;
  placeholder?: string;
  className?: string;
  value?: string;
  onValueChange?: (v: string) => void;
  /** Active model id (e.g. "claude-opus-4-7", "gpt-5"). When omitted the chip
   *  falls back to the active vendor's default. */
  modelId?: string;
  /** Active vendor id ("anthropic" / "openai" / "openai_oauth" / "google").
   *  When omitted the chip infers it from the model id, falling back to
   *  the first vendor in the catalog. */
  vendorId?: string;
  /** Called with the next full model id when the user picks one in the chip. */
  onModelChange?: (modelId: string) => void;
  /** Per-turn thinking-effort pin (steal C): "auto" | none|low|medium|high|xhigh.
   *  "auto"/undefined lets Jarvis size each turn. */
  effort?: string;
  /** The level Jarvis auto-chose for the in-flight turn (from the EventEffort
   *  frame). Shown on the chip when effort is "auto" so the boss sees the level. */
  appliedEffort?: string;
  /** Called when the boss changes the effort level in the chip's Effort submenu.
   *  Omit to hide the effort UI entirely (chip reverts to model-only). */
  onEffortChange?: (level: string) => void;
  /** Active session id - drives the context meter's per-session query. */
  sessionId?: string;
  /** Hide the attachment + voice affordances. Defaults to false. */
  minimal?: boolean;
  /** Optional slash-command hook (e.g. /new, /clear). Called before onSend. */
  onSlash?: (cmd: string) => boolean;
  /**
   * Cancel an in-flight agent turn. When provided AND `isLoading` is true,
   * the action button morphs into a stop button (red border, Square icon)
   * so the user can interrupt without waiting for the turn to finish.
   * Mid-turn typing is still allowed - pressing send while there's text
   * sends a steer instead of stopping.
   */
  onStop?: () => void;
  /** Voice mode: a finalized voice transcript is sent to the real brain via
   *  this callback (host wires it to chat.send(text, {voice:true})). The reply
   *  streams back as normal chat deltas (rendered like any turn) plus spoken
   *  TTS audio (handled inside useVoice). */
  onVoiceSend?: (text: string) => void;
}

export const PromptInputBox = React.forwardRef<HTMLDivElement, PromptInputBoxProps>(
  (props, ref) => {
    const {
      onSend,
      isLoading = false,
      disabled = false,
      placeholder = "ask me anything…",
      className,
      value: controlledValue,
      onValueChange,
      modelId: controlledModelId,
      vendorId,
      onModelChange,
      effort,
      appliedEffort,
      onEffortChange,
      sessionId,
      minimal = false,
      onSlash,
      onStop,
      onVoiceSend,
    } = props;

    // Empty on first server render; hydrated from localStorage in the mount
    // effect below so SSR markup stays deterministic. Only the uncontrolled
    // path persists — when the parent passes `value`, it owns the source of
    // truth.
    const [internalValue, setInternalValue] = React.useState("");
    const isControlled = controlledValue !== undefined;
    const value = controlledValue ?? internalValue;
    const setValue = (v: string) => {
      if (onValueChange) onValueChange(v);
      else setInternalValue(v);
    };

    // Hydrate the in-progress draft from localStorage after mount so SSR
    // matches and a remount (route change, tab switch, iOS Safari
    // backgrounding+restore) restores what the boss was typing.
    React.useEffect(() => {
      if (isControlled) return;
      const stored = readStoredDraft();
      if (stored) setInternalValue(stored);
    }, [isControlled]);

    // Mirror every keystroke into localStorage. Cheap (single key, small
    // string) and iOS Safari's kill-tab path doesn't fire beforeunload
    // reliably, so we can't batch on unload.
    React.useEffect(() => {
      if (isControlled) return;
      writeStoredDraft(internalValue);
    }, [internalValue, isControlled]);

    const [internalModelId, setInternalModelId] = React.useState<string>(
      () => defaultModelFor(findVendor(vendorId ?? null)),
    );
    const modelId = controlledModelId ?? internalModelId;
    const cycleModel = (nextId: string) => {
      if (onModelChange) onModelChange(nextId);
      else setInternalModelId(nextId);
    };

    const [composerFiles, setComposerFiles] = React.useState<ComposerFile[]>([]);
    const [selectedImage, setSelectedImage] = React.useState<string | null>(null);

    // Voice mode (OpenAI Realtime over WebRTC). Owns its own state
    // machine; we just read .active to swap the composer body and
    // hand .start / .stop to the mic / end buttons. Transcripts flow
    // into the conversation stream via the host's callbacks - the
    // composer never renders caption text itself.
    const voice = useVoice(sessionId, {
      onSend: onVoiceSend,
    });
    const voiceActive = voice.active;

    // Wake-word contract with the header's WakeNavButton (which owns the
    // "hey Jarvis" engine): announce when this composer's voice session
    // holds the mic so the wake listener suspends, and start the session
    // when the wake word fires while we're mounted.
    const voiceStartRef = React.useRef(voice.start);
    voiceStartRef.current = voice.start;
    const voiceActiveRef = React.useRef(voiceActive);
    voiceActiveRef.current = voiceActive;
    React.useEffect(() => {
      emitVoiceActive(voiceActive);
      // Release the suspension if this composer unmounts mid-session.
      return () => emitVoiceActive(false);
    }, [voiceActive]);
    React.useEffect(() => {
      if (minimal) return;
      return onWakeDetected(() => {
        if (voiceActiveRef.current) return;
        void voiceStartRef.current();
      });
    }, [minimal]);

    // Deep-link entry: /live?voice=1 (the "Talk to Jarvis" PWA shortcut and
    // the Siri Shortcut both land here) auto-starts listening. If the
    // browser blocks mic acquisition without a fresh gesture (iOS cold
    // start), useVoice surfaces its normal error card with a one-tap Retry -
    // the deep link degrades to one tap instead of failing silently.
    const autoVoiceFired = React.useRef(false);
    React.useEffect(() => {
      if (autoVoiceFired.current || minimal) return;
      try {
        const params = new URLSearchParams(window.location.search);
        if (params.get("voice") === "1") {
          autoVoiceFired.current = true;
          void voiceStartRef.current();
        }
      } catch {
        // No window/search - SSR guard; the effect reruns client-side.
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [minimal]);

    const uploadRef = React.useRef<HTMLInputElement>(null);
    const textareaRef = React.useRef<HTMLTextAreaElement>(null);

    // Auto-resize
    React.useEffect(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.style.height = "auto";
      el.style.height = `${Math.min(el.scrollHeight, 240)}px`;
    }, [value]);

    React.useEffect(() => {
      return () => {
        for (const item of composerFiles) {
          if (item.previewUrl?.startsWith("blob:")) URL.revokeObjectURL(item.previewUrl);
        }
      };
    }, [composerFiles]);

    const addFiles = React.useCallback((incoming: File[]) => {
      const nextFiles = trimFiles([...composerFiles.map((item) => item.file), ...incoming]);
      setComposerFiles((prev) => {
        const nextKeys = new Set(nextFiles.map(fileKey));
        for (const item of prev) {
          if (!nextKeys.has(fileKey(item.file)) && item.previewUrl?.startsWith("blob:")) {
            URL.revokeObjectURL(item.previewUrl);
          }
        }
        return nextFiles.map((file) => {
          const existing = prev.find((item) => fileKey(item.file) === fileKey(file));
          if (existing) return existing;
          return {
            file,
            previewUrl: isImageFile(file) ? URL.createObjectURL(file) : undefined,
          };
        });
      });
    }, [composerFiles]);

    const handlePaste = React.useCallback((e: ClipboardEvent) => {
      const list = Array.from(e.clipboardData?.files ?? []);
      if (list.length === 0) return;
      e.preventDefault();
      addFiles(list);
    }, [addFiles]);

    React.useEffect(() => {
      document.addEventListener("paste", handlePaste);
      return () => document.removeEventListener("paste", handlePaste);
    }, [handlePaste]);

    const clearFiles = React.useCallback(() => {
      setComposerFiles((prev) => {
        for (const item of prev) {
          if (item.previewUrl?.startsWith("blob:")) URL.revokeObjectURL(item.previewUrl);
        }
        return [];
      });
    }, []);

    const handleSubmit = () => {
      const trimmed = value.trim();
      const files = composerFiles.map((item) => item.file);
      if (!trimmed && files.length === 0) return;
      if (onSlash && trimmed.startsWith("/") && onSlash(trimmed)) {
        setValue("");
        clearFiles();
        return;
      }
      onSend(trimmed, files);
      setValue("");
      clearFiles();
    };

    const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        handleSubmit();
      }
    };

    const handleRemoveFile = React.useCallback((target: File) => {
      setComposerFiles((prev) => {
        const removedKey = fileKey(target);
        return prev.filter((item) => {
          const shouldKeep = fileKey(item.file) !== removedKey;
          if (!shouldKeep && item.previewUrl?.startsWith("blob:")) {
            URL.revokeObjectURL(item.previewUrl);
          }
          return shouldKeep;
        });
      });
    }, []);

    const handleDragOver = React.useCallback((e: React.DragEvent) => {
      e.preventDefault();
      e.stopPropagation();
    }, []);

    const handleDrop = React.useCallback((e: React.DragEvent) => {
      e.preventDefault();
      e.stopPropagation();
      const dropped = Array.from(e.dataTransfer.files ?? []);
      if (dropped.length > 0) addFiles(dropped);
    }, [addFiles]);

    const hasContent = value.trim() !== "" || composerFiles.length > 0;

    return (
      <TooltipProvider delayDuration={300}>
        <div
          ref={ref}
          onDragOver={handleDragOver}
          onDrop={handleDrop}
          className={cn(
            // `min-w-0 max-w-full` makes the composer respect its parent's
            // width even when descendants (textarea, action chips) have an
            // intrinsic content width larger than the viewport - without
            // these, iOS Safari leaks the overflow up to the page.
            "min-w-0 max-w-full rounded-3xl border bg-popover p-2 transition-colors duration-200",
            "shadow-[0_8px_30px_rgba(0,0,0,0.04)] dark:shadow-[0_8px_30px_rgba(0,0,0,0.32)]",
            voiceActive && "border-info/40",
            isLoading && "border-info/40",
            (disabled || isLoading) && "opacity-95",
            className,
          )}
        >
          {/* File previews */}
          {composerFiles.length > 0 && !voiceActive && (
            <div className="flex flex-wrap gap-2 p-0 pb-1.5">
              {composerFiles.map(({ file, previewUrl }) => {
                const image = !!previewUrl;
                return (
                  <div key={fileKey(file)} className="relative">
                    {image ? (
                      <div
                        role="button"
                        tabIndex={0}
                        onClick={() => setSelectedImage(previewUrl)}
                        className="h-14 w-14 cursor-pointer overflow-hidden rounded-xl border"
                      >
                        {/* eslint-disable-next-line @next/next/no-img-element */}
                        <img
                          src={previewUrl}
                          alt={file.name}
                          className="h-full w-full object-cover"
                        />
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation();
                            handleRemoveFile(file);
                          }}
                          className="absolute right-0.5 top-0.5 rounded-full bg-black/70 p-0.5"
                          aria-label={`Remove ${file.name}`}
                        >
                          <X className="h-3 w-3 text-white" />
                        </button>
                      </div>
                    ) : (
                      <div className="flex min-w-[180px] max-w-[240px] items-center gap-2 rounded-xl border bg-background/70 px-3 py-2 text-xs text-foreground">
                        <FileText className="size-4 shrink-0 text-muted-foreground" />
                        <div className="min-w-0 flex-1">
                          <div className="truncate font-medium">{file.name}</div>
                          <div className="truncate text-[10px] text-muted-foreground">
                            {[file.type || undefined, file.size > 0 ? `${Math.round(file.size / 1024) || 1} KB` : undefined]
                              .filter(Boolean)
                              .join(" · ")}
                          </div>
                        </div>
                        <button
                          type="button"
                          onClick={() => handleRemoveFile(file)}
                          className="rounded-full p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                          aria-label={`Remove ${file.name}`}
                        >
                          <X className="h-3 w-3" />
                        </button>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}

          {/* Voice error banner. The voice state machine drops out of
           * `active` the instant it hits "error", so without this the
           * whole voice slot below unmounts and the boss bounces back to
           * the text box with zero explanation. Render the failure
           * persistently here (independent of voiceActive) with the real
           * cause + a one-tap retry, so "mic granted then nothing"
           * becomes a legible "ICE failed / SDP 4xx / permission denied"
           * the boss can act on. */}
          {voice.status === "error" && voice.error && (
            <div className="mb-1.5 flex min-w-0 max-w-full items-start gap-2 rounded-2xl border border-danger/40 bg-danger/10 px-3 py-2">
              <AlertCircle className="mt-0.5 size-4 shrink-0 text-danger" aria-hidden />
              <div className="min-w-0 flex-1">
                <p className="text-xs font-semibold text-danger">Voice failed to connect</p>
                <p className="mt-0.5 break-words text-xs text-muted-foreground">{voice.error}</p>
              </div>
              <button
                type="button"
                onClick={() => void voice.start()}
                className="inline-flex h-7 shrink-0 items-center gap-1 rounded-full px-2.5 text-xs font-medium text-danger transition-colors hover:bg-danger/15"
                aria-label="Retry voice connection"
              >
                <RotateCcw className="size-3.5" />
                Retry
              </button>
              <button
                type="button"
                onClick={() => voice.stop()}
                className="inline-flex size-7 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                aria-label="Dismiss voice error"
              >
                <X className="size-3.5" />
              </button>
            </div>
          )}

          {/* Textarea (hidden while voice is active or while the legacy
           * fake recorder runs). Voice replaces the textarea inline so the
           * conversation stream above stays fully visible - tool cards
           * and any agent work keeps streaming as the boss talks. */}
          <div
            className={cn(
              "min-w-0 max-w-full transition-all duration-300",
              voiceActive ? "h-0 overflow-hidden opacity-0" : "opacity-100",
            )}
          >
            <Textarea
              ref={textareaRef}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={disabled ? "reconnecting…" : placeholder}
              // The textarea is NEVER disabled by the WS connection state.
              // Hot-reloads, deploys, brief proxy blips, even iOS Safari
              // killing the socket on backgrounding — all of those flip
              // `disabled` true for a few seconds, and freezing the input
              // out from under the boss mid-thought is the wrong call.
              // Only voice mode locks it (mic IS the input). The Send
              // button below still respects `disabled` so we won't
              // optimistically fire on a dead socket — useChat surfaces
              // a clear error if you do hit Send during a blip.
              disabled={voiceActive}
              autoCapitalize="sentences"
              autoCorrect="on"
              spellCheck
              inputMode="text"
            />
          </div>

          {voiceActive && (
            // Composer slot in voice mode: orb + one-line status only.
            // The actual transcript (what you said, what the agent
            // says back) streams into the conversation thread above -
            // see useChat.addVoiceUserMessage / streamVoiceAssistantDelta.
            // That keeps long, multi-sentence replies readable instead
            // of getting chopped off in a one-line caption.
            //
            // Center the orb + label as one intrinsic group so the
            // status text does not float away from the pulse icon.
            <div className="flex min-h-[44px] w-full min-w-0 items-center justify-center px-3 py-2">
              <div className="flex min-w-0 max-w-full items-center justify-center gap-3">
                <span className="shrink-0">
                  <VoiceOrb status={voice.status} level={voice.level} />
                </span>
                <p
                  className="min-w-0 truncate text-sm font-medium text-muted-foreground"
                  aria-live="polite"
                >
                  {voiceCaptionLabel(voice)}
                </p>
              </div>
            </div>
          )}

          {/* Action row. In voice mode we collapse to a centered cluster
           * so Mute + End sit together with a small gap; in text mode
           * the model chip / context meter / paperclip occupy the left
           * while send/stop pins to the right edge. */}
          <div
            className={cn(
              "flex w-full min-w-0 items-center pt-1.5",
              voiceActive ? "justify-center gap-6" : "justify-between gap-2",
            )}
          >
            <div className={cn("flex min-w-0 items-center gap-1.5", voiceActive && "shrink-0")}>
              {voiceActive ? (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <button
                      type="button"
                      onClick={() => voice.setMuted(!voice.muted)}
                      className={cn(
                        "inline-flex size-11 shrink-0 items-center justify-center rounded-full",
                        "transition-colors",
                        voice.muted
                          ? "bg-muted text-danger"
                          : "text-muted-foreground hover:bg-muted hover:text-foreground",
                      )}
                      aria-label={voice.muted ? "Unmute mic" : "Mute mic"}
                      aria-pressed={voice.muted}
                    >
                      {voice.muted ? <MicOff className="h-4 w-4" /> : <Mic className="h-4 w-4" />}
                    </button>
                  </TooltipTrigger>
                  <TooltipContent side="top">
                    {voice.muted ? "Unmute" : "Mute"}
                  </TooltipContent>
                </Tooltip>
              ) : (
                <>
                  <ModelChip
                    modelId={modelId}
                    vendorId={vendorId}
                    onSelect={cycleModel}
                    effort={effort}
                    appliedEffort={appliedEffort}
                    onEffortChange={onEffortChange}
                  />
                  <ContextMeter sessionId={sessionId} />

  
                  {!minimal && (
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <button
                          type="button"
                          onClick={() => uploadRef.current?.click()}
                          disabled={disabled || voiceActive}
                          className={cn(
                            "inline-flex h-8 w-8 items-center justify-center rounded-full text-muted-foreground",
                            "transition-colors hover:bg-muted hover:text-foreground",
                            "disabled:cursor-not-allowed disabled:opacity-50",
                          )}
                          aria-label="Attach files"
                        >
                          <Paperclip className="h-4 w-4" />
                          <input
                            ref={uploadRef}
                            type="file"
                            accept="image/*,.pdf,.txt,.md,.markdown,.json,.csv,.ts,.tsx,.js,.jsx,.py,.go,.rs,.java,.c,.cc,.cpp,.h,.hpp,.css,.html,.xml,.yaml,.yml,.toml,.ini,.sql"
                            multiple
                            className="hidden"
                            onChange={(e) => {
                              const incoming = Array.from(e.target.files ?? []);
                              if (incoming.length > 0) addFiles(incoming);
                              if (e.target) e.target.value = "";
                            }}
                          />
                        </button>
                      </TooltipTrigger>
                      <TooltipContent side="top">Attach files</TooltipContent>
                    </Tooltip>
                  )}
                </>
              )}
            </div>

            {(() => {
              // Mode selection - single source of truth for icon, color,
              // aria, click behavior, and tooltip. Keeping the matrix in
              // one place makes the stop-vs-steer-vs-send transitions
              // easy to reason about.
              //
              //   - voiceActive                              → "end-voice"
              //   - isLoading + empty input + onStop wired → "stop"
              //   - isLoading + content typed                → "steer" (send mid-turn)
              //   - hasContent                               → "send"
              //   - else                                     → "voice" (or noop in minimal)
              const canStop = isLoading && !!onStop;
              const mode:
                | "stop"
                | "steer"
                | "end-voice"
                | "send"
                | "voice" =
                voiceActive
                  ? "end-voice"
                  : canStop && !hasContent
                    ? "stop"
                    : isLoading && hasContent
                      ? "steer"
                      : hasContent
                        ? "send"
                        : "voice";

              const onClick = () => {
                if (mode === "end-voice") {
                  voice.stop();
                  return;
                }
                if (mode === "stop") {
                  onStop?.();
                  return;
                }
                if (mode === "send" || mode === "steer") {
                  handleSubmit();
                  return;
                }
                // mode === "voice" - kick off the realtime session.
                if (!minimal) void voice.start();
              };

              const disable =
                disabled ||
                // No content + loading + no stop callback → nothing to do.
                (isLoading && !hasContent && !onStop) ||
                // Minimal layout has no voice affordance; gray out idle state.
                (mode === "voice" && minimal);

              const aria =
                mode === "stop"
                  ? "Stop generation"
                  : mode === "steer"
                    ? "Send steer to running turn"
                    : mode === "end-voice"
                      ? "End voice session"
                      : mode === "send"
                        ? "Send message"
                        : "Voice message";

              const tooltip =
                mode === "stop"
                  ? "Stop"
                  : mode === "steer"
                    ? "Steer (mid-turn)"
                    : mode === "end-voice"
                      ? "End voice"
                      : mode === "send"
                        ? "Send"
                        : "Voice";

              return (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <button
                      type="button"
                      onClick={onClick}
                      disabled={disable}
                      className={cn(
                        "inline-flex shrink-0 items-center justify-center rounded-full transition-all duration-200",
                        voiceActive ? "size-11" : "h-9 w-9",
                        mode === "stop" &&
                          "bg-transparent text-danger hover:bg-danger/10",
                        // Same treatment as stop: transparent button, just
                        // the icon carries the color. No filled background.
                        (mode === "send" || mode === "steer") &&
                          "bg-transparent text-foreground hover:bg-muted",
                        mode === "end-voice" &&
                          "bg-transparent text-danger hover:bg-danger/10",
                        mode === "voice" && !minimal &&
                          "bg-transparent text-muted-foreground hover:bg-muted hover:text-foreground",
                        mode === "voice" && minimal &&
                          "cursor-not-allowed bg-muted text-muted-foreground",
                        "disabled:cursor-not-allowed disabled:opacity-50",
                      )}
                      aria-label={aria}
                    >
                      {mode === "stop" ? (
                        <Square className="h-4 w-4 animate-pulse" />
                      ) : mode === "end-voice" ? (
                        <Square className="h-4 w-4" />
                      ) : mode === "send" || mode === "steer" ? (
                        <ArrowUp className="h-4 w-4" />
                      ) : (
                        <Mic className="h-4 w-4" />
                      )}
                    </button>
                  </TooltipTrigger>
                  <TooltipContent side="top">{tooltip}</TooltipContent>
                </Tooltip>
              );
            })()}
          </div>
        </div>

        <ImageDialog open={!!selectedImage} onOpenChange={(open) => !open && setSelectedImage(null)}>
          {selectedImage && (
            <ImageDialogContent className="overflow-hidden">
              <DialogPrimitive.Title className="sr-only">Image preview</DialogPrimitive.Title>
              <motion.div
                initial={{ opacity: 0, scale: 0.96 }}
                animate={{ opacity: 1, scale: 1 }}
                exit={{ opacity: 0, scale: 0.96 }}
                transition={{ duration: 0.18, ease: "easeOut" }}
                className="overflow-hidden rounded-2xl"
              >
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img
                  src={selectedImage}
                  alt="Attachment preview"
                  className="max-h-[80vh] w-full object-contain"
                />
              </motion.div>
            </ImageDialogContent>
          )}
        </ImageDialog>

        <AnimatePresence />
      </TooltipProvider>
    );
  },
);
PromptInputBox.displayName = "PromptInputBox";
