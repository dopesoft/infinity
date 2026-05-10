import { TabFrame } from "@/components/TabFrame";
import { Card, CardContent, CardDescription, CardTitle } from "@/components/ui/card";
import { IconSettings } from "@tabler/icons-react";

export default function SettingsPage() {
  return (
    <TabFrame>
      <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col items-center justify-center p-4">
        <Card className="w-full">
          <CardContent className="flex flex-col items-center gap-3 p-8 text-center">
            <IconSettings className="size-8 text-muted-foreground" aria-hidden />
            <CardTitle>Settings</CardTitle>
            <CardDescription className="max-w-md">
              Phase 2 will land the tools registry view, LLM provider config, API keys and
              token-spend chart here.
            </CardDescription>
          </CardContent>
        </Card>
      </div>
    </TabFrame>
  );
}
