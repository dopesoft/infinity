"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertCircle,
  ExternalLink,
  Plus,
  RefreshCcw,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ResponsiveModal } from "@/components/ui/responsive-modal";
import { GroupLabel, ListRow } from "@/components/ui/list-row";
import { EmptyState } from "@/components/EmptyState";
import { ModalSection, ModalUrl, ModalField } from "@/components/ui/modal-content";
import { RunIndicator } from "@/lib/runs";
import { cn } from "@/lib/utils";
import {
  checkExtension,
  fetchExtensions,
  registerExtension,
  removeExtension,
  type Extension,
  type ExtensionKind,
} from "@/lib/api";

// CustomExtensions is the manual half of "bring your own tool" - paste an
// MCP endpoint, a REST endpoint, or a CLI install recipe and Jarvis can use
// it + build workflows on top. The autonomous half (Jarvis self-registering)
// goes through the extension_* tools; both land in the same mem_extensions
// store, so anything the agent wires shows up here too.
export function CustomExtensions() {
  const [items, setItems] = useState<Extension[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);

  const load = useCallback(async () => {
    const r = await fetchExtensions();
    if ("error" in r) {
      setError(r.error);
    } else {
      setItems(r);
      setError(null);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <div className="min-w-0 space-y-3">
      <GroupLabel
        label="Your own tools"
        count={items.length || undefined}
        trailing={
          <Button size="sm" onClick={() => setAdding(true)}>
            <Plus className="size-4" /> Add
          </Button>
        }
      />
      <p className="text-[12px] leading-relaxed text-quiet">
        MCP servers and REST endpoints connect instantly; CLI tools install into the always-on cloud
        workspace (so crons can use them even when your Mac is offline) and walk you through any
        sign-in. Jarvis can register these himself too.
      </p>

      {error && (
        <div className="flex min-w-0 items-start gap-2 rounded-[10px] bg-danger/10 px-3 py-2.5">
          <AlertCircle className="mt-0.5 size-4 shrink-0 text-danger" aria-hidden />
          <p className="min-w-0 break-words text-[12.5px] text-danger">{error}</p>
        </div>
      )}

      {loading ? (
        <p className="py-2 text-[13.5px] text-quiet">Loading…</p>
      ) : items.length === 0 ? (
        <EmptyState
          icon={Plus}
          align="top"
          className="pt-8"
          title="No custom tools yet"
          description="Add an MCP server, a REST endpoint, or a command-line tool. Then ask Jarvis to use it or build a cron around it."
        />
      ) : (
        <div className="min-w-0">
          {items.map((e) => (
            <ExtensionRow key={e.id || e.name} ext={e} onChanged={load} />
          ))}
        </div>
      )}

      {adding && (
        <AddExtensionModal
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            // cli installs are async - reload now and again shortly so the
            // new row appears once the background install books its row.
            void load();
            setTimeout(() => void load(), 2500);
          }}
        />
      )}
    </div>
  );
}

