# Canvas — setup

Canvas is the Lovable-style IDE inside Studio. It needs three things wired up:

1. **Workspace root** — the directory on the Mac that Canvas browses.
2. **Mac MCP bridge** — already required for `claude_code__*` tools; Canvas
   reuses it for `ls`, `read`, and gated bash.
3. **Preview tunnel** — a Cloudflare Tunnel ingress that exposes your local
   dev server (typically `http://localhost:3000`) so the iframe inside
   Studio can load it.

If you already have the Claude Code bridge running (see
[../claude-code/SETUP.md](../claude-code/SETUP.md)) you can skip straight to
"Preview tunnel."

---

## 1. Workspace root

The Core process needs to know which directory on your Mac to browse. Two
ways to set it:

- **Env var on Core** (the source of truth):

  ```sh
  railway variables --service core --set INFINITY_CANVAS_ROOT=/Users/you/Dev
  ```

  Defaults to `$HOME` on the Mac. Canvas refuses to read or write outside
  this directory, no matter what path the studio sends.

- **Per-device override** in Settings → Canvas → Workspace root. Saved in
  localStorage; client-only. Server-side path enforcement still applies.

---

## 2. Mac MCP bridge

Canvas reuses the `claude_code` MCP server you already configured for the
agent. The only new requirement is that the bridge's `claude_code__Bash`
verb is reachable — Canvas's git endpoints call it.

Verify:

```sh
curl -s -H "CF-Access-Client-Id: $CF_ACCESS_CLIENT_ID" \
        -H "CF-Access-Client-Secret: $CF_ACCESS_CLIENT_SECRET" \
        https://coder.dopesoft.io/sse | head -2
```

If you see `event: endpoint`, the bridge is up.

---

## 3. Preview tunnel

You need a hostname that resolves from anywhere (Railway, your phone) back
to `http://localhost:3000` on your Mac. Recommended setup mirrors the
existing `coder.dopesoft.io` tunnel.

### Add a new ingress to your cloudflared config

If you're already running cloudflared with a `config.yml`, add an ingress
rule above the default 404:

```yml
ingress:
  - hostname: coder.dopesoft.io
    service: http://127.0.0.1:8765
  - hostname: preview.dopesoft.io     # ← new
    service: http://127.0.0.1:3000
  - service: http_status:404
```

### DNS

Either run `cloudflared tunnel route dns <tunnel-id> preview.dopesoft.io`
or add a CNAME in the Cloudflare dashboard pointing
`preview.dopesoft.io` → `<tunnel-id>.cfargotunnel.com`.

### Cloudflare Access (recommended)

Add an Access application protecting `preview.dopesoft.io` with the same
Service Token Studio already uses for the Claude Code bridge. Now Studio's
iframe can render the preview from any device, and nothing else can.

### Reload cloudflared

```sh
launchctl unload ~/Library/LaunchAgents/com.cloudflare.cloudflared.plist
launchctl load   ~/Library/LaunchAgents/com.cloudflare.cloudflared.plist
```

### Set Studio's env var

```sh
railway variables --service studio --set NEXT_PUBLIC_PREVIEW_URL=https://preview.dopesoft.io
```

Studio reads this on boot and the Canvas Preview tab will pick it up. The
boss can still override it per-device in Settings → Canvas.

### Helper script

If you'd rather not edit `config.yml` by hand, run:

```sh
bash docs/canvas/install-tunnel.sh --apply
```

The script patches your existing `cloudflared` config in place (idempotent),
reloads launchd, and verifies the hostname resolves. By default it runs in
**dry-run mode** — it prints the diff but doesn't write. Add `--apply` to
commit changes.

---

## Verification

1. Open Studio → Canvas.
2. The Preview tab should load your localhost:3000 inside the iframe.
3. Click a file in the Files tab → a new editor tab opens to the right of
   Preview, with Monaco showing the file.
4. Make an edit → press Cmd/Ctrl+S → Trust queue surfaces a contract → approve
   → status bar flips to "Saved" and the file lands on disk on your Mac.
5. Trigger any `claude_code__edit` from Live → the iframe auto-refreshes
   within ~600ms of the tool result.

---

## Security model

- Every file read goes through `claude_code__Read` (gate auto-allows reads).
- Every file write goes through `claude_code__Write` → Trust contract → boss approves.
- Every git mutation (`add`, `commit`, `push`, `pull`) goes through `claude_code__Bash`
  → Trust contract → boss approves.
- Read-only git verbs (`status`, `diff`, `log`, …) bypass Trust via the
  `INFINITY_CANVAS_GIT_READ_AUTOAPPROVE` allow-list in
  [`core/internal/proactive/gate.go`](../../core/internal/proactive/gate.go).
  Mutations always queue.
- Path inputs are sanitised against `INFINITY_CANVAS_ROOT` before they
  reach the Mac. Attempts to escape (e.g. `../../etc/passwd`) are rejected.

If you want to disable the git read-only allow-list (every git command
queues a Trust prompt), set:

```sh
railway variables --service core --set INFINITY_CANVAS_GIT_READ_AUTOAPPROVE=
```

---

## Environment reference

| Var | Where | Purpose |
|---|---|---|
| `INFINITY_CANVAS_ROOT` | Core | Workspace root the boss browses |
| `INFINITY_CANVAS_GIT_READ_AUTOAPPROVE` | Core | Comma list of git subcommands that bypass Trust (default `status,diff,log,show,branch,remote,fetch,rev-parse,ls-files,ls-tree,blame,config`) |
| `INFINITY_CANVAS_PREVIEW_URL` | Core | Optional preview URL hint surfaced through `/api/canvas/config`; the studio's `NEXT_PUBLIC_PREVIEW_URL` is the primary lever |
| `NEXT_PUBLIC_PREVIEW_URL` | Studio | The actual iframe `src` default |
| `INFINITY_CLAUDE_CODE_BLOCK` | Core | Existing — still controls which `claude_code__*` verbs gate |
