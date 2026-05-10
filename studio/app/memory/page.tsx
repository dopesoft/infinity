import { TabFrame } from "@/components/TabFrame";
import { Card, CardContent, CardDescription, CardTitle } from "@/components/ui/card";
import { IconBrain } from "@tabler/icons-react";

export default function MemoryPage() {
  return (
    <TabFrame>
      <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col items-center justify-center p-4">
        <Card className="w-full">
          <CardContent className="flex flex-col items-center gap-3 p-8 text-center">
            <IconBrain className="size-8 text-muted-foreground" aria-hidden />
            <CardTitle>Memory</CardTitle>
            <CardDescription className="max-w-md">
              The brain browser. Phase 3 will land the metric strip, triple-stream search, and
              memory master/detail with full provenance chain.
            </CardDescription>
          </CardContent>
        </Card>
      </div>
    </TabFrame>
  );
}
