"use client";

import { Radar } from "lucide-react";
import { ResponsiveModal, ResponsiveModalHeader } from "@/components/ui/responsive-modal";
import { ModalSection, ModalCode, ModalDl } from "@/components/ui/modal-content";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { type SentinelDTO } from "@/lib/api";
import { watchTypeMeta, casualTime } from "./cronMeta";

/* The full sentinel, on click. A sentinel's watch_config and action_chain are
 * JSON that the list row can't show — this surfaces them, plus a plain-English
 * explanation of the watch type and the fire history. Same ResponsiveModal
 * primitive as crons so desktop/mobile behaviour matches. */
export function SentinelDetailModal({
  sentinel,
  open,
  onOpenChange,
}: {
  sentinel: SentinelDTO | null;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  if (!sentinel) return null;
  const meta = watchTypeMeta(sentinel.watch_type);

  return (
    <ResponsiveModal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={sentinel.name}
      description={`${meta.label} sentinel`}
      header={
        <ResponsiveModalHeader
          icon={<Radar className="size-4" />}
          eyebrow={`${meta.label} sentinel`}
          title={sentinel.name}
          subtitle={`fired ${sentinel.fire_count}× · cooldown ${sentinel.cooldown_seconds}s`}
          trailing={
            sentinel.enabled ? (
              <Badge variant="success">enabled</Badge>
            ) : (
              <Badge variant="secondary">disabled</Badge>
            )
          }
        />
      }
      footer={
        <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
          Close
        </Button>
      }
    >
      <ModalSection label="What it watches">
        <p className="text-[13px] text-foreground/90">{meta.blurb}</p>
      </ModalSection>

      <ModalSection label="Watch config">
        <ModalCode>{safeJson(sentinel.watch_config)}</ModalCode>
      </ModalSection>

      <ModalSection label="Action chain">
        <ModalCode>{safeJson(sentinel.action_chain)}</ModalCode>
      </ModalSection>

      <ModalSection label="Details">
        <ModalDl
          entries={[
            { k: "watch", v: sentinel.watch_type },
            { k: "cooldown", v: `${sentinel.cooldown_seconds}s` },
            { k: "fired", v: `${sentinel.fire_count}×` },
            {
              k: "last fired",
              v: sentinel.last_triggered_at ? (
                <span suppressHydrationWarning>{casualTime(sentinel.last_triggered_at)}</span>
              ) : (
                "never"
              ),
            },
            {
              k: "created",
              v: <span suppressHydrationWarning>{casualTime(sentinel.created_at)}</span>,
            },
          ]}
        />
      </ModalSection>
    </ResponsiveModal>
  );
}

function safeJson(v: unknown): string {
  try {
    const s = JSON.stringify(v ?? {}, null, 2);
    return s === "{}" || s === "[]" ? `${s}  (empty)` : s;
  } catch {
    return String(v);
  }
}