function ExtensionRow({ ext, onChanged }: { ext: Extension; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);

  async function onCheck() {
    setBusy(true);
    await checkExtension(ext.name);
    setBusy(false);
    onChanged();
  }
  async function onRemove() {
    // eslint-disable-next-line no-alert
    if (!confirm(`Remove "${ext.name}"? Workflows that depend on it will stop working.`)) return;
    setBusy(true);
    await removeExtension(ext.name);
    setBusy(false);
    onChanged();
  }

  const pending = ext.status === "pending_auth";
  const errored = ext.status === "error";
  const meta = [
    ext.kind,
    ext.status === "pending_auth" ? "needs sign-in" : ext.status,
    ext.source === "agent" ? "registered by Jarvis" : "",
    ext.tool_name || "",
    ext.description || "",
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <ListRow
      tone={ext.status === "active" ? "success" : errored ? "danger" : pending ? "warning" : "quiet"}
      title={ext.name}
      meta={meta}
      chevron={false}
      trailing={
        <Button
          size="icon"
          variant="ghost"
          className="size-9 text-quiet hover:bg-danger/10 hover:text-danger"
          onClick={onRemove}
          disabled={busy}
          aria-label={`Remove ${ext.name}`}
          title={`Remove ${ext.name}`}
        >
          <Trash2 className="size-4" aria-hidden />
        </Button>
      }
    >
      {pending && (
        // The sign-in hand-off: what to do, where to go, and the button that
        // resumes once it's done (project memory: auth is a pause/resume,
        // never a dead end).
        <div className="min-w-0 space-y-2">
          <p className="text-[12.5px] leading-relaxed text-foreground/85">
            {ext.auth_instructions || "Sign-in needed to finish setup."}
          </p>
          {ext.auth_url && (
            <ModalUrl href={ext.auth_url} icon={<ExternalLink className="size-3.5" />}>
              {ext.auth_url}
            </ModalUrl>
          )}
          <Button size="sm" variant="secondary" disabled={busy} onClick={onCheck}>
            <RefreshCcw className={cn("size-3.5", busy && "animate-spin")} /> I&apos;ve signed in
          </Button>
        </div>
      )}

      {errored && ext.last_error && (
        <p className="min-w-0 whitespace-pre-wrap text-[12px] text-danger [overflow-wrap:anywhere]">
          {ext.last_error}
        </p>
      )}

      {/* cli install progress survives navigation via mem_runs. */}
      {ext.kind === "cli" && (
        <RunIndicator kind="extension.register" targetId={ext.name} mode="inline" />
      )}
    </ListRow>
  );
}

// ── Add form ────────────────────────────────────────────────────────────────

const KINDS: { value: ExtensionKind; label: string; hint: string }[] = [
  { value: "cli", label: "CLI tool", hint: "A command-line tool installed in the cloud workspace" },
  { value: "mcp", label: "MCP server", hint: "A remote MCP server (URL + auth)" },
  { value: "http_tool", label: "REST endpoint", hint: "A single REST call exposed as a tool" },
];

