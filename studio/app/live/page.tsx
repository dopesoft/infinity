import { TabFrame } from "@/components/TabFrame";
import { Card, CardContent, CardDescription, CardTitle } from "@/components/ui/card";
import { IconMessage } from "@tabler/icons-react";

export default function LivePage() {
  return (
    <TabFrame>
      <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col items-center justify-center p-4">
        <Card className="w-full">
          <CardContent className="flex flex-col items-center gap-3 p-8 text-center">
            <IconMessage className="size-8 text-muted-foreground" aria-hidden />
            <CardTitle>Live</CardTitle>
            <CardDescription className="max-w-md">
              Phase 1 will land the streaming chat composer here. The agent loop, WebSocket
              transport, and conversation transcript come next.
            </CardDescription>
          </CardContent>
        </Card>
      </div>
    </TabFrame>
  );
}
