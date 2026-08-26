"use client";

import { FileText, Loader2, Paperclip, TriangleAlert } from "lucide-react";
import { cn } from "@/lib/utils";
import type { ChatAttachment } from "@/hooks/useChat";
import { openAttachment, useAttachmentObjectUrl } from "@/lib/attachments";

// The one attachment row used everywhere a message shows its files: the live
// chat bubble, the reloaded transcript, the workspace chat column. Images
// render as tiles (local blob while sending, JWT-fetched raw route after
// reload); everything else is a tappable chip with an honest status line.

export function formatAttachmentSize(bytes?: number): string {
  if (!bytes || bytes <= 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function attachmentStatus(att: ChatAttachment): { label: string; tone: "muted" | "warn" } | null {
  if (att.uploading) return { label: "Uploading…", tone: "muted" };
  if (att.error) return { label: att.error, tone: "warn" };
  if (att.extractStatus === "failed") return { label: "Saved, but I couldn't read the text", tone: "warn" };
  if (att.extractStatus === "empty" && !att.mimeType?.startsWith("image/")) {
    return { label: "No text layer, reading it as pages", tone: "muted" };
  }
  if (att.pageCount && att.pageCount > 0) {
    return { label: `${att.pageCount} page${att.pageCount === 1 ? "" : "s"}`, tone: "muted" };
  }
  return null;
}

function openablePath(att: ChatAttachment): string | undefined {
  if (att.url) return att.url;
  if (att.previewUrl && !att.previewUrl.startsWith("blob:")) return att.previewUrl;
  return undefined;
}

function ImageTile({ att }: { att: ChatAttachment }) {
  const src = useAttachmentObjectUrl(att.previewUrl);
  const status = attachmentStatus(att);
  const path = openablePath(att);
  return (
    <button
      type="button"
      onClick={() => {
        if (path) void openAttachment(path);
      }}
      disabled={!path}
      className={cn(
        "overflow-hidden rounded-xl border border-border/60 bg-background/70 text-left",
        path ? "cursor-pointer" : "cursor-default",
      )}
      aria-label={`Open ${att.name}`}
    >
      <div className="relative flex h-24 w-24 items-center justify-center bg-muted/40">
        {src ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={src} alt={att.name} className="h-24 w-24 object-cover" />
        ) : (
          <Loader2 className="size-4 animate-spin text-muted-foreground" />
        )}
        {att.uploading && (
          <div className="absolute inset-0 flex items-center justify-center bg-background/60">
            <Loader2 className="size-4 animate-spin" />
          </div>
        )}
      </div>
      <div
        className={cn(
          "flex items-center gap-1 border-t border-border/60 px-2 py-1 text-[11px]",
          status?.tone === "warn" ? "text-warning" : "text-muted-foreground",
        )}
      >
        {status?.tone === "warn" ? (
          <TriangleAlert className="size-3 shrink-0" />
        ) : (
          <Paperclip className="size-3 shrink-0" />
        )}
        <span className="max-w-[6rem] truncate">{att.name}</span>
      </div>
    </button>
  );
}

function FileChip({ att }: { att: ChatAttachment }) {
  const size = formatAttachmentSize(att.sizeBytes);
  const status = attachmentStatus(att);
  const path = openablePath(att);
  const meta = [att.mimeType, size].filter(Boolean).join(" · ");
  return (
    <button
      type="button"
      onClick={() => {
        if (path) void openAttachment(path);
      }}
      disabled={!path}
      title={att.name}
      className={cn(
        "inline-flex min-h-11 min-w-0 max-w-full items-center gap-2 rounded-xl border border-border/60 bg-background/70 px-2.5 py-2 text-left text-xs text-foreground",
        path ? "cursor-pointer hover:bg-background" : "cursor-default",
      )}
    >
      {att.uploading ? (
        <Loader2 className="size-3.5 shrink-0 animate-spin text-muted-foreground" />
      ) : status?.tone === "warn" ? (
        <TriangleAlert className="size-3.5 shrink-0 text-warning" />
      ) : (
        <FileText className="size-3.5 shrink-0 text-muted-foreground" />
      )}
      <div className="min-w-0">
        <div className="truncate">{att.name}</div>
        {(meta || status) && (
          <div
            className={cn(
              "truncate text-[10px]",
              status?.tone === "warn" ? "text-warning" : "text-muted-foreground",
            )}
          >
            {[status?.label, meta].filter(Boolean).join(" · ")}
          </div>
        )}
      </div>
    </button>
  );
}

export function AttachmentStrip({
  attachments,
  align = "start",
  className,
}: {
  attachments?: ChatAttachment[];
  align?: "start" | "end";
  className?: string;
}) {
  const list = attachments ?? [];
  if (list.length === 0) return null;
  return (
    <div
      className={cn(
        "mt-2 flex max-w-full flex-wrap gap-2",
        align === "end" ? "justify-end" : "justify-start",
        className,
      )}
    >
      {list.map((att, idx) => {
        const isImage = !!att.previewUrl && (att.mimeType?.startsWith("image/") ?? true);
        const key = `${att.id ?? att.name}-${idx}`;
        return isImage ? <ImageTile key={key} att={att} /> : <FileChip key={key} att={att} />;
      })}
    </div>
  );
}
