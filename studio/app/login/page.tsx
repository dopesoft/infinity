"use client";

import { useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { getSupabaseBrowserClient } from "@/lib/supabase/client";

type AuthStatus = {
  enabled: boolean;
  owner_set: boolean;
  accept_signup: boolean;
};

function coreBaseURL(): string {
  if (typeof window === "undefined") return "";
  const explicit = process.env.NEXT_PUBLIC_CORE_URL;
  return explicit ? explicit.replace(/\/$/, "") : "";
}

export default function LoginPage() {
  const router = useRouter();
  const search = useSearchParams();
  const from = search.get("from") || "/";

  const [tab, setTab] = useState<"signin" | "signup">("signin");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [authStatus, setAuthStatus] = useState<AuthStatus | null>(null);

  useEffect(() => {
    fetch(`${coreBaseURL()}/auth/status`)
      .then((r) => (r.ok ? r.json() : null))
      .then((data: AuthStatus | null) => {
        if (!data) return;
        setAuthStatus(data);
        // First-time setup: there's no owner yet, default to signup.
        if (data.enabled && !data.owner_set) setTab("signup");
        // Owner exists already: hide signup CTA, force sign-in.
        if (data.enabled && data.owner_set) setTab("signin");
      })
      .catch(() => {
        // Core unreachable — let user attempt either flow; Core will reject anyway.
      });
  }, []);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setInfo(null);
    setBusy(true);
    try {
      const supabase = getSupabaseBrowserClient();
      if (tab === "signin") {
        const { error: signInErr } = await supabase.auth.signInWithPassword({ email, password });
        if (signInErr) throw signInErr;
        router.replace(from);
        router.refresh();
      } else {
        const { data, error: signUpErr } = await supabase.auth.signUp({ email, password });
        if (signUpErr) throw signUpErr;
        if (!data.session) {
          // Email confirmation is enabled on the project — surface it instead
          // of silently failing the redirect.
          setInfo("Check your inbox to confirm — then come back and sign in.");
          return;
        }
        router.replace(from);
        router.refresh();
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const ownerLocked = authStatus?.enabled && authStatus.owner_set;

  return (
    <div className="flex min-h-app w-full items-center justify-center bg-background p-4 pt-safe pb-safe">
      <Card className="w-full max-w-sm">
        <CardHeader className="space-y-1.5">
          <CardTitle className="text-2xl tracking-tight">Infinity</CardTitle>
          <CardDescription>
            {ownerLocked
              ? "Sign in to continue."
              : authStatus?.enabled === false
                ? "Auth disabled — running in dev mode."
                : "First sign-up claims this instance."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Tabs value={tab} onValueChange={(v) => setTab(v as "signin" | "signup")}>
            <TabsList className="grid w-full grid-cols-2">
              <TabsTrigger value="signin">Sign in</TabsTrigger>
              <TabsTrigger value="signup" disabled={ownerLocked}>
                Sign up
              </TabsTrigger>
            </TabsList>

            <TabsContent value={tab} className="mt-4">
              <form onSubmit={handleSubmit} className="space-y-3">
                <div className="space-y-1.5">
                  <label htmlFor="email" className="text-xs font-medium text-muted-foreground">
                    Email
                  </label>
                  <Input
                    id="email"
                    type="email"
                    inputMode="email"
                    autoComplete="email"
                    required
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    placeholder="you@example.com"
                  />
                </div>
                <div className="space-y-1.5">
                  <label htmlFor="password" className="text-xs font-medium text-muted-foreground">
                    Password
                  </label>
                  <Input
                    id="password"
                    type="password"
                    autoComplete={tab === "signin" ? "current-password" : "new-password"}
                    required
                    minLength={6}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="••••••••"
                  />
                </div>
                {error && <p className="text-sm text-destructive">{error}</p>}
                {info && <p className="text-sm text-muted-foreground">{info}</p>}
                <Button type="submit" className="w-full" disabled={busy}>
                  {busy && <Loader2 className="size-4 animate-spin" />}
                  {tab === "signin" ? "Sign in" : "Create account"}
                </Button>
              </form>
              {tab === "signup" && !ownerLocked && (
                <p className="mt-3 text-xs text-muted-foreground">
                  This account becomes the owner. Existing memories, sessions, and settings carry
                  over.
                </p>
              )}
            </TabsContent>
          </Tabs>
        </CardContent>
      </Card>
    </div>
  );
}
