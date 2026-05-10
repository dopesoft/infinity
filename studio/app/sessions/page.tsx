import { TabFrame } from "@/components/TabFrame";
import { Card, CardContent, CardDescription, CardTitle } from "@/components/ui/card";
import { IconHistory } from "@tabler/icons-react";

export default function SessionsPage() {
  return (
    <TabFrame>
      <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col items-center justify-center p-4">
        <Card className="w-full">
          <CardContent className="flex flex-col items-center gap-3 p-8 text-center">
            <IconHistory className="size-8 text-muted-foreground" aria-hidden />
            <CardTitle>Sessions</CardTitle>
            <CardDescription className="max-w-md">
              The historical record. Phase 3 will land the master/detail browser and immutable
              session snapshots here.
            </CardDescription>
          </CardContent>
        </Card>
      </div>
    </TabFrame>
  );
}
