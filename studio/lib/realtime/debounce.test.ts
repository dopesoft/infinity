import { afterEach, describe, expect, it, vi } from "vitest";
import { trailingDebounce } from "./debounce";

describe("trailingDebounce", () => {
  afterEach(() => vi.useRealTimers());

  it("turns fifteen events in a second into one call, with the last arguments", () => {
    vi.useFakeTimers();
    const calls: number[] = [];
    const d = trailingDebounce((n: number) => calls.push(n), 500);
    for (let i = 1; i <= 15; i++) {
      d(i);
      vi.advanceTimersByTime(60);
    }
    expect(calls).toEqual([]);
    vi.advanceTimersByTime(500);
    expect(calls).toEqual([15]);
  });

  it("fires again after a quiet spell, and cancel drops a pending call", () => {
    vi.useFakeTimers();
    const calls: string[] = [];
    const d = trailingDebounce((s: string) => calls.push(s), 100);
    d("a");
    vi.advanceTimersByTime(100);
    d("b");
    vi.advanceTimersByTime(100);
    expect(calls).toEqual(["a", "b"]);
    d("c");
    d.cancel();
    vi.advanceTimersByTime(200);
    expect(calls).toEqual(["a", "b"]);
  });
});
