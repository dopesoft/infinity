"use client";

import * as React from "react";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { ArrowUp, Paperclip, Square, X, StopCircle, Mic } from "lucide-react";
import { motion, AnimatePresence } from "framer-motion";
import { cn } from "@/lib/utils";

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
    className={cn(
      // text-sm on desktop, the globals.css min-16px rule still kicks in on
      // pointer:coarse devices to prevent iOS Safari zoom-on-focus.
      "flex w-full resize-none rounded-md border-none bg-transparent px-3 py-2.5 text-sm text-foreground placeholder:text-muted-foreground",
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

// ── VoiceRecorder ─────────────────────────────────────────────────────────
const VoiceRecorder: React.FC<{
  isRecording: boolean;
  onStartRecording: () => void;
  onStopRecording: (duration: number) => void;
  visualizerBars?: number;
}> = ({ isRecording, onStartRecording, onStopRecording, visualizerBars = 32 }) => {
  const [time, setTime] = React.useState(0);
  const timerRef = React.useRef<NodeJS.Timeout | null>(null);

  React.useEffect(() => {
    if (isRecording) {
      onStartRecording();
      timerRef.current = setInterval(() => setTime((t) => t + 1), 1000);
    } else {
      if (timerRef.current) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
      onStopRecording(time);
      setTime(0);
    }
    return () => {
      if (timerRef.current) clearInterval(timerRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isRecording]);

  const formatTime = (s: number) =>
    `${Math.floor(s / 60).toString().padStart(2, "0")}:${(s % 60).toString().padStart(2, "0")}`;

  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center w-full transition-all duration-300 py-3",
        isRecording ? "opacity-100" : "h-0 opacity-0 overflow-hidden",
      )}
    >
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-danger animate-pulse" />
        <span className="font-mono text-sm text-foreground/80">{formatTime(time)}</span>
      </div>
      <div className="flex h-8 w-full items-center justify-center gap-0.5 px-4">
        {Array.from({ length: visualizerBars }).map((_, i) => (
          <div
            key={i}
            className="w-0.5 rounded-full bg-foreground/40 animate-pulse"
            style={{
              height: `${Math.max(15, Math.random() * 100)}%`,
              animationDelay: `${i * 0.05}s`,
              animationDuration: `${0.5 + Math.random() * 0.5}s`,
            }}
          />
        ))}
      </div>
    </div>
  );
};

// ── Model cycle ───────────────────────────────────────────────────────────
export type ModelKey = "sonnet-4-5" | "opus-4-7" | "haiku-4-5";

export const MODELS: { key: ModelKey; label: string; id: string }[] = [
  { key: "sonnet-4-5", label: "Sonnet 4.5", id: "claude-sonnet-4-5" },
  { key: "opus-4-7", label: "Opus 4.7", id: "claude-opus-4-7" },
  { key: "haiku-4-5", label: "Haiku 4.5", id: "claude-haiku-4-5-20251001" },
];

function ModelChip({ value, onCycle }: { value: ModelKey; onCycle: () => void }) {
  const current = MODELS.find((m) => m.key === value) ?? MODELS[0];
  return (
    <button
      type="button"
      onClick={onCycle}
      title="Click to cycle model"
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

// ── PromptInputBox ────────────────────────────────────────────────────────
export interface PromptInputBoxProps {
  onSend: (message: string, files?: File[]) => void;
  isLoading?: boolean;
  disabled?: boolean;
  placeholder?: string;
  className?: string;
  value?: string;
  onValueChange?: (v: string) => void;
  model?: ModelKey;
  onModelChange?: (m: ModelKey) => void;
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
      model: controlledModel,
      onModelChange,
      minimal = false,
      onSlash,
      onStop,
    } = props;

    const [internalValue, setInternalValue] = React.useState("");
    const value = controlledValue ?? internalValue;
    const setValue = (v: string) => {
      if (onValueChange) onValueChange(v);
      else setInternalValue(v);
    };

    const [internalModel, setInternalModel] = React.useState<ModelKey>("sonnet-4-5");
    const model = controlledModel ?? internalModel;
    const cycleModel = () => {
      const idx = MODELS.findIndex((m) => m.key === model);
      const next = MODELS[(idx + 1) % MODELS.length].key;
      if (onModelChange) onModelChange(next);
      else setInternalModel(next);
    };

    const [files, setFiles] = React.useState<File[]>([]);
    const [filePreviews, setFilePreviews] = React.useState<Record<string, string>>({});
    const [selectedImage, setSelectedImage] = React.useState<string | null>(null);
    const [isRecording, setIsRecording] = React.useState(false);

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
            "rounded-3xl border bg-popover p-2 transition-colors duration-200",
            "shadow-[0_8px_30px_rgba(0,0,0,0.04)] dark:shadow-[0_8px_30px_rgba(0,0,0,0.32)]",
            isRecording && "border-danger/70",
            isLoading && "border-info/40",
            (disabled || isLoading) && "opacity-95",
            className,
          )}
        >
          {/* File previews */}
          {files.length > 0 && !isRecording && (
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

          {/* Textarea (hidden while recording) */}
          <div
            className={cn(
              "transition-all duration-300",
              isRecording ? "h-0 overflow-hidden opacity-0" : "opacity-100",
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
              disabled={disabled || isRecording}
              autoCapitalize="sentences"
              autoCorrect="on"
              spellCheck
              inputMode="text"
            />
          </div>

          {isRecording && (
            <VoiceRecorder
              isRecording={isRecording}
              onStartRecording={() => undefined}
              onStopRecording={(d) => {
                setIsRecording(false);
                if (d > 0) onSend(`[Voice message — ${d}s]`, []);
              }}
            />
          )}

          {/* Action row */}
          <div className="flex items-center justify-between gap-2 pt-1.5">
            <div
              className={cn(
                "flex items-center gap-1.5 transition-opacity duration-300",
                isRecording ? "invisible h-0 opacity-0" : "visible opacity-100",
              )}
            >
              <ModelChip value={model} onCycle={cycleModel} />

              {!minimal && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <button
                      type="button"
                      onClick={() => uploadRef.current?.click()}
                      disabled={disabled || isRecording}
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
            </div>

            {(() => {
              // Mode selection — single source of truth for icon, color,
              // aria, click behavior, and tooltip. Keeping the matrix in
              // one place makes the stop-vs-steer-vs-send transitions
              // easy to reason about.
              //
              //   - isLoading + empty input + onStop wired → "stop"
              //   - isLoading + content typed                → "steer" (send mid-turn)
              //   - isRecording                              → "stop-recording"
              //   - hasContent                               → "send"
              //   - else                                     → "voice" (or noop in minimal)
              const canStop = isLoading && !!onStop;
              const mode: "stop" | "steer" | "stop-recording" | "send" | "voice" =
                canStop && !hasContent
                  ? "stop"
                  : isLoading && hasContent
                    ? "steer"
                    : isRecording
                      ? "stop-recording"
                      : hasContent
                        ? "send"
                        : "voice";

              const onClick = () => {
                if (mode === "stop") {
                  onStop?.();
                  return;
                }
                if (mode === "stop-recording") {
                  setIsRecording(false);
                  return;
                }
                if (mode === "send" || mode === "steer") {
                  handleSubmit();
                  return;
                }
                // mode === "voice"
                if (!minimal) setIsRecording(true);
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
                    : mode === "stop-recording"
                      ? "Stop recording"
                      : mode === "send"
                        ? "Send message"
                        : "Voice message";

              const tooltip =
                mode === "stop"
                  ? "Stop"
                  : mode === "steer"
                    ? "Steer (mid-turn)"
                    : mode === "stop-recording"
                      ? "Stop recording"
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
                        "inline-flex h-9 w-9 items-center justify-center rounded-full transition-all duration-200",
                        mode === "stop" &&
                          "border-2 border-danger bg-transparent text-danger hover:bg-danger/10",
                        mode === "steer" &&
                          "border-2 border-danger bg-primary text-primary-foreground hover:bg-primary/90",
                        mode === "stop-recording" &&
                          "bg-transparent text-danger hover:bg-danger/10",
                        mode === "send" &&
                          "bg-primary text-primary-foreground hover:bg-primary/90",
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
                      ) : mode === "stop-recording" ? (
                        <StopCircle className="h-5 w-5" />
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
