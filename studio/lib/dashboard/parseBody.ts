// parseLabeledBody turns a multi-line "Label: value" body string (the
// shape Jarvis emits for triaged surface items - eg. "Account: mr khaya
// \nFrom: Namecheap Support <...>\nSubject: ...\nWhy it matters: ...")
// into ordered label/value pairs the modal can render as labeled rows.
//
// A label is only recognised when:
//   - the line starts at column 0 (no leading whitespace)
//   - the colon is within the first ~32 chars (so prose like
//     "I want to share: ..." inside a value continuation does NOT start
//     a new field)
//
// Continuation lines (lines that follow a labeled line and don't
// themselves match the label pattern) are folded into the prior value
// with a newline preserved. This keeps multi-paragraph values intact
// while letting the renderer break the body into discrete fields.
//
// Returns [] when no labelled lines are detected, so the caller can
// fall back to rendering the raw body as prose.

export type LabeledField = { label: string; value: string };

const LABEL_RE = /^([A-Za-z][\w &\/-]{0,30}):\s*(.*)$/;

export function parseLabeledBody(body: string | null | undefined): LabeledField[] {
  if (!body) return [];
  const lines = body.replace(/\r\n/g, "\n").split("\n");
  const fields: LabeledField[] = [];
  let current: LabeledField | null = null;

  for (const raw of lines) {
    const line = raw.trimEnd();
    if (line === "") {
      // Blank line - preserve as a paragraph break inside the current
      // value when one is open; otherwise skip.
      if (current) current.value += current.value ? "\n\n" : "";
      continue;
    }
    const match = line.match(LABEL_RE);
    if (match && !line.startsWith(" ")) {
      const label = match[1].trim();
      const value = match[2].trim();
      // Heuristic to avoid catching prose containing a colon. A "real"
      // label is short, mostly title/sentence-case, and the value either
      // starts on the same line or on the next. Reject labels that look
      // like sentences (contain a period or end mid-sentence) - those
      // are prose, not field names.
      if (label.length > 1 && !/[.!?]/.test(label)) {
        current = { label, value };
        fields.push(current);
        continue;
      }
    }
    if (current) {
      current.value += (current.value ? "\n" : "") + line.trim();
    }
  }
  return fields.filter((f) => f.value.length > 0);
}

// extractFrom returns the sender name pulled from a parsed body's
// "From" field, when present. Used by the list-row renderer to show
// "Namecheap Support" as the row's lead text. Falls back to the raw
// "From" value when the angle-bracket pattern isn't present.
export function extractFromSender(fields: LabeledField[]): string | null {
  const fromField = fields.find((f) => f.label.toLowerCase() === "from");
  if (!fromField) return null;
  // "Namecheap Support <support@namecheap.com>" → "Namecheap Support"
  const angle = fromField.value.indexOf("<");
  const sender = angle > 0 ? fromField.value.slice(0, angle).trim() : fromField.value.trim();
  return sender.replace(/^["']|["']$/g, "") || null;
}
