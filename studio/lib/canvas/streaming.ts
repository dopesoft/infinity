/**
 * Partial-JSON extraction for live tool-input streaming.
 *
 * As the model writes a tool call's arguments, Core forwards raw partial-JSON
 * chunks (StreamToolInputDelta → `tool_input_delta` WS event). We accumulate
 * those chunks per tool id and, on each delta, best-effort pull out:
 *   • the file PATH being written (so the canvas can open the right tab), and
 *   • the CONTENT being written (so it types in live).
 *
 * The input is INCOMPLETE JSON — e.g. `{"file_path":"/x/y.tsx","content":"impo`
 * — so we can't JSON.parse it. We scan for the relevant keys and decode the
 * string value up to wherever the stream currently ends. This is intentionally
 * best-effort: the authoritative full content always arrives later in the
 * complete `tool_call` event, which reconciles whatever we showed.
 *
 * Model-agnostic: every provider emits the same tool-arg JSON shape
 * (`{"file_path":...,"new_string":...}` / `{"path":...,"content":...}`), so
 * this works regardless of which LLM produced the stream.
 */

// Keys that name the file being written, in priority order. Mirrors
// detection.ts::extractToolFilePath so streaming and the final tool_call agree.
const PATH_KEYS = ["file_path", "path", "filepath", "filename", "file"];
// Keys whose value is the content being written, in priority order.
// new_string = Edit-shaped; content = Write/Save-shaped.
const CONTENT_KEYS = ["new_string", "content"];

type DecodedPrefix = { value: string; complete: boolean };

/**
 * decodeJSONStringPrefix decodes a JSON string literal starting at `start`
 * (the index just AFTER the opening quote) up to the closing unescaped quote
 * or the end of the (possibly truncated) buffer. Handles standard JSON escapes
 * and tolerates a trailing partial escape (drops it) so mid-stream buffers
 * decode cleanly.
 */
function decodeJSONStringPrefix(raw: string, start: number): DecodedPrefix {
  let out = "";
  let i = start;
  while (i < raw.length) {
    const ch = raw[i];
    if (ch === '"') {
      return { value: out, complete: true }; // closing quote — value done
    }
    if (ch === "\\") {
      if (i + 1 >= raw.length) {
        // Trailing lone backslash: escape not yet streamed. Stop here.
        return { value: out, complete: false };
      }
      const esc = raw[i + 1];
      switch (esc) {
        case "n": out += "\n"; i += 2; break;
        case "t": out += "\t"; i += 2; break;
        case "r": out += "\r"; i += 2; break;
        case "b": out += "\b"; i += 2; break;
        case "f": out += "\f"; i += 2; break;
        case '"': out += '"'; i += 2; break;
        case "\\": out += "\\"; i += 2; break;
        case "/": out += "/"; i += 2; break;
        case "u": {
          if (i + 6 > raw.length) {
            // Incomplete \uXXXX at the buffer edge — wait for more.
            return { value: out, complete: false };
          }
          const hex = raw.slice(i + 2, i + 6);
          const code = parseInt(hex, 16);
          if (Number.isNaN(code)) {
            return { value: out, complete: false };
          }
          out += String.fromCharCode(code);
          i += 6;
          break;
        }
        default:
          // Unknown escape — keep the char literally and move on.
          out += esc;
          i += 2;
          break;
      }
      continue;
    }
    out += ch;
    i += 1;
  }
  return { value: out, complete: false }; // ran out of buffer — still streaming
}

/** Find the byte offset just after the opening quote of `"<key>"\s*:\s*"`. */
function findValueStart(raw: string, key: string): number {
  const keyToken = `"${key}"`;
  let from = 0;
  for (;;) {
    const k = raw.indexOf(keyToken, from);
    if (k < 0) return -1;
    let j = k + keyToken.length;
    while (j < raw.length && (raw[j] === " " || raw[j] === "\t" || raw[j] === "\n" || raw[j] === "\r")) j += 1;
    if (raw[j] !== ":") { from = k + keyToken.length; continue; }
    j += 1;
    while (j < raw.length && (raw[j] === " " || raw[j] === "\t" || raw[j] === "\n" || raw[j] === "\r")) j += 1;
    if (raw[j] !== '"') { from = k + keyToken.length; continue; }
    return j + 1; // index just after the opening quote
  }
}

/**
 * extractStreamingPath returns the file path from accumulated partial JSON,
 * but ONLY once the value is complete (closing quote seen) — a half-streamed
 * path would open the wrong tab. Returns null until then.
 */
export function extractStreamingPath(raw: string): string | null {
  for (const key of PATH_KEYS) {
    const start = findValueStart(raw, key);
    if (start < 0) continue;
    const { value, complete } = decodeJSONStringPrefix(raw, start);
    if (complete && value.trim()) return value;
    if (!complete) return null; // path is mid-stream; wait for the rest
  }
  return null;
}

/**
 * extractStreamingContent returns the (possibly partial) decoded content being
 * written, for the live editor view. Returns null when no content key has
 * started streaming yet.
 */
export function extractStreamingContent(raw: string): string | null {
  for (const key of CONTENT_KEYS) {
    const start = findValueStart(raw, key);
    if (start < 0) continue;
    const { value } = decodeJSONStringPrefix(raw, start);
    return value;
  }
  return null;
}
