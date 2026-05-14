"use client";

import { useMemo, useState } from "react";
import { Link2 } from "lucide-react";
import { cn } from "@/lib/utils";

/* Citations — pill row of URL chips rendered below an assistant reply.
 *
 * Visual contract matches the suggestion-chip pill style: rounded-full,
 * thin border, subtle card background, a 16px icon on the left and the
 * source label on the right. The icon is the site's favicon, fetched
 * from DuckDuckGo's privacy-friendly icon service; if it 404s we fall
 * back to a generic link glyph so the pill never collapses.
 *
 * URL extraction runs against the rendered text — every unique http(s)
 * URL in the message body becomes one chip, deduped by host+pathname
 * so the same article cited twice doesn't repeat.
 */

const URL_REGEX = /https?:\/\/[^\s)<>\]"']+/g;

type Citation = {
  url: string;
  host: string;
  label: string;
};

function buildCitations(text: string): Citation[] {
  const seen = new Set<string>();
  const out: Citation[] = [];
  const matches = text.match(URL_REGEX) ?? [];
  for (const raw of matches) {
    // Strip trailing punctuation the regex couldn't have known wasn't
    // part of the URL (periods, commas, semicolons, colons).
    const cleaned = raw.replace(/[.,;:!?]+$/, "");
    let u: URL;
    try {
      u = new URL(cleaned);
    } catch {
      continue;
    }
    // Dedupe key: host + pathname (ignore query/hash) so the same
    // article linked twice from different anchors collapses to one chip.
    const key = u.host + u.pathname.replace(/\/$/, "");
    if (seen.has(key)) continue;
    seen.add(key);
    // Strip leading "www." for a tighter label.
    const host = u.host.replace(/^www\./, "");
    out.push({ url: cleaned, host: u.host, label: host });
  }
  return out;
}

export function Citations({ text }: { text: string }) {
  const citations = useMemo(() => buildCitations(text), [text]);
  if (citations.length === 0) return null;
  return (
    <div className="mt-2 flex flex-col gap-1">
      <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        Sources
      </div>
      <div className="flex flex-wrap gap-1.5">
        {citations.map((c) => (
          <CitationChip key={c.url} citation={c} />
        ))}
      </div>
    </div>
  );
}

function CitationChip({ citation }: { citation: Citation }) {
  const [iconBroken, setIconBroken] = useState(false);
  return (
    <a
      href={citation.url}
      target="_blank"
      rel="noreferrer noopener"
      title={citation.url}
      className={cn(
        "group inline-flex items-center gap-1.5 rounded-full border border-border bg-card/60 px-2.5 py-1",
        "text-[12px] text-foreground/80 transition-colors",
        "hover:border-foreground/30 hover:bg-card hover:text-foreground",
        "max-w-[14rem]",
      )}
    >
      {iconBroken ? (
        <Link2 className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
      ) : (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={`https://icons.duckduckgo.com/ip3/${citation.host}.ico`}
          alt=""
          width={14}
          height={14}
          className="size-3.5 shrink-0 rounded-sm"
          onError={() => setIconBroken(true)}
          loading="lazy"
        />
      )}
      <span className="truncate">{citation.label}</span>
    </a>
  );
}
