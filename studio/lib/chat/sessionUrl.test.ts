import { describe, expect, it } from "vitest";
import { isRestoredSession, nextSessionHref } from "./sessionUrl";

describe("nextSessionHref", () => {
  // The bug: arriving at a chat from the dashboard, then switching chats, left
  // the old id in the URL - and a refresh reads the URL first.
  it("rewrites a stale session param to the conversation actually open", () => {
    expect(nextSessionHref("https://x.io/live?session=OLD", "NEW")).toBe(
      "https://x.io/live?session=NEW",
    );
  });

  it("adds the param when the URL has none, so a refresh has an address", () => {
    expect(nextSessionHref("https://x.io/live", "NEW")).toBe("https://x.io/live?session=NEW");
  });

  it("does nothing when the URL already names the open conversation", () => {
    expect(nextSessionHref("https://x.io/live?session=SAME", "SAME")).toBeNull();
  });

  // A rewrite must never cost him the rest of the query: ?voice=1 is what makes
  // the voice flow auto-start, and dropping it would silently break that entry.
  it("preserves every other param", () => {
    expect(nextSessionHref("https://x.io/live?voice=1&session=OLD", "NEW")).toBe(
      "https://x.io/live?voice=1&session=NEW",
    );
  });

  it("drops the param when there is no session", () => {
    expect(nextSessionHref("https://x.io/live?session=OLD", "")).toBe("https://x.io/live");
  });

  // Only the chat page owns a session param. A screen that doesn't must never
  // grow one just because the hook happens to be mounted somewhere.
  it("leaves other pages alone", () => {
    expect(nextSessionHref("https://x.io/memory?session=OLD", "NEW")).toBeNull();
    expect(nextSessionHref("https://x.io/sessions", "NEW")).toBeNull();
  });

  it("survives a malformed href", () => {
    expect(nextSessionHref("not a url", "NEW")).toBeNull();
  });
});

describe("isRestoredSession", () => {
  it("treats a refresh as restored, so an idle chat can still rotate", () => {
    // The address bar now follows the open chat, so a refresh carries the same
    // id it stored. Reading that as a deep link would retire the rotation.
    expect(isRestoredSession("A", "A")).toBe(true);
  });

  it("treats a deep link to a different chat as an explicit request", () => {
    // He clicked "open the conversation that made this": give him that one,
    // however old it is.
    expect(isRestoredSession("OLD", "CURRENT")).toBe(false);
  });

  it("treats no param as restored", () => {
    expect(isRestoredSession("", "A")).toBe(true);
  });

  it("treats a deep link into a fresh browser as an explicit request", () => {
    expect(isRestoredSession("A", "")).toBe(false);
  });
});
