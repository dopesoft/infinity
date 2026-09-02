"use client";

import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { GroupLabel, StatusDot } from "@/components/ui/list-row";

/* The Job Hunt pursuit.
 *
 * Opened by tapping the job hunt on the dashboard, the same way the coached
 * Psycho-Cybernetics pursuit opens its own cockpit rather than the ordinary
 * goal card. It is a pipeline, not a checkbox: roles move between stages,
 * interview answers get banked and reused, hiring managers get contacted, and
 * a resume or cover letter is generated per role.
 *
 * Shell only for now. The four sections below are the shape the cockpit will
 * fill: the pipeline board, the answer corpus, the outreach list, and the
 * generated documents. Nothing here reads the server yet, so each section
 * says what will fill it rather than implying it is empty.
 *
 * Chrome mirrors the Psycho-Cybernetics cockpit deliberately: one hairline
 * bar, then the content. Same family, not a second design language.
 */

/** The sections, in the order the boss works through them. Held as data so the
 *  render below stays one loop and a new section cannot arrive with its own
 *  bespoke heading treatment. */
const SECTIONS: ReadonlyArray<{ label: string; empty: string }> = [
  {
    label: "pipeline",
    empty: "Roles you are tracking will line up here, by stage.",
  },
  {
    label: "corpus",
    empty: "Interview answers you bank will collect here, grouped by theme.",
  },
  {
    label: "outreach",
    empty: "Hiring managers and recruiters you contact will show here.",
  },
  {
    label: "artifacts",
    empty: "Resumes and cover letters written for a role will show here.",
  },
];

export function JobHuntCockpit({
  title,
  open,
  onOpenChange,
}: {
  pursuitId: string;
  title: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description="Job hunt pipeline"
      size="full"
      desktopHeight="full"
      bodyClassName="flex min-h-0 min-w-0 flex-col overflow-y-hidden p-0 sm:p-0"
      header={<CockpitBar title={title} />}
    >
      <div className="min-h-0 min-w-0 flex-1 overflow-y-auto overflow-x-hidden scroll-touch">
        <div className="mx-auto min-w-0 max-w-full px-4 pb-8 pt-2 sm:max-w-[48rem] sm:px-5">
          {SECTIONS.map((section) => (
            <section key={section.label} className="min-w-0">
              <GroupLabel label={section.label} />
              <p className="py-2 text-[13px] text-quiet">{section.empty}</p>
            </section>
          ))}
        </div>
      </div>
    </ResponsiveModal>
  );
}

/* The whole of the chrome. The title stays as the modal's accessible name
 * (ResponsiveModal renders it sr-only when a custom header is supplied), so
 * showing it here is the only place it appears rather than a second copy. */
function CockpitBar({ title }: { title: string }) {
  return (
    <header className="flex shrink-0 items-center gap-3 border-b border-hairline px-4 py-2.5 pr-12 sm:px-5 sm:pr-14">
      <StatusDot tone="brand" />
      <span className="min-w-0 flex-1 truncate font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
        {title}
      </span>
    </header>
  );
}
