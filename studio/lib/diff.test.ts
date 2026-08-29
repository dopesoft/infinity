import { describe, expect, it } from "vitest";

import { buildCodeChange, changeStats, diffLineClass, looksLikeDiff } from "./diff";

/**
 * Why these exist: "UNLIKE claude which shows the file its working on and a
 * sampling of the code written." A write step now renders as a real hunk, and
 * the hunk arithmetic is the part that can be silently wrong — a diff that
 * shows the whole file as new, or drops the change, is worse than the mono
 * blob it replaced, because it looks authoritative.
 */
describe("buildCodeChange", () => {
  it("shows only what changed, with context either side", () => {
    const before = ["import a", "", "function go() {", "  return 1;", "}", "", "export {}"].join("\n");
    const after = ["import a", "", "function go() {", "  return 2;", "}", "", "export {}"].join("\n");
    const change = buildCodeChange(before, after);

    expect(change.added).toBe(1);
    expect(change.removed).toBe(1);
    // The changed line, both ways round, and NOT the untouched imports.
    expect(change.unified).toContain("-  return 1;");
    expect(change.unified).toContain("+  return 2;");
    expect(change.unified).not.toContain("import a");
    // Context is what makes a hunk readable rather than two orphan lines.
    expect(change.unified).toContain(" function go() {");
    expect(changeStats(change)).toBe("+1 −1");
  });

  it("treats a brand-new file as all additions", () => {
    const change = buildCodeChange("", "one\ntwo\nthree\n");
    expect(change.added).toBe(3);
    expect(change.removed).toBe(0);
    expect(changeStats(change)).toBe("+3");
    expect(change.unified.split("\n")).toEqual(["+one", "+two", "+three"]);
  });

  it("counts a pure deletion honestly", () => {
    const change = buildCodeChange("one\ntwo\nthree\n", "one\nthree\n");
    expect(change.removed).toBe(1);
    expect(change.added).toBe(0);
    expect(changeStats(change)).toBe("−1");
  });

  it("says how much of a long change it is not showing", () => {
    const after = Array.from({ length: 60 }, (_, i) => `line ${i}`).join("\n");
    const change = buildCodeChange("", after, 10);
    expect(change.unified.split("\n")).toHaveLength(10);
    // A silent truncation would read as "that was the whole change".
    expect(change.hidden).toBe(50);
    expect(change.added).toBe(60);
  });

  it("reports nothing when nothing moved", () => {
    const same = "a\nb\nc\n";
    const change = buildCodeChange(same, same);
    expect(change.added).toBe(0);
    expect(change.removed).toBe(0);
    expect(changeStats(change)).toBe("");
  });

  it("produces lines the shared diff tinting understands", () => {
    const change = buildCodeChange("old\n", "new\n");
    const tones = change.unified.split("\n").map(diffLineClass);
    expect(tones).toContain("bg-danger/10 text-danger");
    expect(tones).toContain("bg-success/10 text-success");
  });

  it("stays intact for a file whose lines are mostly identical", () => {
    // The O(n) head/tail trim has to find a change in the MIDDLE of a big file,
    // which is the shape every real edit has.
    const lines = Array.from({ length: 500 }, (_, i) => `line ${i}`);
    const edited = [...lines];
    edited[250] = "line 250 // changed";
    const change = buildCodeChange(lines.join("\n"), edited.join("\n"));
    expect(change.added).toBe(1);
    expect(change.removed).toBe(1);
    expect(change.unified).toContain("+line 250 // changed");
  });
});

describe("looksLikeDiff", () => {
  it("recognises a real unified diff and not ordinary prose", () => {
    expect(looksLikeDiff("@@ -1,2 +1,2 @@\n-a\n+b")).toBe(true);
    expect(looksLikeDiff("I changed the file and it builds now.")).toBe(false);
  });
});
