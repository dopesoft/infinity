"use client";

import * as React from "react";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { ArrowUp, Paperclip, Square, X, Mic, MicOff } from "lucide-react";
import { motion, AnimatePresence } from "framer-motion";
import { cn } from "@/lib/utils";
import { ContextMeter } from "@/components/ContextMeter";
import { VoiceOrb } from "@/components/VoiceOrb";
import { useVoice } from "@/lib/voice/use-voice";

/**
 * AI prompt box — adapted from 21st.dev/r/easemize/ai-prompt-box.
 *
 * Diverges from the source in three ways:
 *   1. Themed against Infinity tokens (--popover / --border / --foreground /
 *      --muted-foreground / --primary). Works in both light and dark, no
 *      hardcoded #1F2023.
 *   2. SSR-safe — removed the module-scope `document.createElement('style')`
 *      that broke Next.js hydration. Scrollbar styling lives in the className
 *      block instead.
 *   3. Replaced the Search / Think / Canvas toggles with a single
 *      <ModelChip> — click cycles through Sonnet 4.5 → Opus 4.7 → Haiku 4.5.
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

function ModelChip({
  modelId,
  vendorId,
  onCycle,
}: {
  modelId: string;
  vendorId?: string;
  onCycle: (nextModelId: string) => void;
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

  const cycle = () => {
    const idx = vendor.models.findIndex((m) => m.id === current.id);
    const next = vendor.models[(idx + 1) % vendor.models.length];
    onCycle(next.id);
  };

  return (
    <button
      type="button"
      onClick={cycle}
      title={`${vendor.label} · click to cycle`}
      className={cn(
        "inline-flex h-8 items-center rounded-full border px-3 text-xs font-medium",
        "border-border bg-muted/50 text-foreground/90 transition-colors",
        "hover:bg-muted hover:text-foreground active:scale-[0.98]",
      )}
    >
      <span className="mr-1.5 h-1.5 w-1.5 rounded-full bg-success" aria-hidden />
      {current.label}
    </button>
  );
}

// Re-export for legacy callers (Settings page imports the catalog directly).
export { VENDORS as MODEL_VENDORS };
export type { VendorId };

// voiceCaptionLabel maps the voice state machine to the small uppercase
// label that sits above the rolling caption text. Kept here (rather than
// inside the hook) so the same label surfaces consistently anywhere the
// composer is rendered.
function voiceCaptionLabel(v: {
  status: import("@/lib/voice/client").VoiceStatus;
  muted: boolean;
  toolName: string | null;
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
    case "assistant-speaking":
      return "Speaking…";
    case "tool-running":
      return v.toolName ? `Running ${v.toolName}` : "Running tool…";
    case "listening":
      return "Listening";
    case "error":
      return "Voice error";
    default:
      return "Voice";
  }
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
  /** Called with the next full model id when the user cycles the chip. */
  onModelChange?: (modelId: string) => void;
  /** Active session id — drives the context meter's per-session query. */
  sessionId?: string;
  /** Hide the attachment + voice affordances. Defaults to false. */
  minimal?: boolean;
  /** Optional slash-command hook (e.g. /new, /clear). Called before onSend. */
  onSlash?: (cmd: string) => boolean;
  /**
   * Cancel an in-flight agent turn. When provided AND `isLoading` is true,
   * the action button morphs into a stop button (red border, Square icon)
   * so the user can interrupt without waiting for the turn to finish.
   * Mid-turn typing is still allowed — pressing send while there's text
   * sends a steer instead of stopping.
   */
  onStop?: () => void;
  /** Voice mode hooks: voice's finalised user transcript becomes a
   *  conversation message; assistant deltas stream into the same
   *  conversation as a pending bubble until the response finalises.
   *  See useChat.addVoiceUserMessage / streamVoiceAssistantDelta. */
  onVoiceUserMessage?: (text: string) => void;
  onVoiceAssistantDelta?: (delta: string, isFinal: boolean) => void;
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
      sessionId,
      minimal = false,
      onSlash,
      onStop,
      onVoiceUserMessage,
      onVoiceAssistantDelta,
    } = props;

    const [internalValue, setInternalValue] = React.useState("");
    const value = controlledValue ?? internalValue;
    const setValue = (v: string) => {
      if (onValueChange) onValueChange(v);
      else setInternalValue(v);
    };

    const [internalModelId, setInternalModelId] = React.useState<string>(
      () => defaultModelFor(findVendor(vendorId ?? null)),
    );
    const modelId = controlledModelId ?? internalModelId;
    const cycleModel = (nextId: string) => {
      if (onModelChange) onModelChange(nextId);
      else setInternalModelId(nextId);
    };

    const [files, setFiles] = React.useState<File[]>([]);
    const [filePreviews, setFilePreviews] = React.useState<Record<string, string>>({});
    const [selectedImage, setSelectedImage] = React.useState<string | null>(null);

    // Voice mode (OpenAI Realtime over WebRTC). Owns its own state
    // machine; we just read .active to swap the composer body and
    // hand .start / .stop to the mic / end buttons. Transcripts flow
    // into the conversation stream via the host's callbacks — the
    // composer never renders caption text itself.
    const voice = useVoice(sessionId, {
      onUserMessage: onVoiceUserMessage,
      onAssistantDelta: onVoiceAssistantDelta,
    });
    const voiceActive = voice.active;

    const uploadRef = React.useRef<HTMLInputElement>(null);
    const textareaRef = React.useRef<HTMLTextAreaElement>(null);

    // Auto-resize
    React.useEffect(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.style.height = "auto";
      el.style.height = `${Math.min(el.scrollHeight, 240)}px`;
    }, [value]);

    const isImage = (f: File) => f.type.startsWith("image/");

    const processFile = (file: File) => {
      if (!isImage(file)) return;
      if (file.size > 10 * 1024 * 1024) return;
      setFiles([file]);
      const reader = new FileReader();
      reader.onload = (e) => setFilePreviews({ [file.name]: e.target?.result as string });
      reader.readAsDataURL(file);
    };

    const handlePaste = React.useCallback((e: ClipboardEvent) => {
      const items = e.clipboardData?.items;
      if (!items) return;
      for (let i = 0; i < items.length; i++) {
        if (items[i].type.startsWith("image/")) {
          const f = items[i].getAsFile();
          if (f) {
            e.preventDefault();
            processFile(f);
            return;
          }
        }
      }
    }, []);

    React.useEffect(() => {
      document.addEventListener("paste", handlePaste);
      return () => document.removeEventListener("paste", handlePaste);
    }, [handlePaste]);

    const handleSubmit = () => {
      const trimmed = value.trim();
      if (!trimmed && files.length === 0) return;
      if (onSlash && trimmed.startsWith("/") && onSlash(trimmed)) {
        setValue("");
        return;
      }
      onSend(trimmed, files);
      setValue("");
      setFiles([]);
      setFilePreviews({});
    };

    const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        handleSubmit();
      }
    };

    const handleRemoveFile = () => {
      setFiles([]);
      setFilePreviews({});
    };

    const handleDragOver = React.useCallback((e: React.DragEvent) => {
      e.preventDefault();
      e.stopPropagation();
    }, []);

    const handleDrop = React.useCallback((e: React.DragEvent) => {
      e.preventDefault();
      e.stopPropagation();
      const dropped = Array.from(e.dataTransfer.files).filter(isImage);
      if (dropped.length > 0) processFile(dropped[0]);
    }, []);

    const hasContent = value.trim() !== "" || files.length > 0;

    return (
      <TooltipProvider delayDuration={300}>
        <div
          ref={ref}
          onDragOver={handleDragOver}
          onDrop={handleDrop}
          className={cn(
            // `min-w-0 max-w-full` makes the composer respect its parent's
            // width even when descendants (textarea, action chips) have an
            // intrinsic content width larger than the viewport — without
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
          {files.length > 0 && !voiceActive && (
            <div className="flex flex-wrap gap-2 p-0 pb-1.5">
              {files.map((file, i) => (
                <div key={i} className="relative">
                  {filePreviews[file.name] && (
                    <div
                      role="button"
                      tabIndex={0}
                      onClick={() => setSelectedImage(filePreviews[file.name])}
                      className="h-14 w-14 cursor-pointer overflow-hidden rounded-xl border"
                    >
                      {/* eslint-disable-next-line @next/next/no-img-element */}
                      <img
                        src={filePreviews[file.name]}
                        alt={file.name}
                        className="h-full w-full object-cover"
                      />
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleRemoveFile();
                        }}
                        className="absolute right-0.5 top-0.5 rounded-full bg-black/70 p-0.5"
                        aria-label="Remove attachment"
                      >
                        <X className="h-3 w-3 text-white" />
                      </button>
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}

          {/* Textarea (hidden while voice is active or while the legacy
           * fake recorder runs). Voice replaces the textarea inline so the
           * conversation stream above stays fully visible — tool cards
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
              placeholder={placeholder}
              // Allow typing while a turn is in flight so the user can
              // queue a steer mid-stream. Recording still locks the
              // textarea since the input is audio at that point.
              disabled={disabled || voiceActive}
              autoCapitalize="sentences"
              autoCorrect="on"
              spellCheck
              inputMode="text"
            />
          </div>

          {voiceActive && (
            // Composer slot in voice mode: orb + one-line status only.
            // The actual transcript (what you said, what the agent
            // says back) streams into the conversation thread above —
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
                  <ModelChip modelId={modelId} vendorId={vendorId} onCycle={cycleModel} />
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
                          aria-label="Attach image"
                        >
                          <Paperclip className="h-4 w-4" />
                          <input
                            ref={uploadRef}
                            type="file"
                            accept="image/*"
                            className="hidden"
                            onChange={(e) => {
                              const f = e.target.files?.[0];
                              if (f) processFile(f);
                              if (e.target) e.target.value = "";
                            }}
                          />
                        </button>
                      </TooltipTrigger>
                      <TooltipContent side="top">Attach image</TooltipContent>
                    </Tooltip>
                  )}
                </>
              )}
            </div>

            {(() => {
              // Mode selection — single source of truth for icon, color,
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
                // mode === "voice" — kick off the realtime session.
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
