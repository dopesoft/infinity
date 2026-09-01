import { afterEach, describe, expect, it, vi } from "vitest";
import {
  beginLoading,
  endLoading,
  loadingSnapshot,
  subscribeLoading,
} from "./store";

/**
 * These test the two rules the mark's usefulness rests on, not the plumbing.
 *
 *  1. A tap is ALWAYS answered. If the screen arrives in 12ms the mark is
 *     still shown long enough to be seen, because "did that register?" is the
 *     question it exists to answer and a one-frame flash answers nothing.
 *  2. A hold ALWAYS ends. A spinner that never stops says "still working"
 *     about something that isn't, which is the UI version of a false green.
 */
describe("app loading store", () => {
  afterEach(() => {
    endLoading("a");
    endLoading("b");
    vi.useRealTimers();
  });

  it("shows immediately and holds for the minimum even on an instant load", () => {
    vi.useFakeTimers();
    beginLoading("a");
    expect(loadingSnapshot()).toBe(true);

    endLoading("a");
    // Still up: the load finished before he could see it register.
    expect(loadingSnapshot()).toBe(true);

    vi.advanceTimersByTime(349);
    expect(loadingSnapshot()).toBe(true);
    vi.advanceTimersByTime(1);
    expect(loadingSnapshot()).toBe(false);
  });

  it("is one spinner for two concurrent holds, and stays up until both end", () => {
    vi.useFakeTimers();
    beginLoading("a");
    beginLoading("b");
    vi.advanceTimersByTime(400);

    endLoading("a");
    vi.advanceTimersByTime(400);
    expect(loadingSnapshot()).toBe(true);

    endLoading("b");
    vi.advanceTimersByTime(400);
    expect(loadingSnapshot()).toBe(false);
  });

  it("releases a hold nobody ended, so the mark can never spin forever", () => {
    vi.useFakeTimers();
    beginLoading("a", 1_000);
    vi.advanceTimersByTime(999);
    expect(loadingSnapshot()).toBe(true);

    vi.advanceTimersByTime(1);
    vi.advanceTimersByTime(400);
    expect(loadingSnapshot()).toBe(false);
  });

  it("notifies subscribers on both edges", () => {
    vi.useFakeTimers();
    const seen: boolean[] = [];
    const stop = subscribeLoading(() => seen.push(loadingSnapshot()));

    beginLoading("a");
    endLoading("a");
    vi.advanceTimersByTime(400);
    stop();

    expect(seen).toEqual([true, false]);
  });
});
