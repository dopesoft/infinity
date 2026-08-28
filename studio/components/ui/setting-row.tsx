import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * SettingRow — the ONE shape a setting takes (Majordomo §2, §5).
 *
 * Settings is a page of repeated shapes: a labelled toggle, a labelled
 * select, a labelled number field, a labelled read-only fact. Before this
 * primitive each of those was hand-rolled per section (a bordered
 * `rounded-md bg-muted/30` label around a bare checkbox here, a `FieldLabel`
 * + `NativeSelect` stack there, a `<li className="border-t">` in
 * Notifications), which is exactly the per-screen drift the reuse-first rule
 * bans. Every settings control now routes through this row.
 *
 * Anatomy — one level deep, no container of its own:
 *   [ label            ]                 [ control ]
 *   [ description      ]
 *   [ full-width child (select / input / textarea)  ]
 *
 *  - Hairline-separated (`border-hairline`), never bordered, never tinted:
 *    the section is the container, the row is just a row (§1.2).
 *  - `min-h-11` so every row clears the 44px touch target on a phone.
 *  - The description STAYS (§1.5): on a setting row the grey sentence says
 *    what turning it on does, which is a decision aid, not furniture. It is
 *    the page/section titles that lose their restating paragraphs.
 *  - `control` sits right-aligned on the label's row (a Switch, a Button, a
 *    chip, a quiet value). `children` render full-width UNDER the label, for
 *    controls that need the width (select, input, textarea).
 *
 * Renders as a `<label>` when `htmlFor` is passed so the label text is a tap
 * target for its control; otherwise a plain `<div>` (a `<label>` wrapping two
 * interactive controls is an a11y bug, so the caller opts in).
 */
export interface SettingRowProps {
  /** Chrome-face name of the setting. Short: "Agent teams", not a sentence. */
  label: React.ReactNode;
  /** What flipping this does. Kept — on a setting row it is a decision aid. */
  description?: React.ReactNode;
  /** Right-aligned control on the label's row: Switch, Button, chip, value. */
  control?: React.ReactNode;
  /** Full-width control under the label: select, input, textarea. */
  children?: React.ReactNode;
  /** Associates the row's label element with a control id (renders a label). */
  htmlFor?: string;
  /** Drop the bottom hairline (last row in a group that closes itself). */
  noRule?: boolean;
  /** Dim the row (control unavailable). Does not disable the control itself. */
  disabled?: boolean;
  className?: string;
}

export function SettingRow({
  label,
  description,
  control,
  children,
  htmlFor,
  noRule,
  disabled,
  className,
}: SettingRowProps) {
  const Tag = (htmlFor ? "label" : "div") as "label" | "div";
  return (
    <Tag
      {...(htmlFor ? { htmlFor } : {})}
      className={cn(
        "flex min-h-11 min-w-0 max-w-full flex-col gap-2 py-2.5",
        !noRule && "border-b border-hairline last:border-b-0",
        disabled && "opacity-60",
        className,
      )}
    >
      <div className="flex min-w-0 items-center justify-between gap-3">
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="min-w-0 font-sans text-[13.5px] font-medium text-foreground">
            {label}
          </span>
          {description ? (
            <span className="min-w-0 text-[12px] leading-relaxed text-quiet [overflow-wrap:anywhere]">
              {description}
            </span>
          ) : null}
        </span>
        {control ? (
          <span className="flex shrink-0 items-center gap-2">{control}</span>
        ) : null}
      </div>
      {children ? <div className="min-w-0 max-w-full">{children}</div> : null}
    </Tag>
  );
}
