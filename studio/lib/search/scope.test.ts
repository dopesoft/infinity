import { describe, expect, it } from "vitest";
import { elsewhere, scopePlaceholder } from "./scope";

const TABS = [
  { id: "facts", label: "Facts" },
  { id: "lessons", label: "Lessons" },
  { id: "wrong", label: "Wrong guesses" },
];

describe("elsewhere", () => {
  // This is the whole point of the helper: a scoped search that can hide a
  // match in another tab is a trap, so an empty tab has to say where the
  // thing actually is.
  it("points at the other tabs when the active one is empty", () => {
    const res = elsewhere("facts", TABS, { facts: 0, lessons: 3, wrong: 1 });
    expect(res?.hits.map((h) => h.id)).toEqual(["lessons", "wrong"]);
    expect(res?.total).toBe(4);
  });

  it("says nothing when the active tab has matches", () => {
    expect(elsewhere("facts", TABS, { facts: 2, lessons: 3 })).toBeNull();
  });

  // Pointing at zero other tabs is noise; a plain "nothing matches" is the
  // honest answer.
  it("says nothing when nothing matches anywhere", () => {
    expect(elsewhere("facts", TABS, { facts: 0, lessons: 0, wrong: 0 })).toBeNull();
  });

  it("treats a missing count as zero rather than throwing", () => {
    const res = elsewhere("facts", TABS, { lessons: 1 });
    expect(res?.hits.map((h) => h.id)).toEqual(["lessons"]);
  });
});

describe("scopePlaceholder", () => {
  it("names the scope and the size out loud", () => {
    expect(scopePlaceholder("Facts", 12481)).toBe("Search 12,481 facts");
  });
  it("drops the number when there is not one", () => {
    expect(scopePlaceholder("Connections")).toBe("Search connections");
  });
});
