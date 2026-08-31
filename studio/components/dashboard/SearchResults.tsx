"use client";

import { GroupLabel, ListRow } from "@/components/ui/list-row";
import { Section } from "@/components/dashboard/Section";
import { kindMeta } from "@/lib/search/useGlobalSearch";
import type { SearchHit } from "@/lib/api";

/**
 * SearchResults — what the dashboard shows while a query is active.
 *
 * The dashboard's search box used to filter only the rows already on screen,
 * which meant the one search field on the page he actually looks at could not
 * find anything he could not already see. It runs the SAME global search as
 * ⌘K now, and its results take over the page: sections out, one Search Results
 * section in.
 *
 * Zero new visual shapes. `Section` for the header and the count, `GroupLabel`
 * per kind, `ListRow` per hit — which already owns the 44px touch height, the
 * hover ground, the hairline and the truncate chain, so this file gets those
 * right by construction rather than by remembering to.
 *
 * THREE TERMINAL STATES, AND THEY ARE DIFFERENT ON PURPOSE. "Nothing matches"
 * and "I could not search" look identical if you let them, and the second one
 * silently tells the boss his thing does not exist. `failed` says what
 * actually happened.
 */
export function SearchResults({
  query,
  groups,
  count,
  loading,
  failed,
  onOpen,
}: {
  query: string;
  groups: [string, SearchHit[]][];
  count: number;
  loading: boolean;
  failed: boolean;
  onOpen: (hit: SearchHit) => void;
}) {
  const settled = !loading;

  return (
    <Section
      title="Search results"
      badge={count > 0 ? count : undefined}
      tone="plain"
      noPad={count > 0}
      contentClassName={count > 0 ? "pt-1" : undefined}
    >
      {failed ? (
        <p className="py-2 text-[13px] leading-relaxed text-quiet">
          I could not run that search just now, so this is not an answer about
          whether {`“${query.trim()}”`} exists. Core is not responding.
        </p>
      ) : count === 0 ? (
        <p className="py-2 text-[13px] leading-relaxed text-quiet">
          {settled
            ? `Nothing matches “${query.trim()}”.`
            : `Looking for “${query.trim()}”…`}
        </p>
      ) : (
        groups.map(([kind, hits]) => {
          const { label, Icon } = kindMeta(kind);
          return (
            <div key={kind} className="min-w-0">
              <GroupLabel label={label} count={hits.length} />
              {hits.map((hit) => (
                <ListRow
                  key={`${hit.kind}:${hit.id}`}
                  leading={<Icon className="size-4" aria-hidden />}
                  title={hit.title}
                  meta={hit.meta}
                  onClick={() => onOpen(hit)}
                />
              ))}
            </div>
          );
        })
      )}
    </Section>
  );
}
