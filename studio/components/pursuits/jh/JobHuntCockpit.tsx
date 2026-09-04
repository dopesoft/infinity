"use client";

import { useCallback, useEffect, useState } from "react";
import { ChevronRight, MessageSquare } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/native-select";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { GroupLabel, StatusDot, WorkRow } from "@/components/ui/list-row";
import { cn } from "@/lib/utils";
import { fetchCockpit } from "@/lib/pursuits/jh/api";
import {
  compLabel,
  describeBoard,
  fitLabel,
  hasGhostRisk,
  locationLabel,
  roleTone,
  sourceLabel,
  stageLabel,
} from "@/lib/pursuits/jh/labels";
import type { JHCockpit as JHCockpitData, JHRole } from "@/lib/pursuits/jh/types";
import { JobHuntConversation } from "./JobHuntConversation";
import { MaterialPanel } from "./MaterialPanel";
import { RoleDetail } from "./RoleDetail";

/* The Job Hunt pursuit.
 *
 * Opened by tapping the job hunt on the dashboard, the same way the coached
 * Psycho-Cybernetics pursuit opens its own cockpit. It is a PIPELINE, not a
 * checkbox: roles move between stages, and that board is the surface.
 *
 * The chrome is deliberately almost nothing, and is the same chrome PCCockpit
 * wears: one hairline bar carrying the live state of the board and the one way
 * through to everything else. The banked answers, the people and the documents
 * sit behind that one affordance rather than as sections stacked above the
 * pipeline, for the same reason the coaching programme does — the thing he
 * came for must not start below the fold.
 *
 * Nothing here is hardcoded from the schema. The columns are the server's own
 * `vocabulary.role_stages`, in pipeline order, so a stage added to the
 * database appears as a column with no change to this file, and a stage the
 * store would reject can never be offered as a move.
 *
 * Every mutation returns the refreshed cockpit from the server and is adopted
 * wholesale; this component never patches its own copy of the board, so it
 * cannot drift from what a chat-side write would see.
 */

/** Which secondary surface is open, if any. One at a time: on a phone each of
 *  them takes the whole screen, and on a laptop they share the one side
 *  panel, so two open at once has no meaning. The role is held by ID rather
 *  than by value, so a refreshed board is always what gets rendered. */
type Panel =
  | { kind: "role"; roleId: string }
  | { kind: "material" }
  | { kind: "chat" }
  | null;

