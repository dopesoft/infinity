"use client";

import { memo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { CodeBlock } from "./CodeBlock";
import { cn } from "@/lib/utils";

/* Markdown - chat-tuned markdown renderer for assistant messages.
 *
 * Uses react-markdown + remark-gfm so GitHub-Flavored markdown lands
 * with tables, task lists, strikethrough, and autolinks. Element
 * mappings are sized for a chat bubble (tight vertical rhythm, no
 * giant H1s) and themed via Tailwind utilities so the renderer stays
 * consistent with the rest of Studio. No inline styles, no @apply.
 *
 * Streaming is fine - react-markdown reparses the input on each chunk
 * and renders whatever's there. Partial fences stay visually inert
 * until the closer arrives.
 *
 * Security: react-markdown is safe-by-default (raw HTML is escaped, no
 * rehype-raw). Links open in a new tab with rel="noreferrer noopener".
 */
export const Markdown = memo(function Markdown({
  text,
  className,
}: {
  text: string;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "min-w-0 max-w-full break-words text-sm leading-relaxed [overflow-wrap:anywhere] [&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
        className,
      )}
    >
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          // Paragraphs: tight top margin so consecutive paragraphs read
          // as a flowing reply, not a stack of cards.
          p: ({ children }) => (
            <p className="my-2 whitespace-pre-wrap break-words [overflow-wrap:anywhere]">
              {children}
            </p>
          ),

          // Headings - sized down. A chat bubble shouldn't host display
          // typography; these are content dividers, not section titles
          // on a marketing page.
          h1: ({ children }) => (
            <h1 className="mb-1 mt-3 min-w-0 max-w-full break-words text-base font-semibold tracking-tight [overflow-wrap:anywhere]">{children}</h1>
          ),
          h2: ({ children }) => (
            <h2 className="mb-1 mt-3 min-w-0 max-w-full break-words text-[15px] font-semibold tracking-tight [overflow-wrap:anywhere]">{children}</h2>
          ),
          h3: ({ children }) => (
            <h3 className="mb-1 mt-2 min-w-0 max-w-full break-words text-sm font-semibold tracking-tight [overflow-wrap:anywhere]">{children}</h3>
          ),
          h4: ({ children }) => (
            <h4 className="mb-1 mt-2 min-w-0 max-w-full break-words text-sm font-medium [overflow-wrap:anywhere]">{children}</h4>
          ),
          h5: ({ children }) => (
            <h5 className="mb-0.5 mt-2 min-w-0 max-w-full break-words text-[13px] font-medium uppercase tracking-wide text-muted-foreground [overflow-wrap:anywhere]">
              {children}
            </h5>
          ),
          h6: ({ children }) => (
            <h6 className="mb-0.5 mt-2 min-w-0 max-w-full break-words text-[12px] font-medium uppercase tracking-wide text-muted-foreground [overflow-wrap:anywhere]">
              {children}
            </h6>
          ),

          // Lists. GFM task lists get the native checkbox renderer; we
          // just strip the marker so the box itself does the indicating.
          ul: ({ children }) => (
            <ul className="my-2 min-w-0 max-w-full list-disc space-y-1 break-words pl-5 [overflow-wrap:anywhere] marker:text-muted-foreground">
              {children}
            </ul>
          ),
          ol: ({ children }) => (
            <ol className="my-2 min-w-0 max-w-full list-decimal space-y-1 break-words pl-5 [overflow-wrap:anywhere] marker:text-muted-foreground">
              {children}
            </ol>
          ),
          li: ({ children, ...props }) => {
            // remark-gfm sets className="task-list-item" on task list <li>.
            // Drop the disc for those so the checkbox stands alone.
            const isTask =
              typeof (props as { className?: string }).className === "string" &&
              (props as { className?: string }).className!.includes("task-list-item");
            return (
              <li className={cn("min-w-0 break-words [overflow-wrap:anywhere]", isTask && "list-none -ml-5 flex items-start gap-2")}>
                {children}
              </li>
            );
          },

          // Inline emphasis.
          strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
          em: ({ children }) => <em className="italic">{children}</em>,
          del: ({ children }) => (
            <del className="text-muted-foreground line-through">{children}</del>
          ),

          // Block quote - left rule + muted text. Inherits inner spacing
          // from the elements it contains.
          blockquote: ({ children }) => (
            <blockquote className="my-2 min-w-0 max-w-full break-words border-l-2 border-border pl-3 text-muted-foreground [overflow-wrap:anywhere]">
              {children}
            </blockquote>
          ),

          // Horizontal rule.
          hr: () => <hr className="my-3 border-border" />,

          // Links - always external in chat. Underline-on-hover keeps
          // the body legible while still signaling interactivity.
          a: ({ href, children }) => (
            <a
              href={href}
              target="_blank"
              rel="noreferrer noopener"
              className="break-words text-info underline decoration-info/40 decoration-1 underline-offset-2 transition-colors [overflow-wrap:anywhere] hover:decoration-info"
            >
              {children}
            </a>
          ),

          // Tables. Wrap in a scroller so narrow phones don't break
          // layout when a wide table arrives.
          table: ({ children }) => (
            <div className="scroll-touch my-2 min-w-0 max-w-full overflow-x-auto rounded-lg border">
              <table className="w-full text-left text-[12.5px]">{children}</table>
            </div>
          ),
          thead: ({ children }) => (
            <thead className="bg-muted/40 text-muted-foreground">{children}</thead>
          ),
          tbody: ({ children }) => <tbody>{children}</tbody>,
          tr: ({ children }) => (
            <tr className="border-b last:border-b-0">{children}</tr>
          ),
          th: ({ children }) => (
            <th className="max-w-64 break-words px-2.5 py-1.5 text-[11px] font-semibold uppercase tracking-wide [overflow-wrap:anywhere]">
              {children}
            </th>
          ),
          td: ({ children }) => (
            <td className="max-w-64 break-words px-2.5 py-1.5 align-top [overflow-wrap:anywhere]">{children}</td>
          ),

          // Code. The `pre` renderer is a transparent passthrough so the
          // CodeBlock below isn't double-wrapped. `code` switches on
          // language hint / newline to distinguish inline from block.
          pre: ({ children }) => <>{children}</>,
          code: ({ className, children, ...props }) => {
            const text = String(children ?? "");
            const langMatch = /language-(\w+)/.exec(className || "");
            const isBlock = !!langMatch || text.includes("\n");
            if (isBlock) {
              return (
                <CodeBlock language={langMatch?.[1]}>
                  {text.replace(/\n$/, "")}
                </CodeBlock>
              );
            }
            return (
              <code
                className="break-words rounded bg-foreground/10 px-1 py-0.5 font-mono text-[0.88em] [overflow-wrap:anywhere]"
                {...props}
              >
                {children}
              </code>
            );
          },
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
});