function AddExtensionModal({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [kind, setKind] = useState<ExtensionKind>("cli");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  // mcp
  const [mcpUrl, setMcpUrl] = useState("");
  const [mcpTransport, setMcpTransport] = useState("http");
  const [mcpAuth, setMcpAuth] = useState("none");
  const [mcpTokenEnv, setMcpTokenEnv] = useState("");
  const [mcpHeaderName, setMcpHeaderName] = useState("");

  // http_tool
  const [httpMethod, setHttpMethod] = useState("GET");
  const [httpUrl, setHttpUrl] = useState("");
  const [httpHeaders, setHttpHeaders] = useState("");
  const [httpBody, setHttpBody] = useState("");

  // cli
  const [cliBinary, setCliBinary] = useState("");
  const [cliInstall, setCliInstall] = useState("");
  const [cliCheck, setCliCheck] = useState("");
  const [cliAuth, setCliAuth] = useState("");
  const [cliUsage, setCliUsage] = useState("");
  const [cliAuthEnvs, setCliAuthEnvs] = useState("");
  const [cliResume, setCliResume] = useState("");

  const buildConfig = useCallback((): { config: Record<string, unknown>; err?: string } => {
    if (kind === "mcp") {
      if (!mcpUrl.trim()) return { config: {}, err: "MCP server URL is required." };
      const c: Record<string, unknown> = { url: mcpUrl.trim(), transport: mcpTransport };
      if (mcpAuth !== "none") {
        c.auth = mcpAuth;
        if (mcpTokenEnv.trim()) c.auth_token_env = mcpTokenEnv.trim();
        if (mcpAuth === "header" && mcpHeaderName.trim()) c.auth_header_name = mcpHeaderName.trim();
      }
      return { config: c };
    }
    if (kind === "http_tool") {
      if (!httpUrl.trim()) return { config: {}, err: "Endpoint URL is required." };
      const c: Record<string, unknown> = { method: httpMethod, url: httpUrl.trim() };
      if (httpHeaders.trim()) {
        try {
          c.headers = JSON.parse(httpHeaders);
        } catch {
          return { config: {}, err: "Headers must be valid JSON (e.g. {\"Authorization\":\"Bearer …\"})." };
        }
      }
      if (httpBody.trim()) c.body_template = httpBody;
      return { config: c };
    }
    // cli
    if (!cliBinary.trim()) return { config: {}, err: "Binary name is required (the command to run)." };
    const c: Record<string, unknown> = { binary: cliBinary.trim() };
    if (cliInstall.trim()) c.install = cliInstall.trim();
    if (cliCheck.trim()) c.check_cmd = cliCheck.trim();
    if (cliAuth.trim()) c.auth_cmd = cliAuth.trim();
    if (cliUsage.trim()) c.usage = cliUsage.trim();
    if (cliAuthEnvs.trim())
      c.auth_envs = cliAuthEnvs.split(",").map((s) => s.trim()).filter(Boolean);
    return { config: c };
  }, [
    kind, mcpUrl, mcpTransport, mcpAuth, mcpTokenEnv, mcpHeaderName,
    httpMethod, httpUrl, httpHeaders, httpBody,
    cliBinary, cliInstall, cliCheck, cliAuth, cliUsage, cliAuthEnvs,
  ]);

  async function submit() {
    setFormError(null);
    const clean = name.trim();
    if (!clean) {
      setFormError("Give it a short kebab-case name.");
      return;
    }
    if (!/^[a-z0-9][a-z0-9-]*$/.test(clean)) {
      setFormError("Name must be lowercase letters, numbers, and hyphens.");
      return;
    }
    const { config, err } = buildConfig();
    if (err) {
      setFormError(err);
      return;
    }
    setSubmitting(true);
    const r = await registerExtension({
      name: clean,
      kind,
      description: description.trim(),
      config,
      resume_intent: kind === "cli" ? cliResume.trim() : undefined,
    });
    setSubmitting(false);
    if (!r.ok) {
      setFormError(r.error || "Couldn't register.");
      return;
    }
    onAdded();
  }

  const selectedKind = useMemo(() => KINDS.find((k) => k.value === kind)!, [kind]);

  const footer = (
    <div className="flex justify-end gap-2">
      <Button variant="ghost" size="sm" onClick={onClose} disabled={submitting}>
        Cancel
      </Button>
      <Button size="sm" onClick={submit} disabled={submitting}>
        {submitting ? "Adding…" : kind === "cli" ? "Install" : "Connect"}
      </Button>
    </div>
  );

  return (
    <ResponsiveModal
      open
      onOpenChange={(o) => !o && onClose()}
      title="Add a custom tool"
      description="MCP server, REST endpoint, or CLI tool"
      size="md"
      footer={footer}
    >
      <div className="space-y-4">
        {/* Kind picker */}
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
          {KINDS.map((k) => (
            <button
              key={k.value}
              type="button"
              onClick={() => setKind(k.value)}
              className={cn(
                "rounded-lg border p-2.5 text-left transition-colors",
                kind === k.value ? "border-info bg-info/10" : "hover:bg-accent/40",
              )}
            >
              <div className="text-xs font-semibold">{k.label}</div>
              <div className="mt-0.5 text-[10px] leading-snug text-muted-foreground">{k.hint}</div>
            </button>
          ))}
        </div>

        <ModalSection label={selectedKind.label}>
          <div className="divide-y divide-border">
            <ModalField label="Name">
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={kind === "cli" ? "higgsfield" : "linear-mcp"}
                inputMode="text"
                autoCapitalize="none"
                spellCheck={false}
              />
            </ModalField>
            <ModalField label="Description">
              <Input
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="What it does + when to use it"
                inputMode="text"
              />
            </ModalField>

            {kind === "mcp" && (
              <>
                <ModalField label="Server URL">
                  <Input value={mcpUrl} onChange={(e) => setMcpUrl(e.target.value)} placeholder="https://mcp.example.com/sse" inputMode="url" autoCapitalize="none" spellCheck={false} />
                </ModalField>
                <ModalField label="Transport">
                  <Picker value={mcpTransport} onChange={setMcpTransport} options={["http", "sse"]} />
                </ModalField>
                <ModalField label="Auth">
                  <Picker value={mcpAuth} onChange={setMcpAuth} options={["none", "bearer", "header"]} />
                </ModalField>
                {mcpAuth !== "none" && (
                  <ModalField label="Token env var">
                    <Input value={mcpTokenEnv} onChange={(e) => setMcpTokenEnv(e.target.value)} placeholder="MY_API_TOKEN (set on Railway - name only)" inputMode="text" autoCapitalize="none" spellCheck={false} />
                  </ModalField>
                )}
                {mcpAuth === "header" && (
                  <ModalField label="Header name">
                    <Input value={mcpHeaderName} onChange={(e) => setMcpHeaderName(e.target.value)} placeholder="X-Api-Key" inputMode="text" autoCapitalize="none" spellCheck={false} />
                  </ModalField>
                )}
              </>
            )}

            {kind === "http_tool" && (
              <>
                <ModalField label="Method">
                  <Picker value={httpMethod} onChange={setHttpMethod} options={["GET", "POST", "PUT", "PATCH", "DELETE"]} />
                </ModalField>
                <ModalField label="URL">
                  <Input value={httpUrl} onChange={(e) => setHttpUrl(e.target.value)} placeholder="https://api.example.com/v1/{{id}}" inputMode="url" autoCapitalize="none" spellCheck={false} />
                </ModalField>
                <ModalField label="Headers (JSON)">
                  <Textarea value={httpHeaders} onChange={(e) => setHttpHeaders(e.target.value)} placeholder={'{"Authorization": "Bearer {{token}}"}'} rows={2} />
                </ModalField>
                <ModalField label="Body template">
                  <Textarea value={httpBody} onChange={(e) => setHttpBody(e.target.value)} placeholder='{"q": "{{query}}"}' rows={2} />
                </ModalField>
              </>
            )}

            {kind === "cli" && (
              <>
                <ModalField label="Binary">
                  <Input value={cliBinary} onChange={(e) => setCliBinary(e.target.value)} placeholder="higgsfield" inputMode="text" autoCapitalize="none" spellCheck={false} />
                </ModalField>
                <ModalField label="Install">
                  <Textarea value={cliInstall} onChange={(e) => setCliInstall(e.target.value)} placeholder="curl -fsSL https://…/install.sh | sh" rows={2} />
                </ModalField>
                <ModalField label="Check command">
                  <Input value={cliCheck} onChange={(e) => setCliCheck(e.target.value)} placeholder="higgsfield account status" inputMode="text" autoCapitalize="none" spellCheck={false} />
                </ModalField>
                <ModalField label="Auth command">
                  <Input value={cliAuth} onChange={(e) => setCliAuth(e.target.value)} placeholder="higgsfield auth login" inputMode="text" autoCapitalize="none" spellCheck={false} />
                </ModalField>
                <ModalField label="Usage">
                  <Textarea value={cliUsage} onChange={(e) => setCliUsage(e.target.value)} placeholder="higgsfield generate create <model> --prompt … --wait" rows={2} />
                </ModalField>
                <ModalField label="Auth env vars">
                  <Input value={cliAuthEnvs} onChange={(e) => setCliAuthEnvs(e.target.value)} placeholder="API_KEY, OTHER_TOKEN (comma-separated names)" inputMode="text" autoCapitalize="none" spellCheck={false} />
                </ModalField>
                <ModalField label="Resume intent">
                  <Input value={cliResume} onChange={(e) => setCliResume(e.target.value)} placeholder="optional: what to do once authenticated" inputMode="text" />
                </ModalField>
              </>
            )}
          </div>
        </ModalSection>

        {formError && <p className="text-[11px] text-danger">{formError}</p>}
      </div>
    </ResponsiveModal>
  );
}

// Picker is a tiny segmented control - cleaner than a Select for the short
// fixed option sets (transport/method/auth) and keeps touch targets large.
function Picker({
  value,
  onChange,
  options,
}: {
  value: string;
  onChange: (v: string) => void;
  options: string[];
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((o) => (
        <button
          key={o}
          type="button"
          onClick={() => onChange(o)}
          className={cn(
            "h-9 rounded-md border px-2.5 font-mono text-[11px] uppercase transition-colors",
            value === o ? "border-info bg-info/10 text-info" : "hover:bg-accent/40",
          )}
        >
          {o}
        </button>
      ))}
    </div>
  );
}