export function JobHuntCockpit({
  pursuitId,
  title,
  open,
  onOpenChange,
}: {
  pursuitId: string;
  title: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [cockpit, setCockpit] = useState<JHCockpitData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [panel, setPanel] = useState<Panel>(null);
  const [stage, setStage] = useState<string | null>(null);
  /* What he is LOOKING AT, kept apart from which panel happens to be open.
   * Opening the conversation replaces the role detail in the one panel slot,
   * but it must not lose the role he was reading: that role is exactly the
   * context the first thing he types needs to carry. */
  const [focusedRoleId, setFocusedRoleId] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        setCockpit(await fetchCockpit(pursuitId, signal));
      } catch (e) {
        if (signal?.aborted) return;
        // A board we could not read is NOT an empty board. Say so, rather than
        // showing a clean pipeline for a hunt we cannot see.
        setError(e instanceof Error ? e.message : "Could not load this job hunt.");
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [pursuitId],
  );

  useEffect(() => {
    if (!open) return;
    const ac = new AbortController();
    setPanel(null);
    setStage(null);
    setFocusedRoleId(null);
    void load(ac.signal);
    return () => ac.abort();
  }, [open, load]);

  // The open role is looked up out of the CURRENT cockpit every render. A
  // stored copy would keep showing the stage he just moved away from.
  const selectedRole: JHRole | null =
    panel?.kind === "role" && cockpit
      ? (cockpit.roles.find((r) => r.id === panel.roleId) ?? null)
      : null;
  const focusedRole: JHRole | null =
    focusedRoleId && cockpit
      ? (cockpit.roles.find((r) => r.id === focusedRoleId) ?? null)
      : null;
  const chatOpen = panel?.kind === "chat";
  const panelOpen = panel?.kind === "material" || chatOpen || Boolean(selectedRole);

  // The stage the phone is looking at. Derived rather than stored, so it can
  // never point at a stage the vocabulary stopped carrying, and it opens on
  // the first stage that actually has something in it.
  const stages = cockpit?.vocabulary.role_stages ?? [];
  const activeStage =
    stage && stages.includes(stage)
      ? stage
      : (stages.find((s) => (cockpit?.summary.roles_by_stage[s] ?? 0) > 0) ?? stages[0] ?? "");

  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description="Job hunt pipeline"
      size="full"
      desktopHeight="full"
      bodyClassName="flex min-h-0 min-w-0 flex-col overflow-y-hidden p-0 sm:p-0"
      header={
        <BoardBar
          title={title}
          state={cockpit ? describeBoard(cockpit) : undefined}
          materialOpen={panel?.kind === "material"}
          onToggleMaterial={() =>
            setPanel((p) => (p?.kind === "material" ? null : { kind: "material" }))
          }
          chatOpen={chatOpen}
          onToggleChat={() =>
            setPanel((p) => (p?.kind === "chat" ? null : { kind: "chat" }))
          }
          showActions={Boolean(cockpit)}
        />
      }
    >
      {error ? (
        <div className="mx-auto flex w-full max-w-[38rem] flex-col items-start gap-3 px-4 py-10 sm:px-6">
          <p className="font-voice text-[15.5px] leading-[1.6] text-danger">
            I could not open your job hunt just now. {error}
          </p>
          <Button variant="outline" onClick={() => void load()}>
            Try again
          </Button>
        </div>
      ) : loading && !cockpit ? (
        <div className="mx-auto flex w-full max-w-[38rem] items-center gap-2 px-4 py-10 text-[13.5px] text-quiet sm:px-6">
          <Spinner className="size-4" />
          Opening your pipeline
        </div>
      ) : !cockpit ? null : (
        <div className="flex min-h-0 min-w-0 flex-1">
          <div
            className={cn(
              "flex min-h-0 min-w-0 flex-1 flex-col",
              // On a phone the panel takes the whole surface, so the board
              // steps aside rather than sitting behind a sheet.
              panelOpen && "hidden lg:flex",
            )}
          >
            <Pipeline
              cockpit={cockpit}
              activeStage={activeStage}
              onStage={setStage}
              selectedRoleId={selectedRole?.id ?? (chatOpen ? focusedRoleId : null)}
              onOpenRole={(roleId) => {
                setFocusedRoleId(roleId);
                setPanel({ kind: "role", roleId });
              }}
            />
          </div>

          {panelOpen ? (
            <aside
              id="jh-panel"
              aria-label={chatOpen ? "Jarvis" : selectedRole ? "Role" : "Material"}
              className={cn(
                "min-h-0 w-full min-w-0 overflow-x-hidden lg:w-[24rem] lg:shrink-0 lg:border-l lg:border-hairline",
                // The conversation owns its own scroller and pins its composer,
                // so the panel must not scroll it a second time.
                chatOpen
                  ? "flex flex-col overflow-y-hidden"
                  : "overflow-y-auto scroll-touch",
              )}
            >
              {chatOpen ? (
                <JobHuntConversation
                  pursuitId={pursuitId}
                  role={focusedRole}
                  onClearRole={() => setFocusedRoleId(null)}
                  onLeave={() => onOpenChange(false)}
                />
              ) : selectedRole ? (
                <RoleDetail
                  cockpit={cockpit}
                  role={selectedRole}
                  onUpdated={setCockpit}
                  onBack={() => setPanel(null)}
                />
              ) : (
                <MaterialPanel
                  cockpit={cockpit}
                  onUpdated={setCockpit}
                  onBack={() => setPanel(null)}
                />
              )}
            </aside>
          ) : null}
        </div>
      )}
    </ResponsiveModal>
  );
}

/* ── The chrome ────────────────────────────────────────────────────────── */

/* The bar's two affordances are peers and must stay identical: the
 * conversation is reached exactly the way the material panel is. They keep the
 * h-9 look the bar was built around, and take the hit area to 44px through the
 * padding either side rather than by growing the chrome, so a thumb lands on
 * them on a phone. */
const BAR_ACTION =
  "relative shrink-0 text-quiet after:absolute after:inset-x-0 after:-inset-y-1 after:content-['']";

/** The whole of the chrome: a dot, the live state of the board in words, and
 *  the way through to everything the pipeline is built on.
 *
 *  The bar is never empty — it carries the state line from the moment the
 *  board loads, so the modal's close button has something to sit against
 *  rather than floating over a blank rule. The `pr-12 sm:pr-14` clearance is
 *  what keeps the state line out from under that button, and matches the
 *  coaching cockpit's bar exactly.
 *
 *  The pursuit title stays as the modal's accessible name (ResponsiveModal
 *  renders it sr-only when a custom header is supplied), so showing it here
 *  while the board loads is a stand-in, never a second copy. */
function BoardBar({
  title,
  state,
  materialOpen,
  onToggleMaterial,
  chatOpen,
  onToggleChat,
  showActions,
}: {
  title: string;
  state?: string;
  materialOpen: boolean;
  onToggleMaterial: () => void;
  chatOpen: boolean;
  onToggleChat: () => void;
  showActions: boolean;
}) {
  return (
    <header className="flex shrink-0 items-center gap-1 border-b border-hairline px-4 py-2.5 pr-12 sm:px-5 sm:pr-14">
      <StatusDot tone="brand" />
      <span className="ml-2 min-w-0 flex-1 truncate font-mono text-[11px] uppercase tracking-[0.08em] text-quiet">
        {state ?? title}
      </span>
      {showActions ? (
        <>
          <Button
            variant="ghost"
            size="sm"
            onClick={onToggleChat}
            aria-expanded={chatOpen}
            aria-controls="jh-panel"
            className={cn(BAR_ACTION, chatOpen && "text-foreground")}
          >
            <MessageSquare className="size-4" aria-hidden />
            <span className="hidden sm:inline">Ask Jarvis</span>
            <span className="sr-only sm:hidden">Ask Jarvis</span>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={onToggleMaterial}
            aria-expanded={materialOpen}
            aria-controls="jh-panel"
            className={cn(BAR_ACTION, materialOpen && "text-foreground")}
          >
            Material
            <ChevronRight
              className={cn("size-4 transition-transform duration-150", materialOpen && "rotate-90")}
              aria-hidden
            />
          </Button>
        </>
      ) : null}
    </header>
  );
}

