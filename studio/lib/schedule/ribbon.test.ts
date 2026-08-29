import { describe, expect, it } from "vitest";
import { placeMarks, positionPct, ticks, RIBBON_WINDOW_MS } from "./ribbon";

// A fixed instant so these never depend on the clock. 2026-08-29 09:00 local.
const NOW = new Date(2026, 7, 29, 9, 0, 0).getTime();
const h = (n: number) => NOW + n * 60 * 60 * 1000;

describe("positionPct", () => {
  it("puts now at the left edge and the far end at the right", () => {
    expect(positionPct(NOW, NOW)).toBe(0);
    expect(positionPct(NOW, NOW + RIBBON_WINDOW_MS)).toBe(100);
  });

  it("places a mark proportionally through the window", () => {
    expect(positionPct(NOW, h(6))).toBeCloseTo(25);
    expect(positionPct(NOW, h(12))).toBeCloseTo(50);
  });

  // The reason this returns null rather than clamping: a clamped mark draws
  // a job at midnight that actually fires next week, which is the opposite of
  // answering "is tonight covered".
  it("refuses to place anything outside the window", () => {
    expect(positionPct(NOW, h(-1))).toBeNull();
    expect(positionPct(NOW, h(25))).toBeNull();
  });
});

describe("ticks", () => {
  it("labels the window in short casual time, never 24h", () => {
    expect(ticks(NOW).map((t) => t.label)).toEqual(["9am", "3pm", "9pm", "3am", "9am"]);
  });
});

describe("placeMarks", () => {
  it("orders marks by time regardless of input order", () => {
    const { placed } = placeMarks(NOW, [
      { id: "late", label: "Improve my code", at: h(18) },
      { id: "early", label: "Nadia watch", at: h(2) },
    ]);
    expect(placed.map((m) => m.id)).toEqual(["early", "late"]);
  });

  // A schedule page that silently omits a job is worse than one that admits
  // it ran out of room, so a collided mark is reported, not dropped.
  it("thins colliding marks and reports what it hid", () => {
    const { placed, hidden } = placeMarks(NOW, [
      { id: "a", label: "Read the inbox", at: h(1) },
      { id: "b", label: "Prep the call", at: h(1.1) },
    ]);
    expect(placed.map((m) => m.id)).toEqual(["a"]);
    expect(hidden.map((m) => m.id)).toEqual(["b"]);
  });

  it("reports out-of-window marks as hidden rather than discarding them", () => {
    const { placed, hidden } = placeMarks(NOW, [
      { id: "next-week", label: "Weekly summary", at: h(72) },
    ]);
    expect(placed).toHaveLength(0);
    expect(hidden.map((m) => m.id)).toEqual(["next-week"]);
  });
});
