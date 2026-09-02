"use client";

import { useEffect, useState } from "react";
import {
  Download,
  FileSpreadsheet,
  FileText,
  Presentation,
  FileType2,
  RotateCw,
} from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { Markdown } from "@/components/chat/Markdown";
import { fetchWorkspaceBlob, downloadWorkspaceFile, fetchDocPages } from "@/lib/api";
import { PdfDeckViewer } from "@/components/canvas/PdfDeckViewer";
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
  // Spreadsheets preview as a side-scrollable HTML grid (NOT a column-chopping
  // PDF); everything else previews as PDF.
  const previewPath = doc.htmlPath ? doc.htmlPath : doc.format === "pdf" ? doc.path : doc.pdfPath;
  const previewType = doc.htmlPath ? "text/html" : "application/pdf";
  const isPdfPreview = !!previewPath && previewType === "application/pdf";

  const [downloading, setDownloading] = useState(false);
  // A preview that COULD NOT BE FETCHED is a different answer from a document
  // that HAS no preview, and both used to render the same download card. That
  // is the silent-green failure in UI form: it reads as "there is nothing to
  // show you" when it means "I could not get it".
  const [previewFailed, setPreviewFailed] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);

  // PDF previews render page-by-page (one slide at a time) via the workspace's
  // rasterized page images; pages=null while loading, [] when unavailable (then
  // we fall back to the native iframe so the preview never regresses).
  const [pages, setPages] = useState<string[] | null>(isPdfPreview ? null : []);
  useEffect(() => {
    if (!isPdfPreview || !previewPath) {
      setPages([]);
      return;
    }
    let cancelled = false;
    setPages(null);
    fetchDocPages(previewPath).then((p) => {
      if (!cancelled) setPages(p);
    });
    return () => {
      cancelled = true;
    };
    // reloadKey: "Try again" has to re-run the page render too, or it only
    // retries half the preview.
  }, [isPdfPreview, previewPath, reloadKey]);

  // The blob/iframe path is only needed for HTML previews and as the PDF
  // fallback (when page rasterization isn't available). Don't fetch it while
  // the deck viewer is in play.
  const needIframe = !!previewPath && (previewType === "text/html" || (isPdfPreview && pages?.length === 0));
  const [pdfUrl, setPdfUrl] = useState<string | null>(null);

  // Load the iframe preview (HTML, or the PDF fallback) as a blob object URL —
  // an iframe can't carry a bearer. Force the content type so it RENDERS.
  useEffect(() => {
    if (!needIframe || !previewPath) return;
    let revoked = false;
    let url: string | null = null;
    setPreviewFailed(false);
    fetchWorkspaceBlob(previewPath).then((blob) => {
      if (revoked) return;
      if (blob) {
        url = URL.createObjectURL(new Blob([blob], { type: previewType }));
        setPdfUrl(url);
      } else {
        setPreviewFailed(true);
      }
    });
    return () => {
      revoked = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [needIframe, previewPath, previewType, reloadKey]);

  async function handleDownload(path: string, filename: string) {
    setDownloading(true);
    try {
      await downloadWorkspaceFile(path, filename);
    } finally {
      setDownloading(false);
    }
  }

  // A rendered sibling PDF (docx/pptx/xlsx → also_pdf) is the portable,
  // share-anywhere version — the boss wants to grab THAT, not just the source
  // .docx. Offer it as its own button whenever a pdfPath exists and the file
  // isn't already a PDF.
  const pdfDownloadPath = doc.format !== "pdf" ? doc.pdfPath : null;
  const pdfDownloadName = doc.filename.replace(/\.[^./]+$/, "") + ".pdf";

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
        {pdfDownloadPath ? (
          <Button
            size="sm"
            variant="ghost"
            className="h-7 shrink-0 gap-1 px-2 text-[11px]"
            onClick={() => void handleDownload(pdfDownloadPath, pdfDownloadName)}
            disabled={downloading}
            title={`Download ${pdfDownloadName}`}
          >
            {downloading ? <Spinner className="size-3.5" /> : <Download className="size-3.5" />}
            PDF
          </Button>
        ) : null}
        <Button
          size="sm"
          variant="ghost"
          className="h-7 shrink-0 gap-1 px-2 text-[11px]"
          onClick={() => void handleDownload(doc.path, doc.filename)}
          disabled={downloading}
          title={`Download ${doc.filename}`}
        >
          {downloading ? <Spinner className="size-3.5" /> : <Download className="size-3.5" />}
          {pdfDownloadPath ? doc.format.toUpperCase() : "Download"}
        </Button>
      </div>

      {/* Body */}
      <div className="relative min-h-0 flex-1 overflow-hidden">
        {isReport ? (
          <div className="scroll-touch h-full overflow-y-auto px-5 py-4 sm:px-8">
            <Markdown text={doc.markdown ?? ""} className="max-w-3xl" />
          </div>
        ) : isPdfPreview && pages === null ? (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            <Spinner className="size-5" />
          </div>
        ) : isPdfPreview && pages && pages.length > 0 ? (
          <PdfDeckViewer pages={pages} filename={doc.filename} />
        ) : previewPath ? (
          pdfUrl ? (
            <iframe src={pdfUrl} title={doc.filename} className="block size-full border-0 bg-white" />
          ) : previewFailed ? (
            <PreviewFailedCard
              doc={doc}
              onRetry={() => setReloadKey((k) => k + 1)}
              onDownload={() => void handleDownload(doc.path, doc.filename)}
              downloading={downloading}
            />
          ) : (
            <div className="flex h-full items-center justify-center text-muted-foreground">
              <Spinner className="size-5" />
            </div>
          )
        ) : (
          <DownloadCard doc={doc} Icon={Icon} onDownload={() => void handleDownload(doc.path, doc.filename)} downloading={downloading} />
        )}
      </div>
    </div>
  );
}

// PreviewFailedCard - what he sees when the bytes could not be fetched. It says
// the true thing ("I could not load it"), offers the retry, and still leaves
// the download within reach. It never pretends the document has no preview.
function PreviewFailedCard({
  doc, onRetry, onDownload, downloading,
}: {
  doc: DocMeta;
  onRetry: () => void;
  onDownload: () => void;
  downloading: boolean;
}) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
      <div className="max-w-sm space-y-1.5">
        <p className="text-sm font-medium">{doc.filename}</p>
        <p className="text-xs leading-relaxed text-muted-foreground">
          I couldn&apos;t load this one to show you.
        </p>
      </div>
      <div className="flex items-center gap-2">
        <Button size="sm" variant="outline" className="gap-1.5" onClick={onRetry}>
          <RotateCw className="size-4" />
          Try again
        </Button>
        <Button size="sm" className="gap-1.5" onClick={() => void onDownload()} disabled={downloading}>
          {downloading ? <Spinner className="size-4" /> : <Download className="size-4" />}
          Download
        </Button>
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
        {downloading ? <Spinner className="size-4" /> : <Download className="size-4" />}
        Download
      </Button>
    </div>
  );
}
