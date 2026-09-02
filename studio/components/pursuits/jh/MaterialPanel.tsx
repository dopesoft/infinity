"use client";

import { useState } from "react";
import { ArrowLeft } from "lucide-react";
import { TabsContent } from "@/components/ui/tabs";
import { PageTabs, PageTabsList, PageTabsTrigger } from "@/components/ui/page-tabs";
import { Button } from "@/components/ui/button";
import { Inset } from "@/components/ui/inset";
import { ListRow, type RowTone } from "@/components/ui/list-row";
import { writeCockpit } from "@/lib/pursuits/jh/api";
import {
  artifactKindLabel,
  artifactStatusLabel,
  contactStatusLabel,
  contactTone,
  corpusSourceLabel,
  shortDate,
} from "@/lib/pursuits/jh/labels";
import type { JHCockpit } from "@/lib/pursuits/jh/types";
import { VocabularySelect } from "./VocabularySelect";

/* Everything the pipeline is built on, one tap away.
 *
 * The banked answers, the people, and the documents are three lists, not three
 * headings stacked on the board. They sit behind one affordance for the same
 * reason the coaching programme does: the pipeline is what the boss came for,
 * and three permanent sections above it push it below the fold on a phone.
 *
 * A row here opens in place rather than navigating: the answer, the message
 * that went out, the document's approval. That is ListRow's own `children`
 * slot, so an opened row looks identical in all three lists.
 *
 * Every write goes through the shared cockpit chokepoint and returns the whole
 * refreshed board, so this panel and the pipeline beside it cannot drift.
 */
export function MaterialPanel({
  cockpit,
  onUpdated,
  onBack,
}: {
  cockpit: JHCockpit;
  onUpdated: (next: JHCockpit) => void;
  onBack: () => void;
}) {
  const { corpus, contacts, artifacts } = cockpit;

  return (
    <div className="min-w-0 max-w-full px-4 pb-10 pt-2 sm:px-5">
      <div className="lg:hidden">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft className="size-4" aria-hidden />
          Back to the pipeline
        </Button>
      </div>

      {/* A sub strip, because the cockpit's own bar sits above it. */}
      <PageTabs defaultValue="answers" className="min-w-0 pt-3">
        <PageTabsList level="sub">
          <PageTabsTrigger value="answers">{tabLabel("Answers", corpus.length)}</PageTabsTrigger>
          <PageTabsTrigger value="people">{tabLabel("People", contacts.length)}</PageTabsTrigger>
          <PageTabsTrigger value="documents">
            {tabLabel("Documents", artifacts.length)}
          </PageTabsTrigger>
        </PageTabsList>

        <TabsContent value="answers" className="min-w-0">
          {corpus.length === 0 ? (
            <Empty>
              No interview answers banked yet. Ones you work through with me collect here, so a
              tailored resume has something real to draw on.
            </Empty>
          ) : (
            corpus.map((entry) => (
              <OpenableRow
                key={entry.id}
                title={entry.question || entry.theme}
                meta={[entry.theme, corpusSourceLabel(entry.source)].filter(Boolean).join(" · ")}
              >
                <Inset variant="quote" text={entry.answer} />
                {entry.tags?.length ? (
                  <p className="pt-2 text-[12px] text-quiet">{entry.tags.join(" · ")}</p>
                ) : null}
              </OpenableRow>
            ))
          )}
        </TabsContent>

        <TabsContent value="people" className="min-w-0">
          {contacts.length === 0 ? (
            <Empty>
              Nobody yet. Hiring managers and recruiters found for a role show up here, with
              where the conversation has got to.
            </Empty>
          ) : (
            contacts.map((contact) => (
              <OpenableRow
                key={contact.id}
                tone={contactTone(contact)}
                title={contact.name}
                meta={[contact.title, contact.company, contactStatusLabel(contact.outreach_status)]
                  .filter(Boolean)
                  .join(" · ")}
              >
                <VocabularySelect
                  value={contact.outreach_status}
                  options={cockpit.vocabulary.contact_statuses}
                  labelFor={contactStatusLabel}
                  ariaLabel={`Where things stand with ${contact.name}`}
                  onSelect={async (status) => {
                    onUpdated(
                      await writeCockpit(cockpit.pursuit.id, "contact/status", {
                        contact_id: contact.id,
                        status,
                      }),
                    );
                  }}
                />
                {contact.outreach_sent_at ? (
                  <p className="pt-2 text-[12px] text-quiet" suppressHydrationWarning>
                    Message went out {shortDate(contact.outreach_sent_at)}
                  </p>
                ) : null}
                {contact.last_message?.trim() ? (
                  <div className="pt-2">
                    <Inset variant="quote" text={contact.last_message.trim()} />
                  </div>
                ) : null}
                {contact.email || contact.linkedin_url ? (
                  <p className="pt-2 text-[12px] text-quiet [overflow-wrap:anywhere]">
                    {[contact.email, contact.linkedin_url].filter(Boolean).join(" · ")}
                  </p>
                ) : null}
              </OpenableRow>
            ))
          )}
        </TabsContent>

        <TabsContent value="documents" className="min-w-0">
          {artifacts.length === 0 ? (
            <Empty>
              Nothing written yet. Resumes and cover letters tailored to a role are filed here,
              and nothing goes out until you have approved it.
            </Empty>
          ) : (
            artifacts.map((artifact) => {
              const role = cockpit.roles.find((r) => r.id === artifact.role_id);
              return (
                <OpenableRow
                  key={artifact.id}
                  title={artifact.title || artifactKindLabel(artifact.kind)}
                  meta={[
                    artifactKindLabel(artifact.kind),
                    role?.company,
                    artifactStatusLabel(artifact.status),
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                >
                  <VocabularySelect
                    value={artifact.status}
                    options={cockpit.vocabulary.artifact_statuses}
                    labelFor={artifactStatusLabel}
                    ariaLabel={`Approval for ${artifact.title || artifactKindLabel(artifact.kind)}`}
                    onSelect={async (status) => {
                      onUpdated(
                        await writeCockpit(cockpit.pursuit.id, "artifact/status", {
                          artifact_id: artifact.id,
                          status,
                        }),
                      );
                    }}
                  />
                  <p className="pt-2 text-[12px] text-quiet" suppressHydrationWarning>
                    Written {shortDate(artifact.created_at)}
                  </p>
                </OpenableRow>
              );
            })
          )}
        </TabsContent>
      </PageTabs>
    </div>
  );
}

/** A tab's name, carrying its count only when there is something to count.
 *  Zero is the empty state's job, not a badge's. */
function tabLabel(word: string, count: number): string {
  return count > 0 ? `${word} ${count}` : word;
}

/** A row that opens in place. One shape for all three lists, so an answer, a
 *  conversation and a document all behave the same way when tapped. */
function OpenableRow({
  title,
  meta,
  tone,
  children,
}: {
  title: string;
  meta?: string;
  tone?: RowTone;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <ListRow
      tone={tone}
      title={title}
      meta={meta}
      onClick={() => setOpen((v) => !v)}
      chevron={false}
    >
      {open ? <div className="min-w-0 max-w-full">{children}</div> : null}
    </ListRow>
  );
}

/** The one empty-state voice for this panel: what will fill it, once. */
function Empty({ children }: { children: React.ReactNode }) {
  return (
    <p className="py-3 font-voice text-[15.5px] leading-[1.6] text-quiet">{children}</p>
  );
}
