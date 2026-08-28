"use client";

/* TEMPORARY screenshot harness for the Majordomo phase-3 modal sweep.
 * Delete after verification. Not linked from any nav. */

import * as React from "react";
import { ObjectViewer } from "@/components/dashboard/ObjectViewer";
import type { DashboardItem } from "@/lib/dashboard/types";

const WORK: DashboardItem = {
  kind: "work",
  data: {
    id: "plan-fixture-1",
    kind: "plan",
    title: "Rewrite the inbox triage skill so it captures the real email",
    subtitle: "error: composio returned 401 for the gmail account",
    column: "running",
    summary:
      "What I did: pulled the last 40 threads and re-ran triage.\nHow it went: two accounts answered, one refused.\nOutcome: 6 follow-ups surfaced, 1 needs you.",
    engine: "Voyager",
    ref: "skill:inbox-triage@v7",
    startedAt: new Date(Date.now() - 1000 * 60 * 14).toISOString(),
    durationMs: 134000,
    scheduledFor: new Date(Date.now() - 1000 * 60 * 20).toISOString(),
    detailHref: "/cron",
    skills: ["inbox-triage", "surface-report"],
    instruction:
      "Every morning at 6am, read every inbox, decide what needs a reply, and draft it.",
    doneCount: 2,
    totalCount: 4,
    planSteps: [
      {
        id: "s1",
        idx: 1,
        title: "List the connected Gmail accounts",
        detail: "Only ACTIVE accounts, never the husk ids.",
        status: "done",
        isCheckpoint: false,
        verifyRequired: true,
        verifyResult: { verdict: "pass", evidence: "4 accounts returned, all ACTIVE." },
        endedAt: new Date(Date.now() - 1000 * 60 * 12).toISOString(),
      },
      {
        id: "s2",
        idx: 2,
        title: "Fetch the last 40 threads per account",
        status: "done",
        isCheckpoint: true,
        verifyRequired: false,
        resultSummary: "148 threads, 31 unread.",
        endedAt: new Date(Date.now() - 1000 * 60 * 8).toISOString(),
      },
      {
        id: "s3",
        idx: 3,
        title: "Decide which of them actually need a reply",
        status: "in_progress",
        isCheckpoint: false,
        verifyRequired: true,
      },
      {
        id: "s4",
        idx: 4,
        title: "Draft the replies and surface them",
        status: "pending",
        isCheckpoint: false,
        verifyRequired: false,
      },
    ],
  },
};

const FOLLOWUP: DashboardItem = {
  kind: "followup",
  data: {
    id: "fu-fixture-1",
    from: "Ariana Weber <ariana@northstarmedia.co>",
    subject: "Re: the Q3 retainer and the two extra deliverables",
    source: "gmail",
    account: "kai@dopesoft.io",
    receivedAt: new Date(Date.now() - 1000 * 60 * 21).toISOString(),
    origin: "followup",
    body:
      "Hi Kai,\n\nCircling back on the retainer. We are happy with the number, but we would want the two extra deliverables folded in rather than billed separately. Can you confirm before Friday?\n\nAriana",
    summary: "She wants the two extras inside the retainer, and needs an answer by Friday.",
    threadUrl: "https://mail.google.com/mail/u/0/#inbox/abc123",
    metadata: { classification: "client", intent: "needs reply", mode: "reply" },
    actions: [],
  } as unknown as DashboardItem["data"],
} as DashboardItem;

export default function MajordomoModalHarness() {
  const [item, setItem] = React.useState<DashboardItem | null>(null);
  return (
    <div className="min-h-app p-6">
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() => setItem(WORK)}
          className="h-11 rounded-[10px] border px-3 text-[13.5px]"
        >
          work
        </button>
        <button
          type="button"
          onClick={() => setItem(FOLLOWUP)}
          className="h-11 rounded-[10px] border px-3 text-[13.5px]"
        >
          followup
        </button>
      </div>
      <ObjectViewer item={item} onClose={() => setItem(null)} />
    </div>
  );
}
