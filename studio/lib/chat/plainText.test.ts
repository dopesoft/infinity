import { describe, expect, it } from "vitest";

import { stripMarkdown } from "./plainText";

// The boss saw this, verbatim, in a monospace block in his chat:
//
//   **Planning agent termination and cancellation****Evaluating workflow
//   cancellation methods**
//
// Asterisks are syntax, not content — and the two headings had run together
// into one unreadable word. These pin both halves of that fix.
describe("stripMarkdown", () => {
  it("renders the exact trace the boss complained about as two readable lines", () => {
    const got = stripMarkdown(
      "**Planning agent termination and cancellation****Evaluating workflow cancellation methods**",
    );
    expect(got).toBe(
      "Planning agent termination and cancellation\nEvaluating workflow cancellation methods",
    );
    expect(got).not.toContain("*");
  });

  it("drops emphasis, headings, code ticks and link syntax", () => {
    expect(stripMarkdown("**bold** and *italic* and `code`")).toBe("bold and italic and code");
    expect(stripMarkdown("### A heading\nbody")).toBe("A heading\nbody");
    expect(stripMarkdown("see [the docs](https://x.dev/y)")).toBe("see the docs");
    expect(stripMarkdown("***all three***")).toBe("all three");
    expect(stripMarkdown("- one\n- two")).toBe("one\ntwo");
  });

  it("leaves ordinary prose exactly as it was", () => {
    const plain = "Reading useChat.ts to find where the steer echo is folded in.";
    expect(stripMarkdown(plain)).toBe(plain);
    expect(stripMarkdown("")).toBe("");
  });

  it("does not eat asterisks that are not emphasis", () => {
    // A glob or a multiplication is content. Emphasis needs a closing partner
    // and no surrounding whitespace; these have neither.
    expect(stripMarkdown("run go build ./... and rg '*.go'")).toContain("*.go");
    expect(stripMarkdown("2 * 3 = 6")).toBe("2 * 3 = 6");
    expect(stripMarkdown("a_b_c_d")).toBe("a_b_c_d");
  });
});
