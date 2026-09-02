"use client";

import { ArrowLeft, ExternalLink } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Inset } from "@/components/ui/inset";
import { GroupLabel, ListRow } from "@/components/ui/list-row";
import { writeCockpit } from "@/lib/pursuits/jh/api";
import {
  artifactKindLabel,
  artifactStatusLabel,
  compLabel,
  contactStatusLabel,
  contactTone,
  fitLabel,
  ghostSentence,
  hasGhostRisk,
  humanise,
  locationLabel,
  shortDate,
  sourceLabel,
  stageLabel,
} from "@/lib/pursuits/jh/labels";
import type { JHCockpit, JHRole } from "@/lib/pursuits/jh/types";
import { VocabularySelect } from "./VocabularySelect";

/* One role, opened.
 *
 * This is where the numbers on the card become reasons: why it fits, what
 * makes it look like a ghost listing, who is at the company, what has been
 * written for it, and the one control that moves it along the pipeline.
 *
 * A ledger, not a dashboard — mono group label, rows, one tinted block per
 * piece of prose, exactly the shape the coaching programme panel uses. Nothing
 * here is a second design language.
 *
 * The stage control writes through the shared cockpit chokepoint and hands the
 * refreshed board straight to `onUpdated`. This component holds no copy of the
 * role: it is looked up out of the cockpit on every render, so a move made
 * from chat and a move made here land in the same place.
 */
export function RoleDetail({
  cockpit,
  role,
  onUpdated,
  onBack,
}: {
  cockpit: JHCockpit;
  role: JHRole;
  onUpdated: (next: JHCockpit) => void;
  onBack: () => void;
}) {
  // The people worth showing next to a role are the ones found FOR it plus
  // anyone else at the same company, because a hiring manager filed against
  // one posting is still the person to talk to about another.
  const company = role.company.trim().toLowerCase();
  const contacts = cockpit.contacts.filter(
    (c) => c.role_id === role.id || c.company.trim().toLowerCase() === company,
  );
  const artifacts = cockpit.artifacts.filter((a) => a.role_id === role.id);
  const fit = fitLabel(role);

  return (
    <div className="min-w-0 max-w-full px-4 pb-10 pt-2 sm:px-5">
      {/* The way back exists only on a phone, where this panel took the whole
          surface. On a laptop the board is still beside it. */}
      <div className="lg:hidden">
        <Button variant="ghost" size="sm" onClick={onBack}>
          <ArrowLeft className="size-4" aria-hidden />
          Back to the pipeline
        </Button>
      </div>

      <h2 className="pt-2 font-voice text-[18px] font-medium leading-snug tracking-tight text-foreground">
        {role.company}
      </h2>
      <p className="mt-0.5 text-[13.5px] leading-relaxed text-muted-foreground">
        {role.role_title}
      </p>

      <GroupLabel label="Stage" />
      <VocabularySelect
        value={role.stage}
        options={cockpit.vocabulary.role_stages}
        labelFor={stageLabel}
        ariaLabel="Stage"
        onSelect={async (stage) => {
          onUpdated(
            await writeCockpit(cockpit.pursuit.id, "role/stage", {
              role_id: role.id,
              stage,
            }),
          );
        }}
      />
      <p className="pt-2 text-[12px] text-quiet" suppressHydrationWarning>
        Moved {shortDate(role.stage_changed_at)}
      </p>

      <GroupLabel label="The posting" />
      <Inset
        variant="kv"
        items={[
          { label: "pay", value: compLabel(role) },
          { label: "where", value: locationLabel(role) },
          { label: "found on", value: sourceLabel(role.source) },
          ...(fit ? [{ label: "fit", value: fit }] : []),
          {
            label: "posted",
            value: <span suppressHydrationWarning>{shortDate(role.posted_at)}</span>,
          },
          {
            label: "spotted",
            value: <span suppressHydrationWarning>{shortDate(role.discovered_at)}</span>,
          },
        ]}
      />
      {role.url ? (
        <a
          href={role.url}
          target="_blank"
          rel="noreferrer"
          className="mt-2 inline-flex min-h-11 items-center gap-2 text-[13px] font-medium text-brand transition-colors hover:text-foreground [overflow-wrap:anywhere]"
        >
          Open the posting
          <ExternalLink className="size-3.5 shrink-0" aria-hidden />
        </a>
      ) : null}

      {role.fit_reasoning?.trim() ? (
        <>
          <GroupLabel label="Why it fits" />
          <Inset variant="quote" text={role.fit_reasoning.trim()} />
        </>
      ) : null}

      {/* Every flag spelled out. A ghost score with the signals hidden is a
          number he cannot argue with; the signals are the whole point. */}
      {hasGhostRisk(role) ? (
        <>
          <GroupLabel label="Ghost risk" />
          <p className="pb-1 font-voice text-[15.5px] leading-[1.55] text-warning">
            {ghostSentence(role)}
          </p>
          <ul className="flex min-w-0 flex-col gap-1.5 pt-1">
            {role.ghost_flags.map((flag, i) => (
              <li
                key={`${flag}-${i}`}
                className="flex min-w-0 gap-2 text-[13.5px] leading-relaxed text-muted-foreground [overflow-wrap:anywhere]"
              >
                <span className="shrink-0 select-none text-warning" aria-hidden>
                  ·
                </span>
                <span className="min-w-0">{humanise(flag)}</span>
              </li>
            ))}
          </ul>
        </>
      ) : null}

      {role.notes?.trim() ? (
        <>
          <GroupLabel label="Notes" />
          <Inset text={role.notes.trim()} />
        </>
      ) : null}

      <GroupLabel label="People there" count={contacts.length} />
      {contacts.length === 0 ? (
        <p className="py-2 text-[13px] text-quiet">
          Nobody at {role.company} yet. Anyone found there shows up here.
        </p>
      ) : (
        contacts.map((contact) => (
          <ListRow
            key={contact.id}
            tone={contactTone(contact)}
            title={contact.name}
            meta={[contact.title, contactStatusLabel(contact.outreach_status)]
              .filter(Boolean)
              .join(" · ")}
          />
        ))
      )}

      <GroupLabel label="Written for this role" count={artifacts.length} />
      {artifacts.length === 0 ? (
        <p className="py-2 text-[13px] text-quiet">
          Nothing written yet. A resume or cover letter tailored to this role is filed here.
        </p>
      ) : (
        artifacts.map((artifact) => (
          <ListRow
            key={artifact.id}
            title={artifact.title || artifactKindLabel(artifact.kind)}
            meta={`${artifactKindLabel(artifact.kind)} · ${artifactStatusLabel(artifact.status)}`}
          />
        ))
      )}
    </div>
  );
}
