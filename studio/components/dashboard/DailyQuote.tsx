import type { DailyQuote as DailyQuoteData } from "@/lib/dashboard/types";

/**
 * DailyQuote — the line under the greeting.
 *
 * One quote per day, assigned server-side against the boss's local day, so it
 * is the same line all day and the same line on his phone as on his desk. The
 * rotation drains the whole corpus before repeating anything (see
 * core/internal/dashboard/quote.go).
 *
 * WHY THIS IS ITS OWN ELEMENT RATHER THAN A PRIMITIVE WE ALREADY HAVE, since
 * the reuse-first rule requires the question to be answered:
 *
 *  - Not `Inset variant="quote"`. `Inset` is contractually the container
 *    allowed *inside a row* (MAJORDOMO §2) and paints a tinted `bg-muted`
 *    block. This sits on the page ground in the header column, unboxed.
 *  - Not `PageHeader.meta`. Meta is the ONE quiet 12.5px line under the title
 *    and it already carries "Three things need you." Two meta lines is how a
 *    header turns back into a status bar.
 *
 * It is the second and last consumer of the `display` register (MAJORDOMO §4,
 * amended 2026-08-30); the greeting is the first. Nothing else may use it.
 *
 * Renders nothing at all when there is no quote — a cold cache, a corpus that
 * has not been seeded yet, or a loader that errored. An empty slot reserving
 * space for a line that never comes is worse than no line.
 */
export function DailyQuote({ quote }: { quote?: DailyQuoteData | null }) {
  if (!quote?.text) return null;

  return (
    <figure className="min-w-0 max-w-prose">
      <blockquote className="min-w-0 font-display text-[18px] italic leading-snug text-foreground sm:text-[20px]">
        {/* Curly quotes, added here rather than stored in the row - same
            convention as `Inset variant="quote"`, which is the only other
            place the product renders a quotation. Keeping them out of the
            corpus means the text stays clean for anything else that reads it
            and there is one place to change the style. */}
        {`\u201C${quote.text}\u201D`}
      </blockquote>
      <figcaption className="mt-1.5 min-w-0 truncate font-sans text-[12px] not-italic text-quiet">
        {quote.author}
        {quote.source ? <span className="opacity-70"> · {quote.source}</span> : null}
      </figcaption>
    </figure>
  );
}
