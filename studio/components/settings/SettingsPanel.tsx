"use client";

import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * SettingsPanel — the frame EVERY Settings section renders inside.
 *
 * ONE STRUCTURE, NO EXCEPTIONS:
 *
 *     [ sub-tabs, if the section has them ]
 *     [ content ]
 *
 * That is the whole component, and the props are deliberately the whole
 * component too. There is no title, no description, no count, no action slot —
 * not because those things do not exist, but because an OPTIONAL slot is how
 * this went wrong twice.
 *
 * FIRST ATTEMPT: each section drew its own header. Ten sections, five
 * treatments — some a big title, some nothing, one wrapped in a card nothing
 * else used. On a phone the header moved, appeared and vanished as you swiped
 * the section chips.
 *
 * SECOND ATTEMPT: this component took `title`, `meta` and `action`, all
 * optional. Which produced exactly the same complaint in a new coat: some
 * panels had a sentence above the sub-tabs and some had nothing, because an
 * optional slot means each consumer still decides. The boss, twice: "I want
 * uniformity and consistency."
 *
 * So the slot is gone. A count belongs on the thing it counts — the tab badge,
 * or the group label above the list. An explanation belongs on the control it
 * explains, which is the house rule everywhere else. An action belongs beside
 * what it acts on. None of them belong in a header that only some sections
 * have, and the only way to guarantee that is to leave nowhere to put them.
 *
 * If a section seems to need a header, it needs a group heading inside its
 * content instead. Those name a GROUP ("Teamwork", "Where things go") and are
 * fine; a heading that names the SECTION is the tab said twice.
 */
export function SettingsPanel({
  /** A `<PageTabs>` strip, always at level="sub". Omit if the section has none. */
  tabs,
  children,
  className,
}: {
  tabs?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col", className)}>
      {tabs}
      {/* No padding here on purpose. The gap under a tab strip belongs to the
          strip (PageTabsList carries mb-6), so it is identical whether the
          strip is a page's primary row or a section's sub-row, and a panel
          cannot add a second helping on top of it. */}
      <div className="min-w-0">{children}</div>
    </div>
  );
}
