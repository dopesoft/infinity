"use client";

import { useEffect, useState } from "react";
import {
  Download, FileSpreadsheet, FileText, Presentation, FileType2, Loader2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Markdown } from "@/components/chat/Markdown";
import { fetchWorkspaceBlob, downloadWorkspaceFile } from "@/lib/api";
import type { DocMeta } from "@/lib/canvas/store";

/**
 * DocumentTab - body of a generated-document tab.
 *
 * Three render paths, all cloud-first (work on any device, independent of
 * the session's Mac/Cloud bridge):
 *   • markdown report  → rendered inline via the chat Markdown component
 *                        (the markdown rode the ws event, no fetch).
 *   • PDF (or any doc with a sibling PDF preview) → inline <iframe> fed by a
 *                        blob object URL from the cloud-direct download proxy.
 *   • binary (xlsx/docx/pptx, no preview) → a download card.
 *
 * Downloads/previews fetch via authedFetch → Blob (the bearer never lands in
 * a URL, and binary bytes never round-trip as text).
 */
function iconFor(format: string) {
  switch (format) {
    case "xlsx": return FileSpreadsheet;
    case "pptx": return Presentation;
    case "pdf": return FileType2;
    default: return FileText; // docx, md
  }
}

function prettyBytes(n?: number): string {
  if (!n || n <= 0) return "";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

export function DocumentTab({ doc }: { doc: DocMeta }) {
  const Icon = iconFor(doc.format);
  const isReport = doc.format === "md" || (!!doc.markdown && doc.format !== "pdf");
  const previewPath = doc.format === "pdf" ? doc.path : doc.pdfPath;

  const [downloading, setDownloading] = useState(false);
  const [pdfUrl, setPdfUrl] = useState<string | null>(null);
  const [pdfLoading, setPdfLoading] = useState(!!previewPath);

  // Load the PDF preview as a blob object URL (iframe can't carry a bearer).
  useEffect(() => {
    if (!previewPath) return;
    let revoked = false;
    let url: string | null = null;
    setPdfLoading(true);
    fetchWorkspaceBlob(previewPath).then((blob) => {
      if (revoked) return;
      if (blob) {
        url = URL.createObjectURL(blob);
        setPdfUrl(url);
      }
      setPdfLoading(false);
    });
    return () => {
      revoked = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [previewPath]);

  async function handleDownload() {
    setDownloading(true);
    try {
      await downloadWorkspaceFile(doc.path, doc.filename);
    } finally {
      setDownloading(false);
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      {/* Toolbar */}
      <div className="flex h-10 shrink-0 items-center gap-2 border-b bg-muted/20 px-3 dark:bg-zinc-900/40">
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate text-xs font-medium" title={doc.filename}>
          {doc.filename}
        </span>
        {doc.bytes ? (
          <span className="shrink-0 text-[11px] text-muted-foreground">{prettyBytes(doc.bytes)}</span>
        ) : null}
        <Button
          size="sm"
          variant="ghost"
          className="h-7 shrink-0 gap-1 px-2 text-[11px]"
          onClick={() => void handleDownload()}
          disabled={downloading}
          title={`Download ${doc.filename}`}
        >
          {downloading ? <Loader2 className="size-3.5 animate-spin" /> : <Download className="size-3.5" />}
          Download
        </Button>
      </div>

      {/* Body */}
      <div className="relative min-h-0 flex-1 overflow-hidden">
        {isReport ? (
          <div className="scroll-touch h-full overflow-y-auto px-5 py-4 sm:px-8">
            <Markdown text={doc.markdown ?? ""} className="max-w-3xl" />
          </div>
        ) : previewPath ? (
          pdfLoading ? (
            <div className="flex h-full items-center justify-center text-muted-foreground">
              <Loader2 className="size-5 animate-spin" />
            </div>
          ) : pdfUrl ? (
            <iframe src={pdfUrl} title={doc.filename} className="block size-full border-0 bg-white" />
          ) : (
            <DownloadCard doc={doc} Icon={Icon} onDownload={handleDownload} downloading={downloading} />
          )
        ) : (
          <DownloadCard doc={doc} Icon={Icon} onDownload={handleDownload} downloading={downloading} />
        )}
      </div>
    </div>
  );
}

function DownloadCard({
  doc, Icon, onDownload, downloading,
}: {
  doc: DocMeta;
  Icon: typeof FileText;
  onDownload: () => void;
  downloading: boolean;
}) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
      <Icon className="size-12 text-muted-foreground/50" />
      <div>
        <p className="text-sm font-medium">{doc.filename}</p>
        <p className="text-xs uppercase tracking-wide text-muted-foreground">
          {doc.format}{doc.bytes ? ` · ${prettyBytes(doc.bytes)}` : ""}
        </p>
      </div>
      <Button size="sm" className="gap-1.5" onClick={() => void onDownload()} disabled={downloading}>
        {downloading ? <Loader2 className="size-4 animate-spin" /> : <Download className="size-4" />}
        Download
      </Button>
    </div>
  );
}