/* ── The pipeline ──────────────────────────────────────────────────────── */

/** The board. Columns on a laptop, one stage at a time on a phone.
 *
 *  A phone gets a stage picker and a vertical list rather than a board that
 *  scrolls sideways: a kanban squeezed onto 375px is either nine unreadable
 *  strips or a horizontal scroll nobody finds. */
function Pipeline({
  cockpit,
  activeStage,
  onStage,
  selectedRoleId,
  onOpenRole,
}: {
  cockpit: JHCockpitData;
  activeStage: string;
  onStage: (stage: string) => void;
  selectedRoleId: string | null;
  onOpenRole: (roleId: string) => void;
}) {
  const stages = cockpit.vocabulary.role_stages;
  const counts = cockpit.summary.roles_by_stage;

  if (cockpit.roles.length === 0) {
    return (
      <div className="mx-auto min-w-0 max-w-[38rem] px-4 py-10 sm:px-6">
        <p className="font-voice text-[15.5px] leading-[1.6] text-foreground">
          Nothing on the board yet.
        </p>
        <p className="pt-2 font-voice text-[15.5px] leading-[1.6] text-quiet">
          Roles I find get filed here and line up by stage, from the first sighting through to an
          offer. The sweep has not filed anything so far.
        </p>
      </div>
    );
  }

  return (
    <div className="min-h-0 min-w-0 flex-1 overflow-auto scroll-touch">
      {/* Phone: pick a stage, then read down it. */}
      <div className="min-w-0 px-4 pb-10 pt-4 lg:hidden">
        <NativeSelect
          value={activeStage}
          onValueChange={onStage}
          aria-label="Stage"
          className="w-full"
        >
          {stages.map((s) => (
            <option key={s} value={s}>
              {`${stageLabel(s)} (${counts[s] ?? 0})`}
            </option>
          ))}
        </NativeSelect>
        <div className="pt-2">
          <StageRoles
            roles={cockpit.roles.filter((r) => r.stage === activeStage)}
            selectedRoleId={selectedRoleId}
            onOpenRole={onOpenRole}
          />
        </div>
      </div>

      {/* Laptop: the columns side by side, in pipeline order. One scroll
          container for BOTH axes: a nested `overflow-x-auto` here would also
          become `overflow-y: auto` (CSS forces it) and, with overscroll
          contained, swallow the trackpad's vertical scroll. */}
      <div className="hidden min-w-0 px-5 pb-10 pt-3 lg:block">
        <div className="flex w-max items-start gap-6">
          {stages.map((s) => (
            <section key={s} className="w-[16rem] shrink-0">
              <GroupLabel label={stageLabel(s)} count={counts[s] ?? 0} />
              <StageRoles
                roles={cockpit.roles.filter((r) => r.stage === s)}
                selectedRoleId={selectedRoleId}
                onOpenRole={onOpenRole}
              />
            </section>
          ))}
        </div>
      </div>
    </div>
  );
}

/** The roles at one stage, or a plain admission that there are none. Shared by
 *  both breakpoints so a column and a phone list can never disagree about what
 *  a role card shows. */
function StageRoles({
  roles,
  selectedRoleId,
  onOpenRole,
}: {
  roles: JHRole[];
  selectedRoleId: string | null;
  onOpenRole: (roleId: string) => void;
}) {
  if (roles.length === 0) {
    return <p className="py-2 text-[13px] text-quiet">Nothing at this stage.</p>;
  }
  return (
    <>
      {roles.map((role) => (
        <RoleCard
          key={role.id}
          role={role}
          selected={role.id === selectedRoleId}
          onOpen={() => onOpenRole(role.id)}
        />
      ))}
    </>
  );
}

/** One role on the board.
 *
 *  Everything he needs to triage without opening it: who, what, what it pays,
 *  where, how well it fits, where it was found, and whether the posting looks
 *  like a ghost. The pay is `compLabel`, which never renders an unstated
 *  salary as a number. */
function RoleCard({
  role,
  selected,
  onOpen,
}: {
  role: JHRole;
  selected: boolean;
  onOpen: () => void;
}) {
  const fit = fitLabel(role);
  return (
    <WorkRow
      kind={sourceLabel(role.source)}
      title={role.company}
      meta={role.role_title}
      summary={[compLabel(role), locationLabel(role), fit].filter(Boolean).join(" · ")}
      status={hasGhostRisk(role) ? "Ghost risk" : undefined}
      tone={roleTone(role)}
      onClick={onOpen}
      className={cn(selected && "bg-accent/60")}
    />
  );
}
