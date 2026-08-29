"use client";

import * as React from "react";
import { CanvasPreview } from "@/components/canvas/CanvasPreview";
import { CanvasTerminal } from "@/components/canvas/CanvasTerminal";
import { CanvasMediaGallery } from "@/components/canvas/CanvasMediaGallery";
import { CanvasFileTab } from "@/components/canvas/CanvasFileTab";
import { ChangesReview } from "@/components/canvas/ChangesReview";
import { DocumentTab } from "@/components/canvas/DocumentTab";
import { BrowserFrame } from "@/components/canvas/BrowserFrame";
import { FileSwitcher } from "@/components/canvas/FileSwitcher";
import { InstrumentBar, type Instrument } from "@/components/canvas/InstrumentBar";
import { LayoutModeSwitch } from "@/components/canvas/LayoutModeSwitch";
import { useCanvasStore } from "@/lib/canvas/store";
import type { DocArtifact, RunDTO } from "@/lib/api";
import { cn } from "@/lib/utils";

/**
 * WorkbenchPane — the instruments, behind one bar.
 *
 * Every existing surface is KEPT and wrapped, not rewritten: CanvasPreview,
 * CanvasTerminal, CanvasMediaGallery, CanvasFileTab, CanvasGitPanel and
 * DocumentTab all still do their jobs. What changed is the chrome above
 * them — two competing tab strips and a fake URL bar became one instrument
 * row and a real browser.
 *
 * Everything stays MOUNTED and is hidden with opacity, exactly as the old
 * right pane did, so switching instruments never re-initialises Monaco,
 * never drops terminal scrollback, and never restarts the media
 * subscription.
 */
export function WorkbenchPane({
  sessionId,
  documents,
  mediaRuns,
  docsLoading,
  onRefreshDocs,
  onDiscussDoc,
  changeCount,
  onClose,
}: {
  sessionId: string;
  documents: DocArtifact[];
  mediaRuns: RunDTO[];
  docsLoading: boolean;
  onRefreshDocs: () => void;
  onDiscussDoc: (doc: DocArtifact) => void;
  changeCount: number;
  onClose: () => void;
}) {
  const store = useCanvasStore();
  const [instrument, setInstrument] = React.useState<Instrument>("browser");
  const [switcherOpen, setSwitcherOpen] = React.useState(false);
  const [rebuiltAt, setRebuiltAt] = React.useState<number | null>(null);

  // The preview refresh key rising IS the rebuild signal the store already
  // emits; stamping it here is what lets the gutter answer "is what I am
  // looking at current".
  React.useEffect(() => {
    if (store.previewRefreshKey > 0) setRebuiltAt(Date.now());
  }, [store.previewRefreshKey]);

  const activeFile = store.tabs.find((t) => t.id === store.activeTabId && t.kind === "file");
  const activeDoc = store.documents.find((d) => d.id === store.activeTabId);
  const fileName = activeFile && activeFile.kind === "file" ? basename(activeFile.path) : activeDoc?.filename;

  // A file opening (he started writing) makes it the thing you are looking at.
  // Documents are deliberately NOT here: a finished document is something he
  // MADE, and the media snap below owns that case.
  React.useEffect(() => {
    if (activeFile) setInstrument("file");
  }, [activeFile]);

  const mediaCount =
    documents.length +
    mediaRuns.reduce(
      (n, r) => n + (r.status !== "running" && r.status !== "error" ? (r.meta?.media?.length ?? 0) : 0),
      0,
    );

  // Something finished rendering: SNAP TO IT. Widening the layout was only
  // half the promise — you should never have to go looking for a thing he
  // just made, so the pane switches to Made as well.
  //
  // Guarded on a rising count from a KNOWN previous value, so the first paint
  // of a session that already has media cannot yank you off what you were
  // reading.
  const mediaSeenRef = React.useRef<number | null>(null);
  React.useEffect(() => {
    const prev = mediaSeenRef.current;
    mediaSeenRef.current = mediaCount;
    if (prev === null) return;
    if (mediaCount > prev) setInstrument("made");
  }, [mediaCount]);

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <InstrumentBar
        active={instrument}
        onSelect={setInstrument}
        fileName={fileName}
        onOpenSwitcher={() => setSwitcherOpen(true)}
        changes={changeCount}
        madeCount={mediaCount}
        onClose={onClose}
        trailing={<LayoutModeSwitch />}
      />

      <div className="relative min-h-0 flex-1">
        <Layer on={instrument === "browser"}>
          <BrowserFrame
            url={store.previewUrl}
            rebuiltAt={rebuiltAt}
            onNavigate={(next) => store.setPreviewUrl(next)}
          >
            <CanvasPreview sessionId={sessionId} />
          </BrowserFrame>
        </Layer>

        <Layer on={instrument === "changes"}>
          <ChangesReview
            sessionId={sessionId}
            onFileOpen={(p) => {
              store.openFile(p);
              setInstrument("file");
            }}
          />
        </Layer>

        <Layer on={instrument === "terminal"}>
          <CanvasTerminal sessionId={sessionId} />
        </Layer>

        <Layer on={instrument === "made"}>
          <CanvasMediaGallery
            documents={documents}
            mediaRuns={mediaRuns}
            loading={docsLoading}
            onRefresh={onRefreshDocs}
            onDiscuss={onDiscussDoc}
          />
        </Layer>

        {/* Files and documents stay mounted so Monaco keeps its cursor and
            scroll position across every instrument switch. */}
        {store.tabs.map((tab) =>
          tab.kind === "file" ? (
            <Layer key={tab.id} on={instrument === "file" && store.activeTabId === tab.id}>
              <CanvasFileTab
                path={tab.path}
                isActive={instrument === "file" && store.activeTabId === tab.id}
                sessionId={sessionId}
              />
            </Layer>
          ) : null,
        )}
        {store.documents.map((doc) => (
          <Layer key={doc.id} on={instrument === "file" && store.activeTabId === doc.id}>
            <DocumentTab doc={doc} />
          </Layer>
        ))}
      </div>

      <FileSwitcher
        open={switcherOpen}
        onOpenChange={setSwitcherOpen}
        onPick={(p) => {
          store.openFile(p);
          setInstrument("file");
        }}
      />
    </div>
  );
}

function Layer({ on, children }: { on: boolean; children: React.ReactNode }) {
  return (
    <div
      className={cn("absolute inset-0 flex flex-col", on ? "opacity-100" : "pointer-events-none opacity-0")}
      aria-hidden={!on}
    >
      {children}
    </div>
  );
}

function basename(p: string): string {
  return p.replace(/^.*[\\/]/, "");
}
